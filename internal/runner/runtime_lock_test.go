package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRuntimeLockContextCanBeCanceledWhileContended(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".anas")
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	secondUnlock, err := acquireRuntimeLockContext(ctx, base)
	if secondUnlock != nil {
		secondUnlock()
		t.Fatal("contended lock unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire contended lock error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled lock acquisition took %v", elapsed)
	}
}

func TestAcquireRuntimeLockContextSucceedsAfterRelease(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".anas")
	firstUnlock, err := acquireRuntimeLock(base)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		unlock, lockErr := acquireRuntimeLockContext(ctx, base)
		if lockErr == nil {
			unlock()
		}
		result <- lockErr
	}()
	time.Sleep(2 * runtimeLockRetryDelay)
	firstUnlock()

	if err := <-result; err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestAcquireRuntimeLockCreatesPrivateLockFile(t *testing.T) {
	base := filepath.Join(t.TempDir(), ".anas")
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	unlock()

	info, err := os.Stat(filepath.Join(base, "state", "lock"))
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("lock mode = %04o, want 0600", got)
	}
}

func TestAcquireRuntimeLockContextRejectsNilContext(t *testing.T) {
	unlock, err := acquireRuntimeLockContext(nil, filepath.Join(t.TempDir(), ".anas"))
	if unlock != nil {
		unlock()
		t.Fatal("nil context unexpectedly acquired lock")
	}
	if err == nil {
		t.Fatal("nil context unexpectedly succeeded")
	}
}
