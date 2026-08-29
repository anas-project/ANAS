package consolejobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ExecutionLease is the process-lifetime ownership boundary for job
// execution and restart recovery. Store readers and appenders use the
// short-lived jobs.lock independently and do not imply that an executor died.
type ExecutionLease struct {
	mu            sync.Mutex
	directory     string
	path          string
	directoryFile *os.File
	file          *os.File
	closed        bool
}

// AcquireExecutionLease obtains exclusive execution ownership for one console
// store. The caller should hold the lease until all job executors have stopped.
func AcquireExecutionLease(ctx context.Context, directory string) (*ExecutionLease, error) {
	if ctx == nil {
		return nil, invalidError("context is nil")
	}
	if directory == "" {
		return nil, invalidError("store directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, &PersistenceError{Operation: "resolve job execution lease", Cause: err}
	}
	directory = filepath.Clean(absolute)
	directoryFile, createdEntries, err := openSecureDirectory(directory)
	if err != nil {
		return nil, &PersistenceError{Operation: "open job execution lease directory", Cause: err}
	}
	closeDirectory := func(cause error) (*ExecutionLease, error) {
		_ = directoryFile.Close()
		return nil, &PersistenceError{Operation: "acquire job execution lease", Cause: cause}
	}
	if len(createdEntries) != 0 {
		if err := directoryFile.Sync(); err != nil {
			return closeDirectory(fmt.Errorf("sync job execution lease directory: %w", err))
		}
		if err := syncCreatedDirectoryEntries(createdEntries); err != nil {
			return closeDirectory(err)
		}
	}
	path := filepath.Join(directory, ExecutionLeaseFilename)
	file, created, err := openSecureNamedFile(path, ExecutionLeaseFilename)
	if err != nil {
		return closeDirectory(err)
	}
	closeFile := func(cause error) (*ExecutionLease, error) {
		_ = file.Close()
		return closeDirectory(cause)
	}
	if created {
		if err := directoryFile.Sync(); err != nil {
			return closeFile(fmt.Errorf("sync new %s directory entry: %w", ExecutionLeaseFilename, err))
		}
	}
	if err := lockFileContext(ctx, nil, file); err != nil {
		return closeFile(fmt.Errorf("lock %s: %w", ExecutionLeaseFilename, err))
	}
	unlockOnError := func(cause error) (*ExecutionLease, error) {
		_ = unlockFile(file)
		return closeFile(cause)
	}
	if err := verifyOpenDirectory(directoryFile, directory); err != nil {
		return unlockOnError(err)
	}
	if err := verifyOpenNamedFile(file, path, ExecutionLeaseFilename); err != nil {
		return unlockOnError(err)
	}
	if err := ctx.Err(); err != nil {
		return unlockOnError(err)
	}
	return &ExecutionLease{
		directory: directory, path: path, directoryFile: directoryFile, file: file,
	}, nil
}

func (lease *ExecutionLease) withOwnership(directory string, action func() error) error {
	if lease == nil || action == nil {
		return ErrUnavailable
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed || lease.directory != directory {
		return ErrUnavailable
	}
	if err := verifyOpenDirectory(lease.directoryFile, lease.directory); err != nil {
		return &PersistenceError{Operation: "verify job execution lease", Cause: err}
	}
	if err := verifyOpenNamedFile(lease.file, lease.path, ExecutionLeaseFilename); err != nil {
		return &PersistenceError{Operation: "verify job execution lease", Cause: err}
	}
	return action()
}

// Close releases execution ownership. It is safe to call more than once.
func (lease *ExecutionLease) Close() error {
	if lease == nil {
		return nil
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return nil
	}
	lease.closed = true
	var integrityErr error
	if err := verifyOpenDirectory(lease.directoryFile, lease.directory); err != nil {
		integrityErr = err
	} else if err := verifyOpenNamedFile(lease.file, lease.path, ExecutionLeaseFilename); err != nil {
		integrityErr = err
	}
	unlockErr := unlockFile(lease.file)
	fileErr := lease.file.Close()
	directoryErr := lease.directoryFile.Close()
	return errors.Join(integrityErr, unlockErr, fileErr, directoryErr)
}
