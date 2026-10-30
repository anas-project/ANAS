package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Only dependency failures retry. Invalid local bindings remain fatal, and a
// partly initialized handler is never returned to a listener.
func awaitAuthorization(ctx context.Context, delay time.Duration, initialize func(context.Context) (http.Handler, error)) (http.Handler, error) {
	warned := false
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		h, err := initialize(attempt)
		cancel()
		if err == nil {
			return h, nil
		}
		if !errors.Is(err, errAuthorizationDependency) {
			return nil, err
		}
		if !warned {
			fmt.Fprintln(os.Stderr, "mail authorization dependencies unavailable; pending withdrawals continue")
			warned = true
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
