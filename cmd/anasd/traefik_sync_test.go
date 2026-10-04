package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

type recordingSync struct {
	mu      sync.Mutex
	actions []string
}

func (r *recordingSync) InvokeSync(_ context.Context, workspace, action string) (consolejobs.CreateResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, workspace+"/"+action)
	return consolejobs.CreateResult{}, nil
}

func (r *recordingSync) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.actions)
}

// INCUS-R-118: a sync on start, one per burst of Traefik starts, and another
// after the event stream reconnects; nothing before configure approved it.
func TestTraefikSyncTriggers(t *testing.T) {
	host := &recordingSync{}
	starts := make(chan struct{}, 4)
	streams := 0
	trigger := newTraefikSyncTrigger(host, "main", nil)
	trigger.approved = func() bool { return true }
	trigger.debounce, trigger.backoff = 10*time.Millisecond, 10*time.Millisecond
	trigger.events = func(ctx context.Context) (<-chan struct{}, <-chan error) {
		streams++
		if streams == 1 {
			return starts, make(chan error)
		}
		<-ctx.Done()
		closed := make(chan struct{})
		close(closed)
		return closed, make(chan error)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { trigger.Run(ctx); close(done) }()
	waitFor(t, func() bool { return host.count() == 1 })
	starts <- struct{}{}
	starts <- struct{}{}
	waitFor(t, func() bool { return host.count() == 2 })
	close(starts)
	waitFor(t, func() bool { return host.count() == 3 })
	cancel()
	<-done
	if host.actions[0] != "main/"+hostaction.ActionTraefikSync {
		t.Fatalf("actions = %v", host.actions)
	}

	unapproved := &recordingSync{}
	quiet := newTraefikSyncTrigger(unapproved, "main", nil)
	quiet.approved = func() bool { return false }
	quiet.events = func(ctx context.Context) (<-chan struct{}, <-chan error) {
		<-ctx.Done()
		closed := make(chan struct{})
		close(closed)
		return closed, make(chan error)
	}
	quietCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	quiet.Run(quietCtx)
	if unapproved.count() != 0 {
		t.Fatal("a host without the configure approval was asked to sync")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
