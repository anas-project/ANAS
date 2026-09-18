package consolejobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRetainedExecutionLeaseCannotTransferOwnership(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	lease, err := AcquireExecutionLease(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	first, err := lease.Retain()
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := lease.Retain()
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	if !errors.Is(lease.Close(), ErrExecutionRetained) {
		t.Fatal("retained lease was closed")
	}
	first()
	first() // Release is idempotent and cannot decrement a different guard.
	if !errors.Is(lease.Close(), ErrExecutionRetained) {
		t.Fatal("second supervisor lost execution ownership")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	other, err := AcquireExecutionLease(ctx, directory)
	if other != nil {
		_ = other.Close()
		t.Fatal("another owner acquired the retained lease")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("contended lease did not observe its context")
	}
	second()
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	other, err = AcquireExecutionLease(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if release, err := lease.Retain(); release != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed lease was retained")
	}
}

func TestNilExecutionLeaseCannotBeRetained(t *testing.T) {
	var lease *ExecutionLease
	if release, err := lease.Retain(); release != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatal("nil lease was retained")
	}
}
