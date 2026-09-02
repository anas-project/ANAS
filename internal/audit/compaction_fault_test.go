package audit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAuditCompactionPreCommitFailuresPreserveCanonical(t *testing.T) {
	injected := syscall.ENOSPC
	tests := []struct {
		name   string
		inject func(*Writer)
	}{
		{
			name: "create temp",
			inject: func(writer *Writer) {
				writer.compaction.createTemp = func(string, string) (logFile, error) {
					return nil, injected
				}
			},
		},
		{
			name: "write temp",
			inject: func(writer *Writer) {
				createTemp := writer.compaction.createTemp
				writer.compaction.createTemp = func(path, name string) (logFile, error) {
					file, err := createTemp(path, name)
					if err != nil {
						return nil, err
					}
					return &auditCompactionFaultFile{logFile: file, writeErr: injected}, nil
				}
			},
		},
		{
			name: "sync temp",
			inject: func(writer *Writer) {
				createTemp := writer.compaction.createTemp
				writer.compaction.createTemp = func(path, name string) (logFile, error) {
					file, err := createTemp(path, name)
					if err != nil {
						return nil, err
					}
					return &auditCompactionFaultFile{logFile: file, syncErr: injected}, nil
				}
			},
		},
		{
			name: "sync temp directory entry",
			inject: func(writer *Writer) {
				writer.compaction.syncDirectory = func(*os.File) error {
					return injected
				}
			},
		},
		{
			name: "rename not committed",
			inject: func(writer *Writer) {
				writer.compaction.rename = func(string, string) error {
					return injected
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer := openAuditCompactionWriter(t, directory, Options{
				MaxEvents:           8,
				CompactionThreshold: disabledAutomaticAuditCompaction,
			})
			if _, err := writer.Append(Event{Type: "precommit-canonical-marker"}); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(directory, Filename)
			beforeBody, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeInfo, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}

			test.inject(writer)
			err = writer.Compact(context.Background())
			if !errors.Is(err, ErrUnavailable) || !errors.Is(err, injected) {
				t.Fatalf("Compact error = %v, want ErrUnavailable and injected failure", err)
			}

			afterBody, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			afterInfo, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(beforeInfo, afterInfo) {
				t.Fatal("pre-commit failure replaced the canonical audit inode")
			}
			if !bytes.Equal(afterBody, beforeBody) {
				t.Fatal("pre-commit failure changed the canonical audit journal")
			}
			if _, err := os.Lstat(filepath.Join(directory, CompactionFilename)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("compaction temp remains after pre-commit failure: %v", err)
			}
		})
	}
}

func TestAuditCompactionRecognizesRenameThatCommittedBeforeError(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{
		MaxEvents:           8,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	})
	if _, err := writer.Append(Event{Type: "rename-committed-marker"}); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("rename reported failure after commit")
	renameCalls := 0
	writer.compaction.rename = func(oldPath, newPath string) error {
		renameCalls++
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		return injected
	}
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatalf("Compact after committed rename error = %v", err)
	}
	if renameCalls != 1 {
		t.Fatalf("rename calls = %d, want 1", renameCalls)
	}
	if writer.state.generation != 1 || !writer.state.compacted {
		t.Fatalf("writer generation after committed rename = %d, compacted=%t", writer.state.generation, writer.state.compacted)
	}
	assertAuditSnapshotKinds(t, readAuditCompactionBody(t, directory))

	next, err := writer.Append(Event{Type: "rename-committed-next"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 2 {
		t.Fatalf("sequence after committed rename = %d, want 2", next.Sequence)
	}
}

func TestAuditCompactionPostRenameDirectorySyncFailureReopensProspectiveGeneration(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{MaxEvents: 1, CompactionThreshold: 1}
	writer := openAuditCompactionWriter(t, directory, options)
	if _, err := writer.Append(Event{
		Type:    "post-rename-pruned-marker",
		Details: map[string]any{"payload": strings.Repeat("x", 16<<10)},
	}); err != nil {
		t.Fatal(err)
	}

	syncDirectory := writer.compaction.syncDirectory
	injected := syscall.ENOSPC
	syncCalls := 0
	writer.compaction.syncDirectory = func(directory *os.File) error {
		syncCalls++
		if syncCalls == 2 {
			return injected
		}
		return syncDirectory(directory)
	}
	if event, err := writer.Append(Event{Type: "post-rename-prospective-marker"}); !errors.Is(err, ErrUnavailable) || !errors.Is(err, injected) {
		t.Fatalf("Append after post-rename sync failure = %#v, %v, want ErrUnavailable and injected failure", event, err)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want 2", syncCalls)
	}

	body := readAuditCompactionBody(t, directory)
	assertAuditSnapshotKinds(t, body)
	assertAuditMarkers(t, body,
		[]string{"post-rename-prospective-marker"},
		[]string{"post-rename-pruned-marker"},
	)
	if err := writer.Close(); err != nil && !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}

	reopened, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatalf("reopen prospective generation: %v", err)
	}
	defer reopened.Close()
	if reopened.state.generation != 1 || reopened.state.lastSequence != 2 || reopened.state.prunedThrough != 1 {
		t.Fatalf("reopened state: generation=%d last=%d pruned=%d, want 1, 2, 1",
			reopened.state.generation, reopened.state.lastSequence, reopened.state.prunedThrough)
	}
	if len(reopened.state.events) != 1 || reopened.state.events[0].event.Type != "post-rename-prospective-marker" {
		t.Fatalf("reopened retained events = %#v", reopened.state.events)
	}
	next, err := reopened.Append(Event{Type: "post-rename-reopened-next"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 3 {
		t.Fatalf("sequence after reopening prospective generation = %d, want 3", next.Sequence)
	}
}

func TestAuditCompactionSuccessfulCommitTruncatesOldInode(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{
		MaxEvents:           8,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	})
	if _, err := writer.Append(Event{Type: "old-inode-marker"}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, Filename)
	stale, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	oldInfo, err := stale.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if oldInfo.Size() == 0 {
		t.Fatal("old audit inode is unexpectedly empty before compaction")
	}

	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	currentInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldInfo, currentInfo) {
		t.Fatal("successful compaction retained the old inode as canonical")
	}
	staleInfo, err := stale.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if staleInfo.Size() != 0 {
		t.Fatalf("old audit inode size after committed compaction = %d, want 0", staleInfo.Size())
	}
	if currentInfo.Size() == 0 {
		t.Fatal("compacted canonical audit journal is empty")
	}
}

func TestAuditAppendENOSPCIsStructuredAndFailsClosed(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{})
	path := filepath.Join(directory, Filename)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	failing := &auditCompactionFaultFile{logFile: writer.file, writeErr: syscall.ENOSPC}
	writer.file = failing
	_, err = writer.Append(Event{Type: "enospc-must-fail"})
	var persistence *PersistenceError
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, syscall.ENOSPC) || !errors.As(err, &persistence) {
		t.Fatalf("Append ENOSPC error = %v, want structured ErrUnavailable wrapping ENOSPC", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("ENOSPC append changed the canonical audit journal")
	}
	if _, err := writer.Append(Event{Type: "enospc-stays-closed"}); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("second Append error = %v, want original ENOSPC cause", err)
	}
}

func TestAuditCloseDoesNotRacePeerCompactionRename(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{MaxEvents: 8, CompactionThreshold: disabledAutomaticAuditCompaction}
	compactor := openAuditCompactionWriter(t, directory, options)
	closer := openAuditCompactionWriter(t, directory, options)
	if _, err := compactor.Append(Event{Type: "close-race-marker"}); err != nil {
		t.Fatal(err)
	}

	renamed := make(chan struct{})
	release := make(chan struct{})
	compactor.compaction.rename = func(oldPath, newPath string) error {
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		close(renamed)
		<-release
		return nil
	}
	compactResult := make(chan error, 1)
	go func() { compactResult <- compactor.Compact(context.Background()) }()
	<-renamed
	closeResult := make(chan error, 1)
	go func() { closeResult <- closer.Close() }()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close while peer holds audit.lock = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind peer compaction")
	}
	close(release)
	if err := <-compactResult; err != nil {
		t.Fatal(err)
	}
}

func TestAuditCommittedCompactionDoesNotReportOldInodeCleanupFailures(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{
		MaxEvents: 8, CompactionThreshold: disabledAutomaticAuditCompaction,
	})
	if _, err := writer.Append(Event{Type: "cleanup-failure-marker"}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("old inode cleanup failed")
	writer.file = &auditCompactionFaultFile{
		logFile: writer.file, truncateErr: injected, syncErr: injected, closeErr: injected,
	}
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatalf("committed compaction reported maintenance failure: %v", err)
	}
	if writer.state.generation != 1 {
		t.Fatalf("generation after cleanup failure = %d, want 1", writer.state.generation)
	}
	next, err := writer.Append(Event{Type: "after-cleanup-failure"})
	if err != nil || next.Sequence != 2 {
		t.Fatalf("append after cleanup failure = %#v, %v", next, err)
	}
}

func TestAuditAutomaticCompactionCleanPreCommitCancellationDoesNotPoisonWriter(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer := openAuditCompactionWriter(t, directory, Options{
				MaxEvents:           1,
				CompactionThreshold: 1,
			})
			defer writer.Close()

			if _, err := writer.Append(Event{
				Type:    "automatic-cancellation-old-marker",
				Details: map[string]any{"payload": strings.Repeat("x", 16<<10)},
			}); err != nil {
				t.Fatal(err)
			}

			ctx := newAuditCompactionInterruptContext(cause)
			syncDirectory := writer.compaction.syncDirectory
			syncCalls := 0
			writer.compaction.syncDirectory = func(directory *os.File) error {
				syncCalls++
				if syncCalls == 1 {
					ctx.interrupt()
				}
				return syncDirectory(directory)
			}

			if event, err := writer.AppendContext(ctx, Event{Type: "automatic-canceled-marker"}); !errors.Is(err, ErrUnavailable) || !errors.Is(err, cause) {
				t.Fatalf("AppendContext after clean cancellation = %#v, %v, want ErrUnavailable and %v", event, err, cause)
			}
			if syncCalls != 2 {
				t.Fatalf("directory sync calls after clean cancellation = %d, want 2", syncCalls)
			}
			if writer.unavailable != nil {
				t.Fatalf("clean cancellation poisoned Writer: %v", writer.unavailable)
			}
			if _, err := os.Lstat(filepath.Join(directory, CompactionFilename)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("compaction temp remains after clean cancellation: %v", err)
			}

			next, err := writer.Append(Event{Type: "automatic-after-cancellation"})
			if err != nil || next.Sequence != 2 {
				t.Fatalf("Append after clean cancellation = %#v, %v, want sequence 2", next, err)
			}
		})
	}
}

func TestAuditExplicitCompactionCleanPreCommitCancellationDoesNotPoisonWriter(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer := openAuditCompactionWriter(t, directory, Options{
				CompactionThreshold: disabledAutomaticAuditCompaction,
			})
			defer writer.Close()
			if _, err := writer.Append(Event{Type: "explicit-cancellation-marker"}); err != nil {
				t.Fatal(err)
			}

			ctx := newAuditCompactionInterruptContext(cause)
			syncDirectory := writer.compaction.syncDirectory
			syncCalls := 0
			writer.compaction.syncDirectory = func(directory *os.File) error {
				syncCalls++
				if syncCalls == 1 {
					ctx.interrupt()
				}
				return syncDirectory(directory)
			}

			if err := writer.Compact(ctx); !errors.Is(err, ErrUnavailable) || !errors.Is(err, cause) {
				t.Fatalf("Compact after clean cancellation = %v, want ErrUnavailable and %v", err, cause)
			}
			if syncCalls != 2 {
				t.Fatalf("directory sync calls after clean cancellation = %d, want 2", syncCalls)
			}
			if writer.unavailable != nil {
				t.Fatalf("clean cancellation poisoned Writer: %v", writer.unavailable)
			}

			next, err := writer.Append(Event{Type: "explicit-after-cancellation"})
			if err != nil || next.Sequence != 2 {
				t.Fatalf("Append after clean explicit cancellation = %#v, %v, want sequence 2", next, err)
			}
		})
	}
}

func TestAuditCompactionCancellationCleanupFailurePoisonsWriter(t *testing.T) {
	injected := syscall.ENOSPC
	for _, failure := range []string{"remove", "directory sync", "close"} {
		t.Run(failure, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer := openAuditCompactionWriter(t, directory, Options{
				CompactionThreshold: disabledAutomaticAuditCompaction,
			})
			defer writer.Close()
			if _, err := writer.Append(Event{Type: "cleanup-failure-marker"}); err != nil {
				t.Fatal(err)
			}

			ctx := newAuditCompactionInterruptContext(context.Canceled)
			syncDirectory := writer.compaction.syncDirectory
			syncCalls := 0
			writer.compaction.syncDirectory = func(directory *os.File) error {
				syncCalls++
				if syncCalls == 1 {
					ctx.interrupt()
				}
				if failure == "directory sync" && syncCalls == 2 {
					return injected
				}
				return syncDirectory(directory)
			}
			if failure == "remove" {
				writer.compaction.remove = func(string) error { return injected }
			}
			if failure == "close" {
				createTemp := writer.compaction.createTemp
				writer.compaction.createTemp = func(path, name string) (logFile, error) {
					file, err := createTemp(path, name)
					if err != nil {
						return nil, err
					}
					return &auditCompactionFaultFile{logFile: file, closeErr: injected}, nil
				}
			}

			err := writer.Compact(ctx)
			if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) || !errors.Is(err, injected) {
				t.Fatalf("Compact with failed %s cleanup = %v, want ErrUnavailable, context.Canceled, and ENOSPC", failure, err)
			}
			if writer.unavailable == nil {
				t.Fatal("failed cancellation cleanup did not poison Writer")
			}
			if _, err := writer.Append(Event{Type: "must-stay-poisoned"}); !errors.Is(err, ErrUnavailable) || !errors.Is(err, injected) {
				t.Fatalf("Append after failed %s cleanup = %v, want poisoned ENOSPC state", failure, err)
			}
		})
	}
}

func TestAuditCompactionContextLookingWriteFailurePoisonsWriter(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{
		CompactionThreshold: disabledAutomaticAuditCompaction,
	})
	defer writer.Close()
	if _, err := writer.Append(Event{Type: "context-looking-write-marker"}); err != nil {
		t.Fatal(err)
	}

	injected := syscall.ENOSPC
	createTemp := writer.compaction.createTemp
	writer.compaction.createTemp = func(path, name string) (logFile, error) {
		file, err := createTemp(path, name)
		if err != nil {
			return nil, err
		}
		return &auditCompactionFaultFile{
			logFile:  file,
			writeErr: errors.Join(context.Canceled, injected),
		}, nil
	}

	err := writer.Compact(context.Background())
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) || !errors.Is(err, injected) {
		t.Fatalf("Compact with context-looking write failure = %v, want ErrUnavailable, context.Canceled, and ENOSPC", err)
	}
	if writer.unavailable == nil {
		t.Fatal("context-looking I/O failure did not poison Writer")
	}
	if _, err := writer.Append(Event{Type: "must-stay-poisoned"}); !errors.Is(err, injected) {
		t.Fatalf("Append after context-looking write failure = %v, want ENOSPC poison", err)
	}
}

func TestAuditCompactionRenameAttemptWithCancellationErrorIsNeverClean(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{
		MaxEvents:           1,
		CompactionThreshold: 1,
	})
	defer writer.Close()
	if _, err := writer.Append(Event{
		Type:    "rename-ambiguity-old-marker",
		Details: map[string]any{"payload": strings.Repeat("x", 16<<10)},
	}); err != nil {
		t.Fatal(err)
	}

	createTemp := writer.compaction.createTemp
	var temp *auditCompactionFaultFile
	writer.compaction.createTemp = func(path, name string) (logFile, error) {
		file, err := createTemp(path, name)
		if err != nil {
			return nil, err
		}
		temp = &auditCompactionFaultFile{logFile: file}
		return temp, nil
	}
	renameFailure := errors.New("rename returned a cancellation-shaped error")
	identityFailure := errors.New("identity check failed after rename")
	writer.compaction.rename = func(oldPath, newPath string) error {
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		temp.statErr = identityFailure
		temp.statErrOnce = true
		return errors.Join(context.Canceled, renameFailure)
	}

	event, err := writer.AppendContext(context.Background(), Event{Type: "rename-ambiguity-prospective-marker"})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) ||
		!errors.Is(err, renameFailure) || !errors.Is(err, identityFailure) {
		t.Fatalf("Append after ambiguous rename = %#v, %v, want fail-closed compound error", event, err)
	}
	if writer.unavailable == nil {
		t.Fatal("ambiguous rename cancellation was incorrectly treated as clean")
	}
	if _, err := os.Lstat(filepath.Join(directory, CompactionFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compaction temp remains after rename attempt: %v", err)
	}
	assertAuditMarkers(t, readAuditCompactionBody(t, directory),
		[]string{"rename-ambiguity-prospective-marker"},
		[]string{"rename-ambiguity-old-marker"},
	)
	if _, err := writer.Append(Event{Type: "must-stay-poisoned"}); !errors.Is(err, renameFailure) {
		t.Fatalf("Append after ambiguous rename = %v, want retained poison", err)
	}
}

type auditCompactionInterruptContext struct {
	context.Context
	done  chan struct{}
	cause error
}

func newAuditCompactionInterruptContext(cause error) *auditCompactionInterruptContext {
	return &auditCompactionInterruptContext{
		Context: context.Background(),
		done:    make(chan struct{}),
		cause:   cause,
	}
}

func (ctx *auditCompactionInterruptContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *auditCompactionInterruptContext) Err() error {
	select {
	case <-ctx.done:
		return ctx.cause
	default:
		return nil
	}
}

func (ctx *auditCompactionInterruptContext) interrupt() {
	close(ctx.done)
}

type auditCompactionFaultFile struct {
	logFile
	writeErr    error
	syncErr     error
	truncateErr error
	closeErr    error
	statErr     error
	statErrOnce bool
}

func (file *auditCompactionFaultFile) Stat() (os.FileInfo, error) {
	if file.statErr != nil {
		err := file.statErr
		if file.statErrOnce {
			file.statErr = nil
		}
		return nil, err
	}
	return file.logFile.Stat()
}

func (file *auditCompactionFaultFile) Write(body []byte) (int, error) {
	if file.writeErr != nil {
		return 0, file.writeErr
	}
	return file.logFile.Write(body)
}

func (file *auditCompactionFaultFile) Sync() error {
	if file.syncErr != nil {
		return file.syncErr
	}
	return file.logFile.Sync()
}

func (file *auditCompactionFaultFile) Truncate(size int64) error {
	if file.truncateErr != nil {
		return file.truncateErr
	}
	return file.logFile.Truncate(size)
}

func (file *auditCompactionFaultFile) Close() error {
	if file.closeErr != nil {
		_ = file.logFile.Close()
		return file.closeErr
	}
	return file.logFile.Close()
}
