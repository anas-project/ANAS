// Package audit provides a fail-closed append-only audit log.
package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// Filename is the only file managed by Writer inside its state directory.
	Filename     = "audit.jsonl"
	lockFilename = "audit.lock"

	defaultAppendTimeout = 30 * time.Second
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

// Event is one structured, durable audit record. Sequence and Timestamp are
// assigned by Writer; callers supply the remaining fields.
type Event struct {
	Sequence    uint64         `json:"sequence"`
	Timestamp   time.Time      `json:"timestamp"`
	Type        string         `json:"type"`
	Actor       string         `json:"actor,omitempty"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Outcome     string         `json:"outcome,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
}

type logFile interface {
	io.Reader
	io.Writer
	io.Seeker
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(int64) error
	Close() error
}

// Writer serializes appends within one process and uses a separate file lock to
// serialize writers across processes. It deliberately does not hide an I/O or
// path-integrity failure: after one occurs, the instance stays unavailable so
// callers cannot accidentally continue without durable audit coverage.
type Writer struct {
	gate          chan struct{}
	done          chan struct{}
	closeSignal   sync.Once
	directory     string
	filePath      string
	lockPath      string
	directoryFile *os.File
	file          logFile
	lockFile      *os.File
	lastSequence  uint64
	now           func() time.Time
	unavailable   error
	closed        bool
}

// Open opens (or creates) the append-only log in dir. The final directory must
// be a non-symlink directory owned by the effective user with mode 0700. The
// log and lock must be single-link, non-symlink regular files owned by the
// effective user with mode 0600. Writer pins all three opened identities and
// fails closed if their path entries are later replaced.
func Open(dir string) (*Writer, error) {
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
	lockFile, lockCreated, err := openSecureNamedFile(lockPath, lockFilename)
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
	lockContext, cancelLock := context.WithTimeout(context.Background(), defaultAppendTimeout)
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

	path := filepath.Join(dir, Filename)
	file, fileCreated, err := openSecureFile(path)
	if err != nil {
		return unlockOnError(err)
	}
	closeFileOnError := func(err error) (*Writer, error) {
		_ = file.Close()
		return unlockOnError(err)
	}
	if fileCreated {
		if err := directoryFile.Sync(); err != nil {
			return closeFileOnError(fmt.Errorf("sync new %s directory entry: %w", Filename, err))
		}
	}

	lastSequence, err := recoverLog(file)
	if err != nil {
		return closeFileOnError(err)
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
		lastSequence:  lastSequence,
		now:           time.Now,
	}
	writer.gate <- struct{}{}
	if err := writer.verifyPaths(); err != nil {
		return closeFileOnError(err)
	}
	if err := unlockAuditFile(lockFile); err != nil {
		return closeFileOnError(fmt.Errorf("unlock audit log: %w", err))
	}
	locked = false
	return writer, nil
}

func openSecureDirectory(dir string) (*os.File, []string, error) {
	info, err := os.Lstat(dir)
	created := false
	var createdEntries []string
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(dir)
		createdEntries, err = missingDirectoryEntries(parent)
		if err != nil {
			return nil, nil, err
		}
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create directory parent: %w", err)
		}
		if err := os.Mkdir(dir, 0o700); err == nil {
			created = true
		} else if !errors.Is(err, os.ErrExist) {
			return nil, nil, fmt.Errorf("create directory: %w", err)
		}
		// The final entry was absent when creation began. Sync it even when a
		// concurrent secure opener won the Mkdir race.
		createdEntries = append(createdEntries, dir)
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect directory: %w", err)
	}
	if created {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, nil, errors.New("directory must be a non-symlink directory")
		}
		if err := validateCurrentOwner(info, "directory"); err != nil {
			return nil, nil, err
		}
	} else if err := validateSecureDirectoryInfo(info); err != nil {
		return nil, nil, err
	}

	directoryFile, err := os.Open(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("open directory: %w", err)
	}
	closeOnError := func(err error) (*os.File, []string, error) {
		_ = directoryFile.Close()
		return nil, nil, err
	}
	if created {
		// Apply the exact mode through the descriptor we just created, never
		// through a path that another process may have replaced.
		if err := directoryFile.Chmod(0o700); err != nil {
			return closeOnError(fmt.Errorf("secure directory: %w", err))
		}
	}
	if err := verifyOpenDirectory(directoryFile, dir); err != nil {
		return closeOnError(err)
	}
	return directoryFile, createdEntries, nil
}

// missingDirectoryEntries returns the currently absent path suffix in
// root-to-leaf order. Every returned directory entry must later be synced in
// its parent before an append can claim durable success.
func missingDirectoryEntries(path string) ([]string, error) {
	var leafToRoot []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect directory parent: %w", err)
		}
		leafToRoot = append(leafToRoot, path)
		parent := filepath.Dir(path)
		if parent == path {
			return nil, fmt.Errorf("no existing ancestor for directory %s", path)
		}
		path = parent
	}
	entries := make([]string, len(leafToRoot))
	for index := range leafToRoot {
		entries[len(leafToRoot)-1-index] = leafToRoot[index]
	}
	return entries, nil
}

func openSecureFile(path string) (*os.File, bool, error) {
	return openSecureNamedFile(path, Filename)
}

func openSecureNamedFile(path, name string) (*os.File, bool, error) {
	for {
		info, err := os.Lstat(path)
		created := false
		switch {
		case errors.Is(err, os.ErrNotExist):
			created = true
		case err != nil:
			return nil, false, fmt.Errorf("inspect %s: %w", name, err)
		default:
			if err := validateSecureFileInfo(info, name); err != nil {
				return nil, false, err
			}
		}

		flags := os.O_RDWR | os.O_APPEND
		if created {
			flags |= os.O_CREATE | os.O_EXCL
		}
		file, err := os.OpenFile(path, flags, 0o600)
		if created && errors.Is(err, os.ErrExist) {
			// Another secure opener won the create race. Reinspect its result
			// rather than weakening O_EXCL or trusting the raced path.
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("open %s: %w", name, err)
		}
		closeOnError := func(err error) (*os.File, bool, error) {
			_ = file.Close()
			return nil, false, err
		}
		if created {
			if err := file.Chmod(0o600); err != nil {
				return closeOnError(fmt.Errorf("secure %s: %w", name, err))
			}
		}
		if err := verifyOpenNamedFile(file, path, name); err != nil {
			return closeOnError(err)
		}
		if created {
			if err := file.Sync(); err != nil {
				return closeOnError(fmt.Errorf("sync new %s: %w", name, err))
			}
		}
		return file, created, nil
	}
}

func validateLogFileInfo(info os.FileInfo) error {
	return validateSecureFileInfo(info, Filename)
}

func validateSecureFileInfo(info os.FileInfo, name string) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a non-symlink regular file", name)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s mode is %04o, want 0600", name, info.Mode().Perm())
	}
	if err := validateCurrentOwner(info, name); err != nil {
		return err
	}
	if err := validateSingleLink(info, name); err != nil {
		return err
	}
	return nil
}

func validateSecureDirectoryInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("directory must be a non-symlink directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("directory mode is %04o, want 0700", info.Mode().Perm())
	}
	return validateCurrentOwner(info, "directory")
}

func verifyOpenDirectory(directoryFile *os.File, path string) error {
	if directoryFile == nil {
		return errors.New("audit directory descriptor is unavailable")
	}
	openedInfo, err := directoryFile.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened directory: %w", err)
	}
	if err := validateSecureDirectoryInfo(openedInfo); err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect directory: %w", err)
	}
	if err := validateSecureDirectoryInfo(pathInfo); err != nil {
		return err
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return errors.New("audit directory changed while it was open")
	}
	return nil
}

func verifyOpenNamedFile(file logFile, path, name string) error {
	if file == nil {
		return fmt.Errorf("%s descriptor is unavailable", name)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", name, err)
	}
	if err := validateSecureFileInfo(openedInfo, name); err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect %s: %w", name, err)
	}
	if err := validateSecureFileInfo(pathInfo, name); err != nil {
		return err
	}
	if !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("%s path entry no longer names the opened file", name)
	}
	return nil
}

func syncCreatedDirectoryEntries(entries []string) error {
	// Work leaf-to-root: sync each new entry in its containing directory,
	// including every parent that MkdirAll had to create.
	for index := len(entries) - 1; index >= 0; index-- {
		if err := syncParentDirectory(entries[index]); err != nil {
			return err
		}
	}
	return nil
}

func syncParentDirectory(path string) error {
	parentPath := filepath.Dir(path)
	parent, err := os.Open(parentPath)
	if err != nil {
		return fmt.Errorf("open parent directory for sync: %w", err)
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil {
		return fmt.Errorf("sync parent directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close parent directory after sync: %w", closeErr)
	}
	return nil
}

func recoverLog(file logFile) (uint64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek to start: %w", err)
	}
	reader := bufio.NewReader(file)
	var (
		completeBytes int64
		lastSequence  uint64
		lineNumber    int
	)
	for {
		line, err := reader.ReadBytes('\n')
		if err == nil {
			lineNumber++
			completeBytes += int64(len(line))
			event, decodeErr := decodeLine(bytes.TrimSuffix(line, []byte{'\n'}))
			if decodeErr != nil {
				return 0, fmt.Errorf("validate line %d: %w", lineNumber, decodeErr)
			}
			want := lastSequence + 1
			if event.Sequence != want {
				return 0, fmt.Errorf("validate line %d: sequence is %d, want %d", lineNumber, event.Sequence, want)
			}
			lastSequence = event.Sequence
			continue
		}
		if !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read log: %w", err)
		}
		if len(line) != 0 {
			// A record is durable and valid only after its terminating newline.
			// The sole recovery exception is therefore the non-newline residual
			// tail left by an interrupted append.
			if err := file.Truncate(completeBytes); err != nil {
				return 0, fmt.Errorf("truncate incomplete tail: %w", err)
			}
			if err := file.Sync(); err != nil {
				return 0, fmt.Errorf("sync truncated log: %w", err)
			}
		}
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			return 0, fmt.Errorf("seek to end: %w", err)
		}
		return lastSequence, nil
	}
}

func decodeLine(line []byte) (Event, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		return Event{}, fmt.Errorf("empty JSON line")
	}
	var event Event
	if err := json.Unmarshal(line, &event); err != nil {
		return Event{}, fmt.Errorf("invalid JSON")
	}
	if event.Sequence == 0 {
		return Event{}, fmt.Errorf("sequence must be positive")
	}
	if event.Timestamp.IsZero() {
		return Event{}, fmt.Errorf("timestamp is required")
	}
	if event.Type == "" {
		return Event{}, fmt.Errorf("type is required")
	}
	return event, nil
}

// Append redacts and durably appends event. It uses an internal deadline so a
// wedged peer cannot block the process forever on the cross-process lock.
func (w *Writer) Append(event Event) (Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultAppendTimeout)
	defer cancel()
	return w.AppendContext(ctx, event)
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
		return Event{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	if w.closed || w.file == nil {
		return Event{}, ErrUnavailable
	}
	if w.unavailable != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrUnavailable, w.unavailable)
	}
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
	if w.lockFile == nil {
		return Event{}, w.markUnavailable(errors.New("cross-process lock is unavailable"))
	}
	if err := lockAuditFile(ctx, w.done, w.lockFile); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errWriterClosed) {
			return Event{}, fmt.Errorf("%w: acquire cross-process lock: %w", ErrUnavailable, err)
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
	if err := w.verifyPaths(); err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("verify audit paths after lock: %w", err))
	}
	interruptAfterLock := func(interruptErr error) (Event, error) {
		if err := unlockAuditFile(w.lockFile); err != nil {
			locked = false
			return Event{}, w.markUnavailable(fmt.Errorf("release interrupted cross-process lock: %w", err))
		}
		locked = false
		return Event{}, fmt.Errorf("%w: %w", ErrUnavailable, interruptErr)
	}
	if err := ctx.Err(); err != nil {
		return interruptAfterLock(err)
	}
	lastSequence, err := recoverLog(w.file)
	if err != nil {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("refresh audit log: %w", err))
	}
	w.lastSequence = lastSequence
	if err := ctx.Err(); err != nil {
		return interruptAfterLock(err)
	}
	if w.lastSequence == ^uint64(0) {
		unlockAfterFailure()
		return Event{}, w.markUnavailable(fmt.Errorf("sequence space exhausted"))
	}
	persisted.Sequence = w.lastSequence + 1
	if persisted.Timestamp.IsZero() {
		persisted.Timestamp = w.now().UTC()
	} else {
		persisted.Timestamp = persisted.Timestamp.UTC()
	}
	line, err := json.Marshal(persisted)
	if err != nil {
		unlockAfterFailure()
		return Event{}, fmt.Errorf("%w: event cannot be encoded", ErrInvalidEvent)
	}
	line = append(line, '\n')
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
	if err := unlockAuditFile(w.lockFile); err != nil {
		locked = false
		return Event{}, w.markUnavailable(fmt.Errorf("release cross-process lock: %w", err))
	}
	locked = false
	w.lastSequence = persisted.Sequence
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
	if err := verifyOpenDirectory(w.directoryFile, w.directory); err != nil {
		return err
	}
	if err := verifyOpenNamedFile(w.lockFile, w.lockPath, lockFilename); err != nil {
		return err
	}
	return verifyOpenNamedFile(w.file, w.filePath, Filename)
}

func writeAll(writer io.Writer, body []byte) error {
	for len(body) > 0 {
		written, err := writer.Write(body)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(body) {
			return io.ErrShortWrite
		}
		body = body[written:]
	}
	return nil
}

func (w *Writer) markUnavailable(err error) error {
	if w.unavailable == nil {
		w.unavailable = err
	}
	return fmt.Errorf("%w: %v", ErrUnavailable, w.unavailable)
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
	if err := w.verifyPaths(); err != nil {
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
