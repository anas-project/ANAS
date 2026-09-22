package jobexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// Real durable Store, confirmation ledger and execution binding. Only the
// plan/result are synthetic; this does not run hostd or mutate a host scope.
func TestObserverConfigurationConfirmedQueueAndWorkspaceBinding(t *testing.T) {
	ctx := context.Background()
	s, _, store, lease := serviceFixture(t, nil)
	ledger, err := hostconfirmation.OpenForTesting(ctx, filepath.Join(t.TempDir(), "confirm"), s.options.Journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	s.options.Confirmations = ledger
	s.available = true
	r := incusprovision.ObserverConfigurationRequest{Schema: incusprovision.ObserverConfigurationSchema, WorkspaceID: "main", Operation: "disable"}
	body, _ := json.Marshal(r)
	created, err := s.InvokePlan(ctx, "alice", "main", hostaction.ActionObserverPlan, body, "plan-observer")
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.StartActionObserved(ctx, created.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	p := incusprovision.ObserverConfigurationPlan{Schema: r.Schema, WorkspaceID: r.WorkspaceID, Operation: r.Operation, StateDigest: strings.Repeat("a", 64)}
	encoded, _ := json.Marshal(p)
	p.Digest = fmt.Sprintf("%x", sha256.Sum256(encoded))
	params := hostaction.ObserverApplyParameters{Schema: "anas.host-action.incus/v1", Request: r, Binding: incusprovision.ObserverConfigurationBinding{Schema: r.Schema, WorkspaceID: r.WorkspaceID, PlanDigest: p.Digest, StateDigest: p.StateDigest}}
	public, _ := json.Marshal(params)
	_, frozen, err := hostaction.FrozenRequest(hostaction.ActionObserverApply, s.options.Release, public)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"schema": "anas.host-action.incus/v1", "action": hostaction.ActionObserverApply, "plan": p, "parameters": params, "action_confirmation": map[string]string{
		"parameters_digest": consolejobs.DigestRequest(frozen), "state_digest": p.StateDigest, "summary_digest": p.Digest, "release_digest": consolejobs.DigestRequest([]byte(s.options.Release.Version + "/" + s.options.Release.Commit))}}
	encoded, _ = json.Marshal(value)
	changed := false
	frame, err := hostaction.ProjectActionEvent(hostaction.ActionObserverPlan, actionabi.Event{ABI: actionabi.Version, JobID: started.ID, InvocationID: started.Action.InvocationID, Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: encoded}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteActionObserved(ctx, lease, frame, s.observer()); err != nil {
		t.Fatal(err)
	}
	proof, err := s.IssueConfirmation(ctx, "alice", "main", started.ID, hostaction.ActionObserverApply)
	if err != nil {
		t.Fatal(err)
	}
	bad := params
	bad.Request.WorkspaceID = "other"
	bad.Binding.WorkspaceID = "other"
	badBody, _ := json.Marshal(bad)
	if _, err = s.InvokeConfirmed(ctx, "alice", "main", hostaction.ActionObserverApply, started.ID, badBody, proof.Token, "bad-scope"); !errors.Is(err, hostaction.ErrDenied) {
		t.Fatal("cross-scope write admitted", err)
	}
	// The denied attempt must not consume the real proof.
	applied, err := s.InvokeConfirmed(ctx, "alice", "main", hostaction.ActionObserverApply, started.ID, public, proof.Token, "apply-observer")
	if err != nil {
		t.Fatal(err)
	}
	running, err := store.StartActionObserved(ctx, applied.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	bound, err := NewHostJobBinding(ctx, store, lease, running.ID, s.options.Release, func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Close()
	wire, err := parametersForExecution(running)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hostaction.CanonicalWireParameters(hostaction.ActionObserverApply, wire); err != nil {
		t.Fatal(err)
	}
	err = bound.WithHostInvocation(ctx, actionabi.Request{ABI: actionabi.Version, JobID: running.ID, InvocationID: running.Action.InvocationID, Action: hostaction.ActionObserverApply, Parameters: wire}, s.options.Release, hostPeer(), func(ctx context.Context) error {
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(wire, &raw)
		var digest string
		_ = json.Unmarshal(raw[consolejobs.ActionConfirmationBindingDigestRequestKey], &digest)
		receipt, err := ledger.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: digest, ApplyJobID: running.ID, InvocationID: running.Action.InvocationID})
		if err != nil {
			return err
		}
		if receipt.Action != hostaction.ActionObserverApply || receipt.StateDigest != p.StateDigest {
			t.Fatal("approval binding lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
