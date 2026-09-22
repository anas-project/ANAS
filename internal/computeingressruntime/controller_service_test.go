package computeingressruntime

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

type serviceDesired struct {
	target  PublicationTarget
	reads   atomic.Int32
	invalid atomic.Bool
}

func (s *serviceDesired) ReadDesired(ctx context.Context, _ Journal) (DesiredSnapshot, error) {
	s.reads.Add(1)
	if s.invalid.Load() {
		return DesiredSnapshot{}, errors.New("fixture unavailable source")
	}
	return DesiredSnapshot{Epoch: s.target.Epoch, Targets: []PublicationTarget{s.target}}, ctx.Err()
}
func (s *serviceDesired) ValidateAuthorization(ctx context.Context, target PublicationTarget) error {
	if target != s.target || s.invalid.Load() {
		return errors.New("fixture invalid authority")
	}
	return ctx.Err()
}
func (*serviceDesired) AfterRetirement() {}

type serviceEffects struct {
	*executorFixture
	failRemove atomic.Bool
	removals   atomic.Int32
	closed     atomic.Int32
	closeError bool
	entered    chan context.Context
	block      <-chan struct{}
}

func (f *serviceEffects) RemoveHTTP(ctx context.Context, target PublicationTarget) error {
	f.removals.Add(1)
	if f.entered != nil {
		f.entered <- ctx
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.failRemove.Load() {
		return errors.New("fixture private route failure")
	}
	return f.executorFixture.RemoveHTTP(ctx, target)
}
func (f *serviceEffects) Close() error {
	f.closed.Add(1)
	if f.closeError {
		return errors.New("fixture private release failure")
	}
	return nil
}

func controllerServiceFixture(t *testing.T) (*ControllerService, FileStateStore, *serviceDesired, *serviceEffects) {
	t.Helper()
	base, executor, target := newExecutorFixture()
	store := FileStateStore{Directory: t.TempDir()}
	if err := os.Chmod(store.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	source := &serviceDesired{target: target}
	effects := &serviceEffects{executorFixture: base}
	executor.Store, executor.Host, executor.Renderer = store, effects, effects
	c := Controller{Executor: executor, Source: source, Interval: time.Hour, OperationTimeout: time.Second, CleanupTimeout: time.Second}
	owner, err := NewControllerService(c, effects)
	if err != nil {
		t.Fatal(err)
	}
	return owner, store, source, effects
}

func startControllerService(t *testing.T, owner *ControllerService) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- owner.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done
}

func awaitServiceSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("controller lifecycle signal missing")
	}
}

func stopControllerService(t *testing.T, owner *ControllerService) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return owner.Stop(ctx)
}

func assertControllerLeaseHeld(t *testing.T, store FileStateStore) {
	t.Helper()
	if err := store.WithExclusive(context.Background(), func(Journal) error { t.Error("replacement acquired retained lease"); return nil }); !errors.Is(err, ErrExecutorBusy) {
		t.Fatalf("lease not retained: %v", err)
	}
}

func TestControllerServiceStartsExplicitlyAndReleasesOnlyAfterDrain(t *testing.T) {
	owner, store, source, effects := controllerServiceFixture(t)
	if owner.Phase() != ControllerNew || source.reads.Load() != 0 {
		t.Fatal("constructor started work")
	}
	entries, err := os.ReadDir(store.Directory)
	if err != nil || len(entries) != 0 {
		t.Fatal("constructor touched state")
	}
	if !errors.Is(owner.Stop(context.Background()), ErrControllerNotRunning) {
		t.Fatal("unstarted owner reported drained")
	}
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	if owner.Phase() != ControllerRunning {
		t.Fatal(owner.Phase())
	}
	assertControllerLeaseHeld(t, store)
	if err := stopControllerService(t, owner); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Done())
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if owner.Phase() != ControllerStopped || effects.closed.Load() != 1 {
		t.Fatal("resources outlived confirmed stop")
	}
	want := append(slices.Clone(openingSteps), closingSteps...)
	if !reflect.DeepEqual(effects.steps, want) {
		t.Fatalf("unsafe effect order %v", effects.steps)
	}
	saved := readLifecycleState(t, store)
	if len(saved.Publications) != 0 || len(saved.Retired) != 1 {
		t.Fatal("drain discarded receipt")
	}
	if err := owner.Stop(context.Background()); err != nil || effects.closed.Load() != 1 {
		t.Fatal("stop not idempotent", err)
	}
	if err := owner.Run(context.Background()); !errors.Is(err, ErrControllerNotRunning) {
		t.Fatal("single-use owner restarted")
	}
}

func TestControllerServiceFailedDrainRetainsLockReadersAndOriginalAttempt(t *testing.T) {
	owner, store, source, effects := controllerServiceFixture(t)
	effects.failRemove.Store(true)
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	if owner.Phase() != ControllerDrainFailed || effects.closed.Load() != 0 {
		t.Fatal("failed drain released reader")
	}
	assertControllerLeaseHeld(t, store)
	select {
	case <-owner.Done():
		t.Fatal("failed drain relinquished ownership")
	default:
	}
	removals := effects.removals.Load()
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerDrain) || effects.removals.Load() != removals {
		t.Fatal("stop automatically retried")
	}
	// Retry failure still cannot reopen work or release the original lease.
	if err := owner.RetryDrain(context.Background()); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	assertControllerLeaseHeld(t, store)
	effects.failRemove.Store(false)
	if err := owner.RetryDrain(context.Background()); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Done())
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if source.reads.Load() != 1 || effects.closed.Load() != 1 {
		t.Fatal("recovery republished or closed twice")
	}
	saved := readLifecycleState(t, store)
	if len(saved.Publications) != 0 || len(saved.Retired) != 1 {
		t.Fatal("retirement missing")
	}
}

func TestControllerServiceCanceledWaitCannotCancelDrain(t *testing.T) {
	owner, store, _, effects := controllerServiceFixture(t)
	block := make(chan struct{})
	effects.block, effects.entered = block, make(chan context.Context, 2)
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	wait, cancelWait := context.WithCancel(context.Background())
	stopResult := make(chan error, 1)
	go func() { stopResult <- owner.Stop(wait) }()
	var cleanup context.Context
	select {
	case cleanup = <-effects.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not start")
	}
	cancelWait()
	if err := <-stopResult; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if cleanup.Err() != nil || effects.closed.Load() != 0 {
		t.Fatal("wait cancellation ended retirement")
	}
	if _, ok := cleanup.Deadline(); !ok {
		t.Fatal("unbounded drain context")
	}
	assertControllerLeaseHeld(t, store)
	close(block)
	if err := stopControllerService(t, owner); err != nil {
		t.Fatal(err)
	}
	awaitServiceSignal(t, owner.Done())
	<-done
}

func TestControllerServiceNoReadinessBeforeRecoveryAndFirstReconcile(t *testing.T) {
	owner, store, source, effects := controllerServiceFixture(t)
	if err := store.WithExclusive(context.Background(), func(j Journal) error {
		return j.Save(context.Background(), ExecutorState{Schema: executorStateSchema, Publications: []AppliedPublication{{Target: source.target, AddressHeld: true}}})
	}); err != nil {
		t.Fatal(err)
	}
	effects.failRemove.Store(true)
	reported := make(chan struct{}, 8)
	owner.controller.Report = func(error) { reported <- struct{}{} }
	cancel, done := startControllerService(t, owner)
	awaitServiceSignal(t, reported)
	select {
	case <-owner.Ready():
		t.Fatal("unrecovered installation reported ready")
	default:
	}
	if source.reads.Load() != 0 {
		t.Fatal("startup consumed work before recovery")
	}
	cancel() // Parent shutdown has the same retained-drain semantics as Stop.
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	assertControllerLeaseHeld(t, store)
	effects.failRemove.Store(false)
	if err := owner.RetryDrain(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-done
	select {
	case <-owner.Ready():
		t.Fatal("cleanup-only retry signaled running")
	default:
	}
}

func TestControllerServiceLockFailureIsNotEmptyInstallation(t *testing.T) {
	first, store, _, _ := controllerServiceFixture(t)
	_, firstDone := startControllerService(t, first)
	awaitServiceSignal(t, first.Ready())
	second, _, _, resources := controllerServiceFixture(t)
	second.controller.Executor.Store = store
	if err := second.Run(context.Background()); !errors.Is(err, ErrExecutorBusy) {
		t.Fatal(err)
	}
	if err := second.Stop(context.Background()); !errors.Is(err, ErrControllerDrain) {
		t.Fatal("busy owner claimed successful drain")
	}
	if resources.closed.Load() != 0 {
		t.Fatal("closed resources without cleanup ownership")
	}
	select {
	case <-second.Ready():
		t.Fatal("busy owner ready")
	default:
	}
	if err := stopControllerService(t, first); err != nil {
		t.Fatal(err)
	}
	<-firstDone
}

func TestControllerServiceResourceReleaseFailureIsNotSuccessfulStop(t *testing.T) {
	owner, store, _, effects := controllerServiceFixture(t)
	effects.closeError = true
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerRelease) {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrControllerRelease) {
		t.Fatal(err)
	}
	if owner.Phase() != ControllerFailed || effects.closed.Load() != 1 {
		t.Fatal(owner.Phase())
	}
	if len(readLifecycleState(t, store).Publications) != 0 {
		t.Fatal("release preceded network retirement")
	}
}

func TestControllerServiceConcurrentDrainWaitersShareOneRetry(t *testing.T) {
	owner, store, _, effects := controllerServiceFixture(t)
	effects.failRemove.Store(true)
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	previous := effects.removals.Load()
	// Mutate the explicit fixture only while the owner is quarantined. Retry's
	// channel handoff orders these changes before the next cleanup operation.
	block := make(chan struct{})
	effects.block, effects.entered = block, make(chan context.Context, 8)
	effects.failRemove.Store(false)
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- owner.RetryDrain(context.Background()) }()
	}
	select {
	case <-effects.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not begin")
	}
	assertControllerLeaseHeld(t, store)
	close(block)
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if effects.removals.Load() != previous+1 || effects.closed.Load() != 1 {
		t.Fatal("concurrent callers duplicated cleanup")
	}
}

func TestControllerServiceFailedDesiredReadNeverSignalsReady(t *testing.T) {
	owner, _, source, effects := controllerServiceFixture(t)
	source.invalid.Store(true)
	reported := make(chan struct{}, 8)
	owner.controller.Report = func(error) { reported <- struct{}{} }
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, reported)
	select {
	case <-owner.Ready():
		t.Fatal("failed first read reported ready")
	default:
	}
	if err := stopControllerService(t, owner); err != nil {
		t.Fatal(err)
	}
	<-done
	if effects.closed.Load() != 1 || len(effects.steps) != 0 {
		t.Fatal("failed desired read published effects")
	}
}

func TestControllerServiceInvalidOrCanceledStartDoesNotTouchState(t *testing.T) {
	owner, store, _, effects := controllerServiceFixture(t)
	if _, err := NewControllerService(Controller{}, effects); err == nil {
		t.Fatal("missing adapters accepted")
	}
	if err := (Controller{}).Run(nil); err == nil {
		t.Fatal("nil owner context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := owner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if owner.Phase() != ControllerNew || effects.closed.Load() != 0 {
		t.Fatal("rejected start transferred ownership")
	}
	if files, err := os.ReadDir(store.Directory); err != nil || len(files) != 0 {
		t.Fatal("rejected start changed state")
	}
}
