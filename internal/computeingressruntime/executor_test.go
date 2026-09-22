package computeingressruntime

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// These are interface-level transaction tests. They do not install host rules,
// prove Traefik consumption or replace the real Docker/Incus acceptance matrix.
type executorFixture struct {
	mu                  sync.Mutex
	state               ExecutorState
	steps               []string
	failStep            string
	saves               int
	failSave            int
	orphan              bool
	invalid             bool
	invalidateOnProbe   bool
	invalidateOnPublish bool
}

func newExecutorFixture() (*executorFixture, Executor, PublicationTarget) {
	f := &executorFixture{state: ExecutorState{Schema: executorStateSchema}}
	e := Executor{Observer: f, Host: f, Probe: f, Renderer: f, Store: f, Authority: f}
	target := PublicationTarget{Epoch: strings.Repeat("a", 64), Incarnation: strings.Repeat("b", 64), NICMAC: "00:16:3e:01:02:03", Publication: computeingress.Publication{
		Reservation: strings.Repeat("c", 32) + ":1", Deployment: "deployment-one", Lease: computeingress.Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", InstanceUUID: "uuid-one", WorkloadID: "job:123", GuestPort: 7000, Host: "ci-job1.example.test", GuestIP: "10.42.0.2", Auth: "none",
	}}
	return f, e, target
}

func cloneExecutorFixtureState(s ExecutorState) ExecutorState {
	s.Publications, s.Retired = slices.Clone(s.Publications), slices.Clone(s.Retired)
	return s
}

func (f *executorFixture) WithExclusive(ctx context.Context, fn func(Journal) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f)
}
func (f *executorFixture) Check(ctx context.Context) error { return ctx.Err() }
func (f *executorFixture) Load(ctx context.Context) (ExecutorState, error) {
	return cloneExecutorFixtureState(f.state), ctx.Err()
}
func (f *executorFixture) Save(ctx context.Context, s ExecutorState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.saves++
	if f.saves == f.failSave {
		return errors.New("fixture persistence failure")
	}
	f.state = cloneExecutorFixtureState(s)
	return nil
}
func (f *executorFixture) ValidateTarget(ctx context.Context, _ PublicationTarget) error {
	if f.invalid {
		return errors.New("fixture target invalidated")
	}
	return ctx.Err()
}
func (f *executorFixture) ValidateAuthorization(ctx context.Context, t PublicationTarget) error {
	return f.ValidateTarget(ctx, t)
}
func (f *executorFixture) CheckHTTPArtifacts(ctx context.Context, _ []PublicationTarget) error {
	if f.orphan {
		return errors.New("fixture unaccounted external artifact")
	}
	return ctx.Err()
}
func (f *executorFixture) step(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.steps = append(f.steps, name)
	if name == f.failStep {
		return errors.New("fixture unconfirmed operation")
	}
	return nil
}
func (f *executorFixture) HoldAddress(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "hold")
}
func (f *executorFixture) EnsureGuestRoute(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "route+")
}
func (f *executorFixture) EnsureHTTPPermit(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "permit+")
}
func (f *executorFixture) RemoveHTTPPermit(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "permit-")
}
func (f *executorFixture) CloseHTTPConnections(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "connections-")
}
func (f *executorFixture) RemoveGuestRoute(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "route-")
}
func (f *executorFixture) ReleaseAddress(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "release")
}
func (f *executorFixture) PublishHTTP(ctx context.Context, _ PublicationTarget) error {
	if f.invalidateOnPublish {
		f.invalid = true
	}
	return f.step(ctx, "publish")
}

func TestExecutorRevalidatesAfterRouteConsumption(t *testing.T) {
	f, e, target := newExecutorFixture()
	f.invalidateOnPublish = true
	if err := e.Reconcile(context.Background(), target.Epoch, []PublicationTarget{target}); err == nil {
		t.Fatal("target changed while Traefik was consuming the route, but publication succeeded")
	}
	want := append(slices.Clone(openingSteps), closingSteps...)
	if !reflect.DeepEqual(f.steps, want) || len(f.state.Publications) != 0 || len(f.state.Retired) != 1 {
		t.Fatalf("route-consumption race did not retire in order: %v", f.steps)
	}
}
func (f *executorFixture) RemoveHTTP(ctx context.Context, _ PublicationTarget) error {
	return f.step(ctx, "unpublish")
}
func (f *executorFixture) ProbeHTTP(ctx context.Context, _ PublicationTarget) error {
	if f.invalidateOnProbe {
		f.invalid = true
	}
	return f.step(ctx, "probe")
}

var openingSteps = []string{"hold", "route+", "permit+", "probe", "publish"}
var closingSteps = []string{"unpublish", "permit-", "connections-", "route-", "release"}

func TestExecutorPublishesAndWithdrawsInOrderWithoutTokenResurrection(t *testing.T) {
	f, e, target := newExecutorFixture()
	ctx := context.Background()
	if err := e.Reconcile(ctx, target.Epoch, []PublicationTarget{target}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.steps, openingSteps) || len(f.state.Publications) != 1 || !f.state.Publications[0].RoutePublished {
		t.Fatalf("incorrect publication sequence: %v", f.steps)
	}
	f.steps = nil
	if err := e.Reconcile(ctx, target.Epoch, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.steps, closingSteps) || len(f.state.Publications) != 0 || len(f.state.Retired) != 1 {
		t.Fatalf("incorrect retirement: %v", f.steps)
	}
	f.steps = nil
	if err := e.Reconcile(ctx, target.Epoch, []PublicationTarget{target}); err != nil {
		t.Fatal(err)
	}
	if len(f.steps) != 0 || len(f.state.Publications) != 0 {
		t.Fatal("retired reservation opened again")
	}
}

func TestExecutorRollsBackEveryFailedOpeningStep(t *testing.T) {
	for _, failed := range openingSteps {
		t.Run(failed, func(t *testing.T) {
			f, e, target := newExecutorFixture()
			f.failStep = failed
			if err := e.Reconcile(context.Background(), target.Epoch, []PublicationTarget{target}); err == nil {
				t.Fatal("failed operation reported success")
			}
			want := append(slices.Clone(openingSteps[:slices.Index(openingSteps, failed)+1]), closingSteps...)
			if !reflect.DeepEqual(f.steps, want) || len(f.state.Publications) != 0 || len(f.state.Retired) != 1 {
				t.Fatalf("unsafe rollback: %v; want %v", f.steps, want)
			}
		})
	}
}

func TestExecutorKeepsAddressAndRetirementUntilEveryClosingStepConfirms(t *testing.T) {
	for _, failed := range closingSteps {
		t.Run(failed, func(t *testing.T) {
			f, e, target := newExecutorFixture()
			ctx := context.Background()
			if err := e.Reconcile(ctx, target.Epoch, []PublicationTarget{target}); err != nil {
				t.Fatal(err)
			}
			f.steps = nil
			f.failStep = failed
			if err := e.Reconcile(ctx, target.Epoch, nil); err == nil {
				t.Fatal("unconfirmed removal reported success")
			}
			want := closingSteps[:slices.Index(closingSteps, failed)+1]
			if !reflect.DeepEqual(f.steps, want) || len(f.state.Publications) != 1 || !f.state.Publications[0].Retiring || !f.state.Publications[0].AddressHeld {
				t.Fatalf("address released before confirmation: %v %#v", f.steps, f.state)
			}
			f.failStep = ""
			f.steps = nil
			// A still-present request does not cancel a persisted retirement.
			if err := e.Reconcile(ctx, target.Epoch, []PublicationTarget{target}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.steps, closingSteps) || len(f.state.Publications) != 0 || len(f.state.Retired) != 1 {
				t.Fatalf("retirement abandoned: %v", f.steps)
			}
		})
	}
}

func TestExecutorRecoveryCleansUnreceiptedSideEffects(t *testing.T) {
	f, e, target := newExecutorFixture()
	f.failSave = 2 // Intent persisted; address mutation succeeded; its receipt failed.
	if err := e.Reconcile(context.Background(), target.Epoch, []PublicationTarget{target}); err == nil {
		t.Fatal("persistence failure ignored")
	}
	if len(f.state.Publications) != 1 || f.state.Publications[0].AddressHeld || !reflect.DeepEqual(f.steps, []string{"hold"}) {
		t.Fatalf("incorrect uncertain state: %#v %v", f.state, f.steps)
	}
	f.failSave = 0
	f.steps = nil
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.steps, closingSteps) || len(f.state.Publications) != 0 {
		t.Fatalf("unreceipted address not cleaned: %v", f.steps)
	}
}

func TestExecutorUnknownArtifactsBlockOpeningAndAddressReuse(t *testing.T) {
	f, e, target := newExecutorFixture()
	ctx := context.Background()
	f.orphan = true
	if err := e.Reconcile(ctx, target.Epoch, []PublicationTarget{target}); err == nil || len(f.steps) != 0 {
		t.Fatal("missing journal treated as clean installation")
	}
	f.orphan = false
	if err := e.Reconcile(ctx, target.Epoch, []PublicationTarget{target}); err != nil {
		t.Fatal(err)
	}
	f.orphan = true
	f.steps = nil
	if err := e.Recover(ctx); err == nil {
		t.Fatal("unknown artifact ignored during recovery")
	}
	if slices.Contains(f.steps, "release") || len(f.state.Publications) != 1 || !f.state.Publications[0].AddressHeld {
		t.Fatal("address released despite orphaned artifacts")
	}
	if !reflect.DeepEqual(f.steps, closingSteps[:4]) {
		t.Fatalf("known exposure was not closed first: %v", f.steps)
	}
}

func TestExecutorRevalidatesAfterProbeAndRejectsCorruptJournal(t *testing.T) {
	f, e, target := newExecutorFixture()
	f.invalidateOnProbe = true
	if err := e.Reconcile(context.Background(), target.Epoch, []PublicationTarget{target}); err == nil || slices.Contains(f.steps, "publish") {
		t.Fatal("stale observation survived probe")
	}
	if len(f.state.Publications) != 0 || len(f.state.Retired) != 1 {
		t.Fatal("invalidated target not retired")
	}
	f, e, target = newExecutorFixture()
	f.state.Schema = "corrupt"
	if err := e.Reconcile(context.Background(), target.Epoch, []PublicationTarget{target}); err == nil || len(f.steps) != 0 {
		t.Fatal("corrupt journal accepted")
	}
}
