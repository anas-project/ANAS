package runner

import (
	"context"
	"errors"
	"github.com/anas-project/ANAS/internal/application"
	"os"
	"path/filepath"
	"testing"
)

// QUALITY-R-003: an unresolved transaction must never look like clean recovery.
func TestDaemonCompensationReturnsRecoveryFailure(t *testing.T) {
	for _, mode := range []string{"docker-failure", "unreadable", "success", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			base := stateDir(workspace)
			bin := t.TempDir()
			for _, name := range []string{"docker", "docker-compose"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			if err := os.MkdirAll(transactionsDir(base), 0700); err != nil {
				t.Fatal(err)
			}
			txn := containerTransaction{APIVersion: activeStateVersion, ID: "test", Kind: containerTransactionKind, State: containerTransactionStopped}
			if mode == "docker-failure" {
				txn.Modules = []string{"example"}
			}
			path := transactionPath(base, txn.ID)
			if err := writeYAMLAtomic(path, &txn, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "unreadable" {
				if err := os.WriteFile(path, []byte("[invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			service := &workspaceDeploymentPlanApplication{workspace: workspace, daemon: true, events: application.NopEventSink{}}
			err := service.CheckCompensation(ctx)
			if mode == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("completed transaction remains: %v", err)
				}
				if err := service.CheckCompensation(ctx); err != nil {
					t.Fatalf("repeat recovery: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("unresolved recovery returned success")
				}
				if _, err = os.Stat(path); err != nil {
					t.Fatalf("lost pending transaction: %v", err)
				}
			}
		})
	}
}
