//go:build unix

package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the real Hook process group while cleanup is in progress. A
// process handling SIGTERM may exit zero; that is not an acknowledgment of
// completed cleanup and cannot authorize Compose down or dependency removal.
func TestCanceledRunningStopHookCannotAuthorizeRemoval(t *testing.T) {
	a, release, composeLog := stopBarrierFixture(t, false)
	dir := filepath.Join(release, "worker")
	files := map[string]string{
		"hook":       "#!/bin/sh\ncat >/dev/null\ntrap 'exit 0' TERM\n/bin/sh child-hook &\nwait\nprintf '{}'\n",
		"child-hook": "#!/bin/sh\ntrap 'printf canceled > child-terminated; exit 0' TERM\nprintf ready > child-ready\nwhile :; do sleep 1; done\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.commandContext = ctx
	done := make(chan error, 1)
	go func() { done <- a.stopModules(release, a.order, false) }()
	// Cancellation is issued only after the descendant has installed its
	// handler. This is not the already-canceled-before-execution test.
	deadline := time.After(8 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
ready:
	for {
		select {
		case err := <-done:
			t.Fatalf("stop returned before its running child was observed: %v", err)
		case <-deadline:
			cancel()
			select {
			case <-done:
			case <-time.After(6 * time.Second):
			}
			t.Fatal("stop Hook descendant did not become ready")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(dir, "child-ready")); err == nil {
				break ready
			}
		}
	}
	cancel()
	select {
	case err := <-done:
		var failure *stopBarrierFailure
		if !errors.As(err, &failure) || failure.Module != "worker" {
			t.Fatalf("canceled cleanup was not retained as a stop-barrier failure: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("cancellation did not bound the running Hook process group")
	}
	if body, err := os.ReadFile(filepath.Join(dir, "child-terminated")); err != nil || string(body) != "canceled" {
		t.Fatal("cancellation did not reach the actual Hook descendant", err)
	}
	if _, err := os.Stat(composeLog); !os.IsNotExist(err) {
		t.Fatal("a zero-exit canceled Hook authorized Compose or dependency cleanup")
	}
}
