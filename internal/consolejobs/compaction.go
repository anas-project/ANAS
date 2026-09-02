package consolejobs

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

type journalCompactionOperations struct {
	createTemp    func(string, string) (journalFile, error)
	rename        func(string, string) error
	remove        func(string) error
	syncDirectory func(*os.File) error
}

func defaultJournalCompactionOperations() journalCompactionOperations {
	return journalCompactionOperations{
		createTemp: func(path, name string) (journalFile, error) {
			return createExclusiveSecureNamedFile(path, name)
		},
		rename: os.Rename,
		remove: os.Remove,
		syncDirectory: func(directory *os.File) error {
			return directory.Sync()
		},
	}
}

func (store *Store) resetCompactionBoundary() {
	store.checkedObsoleteRevision = 0
	threshold := store.options.JournalCompactionThreshold
	if threshold <= 0 {
		store.nextCompactionBytes = math.MaxInt64
		return
	}
	current := store.journal.completeBytes
	if !store.state.compacted && current >= threshold {
		store.nextCompactionBytes = current
		return
	}
	store.advanceCompactionBoundary(current)
}

func (store *Store) advanceCompactionBoundary(current int64) {
	threshold := store.options.JournalCompactionThreshold
	if threshold <= 0 {
		store.nextCompactionBytes = math.MaxInt64
		return
	}
	remainder := current % threshold
	delta := threshold - remainder
	if delta <= 0 || current > math.MaxInt64-delta {
		store.nextCompactionBytes = math.MaxInt64
		return
	}
	store.nextCompactionBytes = current + delta
}

func (store *Store) minimumCompactionSavings() int64 {
	minimum := store.options.JournalCompactionThreshold / 8
	if minimum < 1 {
		return 1
	}
	return minimum
}

func (store *Store) shouldCompactBeforeAppend(recordBytes int) bool {
	if !store.state.initialized || recordBytes < 0 || store.nextCompactionBytes <= 0 {
		return false
	}
	projected := store.journal.completeBytes
	if int64(recordBytes) > math.MaxInt64-projected {
		return true
	}
	projected += int64(recordBytes)
	return projected >= store.nextCompactionBytes
}

// Compact rewrites the durable job journal as a sealed checkpoint generation.
// It preserves jobs, idempotency identities, retained events, and every event
// watermark while physically discarding obsolete append history.
func (store *Store) Compact(ctx context.Context) error {
	if store == nil {
		return ErrUnavailable
	}
	return store.withState(ctx, func() error {
		_, err := store.compactJournalLocked(ctx, store.now().UTC())
		if err != nil {
			return &PersistenceError{Operation: "compact job journal", Cause: err}
		}
		return nil
	})
}

func cleanupStaleCompactionFile(directory *os.File, path string, operations journalCompactionOperations) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale %s: %w", JournalCompactionFilename, err)
	}
	if err := validateSecureFileInfo(info, JournalCompactionFilename); err != nil {
		return err
	}
	if err := operations.remove(path); err != nil {
		return fmt.Errorf("remove stale %s: %w", JournalCompactionFilename, err)
	}
	if err := operations.syncDirectory(directory); err != nil {
		return fmt.Errorf("sync job store after removing stale %s: %w", JournalCompactionFilename, err)
	}
	return nil
}

// ensureCanonicalJournalLocked adopts a newer sealed generation after another
// Store atomically replaces jobs.jsonl. Callers must hold jobs.lock and their
// in-process gate before invoking it.
func (store *Store) ensureCanonicalJournalLocked() error {
	opened, err := store.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", JournalFilename, err)
	}
	current, err := os.Lstat(store.journalPath)
	if err != nil {
		return fmt.Errorf("inspect canonical %s: %w", JournalFilename, err)
	}
	if err := validateSecureFileInfo(current, JournalFilename); err != nil {
		return err
	}
	if os.SameFile(opened, current) {
		return verifyOpenNamedFile(store.file, store.journalPath, JournalFilename)
	}

	replacement, err := openExistingSecureNamedFile(store.journalPath, JournalFilename)
	if err != nil {
		return err
	}
	closeReplacement := func(cause error) error {
		return errors.Join(cause, replacement.Close())
	}
	fresh, err := recoverJournal(replacement)
	if err != nil {
		return closeReplacement(fmt.Errorf("recover replacement journal: %w", err))
	}
	if !fresh.compacted {
		return closeReplacement(errors.New("replacement journal is not a sealed compacted generation"))
	}
	if err := stateAdvances(store.state, fresh); err != nil {
		return closeReplacement(err)
	}
	snapshot, err := snapshotJournal(replacement)
	if err != nil {
		return closeReplacement(err)
	}
	if err := verifyOpenNamedFile(replacement, store.journalPath, JournalFilename); err != nil {
		return closeReplacement(err)
	}
	info, err := replacement.Stat()
	if err != nil {
		return closeReplacement(fmt.Errorf("inspect adopted journal: %w", err))
	}
	newReceipts := registerJournalReceiptHandle(store.journalPath, fresh.storeID, info)
	oldFile := store.file
	oldReceipts := store.receipts
	store.file = replacement
	store.state = fresh
	store.journal = snapshot
	store.receipts = newReceipts
	store.updateNextPruneAt()
	store.resetCompactionBoundary()
	if oldReceipts != nil {
		oldReceipts.close()
	}
	if err := oldFile.Close(); err != nil {
		return fmt.Errorf("close superseded journal: %w", err)
	}
	return nil
}

func (store *Store) compactJournalLocked(ctx context.Context, recordedAt time.Time) (bool, error) {
	return store.compactJournalStateLocked(ctx, store.state, recordedAt)
}

// compactJournalStateLocked reports whether the supplied state reached the
// canonical path and was made durable by a successful post-rename directory
// fsync. A non-nil error after committed=true is therefore a maintenance
// failure, not a failed business transaction.
func (store *Store) compactJournalStateLocked(ctx context.Context, source *storeState, recordedAt time.Time) (committed bool, returnErr error) {
	if source == nil || !source.initialized {
		return false, errors.New("cannot compact an uninitialized journal")
	}
	if source.generation == math.MaxUint64 {
		return false, errors.New("journal generation space exhausted")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.verifyPaths(); err != nil {
		return false, fmt.Errorf("verify journal before compaction: %w", err)
	}

	tempPath := filepath.Join(store.directory, JournalCompactionFilename)
	if err := cleanupStaleCompactionFile(store.directoryFile, tempPath, store.compaction); err != nil {
		return false, err
	}
	temp, err := store.compaction.createTemp(tempPath, JournalCompactionFilename)
	if err != nil {
		return false, err
	}
	renamed := false
	cleanupTemp := func(cause error) error {
		var removeErr error
		if !renamed {
			removeErr = removeCompactionFileIfSame(temp, tempPath, store.compaction)
			if removeErr == nil {
				removeErr = store.compaction.syncDirectory(store.directoryFile)
			}
		}
		closeErr := temp.Close()
		return errors.Join(cause, removeErr, closeErr)
	}

	compactedState, compactedJournal, err := writeCompactedJournal(ctx, temp, source, recordedAt)
	if err != nil {
		return false, cleanupTemp(err)
	}
	if err := verifyOpenNamedFile(temp, tempPath, JournalCompactionFilename); err != nil {
		return false, cleanupTemp(err)
	}
	if !semanticStateEqual(source, compactedState) {
		return false, cleanupTemp(errors.New("compacted journal is not semantically equivalent to its source"))
	}
	if err := store.compaction.syncDirectory(store.directoryFile); err != nil {
		return false, cleanupTemp(fmt.Errorf("sync compaction temp directory entry: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return false, cleanupTemp(err)
	}
	if err := store.verifyPaths(); err != nil {
		return false, cleanupTemp(fmt.Errorf("reverify journal before compaction rename: %w", err))
	}

	renameErr := store.compaction.rename(tempPath, store.journalPath)
	if renameErr == nil {
		renamed = true
	} else {
		canonicalNamesTemp, identityErr := openFileNamesPath(temp, store.journalPath, JournalFilename)
		if identityErr != nil || !canonicalNamesTemp {
			return false, cleanupTemp(errors.Join(fmt.Errorf("replace job journal: %w", renameErr), identityErr))
		}
		renamed = true
	}

	// From this point onward the canonical path may name the new generation.
	// Complete descriptor adoption even when directory fsync reports an error.
	// The old generation or prospective replacement remains the atomic outcome;
	// only a successful post-rename directory fsync confirms the latter commit.
	directorySyncErr := store.compaction.syncDirectory(store.directoryFile)
	committed = directorySyncErr == nil
	if err := verifyOpenNamedFile(temp, store.journalPath, JournalFilename); err != nil {
		return committed, store.markUnavailable(errors.Join(fmt.Errorf("verify replaced job journal: %w", err), directorySyncErr, renameErr, temp.Close()))
	}
	compactedJournal, err = snapshotJournal(temp)
	if err != nil {
		return committed, store.markUnavailable(errors.Join(fmt.Errorf("snapshot replaced job journal: %w", err), directorySyncErr, renameErr, temp.Close()))
	}

	newInfo, err := temp.Stat()
	if err != nil {
		return committed, store.markUnavailable(errors.Join(fmt.Errorf("inspect replaced job journal: %w", err), directorySyncErr, renameErr, temp.Close()))
	}
	newReceipts := registerJournalReceiptHandle(store.journalPath, compactedState.storeID, newInfo)
	oldFile := store.file
	oldReceipts := store.receipts
	store.file = temp
	store.state = compactedState
	store.journal = compactedJournal
	store.receipts = newReceipts
	store.updateNextPruneAt()
	store.resetCompactionBoundary()

	var truncateErr, oldSyncErr error
	if directorySyncErr == nil {
		truncateErr = oldFile.Truncate(0)
		if truncateErr == nil {
			oldSyncErr = oldFile.Sync()
		}
	}
	if oldReceipts != nil {
		oldReceipts.close()
	}
	oldCloseErr := oldFile.Close()
	if directorySyncErr != nil {
		return false, store.markUnavailable(errors.Join(fmt.Errorf("sync replaced job journal directory: %w", directorySyncErr), renameErr, oldCloseErr))
	}
	return true, errors.Join(renameErr, truncateErr, oldSyncErr, oldCloseErr)
}

func removeCompactionFileIfSame(file journalFile, path string, operations journalCompactionOperations) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect compaction temp before cleanup: %w", err)
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect compaction temp path before cleanup: %w", err)
	}
	if !os.SameFile(info, current) {
		return errors.New("compaction temp path no longer names the opened file")
	}
	if err := validateSecureFileInfo(current, JournalCompactionFilename); err != nil {
		return err
	}
	if err := operations.remove(path); err != nil {
		return fmt.Errorf("remove failed compaction temp: %w", err)
	}
	return nil
}

func openFileNamesPath(file journalFile, path, name string) (bool, error) {
	opened, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect opened %s: %w", name, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect canonical %s: %w", name, err)
	}
	if err := validateSecureFileInfo(current, name); err != nil {
		return false, err
	}
	return os.SameFile(opened, current), nil
}

func writeCompactedJournal(ctx context.Context, file journalFile, source *storeState, recordedAt time.Time) (*storeState, journalSnapshot, error) {
	writer := bufio.NewWriterSize(file, 64<<10)
	verification, boundary, _, err := streamCompactedRecords(ctx, writer, source, recordedAt)
	if err != nil {
		return nil, journalSnapshot{}, err
	}
	if err := writer.Flush(); err != nil {
		return nil, journalSnapshot{}, fmt.Errorf("flush compacted journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, journalSnapshot{}, fmt.Errorf("sync compacted journal: %w", err)
	}
	recovered, err := recoverJournal(file)
	if err != nil {
		return nil, journalSnapshot{}, fmt.Errorf("verify compacted journal recovery: %w", err)
	}
	snapshot, err := snapshotJournal(file)
	if err != nil {
		return nil, journalSnapshot{}, err
	}
	if !semanticStateEqual(source, verification) || !semanticStateEqual(source, recovered) || recovered.generation != boundary.Generation {
		return nil, journalSnapshot{}, errors.New("recovered compacted journal does not match source state")
	}
	return recovered, snapshot, nil
}

func measureCompactedJournal(ctx context.Context, source *storeState, recordedAt time.Time) (int64, error) {
	_, _, size, err := streamCompactedRecords(ctx, io.Discard, source, recordedAt)
	return size, err
}

func streamCompactedRecords(ctx context.Context, writer io.Writer, source *storeState, recordedAt time.Time) (*storeState, snapshotBoundary, int64, error) {
	jobIDs := make([]string, 0, len(source.jobs))
	for jobID := range source.jobs {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)
	idempotencyIDs := make([]string, 0, len(source.idempotency))
	for identity := range source.idempotency {
		idempotencyIDs = append(idempotencyIDs, identity)
	}
	sort.Strings(idempotencyIDs)

	var eventCount uint64
	for _, events := range source.events {
		if uint64(len(events)) > math.MaxUint64-eventCount {
			return nil, snapshotBoundary{}, 0, errors.New("snapshot event count overflow")
		}
		eventCount += uint64(len(events))
	}
	boundary := snapshotBoundary{
		Generation:       source.generation + 1,
		SourceSequence:   source.lastRecordSequence,
		LastEventID:      source.lastEventID,
		JobCount:         uint64(len(source.jobs)),
		IdempotencyCount: uint64(len(source.idempotency)),
		EventCount:       eventCount,
	}

	verification := newStoreState()
	var totalBytes int64
	writeRecord := func(record journalRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		record.SchemaVersion = JournalVersion
		record.Sequence = verification.lastRecordSequence + 1
		record.RecordedAt = recordedAt
		if err := verification.apply(record); err != nil {
			return fmt.Errorf("prepare compacted journal record: %w", err)
		}
		line, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode compacted journal record: %w", err)
		}
		line = append(line, '\n')
		if err := validateJournalAppendSize(0, len(line)); err != nil {
			return err
		}
		if int64(len(line)) > math.MaxInt64-totalBytes {
			return errors.New("compacted journal size overflow")
		}
		if err := writeAll(writer, line); err != nil {
			return fmt.Errorf("write compacted journal: %w", err)
		}
		totalBytes += int64(len(line))
		return nil
	}

	if err := writeRecord(journalRecord{Kind: recordStoreInitialized, StoreID: source.storeID}); err != nil {
		return nil, snapshotBoundary{}, 0, err
	}
	if err := writeRecord(journalRecord{Kind: recordSnapshotBegin, Snapshot: &boundary}); err != nil {
		return nil, snapshotBoundary{}, 0, err
	}
	for _, jobID := range jobIDs {
		job := cloneJob(source.jobs[jobID])
		if err := writeRecord(journalRecord{Kind: recordJobSnapshot, Job: &job}); err != nil {
			return nil, snapshotBoundary{}, 0, err
		}
	}
	for _, identity := range idempotencyIDs {
		item := source.idempotency[identity]
		if err := writeRecord(journalRecord{Kind: recordIdempotencyState, Idempotency: &item}); err != nil {
			return nil, snapshotBoundary{}, 0, err
		}
	}
	for _, jobID := range jobIDs {
		cursor := eventCursor{JobID: jobID, PrunedThrough: source.prunedThrough[jobID], LatestEventID: source.latestEventByJob[jobID]}
		if err := writeRecord(journalRecord{Kind: recordEventCursor, Cursor: &cursor}); err != nil {
			return nil, snapshotBoundary{}, 0, err
		}
	}
	events := newRetainedEventHeap(source, jobIDs)
	for events.Len() != 0 {
		item := heap.Pop(events).(retainedEventHeapItem)
		event := cloneEvent(source.events[item.jobID][item.index])
		if err := writeRecord(journalRecord{Kind: recordEventSnapshot, Event: &event}); err != nil {
			return nil, snapshotBoundary{}, 0, err
		}
		item.index++
		if item.index < len(source.events[item.jobID]) {
			item.eventID = source.events[item.jobID][item.index].ID
			heap.Push(events, item)
		}
	}
	if verification.snapshot == nil {
		return nil, snapshotBoundary{}, 0, errors.New("snapshot digest state disappeared before footer")
	}
	completed := boundary
	completed.Digest = hex.EncodeToString(verification.snapshot.digest[:])
	if err := writeRecord(journalRecord{Kind: recordSnapshotEnd, Snapshot: &completed}); err != nil {
		return nil, snapshotBoundary{}, 0, err
	}
	return verification, boundary, totalBytes, nil
}

type retainedEventHeapItem struct {
	jobID   string
	index   int
	eventID uint64
}

type retainedEventHeap []retainedEventHeapItem

func newRetainedEventHeap(state *storeState, jobIDs []string) *retainedEventHeap {
	items := make(retainedEventHeap, 0, len(jobIDs))
	for _, jobID := range jobIDs {
		if events := state.events[jobID]; len(events) != 0 {
			items = append(items, retainedEventHeapItem{jobID: jobID, eventID: events[0].ID})
		}
	}
	heap.Init(&items)
	return &items
}

func (items retainedEventHeap) Len() int { return len(items) }
func (items retainedEventHeap) Less(left, right int) bool {
	if items[left].eventID == items[right].eventID {
		return items[left].jobID < items[right].jobID
	}
	return items[left].eventID < items[right].eventID
}
func (items retainedEventHeap) Swap(left, right int) {
	items[left], items[right] = items[right], items[left]
}
func (items *retainedEventHeap) Push(value any) {
	*items = append(*items, value.(retainedEventHeapItem))
}
func (items *retainedEventHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func semanticStateEqual(left, right *storeState) bool {
	if left == nil || right == nil || left.initialized != right.initialized || left.storeID != right.storeID || left.lastEventID != right.lastEventID {
		return false
	}
	if !reflect.DeepEqual(left.jobs, right.jobs) || !reflect.DeepEqual(left.idempotency, right.idempotency) {
		return false
	}
	for jobID := range left.jobs {
		if left.prunedThrough[jobID] != right.prunedThrough[jobID] || left.latestEventByJob[jobID] != right.latestEventByJob[jobID] {
			return false
		}
		leftEvents, rightEvents := left.events[jobID], right.events[jobID]
		if len(leftEvents) != len(rightEvents) {
			return false
		}
		for index := range leftEvents {
			if !reflect.DeepEqual(leftEvents[index], rightEvents[index]) {
				return false
			}
		}
	}
	return true
}

func stateAdvances(previous, next *storeState) error {
	if previous == nil || next == nil || previous.storeID != next.storeID {
		return errors.New("replacement journal belongs to a different store")
	}
	if next.generation <= previous.generation {
		return fmt.Errorf("replacement journal generation is %d, want greater than %d", next.generation, previous.generation)
	}
	if next.lastEventID < previous.lastEventID {
		return errors.New("replacement journal rolls back the global event watermark")
	}
	for identity, item := range previous.idempotency {
		if replacement, exists := next.idempotency[identity]; !exists || replacement != item {
			return fmt.Errorf("replacement journal changes idempotency identity %s", identity)
		}
	}
	for jobID, oldJob := range previous.jobs {
		newJob, exists := next.jobs[jobID]
		if !exists || newJob.Revision < oldJob.Revision {
			return fmt.Errorf("replacement journal rolls back job %s", jobID)
		}
		if newJob.Revision == oldJob.Revision && !reflect.DeepEqual(newJob, oldJob) {
			return fmt.Errorf("replacement journal changes job %s without a revision", jobID)
		}
		if newJob.Revision > oldJob.Revision {
			if !sameImmutableJobFields(oldJob, newJob) {
				return fmt.Errorf("replacement journal changes immutable fields for job %s", jobID)
			}
			if !validPersistedJobAdvance(oldJob, newJob) {
				return fmt.Errorf("replacement journal rolls back the lifecycle of job %s", jobID)
			}
		}
		if next.prunedThrough[jobID] < previous.prunedThrough[jobID] || next.latestEventByJob[jobID] < previous.latestEventByJob[jobID] {
			return fmt.Errorf("replacement journal rolls back event cursors for job %s", jobID)
		}
		newEvents := next.events[jobID]
		for _, oldEvent := range previous.events[jobID] {
			if oldEvent.ID <= next.prunedThrough[jobID] {
				continue
			}
			index := sort.Search(len(newEvents), func(index int) bool { return newEvents[index].ID >= oldEvent.ID })
			if index == len(newEvents) || !reflect.DeepEqual(newEvents[index], oldEvent) {
				return fmt.Errorf("replacement journal changes retained event %d for job %s", oldEvent.ID, jobID)
			}
		}
	}
	return nil
}

// validPersistedJobAdvance accepts the transitive closure of supported job
// updates so a Store may adopt a generation containing several peer writes,
// while rejecting lifecycle, timestamp, warning, and compensation rollback.
func validPersistedJobAdvance(previous, next Job) bool {
	if next.Revision <= previous.Revision || !warningsHavePrefix(previous.Warnings, next.Warnings) {
		return false
	}
	if previous.StartedAt != nil && (next.StartedAt == nil || !next.StartedAt.Equal(*previous.StartedAt)) {
		return false
	}
	if previous.FinishedAt != nil && (next.FinishedAt == nil || !next.FinishedAt.Equal(*previous.FinishedAt)) {
		return false
	}

	switch previous.Status {
	case StatusQueued:
		if next.Status == StatusRunning {
			return validPersistedJobUpdate(previous, next)
		}
		return next.Status.terminal() && next.StartedAt != nil && next.Revision-previous.Revision >= 2
	case StatusRunning:
		return validPersistedJobUpdate(previous, next)
	default:
		if next.Status != previous.Status || !previous.NeedsCompensationCheck || next.NeedsCompensationCheck || next.Revision != previous.Revision+1 {
			return false
		}
		expected := cloneJob(previous)
		expected.Revision = next.Revision
		expected.NeedsCompensationCheck = false
		expected.Warnings = append([]string(nil), next.Warnings...)
		return reflect.DeepEqual(expected, next)
	}
}

func warningsHavePrefix(previous, next []string) bool {
	if len(next) < len(previous) {
		return false
	}
	for index := range previous {
		if previous[index] != next[index] {
			return false
		}
	}
	return true
}
