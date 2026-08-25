//go:build unix

package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestModuleCommandLocksHonorContextAndConflictScopes(t *testing.T) {
	workspace := t.TempDir()
	unlock, err := acquireModuleCommandLock(context.Background(), workspace, "demo", "module_write")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := acquireModuleCommandLock(ctx, workspace, "demo", "module_read"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("conflicting lock error = %v", err)
	}

	otherUnlock, err := acquireModuleCommandLock(context.Background(), workspace, "other", "module_write")
	if err != nil {
		t.Fatalf("independent module lock: %v", err)
	}
	otherUnlock()
}
