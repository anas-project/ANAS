//go:build unix

package consolejobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	jobSubprocessMarker = "ANAS_CONSOLEJOBS_SUBPROCESS"
	jobSubprocessDir    = "ANAS_CONSOLEJOBS_DIRECTORY"
	jobSubprocessJobID  = "ANAS_CONSOLEJOBS_JOB_ID"
	jobSubprocessIndex  = "ANAS_CONSOLEJOBS_PROCESS_INDEX"
	rewriteProcessMark  = "ANAS_CONSOLEJOBS_REWRITE_PROCESS"
	rewriteProcessPath  = "ANAS_CONSOLEJOBS_REWRITE_PATH"
	rewriteProcessTail  = "ANAS_CONSOLEJOBS_REWRITE_TAIL"
	childEventCount     = 5
)

func TestOpenCreatesAndRequiresSecurePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "console", "jobs")
	store, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for path, mode := range map[string]os.FileMode{
		dir:                                 0o700,
		filepath.Join(dir, LockFilename):    0o600,
		filepath.Join(dir, JournalFilename): 0o600,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), mode)
		}
	}
}

func TestConcurrentFirstOpenSharesOneInitializedStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	const openerCount = 12
	start := make(chan struct{})
	errorsByOpener := make(chan error, openerCount)
	for index := 0; index < openerCount; index++ {
		go func() {
			<-start
			store, err := Open(dir, Options{})
			if err == nil {
				err = store.Close()
			}
			errorsByOpener <- err
		}()
	}
	close(start)
	for index := 0; index < openerCount; index++ {
		if err := <-errorsByOpener; err != nil {
			t.Fatalf("concurrent Open %d: %v", index, err)
		}
	}

	store, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.state.storeID == "" {
		t.Fatal("initialized store has no durable identity")
	}
	lockStoreID, err := readLockMetadata(store.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if lockStoreID != store.state.storeID {
		t.Fatalf("lock store ID = %q, journal store ID = %q", lockStoreID, store.state.storeID)
	}
}

func TestOpenRejectsHardlinksAndWidePaths(t *testing.T) {
	t.Run("wide directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "jobs")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(dir, Options{}); err == nil || !strings.Contains(err.Error(), "0700") {
			t.Fatalf("Open error = %v, want directory mode rejection", err)
		}
	})
	for _, name := range []string{LockFilename, JournalFilename} {
		t.Run("hardlink "+name, func(t *testing.T) {
			dir := secureJobTestDir(t)
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(path, filepath.Join(t.TempDir(), "alias")); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir, Options{}); err == nil || !strings.Contains(err.Error(), "link count") {
				t.Fatalf("Open error = %v, want hardlink rejection", err)
			}
		})
	}
}

func TestStoreFailsClosedWhenPinnedPathIsReplaced(t *testing.T) {
	for _, name := range []string{LockFilename, JournalFilename} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "jobs")
			store, err := Open(dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			if err := os.Rename(path, path+".old"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.List(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("List error = %v, want ErrUnavailable", err)
			}
			if err := store.Close(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Close error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestOpenDoesNotSilentlyReinitializeMissingOrTruncatedJournal(t *testing.T) {
	for _, mutate := range []struct {
		name string
		run  func(string) error
	}{
		{name: "missing", run: os.Remove},
		{name: "truncated", run: func(path string) error { return os.Truncate(path, 0) }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "jobs")
			store, err := Open(dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if err := mutate.run(filepath.Join(dir, JournalFilename)); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir, Options{}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Open error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestContextCancellationWhileWaitingForFileLockDoesNotPoisonStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	holder, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	waiter, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()
	lockContext, cancelLock := context.WithTimeout(context.Background(), time.Second)
	defer cancelLock()
	if err := lockFileContext(lockContext, nil, holder.lockFile); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = unlockFile(holder.lockFile)
		}
	}()

	waitContext, cancelWait := context.WithTimeout(context.Background(), 75*time.Millisecond)
	_, err = waiter.List(waitContext)
	cancelWait()
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("List error = %v, want unavailable deadline", err)
	}
	if err := unlockFile(holder.lockFile); err != nil {
		t.Fatal(err)
	}
	locked = false
	if _, err := waiter.List(context.Background()); err != nil {
		t.Fatalf("healthy store was poisoned by cancellation: %v", err)
	}
}

func TestCloseCancelsOperationWaitingForFileLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	holder, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	waiter, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lockContext, cancelLock := context.WithTimeout(context.Background(), time.Second)
	defer cancelLock()
	if err := lockFileContext(lockContext, nil, holder.lockFile); err != nil {
		t.Fatal(err)
	}
	defer unlockFile(holder.lockFile)

	operationResult := make(chan error, 1)
	go func() {
		_, err := waiter.List(context.Background())
		operationResult <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(waiter.gate) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(waiter.gate) != 0 {
		t.Fatal("operation never entered store gate")
	}
	closeResult := make(chan error, 1)
	go func() { closeResult <- waiter.Close() }()
	select {
	case err := <-operationResult:
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, errStoreClosed) {
			t.Fatalf("operation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel lock waiter")
	}
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close remained blocked")
	}
}

func TestMultipleProcessesSerializeEventIDs(t *testing.T) {
	if !crossProcessLockSupported {
		t.Skip("cross-process locking unsupported")
	}
	dir := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(dir, Options{EventCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), subprocessCreateSpec("parent-key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const processCount = 4
	type child struct {
		command *exec.Cmd
		output  bytes.Buffer
	}
	children := make([]child, processCount)
	for index := range children {
		item := &children[index]
		item.command = exec.Command(executable, "-test.run=^TestConsoleJobsSubprocess$")
		item.command.Env = append(os.Environ(),
			jobSubprocessMarker+"=1", jobSubprocessDir+"="+dir,
			jobSubprocessJobID+"="+created.Job.ID, jobSubprocessIndex+"="+strconv.Itoa(index),
		)
		item.command.Stdout = &item.output
		item.command.Stderr = &item.output
		if err := item.command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for index := range children {
		if err := children[index].command.Wait(); err != nil {
			t.Fatalf("child %d: %v\n%s", index, err, children[index].output.String())
		}
	}
	reopened, err := Open(dir, Options{EventCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := reopened.Replay(context.Background(), created.Job.ID, ReplayOptions{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if want := processCount * childEventCount; len(page.Events) != want {
		t.Fatalf("events = %d, want %d", len(page.Events), want)
	}
	for index, event := range page.Events {
		if event.ID != uint64(index+1) {
			t.Fatalf("event %d ID = %d", index, event.ID)
		}
	}
}

func TestCrossProcessPrefixRewriteAndGrowthForcesFullRecovery(t *testing.T) {
	// A separate process has no receipt authority. Even if it appends a tail
	// that is valid relative to the cached state, prefix mutation forces a full
	// replay and exposes the corruption.
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	job := createJobForTest(t, store, testCreateSpec("cross-process-rewrite", "workspace-a", false))
	now := time.Now().UTC()
	record := journalRecord{
		SchemaVersion: JournalVersion,
		Sequence:      store.state.lastRecordSequence + 1,
		Kind:          recordEventAdded,
		RecordedAt:    now,
		Event: &Event{
			ID: store.state.lastEventID + 1, JobID: job.ID, Timestamp: now, Kind: "cross-process-tail",
		},
	}
	tail, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	tail = append(tail, '\n')
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestCrossProcessJournalRewriteHelper$")
	command.Env = append(os.Environ(),
		rewriteProcessMark+"=1",
		rewriteProcessPath+"="+filepath.Join(directory, JournalFilename),
		rewriteProcessTail+"="+base64.StdEncoding.EncodeToString(tail),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("rewrite subprocess: %v\n%s", err, output)
	}

	observed := &observedJournal{journalFile: store.file}
	store.file = observed
	if _, err := store.Get(context.Background(), job.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get after cross-process rewrite plus growth error = %v, want ErrUnavailable", err)
	}
	if observed.fullScans == 0 {
		t.Fatal("cross-process growth trusted the cached prefix without a local receipt")
	}
}

func TestCrossProcessJournalRewriteHelper(t *testing.T) {
	if os.Getenv(rewriteProcessMark) != "1" {
		t.Skip("subprocess helper")
	}
	tail, err := base64.StdEncoding.DecodeString(os.Getenv(rewriteProcessTail))
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(os.Getenv(rewriteProcessPath), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte{'['}, 0); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := writeAll(file, tail); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConsoleJobsSubprocess(t *testing.T) {
	if os.Getenv(jobSubprocessMarker) != "1" {
		t.Skip("subprocess helper")
	}
	processIndex, err := strconv.Atoi(os.Getenv(jobSubprocessIndex))
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(os.Getenv(jobSubprocessDir), Options{EventCapacity: 100})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < childEventCount; index++ {
		_, err := store.AppendEvent(context.Background(), os.Getenv(jobSubprocessJobID), EventInput{
			Kind: "child.output", Data: map[string]any{"process": processIndex, "index": index},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerMetadataValidationRejectsDifferentEUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid = uint32(os.Geteuid() + 1)
	if err := validateCurrentOwner(overriddenJobFileInfo{FileInfo: info, system: &stat}, JournalFilename); err == nil {
		t.Fatal("different owner was accepted")
	}
}

type overriddenJobFileInfo struct {
	os.FileInfo
	system any
}

func (info overriddenJobFileInfo) Sys() any { return info.system }

func secureJobTestDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "jobs")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func subprocessCreateSpec(key string) CreateSpec {
	body := []byte(`{"operation":"test"}`)
	return CreateSpec{
		Kind: "test.mutation", WorkspaceID: "workspace-a", Mutating: true,
		Request: map[string]any{"operation": "test"},
		Idempotency: IdempotencyInput{
			Principal: "owner", Method: "POST", CanonicalPath: "/api/v1/workspaces/workspace-a/test",
			Key: key, RequestDigest: DigestRequest(body),
		},
	}
}
