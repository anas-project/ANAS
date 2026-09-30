package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// Real Store/lease/confirmation/binding/projection/ABI round-trip; the observed
// host plan is a fixture. No privileged process or host effect is executed.
func TestConfirmedIncusPlanSurvivesQueuePersistenceAndExecutorHandoff(t *testing.T) {
	ctx := context.Background()
	s, _, store, lease := serviceFixture(t, nil)
	ledger, err := hostconfirmation.OpenForTesting(ctx, filepath.Join(t.TempDir(), "confirmations"), s.options.Journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ledger.Close(); err != nil {
			t.Error(err)
		}
	})
	s.options.Confirmations = ledger
	s.available = true // Exercise admission without installing the native listener.
	s.options.ChineseSpeedup = func(context.Context, string) (bool, error) { return true, nil }
	plan, err := s.InvokePlan(ctx, "alice", "main", hostaction.ActionInstallPlan, json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{}}`), "plan-key")
	if err != nil {
		t.Fatal("plan admission", err)
	}
	started, err := store.StartActionObserved(ctx, plan.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	s.options.ChineseSpeedup = func(context.Context, string) (bool, error) {
		t.Fatal("confirmed action re-read workspace policy instead of the frozen plan")
		return false, nil
	}
	request := incusprovision.Request{Interface: "incus_container", StorageSizeGiB: 64, ChineseSpeedup: true}
	params := hostaction.IncusApplyParameters{Schema: "anas.host-action.incus/v1", Request: request,
		Binding: incusprovision.Binding{Schema: incusprovision.Schema, Phase: incusprovision.PhaseInstall, PlanDigest: strings.Repeat("b", 64), Destructive: true}}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	_, frozen, err := hostaction.FrozenRequest(hostaction.ActionInstall, s.options.Release, body)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{
		"schema": "anas.host-action.incus/v1", "action": hostaction.ActionInstall, "phase": "install", "parameters": params,
		"inspect": incusprovision.InspectResult{Schema: incusprovision.Schema, ObservedAt: time.Now().UTC(),
			Plan: incusprovision.Plan{Schema: incusprovision.Schema, Digest: params.Binding.PlanDigest, StateDigest: strings.Repeat("c", 64), Request: request}},
		"action_confirmation": map[string]string{"parameters_digest": consolejobs.DigestRequest(frozen), "state_digest": strings.Repeat("c", 64),
			"summary_digest": params.Binding.PlanDigest, "release_digest": consolejobs.DigestRequest([]byte(s.options.Release.Version + "/" + s.options.Release.Commit))},
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := false
	frame, err := hostaction.ProjectActionEvent(hostaction.ActionInstallPlan, actionabi.Event{ABI: actionabi.Version, JobID: started.ID, InvocationID: started.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &unchanged, Value: result}})
	if err != nil {
		t.Fatal("plan public projection", err)
	}
	if _, err := store.CompleteActionObserved(ctx, lease, frame, s.observer()); err != nil {
		t.Fatal(err)
	}
	proof, err := s.IssueConfirmation(ctx, "alice", "main", started.ID, hostaction.ActionInstall)
	if err != nil {
		t.Fatal("confirmation", err)
	}
	applied, err := s.InvokeConfirmed(ctx, "alice", "main", hostaction.ActionInstall, started.ID, body, proof.Token, "apply-key")
	if err != nil {
		t.Fatal("confirmed admission", err)
	}
	running, err := store.StartActionObserved(ctx, applied.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newHostActionRunner(store, lease, s.options.Release, func(context.Context, consolejobs.Job) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	invocations := 0
	var bindingDigest string
	runner.dial = func(ctx context.Context, request actionabi.Request) (hostStream, error) {
		if request.JobID != running.ID || request.InvocationID != running.Action.InvocationID || request.Action != hostaction.ActionInstall {
			t.Fatal("hostd received another invocation")
		}
		wire, err := hostaction.CanonicalWireParameters(hostaction.ActionInstall, request.Parameters)
		if err != nil {
			t.Fatal("privileged input boundary", err)
		}
		var decoded hostaction.IncusApplyParameters
		decoded, bindingDigest, err = hostaction.DecodeApplyParameters(hostaction.ActionInstall, wire)
		if err != nil || decoded != params {
			t.Fatal("decoded confirmed input drifted", err)
		}
		receipt, err := ledger.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: bindingDigest, ApplyJobID: running.ID, InvocationID: running.Action.InvocationID})
		if err != nil {
			return nil, err
		}
		if receipt.Action != hostaction.ActionInstall || receipt.ParametersDigest != consolejobs.DigestRequest(frozen) {
			t.Fatal("root claim changed approved action or bytes")
		}
		invocations++
		changed := true
		value, _ := json.Marshal(incusprovision.ApplyResult{Schema: incusprovision.Schema, Phase: incusprovision.PhaseInstall,
			PlanDigest: params.Binding.PlanDigest, Disposition: "installed", Receipts: []incusprovision.Receipt{}})
		return frames(t,
			actionabi.Event{ABI: actionabi.Version, JobID: running.ID, InvocationID: running.Action.InvocationID, Type: "progress",
				Progress: &actionabi.Progress{Phase: string(incusprovision.PhaseInstall)}},
			actionabi.Event{ABI: actionabi.Version, JobID: running.ID, InvocationID: running.Action.InvocationID, Type: "result",
				Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: value}}), nil
	}
	finished, err := runner.ExecutePreflight(ctx, running.ID, s.observer())
	if err != nil || invocations != 1 || finished.Status != consolejobs.StatusSucceeded {
		t.Fatal("executor handoff", finished.Status, err)
	}
	if _, err := ledger.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: bindingDigest, ApplyJobID: running.ID, InvocationID: running.Action.InvocationID}); !errors.Is(err, hostconfirmation.ErrConsumed) {
		t.Fatal("consumed approval allowed a second execution")
	}
}

func TestConfirmedImagePruneUsesServerPlanParameters(t *testing.T) {
	ctx := context.Background()
	s, _, store, lease := serviceFixture(t, nil)
	ledger, err := hostconfirmation.OpenForTesting(ctx, filepath.Join(t.TempDir(), "confirmations"), s.options.Journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	s.options.Confirmations = ledger
	s.available = true
	plan, err := s.InvokeImagePrunePlan(ctx, "alice", "main", "prune-plan-key")
	if err != nil {
		t.Fatal("plan admission", err)
	}
	started, err := store.StartActionObserved(ctx, plan.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	target := incusprovision.ImagePruneTarget{Project: "anas-a", Fingerprint: strings.Repeat("d", 64)}
	params := hostaction.IncusImagePruneApplyParameters{Schema: "anas.host-action.incus/v1",
		Request: incusprovision.ImagePruneRequest{Schema: incusprovision.ImagePruneSchema, WorkspaceID: "main"},
		Binding: incusprovision.ImagePruneBinding{Schema: incusprovision.ImagePruneSchema, WorkspaceID: "main", PlanDigest: strings.Repeat("b", 64),
			StateDigest: strings.Repeat("c", 64), SummaryDigest: strings.Repeat("b", 64), Delete: []incusprovision.ImagePruneTarget{target}},
	}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	_, frozen, err := hostaction.FrozenRequest(hostaction.ActionImagePrune, s.options.Release, body)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{
		"schema": "anas.host-action.incus/v1", "action": hostaction.ActionImagePrune, "parameters": params,
		"plan": incusprovision.ImagePrunePlanResult{Schema: incusprovision.ImagePruneSchema, WorkspaceID: "main", StateDigest: strings.Repeat("c", 64), Digest: strings.Repeat("b", 64),
			Delete: []incusprovision.ImagePruneTarget{target}},
		"action_confirmation": map[string]string{"parameters_digest": consolejobs.DigestRequest(frozen), "state_digest": strings.Repeat("c", 64),
			"summary_digest": strings.Repeat("b", 64), "release_digest": consolejobs.DigestRequest([]byte(s.options.Release.Version + "/" + s.options.Release.Commit))},
	}
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := false
	frame, err := hostaction.ProjectActionEvent(hostaction.ActionImagePrunePlan, actionabi.Event{ABI: actionabi.Version, JobID: started.ID, InvocationID: started.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &unchanged, Value: result}})
	if err != nil {
		t.Fatal("plan projection", err)
	}
	if _, err := store.CompleteActionObserved(ctx, lease, frame, s.observer()); err != nil {
		t.Fatal(err)
	}
	proof, err := s.IssueConfirmation(ctx, "alice", "main", started.ID, hostaction.ActionImagePrune)
	if err != nil {
		t.Fatal("confirmation", err)
	}
	applied, err := s.InvokeImagePruneConfirmed(ctx, "alice", "main", started.ID, proof.Token, "prune-apply-key")
	if err != nil {
		t.Fatal("confirmed admission", err)
	}
	running, err := store.StartActionObserved(ctx, applied.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := parametersForExecution(running)
	if err != nil {
		t.Fatal(err)
	}
	decoded, digest, err := hostaction.DecodeImagePruneApplyParameters(hostaction.ActionImagePrune, wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Binding.Delete) != 1 || decoded.Binding.Delete[0] != target {
		t.Fatalf("server plan parameters changed: %#v", decoded.Binding.Delete)
	}
	receipt, err := ledger.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: digest, ApplyJobID: running.ID, InvocationID: running.Action.InvocationID})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Action != hostaction.ActionImagePrune || receipt.ParametersDigest != consolejobs.DigestRequest(frozen) {
		t.Fatalf("claim receipt = %#v", receipt)
	}
}
