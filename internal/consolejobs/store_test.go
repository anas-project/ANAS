package consolejobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCreateOrGetIdempotency(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	spec := testCreateSpec("same-key", "workspace-a", true)

	first, err := store.CreateOrGet(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if first.Existing {
		t.Fatal("first CreateOrGet reported an existing job")
	}
	second, err := store.CreateOrGet(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Existing || second.Job.ID != first.Job.ID {
		t.Fatalf("same request result = %+v, want existing job %s", second, first.Job.ID)
	}

	conflicting := spec
	conflicting.Idempotency.RequestDigest = DigestRequest([]byte("different canonical request"))
	_, err = store.CreateOrGet(context.Background(), conflicting)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different digest error = %v, want ErrIdempotencyConflict", err)
	}
	var conflict *IdempotencyConflictError
	if !errors.As(err, &conflict) || conflict.ExistingJobID != first.Job.ID {
		t.Fatalf("different digest error = %#v, want existing job %s", err, first.Job.ID)
	}
}

func TestStartEnforcesWorkspaceSerializationAndAllowsReadOnlyConcurrency(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	ctx := context.Background()

	firstMutation := createJobForTest(t, store, testCreateSpec("mutation-1", "workspace-a", true))
	secondMutation := createJobForTest(t, store, testCreateSpec("mutation-2", "workspace-a", true))
	firstRead := createJobForTest(t, store, testCreateSpec("read-1", "workspace-a", false))
	secondRead := createJobForTest(t, store, testCreateSpec("read-2", "workspace-a", false))

	if _, err := store.Start(ctx, firstMutation.ID); err != nil {
		t.Fatal(err)
	}
	_, err := store.Start(ctx, secondMutation.ID)
	if !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("second workspace mutation error = %v, want ErrWorkspaceBusy", err)
	}
	var busy *WorkspaceBusyError
	if !errors.As(err, &busy) || busy.RunningJobID != firstMutation.ID || busy.WorkspaceID != "workspace-a" {
		t.Fatalf("workspace busy details = %#v", err)
	}

	for _, job := range []Job{firstRead, secondRead} {
		started, err := store.Start(ctx, job.ID)
		if err != nil {
			t.Fatalf("start read-only job %s: %v", job.ID, err)
		}
		if started.Status != StatusRunning {
			t.Fatalf("read-only job status = %s, want running", started.Status)
		}
	}
}

func TestStartEnforcesGlobalRunningCapacity(t *testing.T) {
	store, _ := openStoreForTest(t, Options{MaxRunningJobs: 2})
	ctx := context.Background()
	jobs := []Job{
		createJobForTest(t, store, testCreateSpec("cap-1", "workspace-a", false)),
		createJobForTest(t, store, testCreateSpec("cap-2", "workspace-b", false)),
		createJobForTest(t, store, testCreateSpec("cap-3", "workspace-c", false)),
	}
	for _, job := range jobs[:2] {
		if _, err := store.Start(ctx, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, err := store.Start(ctx, jobs[2].ID)
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("third Start error = %v, want ErrCapacity", err)
	}
	var capacity *CapacityError
	if !errors.As(err, &capacity) || capacity.Resource != "running jobs" || capacity.Limit != 2 || capacity.Current != 2 {
		t.Fatalf("capacity details = %#v", err)
	}
}

func TestJobStateTransitions(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	ctx := context.Background()
	job := createJobForTest(t, store, testCreateSpec("transition-success", "workspace-a", true))

	running, err := store.Start(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != StatusRunning || running.StartedAt == nil || running.Revision != 2 {
		t.Fatalf("running job = %+v", running)
	}
	progress := 55
	updated, err := store.UpdateRunning(ctx, job.ID, ProgressUpdate{
		Progress: &progress,
		Warnings: []string{"authorization: should-not-survive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Progress != 55 || updated.Revision != 3 || strings.Contains(strings.Join(updated.Warnings, " "), "should-not-survive") {
		t.Fatalf("progress update = %+v", updated)
	}

	complete := 100
	finished, err := store.Transition(ctx, job.ID, StatusSucceeded, TransitionInput{
		Progress: &complete,
		Result:   map[string]any{"changed": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != StatusSucceeded || finished.Progress != 100 || finished.FinishedAt == nil || finished.Revision != 4 {
		t.Fatalf("finished job = %+v", finished)
	}
	if _, err := store.Start(ctx, job.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restart terminal job error = %v, want ErrConflict", err)
	}
	if _, err := store.UpdateRunning(ctx, job.ID, ProgressUpdate{Progress: &progress}); !errors.Is(err, ErrConflict) {
		t.Fatalf("update terminal job error = %v, want ErrConflict", err)
	}

	failedJob := createJobForTest(t, store, testCreateSpec("transition-failed", "workspace-b", true))
	if _, err := store.Start(ctx, failedJob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(ctx, failedJob.ID, StatusFailed, TransitionInput{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("failed transition without error = %v, want ErrInvalid", err)
	}
	failed, err := store.Transition(ctx, failedJob.ID, StatusFailed, TransitionInput{
		Error: &JobError{Code: "command_failed", Message: "token=should-not-survive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed || failed.Error == nil || failed.Error.Code != "command_failed" || strings.Contains(failed.Error.Message, "should-not-survive") {
		t.Fatalf("failed job = %+v", failed)
	}
}

func TestRestartInterruptsRunningJobsAndRequiresCompensation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	running := createJobForTest(t, store, testCreateSpec("restart-running", "workspace-a", true))
	if _, err := store.Start(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	stillRunning, err := reopened.Get(ctx, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillRunning.Status != StatusRunning {
		t.Fatalf("opening a reader changed running job to %s", stillRunning.Status)
	}
	lease, err := AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := reopened.RecoverInterruptedJobs(ctx, lease); err != nil {
		t.Fatal(err)
	}
	interrupted, err := reopened.Get(ctx, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != StatusInterrupted || interrupted.FinishedAt == nil || !interrupted.NeedsCompensationCheck {
		t.Fatalf("recovered running job = %+v", interrupted)
	}
	if interrupted.Error == nil || interrupted.Error.Code != "daemon_restarted" {
		t.Fatalf("recovered job error = %+v", interrupted.Error)
	}

	next := createJobForTest(t, reopened, testCreateSpec("after-restart", "workspace-a", true))
	_, err = reopened.Start(ctx, next.ID)
	if !errors.Is(err, ErrCompensationRequired) {
		t.Fatalf("Start before compensation check error = %v, want ErrCompensationRequired", err)
	}
	var compensation *CompensationRequiredError
	if !errors.As(err, &compensation) || !reflect.DeepEqual(compensation.JobIDs, []string{running.ID}) {
		t.Fatalf("compensation error details = %#v", err)
	}
	acknowledged, err := reopened.AcknowledgeCompensation(ctx, running.ID, "operator verified workspace")
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.NeedsCompensationCheck {
		t.Fatal("compensation acknowledgement left the gate enabled")
	}
	started, err := reopened.Start(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != StatusRunning {
		t.Fatalf("job status after acknowledgement = %s, want running", started.Status)
	}
}

func TestEventsUseGlobalIDsAndCapacityProducesTypedGap(t *testing.T) {
	store, _ := openStoreForTest(t, Options{EventCapacity: 2})
	ctx := context.Background()
	firstJob := createJobForTest(t, store, testCreateSpec("events-a", "workspace-a", false))
	secondJob := createJobForTest(t, store, testCreateSpec("events-b", "workspace-b", false))

	first := appendEventForTest(t, store, firstJob.ID, "progress")
	second := appendEventForTest(t, store, secondJob.ID, "progress")
	third := appendEventForTest(t, store, firstJob.ID, "progress")
	fourth := appendEventForTest(t, store, firstJob.ID, "progress")
	if got := []uint64{first.ID, second.ID, third.ID, fourth.ID}; !reflect.DeepEqual(got, []uint64{1, 2, 3, 4}) {
		t.Fatalf("global event IDs = %v", got)
	}

	page, err := store.Replay(ctx, firstJob.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(page.Events); !reflect.DeepEqual(got, []uint64{3, 4}) {
		t.Fatalf("retained first-job event IDs = %v, want [3 4]", got)
	}
	if page.PrunedThrough != 1 || page.LatestID != 4 {
		t.Fatalf("first-job page watermarks = %+v", page)
	}

	zero := uint64(0)
	_, err = store.Replay(ctx, firstJob.ID, ReplayOptions{AfterID: &zero})
	if !errors.Is(err, ErrEventGap) {
		t.Fatalf("stale capacity cursor error = %v, want ErrEventGap", err)
	}
	var gap *EventGapError
	if !errors.As(err, &gap) || gap.PrunedThrough != 1 || gap.OldestAvailable != 3 || gap.LatestID != 4 {
		t.Fatalf("capacity gap details = %#v", err)
	}

	secondPage, err := store.Replay(ctx, secondJob.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(secondPage.Events); !reflect.DeepEqual(got, []uint64{2}) {
		t.Fatalf("second-job event IDs = %v, want [2]", got)
	}
}

func TestRepeatedReadsUseCachedStateAndIncrementalJournalTail(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	writer, err := Open(directory, Options{EventCapacity: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := Open(directory, Options{EventCapacity: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	observed := &observedJournal{journalFile: reader.file}
	reader.file = observed
	created := createJobForTest(t, writer, testCreateSpec("incremental-reader", "workspace-a", false))
	if _, err := reader.Get(context.Background(), created.ID); err != nil {
		t.Fatalf("incrementally refresh created job: %v", err)
	}
	if observed.readBytes == 0 {
		t.Fatal("cross-store update did not read the appended journal tail")
	}
	if observed.fullScans != 0 {
		t.Fatalf("cross-store update performed %d full scans, want incremental tail read", observed.fullScans)
	}

	event := appendEventForTest(t, writer, created.ID, "queued")
	page, err := reader.Replay(context.Background(), created.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(page.Events); !reflect.DeepEqual(got, []uint64{event.ID}) {
		t.Fatalf("incrementally refreshed event IDs = %v, want [%d]", got, event.ID)
	}
	readsAfterRefresh := observed.readBytes
	for index := 0; index < 5; index++ {
		if _, err := reader.Get(context.Background(), created.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.Replay(context.Background(), created.ID, ReplayOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if observed.readBytes != readsAfterRefresh {
		t.Fatalf("unchanged Get/Replay read %d more journal bytes", observed.readBytes-readsAfterRefresh)
	}
	if observed.fullScans != 0 {
		t.Fatalf("unchanged Get/Replay performed %d full scans", observed.fullScans)
	}
}

func TestFailedAppendDoesNotPublishIncrementalReceipt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	writer, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	observed := &observedJournal{journalFile: reader.file}
	reader.file = observed
	syncFailure := errors.New("injected append sync failure")
	writer.file = &failNthSyncJournal{journalFile: writer.file, failAt: 2, err: syncFailure}
	spec := testCreateSpec("uncertain-append-receipt", "workspace-a", false)
	if _, err := writer.Create(context.Background(), spec); !errors.Is(err, ErrUnavailable) || !errors.Is(err, syncFailure) {
		t.Fatalf("Create with failed append sync error = %v", err)
	}

	recovered, err := reader.CreateOrGet(context.Background(), spec)
	if err != nil {
		t.Fatalf("recover uncertain append from second Store: %v", err)
	}
	if !recovered.Existing {
		t.Fatal("second Store did not recover the uncertain committed append")
	}
	if observed.fullScans == 0 {
		t.Fatal("failed append published a receipt and bypassed full recovery")
	}
}

func TestOversizedAggregatedJobRecordIsInvalidWithoutPoisoningStore(t *testing.T) {
	store, directory := openStoreForTest(t, Options{})
	ctx := context.Background()
	job := createJobForTest(t, store, testCreateSpec("oversized-record", "workspace-a", false))
	if _, err := store.Start(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	warnings := make([]string, 256)
	for index := range warnings {
		warnings[index] = strings.Repeat("w", 4096)
	}
	first, err := store.UpdateRunning(ctx, job.ID, ProgressUpdate{Warnings: warnings})
	if err != nil {
		t.Fatalf("first warning batch: %v", err)
	}
	journalPath := filepath.Join(directory, JournalFilename)
	before, err := os.Stat(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedJournal{journalFile: store.file}
	store.file = observed

	_, err = store.UpdateRunning(ctx, job.ID, ProgressUpdate{Warnings: warnings})
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized aggregate error = %v, want ErrInvalid without ErrUnavailable", err)
	}
	if observed.writeCalls != 0 {
		t.Fatalf("oversized aggregate performed %d journal writes", observed.writeCalls)
	}
	after, err := os.Stat(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("journal size after rejected aggregate = %d, want %d", after.Size(), before.Size())
	}

	unchanged, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get after oversized aggregate: %v", err)
	}
	if unchanged.Revision != first.Revision || len(unchanged.Warnings) != len(first.Warnings) {
		t.Fatalf("job changed after oversized aggregate: revision=%d warnings=%d", unchanged.Revision, len(unchanged.Warnings))
	}
	progress := 75
	if _, err := store.UpdateRunning(ctx, job.ID, ProgressUpdate{Progress: &progress}); err != nil {
		t.Fatalf("healthy operation after oversized aggregate: %v", err)
	}
}

func TestJournalAppendSizeBoundaries(t *testing.T) {
	for _, test := range []struct {
		name             string
		transactionBytes int
		recordBytes      int
		wantInvalid      bool
	}{
		{name: "maximum record", recordBytes: maximumJournalRecordBytes},
		{name: "record over maximum", recordBytes: maximumJournalRecordBytes + 1, wantInvalid: true},
		{name: "maximum transaction", transactionBytes: maximumJournalTransactionBytes - maximumJournalRecordBytes, recordBytes: maximumJournalRecordBytes},
		{name: "transaction over maximum", transactionBytes: maximumJournalTransactionBytes - maximumJournalRecordBytes + 1, recordBytes: maximumJournalRecordBytes, wantInvalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateJournalAppendSize(test.transactionBytes, test.recordBytes)
			if errors.Is(err, ErrInvalid) != test.wantInvalid {
				t.Fatalf("validateJournalAppendSize error = %v, want invalid=%t", err, test.wantInvalid)
			}
		})
	}
}

func TestAppendEventRejectsTerminalJobs(t *testing.T) {
	store, _ := openStoreForTest(t, Options{})
	ctx := context.Background()
	job := createJobForTest(t, store, testCreateSpec("terminal-events", "workspace-a", false))
	queued := appendEventForTest(t, store, job.ID, "queued")
	if _, err := store.Start(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	running := appendEventForTest(t, store, job.ID, "running")
	if _, err := store.Transition(ctx, job.ID, StatusSucceeded, TransitionInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, job.ID, EventInput{Kind: "too-late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal AppendEvent error = %v, want ErrConflict", err)
	}
	page, err := store.Replay(ctx, job.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(page.Events); !reflect.DeepEqual(got, []uint64{queued.ID, running.ID}) {
		t.Fatalf("events after rejected terminal append = %v", got)
	}
}

func TestEventRetentionProducesTypedGap(t *testing.T) {
	store, _ := openStoreForTest(t, Options{EventCapacity: 100, EventRetention: time.Hour})
	ctx := context.Background()
	base := time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }
	job := createJobForTest(t, store, testCreateSpec("retention", "workspace-a", false))
	event := appendEventForTest(t, store, job.ID, "progress")
	store.now = func() time.Time { return base.Add(2 * time.Hour) }

	zero := uint64(0)
	_, err := store.Replay(ctx, job.ID, ReplayOptions{AfterID: &zero})
	if !errors.Is(err, ErrEventGap) {
		t.Fatalf("stale retention cursor error = %v, want ErrEventGap", err)
	}
	var gap *EventGapError
	if !errors.As(err, &gap) || gap.PrunedThrough != event.ID || gap.OldestAvailable != 0 || gap.LatestID != event.ID {
		t.Fatalf("retention gap details = %#v", err)
	}
}

func TestIncrementalRefreshStillAppliesCrossStoreRetention(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	options := Options{EventCapacity: 100, EventRetention: time.Hour}
	writer, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	base := time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)
	writer.now = func() time.Time { return base }
	reader.now = func() time.Time { return base.Add(2 * time.Hour) }
	job := createJobForTest(t, writer, testCreateSpec("cross-store-retention", "workspace-a", false))
	event := appendEventForTest(t, writer, job.ID, "progress")

	zero := uint64(0)
	_, err = reader.Replay(context.Background(), job.ID, ReplayOptions{AfterID: &zero})
	if !errors.Is(err, ErrEventGap) {
		t.Fatalf("cross-store stale cursor error = %v, want ErrEventGap", err)
	}
	var gap *EventGapError
	if !errors.As(err, &gap) || gap.PrunedThrough != event.ID {
		t.Fatalf("cross-store retention gap = %#v, want watermark %d", err, event.ID)
	}
}

func TestOpenRecoversIncompleteTailButRejectsMiddleCorruption(t *testing.T) {
	t.Run("incomplete tail", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "jobs")
		store, err := Open(directory, Options{})
		if err != nil {
			t.Fatal(err)
		}
		first := createJobForTest(t, store, testCreateSpec("tail-first", "workspace-a", false))
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, JournalFilename)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(`{"schema_version":1,"sequence":`)); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		recovered, err := Open(directory, Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer recovered.Close()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != int64(len(before)) {
			t.Fatalf("recovered journal size = %d, want %d", info.Size(), len(before))
		}
		if _, err := recovered.Get(context.Background(), first.ID); err != nil {
			t.Fatalf("get job after tail recovery: %v", err)
		}
		createJobForTest(t, recovered, testCreateSpec("tail-second", "workspace-b", false))
	})

	t.Run("middle corruption", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "jobs")
		store, err := Open(directory, Options{})
		if err != nil {
			t.Fatal(err)
		}
		createJobForTest(t, store, testCreateSpec("middle-first", "workspace-a", false))
		createJobForTest(t, store, testCreateSpec("middle-second", "workspace-b", false))
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, JournalFilename)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		separator := bytes.IndexByte(body, '\n')
		if separator < 0 || separator == len(body)-1 {
			t.Fatalf("journal fixture does not have two records: %q", body)
		}
		corrupt := append([]byte(nil), body[:separator+1]...)
		corrupt = append(corrupt, []byte("{not-json}\n")...)
		corrupt = append(corrupt, body[separator+1:]...)
		if err := os.WriteFile(path, corrupt, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(directory, Options{}); err == nil || !strings.Contains(err.Error(), "journal line 2") {
			t.Fatalf("Open middle-corrupt journal error = %v, want line 2 rejection", err)
		}
	})
}

func TestIncrementalRefreshFailsClosedOnJournalCorruption(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{
			name: "corrupt appended record",
			mutate: func(path string) error {
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					return err
				}
				_, writeErr := file.Write([]byte("{not-json}\n"))
				return errors.Join(writeErr, file.Close())
			},
		},
		{
			name: "same-size rewrite",
			mutate: func(path string) error {
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				body[0] = '['
				return os.WriteFile(path, body, 0o600)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "jobs")
			store, err := Open(directory, Options{})
			if err != nil {
				t.Fatal(err)
			}
			job := createJobForTest(t, store, testCreateSpec("live-corruption", "workspace-a", false))
			if err := test.mutate(filepath.Join(directory, JournalFilename)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(context.Background(), job.ID); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Get after corruption error = %v, want ErrUnavailable", err)
			}
			if _, err := store.List(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("List after corruption error = %v, want failed-closed store", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close after corruption: %v", err)
			}
		})
	}
}

func TestUnknownJournalGrowthRevalidatesTheCachedPrefix(t *testing.T) {
	// Model an unsupported writer changing the journal between Store
	// operations. Because it cannot publish an in-process receipt, the next
	// operation must revalidate the complete prefix rather than trust its cache.
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job := createJobForTest(t, store, testCreateSpec("rewrite-and-grow", "workspace-a", false))

	now := time.Now().UTC()
	appended := journalRecord{
		SchemaVersion: JournalVersion,
		Sequence:      store.state.lastRecordSequence + 1,
		Kind:          recordEventAdded,
		RecordedAt:    now,
		Event: &Event{
			ID: store.state.lastEventID + 1, JobID: job.ID, Timestamp: now, Kind: "externally-appended",
		},
	}
	line, err := json.Marshal(appended)
	if err != nil {
		t.Fatal(err)
	}
	line = append(line, '\n')
	path := filepath.Join(directory, JournalFilename)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{'['}, 0); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := writeAll(file, line); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	observed := &observedJournal{journalFile: store.file}
	store.file = observed
	if _, err := store.Get(context.Background(), job.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get after prefix rewrite plus valid growth error = %v, want ErrUnavailable", err)
	}
	if observed.fullScans == 0 {
		t.Fatal("unknown growth trusted cached prefix instead of performing full recovery")
	}
}

func TestSecretsAndRawIdempotencyKeyAreNeverPersisted(t *testing.T) {
	store, directory := openStoreForTest(t, Options{})
	rawKey := "IK-raw-7e58316c-never-persist"
	spec := testCreateSpec(rawKey, "workspace-a", false)
	spec.Request = map[string]any{
		"Authorization: Bearer map-key-secret": "map-value-secret",
		"message":                              "client_secret=free-text-secret; Bearer scheme-secret",
		"nested":                               map[string]any{"x-api-key": "api-value-secret"},
	}
	created, err := store.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AppendEvent(context.Background(), created.Job.ID, EventInput{
		Kind: "progress",
		Data: map[string]any{
			"cookie":  "session-cookie-secret",
			"message": "handoff_token=event-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(directory, JournalFilename))
	if err != nil {
		t.Fatal(err)
	}
	persisted := string(body)
	for _, secret := range []string{
		rawKey,
		"Authorization: Bearer map-key-secret",
		"map-key-secret",
		"map-value-secret",
		"free-text-secret",
		"scheme-secret",
		"x-api-key",
		"api-value-secret",
		"session-cookie-secret",
		"event-secret",
	} {
		if strings.Contains(persisted, secret) {
			t.Errorf("journal leaked %q", secret)
		}
	}
	if !strings.Contains(persisted, DigestRequest([]byte(rawKey))) {
		t.Fatal("journal does not contain the one-way Idempotency-Key digest")
	}
	if !strings.Contains(persisted, `\u003credacted\u003e`) || !strings.Contains(persisted, `\u003credacted-key\u003e`) {
		t.Fatalf("journal did not preserve redaction markers: %s", persisted)
	}
}

func TestSyncFailureFailsClosed(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	syncFailure := errors.New("injected fsync failure")
	store.file = &failNthSyncJournal{journalFile: store.file, failAt: 2, err: syncFailure}
	spec := testCreateSpec("sync-uncertain", "workspace-a", true)

	_, err = store.Create(context.Background(), spec)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, syncFailure) {
		t.Fatalf("Create sync failure = %v, want ErrUnavailable and injected cause", err)
	}
	if _, err := store.List(context.Background()); !errors.Is(err, ErrUnavailable) || !errors.Is(err, syncFailure) {
		t.Fatalf("operation after sync failure = %v, want failed-closed store", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// The append may have reached the kernel before fsync reported failure. A
	// restart must recover that uncertain commit, so retry remains idempotent.
	reopened, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retried, err := reopened.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !retried.Existing {
		t.Fatal("retry after an uncertain fsync outcome created a second job")
	}
}

type failNthSyncJournal struct {
	journalFile
	failAt int
	calls  int
	err    error
}

type observedJournal struct {
	journalFile
	readBytes  int
	writeCalls int
	fullScans  int
}

func (file *observedJournal) Read(body []byte) (int, error) {
	read, err := file.journalFile.Read(body)
	file.readBytes += read
	return read, err
}

func (file *observedJournal) Write(body []byte) (int, error) {
	file.writeCalls++
	return file.journalFile.Write(body)
}

func (file *observedJournal) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		file.fullScans++
	}
	return file.journalFile.Seek(offset, whence)
}

func (file *failNthSyncJournal) Sync() error {
	file.calls++
	if file.calls == file.failAt {
		return file.err
	}
	return file.journalFile.Sync()
}

func openStoreForTest(t *testing.T, options Options) (*Store, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store, directory
}

func testCreateSpec(key, workspaceID string, mutating bool) CreateSpec {
	requestBody := []byte("request:" + key)
	return CreateSpec{
		Kind:        "test.job",
		WorkspaceID: workspaceID,
		Mutating:    mutating,
		Request:     map[string]any{"value": key},
		Idempotency: IdempotencyInput{
			Principal:     "operator",
			Method:        "POST",
			CanonicalPath: "/api/v1/jobs",
			Key:           key,
			RequestDigest: DigestRequest(requestBody),
		},
	}
}

func createJobForTest(t *testing.T, store *Store, spec CreateSpec) Job {
	t.Helper()
	result, err := store.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return result.Job
}

func appendEventForTest(t *testing.T, store *Store, jobID, kind string) Event {
	t.Helper()
	event, err := store.AppendEvent(context.Background(), jobID, EventInput{Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func eventIDs(events []Event) []uint64 {
	ids := make([]uint64, len(events))
	for index := range events {
		ids[index] = events[index].ID
	}
	return ids
}
