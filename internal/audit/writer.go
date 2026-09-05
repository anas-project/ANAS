// Package audit provides a fail-closed append-only audit log.
package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// Filename is the canonical audit journal managed by Writer.
	Filename     = "audit.jsonl"
	lockFilename = "audit.lock"

	// Redacted is written in place of credential-bearing audit values.
	Redacted = "<redacted>"
)

var (
	// ErrUnavailable means the writer cannot durably accept more events. Callers
	// must treat this as a reason to fail the security-sensitive operation.
	ErrUnavailable = errors.New("audit writer unavailable")
	// ErrInvalidEvent means an event could not be represented safely as JSON.
	ErrInvalidEvent = errors.New("invalid audit event")
	errWriterClosed = errors.New("audit writer is closed")
)

// Event is one structured, durable audit record. Sequence is always assigned
// by Writer. A non-zero Timestamp is preserved as the occurrence time; the
// independent retention clock is assigned by Writer at durable commit time.
type Event struct {
	Sequence    uint64         `json:"sequence"`
	Timestamp   time.Time      `json:"timestamp"`
	Type        string         `json:"type"`
	Actor       string         `json:"actor,omitempty"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Outcome     string         `json:"outcome,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

// Writer serializes appends within one process and uses a separate file lock to
// serialize writers across processes. It deliberately does not hide an I/O or
// path-integrity failure: after one occurs, the instance stays unavailable so
// callers cannot accidentally continue without durable audit coverage.
type Writer struct {
	gate                chan struct{}
	done                chan struct{}
	closeSignal         sync.Once
	directory           string
	filePath            string
	lockPath            string
	directoryFile       *os.File
	file                logFile
	lockFile            *os.File
	options             Options
	state               *auditState
	journal             auditSnapshot
	compaction          auditCompactionOperations
	nextCompactionBytes int64
	now                 func() time.Time
	unavailable         error
	closed              bool
}

// Open opens (or creates) the append-only log in dir. The final directory must
// be a non-symlink directory owned by the effective user with mode 0700. The
// log and lock must be single-link, non-symlink regular files owned by the
// effective user with mode 0600. Writer pins all three opened identities and
// fails closed if their path entries are later replaced.
func Open(dir string) (*Writer, error) {
	return OpenWithOptions(dir, Options{})
}

// OpenWithOptions opens the audit journal with an independent retention and
// compaction policy. All cooperating writers for one directory should use the
// same policy; only audit.lock serializes changes to that policy's state.
func OpenWithOptions(dir string, options Options) (*Writer, error) {
	resolved, err := options.withDefaults()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, fmt.Errorf("open audit log: directory is empty")
	}
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("open audit log: resolve directory: %w", err)
	}
	dir = filepath.Clean(absoluteDir)
	directoryFile, createdDirectoryEntries, err := openSecureDirectory(dir)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	closeDirectoryOnError := func(err error) (*Writer, error) {
		_ = directoryFile.Close()
		return nil, fmt.Errorf("open audit log: %w", err)
	}
	if len(createdDirectoryEntries) != 0 {
		if err := directoryFile.Sync(); err != nil {
			return closeDirectoryOnError(fmt.Errorf("sync directory metadata: %w", err))
		}
		if err := syncCreatedDirectoryEntries(createdDirectoryEntries); err != nil {
			return closeDirectoryOnError(err)
		}
	}

	lockPath := filepath.Join(dir, lockFilename)
	lockFile, lockCreated, err := openSecureLockFile(lockPath, lockFilename)
	if err != nil {
		return closeDirectoryOnError(err)
	}
	closeLockOnError := func(err error) (*Writer, error) {
		_ = lockFile.Close()
		return closeDirectoryOnError(err)
	}
	if lockCreated {
		if err := directoryFile.Sync(); err != nil {
			return closeLockOnError(fmt.Errorf("sync new %s directory entry: %w", lockFilename, err))
		}
	}
	lockContext, cancelLock := context.WithTimeout(context.Background(), resolved.LockTimeout)
	defer cancelLock()
	if err := lockAuditFile(lockContext, nil, lockFile); err != nil {
		return closeLockOnError(fmt.Errorf("lock audit log: %w", err))
	}
	locked := true
	unlockOnError := func(err error) (*Writer, error) {
		if locked {
			_ = unlockAuditFile(lockFile)
			locked = false
		}
		return closeLockOnError(err)
	}
	if err := verifyOpenDirectory(directoryFile, dir); err != nil {
		return unlockOnError(err)
	}
	if err := verifyOpenNamedFile(lockFile, lockPath, lockFilename); err != nil {
		return unlockOnError(err)
	}
	metadataDisk, metadataReadError := readLockMetadataDiskState(lockFile)
	resetTornInitialMetadata := errors.Is(metadataReadError, errNoValidInitialLockMetadata)
	if metadataReadError != nil && !resetTornInitialMetadata {
		return unlockOnError(metadataReadError)
	}
	metadata := metadataDisk.metadata
	retention := retentionPolicyForOptions(resolved)
	if metadataReadError == nil && metadata.Retention != nil && !retentionPoliciesEqual(metadata.Retention, retention) {
		return unlockOnError(errors.New("audit retention policy does not match the fixed lock metadata"))
	}
	compaction := defaultAuditCompactionOperations()
	if err := cleanupStaleCompactionFile(directoryFile, filepath.Join(dir, CompactionFilename), compaction); err != nil {
		return unlockOnError(err)
	}

	path := filepath.Join(dir, Filename)
	if metadata.StoreID != "" || resetTornInitialMetadata {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			if resetTornInitialMetadata {
				return unlockOnError(fmt.Errorf("%w: audit journal is missing", metadataReadError))
			}
			return unlockOnError(errors.New("initialized audit journal is missing"))
		} else if err != nil {
			return unlockOnError(fmt.Errorf("inspect initialized %s: %w", Filename, err))
		}
	}
	file, fileCreated, err := openSecureFile(path)
	if err != nil {
		return unlockOnError(err)
	}
	closeFileOnError := func(err error) (*Writer, error) {
		_ = file.Close()
		return unlockOnError(err)
	}
	if fileCreated {
		if metadata.StoreID != "" || resetTornInitialMetadata {
			return closeFileOnError(errors.New("initialized audit journal was replaced while opening"))
		}
		if err := directoryFile.Sync(); err != nil {
			return closeFileOnError(fmt.Errorf("sync new %s directory entry: %w", Filename, err))
		}
	}
	initialShape, partialInitialRecord, err := classifyInitialAuditJournal(file)
	if err != nil {
		return closeFileOnError(err)
	}
	pristineChecksummedMetadata := metadataDisk.revision > 0 && !metadataDisk.legacy && lockMetadataIsPristine(metadata)
	switch {
	case resetTornInitialMetadata:
		if fileCreated || (initialShape != initialJournalEmpty && initialShape != initialJournalLegacy) {
			return closeFileOnError(metadataReadError)
		}
	case metadata.StoreID == "":
		if initialShape == initialJournalEnvelope ||
			(initialShape == initialJournalPartial && !isIdentifiableLegacyEventPrefix(partialInitialRecord)) {
			return closeFileOnError(errors.New("audit lock metadata is missing for an initialized or ambiguous journal"))
		}
	case pristineChecksummedMetadata:
		if initialShape == initialJournalLegacy {
			return closeFileOnError(errors.New("pristine audit lock metadata cannot initialize a legacy journal"))
		}
		if initialShape == initialJournalPartial && !isPossibleAuditHeaderPrefix(partialInitialRecord, metadata.StoreID) {
			return closeFileOnError(errors.New("partial audit header does not match the pristine lock metadata"))
		}
	case initialShape == initialJournalPartial:
		return closeFileOnError(errors.New("initialized audit journal has no complete record"))
	}

	expectedStoreID := metadata.StoreID
	if pristineChecksummedMetadata && (initialShape == initialJournalEmpty || initialShape == initialJournalPartial) {
		// A durable pristine slot is the authority for the initialization
		// window in which the header is still empty or incomplete.
		expectedStoreID = ""
	}
	state, snapshot, err := recoverAuditLog(file, true, expectedStoreID)
	if err != nil {
		return closeFileOnError(err)
	}
	if metadata.StoreID == "" && snapshot.completeBytes > 0 && !state.legacy {
		return closeFileOnError(errors.New("audit lock metadata is missing for an initialized journal"))
	}
	if metadata.StoreID != "" && state.storeID == "" {
		if metadataDisk.revision == 0 || metadataDisk.legacy || !lockMetadataIsPristine(metadata) {
			return closeFileOnError(errors.New("initialized audit journal is empty"))
		}
		state.storeID = metadata.StoreID
	}
	if state.storeID == "" {
		state.storeID, err = newStoreID()
		if err != nil {
			return closeFileOnError(err)
		}
	}
	if resetTornInitialMetadata && snapshot.completeBytes > 0 {
		initialMetadata := lockMetadataForState(state, retention)
		if err := replaceTornInitialLockMetadata(lockFile, directoryFile, initialMetadata); err != nil {
			return closeFileOnError(fmt.Errorf("replace torn initial legacy audit metadata: %w", err))
		}
		metadata = initialMetadata
		resetTornInitialMetadata = false
	}
	if snapshot.completeBytes == 0 {
		initialMetadata := lockMetadataForState(state, retention)
		if resetTornInitialMetadata {
			if err := replaceTornInitialLockMetadata(lockFile, directoryFile, initialMetadata); err != nil {
				return closeFileOnError(fmt.Errorf("replace torn initial audit lock metadata: %w", err))
			}
			metadata = initialMetadata
			resetTornInitialMetadata = false
		} else if !lockMetadataMatchesState(metadata, state, retention) {
			if err := writeLockMetadata(lockFile, directoryFile, initialMetadata); err != nil {
				return closeFileOnError(fmt.Errorf("initialize audit lock metadata: %w", err))
			}
			metadata = initialMetadata
		}
		recordedAt := time.Now().UTC()
		header, err := marshalAuditRecord(auditRecord{
			SchemaVersion: journalSchemaVersion,
			Kind:          recordHeader,
			RecordedAt:    recordedAt,
			StoreID:       state.storeID,
		})
		if err != nil {
			return closeFileOnError(fmt.Errorf("encode audit header: %w", err))
		}
		if err := writeAll(file, header); err != nil {
			return closeFileOnError(fmt.Errorf("write audit header: %w", err))
		}
		if err := file.Sync(); err != nil {
			return closeFileOnError(fmt.Errorf("sync audit header: %w", err))
		}
		if err := directoryFile.Sync(); err != nil {
			return closeFileOnError(fmt.Errorf("sync audit header directory entry: %w", err))
		}
		state, snapshot, err = recoverAuditLog(file, false, state.storeID)
		if err != nil {
			return closeFileOnError(fmt.Errorf("verify audit header: %w", err))
		}
	}
	if metadata.StoreID != "" {
		if err := auditStateCoversLockMetadata(state, metadata); err != nil {
			return closeFileOnError(err)
		}
	}
	desiredMetadata := lockMetadataForState(state, retention)
	if !lockMetadataMatchesState(metadata, state, retention) {
		if err := writeLockMetadata(lockFile, directoryFile, desiredMetadata); err != nil {
			return closeFileOnError(err)
		}
	}
	writer := &Writer{
		gate:          make(chan struct{}, 1),
		done:          make(chan struct{}),
		directory:     dir,
		filePath:      path,
		lockPath:      lockPath,
		directoryFile: directoryFile,
		file:          file,
		lockFile:      lockFile,
		options:       resolved,
		state:         state,
		journal:       snapshot,
		compaction:    compaction,
		now:           time.Now,
	}
	writer.gate <- struct{}{}
	writer.resetCompactionBoundary()
	if err := writer.verifyPaths(); err != nil {
		return closeFileOnError(err)
	}
	if err := unlockAuditFile(lockFile); err != nil {
		return closeFileOnError(fmt.Errorf("unlock audit log: %w", err))
	}
	locked = false
	return writer, nil
}

// missingDirectoryEntries returns the currently absent path suffix in
// root-to-leaf order. Every returned directory entry must later be synced in
// its parent before an append can claim durable success.

func openSecureFile(path string) (*os.File, bool, error) {
	return openSecureNamedFile(path, Filename)
}

func validateLogFileInfo(info os.FileInfo) error {
	return validateSecureFileInfo(info, Filename)
}

// Append redacts and durably appends event. It uses an internal deadline so a
// wedged peer cannot block the process forever on the cross-process lock.
func (w *Writer) Append(event Event) (Event, error) {
	timeout := DefaultLockTimeout
	if w != nil && w.options.LockTimeout > 0 {
		timeout = w.options.LockTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return w.AppendContext(ctx, event)
}

// List returns a defensive copy of the retained events in increasing sequence
// order. It refreshes the canonical journal under audit.lock, so queries never
// bypass lineage, replacement, or corruption checks performed by Writer.
func (w *Writer) List(ctx context.Context) ([]Event, error) {
	if w == nil {
		return nil, ErrUnavailable
	}
	if ctx == nil {
		return nil, &PersistenceError{Operation: "query audit events", Cause: errors.New("context is nil")}
	}
	if err := w.acquire(ctx); err != nil {
		return nil, &PersistenceError{Operation: "query audit events", Cause: err}
	}
	defer w.release()
	if w.closed || w.file == nil || w.lockFile == nil {
		return nil, ErrUnavailable
	}
	if w.unavailable != nil {
		return nil, w.markUnavailable(w.unavailable)
	}
	if err := lockAuditFile(ctx, w.done, w.lockFile); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errWriterClosed) {
			return nil, &PersistenceError{Operation: "query audit events", Cause: err}
		}
		return nil, w.markUnavailable(fmt.Errorf("acquire cross-process lock for audit query: %w", err))
	}
	locked := true
	defer func() {
		if locked {
			_ = unlockAuditFile(w.lockFile)
		}
	}()
	if err := w.refreshLocked(); err != nil {
		return nil, w.markUnavailable(fmt.Errorf("refresh audit log before query: %w", err))
	}
	if err := ctx.Err(); err != nil {
		if unlockErr := unlockAuditFile(w.lockFile); unlockErr != nil {
			locked = false
			return nil, w.markUnavailable(fmt.Errorf("release interrupted audit query lock: %w", unlockErr))
		}
		locked = false
		return nil, &PersistenceError{Operation: "query audit events", Cause: err}
	}
	events := make([]Event, 0, len(w.state.events))
	for _, stored := range w.state.events {
		cloned, err := sanitizeEvent(stored.event)
		if err != nil {
			return nil, w.markUnavailable(fmt.Errorf("clone retained audit event: %w", err))
		}
		events = append(events, cloned)
	}
	if err := unlockAuditFile(w.lockFile); err != nil {
		locked = false
		return nil, w.markUnavailable(fmt.Errorf("release cross-process lock after audit query: %w", err))
	}
	locked = false
	return events, nil
}

// AppendContext is Append with caller-controlled cancellation. Cancellation
// while waiting for the in-process or cross-process lock fails this operation
// closed without poisoning an otherwise healthy Writer.
func (w *Writer) AppendContext(ctx context.Context, event Event) (Event, error) {
	if w == nil {
		return Event{}, ErrUnavailable
	}
	if ctx == nil {
		return Event{}, fmt.Errorf("%w: context is nil", ErrUnavailable)
	}
	if err := w.acquire(ctx); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer w.release()
	if err := ctx.Err(); err != nil {
		return Event{}, &PersistenceError{Operation: "append audit event", Cause: err}
	}

	if w.closed || w.file == nil {
		return Event{}, ErrUnavailable
	}
	if w.unavailable != nil {
		return Event{}, w.markUnavailable(w.unavailable)
	}
	persisted, err := prepareAuditEvent(event)
	if err != nil {
		return Event{}, err
	}
	if w.lockFile == nil {
		return Event{}, w.markUnavailable(errors.New("cross-process lock is unavailable"))
	}
	if err := lockAuditFile(ctx, w.done, w.lockFile); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errWriterClosed) {
			return Event{}, &PersistenceError{Operation: "acquire audit lock", Cause: err}
		}
		return Event{}, w.markUnavailable(fmt.Errorf("acquire cross-process lock: %w", err))
	}
	locked := true
	unlockAfterFailure := func() {
		if locked {
			_ = unlockAuditFile(w.lockFile)
			locked = false
		}
	}
	if err := w.refreshLocked(); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("refresh audit log after lock: %w", err))
	}
	interruptAfterLock := func(interruptErr error) (Event, error) {
		if err := unlockAuditFile(w.lockFile); err != nil {
			locked = false
			return Event{}, w.markUnavailable(fmt.Errorf("release interrupted cross-process lock: %w", err))
		}
		locked = false
		return Event{}, &PersistenceError{Operation: "append audit event", Cause: interruptErr}
	}
	if err := ctx.Err(); err != nil {
		return interruptAfterLock(err)
	}
	recordedAt, err := w.commitTime()
	if err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(err)
	}
	if err := ctx.Err(); err != nil {
		return interruptAfterLock(err)
	}
	if w.state.lastSequence == ^uint64(0) {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("sequence space exhausted"))
	}
	persisted.Sequence = w.state.lastSequence + 1
	if persisted.Timestamp.IsZero() {
		persisted.Timestamp = recordedAt
	} else {
		persisted.Timestamp = persisted.Timestamp.UTC()
	}
	preview := w.state.clone()
	preview.events = append(preview.events, storedEvent{event: persisted, recordedAt: recordedAt})
	preview.lastSequence = persisted.Sequence
	preview.lastRecordedAt = recordedAt
	if err := applyRetentionPolicy(preview, recordedAt, w.options); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("apply audit retention: %w", err))
	}
	record := auditRecord{
		SchemaVersion: journalSchemaVersion,
		Kind:          recordEvent,
		RecordedAt:    recordedAt,
		Event:         &persisted,
		PrunedThrough: preview.prunedThrough,
	}
	line, err := marshalAuditRecord(record)
	if err != nil {
		unlockAfterFailure()
		if errors.Is(err, errRecordTooLarge) {
			return Event{}, fmt.Errorf("%w: encoded audit event exceeds %d bytes", ErrInvalidEvent, maximumRecordBytes)
		}
		return Event{}, fmt.Errorf("%w: event cannot be encoded", ErrInvalidEvent)
	}
	decoded, err := decodeAuditRecord(line[:len(line)-1])
	if err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("verify encoded audit event: %w", err))
	}
	prospective := w.state.clone()
	if err := applyEventRecord(prospective, decoded); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("apply encoded audit event: %w", err))
	}
	projected := w.journal.completeBytes
	if int64(len(line)) > int64(^uint64(0)>>1)-projected {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(errors.New("audit journal size overflows int64"))
	}
	projected += int64(len(line))
	compact := w.state.legacy
	evaluated := compact
	if !compact {
		compact, evaluated, err = w.shouldCompactBeforeAppend(prospective, projected, recordedAt)
		if err != nil {
			unlockAfterFailure()
			return Event{}, w.markUnavailable(fmt.Errorf("evaluate audit compaction: %w", err))
		}
	}
	if compact {
		committed, err := w.compactStateLocked(ctx, prospective, recordedAt)
		if err != nil {
			if committed {
				// compactStateLocked currently suppresses post-commit cleanup errors;
				// retain this guard so a future maintenance hook cannot cause retry.
				err = nil
			} else if isCleanCompactionCancellation(err) {
				return interruptAfterLock(err)
			} else {
				unlockAfterFailure()
				return Event{}, w.markUnavailable(fmt.Errorf("compact prospective audit event: %w", err))
			}
		}
		if err := w.persistLockMetadataLocked(w.state); err != nil {
			unlockAfterFailure()
			return Event{}, w.markUnavailable(fmt.Errorf("persist audit watermark after compaction: %w", err))
		}
		if err := unlockAuditFile(w.lockFile); err != nil {
			locked = false
			return Event{}, w.markUnavailable(fmt.Errorf("release cross-process lock: %w", err))
		}
		locked = false
		return persisted, nil
	}
	if err := ctx.Err(); err != nil {
		return interruptAfterLock(err)
	}
	if err := writeAll(w.file, line); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("append record: %w", err))
	}
	if err := w.file.Sync(); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("sync record: %w", err))
	}
	// Syncing the containing directory is cheap relative to the mandatory file
	// sync and covers the first record after a newly installed log entry.
	if w.directoryFile == nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(errors.New("audit directory descriptor is unavailable"))
	}
	if err := w.directoryFile.Sync(); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("sync audit directory: %w", err))
	}
	if err := w.verifyPaths(); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("reverify audit paths after sync: %w", err))
	}
	snapshot, err := snapshotAuditLog(w.file)
	if err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("snapshot appended audit log: %w", err))
	}
	if err := w.persistLockMetadataLocked(prospective); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("persist appended audit watermark: %w", err))
	}
	if err := unlockAuditFile(w.lockFile); err != nil {
		locked = false
		return Event{}, w.markUnavailable(fmt.Errorf("release cross-process lock: %w", err))
	}
	locked = false
	w.state = prospective
	w.journal = snapshot
	if evaluated {
		w.advanceCompactionBoundary(snapshot.completeBytes)
	}
	return persisted, nil
}

func prepareAuditEvent(event Event) (Event, error) {
	if event.Sequence != 0 {
		return Event{}, fmt.Errorf("%w: sequence is assigned by the writer", ErrInvalidEvent)
	}
	if event.Type == "" {
		return Event{}, fmt.Errorf("%w: type is required", ErrInvalidEvent)
	}
	persisted, err := sanitizeEvent(event)
	if err != nil {
		return Event{}, fmt.Errorf("%w: event contains an unsupported value", ErrInvalidEvent)
	}
	probe := persisted
	probe.Sequence = ^uint64(0)
	if probe.Timestamp.IsZero() {
		probe.Timestamp = time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC)
	}
	_, err = marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion,
		Kind:          recordEvent,
		RecordedAt:    time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.UTC),
		Event:         &probe,
		PrunedThrough: ^uint64(0) - 1,
	})
	if errors.Is(err, errRecordTooLarge) {
		return Event{}, fmt.Errorf("%w: encoded audit event exceeds %d bytes", ErrInvalidEvent, maximumRecordBytes)
	}
	if err != nil {
		return Event{}, fmt.Errorf("%w: event cannot be encoded", ErrInvalidEvent)
	}
	return persisted, nil
}

func (w *Writer) acquire(ctx context.Context) error {
	select {
	case <-w.done:
		return errWriterClosed
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		return errWriterClosed
	case <-w.gate:
	}
	select {
	case <-w.done:
		w.release()
		return errWriterClosed
	default:
		return nil
	}
}

func (w *Writer) release() {
	w.gate <- struct{}{}
}

func (w *Writer) verifyPaths() error {
	if err := w.verifyStablePaths(); err != nil {
		return err
	}
	return verifyOpenNamedFile(w.file, w.filePath, Filename)
}

func (w *Writer) verifyStablePaths() error {
	if err := verifyOpenDirectory(w.directoryFile, w.directory); err != nil {
		return err
	}
	return verifyOpenNamedFile(w.lockFile, w.lockPath, lockFilename)
}

func (w *Writer) readVerifiedLockMetadataLocked() (lockMetadata, error) {
	metadata, err := readLockMetadata(w.lockFile)
	if err != nil {
		return lockMetadata{}, err
	}
	if metadata.StoreID == "" || w.state == nil || metadata.StoreID != w.state.storeID {
		return lockMetadata{}, errors.New("audit lock metadata belongs to a different store")
	}
	if !retentionPoliciesEqual(metadata.Retention, retentionPolicyForOptions(w.options)) {
		return lockMetadata{}, errors.New("audit retention policy changed for an open Writer")
	}
	return metadata, nil
}

func (w *Writer) persistLockMetadataLocked(state *auditState) error {
	if state == nil || state.storeID == "" {
		return errors.New("cannot persist audit metadata for an uninitialized state")
	}
	return writeLockMetadata(w.lockFile, w.directoryFile,
		lockMetadataForState(state, retentionPolicyForOptions(w.options)))
}

// refreshLocked adopts a sealed generation installed by another Writer and
// otherwise refreshes unknown same-inode growth. Callers hold audit.lock.
func (w *Writer) refreshLocked() error {
	if err := w.verifyStablePaths(); err != nil {
		return err
	}
	metadata, err := w.readVerifiedLockMetadataLocked()
	if err != nil {
		return err
	}
	if err := w.ensureCanonicalLocked(); err != nil {
		return err
	}
	info, err := w.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", Filename, err)
	}
	if w.journal.matches(info) {
		if err := auditStateCoversLockMetadata(w.state, metadata); err != nil {
			return err
		}
		return w.verifyPaths()
	}
	recovered, snapshot, err := recoverAuditLog(w.file, true, w.state.storeID)
	if err != nil {
		return err
	}
	if recovered.generation != w.state.generation {
		return errors.New("audit generation changed without replacing the canonical inode")
	}
	if err := stateContinues(w.state, recovered); err != nil {
		return err
	}
	w.state = recovered
	w.journal = snapshot
	w.resetCompactionBoundary()
	if err := auditStateCoversLockMetadata(w.state, metadata); err != nil {
		return err
	}
	return w.verifyPaths()
}

func (w *Writer) ensureCanonicalLocked() error {
	opened, err := w.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", Filename, err)
	}
	current, err := os.Lstat(w.filePath)
	if err != nil {
		return fmt.Errorf("inspect canonical %s: %w", Filename, err)
	}
	if err := validateSecureFileInfo(current, Filename); err != nil {
		return err
	}
	if os.SameFile(opened, current) {
		return verifyOpenNamedFile(w.file, w.filePath, Filename)
	}

	replacement, err := openExistingSecureNamedFile(w.filePath, Filename)
	if err != nil {
		return err
	}
	closeReplacement := func(cause error) error {
		return errors.Join(cause, replacement.Close())
	}
	fresh, snapshot, err := recoverAuditLog(replacement, true, w.state.storeID)
	if err != nil {
		return closeReplacement(fmt.Errorf("recover replacement audit journal: %w", err))
	}
	if err := stateAdvances(w.state, fresh); err != nil {
		return closeReplacement(err)
	}
	if err := verifyOpenNamedFile(replacement, w.filePath, Filename); err != nil {
		return closeReplacement(err)
	}
	old := w.file
	w.file = replacement
	w.state = fresh
	w.journal = snapshot
	w.resetCompactionBoundary()
	if err := old.Close(); err != nil {
		return fmt.Errorf("close superseded audit journal: %w", err)
	}
	return nil
}

func (w *Writer) commitTime() (time.Time, error) {
	recordedAt := w.now().UTC()
	if recordedAt.IsZero() {
		return time.Time{}, errors.New("audit commit clock returned zero time")
	}
	if !w.state.lastRecordedAt.IsZero() && recordedAt.Before(w.state.lastRecordedAt) {
		recordedAt = w.state.lastRecordedAt
	}
	return recordedAt, nil
}

func (w *Writer) verifyPathsForClose() error {
	if err := w.verifyStablePaths(); err != nil {
		return err
	}
	locked, err := tryAuditLock(w.lockFile)
	if err != nil {
		return fmt.Errorf("try audit lock before close: %w", err)
	}
	if !locked {
		// A supported peer may be between rename and descriptor adoption. The
		// peer owns path verification while holding audit.lock.
		return nil
	}
	var verifyErr error
	if err := w.verifyStablePaths(); err != nil {
		verifyErr = err
	} else if err := w.ensureCanonicalLocked(); err != nil {
		verifyErr = err
	} else {
		verifyErr = w.verifyPaths()
	}
	return errors.Join(verifyErr, unlockAuditFile(w.lockFile))
}

func (w *Writer) markUnavailable(err error) error {
	if w.unavailable == nil {
		w.unavailable = err
	}
	return &PersistenceError{Operation: "audit writer failed closed", Cause: w.unavailable}
}

// Close cancels any append waiting on the cross-process lock, verifies that the
// pinned paths were not replaced, and closes all descriptors.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.closeSignal.Do(func() { close(w.done) })
	<-w.gate
	defer w.release()
	if w.closed {
		return nil
	}
	w.closed = true
	var integrityErr error
	if err := w.verifyPathsForClose(); err != nil {
		integrityErr = w.markUnavailable(fmt.Errorf("verify audit paths before close: %w", err))
	}
	var fileErr error
	if w.file != nil {
		fileErr = w.file.Close()
	}
	var lockErr error
	if w.lockFile != nil {
		lockErr = w.lockFile.Close()
	}
	var directoryErr error
	if w.directoryFile != nil {
		directoryErr = w.directoryFile.Close()
	}
	return errors.Join(integrityErr, fileErr, lockErr, directoryErr)
}
