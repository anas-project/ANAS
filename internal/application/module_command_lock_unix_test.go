//go:build unix

package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestModuleCommandLockRejectsPendingConfigTransaction(t *testing.T) {
	workspace := t.TempDir()
	transactionPath := filepath.Join(workspace, ".anas", "state", "config-write-transaction")
	if err := os.MkdirAll(transactionPath, 0700); err != nil {
		t.Fatal(err)
	}
	if unlock, err := acquireModuleCommandLock(context.Background(), workspace, "demo", "module_read"); err == nil {
		unlock()
		t.Fatal("module command lock accepted a pending configuration transaction")
	} else if !errors.Is(err, errModuleCommandConfigRecoveryRequired) {
		t.Fatalf("pending transaction error = %v", err)
	}
}

func TestInvokeModuleCommandMapsPendingConfigTransactionToRecoveryRequired(t *testing.T) {
	workspace, command := writeModuleCommandApplicationFixture(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"result\",\"changed\":false,\"result\":{}}'\n")
	transactionPath := filepath.Join(workspace, ".anas", "state", "config-write-transaction")
	if err := os.MkdirAll(transactionPath, 0700); err != nil {
		t.Fatal(err)
	}
	_, err := NewService(workspace).InvokeModuleCommand(context.Background(), InvokeModuleCommandRequest{
		Module: "demo", Command: "repair", Parameters: map[string]any{"enabled": true},
		CommandDigest: command.Digest, Confirmed: true,
	})
	applicationError := requireApplicationError(t, err)
	if applicationError.Kind != ErrorKindInternal || applicationError.Code != "config_recovery_required" {
		t.Fatalf("application error = %#v", applicationError)
	}
}
