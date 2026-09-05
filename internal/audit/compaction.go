package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type auditCompactionOperations struct {
	createTemp    func(string, string) (logFile, error)
	rename        func(string, string) error
	remove        func(string) error
	syncDirectory func(*os.File) error
}

// cleanCompactionCancellationError means cancellation was observed before the
// canonical journal was replaced and every temporary-file cleanup step
// completed successfully. Callers may report the interrupted operation without
// failing the Writer closed.
type cleanCompactionCancellationError struct {
	cause error
}

func (err *cleanCompactionCancellationError) Error() string {
	return err.cause.Error()
}

func (err *cleanCompactionCancellationError) Unwrap() error {
	return err.cause
}

func isCleanCompactionCancellation(err error) bool {
	var cancellation *cleanCompactionCancellationError
	return errors.As(err, &cancellation)
}

func defaultAuditCompactionOperations() auditCompactionOperations {
	return auditCompactionOperations{
		createTemp: func(path, name string) (logFile, error) {
			return createExclusiveSecureNamedFile(path, name)
		},
		rename: os.Rename,
		remove: os.Remove,
		syncDirectory: func(directory *os.File) error {
			return directory.Sync()
		},
	}
}

func cleanupStaleCompactionFile(directory *os.File, path string, operations auditCompactionOperations) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect stale %s: %w", CompactionFilename, err)
	}
	if err := validateSecureFileInfo(info, CompactionFilename); err != nil {
		return err
	}
	if err := operations.remove(path); err != nil {
		return fmt.Errorf("remove stale %s: %w", CompactionFilename, err)
	}
	if err := operations.syncDirectory(directory); err != nil {
		return fmt.Errorf("sync audit directory after removing stale %s: %w", CompactionFilename, err)
	}
	return nil
}

func removeCompactionFileIfSame(file logFile, path string, operations auditCompactionOperations) error {
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s before cleanup: %w", CompactionFilename, err)
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s before cleanup: %w", CompactionFilename, err)
	}
	if err := validateSecureFileInfo(current, CompactionFilename); err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return errors.New("audit compaction path no longer names the opened file")
	}
	if err := operations.remove(path); err != nil {
		return fmt.Errorf("remove failed %s: %w", CompactionFilename, err)
	}
	return nil
}

func (w *Writer) resetCompactionBoundary() {
	threshold := w.options.CompactionThreshold
	if threshold <= 0 {
		w.nextCompactionBytes = math.MaxInt64
		return
	}
	current := w.journal.completeBytes
	remainder := current % threshold
	delta := threshold - remainder
	if delta <= 0 || current > math.MaxInt64-delta {
		w.nextCompactionBytes = math.MaxInt64
		return
	}
	w.nextCompactionBytes = current + delta
}

func (w *Writer) advanceCompactionBoundary(projected int64) {
	w.journal.completeBytes = projected
	w.resetCompactionBoundary()
}

func (w *Writer) minimumCompactionSavings() int64 {
	minimum := w.options.CompactionThreshold / 8
	if minimum < 1 {
		return 1
	}
	return minimum
}

func (w *Writer) shouldCompactBeforeAppend(prospective *auditState, projected int64, recordedAt time.Time) (bool, bool, error) {
	if projected < w.nextCompactionBytes {
		return false, false, nil
	}
	if !prospective.hasObsoleteHistory {
		return false, true, nil
	}
	checkpointBytes, err := encodedCheckpointSize(prospective, recordedAt)
	if err != nil {
		return false, true, err
	}
	savings := projected - checkpointBytes
	return savings >= w.minimumCompactionSavings(), true, nil
}

func encodedCheckpointSize(state *auditState, recordedAt time.Time) (int64, error) {
	if state.generation == math.MaxUint64 {
		return 0, errors.New("audit generation space exhausted")
	}
	boundary := checkpointBoundary{
		StoreID: state.storeID, Generation: state.generation + 1,
		LastSequence: state.lastSequence, PrunedThrough: state.prunedThrough,
		EventCount: uint64(len(state.events)),
	}
	begin, err := marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion, Kind: recordSnapshotBegin,
		RecordedAt: recordedAt, Checkpoint: &boundary,
	})
	if err != nil {
		return 0, err
	}
	total := int64(len(begin))
	for _, retained := range state.events {
		line, err := marshalAuditRecord(auditRecord{
			SchemaVersion: journalSchemaVersion, Kind: recordSnapshotEvent,
			RecordedAt: retained.recordedAt, Event: &retained.event,
		})
		if err != nil {
			return 0, err
		}
		if int64(len(line)) > math.MaxInt64-total {
			return 0, errors.New("audit checkpoint size overflows int64")
		}
		total += int64(len(line))
	}
	endBoundary := boundary
	endBoundary.Digest = strings.Repeat("0", sha256.Size*2)
	end, err := marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion, Kind: recordSnapshotEnd,
		RecordedAt: recordedAt, Checkpoint: &endBoundary,
	})
	if err != nil {
		return 0, err
	}
	if int64(len(end)) > math.MaxInt64-total {
		return 0, errors.New("audit checkpoint size overflows int64")
	}
	return total + int64(len(end)), nil
}

func applyRetentionPolicy(state *auditState, recordedAt time.Time, options Options) error {
	drop := 0
	if options.MaxEvents > 0 && len(state.events) > options.MaxEvents {
		drop = len(state.events) - options.MaxEvents
	}
	if options.Retention > 0 {
		cutoff := recordedAt.Add(-options.Retention)
		timeDrop := 0
		for timeDrop < len(state.events) && state.events[timeDrop].recordedAt.Before(cutoff) {
			timeDrop++
		}
		if timeDrop > drop {
			drop = timeDrop
		}
	}
	if drop == 0 {
		return nil
	}
	return advancePruneWatermark(state, state.events[drop-1].event.Sequence)
}

// Compact applies the configured audit retention policy and, when it makes
// history obsolete, rewrites the live suffix as one sealed generation. It is
// safe to call periodically even when no append occurs.
func (w *Writer) Compact(ctx context.Context) error {
	if w == nil {
		return ErrUnavailable
	}
	if ctx == nil {
		return &PersistenceError{Operation: "compact audit log", Cause: errors.New("context is nil")}
	}
	if err := w.acquire(ctx); err != nil {
		return &PersistenceError{Operation: "compact audit log", Cause: err}
	}
	defer w.release()
	if w.closed || w.file == nil || w.lockFile == nil {
		return ErrUnavailable
	}
	if w.unavailable != nil {
		return w.markUnavailable(w.unavailable)
	}
	if err := lockAuditFile(ctx, w.done, w.lockFile); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errWriterClosed) {
			return &PersistenceError{Operation: "compact audit log", Cause: err}
		}
		return w.markUnavailable(fmt.Errorf("acquire cross-process lock for compaction: %w", err))
	}
	locked := true
	defer func() {
		if locked {
			_ = unlockAuditFile(w.lockFile)
		}
	}()
	if err := w.refreshLocked(); err != nil {
		return w.markUnavailable(fmt.Errorf("refresh audit log before compaction: %w", err))
	}
	recordedAt, err := w.commitTime()
	if err != nil {
		return w.markUnavailable(err)
	}
	prospective := w.state.clone()
	if err := applyRetentionPolicy(prospective, recordedAt, w.options); err != nil {
		return w.markUnavailable(err)
	}
	if !prospective.hasObsoleteHistory && prospective.compacted {
		if err := unlockAuditFile(w.lockFile); err != nil {
			locked = false
			return w.markUnavailable(fmt.Errorf("release cross-process lock after compaction no-op: %w", err))
		}
		locked = false
		return nil
	}
	committed, err := w.compactStateLocked(ctx, prospective, recordedAt)
	if err != nil {
		if committed {
			// The durable commit point was reached; maintenance errors after it
			// cannot be reported as a failed audit transaction.
			err = nil
		} else if isCleanCompactionCancellation(err) {
			if unlockErr := unlockAuditFile(w.lockFile); unlockErr != nil {
				locked = false
				return w.markUnavailable(fmt.Errorf("release cross-process lock after interrupted compaction: %w", unlockErr))
			}
			locked = false
			return &PersistenceError{Operation: "compact audit log", Cause: err}
		} else {
			return w.markUnavailable(fmt.Errorf("compact audit log: %w", err))
		}
	}
	if err := w.persistLockMetadataLocked(w.state); err != nil {
		return w.markUnavailable(fmt.Errorf("persist audit watermark after compaction: %w", err))
	}
	if err := unlockAuditFile(w.lockFile); err != nil {
		locked = false
		return w.markUnavailable(fmt.Errorf("release cross-process lock after compaction: %w", err))
	}
	locked = false
	return nil
}

func (w *Writer) compactStateLocked(ctx context.Context, source *auditState, recordedAt time.Time) (committed bool, returnErr error) {
	if source == nil || source.storeID == "" {
		return false, errors.New("cannot compact an uninitialized audit journal")
	}
	if source.generation == math.MaxUint64 {
		return false, errors.New("audit generation space exhausted")
	}
	if err := ctx.Err(); err != nil {
		return false, &cleanCompactionCancellationError{cause: err}
	}
	if err := w.verifyPaths(); err != nil {
		return false, fmt.Errorf("verify audit paths before compaction: %w", err)
	}

	tempPath := filepath.Join(w.directory, CompactionFilename)
	if err := cleanupStaleCompactionFile(w.directoryFile, tempPath, w.compaction); err != nil {
		return false, err
	}
	temp, err := w.compaction.createTemp(tempPath, CompactionFilename)
	if err != nil {
		return false, err
	}
	renameAttempted := false
	renamed := false
	adopted := false
	cleanupTemp := func(cause error, cancellationObserved bool) error {
		var removeErr error
		if !renamed && !adopted {
			removeErr = removeCompactionFileIfSame(temp, tempPath, w.compaction)
			if removeErr == nil {
				removeErr = w.compaction.syncDirectory(w.directoryFile)
			}
		}
		var closeErr error
		if !adopted {
			closeErr = temp.Close()
		}
		if cancellationObserved && !renameAttempted && !renamed && !adopted &&
			removeErr == nil && closeErr == nil {
			return &cleanCompactionCancellationError{cause: cause}
		}
		return errors.Join(cause, removeErr, closeErr)
	}

	recovered, snapshot, err := writeAuditCheckpoint(ctx, temp, source, recordedAt)
	if err != nil {
		ctxErr := ctx.Err()
		return false, cleanupTemp(err,
			(err == context.Canceled || err == context.DeadlineExceeded) && err == ctxErr)
	}
	if err := verifyOpenNamedFile(temp, tempPath, CompactionFilename); err != nil {
		return false, cleanupTemp(err, false)
	}
	if !auditStatesSemanticallyEqual(source, recovered) {
		return false, cleanupTemp(errors.New("audit checkpoint is not semantically equivalent to its source"), false)
	}
	if err := w.compaction.syncDirectory(w.directoryFile); err != nil {
		return false, cleanupTemp(fmt.Errorf("sync audit compaction directory entry: %w", err), false)
	}
	if err := ctx.Err(); err != nil {
		return false, cleanupTemp(err, true)
	}
	if err := w.verifyPaths(); err != nil {
		return false, cleanupTemp(fmt.Errorf("reverify audit paths before compaction rename: %w", err), false)
	}

	renameAttempted = true
	renameErr := w.compaction.rename(tempPath, w.filePath)
	if renameErr == nil {
		renamed = true
	} else {
		canonicalNamesTemp, identityErr := openFileNamesPath(temp, w.filePath, Filename)
		if identityErr != nil || !canonicalNamesTemp {
			return false, cleanupTemp(errors.Join(fmt.Errorf("replace audit journal: %w", renameErr), identityErr), false)
		}
		renamed = true
	}
	if err := verifyOpenNamedFile(temp, w.filePath, Filename); err != nil {
		return false, cleanupTemp(fmt.Errorf("verify replaced audit journal: %w", err), false)
	}

	old := w.file
	w.file = temp
	w.state = recovered
	w.journal = snapshot
	w.resetCompactionBoundary()
	adopted = true
	if err := w.compaction.syncDirectory(w.directoryFile); err != nil {
		_ = old.Close()
		return false, fmt.Errorf("sync replaced audit journal: %w", err)
	}
	committed = true

	// Once the rename is durable, the old inode can never become canonical
	// again. Truncating it releases blocks still pinned by stale Writer FDs; any
	// cleanup failure is maintenance-only and cannot turn a committed event into
	// a reported failure that callers might retry.
	_ = old.Truncate(0)
	_ = old.Sync()
	_ = old.Close()
	return true, nil
}

func writeAuditCheckpoint(ctx context.Context, file logFile, source *auditState, recordedAt time.Time) (*auditState, auditSnapshot, error) {
	boundary := checkpointBoundary{
		StoreID: source.storeID, Generation: source.generation + 1,
		LastSequence: source.lastSequence, PrunedThrough: source.prunedThrough,
		EventCount: uint64(len(source.events)),
	}
	begin, err := marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion, Kind: recordSnapshotBegin,
		RecordedAt: recordedAt, Checkpoint: &boundary,
	})
	if err != nil {
		return nil, auditSnapshot{}, err
	}
	hasher := sha256.New()
	if err := writeAll(file, begin); err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("write audit snapshot_begin: %w", err)
	}
	_, _ = hasher.Write(begin)
	for _, retained := range source.events {
		if err := ctx.Err(); err != nil {
			return nil, auditSnapshot{}, err
		}
		line, err := marshalAuditRecord(auditRecord{
			SchemaVersion: journalSchemaVersion, Kind: recordSnapshotEvent,
			RecordedAt: retained.recordedAt, Event: &retained.event,
		})
		if err != nil {
			return nil, auditSnapshot{}, err
		}
		if err := writeAll(file, line); err != nil {
			return nil, auditSnapshot{}, fmt.Errorf("write audit snapshot event: %w", err)
		}
		_, _ = hasher.Write(line)
	}
	endBoundary := boundary
	endBoundary.Digest = hex.EncodeToString(hasher.Sum(nil))
	end, err := marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion, Kind: recordSnapshotEnd,
		RecordedAt: recordedAt, Checkpoint: &endBoundary,
	})
	if err != nil {
		return nil, auditSnapshot{}, err
	}
	if err := writeAll(file, end); err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("write audit snapshot_end: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("sync audit checkpoint: %w", err)
	}
	recovered, snapshot, err := recoverAuditLog(file, false, source.storeID)
	if err != nil {
		return nil, auditSnapshot{}, fmt.Errorf("verify audit checkpoint: %w", err)
	}
	return recovered, snapshot, nil
}

func auditStatesSemanticallyEqual(source, checkpoint *auditState) bool {
	if source == nil || checkpoint == nil || checkpoint.storeID != source.storeID ||
		checkpoint.generation != source.generation+1 || checkpoint.lastSequence != source.lastSequence ||
		checkpoint.prunedThrough != source.prunedThrough || len(checkpoint.events) != len(source.events) {
		return false
	}
	for index := range source.events {
		if !source.events[index].recordedAt.Equal(checkpoint.events[index].recordedAt) ||
			!eventsSemanticallyEqual(source.events[index].event, checkpoint.events[index].event) {
			return false
		}
	}
	return true
}
