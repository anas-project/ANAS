package consolejobs

import (
	"bufio"
	"bytes"
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

type storeState struct {
	initialized        bool
	storeID            string
	lastRecordSequence uint64
	lastEventID        uint64
	jobs               map[string]Job
	idempotency        map[string]persistedIdempotency
	events             map[string][]Event
	prunedThrough      map[string]uint64
	latestEventByJob   map[string]uint64
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
	result.storeID = state.storeID
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
	default:
		err = fmt.Errorf("unknown record kind %q", record.Kind)
	}
	if err != nil {
		return err
	}
	state.lastRecordSequence = record.Sequence
	return nil
}

func (state *storeState) applyInitialized(record journalRecord) error {
	if state.initialized || record.Sequence != 1 || record.Job != nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil {
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
	if record.Job == nil || record.Idempotency == nil || record.Event != nil || record.Prune != nil {
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
	if record.Job == nil || record.Idempotency != nil || record.Event != nil || record.Prune != nil {
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
	return nil
}

func (state *storeState) applyEvent(record journalRecord) error {
	if record.Event == nil || record.Job != nil || record.Idempotency != nil || record.Prune != nil {
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
	if event.Timestamp.IsZero() {
		return errors.New("event timestamp is required")
	}
	if err := validateIdentifier("event kind", event.Kind, 128); err != nil {
		return err
	}
	if !payloadIsSanitized(event.Data) {
		return errors.New("event data is not sanitized")
	}
	state.events[event.JobID] = append(state.events[event.JobID], event)
	state.lastEventID = event.ID
	state.latestEventByJob[event.JobID] = event.ID
	return nil
}

func (state *storeState) applyPrune(record journalRecord) error {
	if record.Prune == nil || record.Job != nil || record.Idempotency != nil || record.Event != nil {
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
