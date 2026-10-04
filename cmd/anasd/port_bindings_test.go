package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
	"github.com/anas-project/ANAS/internal/runtimeissues"
)

func testPortWatch(t *testing.T, host *recordingSync) (*portBindingWatch, *sync.Map, string) {
	t.Helper()
	path := t.TempDir()
	identities := &sync.Map{}
	identities.Store(path, "a")
	w := newPortBindingWatch(host, []portWorkspace{{ID: "main", Path: path}}, nil)
	w.approved = func() bool { return true }
	w.activation = func(p string) string {
		value, _ := identities.Load(p)
		return value.(string)
	}
	w.check = func(context.Context) ([]incusprovision.PortBinding, []incusprovision.PortFinding, error) {
		return nil, nil, nil
	}
	w.events = func(ctx context.Context) (<-chan struct{}, <-chan error) {
		return make(chan struct{}), make(chan error)
	}
	w.poll, w.interval, w.debounce = 5*time.Millisecond, time.Hour, time.Millisecond
	return w, identities, path
}

// INCUS-R-152: a sync when anasd starts and after each activation, from
// either the console or the CLI; nothing before configure approved it.
func TestPortSyncFollowsActivations(t *testing.T) {
	host := &recordingSync{}
	w, identities, path := testPortWatch(t, host)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	waitFor(t, func() bool { return host.count() == 1 })
	identities.Store(path, "b")
	waitFor(t, func() bool { return host.count() == 2 })
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	if host.count() != 2 || host.actions[0] != "main/"+hostaction.ActionPortsSync {
		t.Fatalf("actions = %v", host.actions)
	}

	quiet := &recordingSync{}
	unapproved, _, _ := testPortWatch(t, quiet)
	unapproved.approved = func() bool { return false }
	quietCtx, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	unapproved.Run(quietCtx)
	if quiet.count() != 0 {
		t.Fatal("a host without the configure approval was asked to sync")
	}
}

// INCUS-R-162: findings become the workspace's runtime issues, one record
// per binding, and resolve when a later check no longer finds them. A check
// that fails leaves the records alone.
func TestPortCheckRecordsAndResolvesIssues(t *testing.T) {
	w, _, path := testPortWatch(t, &recordingSync{})
	binding := incusprovision.PortBinding{Workspace: "main", Lease: "forgejo.runners", Protocol: "tcp", HostPort: 30022}
	w.check = func(context.Context) ([]incusprovision.PortBinding, []incusprovision.PortFinding, error) {
		return []incusprovision.PortBinding{binding}, []incusprovision.PortFinding{{Binding: binding, Reason: "hold_lost"}, {Binding: binding, Reason: "rules_drifted"}}, nil
	}
	w.checkOnce(context.Background())
	store := runtimeissues.WorkspacePath(path)
	issues, err := runtimeissues.Load(store)
	if err != nil || len(issues) != 1 || issues[0].Key != "incus.port-binding/forgejo.runners/tcp/30022" || issues[0].Source != runtimeissues.SourceAnasd ||
		issues[0].Message != "applied port binding forgejo.runners/tcp/30022: hold_lost, rules_drifted" {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	w.check = func(context.Context) ([]incusprovision.PortBinding, []incusprovision.PortFinding, error) {
		return nil, nil, errors.New("nft unavailable")
	}
	w.checkOnce(context.Background())
	if issues, _ = runtimeissues.Load(store); !issues[0].Open() {
		t.Fatal("a failed check resolved a record")
	}
	w.check = func(context.Context) ([]incusprovision.PortBinding, []incusprovision.PortFinding, error) {
		return []incusprovision.PortBinding{binding}, nil, nil
	}
	w.checkOnce(context.Background())
	if issues, _ = runtimeissues.Load(store); issues[0].Open() {
		t.Fatal("a cleared finding stayed open")
	}
	if filepath.Dir(store) != filepath.Join(path, ".anas", "state") {
		t.Fatalf("store = %s", store)
	}
}

// A container start triggers a check without waiting for the interval.
func TestPortCheckRunsOnContainerStart(t *testing.T) {
	w, _, _ := testPortWatch(t, &recordingSync{})
	starts := make(chan struct{}, 1)
	w.events = func(context.Context) (<-chan struct{}, <-chan error) { return starts, make(chan error) }
	var mu sync.Mutex
	checks := 0
	w.check = func(context.Context) ([]incusprovision.PortBinding, []incusprovision.PortFinding, error) {
		mu.Lock()
		checks++
		mu.Unlock()
		return nil, nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	// The first tick after start runs the initial check.
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return checks == 1 })
	starts <- struct{}{}
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return checks == 2 })
	cancel()
	<-done
}
