package consolejobs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCompactionRoundTripPreservesTerminalEventsAndPrunedWatermarks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	options := Options{EventCapacity: 100, EventRetention: time.Hour}
	store, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	current := base.Add(2 * time.Hour)
	store.now = func() time.Time { return base }
	fullyPruned := createJobForTest(t, store, testCreateSpec("fully-pruned", "workspace-pruned", false))
	prunedEvent := appendEventForTest(t, store, fullyPruned.ID, "old-progress")

	store.now = func() time.Time { return current }
	zero := uint64(0)
	if _, err := store.Replay(context.Background(), fullyPruned.ID, ReplayOptions{AfterID: &zero}); !errors.Is(err, ErrEventGap) {
		t.Fatalf("Replay after retention error = %v, want ErrEventGap", err)
	}

	terminal := createJobForTest(t, store, testCreateSpec("terminal-retained", "workspace-terminal", false))
	firstRetained := appendEventForTest(t, store, terminal.ID, "queued")
	if _, err := store.Start(context.Background(), terminal.ID); err != nil {
		t.Fatal(err)
	}
	secondRetained := appendEventForTest(t, store, terminal.ID, "running")
	terminalState, err := store.Transition(context.Background(), terminal.ID, StatusSucceeded, TransitionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})
	reopened.now = func() time.Time { return current }

	gotTerminal, err := reopened.Get(context.Background(), terminal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotTerminal, terminalState) {
		t.Fatalf("terminal job after compaction = %+v, want %+v", gotTerminal, terminalState)
	}
	terminalPage, err := reopened.Replay(context.Background(), terminal.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(terminalPage.Events); !reflect.DeepEqual(got, []uint64{firstRetained.ID, secondRetained.ID}) {
		t.Fatalf("terminal retained event IDs = %v, want [%d %d]", got, firstRetained.ID, secondRetained.ID)
	}
	if terminalPage.PrunedThrough != 0 || terminalPage.LatestID != secondRetained.ID {
		t.Fatalf("terminal event watermarks = %+v", terminalPage)
	}

	prunedPage, err := reopened.Replay(context.Background(), fullyPruned.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(prunedPage.Events) != 0 || prunedPage.PrunedThrough != prunedEvent.ID || prunedPage.LatestID != prunedEvent.ID {
		t.Fatalf("fully pruned event page = %+v, want empty page at watermark %d", prunedPage, prunedEvent.ID)
	}
	_, err = reopened.Replay(context.Background(), fullyPruned.ID, ReplayOptions{AfterID: &zero})
	var gap *EventGapError
	if !errors.As(err, &gap) || gap.PrunedThrough != prunedEvent.ID || gap.OldestAvailable != 0 || gap.LatestID != prunedEvent.ID {
		t.Fatalf("fully pruned gap after compaction = %#v", err)
	}

	next := appendEventForTest(t, reopened, fullyPruned.ID, "after-compaction")
	if next.ID != secondRetained.ID+1 {
		t.Fatalf("first event ID after compaction = %d, want %d", next.ID, secondRetained.ID+1)
	}
}

func TestCompactShrinksCanonicalAndStaleStoreTransparentlyRebinds(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	options := Options{EventCapacity: 1, EventRetention: 24 * time.Hour}
	compactor, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := compactor.Close(); err != nil {
			t.Errorf("close compactor: %v", err)
		}
	})
	job := createJobForTest(t, compactor, testCreateSpec("physical-reclaim", "workspace-a", false))
	var latest Event
	for index := 0; index < 128; index++ {
		latest, err = compactor.AppendEvent(context.Background(), job.ID, EventInput{
			Kind: "progress",
			Data: map[string]any{"chunk": strings.Repeat("x", 1024)},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	stale, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stale.Close(); err != nil {
			t.Errorf("close stale store: %v", err)
		}
	})
	staleDescriptor := stale.file
	canonicalPath := filepath.Join(directory, JournalFilename)
	before, err := os.Stat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := compactor.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= before.Size() {
		t.Fatalf("compacted journal size = %d, want less than %d", after.Size(), before.Size())
	}
	staleInfo, err := staleDescriptor.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if staleInfo.Size() != 0 {
		t.Fatalf("superseded journal descriptor size = %d, want 0", staleInfo.Size())
	}

	next, err := stale.AppendEvent(context.Background(), job.ID, EventInput{Kind: "after-rebind"})
	if err != nil {
		t.Fatalf("append through stale Store after compaction: %v", err)
	}
	if next.ID != latest.ID+1 {
		t.Fatalf("event ID after stale Store rebind = %d, want %d", next.ID, latest.ID+1)
	}
	if stale.file == staleDescriptor {
		t.Fatal("stale Store retained its superseded journal descriptor")
	}
	page, err := compactor.Replay(context.Background(), job.ID, ReplayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eventIDs(page.Events); !reflect.DeepEqual(got, []uint64{next.ID}) {
		t.Fatalf("events after transparent rebind = %v, want [%d]", got, next.ID)
	}
}

func TestOpenRejectsCompleteCheckpointWithoutSnapshotEnd(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	journalPath := filepath.Join(directory, JournalFilename)
	body, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"kind":"snapshot_end"`)) || !bytes.HasSuffix(body, []byte{'\n'}) {
		t.Fatalf("compacted fixture is not a newline-terminated sealed checkpoint: %q", body)
	}
	withoutFinalNewline := bytes.TrimSuffix(body, []byte{'\n'})
	footerStart := bytes.LastIndexByte(withoutFinalNewline, '\n')
	if footerStart < 0 {
		t.Fatal("compacted fixture has no snapshot records before its footer")
	}
	incomplete := append([]byte(nil), withoutFinalNewline[:footerStart+1]...)
	if err := os.WriteFile(journalPath, incomplete, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Open(directory, Options{})
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "incomplete compacted snapshot") {
		t.Fatalf("Open checkpoint without snapshot_end error = %v, want fail-closed incomplete snapshot", err)
	}
	info, statErr := os.Stat(journalPath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Size() != int64(len(incomplete)) {
		t.Fatalf("newline-terminated incomplete checkpoint was truncated to %d bytes, want %d", info.Size(), len(incomplete))
	}
}

func TestOpenRemovesSecureOrphanCompactionFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	orphanPath := filepath.Join(directory, JournalCompactionFilename)
	if err := os.WriteFile(orphanPath, []byte("uncommitted checkpoint"), 0o600); err != nil {
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
	if _, err := os.Lstat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan compaction path after Open error = %v, want not exist", err)
	}
}

func TestStoreRejectsCanonicalRollbackToLowerSealedGeneration(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	createJobForTest(t, store, testCreateSpec("generation-rollback", "workspace-a", false))
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstGeneration, err := os.ReadFile(store.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state.generation != 2 {
		t.Fatalf("current generation = %d, want 2", store.state.generation)
	}

	rollbackPath := filepath.Join(directory, "jobs.rollback")
	if err := os.WriteFile(rollbackPath, firstGeneration, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rollbackPath, store.journalPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(context.Background()); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "generation is 1, want greater than 2") {
		t.Fatalf("List after sealed generation rollback error = %v, want fail-closed generation rejection", err)
	}
	if err := store.Close(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Close after generation rollback = %v, want ErrUnavailable", err)
	}
}

func TestStateAdvancesRejectsSemanticRollback(t *testing.T) {
	store, _ := openStoreForTest(t, Options{EventCapacity: 1})
	job := createJobForTest(t, store, testCreateSpec("semantic-rollback", "workspace-a", false))
	first := appendEventForTest(t, store, job.ID, "first")
	appendEventForTest(t, store, job.ID, "second")
	if _, err := store.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), job.ID, StatusSucceeded, TransitionInput{}); err != nil {
		t.Fatal(err)
	}
	previous := store.state.clone()
	previous.generation = 2

	tests := []struct {
		name   string
		mutate func(*storeState)
	}{
		{name: "same generation", mutate: func(next *storeState) { next.generation = 2 }},
		{name: "lower generation", mutate: func(next *storeState) { next.generation = 1 }},
		{name: "missing job", mutate: func(next *storeState) {
			next.generation = 3
			delete(next.jobs, job.ID)
		}},
		{name: "missing idempotency", mutate: func(next *storeState) {
			next.generation = 3
			for identity := range next.idempotency {
				delete(next.idempotency, identity)
				break
			}
		}},
		{name: "job without revision", mutate: func(next *storeState) {
			next.generation = 3
			changed := next.jobs[job.ID]
			changed.Progress++
			next.jobs[job.ID] = changed
		}},
		{name: "job revision", mutate: func(next *storeState) {
			next.generation = 3
			changed := next.jobs[job.ID]
			changed.Revision--
			next.jobs[job.ID] = changed
		}},
		{name: "immutable job field", mutate: func(next *storeState) {
			next.generation = 3
			changed := next.jobs[job.ID]
			changed.Revision++
			changed.Kind = "changed-kind"
			next.jobs[job.ID] = changed
		}},
		{name: "terminal lifecycle", mutate: func(next *storeState) {
			next.generation = 3
			changed := next.jobs[job.ID]
			changed.Revision++
			changed.Status = StatusRunning
			changed.FinishedAt = nil
			next.jobs[job.ID] = changed
		}},
		{name: "global event watermark", mutate: func(next *storeState) {
			next.generation = 3
			next.lastEventID--
		}},
		{name: "latest event cursor", mutate: func(next *storeState) {
			next.generation = 3
			next.latestEventByJob[job.ID]--
		}},
		{name: "prune watermark", mutate: func(next *storeState) {
			next.generation = 3
			next.prunedThrough[job.ID] = first.ID - 1
		}},
		{name: "retained event", mutate: func(next *storeState) {
			next.generation = 3
			events := next.events[job.ID]
			events[0].Kind = "tampered"
			next.events[job.ID] = events
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := previous.clone()
			test.mutate(next)
			if err := stateAdvances(previous, next); err == nil {
				t.Fatal("stateAdvances accepted a semantic rollback")
			}
		})
	}
}
