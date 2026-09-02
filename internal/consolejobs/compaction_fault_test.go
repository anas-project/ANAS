package consolejobs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCompactionFailureBeforeRenamePreservesCanonicalGeneration(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*Store, error)
	}{
		{
			name: "temp fsync",
			inject: func(store *Store, injected error) {
				createTemp := store.compaction.createTemp
				store.compaction.createTemp = func(path, name string) (journalFile, error) {
					file, err := createTemp(path, name)
					if err != nil {
						return nil, err
					}
					return &compactionSyncFailureFile{journalFile: file, err: injected}, nil
				}
			},
		},
		{
			name: "rename before commit",
			inject: func(store *Store, injected error) {
				store.compaction.rename = func(string, string) error { return injected }
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, directory := openCompactionFaultStore(t)
			job := createCompactionFaultJob(t, store, "before-rename")
			appendCompactionFaultEvent(t, store, job.ID, "ready")

			canonicalPath := filepath.Join(directory, JournalFilename)
			beforeBody, err := os.ReadFile(canonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeInfo, err := os.Lstat(canonicalPath)
			if err != nil {
				t.Fatal(err)
			}

			injected := errors.New("injected compaction failure before rename")
			test.inject(store, injected)
			err = store.Compact(context.Background())
			if !errors.Is(err, injected) {
				t.Fatalf("Compact error = %v, want injected failure", err)
			}
			if store.unavailable != nil {
				t.Fatalf("pre-commit compaction failure poisoned Store: %v", store.unavailable)
			}

			afterBody, err := os.ReadFile(canonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			afterInfo, err := os.Lstat(canonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterBody, beforeBody) {
				t.Fatal("pre-commit failure changed canonical journal contents")
			}
			if !os.SameFile(beforeInfo, afterInfo) {
				t.Fatal("pre-commit failure replaced the canonical journal inode")
			}
			if _, err := os.Lstat(filepath.Join(directory, JournalCompactionFilename)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed compaction temp remains: %v", err)
			}

			got, err := store.Get(context.Background(), job.ID)
			if err != nil {
				t.Fatalf("Store is unusable after pre-commit failure: %v", err)
			}
			if got.ID != job.ID {
				t.Fatalf("Get job ID = %q, want %q", got.ID, job.ID)
			}
		})
	}
}

func TestCompactionAdoptsCommittedRenameWhenHookReturnsError(t *testing.T) {
	store, directory := openCompactionFaultStore(t)
	job := createCompactionFaultJob(t, store, "committed-rename")
	appendCompactionFaultEvent(t, store, job.ID, "ready")
	source := store.state.clone()

	canonicalPath := filepath.Join(directory, JournalFilename)
	oldInfo, err := os.Lstat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	originalRename := store.compaction.rename
	injected := errors.New("injected error after committed rename")
	store.compaction.rename = func(oldPath, newPath string) error {
		if err := originalRename(oldPath, newPath); err != nil {
			return err
		}
		return injected
	}

	err = store.Compact(context.Background())
	if !errors.Is(err, injected) {
		t.Fatalf("Compact error = %v, want committed-rename hook error", err)
	}
	if store.unavailable != nil {
		t.Fatalf("committed rename hook error poisoned Store: %v", store.unavailable)
	}
	newInfo, err := os.Lstat(canonicalPath)
	if err != nil {
		t.Fatalf("committed rename removed canonical journal: %v", err)
	}
	if os.SameFile(oldInfo, newInfo) {
		t.Fatal("committed rename did not install a new journal generation")
	}
	if _, err := os.Lstat(filepath.Join(directory, JournalCompactionFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed compaction unexpectedly removed or retained the wrong path: %v", err)
	}
	if err := verifyOpenNamedFile(store.file, canonicalPath, JournalFilename); err != nil {
		t.Fatalf("Store did not adopt the committed canonical journal: %v", err)
	}

	canonical, err := openExistingSecureNamedFile(canonicalPath, JournalFilename)
	if err != nil {
		t.Fatal(err)
	}
	recovered, recoverErr := recoverJournal(canonical)
	closeErr := canonical.Close()
	if recoverErr != nil || closeErr != nil {
		t.Fatalf("recover committed canonical = %v; close = %v", recoverErr, closeErr)
	}
	if !semanticStateEqual(source, recovered) {
		t.Fatal("committed replacement is not semantically equivalent to its source")
	}
	if recovered.generation != source.generation+1 {
		t.Fatalf("generation = %d, want %d", recovered.generation, source.generation+1)
	}
	if _, err := store.Get(context.Background(), job.ID); err != nil {
		t.Fatalf("Store is unusable after adopting committed rename: %v", err)
	}
}

func TestCompactionRebindsOldReaderWithoutUsingOldGenerationTail(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	writer := openCompactionFaultStoreAt(t, directory)
	reader := openCompactionFaultStoreAt(t, directory)
	job := createCompactionFaultJob(t, writer, "reader-rebind")
	if _, err := reader.Get(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if writer.receipts.coordinator != reader.receipts.coordinator {
		t.Fatal("Stores did not begin on the same receipt generation")
	}

	oldCoordinator := reader.receipts.coordinator
	oldJournal := &compactionObservedJournal{journalFile: reader.file}
	reader.file = oldJournal
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writer.receipts.coordinator == oldCoordinator {
		t.Fatal("compacted generation inherited the old receipt coordinator")
	}
	event := appendCompactionFaultEvent(t, writer, job.ID, "after-compaction")

	page, err := reader.Replay(context.Background(), job.ID, ReplayOptions{Limit: 16})
	if err != nil {
		t.Fatalf("old reader did not rebind to the canonical generation: %v", err)
	}
	if len(page.Events) == 0 || page.Events[len(page.Events)-1].ID != event.ID {
		t.Fatalf("rebound reader events = %#v, want latest event %d", page.Events, event.ID)
	}
	if reader.receipts.coordinator != writer.receipts.coordinator {
		t.Fatal("rebound reader did not join the replacement generation receipt coordinator")
	}
	if reader.receipts.coordinator == oldCoordinator {
		t.Fatal("rebound reader retained the old generation receipt coordinator")
	}
	if reader.file == oldJournal {
		t.Fatal("reader retained the superseded journal descriptor")
	}
	if oldJournal.readBytes != 0 || oldJournal.fullScans != 0 {
		t.Fatalf("reader consumed the old generation tail: read_bytes=%d full_scans=%d", oldJournal.readBytes, oldJournal.fullScans)
	}
}

func TestStoreCloseAcceptsStaleDescriptorAfterPeerCompaction(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	stale := openCompactionFaultStoreAt(t, directory)
	compactor := openCompactionFaultStoreAt(t, directory)
	createCompactionFaultJob(t, stale, "close-stale")
	if err := compactor.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatalf("Close after peer compaction = %v, want nil", err)
	}
}

func TestAutomaticCompactionCommitsProspectivePruneOnceAtExactThreshold(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store := openCompactionFaultStoreAtWithOptions(t, directory, Options{EventCapacity: 1})
	job := createCompactionFaultJob(t, store, "exact-prune-threshold")
	obsoleteMarker := strings.Repeat("obsolete-compaction-payload-", 512)
	first, err := store.AppendEvent(context.Background(), job.ID, EventInput{
		Kind: "obsolete-event",
		Data: map[string]any{"marker": obsoleteMarker},
	})
	if err != nil {
		t.Fatal(err)
	}

	fixed := time.Now().UTC().Add(time.Minute)
	input := EventInput{Kind: "at-threshold", Data: map[string]any{"value": "retained"}}
	expected, projected := armExactEventCompactionThreshold(t, store, job.ID, input, fixed)
	beforeGeneration := store.state.generation
	beforeInfo, err := os.Lstat(store.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if projected != store.nextCompactionBytes {
		t.Fatalf("projected bytes = %d, compaction boundary = %d", projected, store.nextCompactionBytes)
	}
	if !expected.hasObsoleteHistory || expected.prunedThrough[job.ID] != first.ID {
		t.Fatal("prospective transaction did not include the expected capacity prune")
	}

	event, err := store.AppendEvent(context.Background(), job.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if !semanticStateEqual(expected, store.state) {
		t.Fatal("automatic compaction did not commit the prospective state exactly once")
	}
	if store.state.generation != beforeGeneration+1 {
		t.Fatalf("generation = %d, want %d", store.state.generation, beforeGeneration+1)
	}
	afterInfo, err := os.Lstat(store.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("exact threshold did not replace the journal generation")
	}
	assertSingleCompactionFaultEvent(t, store, job.ID, event.ID)
	if store.state.prunedThrough[job.ID] != first.ID {
		t.Fatalf("pruned through = %d, want obsolete event %d", store.state.prunedThrough[job.ID], first.ID)
	}

	canonicalBody, err := os.ReadFile(store.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonicalBody), obsoleteMarker) {
		t.Fatal("compacted checkpoint retained the physically pruned event payload")
	}
	if got := strings.Count(string(canonicalBody), `"kind":"at-threshold"`); got != 1 {
		t.Fatalf("retained event occurrences = %d, want exactly 1", got)
	}

	reopened := openCompactionFaultStoreAt(t, directory)
	if reopened.state.generation != beforeGeneration+1 || !semanticStateEqual(expected, reopened.state) {
		t.Fatal("reopened Store did not recover the prospective compacted generation")
	}
	assertSingleCompactionFaultEvent(t, reopened, job.ID, event.ID)
}

func TestAutomaticCompactionTreatsRecoveredRenameErrorAsCommitted(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store := openCompactionFaultStoreAtWithOptions(t, directory, Options{EventCapacity: 1})
	job := createCompactionFaultJob(t, store, "automatic-committed-rename")
	appendCompactionFaultEvent(t, store, job.ID, "obsolete")
	input := EventInput{Kind: "committed-once"}
	expected, _ := armExactEventCompactionThreshold(t, store, job.ID, input, time.Now().UTC().Add(time.Minute))

	originalRename := store.compaction.rename
	injected := errors.New("injected error after automatic committed rename")
	store.compaction.rename = func(oldPath, newPath string) error {
		if err := originalRename(oldPath, newPath); err != nil {
			return err
		}
		return injected
	}

	event, err := store.AppendEvent(context.Background(), job.ID, input)
	if err != nil {
		t.Fatalf("AppendEvent after proven committed rename = %v, want success", err)
	}
	if !semanticStateEqual(expected, store.state) {
		t.Fatal("automatic compaction did not retain the committed prospective state")
	}
	assertSingleCompactionFaultEvent(t, store, job.ID, event.ID)

	reopened := openCompactionFaultStoreAt(t, directory)
	assertSingleCompactionFaultEvent(t, reopened, job.ID, event.ID)
}

func TestAutomaticCompactionDoesNotReportOldGenerationCleanupAsBusinessFailure(t *testing.T) {
	tests := []struct {
		name string
		wrap func(journalFile, error) journalFile
	}{
		{
			name: "truncate",
			wrap: func(file journalFile, injected error) journalFile {
				return &compactionOldGenerationFailureFile{journalFile: file, truncateErr: injected}
			},
		},
		{
			name: "sync",
			wrap: func(file journalFile, injected error) journalFile {
				return &compactionOldGenerationFailureFile{journalFile: file, syncErr: injected}
			},
		},
		{
			name: "close",
			wrap: func(file journalFile, injected error) journalFile {
				return &compactionOldGenerationFailureFile{journalFile: file, closeErr: injected}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "jobs")
			store := openCompactionFaultStoreAtWithOptions(t, directory, Options{EventCapacity: 1})
			job := createCompactionFaultJob(t, store, "cleanup-"+test.name)
			if _, err := store.AppendEvent(context.Background(), job.ID, EventInput{
				Kind: "obsolete",
				Data: map[string]any{"payload": strings.Repeat("obsolete-cleanup-payload-", 512)},
			}); err != nil {
				t.Fatal(err)
			}
			input := EventInput{Kind: "retained-" + test.name}
			expected, _ := armExactEventCompactionThreshold(t, store, job.ID, input, time.Now().UTC().Add(time.Minute))
			injected := errors.New("injected old generation " + test.name + " failure")
			store.file = test.wrap(store.file, injected)

			event, err := store.AppendEvent(context.Background(), job.ID, input)
			if err != nil {
				t.Fatalf("AppendEvent with old generation %s failure = %v, want success", test.name, err)
			}
			if store.unavailable != nil {
				t.Fatalf("old generation %s failure poisoned Store: %v", test.name, store.unavailable)
			}
			if !semanticStateEqual(expected, store.state) {
				t.Fatal("automatic compaction did not retain the committed prospective state")
			}
			assertSingleCompactionFaultEvent(t, store, job.ID, event.ID)

			reopened := openCompactionFaultStoreAt(t, directory)
			assertSingleCompactionFaultEvent(t, reopened, job.ID, event.ID)
		})
	}
}

func TestAutomaticCompactionSkipsLiveOnlyHistoryAcrossThreshold(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store := openCompactionFaultStoreAtWithOptions(t, directory, Options{JournalCompactionThreshold: 1})
	beforeInfo, err := os.Lstat(store.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 24; index++ {
		key := "live-only-" + string(rune('a'+index))
		if _, err := store.Create(context.Background(), compactionFaultCreateSpec(key)); err != nil {
			t.Fatal(err)
		}
	}
	afterInfo, err := os.Lstat(store.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if store.state.generation != 0 || store.state.compacted {
		t.Fatalf("live-only history unexpectedly compacted to generation %d", store.state.generation)
	}
	if store.state.hasObsoleteHistory {
		t.Fatal("live-only creates were marked as obsolete history")
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("live-only threshold crossings replaced the journal inode")
	}
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 24 {
		t.Fatalf("live jobs = %d, want 24", len(jobs))
	}
}

func TestPostRenameDirectorySyncFailureFailsClosedWithRecoverableProspectiveCommit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store := openCompactionFaultStoreAtWithOptions(t, directory, Options{EventCapacity: 1})
	eventJob := createCompactionFaultJob(t, store, "post-rename-events")
	obsoleteMarker := strings.Repeat("post-rename-obsolete-payload-", 512)
	if _, err := store.AppendEvent(context.Background(), eventJob.ID, EventInput{
		Kind: "obsolete-event",
		Data: map[string]any{"marker": obsoleteMarker},
	}); err != nil {
		t.Fatal(err)
	}
	retained := appendCompactionFaultEvent(t, store, eventJob.ID, "retained-event")
	if !store.state.hasObsoleteHistory {
		t.Fatal("capacity prune did not mark obsolete journal history")
	}
	store.options.JournalCompactionThreshold = 1
	store.resetCompactionBoundary()
	source := store.state.clone()
	spec := compactionFaultCreateSpec("post-rename-business")

	syncDirectory := store.compaction.syncDirectory
	injected := errors.New("injected post-rename directory fsync failure")
	syncCalls := 0
	store.compaction.syncDirectory = func(directory *os.File) error {
		syncCalls++
		if syncCalls == 2 {
			return injected
		}
		return syncDirectory(directory)
	}

	_, err := store.Create(context.Background(), spec)
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, injected) {
		t.Fatalf("Create error = %v, want ErrUnavailable and injected directory fsync failure", err)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want rename-preceding and post-rename calls", syncCalls)
	}
	if store.unavailable == nil || !errors.Is(store.unavailable, injected) {
		t.Fatalf("Store unavailable cause = %v, want injected failure", store.unavailable)
	}
	if _, err := store.List(context.Background()); !errors.Is(err, ErrUnavailable) || !errors.Is(err, injected) {
		t.Fatalf("operation after directory fsync failure = %v, want failed-closed Store", err)
	}

	canonicalPath := filepath.Join(directory, JournalFilename)
	canonical, err := openExistingSecureNamedFile(canonicalPath, JournalFilename)
	if err != nil {
		t.Fatal(err)
	}
	recovered, recoverErr := recoverJournal(canonical)
	closeErr := canonical.Close()
	if recoverErr != nil || closeErr != nil {
		t.Fatalf("recover post-rename canonical = %v; close = %v", recoverErr, closeErr)
	}
	if !recovered.compacted || recovered.snapshot != nil {
		t.Fatal("post-rename canonical is not a sealed compacted generation")
	}
	if recovered.generation != source.generation+1 {
		t.Fatalf("recovered generation = %d, want %d", recovered.generation, source.generation+1)
	}
	if recovered.lastEventID != source.lastEventID || recovered.prunedThrough[eventJob.ID] != source.prunedThrough[eventJob.ID] {
		t.Fatal("post-rename canonical changed event watermarks while committing the business mutation")
	}
	if events := recovered.events[eventJob.ID]; len(events) != 1 || events[0].ID != retained.ID {
		t.Fatalf("recovered retained events = %#v, want event %d", events, retained.ID)
	}
	_, identity, err := prepareCreate(spec)
	if err != nil {
		t.Fatal(err)
	}
	persisted, exists := recovered.idempotency[identity.Identity]
	if !exists {
		t.Fatal("committed prospective generation is missing business idempotency identity")
	}
	committedJob, exists := recovered.jobs[persisted.JobID]
	if !exists {
		t.Fatal("committed idempotency identity references a missing job")
	}
	if len(recovered.jobs) != len(source.jobs)+1 {
		t.Fatalf("recovered jobs = %d, want source plus one business mutation", len(recovered.jobs))
	}
	canonicalBody, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(canonicalBody), obsoleteMarker) {
		t.Fatal("post-rename sealed generation retained obsolete event bytes")
	}

	reopened := openCompactionFaultStoreAt(t, directory)
	retried, err := reopened.Create(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !retried.Existing || retried.Job.ID != committedJob.ID {
		t.Fatalf("retry = %#v, want existing committed job %s", retried, committedJob.ID)
	}
	if len(reopened.state.jobs) != len(source.jobs)+1 {
		t.Fatal("idempotent retry duplicated the uncertain business commit")
	}
	page, err := reopened.Replay(context.Background(), eventJob.ID, ReplayOptions{Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	if page.LatestID != source.latestEventByJob[eventJob.ID] || page.PrunedThrough != source.prunedThrough[eventJob.ID] || len(page.Events) != 1 || page.Events[0].ID != retained.ID {
		t.Fatalf("reopened event page = %#v, want preserved watermarks and event %d", page, retained.ID)
	}
}

type compactionSyncFailureFile struct {
	journalFile
	err error
}

func (file *compactionSyncFailureFile) Sync() error { return file.err }

type compactionOldGenerationFailureFile struct {
	journalFile
	truncateErr error
	syncErr     error
	closeErr    error
}

func (file *compactionOldGenerationFailureFile) Truncate(size int64) error {
	if file.truncateErr != nil {
		return file.truncateErr
	}
	return file.journalFile.Truncate(size)
}

func (file *compactionOldGenerationFailureFile) Sync() error {
	if file.syncErr != nil {
		return file.syncErr
	}
	return file.journalFile.Sync()
}

func (file *compactionOldGenerationFailureFile) Close() error {
	return errors.Join(file.journalFile.Close(), file.closeErr)
}

type compactionObservedJournal struct {
	journalFile
	readBytes int
	fullScans int
}

func (file *compactionObservedJournal) Read(body []byte) (int, error) {
	read, err := file.journalFile.Read(body)
	file.readBytes += read
	return read, err
}

func (file *compactionObservedJournal) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		file.fullScans++
	}
	return file.journalFile.Seek(offset, whence)
}

func openCompactionFaultStore(t *testing.T) (*Store, string) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "jobs")
	return openCompactionFaultStoreAt(t, directory), directory
}

func openCompactionFaultStoreAt(t *testing.T, directory string) *Store {
	return openCompactionFaultStoreAtWithOptions(t, directory, Options{})
}

func openCompactionFaultStoreAtWithOptions(t *testing.T, directory string, options Options) *Store {
	t.Helper()
	store, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close compaction fault Store: %v", err)
		}
	})
	return store
}

func createCompactionFaultJob(t *testing.T, store *Store, key string) Job {
	t.Helper()
	created, err := store.Create(context.Background(), compactionFaultCreateSpec(key))
	if err != nil {
		t.Fatal(err)
	}
	return created.Job
}

func compactionFaultCreateSpec(key string) CreateSpec {
	requestBody := []byte("request:" + key)
	return CreateSpec{
		Kind:        "test.compaction-fault",
		WorkspaceID: "workspace-a",
		Request:     map[string]any{"key": key},
		Idempotency: IdempotencyInput{
			Principal:     "operator",
			Method:        "POST",
			CanonicalPath: "/api/v1/jobs",
			Key:           key,
			RequestDigest: DigestRequest(requestBody),
		},
	}
}

func appendCompactionFaultEvent(t *testing.T, store *Store, jobID, kind string) Event {
	t.Helper()
	event, err := store.AppendEvent(context.Background(), jobID, EventInput{Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func armExactEventCompactionThreshold(t *testing.T, store *Store, jobID string, input EventInput, recordedAt time.Time) (*storeState, int64) {
	t.Helper()
	store.now = func() time.Time { return recordedAt }
	data, err := sanitizePayload(input.Data)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{ID: store.state.lastEventID + 1, JobID: jobID, Timestamp: recordedAt, Kind: input.Kind, Data: data}
	records := []journalRecord{{Kind: recordEventAdded, RecordedAt: recordedAt, Event: &event}}
	preview := store.state.clone()
	preview.events[jobID] = append(preview.events[jobID], cloneEvent(event))
	preview.lastEventID = event.ID
	preview.latestEventByJob[jobID] = event.ID
	records = append(records, store.pruneRecords(preview, recordedAt)...)
	prospective, body, err := store.prepareJournalRecords(records)
	if err != nil {
		t.Fatal(err)
	}
	projected := store.journal.completeBytes + int64(len(body))
	store.options.JournalCompactionThreshold = projected
	store.resetCompactionBoundary()
	return prospective, projected
}

func assertSingleCompactionFaultEvent(t *testing.T, store *Store, jobID string, eventID uint64) {
	t.Helper()
	page, err := store.Replay(context.Background(), jobID, ReplayOptions{Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].ID != eventID || page.LatestID != eventID {
		t.Fatalf("events = %#v latest=%d, want exactly event %d", page.Events, page.LatestID, eventID)
	}
}
