package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type observerConfigFixture struct {
	state         State
	desired       IngressObservationScope
	root          *os.Root
	dir           string
	writes, saves int
	failWrite     string
	failSave      int
	closeError    bool
}

func cloneObserverState(t *testing.T, state State) State {
	t.Helper()
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var out State
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func newObserverConfigFixture(t *testing.T) (*ObserverConfigurationBackend, *observerConfigFixture, ObserverConfigurationRequest) {
	t.Helper()
	_, observed, _, _ := observationFixture(t)
	f := &observerConfigFixture{desired: observed.scope, dir: t.TempDir()}
	f.state = State{Schema: StateSchema, Ownership: Ownership{ID: f.desired.OwnershipID}, Bundle: &ConnectionBundle{Schema: BundleSchema}}
	f.desired.BundleDigest = stableDigest(*f.state.Bundle)
	a := f.desired.Snapshot
	a.WorkspaceDigest, a.ManifestDigest, a.ActivatedAt = "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("e", 64), "2026-09-21T00:00:00Z"
	a.Epoch = digestBytes([]byte(a.WorkspaceDigest + "\x00" + a.Deployment + "\x00" + a.ActivatedAt + "\x00" + a.ManifestDigest))
	var err error
	f.root, err = os.OpenRoot(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.root.Close() })
	b := &ObserverConfigurationBackend{open: func(ctx context.Context, r ObserverConfigurationRequest, write bool) (*observerConfigurationSession, error) {
		s := &observerConfigurationSession{state: cloneObserverState(t, f.state), stamp: strings.Repeat("f", 64), check: func(ctx context.Context) error { return ctx.Err() }, close: func() error {
			if f.closeError {
				return ErrUnsafeState
			}
			return nil
		}}
		if r.Operation == "refresh" {
			s.desired = &f.desired
		}
		s.read = func(context.Context) ([]byte, error) { return readObserverScopeAt(f.root, "test.json") }
		s.replace = func(ctx context.Context, before, desired []byte) error {
			if !write {
				t.Fatal("read-only plan attempted file mutation")
			}
			f.writes++
			if f.state.ObserverScopes[r.WorkspaceID].Status != "pending" {
				t.Fatal("file changed before durable revocation intent")
			}
			if f.failWrite == "before" {
				return ErrUnsafeState
			}
			err := replaceObserverScopeAt(ctx, f.root, "test.json", before, desired, s.check)
			if err == nil && f.failWrite == "after" {
				return ErrUnsafeState
			}
			return err
		}
		s.save = func(ctx context.Context, state State) error {
			if !write {
				t.Fatal("read-only plan attempted state mutation")
			}
			f.saves++
			if f.saves == f.failSave {
				return ErrUnsafeState
			}
			f.state = cloneObserverState(t, state)
			return ctx.Err()
		}
		return s, nil
	}}
	return b, f, ObserverConfigurationRequest{Schema: ObserverConfigurationSchema, WorkspaceID: "test", Operation: "refresh"}
}

func observerConfigBinding(p ObserverConfigurationPlan) ObserverConfigurationBinding {
	return ObserverConfigurationBinding{Schema: p.Schema, WorkspaceID: p.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest}
}

func TestObserverConfigurationDerivesScopeAndRevokesWithoutDaemon(t *testing.T) {
	b, f, r := newObserverConfigFixture(t)
	ctx := context.Background()
	p, err := b.Plan(ctx, r)
	if err != nil || p.Validate() != nil {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if f.saves != 0 || f.writes != 0 || p.PreviousDigest != "" || p.LeaseCount != 1 {
		t.Fatal("planning changed state or lost scope")
	}
	out, err := b.Apply(ctx, r, observerConfigBinding(p))
	if err != nil || out.Validate() != nil || !out.Enabled {
		t.Fatalf("apply: %+v %v", out, err)
	}
	body, err := f.root.ReadFile("test.json")
	if err != nil || !observerScopeEnabled(f.state, "test", body) {
		t.Fatal("scope not committed", err)
	}
	if bytes.Contains(body, []byte("private_key")) || bytes.Contains(body, []byte("certificate_pem")) {
		t.Fatal("scope contains credentials")
	}
	p, err = b.Plan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	out, err = b.Apply(ctx, r, observerConfigBinding(p))
	if err != nil || !out.Unchanged || f.writes != 1 || f.saves != 2 {
		t.Fatal("repeat refresh rewrote scope", err)
	}
	// Disable does not need a live deployment, connection bundle or API reader.
	f.state.Bundle = nil
	r.Operation = "disable"
	p, err = b.Plan(ctx, r)
	if err != nil || p.DesiredDigest != "" || p.LeaseCount != 0 {
		t.Fatal("disable plan", err)
	}
	out, err = b.Apply(ctx, r, observerConfigBinding(p))
	if err != nil || out.Enabled || out.PublicationEnabled {
		t.Fatal("disable result", err)
	}
	if _, err = f.root.Stat("test.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scope still present", err)
	}
	if err := f.root.WriteFile("test.json", body, 0600); err != nil {
		t.Fatal(err)
	}
	if observerScopeEnabled(f.state, "test", body) {
		t.Fatal("restored old file resurrected authority")
	}
	if _, err = b.Plan(ctx, r); err == nil {
		t.Fatal("tombstone silently adopted restored artifact")
	}
}

func TestObserverConfigurationMustRetireBeforeHostUninstall(t *testing.T) {
	for _, status := range []string{"enabled", "pending", "unknown"} {
		t.Run(status, func(t *testing.T) {
			state := State{Schema: StateSchema, ObserverScopes: map[string]ObserverScopeRecord{"main": {Generation: 1, Status: status, Digest: strings.Repeat("a", 64)}}}
			// A nil runtime would panic if any host operation were reached.
			result, err := (&Backend{}).applyUninstall(context.Background(), Request{}, Plan{}, Observation{}, &state)
			if !errors.Is(err, ErrBlocked) || len(result.Blockers) != 1 || result.Blockers[0] != "disable_observer_scopes_before_uninstall" {
				t.Fatal(result, err)
			}
		})
	}
}

func TestObserverConfigurationLostFinalAcknowledgementRequiresReadback(t *testing.T) {
	b, f, r := newObserverConfigFixture(t)
	ctx := context.Background()
	plan, err := b.Plan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	original := b.open
	b.open = func(ctx context.Context, r ObserverConfigurationRequest, write bool) (*observerConfigurationSession, error) {
		s, err := original(ctx, r, write)
		if err != nil {
			return nil, err
		}
		save := s.save
		s.save = func(ctx context.Context, state State) error {
			if err := save(ctx, state); err != nil {
				return err
			}
			if state.ObserverScopes[r.WorkspaceID].Status == "enabled" {
				// The durable commit happened, but its acknowledgement was lost.
				// An error cannot be interpreted as proof of no external effect.
				return ErrUnsafeState
			}
			return nil
		}
		return s, nil
	}
	if out, err := b.Apply(ctx, r, observerConfigBinding(plan)); err == nil || out.Schema != "" {
		t.Fatal("uncertain final acknowledgement returned success")
	}
	body, err := readObserverScopeAt(f.root, "test.json")
	if err != nil || !observerScopeEnabled(f.state, r.WorkspaceID, body) {
		t.Fatal("fixture did not preserve the committed approved configuration", err)
	}
	b.open = original
	if _, err := b.Apply(ctx, r, observerConfigBinding(plan)); err == nil {
		t.Fatal("old approval was reusable after an uncertain completed commit")
	}
	fresh, err := b.Plan(ctx, r)
	if err != nil || fresh.Recovery || fresh.PreviousDigest != fresh.DesiredDigest {
		t.Fatal("fresh plan did not report the actual committed state", err)
	}
	out, err := b.Apply(ctx, r, observerConfigBinding(fresh))
	if err != nil || !out.Unchanged || f.writes != 1 {
		t.Fatal("readback reconciliation changed the already committed artifact", err)
	}
}

func TestObserverConfigurationInterruptedWritesRequireBoundRecovery(t *testing.T) {
	for _, failure := range []string{"before", "after", "commit"} {
		t.Run(failure, func(t *testing.T) {
			b, f, r := newObserverConfigFixture(t)
			ctx := context.Background()
			p, err := b.Plan(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "commit" {
				f.failSave = 2
			} else {
				f.failWrite = failure
			}
			if out, err := b.Apply(ctx, r, observerConfigBinding(p)); err == nil || out.Schema != "" {
				t.Fatal("uncertain write returned success")
			}
			body, _ := readObserverScopeAt(f.root, "test.json")
			if f.state.ObserverScopes["test"].Status != "pending" || observerScopeEnabled(f.state, "test", body) {
				t.Fatal("partial write retained observation authority")
			}
			f.failSave, f.failWrite = 0, ""
			// A newly computed plan, not the old approval, can settle the exact
			// previously intended bytes. No recovery by arbitrary file adoption.
			if _, err := b.Apply(ctx, r, observerConfigBinding(p)); err == nil {
				t.Fatal("stale plan survived pending transaction")
			}
			next, err := b.Plan(ctx, r)
			if err != nil || !next.Recovery {
				t.Fatal("recovery plan", err)
			}
			if _, err := b.Apply(ctx, r, observerConfigBinding(next)); err != nil {
				t.Fatal(err)
			}
			body, _ = readObserverScopeAt(f.root, "test.json")
			if !observerScopeEnabled(f.state, "test", body) {
				t.Fatal("recovery did not commit exact scope")
			}
		})
	}
}

func TestObserverConfigurationRejectsStalePlanAndForeignFiles(t *testing.T) {
	for _, scenario := range []string{"unowned", "tampered", "epoch", "bundle", "unsupported-interface", "cancel", "close"} {
		t.Run(scenario, func(t *testing.T) {
			b, f, r := newObserverConfigFixture(t)
			ctx := context.Background()
			p, err := b.Plan(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "unowned":
				body, _ := json.Marshal(f.desired)
				if err := f.root.WriteFile("test.json", body, 0600); err != nil {
					t.Fatal(err)
				}
			case "tampered":
				if err := f.root.WriteFile("test.json", []byte("unknown-private-marker"), 0600); err != nil {
					t.Fatal(err)
				}
			case "epoch":
				f.desired.Snapshot.Epoch = strings.Repeat("a", 64)
			case "bundle":
				f.state.Bundle.Endpoint = "https://different.invalid"
			case "unsupported-interface":
				f.desired.Snapshot.Authorizations[0].Interface = "incus_vm"
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "close":
				f.closeError = true
			}
			if scenario == "close" {
				if result, err := b.Plan(ctx, r); err == nil || result.Schema != "" {
					t.Fatal("failed close returned usable plan")
				}
				return
			}
			if _, err := b.Apply(ctx, r, observerConfigBinding(p)); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if f.saves != 0 || f.writes != 0 {
				t.Fatal("invalid plan reached writes")
			}
		})
	}
}

func TestObserverConfigurationPendingChangedEpochMustDisableFirst(t *testing.T) {
	b, f, r := newObserverConfigFixture(t)
	ctx := context.Background()
	p, err := b.Plan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	f.failWrite = "after"
	if _, err = b.Apply(ctx, r, observerConfigBinding(p)); err == nil {
		t.Fatal("failure ignored")
	}
	f.failWrite = ""
	a := f.desired.Snapshot
	a.ActivatedAt = "2026-09-21T01:00:00Z"
	a.Epoch = digestBytes([]byte(a.WorkspaceDigest + "\x00" + a.Deployment + "\x00" + a.ActivatedAt + "\x00" + a.ManifestDigest))
	if _, err = b.Plan(ctx, r); err == nil {
		t.Fatal("changed epoch replaced unfinished old transition")
	}
	r.Operation = "disable"
	p, err = b.Plan(ctx, r)
	if err != nil || !p.Recovery {
		t.Fatal(err)
	}
	if _, err = b.Apply(ctx, r, observerConfigBinding(p)); err != nil {
		t.Fatal(err)
	}
	r.Operation = "refresh"
	p, err = b.Plan(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Apply(ctx, r, observerConfigBinding(p)); err != nil {
		t.Fatal(err)
	}
}

func TestObserverFileRefusesLinksReplacementAndUnexpectedBytes(t *testing.T) {
	for _, scenario := range []string{"symlink", "hardlink", "changed", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			_, f, _ := newObserverConfigFixture(t)
			ctx := context.Background()
			before := []byte("before")
			if err := f.root.WriteFile("test.json", before, 0600); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "symlink":
				if err := f.root.Remove("test.json"); err != nil {
					t.Fatal(err)
				}
				if err := f.root.Symlink("other", "test.json"); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(f.dir, "test.json"), filepath.Join(f.dir, "other")); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := f.root.WriteFile("test.json", []byte("someone else"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := replaceObserverScopeAt(ctx, f.root, "test.json", before, []byte("new"), func(ctx context.Context) error { return ctx.Err() }); err == nil {
				t.Fatal("unsafe replacement accepted")
			}
		})
	}
}
