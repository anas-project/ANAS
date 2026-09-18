//go:build linux && (amd64 || arm64)

package computeingress_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// These filesystem tests are local only: no Incus, Docker, root networking,
// credentials or server access. Authoring them does not constitute execution.
func integrityWriter(t *testing.T, directory string) *computeingress.RequestWriter {
	t.Helper()
	writer, err := computeingress.OpenRequestWriter(directory)
	if err != nil {
		t.Fatalf("open private local request directory: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

func integrityRequest() computeingress.Request {
	return computeingress.Request{Action: "publish", InstanceID: "anas-tests-one", WorkloadID: "job-one", GuestPort: 8080}
}

func integrityRequestFile(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 1 {
		t.Fatalf("want exactly one request, got %d", len(names))
	}
	return names[0]
}

func integrityReadRequest(t *testing.T, directory string) computeingress.Request {
	t.Helper()
	name := integrityRequestFile(t, directory)
	file, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	request, err := computeingress.ReadRequest(file, name)
	if err != nil {
		t.Fatalf("mediator cannot read submitted request: %v", err)
	}
	return request
}

func TestRequestWriterIntegritySubmitRetryAndWithdraw(t *testing.T) {
	directory := t.TempDir()
	writer := integrityWriter(t, directory)
	request := integrityRequest()
	receipt, err := writer.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := writer.Submit(context.Background(), request)
	if err != nil || retry != receipt {
		t.Fatalf("retry should reuse local receipt: %v", err)
	}
	if got := integrityReadRequest(t, directory); got != request {
		t.Fatalf("request changed: %#v", got)
	}
	name := integrityRequestFile(t, directory)
	info, err := os.Lstat(filepath.Join(directory, name))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > computeingress.MaxRequestBytes {
		t.Fatalf("request file mode or size is invalid: %v", err)
	}
	other := request
	other.WorkloadID = "different-job"
	if _, err := writer.Submit(context.Background(), other); !errors.Is(err, computeingress.ErrRequestConflict) {
		t.Fatalf("different workload must not overwrite a slot: %v", err)
	}
	if err := writer.Withdraw(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := writer.Withdraw(context.Background(), receipt); err != nil {
		t.Fatalf("withdraw retry must be idempotent: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("withdraw must not accumulate revoked request files: %v", err)
	}
	if _, err := writer.Submit(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	// A successful old withdrawal remains harmless after the slot is reused.
	if err := writer.Withdraw(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if got := integrityReadRequest(t, directory); got != other {
		t.Fatal("old receipt removed a later submission")
	}
}

func TestRequestWriterIntegrityForeignAndStaleReceipts(t *testing.T) {
	directory := t.TempDir()
	first := integrityWriter(t, directory)
	second := integrityWriter(t, directory)
	request := integrityRequest()
	old, err := first.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Withdraw(context.Background(), old); !errors.Is(err, computeingress.ErrRequestReceiptStale) {
		t.Fatalf("foreign writer accepted receipt: %v", err)
	}
	shared, err := second.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Withdraw(context.Background(), shared); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	// Identical JSON is insufficient: the replacement has a different inode.
	if err := first.Withdraw(context.Background(), old); !errors.Is(err, computeingress.ErrRequestReceiptStale) {
		t.Fatalf("stale receipt accepted a replacement inode: %v", err)
	}
	if got := integrityReadRequest(t, directory); got != request {
		t.Fatal("stale withdrawal changed the new request")
	}
}

func TestRequestWriterIntegrityResumeDoesNotRepublish(t *testing.T) {
	directory := t.TempDir()
	first := integrityWriter(t, directory)
	request := integrityRequest()
	if _, err := first.Resume(context.Background(), request); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resume must not create a missing request: %v", err)
	}
	if _, err := first.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	name := integrityRequestFile(t, directory)
	before, err := os.Stat(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := integrityWriter(t, directory)
	wrong := request
	wrong.WorkloadID = "wrong-recovery-workload"
	if _, err := second.Resume(context.Background(), wrong); !errors.Is(err, computeingress.ErrRequestConflict) {
		t.Fatalf("resume adopted a different workload: %v", err)
	}
	receipt, err := second.Resume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(directory, name))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("resume rewrote the request inode")
	}
	if err := second.Withdraw(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Resume(context.Background(), request); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resume resurrected a withdrawn intent: %v", err)
	}
}

func TestRequestWriterIntegrityZeroValueAndPrivateDirectory(t *testing.T) {
	var zero computeingress.RequestWriter
	if _, err := zero.Submit(context.Background(), integrityRequest()); !errors.Is(err, computeingress.ErrRequestWriterUnavailable) {
		t.Fatalf("zero-value writer did not fail closed: %v", err)
	}
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	missing := filepath.Join(parent, "missing")
	if _, err := computeingress.OpenRequestWriter(missing); !errors.Is(err, computeingress.ErrRequestWriterUnavailable) {
		t.Fatalf("missing directory accepted: %v", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("writer created an installation-owned directory")
	}
	private := filepath.Join(parent, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := computeingress.OpenRequestWriter(alias); !errors.Is(err, computeingress.ErrRequestWriterUnavailable) {
		t.Fatalf("symlink directory accepted: %v", err)
	}
	if err := os.Chmod(private, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := computeingress.OpenRequestWriter(private); !errors.Is(err, computeingress.ErrRequestWriterUnavailable) {
		t.Fatalf("shared directory accepted: %v", err)
	}
}

func TestRequestWriterIntegrityCloseKeepsIntent(t *testing.T) {
	directory := t.TempDir()
	writer := integrityWriter(t, directory)
	request := integrityRequest()
	if _, err := writer.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := integrityReadRequest(t, directory); got != request {
		t.Fatal("closing a local publisher retracted durable intent")
	}
	if _, err := writer.Submit(context.Background(), request); !errors.Is(err, computeingress.ErrRequestWriterUnavailable) {
		t.Fatalf("closed writer accepted a request: %v", err)
	}
}

func TestRequestWriterIntegrityChangedFileIsNotWithdrawn(t *testing.T) {
	directory := t.TempDir()
	writer := integrityWriter(t, directory)
	request := integrityRequest()
	receipt, err := writer.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.WorkloadID = "replacement"
	body, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, integrityRequestFile(t, directory)), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writer.Withdraw(context.Background(), receipt); !errors.Is(err, computeingress.ErrRequestReceiptStale) {
		t.Fatalf("mutated request accepted old receipt: %v", err)
	}
	if got := integrityReadRequest(t, directory); got != changed {
		t.Fatal("foreign request content was removed")
	}
}

func TestRequestWriterIntegrityRejectsSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			writer := integrityWriter(t, directory)
			request := integrityRequest()
			receipt, err := writer.Submit(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			name := integrityRequestFile(t, directory)
			if err := writer.Withdraw(context.Background(), receipt); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "must-not-change")
			body, _ := json.Marshal(request)
			if err := os.WriteFile(outside, body, 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(directory, name)
			switch kind {
			case "symlink":
				err = os.Symlink(outside, target)
			case "hardlink":
				err = os.Link(outside, target)
			case "fifo":
				err = syscall.Mkfifo(target, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Submit(context.Background(), request); err == nil {
				t.Fatal("special request file accepted")
			} else if strings.Contains(err.Error(), outside) {
				t.Fatal("error disclosed a caller-controlled path")
			}
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != string(body) {
				t.Fatal("outside file was changed")
			}
		})
	}
}

func TestRequestWriterIntegrityRejectsReplacedDirectory(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "lease")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writer := integrityWriter(t, directory)
	if err := os.Rename(directory, filepath.Join(parent, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Submit(context.Background(), integrityRequest()); !errors.Is(err, computeingress.ErrRequestWriterUnavailable) {
		t.Fatalf("replaced lease directory accepted: %v", err)
	}
	for _, name := range []string{"lease", "original"} {
		entries, err := os.ReadDir(filepath.Join(parent, name))
		if err != nil || len(entries) != 0 {
			t.Fatal("replaced directory received a write")
		}
	}
}

func TestRequestWriterIntegrityAdmissionIsBounded(t *testing.T) {
	directory := t.TempDir()
	writer := integrityWriter(t, directory)
	for i := 0; i < 255; i++ {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf(".ignored-%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	first := integrityRequest()
	receipt, err := writer.Submit(context.Background(), first)
	if err != nil {
		t.Fatalf("256th entry should fit, including temporary publication: %v", err)
	}
	second := first
	second.InstanceID = "anas-tests-two"
	if _, err := writer.Submit(context.Background(), second); err == nil {
		t.Fatal("request directory exceeded the mediator's 256-entry bound")
	}
	if err := writer.Withdraw(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Submit(context.Background(), second); err != nil {
		t.Fatal(err)
	}
}

func TestRequestWriterIntegrityLockWaitHonorsCancellation(t *testing.T) {
	directory := t.TempDir()
	writer := integrityWriter(t, directory)
	lock, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := writer.Submit(ctx, integrityRequest()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked writer did not honor context: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("canceled lock acquisition left an intent or temporary file")
	}
}

func TestRequestWriterIntegrityConcurrentIdenticalSubmissions(t *testing.T) {
	directory := t.TempDir()
	writers := []*computeingress.RequestWriter{integrityWriter(t, directory), integrityWriter(t, directory)}
	var workers sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < cap(failures); i++ {
		writer := writers[i%len(writers)]
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := writer.Submit(context.Background(), integrityRequest())
			failures <- err
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := integrityReadRequest(t, directory); got != integrityRequest() {
		t.Fatal("concurrent submissions did not converge")
	}
}
