//go:build unix

package audit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	auditSubprocessMarker = "ANAS_AUDIT_WRITER_SUBPROCESS"
	auditSubprocessDir    = "ANAS_AUDIT_WRITER_DIRECTORY"
	subprocessAppendCount = 12
)

func TestOpenRejectsHardLinkedLogAndLock(t *testing.T) {
	for _, name := range []string{Filename, lockFilename} {
		t.Run(name, func(t *testing.T) {
			dir := secureTestDir(t)
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(path, filepath.Join(t.TempDir(), "second-link")); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "link count") {
				t.Fatalf("Open error = %v, want hard-link rejection", err)
			}
		})
	}
}

func TestOwnerAndLinkMetadataValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned-file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("stat owner metadata unavailable")
	}

	wrongOwner := *stat
	wrongOwner.Uid = uint32(os.Geteuid() + 1)
	if err := validateCurrentOwner(overriddenSystemInfo{FileInfo: info, system: &wrongOwner}, Filename); err == nil {
		t.Fatal("validateCurrentOwner accepted a different effective owner")
	}

	multipleLinks := *stat
	multipleLinks.Nlink = 2
	if err := validateSingleLink(overriddenSystemInfo{FileInfo: info, system: &multipleLinks}, Filename); err == nil {
		t.Fatal("validateSingleLink accepted multiple links")
	}
}

func TestAppendFailsClosedWhenPinnedFilePathIsReplaced(t *testing.T) {
	for _, name := range []string{Filename, lockFilename} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "audit")
			writer, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Append(Event{Type: "before-replacement"}); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(dir, name)
			if err := os.Rename(path, path+".replaced"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Append(Event{Type: "after-replacement"}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Append error = %v, want ErrUnavailable", err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Fatalf("replacement %s received audit data: %q", name, body)
			}
			if err := writer.Close(); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Close error = %v, want replacement integrity failure", err)
			}
		})
	}
}

func TestAppendContextCancellationDoesNotPoisonWriter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	lockHolder, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lockHolder.Close()
	waiter, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer waiter.Close()

	lockContext, cancelLock := context.WithTimeout(context.Background(), time.Second)
	defer cancelLock()
	if err := lockAuditFile(lockContext, nil, lockHolder.lockFile); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = unlockAuditFile(lockHolder.lockFile)
		}
	}()

	waitContext, cancelWait := context.WithTimeout(context.Background(), 75*time.Millisecond)
	_, err = waiter.AppendContext(waitContext, Event{Type: "must-time-out"})
	cancelWait()
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AppendContext error = %v, want ErrUnavailable and DeadlineExceeded", err)
	}
	if err := unlockAuditFile(lockHolder.lockFile); err != nil {
		t.Fatal(err)
	}
	locked = false
	if event, err := waiter.Append(Event{Type: "after-timeout"}); err != nil || event.Sequence != 1 {
		t.Fatalf("Append after timeout = %#v, %v", event, err)
	}
}

func TestAppendContextAlreadyCanceledDoesNotWriteOrPoison(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := writer.AppendContext(canceled, Event{Type: "must-not-write"}); !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("AppendContext error = %v, want ErrUnavailable and context.Canceled", err)
	}
	if events := readEvents(t, filepath.Join(dir, Filename)); len(events) != 0 {
		t.Fatalf("canceled append persisted %d events", len(events))
	}
	if event, err := writer.Append(Event{Type: "after-cancel"}); err != nil || event.Sequence != 1 {
		t.Fatalf("Append after cancel = %#v, %v", event, err)
	}
}

func TestCloseCancelsAppendWaitingForCrossProcessLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	lockHolder, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lockHolder.Close()
	waiter, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	lockContext, cancelLock := context.WithTimeout(context.Background(), time.Second)
	defer cancelLock()
	if err := lockAuditFile(lockContext, nil, lockHolder.lockFile); err != nil {
		t.Fatal(err)
	}
	defer unlockAuditFile(lockHolder.lockFile)

	appendResult := make(chan error, 1)
	go func() {
		_, err := waiter.AppendContext(context.Background(), Event{Type: "blocked"})
		appendResult <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(waiter.gate) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(waiter.gate) != 0 {
		t.Fatal("AppendContext did not acquire the in-process gate")
	}

	closeResult := make(chan error, 1)
	go func() { closeResult <- waiter.Close() }()
	select {
	case err := <-appendResult:
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, errWriterClosed) {
			t.Fatalf("blocked AppendContext error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AppendContext was not canceled by Close")
	}
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close remained blocked after canceling the lock waiter")
	}
}

func TestAppendSerializesAcrossProcesses(t *testing.T) {
	if !crossProcessAuditLockSupported {
		t.Skip("cross-process lock is unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "audit")
	const processCount = 4
	type childProcess struct {
		command *exec.Cmd
		output  bytes.Buffer
	}
	children := make([]childProcess, processCount)
	for index := range children {
		child := &children[index]
		child.command = exec.Command(executable, "-test.run=^TestAuditWriterSubprocess$")
		child.command.Env = append(os.Environ(),
			auditSubprocessMarker+"=1",
			auditSubprocessDir+"="+dir,
		)
		child.command.Stdout = &child.output
		child.command.Stderr = &child.output
		if err := child.command.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for index := range children {
		child := &children[index]
		if err := child.command.Wait(); err != nil {
			t.Fatalf("child %d: %v\n%s", index, err, child.output.String())
		}
	}

	events := readEvents(t, filepath.Join(dir, Filename))
	if want := processCount * subprocessAppendCount; len(events) != want {
		t.Fatalf("persisted events = %d, want %d", len(events), want)
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
}

func TestAuditWriterSubprocess(t *testing.T) {
	if os.Getenv(auditSubprocessMarker) != "1" {
		t.Skip("subprocess helper")
	}
	dir := os.Getenv(auditSubprocessDir)
	if dir == "" {
		t.Fatal("subprocess directory is empty")
	}
	writer, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < subprocessAppendCount; index++ {
		if _, err := writer.Append(Event{
			Type:    "subprocess",
			Details: map[string]any{"pid": os.Getpid(), "index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

type overriddenSystemInfo struct {
	os.FileInfo
	system any
}

func (info overriddenSystemInfo) Sys() any { return info.system }
