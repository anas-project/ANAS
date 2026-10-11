package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// admitConfirmedInstall completes an incus.install plan and admits its
// confirmed apply through the real Store and confirmation ledger. The apply
// job is returned still queued.
func admitConfirmedInstall(t *testing.T) (*HostActionService, *consolejobs.Store, *consolejobs.ExecutionLease, consolejobs.Job) {
	t.Helper()
	ctx := context.Background()
	s, _, store, lease := serviceFixture(t, nil)
	ledger, err := hostconfirmation.OpenForTesting(ctx, filepath.Join(t.TempDir(), "confirmations"), s.options.Journal)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	s.options.Confirmations = ledger
	s.available = true
	s.options.ChineseSpeedup = func(context.Context, string) (bool, error) { return false, nil }
	plan, err := s.InvokePlan(ctx, "alice", "main", hostaction.ActionInstallPlan, json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{}}`), "plan-key")
	if err != nil {
		t.Fatal("plan admission", err)
	}
	started, err := store.StartActionObserved(ctx, plan.Job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	request := incusprovision.Request{Interface: "incus_container", StorageSizeGiB: 64,
		PortRangeFirst: incusprovision.DefaultPortRangeFirst, PortRangeLast: incusprovision.DefaultPortRangeLast}
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
	value, err := json.Marshal(map[string]any{
		"schema": "anas.host-action.incus/v1", "action": hostaction.ActionInstall, "phase": "install", "parameters": params,
		"inspect": incusprovision.InspectResult{Schema: incusprovision.Schema, ObservedAt: time.Now().UTC(),
			Plan: incusprovision.Plan{Schema: incusprovision.Schema, Digest: params.Binding.PlanDigest, StateDigest: strings.Repeat("c", 64), Request: request}},
		"action_confirmation": map[string]string{"parameters_digest": consolejobs.DigestRequest(frozen), "state_digest": strings.Repeat("c", 64),
			"summary_digest": params.Binding.PlanDigest, "release_digest": consolejobs.DigestRequest([]byte(s.options.Release.Version + "/" + s.options.Release.Commit))},
	})
	if err != nil {
		t.Fatal(err)
	}
	unchanged := false
	frame, err := hostaction.ProjectActionEvent(hostaction.ActionInstallPlan, actionabi.Event{ABI: actionabi.Version, JobID: started.ID, InvocationID: started.Action.InvocationID,
		Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &unchanged, Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteActionObserved(ctx, lease, frame, s.observer()); err != nil {
		t.Fatal(err)
	}
	proof, err := s.IssueConfirmation(ctx, "alice", "main", started.ID, hostaction.ActionInstall)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := s.InvokeConfirmed(ctx, "alice", "main", hostaction.ActionInstall, started.ID, body, proof.Token, "apply-key")
	if err != nil || applied.Job.Status != consolejobs.StatusQueued {
		t.Fatal("confirmed admission", err)
	}
	return s, store, lease, applied.Job
}

// runInstallTo starts the queued apply and lets "hostd" end it with event.
func runInstallTo(t *testing.T, s *HostActionService, store *consolejobs.Store, lease *consolejobs.ExecutionLease, job consolejobs.Job, failure actionabi.Failure) consolejobs.Job {
	t.Helper()
	ctx := context.Background()
	running, err := store.StartActionObserved(ctx, job.ID, lease, s.observer())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newHostActionRunner(store, lease, s.options.Release, func(context.Context, consolejobs.Job) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	runner.dial = func(context.Context, actionabi.Request) (hostStream, error) {
		return frames(t, actionabi.Event{ABI: actionabi.Version, JobID: running.ID, InvocationID: running.Action.InvocationID, Type: "error", Error: &failure}), nil
	}
	// The runner also returns an error for an unknown outcome; the recorded
	// job is what the queue sees.
	_, runErr := runner.ExecutePreflight(ctx, running.ID, s.observer())
	finished, err := store.Get(ctx, running.ID)
	if err != nil || (runErr != nil && failure.Outcome != actionabi.Unknown) {
		t.Fatal(err, runErr)
	}
	return finished
}

func pendingCompensation(t *testing.T, store *consolejobs.Store, id string) bool {
	t.Helper()
	pending, err := store.PendingCompensations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(pending, func(job consolejobs.Job) bool { return job.ID == id })
}

// A failed host action used to leave a compensation check nothing cleared,
// so every later write in its workspace stayed queued (finance.hlong.wang,
// 2026-10-11). hostd keeps the host-side recovery obligation as intents.
func TestFailedHostActionDoesNotBlockItsWorkspace(t *testing.T) {
	s, store, lease, job := admitConfirmedInstall(t)
	failed := runInstallTo(t, s, store, lease, job, actionabi.Failure{Outcome: actionabi.Failed,
		Code: "incus_host_action_failed", Message: "Incus host action could not be completed"})
	if failed.Status != consolejobs.StatusFailed || !pendingCompensation(t, store, failed.ID) {
		t.Fatalf("fixture did not produce a failed job awaiting compensation: %s", failed.Status)
	}
	if _, err := s.advance(context.Background()); err != nil {
		t.Fatal(err)
	}
	settled, err := store.Get(context.Background(), failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pendingCompensation(t, store, failed.ID) || settled.Status != consolejobs.StatusFailed ||
		!slices.ContainsFunc(settled.Warnings, func(w string) bool { return strings.Contains(w, "hostd keeps its effect intents") }) {
		t.Fatalf("failed host action still blocks its workspace: %+v", settled)
	}
}

// An unknown outcome may have outlived its evidence; it keeps the check.
func TestUnknownHostActionOutcomeKeepsCompensationCheck(t *testing.T) {
	s, store, lease, job := admitConfirmedInstall(t)
	unknown := runInstallTo(t, s, store, lease, job, actionabi.Failure{Outcome: actionabi.Unknown,
		Code: "host_audit_unconfirmed", Message: "Host action audit completion could not be confirmed"})
	if unknown.Status != consolejobs.StatusInterrupted || !pendingCompensation(t, store, unknown.ID) {
		t.Fatalf("fixture did not produce an interrupted job awaiting compensation: %s", unknown.Status)
	}
	if _, err := s.advance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !pendingCompensation(t, store, unknown.ID) {
		t.Fatal("an unknown host action outcome lost its compensation check")
	}
}

// Any host action that has not started can be cancelled, not just preflight;
// a confirmed apply never reaches hostd's claim.
func TestQueuedConfirmedHostActionCanBeCancelled(t *testing.T) {
	s, store, _, job := admitConfirmedInstall(t)
	canceled, err := s.CancelQueued(context.Background(), "alice", job.ID)
	if err != nil || canceled.Status != consolejobs.StatusCanceled || pendingCompensation(t, store, job.ID) {
		t.Fatalf("queued confirmed install was not cancelled: %v %+v", err, canceled)
	}
	if _, err := s.CancelQueued(context.Background(), "alice", job.ID); !errors.Is(err, consolejobs.ErrConflict) {
		t.Fatalf("second cancel = %v, want conflict", err)
	}
}
