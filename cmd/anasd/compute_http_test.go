package main

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/runner"
)

// INCUS-R-145: every registered workspace is reconciled each pass, and a
// failing or rejecting workspace is logged once per state, not every pass.
func TestComputeHTTPMediatorReconcilesEveryWorkspaceAndLogsChangesOnce(t *testing.T) {
	var logs []string
	calls := map[string]int{}
	m := newComputeHTTPMediator([]string{"/srv/a", "/srv/b"}, func(format string, args ...any) { logs = append(logs, format) })
	failing := true
	m.reconcile = func(_ context.Context, workspace string) (runner.ComputeHTTPReconcileResult, error) {
		calls[workspace]++
		if workspace == "/srv/a" && failing {
			return runner.ComputeHTTPReconcileResult{}, errors.New("HTTP authorization requires a fully active running deployment")
		}
		return runner.ComputeHTTPReconcileResult{Published: 1}, nil
	}
	m.pass(context.Background())
	m.pass(context.Background())
	if calls["/srv/a"] != 2 || calls["/srv/b"] != 2 || len(logs) != 1 {
		t.Fatalf("calls = %v, logs = %v", calls, logs)
	}
	failing = false
	m.pass(context.Background())
	failing = true
	m.pass(context.Background())
	if len(logs) != 2 {
		t.Fatalf("a recurring failure was not reported again after recovery: %v", logs)
	}
}

func TestComputeHTTPMediatorStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := newComputeHTTPMediator([]string{"/srv/a"}, nil)
	m.reconcile = func(context.Context, string) (runner.ComputeHTTPReconcileResult, error) {
		cancel()
		return runner.ComputeHTTPReconcileResult{}, nil
	}
	m.Run(ctx) // returns instead of sleeping forever
}
