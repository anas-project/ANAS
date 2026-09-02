package consolejobs

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	maximumJournalRecordBytes      = 2 << 20
	maximumJournalTransactionBytes = 64 << 20
)

var errJournalRecordTooLarge = errors.New("journal record exceeds size limit")

type journalSnapshot struct {
	completeBytes int64
	modTime       time.Time
	changeSeconds int64
	changeNanos   int64
	changeKnown   bool
}

type recordKind string

const (
	recordStoreInitialized recordKind = "store_initialized"
	recordJobCreated       recordKind = "job_created"
	recordJobUpdated       recordKind = "job_updated"
	recordEventAdded       recordKind = "event_added"
	recordEventsPruned     recordKind = "events_pruned"
	recordSnapshotBegin    recordKind = "snapshot_begin"
	recordJobSnapshot      recordKind = "job_state"
	recordIdempotencyState recordKind = "idempotency_state"
	recordEventCursor      recordKind = "event_cursor"
	recordEventSnapshot    recordKind = "event_state"
	recordSnapshotEnd      recordKind = "snapshot_end"
)

type journalRecord struct {
	SchemaVersion int                   `json:"schema_version"`
	Sequence      uint64                `json:"sequence"`
	Kind          recordKind            `json:"kind"`
	RecordedAt    time.Time             `json:"recorded_at"`
	StoreID       string                `json:"store_id,omitempty"`
	Job           *Job                  `json:"job,omitempty"`
	Idempotency   *persistedIdempotency `json:"idempotency,omitempty"`
	Event         *Event                `json:"event,omitempty"`
	Prune         *eventPrune           `json:"prune,omitempty"`
	Cursor        *eventCursor          `json:"cursor,omitempty"`
	Snapshot      *snapshotBoundary     `json:"snapshot,omitempty"`
}

type persistedIdempotency struct {
	Identity      string `json:"identity"`
	Principal     string `json:"principal"`
	Method        string `json:"method"`
	CanonicalPath string `json:"canonical_path"`
	WorkspaceID   string `json:"workspace_id"`
	KeyDigest     string `json:"key_digest"`
	RequestDigest string `json:"request_digest"`
	JobID         string `json:"job_id"`
}

type eventPrune struct {
	JobID   string `json:"job_id"`
	Through uint64 `json:"through"`
	Reason  string `json:"reason"`
}

type eventCursor struct {
	JobID         string `json:"job_id"`
	PrunedThrough uint64 `json:"pruned_through"`
	LatestEventID uint64 `json:"latest_event_id"`
}

// snapshotBoundary appears once at the beginning and once at the sealed end
// of a compacted generation. Digest is empty in snapshot_begin and contains
// the chained semantic digest in snapshot_end.
type snapshotBoundary struct {
	Generation       uint64 `json:"generation"`
	SourceSequence   uint64 `json:"source_sequence"`
	LastEventID      uint64 `json:"last_event_id"`
	JobCount         uint64 `json:"job_count"`
	IdempotencyCount uint64 `json:"idempotency_count"`
	EventCount       uint64 `json:"event_count"`
	Digest           string `json:"digest,omitempty"`
}

type snapshotProgress struct {
	boundary            snapshotBoundary
	jobCount            uint64
	idempotencyCount    uint64
	eventCount          uint64
	lastRetainedEventID uint64
	digest              [sha256.Size]byte
	cursors             map[string]struct{}
	idempotencyJobs     map[string]struct{}
}

type storeState struct {
	initialized        bool
	compacted          bool
	hasObsoleteHistory bool
	obsoleteRevision   uint64
	storeID            string
	generation         uint64
	lastRecordSequence uint64
	lastEventID        uint64
	jobs               map[string]Job
	idempotency        map[string]persistedIdempotency
	events             map[string][]Event
	prunedThrough      map[string]uint64
	latestEventByJob   map[string]uint64
	snapshot           *snapshotProgress
}

func newStoreState() *storeState {
	return &storeState{
		jobs:             make(map[string]Job),
		idempotency:      make(map[string]persistedIdempotency),
		events:           make(map[string][]Event),
		prunedThrough:    make(map[string]uint64),
		latestEventByJob: make(map[string]uint64),
	}
}

func (state *storeState) clone() *storeState {
	result := newStoreState()
	result.initialized = state.initialized
	result.compacted = state.compacted
	result.hasObsoleteHistory = state.hasObsoleteHistory
	result.obsoleteRevision = state.obsoleteRevision
	result.storeID = state.storeID
	result.generation = state.generation
	result.lastRecordSequence = state.lastRecordSequence
	result.lastEventID = state.lastEventID
	for id, job := range state.jobs {
		result.jobs[id] = cloneJob(job)
	}
	for identity, item := range state.idempotency {
		result.idempotency[identity] = item
	}
	for jobID, events := range state.events {
		cloned := make([]Event, len(events))
		for index, event := range events {
			cloned[index] = cloneEvent(event)
		}
		result.events[jobID] = cloned
	}
	for jobID, through := range state.prunedThrough {
		result.prunedThrough[jobID] = through
	}
	for jobID, latest := range state.latestEventByJob {
		result.latestEventByJob[jobID] = latest
	}
	if state.snapshot != nil {
		progress := *state.snapshot
		progress.cursors = make(map[string]struct{}, len(state.snapshot.cursors))
		for jobID := range state.snapshot.cursors {
			progress.cursors[jobID] = struct{}{}
		}
		progress.idempotencyJobs = make(map[string]struct{}, len(state.snapshot.idempotencyJobs))
		for jobID := range state.snapshot.idempotencyJobs {
			progress.idempotencyJobs[jobID] = struct{}{}
		}
		result.snapshot = &progress
	}
	return result
}

func recoverJournal(file journalFile) (*storeState, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek journal start: %w", err)
	}
	state := newStoreState()
	if _, err := applyJournalTail(file, state, 0); err != nil {
		return nil, err
	}
	if err := state.validateRecovered(); err != nil {
		return nil, err
	}
	return state, nil
}

// refreshJournal applies a tail incrementally only when a bounded process-local
// receipt chain proves that cooperating Store instances produced every growth
// step. File metadata alone cannot distinguish a pure append from an in-place
// prefix rewrite followed by growth, and a hash chain rooted in cached state
// has the same limitation. Unknown growth therefore falls back to full recovery.
// Receipts are deliberately optional: after a crash, across processes, or when
// the bounded receipt history expires, correctness costs one full scan.
//
// The supported-writer boundary requires every journal writer to hold
// jobs.lock. A same-privilege process that edits the 0600 journal while a Store
// holds that advisory lock can race any metadata-only receipt scheme; defending
// that case would require rereading the entire prefix on every append and would
// destroy steady-state append performance. Such a writer is treated as a
// compromise of the journal-owning service account, not a supported peer.
func refreshJournal(file journalFile, state *storeState, snapshot journalSnapshot, receipts *journalReceiptHandle) (*storeState, journalSnapshot, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, journalSnapshot{}, fmt.Errorf("inspect journal for refresh: %w", err)
	}
	current := newJournalSnapshot(info)
	if current.completeBytes < snapshot.completeBytes {
		return nil, journalSnapshot{}, fmt.Errorf("journal shrank from %d to %d bytes", snapshot.completeBytes, current.completeBytes)
	}
	if current.completeBytes == snapshot.completeBytes {
		if snapshot.equal(current) {
			return state, snapshot, nil
		}
		return recoverJournalWithSnapshot(file)
	}
	if !receipts.confirmsAppendChain(snapshot, current) {
		return recoverJournalWithSnapshot(file)
	}

	fresh := state.clone()
	completeBytes, err := applyJournalTail(file, fresh, snapshot.completeBytes)
	if err != nil {
		return nil, journalSnapshot{}, err
	}
	if err := fresh.validateRecovered(); err != nil {
		return nil, journalSnapshot{}, err
	}
	info, err = file.Stat()
	if err != nil {
		return nil, journalSnapshot{}, fmt.Errorf("inspect refreshed journal: %w", err)
	}
	refreshed := newJournalSnapshot(info)
	if info.Size() != completeBytes {
		return nil, journalSnapshot{}, fmt.Errorf("refreshed journal size is %d, want %d", info.Size(), completeBytes)
	}
	if !refreshed.equal(current) {
		return nil, journalSnapshot{}, errors.New("journal changed during incremental refresh")
	}
	return fresh, refreshed, nil
}

func (state *storeState) validateRecovered() error {
	if state.snapshot != nil {
		return errors.New("journal has an incomplete compacted snapshot")
	}
	if state.lastRecordSequence != 0 && !state.initialized {
		return errors.New("journal is missing its initialization record")
	}
	return nil
}

func recoverJournalWithSnapshot(file journalFile) (*storeState, journalSnapshot, error) {
	fresh, err := recoverJournal(file)
	if err != nil {
		return nil, journalSnapshot{}, err
	}
	freshSnapshot, err := snapshotJournal(file)
	if err != nil {
		return nil, journalSnapshot{}, err
	}
	return fresh, freshSnapshot, nil
}

func snapshotJournal(file journalFile) (journalSnapshot, error) {
	info, err := file.Stat()
	if err != nil {
		return journalSnapshot{}, fmt.Errorf("inspect journal snapshot: %w", err)
	}
	return newJournalSnapshot(info), nil
}

func newJournalSnapshot(info os.FileInfo) journalSnapshot {
	seconds, nanos, known := journalChangeTime(info)
	return journalSnapshot{
		completeBytes: info.Size(),
		modTime:       info.ModTime(),
		changeSeconds: seconds,
		changeNanos:   nanos,
		changeKnown:   known,
	}
}

func (snapshot journalSnapshot) matches(info os.FileInfo) bool {
	if info.Size() != snapshot.completeBytes || !info.ModTime().Equal(snapshot.modTime) {
		return false
	}
	seconds, nanos, known := journalChangeTime(info)
	return known == snapshot.changeKnown && (!known || (seconds == snapshot.changeSeconds && nanos == snapshot.changeNanos))
}

func applyJournalTail(file journalFile, state *storeState, offset int64) (int64, error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek journal offset %d: %w", offset, err)
	}
	reader := bufio.NewReader(file)
	completeBytes := offset
	for {
		line, readErr := readBoundedJournalLine(reader)
		if errors.Is(readErr, errJournalRecordTooLarge) {
			lineNumber := state.lastRecordSequence + 1
			return 0, fmt.Errorf("validate journal line %d: record exceeds %d bytes", lineNumber, maximumJournalRecordBytes)
		}
		if readErr == nil {
			lineNumber := state.lastRecordSequence + 1
			completeBytes += int64(len(line))
			record, err := decodeJournalRecord(bytes.TrimSuffix(line, []byte{'\n'}))
			if err != nil {
				return 0, fmt.Errorf("validate journal line %d: %w", lineNumber, err)
			}
			if err := state.apply(record); err != nil {
				return 0, fmt.Errorf("validate journal line %d: %w", lineNumber, err)
			}
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			return 0, fmt.Errorf("read journal: %w", readErr)
		}
		if len(line) != 0 {
			// A record is committed only with its newline. The sole recovery
			// exception is therefore an incomplete residual final record.
			if err := file.Truncate(completeBytes); err != nil {
				return 0, fmt.Errorf("truncate incomplete journal tail: %w", err)
			}
			if err := file.Sync(); err != nil {
				return 0, fmt.Errorf("sync truncated journal: %w", err)
			}
		}
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return 0, fmt.Errorf("seek journal end: %w", err)
		}
		return completeBytes, nil
	}
}

// readBoundedJournalLine is equivalent to ReadBytes('\n') for the journal,
// except it refuses a record as soon as its encoded line would exceed the
// durable format limit. ReadSlice keeps each incoming fragment inside the
// bufio.Reader buffer, while line itself never grows beyond the limit.
func readBoundedJournalLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line) > maximumJournalRecordBytes-len(fragment) {
			return nil, errJournalRecordTooLarge
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return line, err
		}
	}
}

func decodeJournalRecord(line []byte) (journalRecord, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return journalRecord{}, errors.New("empty JSON record")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var record journalRecord
	if err := decoder.Decode(&record); err != nil {
		return journalRecord{}, errors.New("invalid JSON record")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return journalRecord{}, errors.New("record has trailing JSON data")
	}
	return record, nil
}

func (state *storeState) apply(record journalRecord) error {
	if record.SchemaVersion != JournalVersion {
		return fmt.Errorf("schema version is %d, want %d", record.SchemaVersion, JournalVersion)
	}
	wantSequence := state.lastRecordSequence + 1
	if wantSequence == 0 || record.Sequence != wantSequence {
		return fmt.Errorf("record sequence is %d, want %d", record.Sequence, wantSequence)
	}
	if record.RecordedAt.IsZero() {
		return errors.New("recorded_at is required")
	}
	if state.snapshot != nil && !isSnapshotContinuation(record.Kind) {
		return errors.New("ordinary journal record appeared before snapshot_end")
	}
	var err error
	if record.Kind != recordStoreInitialized {
		if !state.initialized {
			return errors.New("journal is missing its initialization record")
		}
		if record.StoreID != "" {
			return errors.New("only store_initialized may contain store_id")
		}
	}
	switch record.Kind {
	case recordStoreInitialized:
		err = state.applyInitialized(record)
	case recordJobCreated:
		err = state.applyJobCreated(record)
	case recordJobUpdated:
		err = state.applyJobUpdated(record)
	case recordEventAdded:
		err = state.applyEvent(record)
	case recordEventsPruned:
		err = state.applyPrune(record)
	case recordSnapshotBegin:
		err = state.applySnapshotBegin(record)
	case recordJobSnapshot:
		err = state.applyJobSnapshot(record)
	case recordIdempotencyState:
		err = state.applyIdempotencySnapshot(record)
	case recordEventCursor:
		err = state.applyEventCursor(record)
	case recordEventSnapshot:
		err = state.applyEventSnapshot(record)
	case recordSnapshotEnd:
		err = state.applySnapshotEnd(record)
	default:
		err = fmt.Errorf("unknown record kind %q", record.Kind)
	}
	if err != nil {
		return err
	}
	state.lastRecordSequence = record.Sequence
	return nil
}

func isSnapshotContinuation(kind recordKind) bool {
	switch kind {
	case recordJobSnapshot, recordIdempotencyState, recordEventCursor, recordEventSnapshot, recordSnapshotEnd:
		return true
	default:
		return false
	}
}

func hasSnapshotFields(record journalRecord) bool {
	return record.Cursor != nil || record.Snapshot != nil
}

func (state *storeState) applyInitialized(record journalRecord) error {
	if state.initialized || record.Sequence != 1 || record.Job != nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || hasSnapshotFields(record) {
		return errors.New("invalid or duplicate store_initialized record")
	}
	if !hexDigestPattern.MatchString(record.StoreID) {
		return errors.New("store_initialized has an invalid store_id")
	}
	state.initialized = true
	state.storeID = record.StoreID
	return nil
}

func (state *storeState) applyJobCreated(record journalRecord) error {
	if record.Job == nil || record.Idempotency == nil || record.Event != nil || record.Prune != nil || hasSnapshotFields(record) {
		return errors.New("job_created record has invalid fields")
	}
	job := cloneJob(*record.Job)
	if err := validatePersistedJob(job); err != nil {
		return err
	}
	if job.Status != StatusQueued || job.Revision != 1 {
		return errors.New("new job must be queued at revision 1")
	}
	if _, exists := state.jobs[job.ID]; exists {
		return fmt.Errorf("job %s already exists", job.ID)
	}
	idempotency := *record.Idempotency
	if err := validatePersistedIdempotency(idempotency, job); err != nil {
		return err
	}
	if _, exists := state.idempotency[idempotency.Identity]; exists {
		return errors.New("idempotency identity already exists")
	}
	state.jobs[job.ID] = job
	state.idempotency[idempotency.Identity] = idempotency
	return nil
}

func (state *storeState) applyJobUpdated(record journalRecord) error {
	if record.Job == nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || hasSnapshotFields(record) {
		return errors.New("job_updated record has invalid fields")
	}
	next := cloneJob(*record.Job)
	if err := validatePersistedJob(next); err != nil {
		return err
	}
	previous, exists := state.jobs[next.ID]
	if !exists {
		return fmt.Errorf("updated job %s does not exist", next.ID)
	}
	if next.Revision != previous.Revision+1 {
		return fmt.Errorf("job %s revision is %d, want %d", next.ID, next.Revision, previous.Revision+1)
	}
	if !sameImmutableJobFields(previous, next) {
		return fmt.Errorf("job %s immutable fields changed", next.ID)
	}
	if !validPersistedJobUpdate(previous, next) {
		return fmt.Errorf("job %s has invalid status update %s -> %s", next.ID, previous.Status, next.Status)
	}
	state.jobs[next.ID] = next
	state.hasObsoleteHistory = true
	if state.obsoleteRevision != ^uint64(0) {
		state.obsoleteRevision++
	}
	return nil
}

func (state *storeState) applyEvent(record journalRecord) error {
	if record.Event == nil || record.Job != nil || record.Idempotency != nil || record.Prune != nil || hasSnapshotFields(record) {
		return errors.New("event_added record has invalid fields")
	}
	event := cloneEvent(*record.Event)
	job, exists := state.jobs[event.JobID]
	if !exists {
		return fmt.Errorf("event references missing job %s", event.JobID)
	}
	if job.Status.terminal() {
		return fmt.Errorf("event references terminal job %s", event.JobID)
	}
	if event.ID == 0 || event.ID != state.lastEventID+1 {
		return fmt.Errorf("event ID is %d, want %d", event.ID, state.lastEventID+1)
	}
	if err := validatePersistedEvent(event); err != nil {
		return err
	}
	state.events[event.JobID] = append(state.events[event.JobID], event)
	state.lastEventID = event.ID
	state.latestEventByJob[event.JobID] = event.ID
	return nil
}

func (state *storeState) applyPrune(record journalRecord) error {
	if record.Prune == nil || record.Job != nil || record.Idempotency != nil || record.Event != nil || hasSnapshotFields(record) {
		return errors.New("events_pruned record has invalid fields")
	}
	prune := *record.Prune
	if _, exists := state.jobs[prune.JobID]; !exists {
		return fmt.Errorf("prune references missing job %s", prune.JobID)
	}
	if prune.Through <= state.prunedThrough[prune.JobID] || prune.Through > state.latestEventByJob[prune.JobID] {
		return fmt.Errorf("invalid prune watermark %d for job %s", prune.Through, prune.JobID)
	}
	if prune.Reason != "capacity" && prune.Reason != "retention" && prune.Reason != "capacity+retention" {
		return fmt.Errorf("invalid prune reason %q", prune.Reason)
	}
	events := state.events[prune.JobID]
	firstRetained := sort.Search(len(events), func(index int) bool { return events[index].ID > prune.Through })
	state.events[prune.JobID] = append([]Event(nil), events[firstRetained:]...)
	state.prunedThrough[prune.JobID] = prune.Through
	state.hasObsoleteHistory = true
	if state.obsoleteRevision != ^uint64(0) {
		state.obsoleteRevision++
	}
	return nil
}

func (state *storeState) applySnapshotBegin(record journalRecord) error {
	if record.Snapshot == nil || record.Job != nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || record.Cursor != nil {
		return errors.New("snapshot_begin record has invalid fields")
	}
	if state.snapshot != nil || state.compacted || record.Sequence != 2 || len(state.jobs) != 0 || len(state.idempotency) != 0 || len(state.events) != 0 {
		return errors.New("snapshot_begin must immediately follow store_initialized")
	}
	boundary := *record.Snapshot
	if boundary.Generation == 0 || boundary.SourceSequence == 0 || boundary.Digest != "" {
		return errors.New("snapshot_begin has invalid generation, source sequence, or digest")
	}
	progress := &snapshotProgress{
		boundary:        boundary,
		cursors:         make(map[string]struct{}),
		idempotencyJobs: make(map[string]struct{}),
	}
	digest, err := advanceSnapshotDigest(progress.digest, record)
	if err != nil {
		return err
	}
	progress.digest = digest
	state.snapshot = progress
	return nil
}

func (state *storeState) applyJobSnapshot(record journalRecord) error {
	if state.snapshot == nil || record.Job == nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || record.Cursor != nil || record.Snapshot != nil {
		return errors.New("job_state record has invalid fields")
	}
	job := cloneJob(*record.Job)
	if err := validatePersistedJob(job); err != nil {
		return err
	}
	if _, exists := state.jobs[job.ID]; exists {
		return fmt.Errorf("snapshot job %s already exists", job.ID)
	}
	if state.snapshot.jobCount >= state.snapshot.boundary.JobCount {
		return errors.New("snapshot contains more jobs than declared")
	}
	digest, err := advanceSnapshotDigest(state.snapshot.digest, record)
	if err != nil {
		return err
	}
	state.jobs[job.ID] = job
	state.snapshot.jobCount++
	state.snapshot.digest = digest
	return nil
}

func (state *storeState) applyIdempotencySnapshot(record journalRecord) error {
	if state.snapshot == nil || record.Idempotency == nil || record.Job != nil || record.Event != nil || record.Prune != nil || record.Cursor != nil || record.Snapshot != nil {
		return errors.New("idempotency_state record has invalid fields")
	}
	item := *record.Idempotency
	job, exists := state.jobs[item.JobID]
	if !exists {
		return fmt.Errorf("snapshot idempotency references missing job %s", item.JobID)
	}
	if err := validatePersistedIdempotency(item, job); err != nil {
		return err
	}
	if _, exists := state.idempotency[item.Identity]; exists {
		return errors.New("snapshot idempotency identity already exists")
	}
	if _, exists := state.snapshot.idempotencyJobs[item.JobID]; exists {
		return fmt.Errorf("snapshot job %s has multiple idempotency identities", item.JobID)
	}
	if state.snapshot.idempotencyCount >= state.snapshot.boundary.IdempotencyCount {
		return errors.New("snapshot contains more idempotency records than declared")
	}
	digest, err := advanceSnapshotDigest(state.snapshot.digest, record)
	if err != nil {
		return err
	}
	state.idempotency[item.Identity] = item
	state.snapshot.idempotencyJobs[item.JobID] = struct{}{}
	state.snapshot.idempotencyCount++
	state.snapshot.digest = digest
	return nil
}

func (state *storeState) applyEventCursor(record journalRecord) error {
	if state.snapshot == nil || record.Cursor == nil || record.Job != nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || record.Snapshot != nil {
		return errors.New("event_cursor record has invalid fields")
	}
	cursor := *record.Cursor
	if _, exists := state.jobs[cursor.JobID]; !exists {
		return fmt.Errorf("snapshot cursor references missing job %s", cursor.JobID)
	}
	if _, exists := state.snapshot.cursors[cursor.JobID]; exists {
		return fmt.Errorf("snapshot job %s has multiple event cursors", cursor.JobID)
	}
	if cursor.PrunedThrough > cursor.LatestEventID || cursor.LatestEventID > state.snapshot.boundary.LastEventID {
		return fmt.Errorf("snapshot event cursor for job %s is invalid", cursor.JobID)
	}
	if cursor.LatestEventID == 0 && cursor.PrunedThrough != 0 {
		return fmt.Errorf("snapshot empty event cursor for job %s has a prune watermark", cursor.JobID)
	}
	digest, err := advanceSnapshotDigest(state.snapshot.digest, record)
	if err != nil {
		return err
	}
	state.prunedThrough[cursor.JobID] = cursor.PrunedThrough
	state.latestEventByJob[cursor.JobID] = cursor.LatestEventID
	state.snapshot.cursors[cursor.JobID] = struct{}{}
	state.snapshot.digest = digest
	return nil
}

func (state *storeState) applyEventSnapshot(record journalRecord) error {
	if state.snapshot == nil || record.Event == nil || record.Job != nil || record.Idempotency != nil || record.Prune != nil || record.Cursor != nil || record.Snapshot != nil {
		return errors.New("event_state record has invalid fields")
	}
	event := cloneEvent(*record.Event)
	if _, exists := state.jobs[event.JobID]; !exists {
		return fmt.Errorf("snapshot event references missing job %s", event.JobID)
	}
	if _, exists := state.snapshot.cursors[event.JobID]; !exists {
		return fmt.Errorf("snapshot event for job %s precedes its cursor", event.JobID)
	}
	if err := validatePersistedEvent(event); err != nil {
		return err
	}
	if event.ID <= state.snapshot.lastRetainedEventID || event.ID > state.snapshot.boundary.LastEventID {
		return fmt.Errorf("snapshot retained event ID %d is out of order or range", event.ID)
	}
	if event.ID <= state.prunedThrough[event.JobID] || event.ID > state.latestEventByJob[event.JobID] {
		return fmt.Errorf("snapshot retained event ID %d is outside job %s cursor", event.ID, event.JobID)
	}
	if state.snapshot.eventCount >= state.snapshot.boundary.EventCount {
		return errors.New("snapshot contains more events than declared")
	}
	digest, err := advanceSnapshotDigest(state.snapshot.digest, record)
	if err != nil {
		return err
	}
	state.events[event.JobID] = append(state.events[event.JobID], event)
	state.snapshot.eventCount++
	state.snapshot.lastRetainedEventID = event.ID
	state.snapshot.digest = digest
	return nil
}

func (state *storeState) applySnapshotEnd(record journalRecord) error {
	if state.snapshot == nil || record.Snapshot == nil || record.Job != nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil || record.Cursor != nil {
		return errors.New("snapshot_end record has invalid fields")
	}
	completed := *record.Snapshot
	started := state.snapshot.boundary
	wantDigest := hex.EncodeToString(state.snapshot.digest[:])
	if completed.Generation != started.Generation || completed.SourceSequence != started.SourceSequence ||
		completed.LastEventID != started.LastEventID || completed.JobCount != started.JobCount ||
		completed.IdempotencyCount != started.IdempotencyCount || completed.EventCount != started.EventCount ||
		!hexDigestPattern.MatchString(completed.Digest) || completed.Digest != wantDigest {
		return errors.New("snapshot_end does not match the sealed snapshot")
	}
	if state.snapshot.jobCount != started.JobCount || state.snapshot.idempotencyCount != started.IdempotencyCount || state.snapshot.eventCount != started.EventCount {
		return errors.New("snapshot record counts do not match snapshot_begin")
	}
	if uint64(len(state.snapshot.cursors)) != started.JobCount || uint64(len(state.snapshot.idempotencyJobs)) != started.JobCount || started.IdempotencyCount != started.JobCount {
		return errors.New("snapshot must contain one cursor and one idempotency identity per job")
	}
	var latestGlobal uint64
	for jobID := range state.jobs {
		latest := state.latestEventByJob[jobID]
		if latest > latestGlobal {
			latestGlobal = latest
		}
		events := state.events[jobID]
		if len(events) == 0 {
			if latest != 0 && state.prunedThrough[jobID] != latest {
				return fmt.Errorf("snapshot job %s is missing its latest retained event", jobID)
			}
			continue
		}
		if events[len(events)-1].ID != latest {
			return fmt.Errorf("snapshot job %s latest event does not match its cursor", jobID)
		}
	}
	if latestGlobal != started.LastEventID {
		return fmt.Errorf("snapshot global event watermark is %d, want %d", latestGlobal, started.LastEventID)
	}
	state.lastEventID = started.LastEventID
	state.generation = started.Generation
	state.compacted = true
	state.hasObsoleteHistory = false
	state.obsoleteRevision = 0
	state.snapshot = nil
	return nil
}

func advanceSnapshotDigest(current [sha256.Size]byte, record journalRecord) ([sha256.Size]byte, error) {
	payload := struct {
		Kind        recordKind            `json:"kind"`
		Job         *Job                  `json:"job,omitempty"`
		Idempotency *persistedIdempotency `json:"idempotency,omitempty"`
		Event       *Event                `json:"event,omitempty"`
		Cursor      *eventCursor          `json:"cursor,omitempty"`
		Snapshot    *snapshotBoundary     `json:"snapshot,omitempty"`
	}{
		Kind: record.Kind, Job: record.Job, Idempotency: record.Idempotency,
		Event: record.Event, Cursor: record.Cursor, Snapshot: record.Snapshot,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode snapshot digest input: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write(current[:])
	_, _ = hash.Write(body)
	var next [sha256.Size]byte
	copy(next[:], hash.Sum(nil))
	return next, nil
}

func validatePersistedEvent(event Event) error {
	if event.ID == 0 || event.Timestamp.IsZero() {
		return errors.New("event ID and timestamp are required")
	}
	if err := validateIdentifier("event job ID", event.JobID, 256); err != nil {
		return err
	}
	if err := validateIdentifier("event kind", event.Kind, 128); err != nil {
		return err
	}
	if !payloadIsSanitized(event.Data) {
		return errors.New("event data is not sanitized")
	}
	return nil
}

func validatePersistedJob(job Job) error {
	for name, value := range map[string]string{
		"job ID": job.ID, "job kind": job.Kind, "workspace ID": job.WorkspaceID, "created by": job.CreatedBy,
	} {
		if err := validateIdentifier(name, value, 256); err != nil {
			return err
		}
	}
	if !job.Status.valid() {
		return fmt.Errorf("invalid job status %q", job.Status)
	}
	if job.CreatedAt.IsZero() || job.Revision == 0 || job.Progress < 0 || job.Progress > 100 {
		return errors.New("job timestamps, revision, or progress are invalid")
	}
	switch job.Status {
	case StatusQueued:
		if job.StartedAt != nil || job.FinishedAt != nil {
			return errors.New("queued job has execution timestamps")
		}
	case StatusRunning:
		if job.StartedAt == nil || job.FinishedAt != nil {
			return errors.New("running job timestamps are invalid")
		}
	default:
		if job.FinishedAt == nil {
			return errors.New("terminal job requires finished_at")
		}
	}
	if job.Status == StatusSucceeded && job.Error != nil {
		return errors.New("succeeded job must not contain an error")
	}
	if job.Status == StatusFailed && job.Error == nil {
		return errors.New("failed job requires a structured error")
	}
	if job.NeedsCompensationCheck && job.Status != StatusFailed && job.Status != StatusInterrupted {
		return errors.New("only failed or interrupted jobs may require compensation")
	}
	if !payloadIsSanitized(job.Request) || !payloadIsSanitized(job.Result) {
		return errors.New("job request or result is not sanitized")
	}
	for _, warning := range job.Warnings {
		if sanitizeText(warning) != warning {
			return errors.New("job warning is not sanitized")
		}
	}
	if job.Error != nil && sanitizeText(job.Error.Message) != job.Error.Message {
		return errors.New("job error is not sanitized")
	}
	return nil
}

func sameImmutableJobFields(left, right Job) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.WorkspaceID == right.WorkspaceID &&
		left.Mutating == right.Mutating && left.CreatedBy == right.CreatedBy && left.CreatedAt.Equal(right.CreatedAt) &&
		reflect.DeepEqual(left.Request, right.Request)
}

func validPersistedJobUpdate(previous, next Job) bool {
	if previous.Status == next.Status {
		if previous.Status == StatusRunning {
			return next.StartedAt != nil && previous.StartedAt != nil && next.StartedAt.Equal(*previous.StartedAt) &&
				next.FinishedAt == nil && next.Error == nil && next.Result == nil && !next.NeedsCompensationCheck
		}
		if (previous.Status == StatusInterrupted || previous.Status == StatusFailed) && previous.NeedsCompensationCheck && !next.NeedsCompensationCheck {
			before := cloneJob(previous)
			before.Revision = next.Revision
			before.NeedsCompensationCheck = false
			before.Warnings = append([]string(nil), next.Warnings...)
			return reflect.DeepEqual(before, next)
		}
		return false
	}
	if previous.Status == StatusQueued {
		return next.Status == StatusRunning
	}
	return previous.Status == StatusRunning && next.Status.terminal()
}

func validatePersistedIdempotency(value persistedIdempotency, job Job) error {
	if value.JobID != job.ID || value.WorkspaceID != job.WorkspaceID || value.Principal != job.CreatedBy {
		return errors.New("idempotency record does not match its job")
	}
	if !hexDigestPattern.MatchString(value.KeyDigest) || !hexDigestPattern.MatchString(value.RequestDigest) || !hexDigestPattern.MatchString(value.Identity) {
		return errors.New("idempotency digests are invalid")
	}
	for name, field := range map[string]string{
		"principal": value.Principal, "HTTP method": value.Method,
		"canonical path": value.CanonicalPath, "workspace ID": value.WorkspaceID,
	} {
		if err := validateIdentifier(name, field, 1024); err != nil {
			return err
		}
	}
	if value.Method != strings.ToUpper(value.Method) || !strings.HasPrefix(value.CanonicalPath, "/") || path.Clean(value.CanonicalPath) != value.CanonicalPath || strings.ContainsAny(value.CanonicalPath, "?#") {
		return errors.New("idempotency method or canonical path is invalid")
	}
	wantIdentity := idempotencyIdentity(value.Principal, value.Method, value.CanonicalPath, value.WorkspaceID, value.KeyDigest)
	if value.Identity != wantIdentity {
		return errors.New("idempotency identity digest does not match its fields")
	}
	return nil
}

func payloadIsSanitized(value map[string]any) bool {
	if value == nil {
		return true
	}
	sanitized, err := sanitizePayload(value)
	return err == nil && reflect.DeepEqual(value, sanitized)
}
