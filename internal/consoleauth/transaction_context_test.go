package consoleauth

import (
	"context"
	"testing"
	"time"
)

func requireBoundedConvergenceContext(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := ctx.Err(); err != nil {
		t.Fatalf("convergence context is already canceled: %v", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("convergence context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > defaultTransactionConvergenceTimeout {
		t.Fatalf("convergence context deadline remaining = %s", remaining)
	}
}
