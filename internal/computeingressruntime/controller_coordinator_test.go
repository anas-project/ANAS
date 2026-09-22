package computeingressruntime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func awaitControllerChange(t *testing.T, change *ControllerChange) error {
	t.Helper()
	select {
	case <-change.done:
	case <-time.After(5 * time.Second):
		t.Fatal("configuration barrier did not finish")
	}
	ready, err := change.Poll()
	if !ready {
		t.Fatal("completed change not ready")
	}
	return err
}

func TestControllerCoordinatorFencesLaunchThroughConfigurationCompletion(t *testing.T) {
	c, err := NewControllerCoordinator([]string{"main", "other"})
	if err != nil {
		t.Fatal(err)
	}
	owner, store, _, effects := controllerServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx, "main", owner); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Ready())
	change, err := c.BeginChange(ctx, []string{"main"})
	if err != nil || awaitControllerChange(t, change) != nil {
		t.Fatal("drain", err)
	}
	if effects.closed.Load() != 1 || owner.Phase() != ControllerStopped {
		t.Fatal("readers not drained")
	}
	state := readLifecycleState(t, store)
	if len(state.Publications) != 0 || len(state.Retired) != 1 {
		t.Fatal("missing durable retirement")
	}
	next, _, _, _ := controllerServiceFixture(t)
	if err := c.Start(ctx, "main", next); !errors.Is(err, ErrControllerChange) {
		t.Fatal("start bypassed held configuration fence", err)
	}
	if next.Phase() != ControllerNew {
		t.Fatal("rejected start consumed the new owner")
	}
	if _, err := c.BeginChange(ctx, []string{"main"}); !errors.Is(err, ErrControllerChange) {
		t.Fatal("overlap admitted", err)
	}
	other, err := c.BeginChange(ctx, []string{"other"})
	if err != nil || awaitControllerChange(t, other) != nil {
		t.Fatal("unrelated workspace blocked", err)
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}
	if err := change.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := change.Poll(); !errors.Is(err, ErrControllerChange) {
		t.Fatal("released proof reusable")
	}
	if err := c.Start(ctx, "main", next); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, next.Ready())
	last, err := c.BeginChange(ctx, []string{"main"})
	if err != nil || awaitControllerChange(t, last) != nil {
		t.Fatal(err)
	}
	if err := last.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerCoordinatorFailedDrainRequiresNewExplicitChange(t *testing.T) {
	c, _ := NewControllerCoordinator([]string{"main"})
	owner, store, _, effects := controllerServiceFixture(t)
	ctx := context.Background()
	if err := c.Start(ctx, "main", owner); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Ready())
	effects.failRemove.Store(true)
	first, err := c.BeginChange(ctx, []string{"main"})
	if err != nil || !errors.Is(awaitControllerChange(t, first), ErrControllerDrain) {
		t.Fatal(err)
	}
	assertControllerLeaseHeld(t, store)
	count := effects.removals.Load()
	for range 5 {
		if ready, err := first.Poll(); !ready || !errors.Is(err, ErrControllerDrain) {
			t.Fatal("failed attempt changed")
		}
	}
	if effects.removals.Load() != count || effects.closed.Load() != 0 {
		t.Fatal("poll retried or released resources")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	replacement, _, _, _ := controllerServiceFixture(t)
	if err := c.Start(ctx, "main", replacement); err == nil {
		t.Fatal("failed owner replaced")
	}
	effects.failRemove.Store(false)
	second, err := c.BeginChange(ctx, []string{"main"})
	if err != nil || awaitControllerChange(t, second) != nil {
		t.Fatal("explicit drain retry", err)
	}
	if effects.removals.Load() != count+1 || effects.closed.Load() != 1 {
		t.Fatal("retry did not use original owner")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerCoordinatorCanceledWaitCannotReleaseActiveFence(t *testing.T) {
	c, _ := NewControllerCoordinator([]string{"main"})
	owner, _, _, effects := controllerServiceFixture(t)
	entered, release := make(chan context.Context, 1), make(chan struct{})
	effects.entered, effects.block = entered, release
	if err := c.Start(context.Background(), "main", owner); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Ready())
	ctx, cancel := context.WithCancel(context.Background())
	change, err := c.BeginChange(ctx, []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	var drainCtx context.Context
	select {
	case drainCtx = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain not started")
	}
	cancel()
	if drainCtx.Err() != nil {
		t.Fatal("caller canceled independent cleanup")
	}
	if ready, err := change.Poll(); ready || err != nil {
		t.Fatal("premature completion", err)
	}
	if err := change.Release(); !errors.Is(err, ErrControllerDrain) {
		t.Fatal("released while active", err)
	}
	close(release)
	if err := awaitControllerChange(t, change); err != nil {
		t.Fatal(err)
	}
	if err := change.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerCoordinatorRejectsUnknownDuplicateAndCanceledScopes(t *testing.T) {
	for _, ids := range [][]string{nil, {""}, {"main", "main"}} {
		if _, err := NewControllerCoordinator(ids); err == nil {
			t.Fatal("invalid scope registry accepted")
		}
	}
	c, _ := NewControllerCoordinator([]string{"main", "other"})
	if !c.MatchesScopes([]string{"other", "main"}) || c.MatchesScopes([]string{"main", "main"}) || c.MatchesScopes([]string{"main"}) {
		t.Fatal("scope registration comparison is incomplete")
	}
	for _, ids := range [][]string{nil, {"absent"}, {"main", "main"}, {"main", "absent"}} {
		if _, err := c.BeginChange(context.Background(), ids); err == nil {
			t.Fatal("invalid change accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.BeginChange(ctx, []string{"main"}); err == nil {
		t.Fatal("canceled change admitted")
	}
	change, err := c.BeginChange(context.Background(), []string{"main", "other"})
	if err != nil || awaitControllerChange(t, change) != nil {
		t.Fatal("invalid attempt partially fenced scopes", err)
	}
	if err := change.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerCoordinatorStopsAllScopesBeforeWaitingForSlowScope(t *testing.T) {
	c, _ := NewControllerCoordinator([]string{"main", "other"})
	first, _, _, effects := controllerServiceFixture(t)
	second, _, _, otherEffects := controllerServiceFixture(t)
	entered, release := make(chan context.Context, 1), make(chan struct{})
	effects.entered, effects.block = entered, release
	for id, owner := range map[string]*ControllerService{"main": first, "other": second} {
		if err := c.Start(context.Background(), id, owner); err != nil {
			t.Fatal(err)
		}
		awaitServiceSignal(t, owner.Ready())
	}
	change, err := c.BeginChange(context.Background(), []string{"main", "other"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first scope not draining")
	}
	awaitServiceSignal(t, second.Done())
	if otherEffects.closed.Load() != 1 || effects.closed.Load() != 0 {
		t.Fatal("slow scope prevented independent retirement")
	}
	if ready, _ := change.Poll(); ready {
		t.Fatal("incomplete global drain succeeded")
	}
	close(release)
	if err := awaitControllerChange(t, change); err != nil {
		t.Fatal(err)
	}
	if err := change.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestControllerCoordinatorShutdownNeverAutomaticallyRetriesOrReopens(t *testing.T) {
	c, _ := NewControllerCoordinator([]string{"main"})
	owner, store, _, effects := controllerServiceFixture(t)
	if err := c.Start(context.Background(), "main", owner); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Ready())
	effects.failRemove.Store(true)
	shutdown, err := c.BeginShutdown(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := awaitControllerChange(t, shutdown); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	assertControllerLeaseHeld(t, store)
	count := effects.removals.Load()
	for range 4 {
		same, err := c.BeginShutdown(context.Background())
		if err != nil || same != shutdown {
			t.Fatal("shutdown retried on poll", err)
		}
	}
	if count != effects.removals.Load() {
		t.Fatal("failed drain retried implicitly")
	}
	if err := shutdown.Release(); !errors.Is(err, ErrControllerChange) {
		t.Fatal("shutdown fence released", err)
	}
	effects.failRemove.Store(false)
	retry, err := c.RetryShutdown(context.Background())
	if err != nil || retry == shutdown {
		t.Fatal("explicit retry missing", err)
	}
	if err := awaitControllerChange(t, retry); err != nil {
		t.Fatal(err)
	}
	if _, err := shutdown.Poll(); !errors.Is(err, ErrControllerDrain) {
		t.Fatal("old attempt mutated by retry")
	}
	replacement, _, _, _ := controllerServiceFixture(t)
	if err := c.Start(context.Background(), "main", replacement); !errors.Is(err, ErrControllerChange) {
		t.Fatal("shutdown reopened launches", err)
	}
	if _, err := c.BeginChange(context.Background(), []string{"main"}); !errors.Is(err, ErrControllerChange) {
		t.Fatal("shutdown admitted config write", err)
	}
}
