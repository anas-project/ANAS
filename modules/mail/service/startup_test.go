package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthorizationDependencyRetriesUntilVerifiedHandler(t *testing.T) {
	calls := 0
	h, err := awaitAuthorization(t.Context(), time.Millisecond, func(ctx context.Context) (http.Handler, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Fatal("bootstrap attempt has no bounded deadline")
		}
		if calls < 3 {
			return nil, fmt.Errorf("%w: IAM discovery", errAuthorizationDependency)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), nil
	})
	if err != nil || h == nil || calls != 3 {
		t.Fatal("temporary outage terminated startup or skipped verification")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 204 {
		t.Fatal("wrong authorization handler published")
	}
}

func TestAuthorizationInvalidBindingsAreFatalAndDiscardPartialHandler(t *testing.T) {
	want := errors.New("invalid local binding")
	calls := 0
	h, err := awaitAuthorization(t.Context(), time.Millisecond, func(context.Context) (http.Handler, error) {
		calls++
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("partial handler used") }), want
	})
	if h != nil || !errors.Is(err, want) || calls != 1 {
		t.Fatal("invalid binding was retried or published")
	}
}

func TestAuthorizationWaitStopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	h, err := awaitAuthorization(ctx, time.Hour, func(context.Context) (http.Handler, error) {
		calls++
		cancel()
		return nil, errAuthorizationDependency
	})
	if h != nil || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("shutdown did not stop initialization wait")
	}
}
