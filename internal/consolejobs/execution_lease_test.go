package consolejobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestExecutionLeaseIsExclusiveUntilReleased(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	first, err := AcquireExecutionLease(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	waitContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second, err := AcquireExecutionLease(waitContext, directory)
	if second != nil {
		_ = second.Close()
		t.Fatal("contended execution lease unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended execution lease error = %v, want deadline exceeded", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	afterRelease, err := AcquireExecutionLease(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := afterRelease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionLeaseRejectsNilContext(t *testing.T) {
	lease, err := AcquireExecutionLease(nil, filepath.Join(t.TempDir(), "jobs"))
	if lease != nil {
		_ = lease.Close()
		t.Fatal("nil context unexpectedly acquired execution lease")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil context error = %v, want ErrInvalid", err)
	}
}
