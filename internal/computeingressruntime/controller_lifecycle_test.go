package computeingressruntime

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
)

// The controller, pinned observer and durable flock/journal are real. Host
// networking, Traefik and the application probe are explicit test adapters.
type lifecycleDesiredSource struct {
	fixture     *incusReaderFixture
	target      PublicationTarget
	retirements chan struct{}
}

func (s *lifecycleDesiredSource) ReadDesired(ctx context.Context, _ Journal) (DesiredSnapshot, error) {
	facts, err := s.fixture.reader.ObserveHTTP(ctx, s.fixture.grant, s.fixture.request)
	if err != nil {
		return DesiredSnapshot{}, err
	}
	planner, err := computeingress.NewPlanner(s.fixture.grant.Deployment, []*computeingress.Authorization{s.fixture.grant}, nil)
	if err != nil {
		return DesiredSnapshot{}, err
	}
	publication, err := planner.Reserve(computeingress.Lease{Consumer: s.fixture.grant.Consumer, Resource: s.fixture.grant.Resource}, s.fixture.request, facts, "")
	if err != nil {
		return DesiredSnapshot{}, err
	}
	target := PublicationTarget{Epoch: strings.Repeat("a", 64), Incarnation: facts.Incarnation, NICMAC: facts.GuestMAC, Publication: publication}
	previous := target
	previous.Publication.Reservation = s.target.Publication.Reservation
	if previous == s.target {
		target = s.target
	}
	s.target = target
	return DesiredSnapshot{Epoch: target.Epoch, Targets: []PublicationTarget{target}}, nil
}

func (s *lifecycleDesiredSource) ValidateAuthorization(ctx context.Context, t PublicationTarget) error {
	if t != s.target {
		return errors.New("fixture request no longer authorized")
	}
	return ctx.Err()
}

func (s *lifecycleDesiredSource) AfterRetirement() {
	s.target = PublicationTarget{}
	select {
	case s.retirements <- struct{}{}:
	default:
	}
}

type lifecycleEffects struct {
	*executorFixture
	published   chan PublicationTarget
	released    chan PublicationTarget
	onPublish   func()
	failRemoval bool
	pending     PublicationTarget
}

func (e *lifecycleEffects) PublishHTTP(ctx context.Context, t PublicationTarget) error {
	if err := e.executorFixture.PublishHTTP(ctx, t); err != nil {
		return err
	}
	if e.onPublish != nil {
		callback := e.onPublish
		e.onPublish = nil
		callback()
	}
	e.pending = t
	return nil
}

func (e *lifecycleEffects) CheckHTTPArtifacts(ctx context.Context, targets []PublicationTarget) error {
	if err := e.executorFixture.CheckHTTPArtifacts(ctx, targets); err != nil {
		return err
	}
	// The final inventory follows post-publication validation and journal
	// persistence. Synchronize test mutations here, not halfway through publish.
	if e.pending != (PublicationTarget{}) {
		e.published <- e.pending
		e.pending = PublicationTarget{}
	}
	return nil
}
func (e *lifecycleEffects) RemoveHTTP(ctx context.Context, t PublicationTarget) error {
	e.pending = PublicationTarget{}
	if err := e.executorFixture.RemoveHTTP(ctx, t); err != nil {
		return err
	}
	if e.failRemoval {
		return errors.New("fixture route removal unconfirmed")
	}
	return nil
}
func (e *lifecycleEffects) ReleaseAddress(ctx context.Context, t PublicationTarget) error {
	if err := e.executorFixture.ReleaseAddress(ctx, t); err != nil {
		return err
	}
	e.released <- t
	return nil
}

func awaitLifecycleTarget(t *testing.T, channel <-chan PublicationTarget) PublicationTarget {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("controller did not reach the expected lifecycle step")
		return PublicationTarget{}
	}
}

func awaitLifecycleRetirement(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(10 * time.Second):
		t.Fatal("controller did not finish the retirement barrier")
	}
}

func readLifecycleState(t *testing.T, store FileStateStore) ExecutorState {
	t.Helper()
	var state ExecutorState
	if err := store.WithExclusive(context.Background(), func(j Journal) error { var err error; state, err = j.Load(context.Background()); return err }); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestControllerPausesRetiresAndResumesUnderNewReservation(t *testing.T) {
	for _, state := range []struct {
		name, status string
		code         int
	}{{"pause", "Frozen", 110}, {"stop", "Stopped", 102}} {
		t.Run(state.name, func(t *testing.T) {
			f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
			base, executor, _ := newExecutorFixture()
			effects := &lifecycleEffects{executorFixture: base, published: make(chan PublicationTarget, 8), released: make(chan PublicationTarget, 8)}
			source := &lifecycleDesiredSource{fixture: f, retirements: make(chan struct{}, 8)}
			store := FileStateStore{Directory: t.TempDir()}
			if err := os.Chmod(store.Directory, 0700); err != nil {
				t.Fatal(err)
			}
			executor.Observer = f.reader
			executor.Host = effects
			executor.Renderer = effects
			executor.Store = store
			events := make(chan struct{}, 4)
			controller := Controller{Executor: executor, Source: source, Interval: time.Hour, OperationTimeout: 5 * time.Second, CleanupTimeout: 2 * time.Second, Events: events}
			ctx, cancel := context.WithCancel(context.Background())
			finished := make(chan error, 1)
			go func() { finished <- controller.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(10 * time.Second):
					t.Error("controller failed to stop")
				}
			})
			awaitLifecycleRetirement(t, source.retirements) // Startup recovery.
			first := awaitLifecycleTarget(t, effects.published)
			f.mu.Lock()
			instance := f.values["/1.0/instances/anas-fj-job1?project=anas-runners"].(map[string]any)
			instance["status"], instance["status_code"] = state.status, state.code
			f.mu.Unlock()
			events <- struct{}{}
			retired := awaitLifecycleTarget(t, effects.released)
			if retired != first {
				t.Fatal("retirement lost original identity")
			}
			awaitLifecycleRetirement(t, source.retirements)
			f.mu.Lock()
			instance["status"], instance["status_code"] = "Running", 103
			// Keep UUID, IP, MAC, generation and start time unchanged. The
			// completed retirement itself must prevent reuse of the old token.
			f.mu.Unlock()
			events <- struct{}{}
			second := awaitLifecycleTarget(t, effects.published)
			if second.Publication.Reservation == first.Publication.Reservation {
				t.Fatal("resumed publication resurrected retired token")
			}
			if second.Incarnation != first.Incarnation {
				t.Fatal("test did not preserve the guest identity for pause/resume")
			}
			cancel()
			if third := awaitLifecycleTarget(t, effects.released); third != second {
				t.Fatal("shutdown did not withdraw the current publication")
			}
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("controller result: %v", err)
				}
				finished <- err
			case <-time.After(10 * time.Second):
				t.Fatal("shutdown did not finish")
			}
			saved := readLifecycleState(t, store)
			if len(saved.Publications) != 0 || len(saved.Retired) != 2 {
				t.Fatalf("durable lifecycle receipts: %+v", saved)
			}
			want := append(slices.Clone(openingSteps), closingSteps...)
			want = append(want, openingSteps...)
			want = append(want, closingSteps...)
			if !reflect.DeepEqual(base.steps, want) {
				t.Fatalf("lifecycle order: %v", base.steps)
			}
		})
	}
}

func TestControllerFailedShutdownKeepsDurableRetirementAndAddress(t *testing.T) {
	f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
	base, executor, _ := newExecutorFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	effects := &lifecycleEffects{executorFixture: base, published: make(chan PublicationTarget, 8), released: make(chan PublicationTarget, 8), onPublish: cancel, failRemoval: true}
	source := &lifecycleDesiredSource{fixture: f, retirements: make(chan struct{}, 8)}
	store := FileStateStore{Directory: t.TempDir()}
	if err := os.Chmod(store.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	executor.Observer = f.reader
	executor.Host = effects
	executor.Renderer = effects
	executor.Store = store
	controller := Controller{Executor: executor, Source: source, Interval: time.Hour, OperationTimeout: 5 * time.Second, CleanupTimeout: time.Second}
	err := controller.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown result: %v", err)
	}
	saved := readLifecycleState(t, store)
	if len(saved.Publications) != 1 || !saved.Publications[0].Retiring || !saved.Publications[0].AddressHeld || len(saved.Retired) != 0 {
		t.Fatal("failed shutdown discarded cleanup ownership")
	}
	if slices.Contains(base.steps, "release") || slices.Contains(base.steps, "permit-") {
		t.Fatal("cleanup continued past an unconfirmed route removal")
	}
	// A fresh executor after restart uses the same durable journal and
	// independent cleanup context; an unavailable guest must not block revoke.
	effects.failRemoval = false
	executor.Authority = source
	if err := executor.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := readLifecycleState(t, store)
	if len(final.Publications) != 0 || len(final.Retired) != 1 {
		t.Fatal("restart did not finish durable retirement")
	}
}
