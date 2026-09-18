//go:build linux && (amd64 || arm64)

package computeingress

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

func receiptRegressionRequest() Request {
	return Request{Action: "publish", InstanceID: "anas-test-job1", WorkloadID: "job:1", GuestPort: 7000}
}

func receiptRegressionWriter(t *testing.T, directory string) *RequestWriter {
	t.Helper()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writer, err := OpenRequestWriter(directory)
	if err != nil {
		t.Fatalf("open private local request directory: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	return writer
}

func TestRequestReceiptDurableSubmissionAndReopen(t *testing.T) {
	directory := t.TempDir()
	writer := receiptRegressionWriter(t, directory)
	request := receiptRegressionRequest()
	first, err := writer.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.Submit(context.Background(), request)
	if err != nil || first != second {
		t.Fatalf("same request did not reuse its receipt: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != requestSlotName(request) {
		t.Fatalf("submission left unexpected artifacts: entries=%d, error=%v", len(entries), err)
	}
	info, err := os.Stat(filepath.Join(directory, entries[0].Name()))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("request permissions: %v", err)
	}
	root, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := ReadRequest(root, entries[0].Name())
	_ = root.Close()
	if err != nil || observed != request {
		t.Fatalf("mediator reader rejected published request: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, requestSlotName(request))); err != nil {
		t.Fatal("Close retracted a durable request")
	}
	reopened := receiptRegressionWriter(t, directory)
	receipt, err := reopened.Submit(context.Background(), request)
	if err != nil || receipt == first {
		t.Fatalf("new writer did not recover a distinct local receipt: %v", err)
	}
	if err := reopened.Withdraw(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Withdraw(context.Background(), receipt); err != nil {
		t.Fatal("withdrawal retry was not idempotent")
	}
	if _, err := os.Stat(filepath.Join(directory, requestSlotName(request))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request still exists after withdrawal: %v", err)
	}
}

func TestRequestReceiptCannotRemoveAnotherWritersReplacement(t *testing.T) {
	directory := t.TempDir()
	firstWriter := receiptRegressionWriter(t, directory)
	secondWriter := receiptRegressionWriter(t, directory)
	oldRequest := receiptRegressionRequest()
	old, err := firstWriter.Submit(context.Background(), oldRequest)
	if err != nil {
		t.Fatal(err)
	}
	cooperating, err := secondWriter.Submit(context.Background(), oldRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstWriter.Withdraw(context.Background(), cooperating); !errors.Is(err, ErrRequestReceiptStale) {
		t.Fatal("a receipt from another writer was accepted")
	}
	if err := secondWriter.Withdraw(context.Background(), cooperating); err != nil {
		t.Fatal(err)
	}
	newRequest := oldRequest
	newRequest.WorkloadID = "job:2"
	if requestSlotName(newRequest) != requestSlotName(oldRequest) {
		t.Fatal("slot identity unexpectedly includes the workload")
	}
	current, err := secondWriter.Submit(context.Background(), newRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstWriter.Withdraw(context.Background(), old); !errors.Is(err, ErrRequestReceiptStale) {
		t.Fatalf("old receipt was not rejected: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(directory, requestSlotName(newRequest)))
	observed, parseErr := ParseRequest(body)
	if err != nil || parseErr != nil || observed != newRequest {
		t.Fatal("old receipt damaged the new request")
	}
	if _, err := firstWriter.Submit(context.Background(), oldRequest); !errors.Is(err, ErrRequestConflict) {
		t.Fatal("old request overwrote the replacement")
	}
	if err := secondWriter.Withdraw(context.Background(), current); err != nil {
		t.Fatal(err)
	}
}

func TestRequestReceiptRetiresWhenIdenticalIntentUsesNewInode(t *testing.T) {
	directory := t.TempDir()
	firstWriter := receiptRegressionWriter(t, directory)
	secondWriter := receiptRegressionWriter(t, directory)
	request := receiptRegressionRequest()
	old, err := firstWriter.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	other, err := secondWriter.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := secondWriter.Withdraw(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := secondWriter.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	current, err := firstWriter.Submit(context.Background(), request)
	if err != nil || current == old {
		t.Fatalf("recreated intent reused stale authority: %v", err)
	}
	if err := firstWriter.Withdraw(context.Background(), old); !errors.Is(err, ErrRequestReceiptStale) {
		t.Fatal("retired receipt could still revoke")
	}
	if err := firstWriter.Withdraw(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	if _, err := secondWriter.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := firstWriter.Withdraw(context.Background(), current); err != nil {
		t.Fatal("completed withdrawal is not idempotent")
	}
	if _, err := os.Stat(filepath.Join(directory, requestSlotName(request))); err != nil {
		t.Fatal("old completed withdrawal removed a later request")
	}
}

func TestRequestReceiptRejectsLinksFIFOAndSharedPermissions(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "shared-file"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			writer := receiptRegressionWriter(t, directory)
			request := receiptRegressionRequest()
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "untouched.json")
			if err := os.WriteFile(target, body, 0600); err != nil {
				t.Fatal(err)
			}
			slot := filepath.Join(directory, requestSlotName(request))
			switch kind {
			case "symlink":
				err = os.Symlink(target, slot)
			case "hardlink":
				err = os.Link(target, slot)
			case "fifo":
				err = syscall.Mkfifo(slot, 0600)
			case "shared-file":
				err = os.WriteFile(slot, body, 0600)
				if err == nil {
					err = os.Chmod(slot, 0644)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Submit(context.Background(), request); err == nil {
				t.Fatal("unsafe slot was accepted")
			}
			after, err := os.ReadFile(target)
			if err != nil || string(after) != string(body) {
				t.Fatal("rejected submission modified the link target")
			}
		})
	}
}

func TestRequestReceiptRejectsDirectoryReplacementAndSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "lease")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writer := receiptRegressionWriter(t, directory)
	moved := filepath.Join(parent, "old")
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Submit(context.Background(), receiptRegressionRequest()); err == nil {
		t.Fatal("writer silently followed a replacement directory")
	}
	for _, path := range []string{directory, moved} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("directory replacement caused a write")
		}
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenRequestWriter(alias); err == nil {
		_ = candidate.Close()
		t.Fatal("symlink root was accepted")
	}
	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenRequestWriter(directory); err == nil {
		_ = candidate.Close()
		t.Fatal("shared root was accepted")
	}
}

func TestRequestReceiptConcurrentWritersProduceOneIntent(t *testing.T) {
	directory := t.TempDir()
	const workers = 8
	writers := make([]*RequestWriter, workers)
	for i := range writers {
		writers[i] = receiptRegressionWriter(t, directory)
	}
	var wait sync.WaitGroup
	failures := make(chan error, workers)
	for _, writer := range writers {
		wait.Add(1)
		go func(writer *RequestWriter) {
			defer wait.Done()
			_, err := writer.Submit(context.Background(), receiptRegressionRequest())
			failures <- err
		}(writer)
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != requestSlotName(receiptRegressionRequest()) {
		t.Fatalf("concurrent writes left duplicate/temporary artifacts: %v", err)
	}
}

func TestRequestReceiptBoundsDirectoryAndRejectsZeroWriter(t *testing.T) {
	var zero RequestWriter
	if _, err := zero.Submit(context.Background(), receiptRegressionRequest()); !errors.Is(err, ErrRequestWriterUnavailable) {
		t.Fatal("zero-value writer did not fail closed")
	}
	if err := zero.Close(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer := receiptRegressionWriter(t, directory)
	for i := 0; i < 256; i++ {
		if err := os.WriteFile(filepath.Join(directory, ".unrelated-"+strconv.Itoa(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Submit(context.Background(), receiptRegressionRequest()); err == nil {
		t.Fatal("full request directory admitted another intent")
	}
	if _, err := os.Stat(filepath.Join(directory, requestSlotName(receiptRegressionRequest()))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected admission left a request")
	}
}
