package audit

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"
)

const (
	lockMetadataSlotBytes    = 512
	maximumLockMetadataBytes = 2 * lockMetadataSlotBytes
)

var errNoValidInitialLockMetadata = errors.New("audit lock metadata has no complete valid initial slot")

type lockMetadata struct {
	SchemaVersion  int                      `json:"schema_version"`
	StoreID        string                   `json:"store_id"`
	Generation     uint64                   `json:"generation,omitempty"`
	LastSequence   uint64                   `json:"last_sequence,omitempty"`
	PrunedThrough  uint64                   `json:"pruned_through,omitempty"`
	LastRecordedAt *time.Time               `json:"last_recorded_at,omitempty"`
	Retention      *retentionPolicyMetadata `json:"retention,omitempty"`
}

type retentionPolicyMetadata struct {
	MaxEvents      int64 `json:"max_events"`
	RetentionNanos int64 `json:"retention_nanoseconds"`
}

type lockMetadataPayload struct {
	Revision uint64       `json:"revision"`
	Metadata lockMetadata `json:"metadata"`
}

type lockMetadataEnvelope struct {
	Revision uint64       `json:"revision"`
	Metadata lockMetadata `json:"metadata"`
	Digest   string       `json:"digest"`
}

type lockMetadataDiskState struct {
	metadata lockMetadata
	revision uint64
	slot     int
	legacy   bool
}

func newStoreID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate audit store ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func readLockMetadata(file *os.File) (lockMetadata, error) {
	state, err := readLockMetadataDiskState(file)
	return state.metadata, err
}

func readLockMetadataDiskState(file *os.File) (lockMetadataDiskState, error) {
	if file == nil {
		return lockMetadataDiskState{}, errors.New("audit lock descriptor is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return lockMetadataDiskState{}, fmt.Errorf("inspect audit lock metadata: %w", err)
	}
	if info.Size() == 0 {
		return lockMetadataDiskState{slot: -1}, nil
	}
	if info.Size() > maximumLockMetadataBytes {
		return lockMetadataDiskState{}, errors.New("audit lock metadata exceeds size limit")
	}
	body := make([]byte, info.Size())
	if _, err := file.ReadAt(body, 0); err != nil {
		return lockMetadataDiskState{}, fmt.Errorf("read audit lock metadata: %w", err)
	}
	if len(body) <= lockMetadataSlotBytes {
		if metadata, err := decodeLegacyLockMetadata(body); err == nil {
			return lockMetadataDiskState{metadata: metadata, slot: -1, legacy: true}, nil
		}
	}

	best := lockMetadataDiskState{slot: -1}
	found := false
	for slot := 0; slot < 2; slot++ {
		start := slot * lockMetadataSlotBytes
		end := start + lockMetadataSlotBytes
		if len(body) < end {
			continue
		}
		metadata, revision, err := decodeLockMetadataSlot(body[start:end])
		if err != nil {
			continue
		}
		if found && revision == best.revision {
			if !lockMetadataValuesEqual(metadata, best.metadata) {
				return lockMetadataDiskState{}, errors.New("audit lock metadata slots reuse a revision with different values")
			}
			continue
		}
		if !found || revision > best.revision {
			best = lockMetadataDiskState{metadata: metadata, revision: revision, slot: slot}
			found = true
		}
	}
	if !found {
		if metadata, ok := decodeLegacyLockMetadataPrefix(body); ok {
			return lockMetadataDiskState{metadata: metadata, slot: -1, legacy: true}, nil
		}
		if len(body) <= lockMetadataSlotBytes {
			return lockMetadataDiskState{}, errNoValidInitialLockMetadata
		}
		return lockMetadataDiskState{}, errors.New("audit lock metadata has no complete valid slot")
	}
	return best, nil
}

// decodeLegacyLockMetadataPrefix preserves the last complete legacy record
// while its first migration write is being placed in slot 1. WriteAt leaves a
// zero-filled sparse gap between the old newline and the slot boundary; any
// other bytes in that gap make the fallback ambiguous and therefore invalid.
func decodeLegacyLockMetadataPrefix(body []byte) (lockMetadata, bool) {
	prefixLength := len(body)
	if prefixLength > lockMetadataSlotBytes {
		prefixLength = lockMetadataSlotBytes
	}
	prefix := body[:prefixLength]
	newline := bytes.IndexByte(prefix, '\n')
	if newline < 0 {
		return lockMetadata{}, false
	}
	for _, value := range prefix[newline+1:] {
		if value != 0 {
			return lockMetadata{}, false
		}
	}
	metadata, err := decodeLegacyLockMetadata(prefix[:newline+1])
	return metadata, err == nil
}

func decodeLegacyLockMetadata(body []byte) (lockMetadata, error) {
	if len(body) == 0 || body[len(body)-1] != '\n' || bytes.Count(body, []byte{'\n'}) != 1 {
		return lockMetadata{}, errors.New("legacy audit lock metadata must be one complete JSON line")
	}
	var metadata lockMetadata
	if err := json.Unmarshal(bytes.TrimSuffix(body, []byte{'\n'}), &metadata); err != nil {
		return lockMetadata{}, errors.New("legacy audit lock metadata is invalid JSON")
	}
	if err := validateLockMetadata(&metadata); err != nil {
		return lockMetadata{}, err
	}
	return metadata, nil
}

func decodeLockMetadataSlot(slot []byte) (lockMetadata, uint64, error) {
	if len(slot) != lockMetadataSlotBytes || slot[len(slot)-1] != '\n' {
		return lockMetadata{}, 0, errors.New("audit lock metadata slot has an invalid size or terminator")
	}
	var envelope lockMetadataEnvelope
	if err := json.Unmarshal(bytes.TrimSpace(slot), &envelope); err != nil {
		return lockMetadata{}, 0, errors.New("audit lock metadata slot is invalid JSON")
	}
	if envelope.Revision == 0 || len(envelope.Digest) != sha256.Size*2 {
		return lockMetadata{}, 0, errors.New("audit lock metadata slot has an invalid revision or digest")
	}
	if err := validateLockMetadata(&envelope.Metadata); err != nil {
		return lockMetadata{}, 0, err
	}
	digest, err := digestLockMetadata(envelope.Revision, envelope.Metadata)
	if err != nil {
		return lockMetadata{}, 0, err
	}
	if envelope.Digest != digest {
		return lockMetadata{}, 0, errors.New("audit lock metadata slot digest mismatch")
	}
	return envelope.Metadata, envelope.Revision, nil
}

func validateLockMetadata(metadata *lockMetadata) error {
	if metadata == nil {
		return errors.New("audit lock metadata is unavailable")
	}
	if metadata.SchemaVersion != journalSchemaVersion || len(metadata.StoreID) != 32 {
		return errors.New("audit lock metadata has invalid schema or store ID")
	}
	if _, err := hex.DecodeString(metadata.StoreID); err != nil {
		return errors.New("audit lock metadata store ID is invalid")
	}
	if metadata.PrunedThrough > metadata.LastSequence {
		return errors.New("audit lock metadata has invalid sequence watermarks")
	}
	if metadata.LastRecordedAt != nil {
		if metadata.LastRecordedAt.IsZero() {
			return errors.New("audit lock metadata has a zero commit time")
		}
		value := metadata.LastRecordedAt.UTC()
		metadata.LastRecordedAt = &value
	}
	if metadata.Retention != nil && (metadata.Retention.MaxEvents < 0 || metadata.Retention.RetentionNanos < 0) {
		return errors.New("audit lock metadata has an invalid retention policy")
	}
	return nil
}

func writeLockMetadata(file *os.File, directory *os.File, metadata lockMetadata) error {
	if file == nil || directory == nil {
		return errors.New("audit lock or directory descriptor is unavailable")
	}
	current, err := readLockMetadataDiskState(file)
	if err != nil {
		return err
	}
	if current.revision == math.MaxUint64 {
		return errors.New("audit lock metadata revision space exhausted")
	}
	revision := current.revision + 1
	targetSlot := 0
	if current.legacy || current.slot == 0 {
		targetSlot = 1
	}
	return writeLockMetadataSlot(file, directory, metadata, revision, targetSlot)
}

// replaceTornInitialLockMetadata is only for the first-slot crash window. Its
// caller must already have proved that the canonical journal is the existing,
// exact empty file created before initialization began.
func replaceTornInitialLockMetadata(file *os.File, directory *os.File, metadata lockMetadata) error {
	if file == nil || directory == nil {
		return errors.New("audit lock or directory descriptor is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect torn initial audit lock metadata: %w", err)
	}
	if info.Size() <= 0 || info.Size() > lockMetadataSlotBytes {
		return errors.New("torn initial audit lock metadata has an invalid size")
	}
	return writeLockMetadataSlot(file, directory, metadata, 1, 0)
}

func writeLockMetadataSlot(file *os.File, directory *os.File, metadata lockMetadata, revision uint64, targetSlot int) error {
	metadata.SchemaVersion = journalSchemaVersion
	if err := validateLockMetadata(&metadata); err != nil {
		return err
	}
	digest, err := digestLockMetadata(revision, metadata)
	if err != nil {
		return fmt.Errorf("encode audit lock metadata: %w", err)
	}
	body, err := json.Marshal(lockMetadataEnvelope{
		Revision: revision,
		Metadata: metadata,
		Digest:   digest,
	})
	if err != nil {
		return fmt.Errorf("encode audit lock metadata envelope: %w", err)
	}
	if len(body) >= lockMetadataSlotBytes {
		return errors.New("encoded audit lock metadata exceeds slot size")
	}
	slot := make([]byte, lockMetadataSlotBytes)
	for index := range slot {
		slot[index] = ' '
	}
	copy(slot, body)
	slot[len(slot)-1] = '\n'
	if targetSlot < 0 || targetSlot > 1 {
		return errors.New("audit lock metadata target slot is invalid")
	}
	if err := writeAllAt(file, slot, int64(targetSlot*lockMetadataSlotBytes)); err != nil {
		return fmt.Errorf("write audit lock metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync audit lock metadata: %w", err)
	}
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync audit directory after lock metadata: %w", err)
	}
	committed, err := readLockMetadataDiskState(file)
	if err != nil {
		return fmt.Errorf("verify audit lock metadata: %w", err)
	}
	if committed.revision != revision || committed.slot != targetSlot || !lockMetadataValuesEqual(committed.metadata, metadata) {
		return errors.New("verify audit lock metadata: committed slot does not match the requested state")
	}
	return nil
}

func digestLockMetadata(revision uint64, metadata lockMetadata) (string, error) {
	body, err := json.Marshal(lockMetadataPayload{Revision: revision, Metadata: metadata})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func writeAllAt(file *os.File, body []byte, offset int64) error {
	for len(body) > 0 {
		written, err := file.WriteAt(body, offset)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(body) {
			return errors.New("short audit lock metadata write")
		}
		body = body[written:]
		offset += int64(written)
	}
	return nil
}

func lockMetadataValuesEqual(left, right lockMetadata) bool {
	leftBody, leftErr := json.Marshal(left)
	rightBody, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBody, rightBody)
}

func retentionPolicyForOptions(options Options) *retentionPolicyMetadata {
	if options.MaxEvents == 0 && options.Retention == 0 {
		return nil
	}
	return &retentionPolicyMetadata{
		MaxEvents:      int64(options.MaxEvents),
		RetentionNanos: int64(options.Retention),
	}
}

func retentionPoliciesEqual(left, right *retentionPolicyMetadata) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.MaxEvents == right.MaxEvents && left.RetentionNanos == right.RetentionNanos
}

func lockMetadataForState(state *auditState, retention *retentionPolicyMetadata) lockMetadata {
	metadata := lockMetadata{
		SchemaVersion: journalSchemaVersion,
		StoreID:       state.storeID,
		Generation:    state.generation,
		LastSequence:  state.lastSequence,
		PrunedThrough: state.prunedThrough,
		Retention:     retention,
	}
	if !state.lastRecordedAt.IsZero() {
		value := state.lastRecordedAt.UTC()
		metadata.LastRecordedAt = &value
	}
	return metadata
}

func lockMetadataIsPristine(metadata lockMetadata) bool {
	return metadata.StoreID != "" && metadata.Generation == 0 && metadata.LastSequence == 0 &&
		metadata.PrunedThrough == 0 && metadata.LastRecordedAt == nil
}

func auditStateCoversLockMetadata(state *auditState, metadata lockMetadata) error {
	if state == nil || metadata.StoreID == "" || state.storeID != metadata.StoreID {
		return errors.New("audit journal does not match the fixed lock lineage")
	}
	if state.generation < metadata.Generation || state.lastSequence < metadata.LastSequence ||
		state.prunedThrough < metadata.PrunedThrough {
		return errors.New("audit journal rolls back a fixed lock watermark")
	}
	if metadata.LastRecordedAt != nil && state.lastRecordedAt.Before(*metadata.LastRecordedAt) {
		return errors.New("audit journal rolls back the fixed lock commit time")
	}
	return nil
}

func lockMetadataMatchesState(metadata lockMetadata, state *auditState, retention *retentionPolicyMetadata) bool {
	if state == nil || metadata.StoreID != state.storeID || metadata.Generation != state.generation ||
		metadata.LastSequence != state.lastSequence || metadata.PrunedThrough != state.prunedThrough ||
		!retentionPoliciesEqual(metadata.Retention, retention) {
		return false
	}
	if metadata.LastRecordedAt == nil || state.lastRecordedAt.IsZero() {
		return metadata.LastRecordedAt == nil && state.lastRecordedAt.IsZero()
	}
	return metadata.LastRecordedAt.Equal(state.lastRecordedAt)
}
