package consoleauth

import (
	"context"
	"time"
)

const defaultTransactionConvergenceTimeout = 30 * time.Second

// transactionConvergenceContext preserves request-scoped values while
// detaching a durable authentication transaction from request cancellation.
// Once a transaction WAL exists, the authentication and capability stores
// must converge even if the client disconnects; the independent deadline
// keeps that convergence work bounded.
func (store *Store) transactionConvergenceContext(requestContext context.Context) (context.Context, context.CancelFunc) {
	timeout := store.transactionTimeout
	if timeout <= 0 {
		timeout = defaultTransactionConvergenceTimeout
	}
	return context.WithTimeout(context.WithoutCancel(requestContext), timeout)
}
