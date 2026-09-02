package audit

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"time"
)

type auditRecordKind string

const (
	recordHeader        auditRecordKind = "header"
	recordEvent         auditRecordKind = "event"
	recordPrune         auditRecordKind = "prune"
	recordSnapshotBegin auditRecordKind = "snapshot_begin"
	recordSnapshotEvent auditRecordKind = "snapshot_event"
	recordSnapshotEnd   auditRecordKind = "snapshot_end"
)

type auditRecord struct {
	SchemaVersion int                 `json:"schema_version"`
	Kind          auditRecordKind     `json:"record_kind"`
	RecordedAt    time.Time           `json:"recorded_at"`
	StoreID       string              `json:"store_id,omitempty"`
	Event         *Event              `json:"event,omitempty"`
	PrunedThrough uint64              `json:"pruned_through,omitempty"`
	Checkpoint    *checkpointBoundary `json:"checkpoint,omitempty"`
}

type checkpointBoundary struct {
	StoreID       string `json:"store_id"`
	Generation    uint64 `json:"generation"`
	LastSequence  uint64 `json:"last_sequence"`
	PrunedThrough uint64 `json:"pruned_through"`
	EventCount    uint64 `json:"event_count"`
	Digest        string `json:"digest,omitempty"`
}

type storedEvent struct {
	event      Event
	recordedAt time.Time
}

type auditState struct {
	storeID            string
	generation         uint64
	header             bool
	compacted          bool
	legacy             bool
	lastSequence       uint64
	prunedThrough      uint64
	lastRecordedAt     time.Time
	events             []storedEvent
	hasObsoleteHistory bool
}

func (state *auditState) clone() *auditState {
	result := *state
	result.events = append([]storedEvent(nil), state.events...)
	return &result
}

type auditSnapshot struct {
	completeBytes int64
	modTime       time.Time
}

type auditJournalInitialShape uint8

const (
	initialJournalEmpty auditJournalInitialShape = iota
	initialJournalPartial
	initialJournalLegacy
	initialJournalEnvelope
)

// classifyInitialAuditJournal inspects only the first record and never mutates
// the file. Open uses it to decide whether tail repair is authorized before a
// lock-metadata lineage has been established.
func classifyInitialAuditJournal(file logFile) (auditJournalInitialShape, []byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return initialJournalEmpty, nil, fmt.Errorf("seek to classify audit journal: %w", err)
	}
	line, complete, err := readBoundedAuditLine(bufio.NewReaderSize(file, 64<<10))
	if err != nil {
		return initialJournalEmpty, nil, fmt.Errorf("classify first audit record: %w", err)
	}
	if len(line) == 0 && !complete {
		return initialJournalEmpty, nil, nil
	}
	if !complete {
		return initialJournalPartial, line, nil
	}
	var envelope struct {
		Kind auditRecordKind `json:"record_kind"`
	}
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &envelope); err != nil {
		return initialJournalEmpty, nil, errors.New("classify first audit record: invalid JSON")
	}
	if envelope.Kind == "" {
		return initialJournalLegacy, nil, nil
	}
	return initialJournalEnvelope, nil, nil
}

// isIdentifiableLegacyEventPrefix accepts only the unambiguous prefix emitted
// by the former json.Marshal(Event) writer. Very early ambiguous tears (for
// example just "{\"s") fail closed instead of being guessed as legacy.
func isIdentifiableLegacyEventPrefix(body []byte) bool {
	marker := []byte(`{"sequence":`)
	if len(body) < len(`{"se`) {
		return false
	}
	if len(body) <= len(marker) {
		return bytes.Equal(body, marker[:len(body)])
	}
	return bytes.HasPrefix(body, marker)
}

// isPossibleAuditHeaderPrefix proves that body can be a prefix of the exact
// canonical header encoding written for storeID. It deliberately rejects a
// different StoreID and arbitrary first-record residue before recovery can
// truncate either one.
func isPossibleAuditHeaderPrefix(body []byte, storeID string) bool {
	fixed := []byte(`{"schema_version":1,"record_kind":"header","recorded_at":"`)
	if len(body) <= len(fixed) {
		return bytes.Equal(body, fixed[:len(body)])
	}
	if !bytes.HasPrefix(body, fixed) {
		return false
	}
	remainder := body[len(fixed):]
	quote := bytes.IndexByte(remainder, '"')
	if quote < 0 {
		return isPossibleUTCHeaderTimePrefix(remainder)
	}
	timestamp := remainder[:quote]
	parsed, err := time.Parse(time.RFC3339Nano, string(timestamp))
	if err != nil || len(timestamp) == 0 || timestamp[len(timestamp)-1] != 'Z' {
		return false
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return false
	}
	expectedSuffix := []byte(`,"store_id":"` + storeID + `"}`)
	suffix := remainder[quote+1:]
	return len(suffix) <= len(expectedSuffix) && bytes.Equal(suffix, expectedSuffix[:len(suffix)])
}

func isPossibleUTCHeaderTimePrefix(value []byte) bool {
	if len(value) > len("2006-01-02T15:04:05.999999999Z") {
		return false
	}
	separators := map[int]byte{4: '-', 7: '-', 10: 'T', 13: ':', 16: ':'}
	baseLength := len(value)
	if baseLength > len("2006-01-02T15:04:05") {
		baseLength = len("2006-01-02T15:04:05")
	}
	for index := 0; index < baseLength; index++ {
		if separator, ok := separators[index]; ok {
			if value[index] != separator {
				return false
			}
			continue
		}
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	if len(value) >= 7 && (decimalPair(value[5:7]) < 1 || decimalPair(value[5:7]) > 12) {
		return false
	}
	if len(value) >= 10 {
		year := decimalQuad(value[:4])
		month := decimalPair(value[5:7])
		day := decimalPair(value[8:10])
		if day < 1 || day > daysInUTCMonth(year, month) {
			return false
		}
	}
	if len(value) >= 13 && decimalPair(value[11:13]) > 23 {
		return false
	}
	if len(value) >= 16 && decimalPair(value[14:16]) > 59 {
		return false
	}
	if len(value) >= 19 && decimalPair(value[17:19]) > 59 {
		return false
	}
	if len(value) <= 19 {
		return true
	}
	tail := value[19:]
	if tail[0] == 'Z' {
		return len(tail) == 1
	}
	if tail[0] != '.' || len(tail) > 11 {
		return false
	}
	for index := 1; index < len(tail); index++ {
		if tail[index] == 'Z' {
			return index == len(tail)-1 && index >= 2 && index <= 10
		}
		if tail[index] < '0' || tail[index] > '9' || index > 9 {
			return false
		}
	}
	return true
}

func decimalPair(value []byte) int {
	return int(value[0]-'0')*10 + int(value[1]-'0')
}

func decimalQuad(value []byte) int {
	return decimalPair(value[:2])*100 + decimalPair(value[2:])
}

func daysInUTCMonth(year, month int) int {
	switch month {
	case 2:
		if year%400 == 0 || (year%4 == 0 && year%100 != 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

func snapshotAuditLog(file logFile) (auditSnapshot, error) {
	info, err := file.Stat()
	if err != nil {
		return auditSnapshot{}, fmt.Errorf("inspect audit log: %w", err)
	}
	return auditSnapshot{completeBytes: info.Size(), modTime: info.ModTime()}, nil
}

func (snapshot auditSnapshot) matches(info os.FileInfo) bool {
	return snapshot.completeBytes == info.Size() && snapshot.modTime.Equal(info.ModTime())
}

func marshalAuditRecord(record auditRecord) ([]byte, error) {
	body, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > maximumRecordBytes {
		return nil, errRecordTooLarge
	}
	return body, nil
}

func decodeAuditRecord(line []byte) (auditRecord, error) {
	var record auditRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return auditRecord{}, errors.New("invalid JSON")
	}
	if record.SchemaVersion != journalSchemaVersion {
		return auditRecord{}, fmt.Errorf("schema version is %d, want %d", record.SchemaVersion, journalSchemaVersion)
	}
	if record.Kind == "" {
		return auditRecord{}, errors.New("record kind is required")
	}
	return record, nil
}

// recoverAuditLog validates the complete journal and optionally repairs the
// sole tolerated crash residue: a final non-newline record after a complete
// legacy stream or sealed checkpoint. It never aggregates more than one
// maximum-sized record in memory.
func recoverAuditLog(file logFile, repairTail bool, expectedStoreID string) (*auditState, auditSnapshot, error) {
	initialInfo, err := file.Stat()
	if err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("inspect audit log before recovery: %w", err)
	}
	// Legacy Event-only journals did not persist a separate commit clock. Use
	// the journal inode's modification time for their one-time migration so a
	// caller-controlled occurrence timestamp cannot extend retention.
	legacyRecordedAt := initialInfo.ModTime().UTC()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("seek to start: %w", err)
	}
	state := &auditState{storeID: expectedStoreID}
	reader := bufio.NewReaderSize(file, 64<<10)
	var (
		completeBytes int64
		lineNumber    int
		sawEnvelope   bool
		sawLegacy     bool
		checkpoint    *checkpointRecovery
	)
	for {
		line, complete, err := readBoundedAuditLine(reader)
		if err != nil {
			return nil, auditSnapshot{}, fmt.Errorf("read line %d: %w", lineNumber+1, err)
		}
		if len(line) == 0 && !complete {
			break
		}
		if !complete {
			if checkpoint != nil && !checkpoint.sealed {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: incomplete checkpoint", lineNumber+1)
			}
			if sawEnvelope && !state.header && !state.compacted {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: cannot repair an incomplete tail before a header or sealed checkpoint", lineNumber+1)
			}
			if !repairTail {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: incomplete final record", lineNumber+1)
			}
			if err := file.Truncate(completeBytes); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("truncate incomplete tail: %w", err)
			}
			if err := file.Sync(); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("sync truncated log: %w", err)
			}
			break
		}

		lineNumber++
		completeBytes += int64(len(line))
		body := bytes.TrimSuffix(line, []byte{'\n'})
		var envelope struct {
			Kind auditRecordKind `json:"record_kind"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, auditSnapshot{}, fmt.Errorf("validate line %d: invalid JSON", lineNumber)
		}
		if envelope.Kind == "" {
			if sawEnvelope || checkpoint != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: legacy event follows journal envelope", lineNumber)
			}
			event, err := decodeLine(body)
			if err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
			if err := applyLegacyEvent(state, event, legacyRecordedAt); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
			sawLegacy = true
			continue
		}

		if sawLegacy {
			return nil, auditSnapshot{}, fmt.Errorf("validate line %d: journal envelope follows legacy events", lineNumber)
		}
		sawEnvelope = true
		record, err := decodeAuditRecord(body)
		if err != nil {
			return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
		}
		if checkpoint != nil && !checkpoint.sealed {
			if err := checkpoint.apply(state, record, line); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
			continue
		}
		switch record.Kind {
		case recordHeader:
			if lineNumber != 1 {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: header must be the first record", lineNumber)
			}
			if err := applyHeaderRecord(state, record); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
		case recordSnapshotBegin:
			if lineNumber != 1 || state.lastSequence != 0 || checkpoint != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: snapshot_begin must be the first record", lineNumber)
			}
			checkpoint, err = beginCheckpoint(state, record, line)
			if err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
		case recordEvent:
			if err := applyEventRecord(state, record); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
		case recordPrune:
			if err := applyPruneRecord(state, record); err != nil {
				return nil, auditSnapshot{}, fmt.Errorf("validate line %d: %w", lineNumber, err)
			}
		default:
			return nil, auditSnapshot{}, fmt.Errorf("validate line %d: unexpected record kind %q", lineNumber, record.Kind)
		}
	}
	if checkpoint != nil && !checkpoint.sealed {
		return nil, auditSnapshot{}, errors.New("audit checkpoint is missing snapshot_end")
	}
	if sawEnvelope && !state.header && !state.compacted {
		return nil, auditSnapshot{}, errors.New("audit journal envelope is missing its header")
	}
	if completeBytes == 0 && expectedStoreID != "" {
		return nil, auditSnapshot{}, errors.New("initialized audit journal is empty")
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("seek to end: %w", err)
	}
	snapshot, err := snapshotAuditLog(file)
	if err != nil {
		return nil, auditSnapshot{}, err
	}
	if snapshot.completeBytes != completeBytes {
		return nil, auditSnapshot{}, fmt.Errorf("audit log size is %d after recovery, want %d", snapshot.completeBytes, completeBytes)
	}
	return state, snapshot, nil
}

func applyHeaderRecord(state *auditState, record auditRecord) error {
	if record.StoreID == "" || record.Event != nil || record.Checkpoint != nil || record.PrunedThrough != 0 || record.RecordedAt.IsZero() {
		return errors.New("header has invalid fields")
	}
	if len(record.StoreID) != 32 {
		return errors.New("header has an invalid store ID")
	}
	if _, err := hex.DecodeString(record.StoreID); err != nil {
		return errors.New("header has an invalid store ID")
	}
	if state.storeID != "" && state.storeID != record.StoreID {
		return errors.New("audit header belongs to a different store")
	}
	state.storeID = record.StoreID
	state.header = true
	return nil
}

func readBoundedAuditLine(reader *bufio.Reader) ([]byte, bool, error) {
	line := make([]byte, 0, 4<<10)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > maximumRecordBytes-len(line) {
			return nil, false, errRecordTooLarge
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, false, nil
		default:
			return nil, false, err
		}
	}
}

func decodeLine(line []byte) (Event, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return Event{}, errors.New("empty JSON line")
	}
	var event Event
	if err := json.Unmarshal(line, &event); err != nil {
		return Event{}, errors.New("invalid JSON")
	}
	if err := validatePersistedEvent(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func validatePersistedEvent(event Event) error {
	if event.Sequence == 0 {
		return errors.New("sequence must be positive")
	}
	if event.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if event.Type == "" {
		return errors.New("type is required")
	}
	return nil
}

func applyLegacyEvent(state *auditState, event Event, recordedAt time.Time) error {
	want := state.lastSequence + 1
	if event.Sequence != want {
		return fmt.Errorf("sequence is %d, want %d", event.Sequence, want)
	}
	if recordedAt.IsZero() {
		return errors.New("legacy audit file commit time is unavailable")
	}
	state.events = append(state.events, storedEvent{event: event, recordedAt: recordedAt})
	state.lastSequence = event.Sequence
	state.lastRecordedAt = recordedAt
	state.legacy = true
	return nil
}

func applyEventRecord(state *auditState, record auditRecord) error {
	if record.StoreID != "" || record.Event == nil || record.Checkpoint != nil {
		return errors.New("event record has invalid fields")
	}
	if record.RecordedAt.IsZero() {
		return errors.New("event recorded_at is required")
	}
	if !state.lastRecordedAt.IsZero() && record.RecordedAt.Before(state.lastRecordedAt) {
		return errors.New("event recorded_at moves backward")
	}
	if err := validatePersistedEvent(*record.Event); err != nil {
		return err
	}
	want := state.lastSequence + 1
	if record.Event.Sequence != want {
		return fmt.Errorf("sequence is %d, want %d", record.Event.Sequence, want)
	}
	if record.PrunedThrough < state.prunedThrough || record.PrunedThrough >= record.Event.Sequence {
		return fmt.Errorf("pruned_through is %d outside [%d,%d)", record.PrunedThrough, state.prunedThrough, record.Event.Sequence)
	}
	state.events = append(state.events, storedEvent{event: *record.Event, recordedAt: record.RecordedAt.UTC()})
	state.lastSequence = record.Event.Sequence
	state.lastRecordedAt = record.RecordedAt.UTC()
	return advancePruneWatermark(state, record.PrunedThrough)
}

func applyPruneRecord(state *auditState, record auditRecord) error {
	if record.StoreID != "" || record.Event != nil || record.Checkpoint != nil {
		return errors.New("prune record has invalid fields")
	}
	if record.RecordedAt.IsZero() {
		return errors.New("prune recorded_at is required")
	}
	if !state.lastRecordedAt.IsZero() && record.RecordedAt.Before(state.lastRecordedAt) {
		return errors.New("prune recorded_at moves backward")
	}
	if record.PrunedThrough <= state.prunedThrough || record.PrunedThrough > state.lastSequence {
		return fmt.Errorf("pruned_through is %d outside (%d,%d]", record.PrunedThrough, state.prunedThrough, state.lastSequence)
	}
	state.lastRecordedAt = record.RecordedAt.UTC()
	return advancePruneWatermark(state, record.PrunedThrough)
}

func advancePruneWatermark(state *auditState, through uint64) error {
	if through == state.prunedThrough {
		return nil
	}
	drop := through - state.prunedThrough
	if drop > uint64(len(state.events)) {
		return errors.New("prune watermark skips unavailable events")
	}
	state.events = state.events[int(drop):]
	state.prunedThrough = through
	state.hasObsoleteHistory = true
	return nil
}

type checkpointRecovery struct {
	boundary   checkpointBoundary
	recordedAt time.Time
	hasher     hash.Hash
	seen       uint64
	sealed     bool
}

func beginCheckpoint(state *auditState, record auditRecord, line []byte) (*checkpointRecovery, error) {
	if record.StoreID != "" || record.Checkpoint == nil || record.Event != nil || record.PrunedThrough != 0 || record.RecordedAt.IsZero() {
		return nil, errors.New("snapshot_begin has invalid fields")
	}
	boundary := *record.Checkpoint
	if boundary.StoreID == "" || boundary.Generation == 0 || boundary.PrunedThrough > boundary.LastSequence {
		return nil, errors.New("snapshot_begin has invalid lineage or watermarks")
	}
	if boundary.EventCount != boundary.LastSequence-boundary.PrunedThrough {
		return nil, errors.New("snapshot_begin event count does not match watermarks")
	}
	if boundary.Digest != "" {
		return nil, errors.New("snapshot_begin digest must be empty")
	}
	if state.storeID != "" && state.storeID != boundary.StoreID {
		return nil, errors.New("audit checkpoint belongs to a different store")
	}
	state.storeID = boundary.StoreID
	state.generation = boundary.Generation
	state.compacted = true
	state.prunedThrough = boundary.PrunedThrough
	state.lastRecordedAt = record.RecordedAt.UTC()
	progress := &checkpointRecovery{
		boundary:   boundary,
		recordedAt: record.RecordedAt.UTC(),
		hasher:     sha256.New(),
	}
	_, _ = progress.hasher.Write(line)
	return progress, nil
}

func (progress *checkpointRecovery) apply(state *auditState, record auditRecord, line []byte) error {
	switch record.Kind {
	case recordSnapshotEvent:
		if progress.seen >= progress.boundary.EventCount {
			return errors.New("checkpoint contains too many events")
		}
		if record.StoreID != "" || record.Event == nil || record.Checkpoint != nil || record.PrunedThrough != 0 || record.RecordedAt.IsZero() {
			return errors.New("snapshot_event has invalid fields")
		}
		if err := validatePersistedEvent(*record.Event); err != nil {
			return err
		}
		want := progress.boundary.PrunedThrough + progress.seen + 1
		if record.Event.Sequence != want {
			return fmt.Errorf("snapshot event sequence is %d, want %d", record.Event.Sequence, want)
		}
		if progress.seen != 0 && record.RecordedAt.Before(state.events[len(state.events)-1].recordedAt) {
			return errors.New("snapshot event recorded_at moves backward")
		}
		state.events = append(state.events, storedEvent{event: *record.Event, recordedAt: record.RecordedAt.UTC()})
		progress.seen++
		_, _ = progress.hasher.Write(line)
		return nil
	case recordSnapshotEnd:
		if record.StoreID != "" || record.Checkpoint == nil || record.Event != nil || record.PrunedThrough != 0 || record.RecordedAt.IsZero() {
			return errors.New("snapshot_end has invalid fields")
		}
		if !record.RecordedAt.Equal(progress.recordedAt) {
			return errors.New("snapshot_end recorded_at does not match snapshot_begin")
		}
		end := *record.Checkpoint
		if end.StoreID != progress.boundary.StoreID || end.Generation != progress.boundary.Generation ||
			end.LastSequence != progress.boundary.LastSequence || end.PrunedThrough != progress.boundary.PrunedThrough ||
			end.EventCount != progress.boundary.EventCount || progress.seen != progress.boundary.EventCount {
			return errors.New("snapshot_end does not match snapshot_begin")
		}
		if end.Digest == "" || end.Digest != hex.EncodeToString(progress.hasher.Sum(nil)) {
			return errors.New("snapshot checkpoint digest mismatch")
		}
		state.lastSequence = end.LastSequence
		progress.sealed = true
		return nil
	default:
		return fmt.Errorf("checkpoint expected snapshot_event or snapshot_end, got %q", record.Kind)
	}
}

func stateAdvances(previous, next *auditState) error {
	if err := stateContinues(previous, next); err != nil {
		return err
	}
	if !next.compacted || next.generation <= previous.generation {
		return fmt.Errorf("replacement audit generation is %d, want greater than %d", next.generation, previous.generation)
	}
	return nil
}

func stateContinues(previous, next *auditState) error {
	if previous == nil || next == nil {
		return errors.New("audit state is unavailable")
	}
	if previous.storeID == "" || next.storeID != previous.storeID {
		return errors.New("replacement audit journal belongs to a different store")
	}
	if next.generation < previous.generation {
		return errors.New("audit journal generation moved backward")
	}
	if next.lastSequence < previous.lastSequence || next.prunedThrough < previous.prunedThrough {
		return errors.New("replacement audit journal rolls back a sequence watermark")
	}
	for _, old := range previous.events {
		if old.event.Sequence <= next.prunedThrough {
			continue
		}
		index := old.event.Sequence - next.prunedThrough - 1
		if index >= uint64(len(next.events)) {
			return fmt.Errorf("replacement audit journal changes retained sequence %d", old.event.Sequence)
		}
		retained := next.events[index]
		if !old.recordedAt.Equal(retained.recordedAt) {
			return fmt.Errorf("replacement audit journal changes retained sequence %d commit time", old.event.Sequence)
		}
		if !eventsSemanticallyEqual(old.event, retained.event) {
			return fmt.Errorf("replacement audit journal changes retained sequence %d", old.event.Sequence)
		}
	}
	return nil
}

func eventsSemanticallyEqual(left, right Event) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBody, rightBody)
}
