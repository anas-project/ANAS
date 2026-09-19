package consolejobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
)

type actionConfirmationAudit struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (a *actionConfirmationAudit) AppendContext(_ context.Context, event audit.Event) (audit.Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return audit.Event{}, a.err
	}
	event.Sequence = uint64(len(a.events) + 1)
	a.events = append(a.events, event)
	return event, nil
}

func TestActionConfirmationSharedPlanApplyClaim(t *testing.T) {
	ctx := context.Background()
	aud := &actionConfirmationAudit{}
	confirmations, err := hostconfirmation.OpenForTesting(ctx, filepath.Join(t.TempDir(), "confirmations"), aud)
	if err != nil {
		t.Fatal(err)
	}
	jobsDir := filepath.Join(t.TempDir(), "jobs")
	jobs, err := Open(jobsDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireExecutionLease(ctx, jobsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	params := map[string]any{"scope": "anas-owned"}
	paramsBody, _ := json.Marshal(params)
	binding := actionConfirmationBinding(time.Now().UTC())
	binding.ParametersDigest = DigestRequest(paramsBody)
	plan := createSucceededConfirmationPlan(t, ctx, jobs, lease, binding)
	issued, err := jobs.IssueActionConfirmation(ctx, confirmations, ActionConfirmationIssueInput{
		PlanJobID: plan.ID, Action: binding.Action, Actor: binding.Actor, WorkspaceID: binding.WorkspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding.PlanJobID = plan.ID
	binding.PlanInvocationID = plan.Action.InvocationID
	binding.PlannedAt = plan.CreatedAt.UTC()
	drifted := binding
	drifted.StateDigest = strings.Repeat("e", 64)
	if _, err := jobs.CreateConfirmedActionObserved(ctx, confirmations, CreateSpec{
		WorkspaceID: binding.WorkspaceID, Mutating: true, Request: params,
		Idempotency: IdempotencyInput{Principal: binding.Actor, Key: "apply-key-drift"},
	}, binding.Action, "apply-call-drift", ActionConfirmationApplyInput{
		Token: issued.Token, Binding: drifted, ObservedParametersDigest: binding.ParametersDigest,
		ObservedStateDigest: binding.StateDigest, ObservedSummaryDigest: binding.SummaryDigest, ObservedReleaseDigest: binding.ReleaseDigest,
	}, passActionConfirmationObserver()); !errors.Is(err, ErrConfirmationInvalid) {
		t.Fatalf("state drift error = %v, want confirmation invalid", err)
	}
	apply, err := jobs.CreateConfirmedActionObserved(ctx, confirmations, CreateSpec{
		WorkspaceID: binding.WorkspaceID, Mutating: true, Request: params,
		Idempotency: IdempotencyInput{Principal: binding.Actor, Key: "apply-key"},
	}, binding.Action, "apply-call-1", ActionConfirmationApplyInput{
		Token: issued.Token, Binding: binding, ObservedParametersDigest: binding.ParametersDigest,
		ObservedStateDigest: binding.StateDigest, ObservedSummaryDigest: binding.SummaryDigest, ObservedReleaseDigest: binding.ReleaseDigest,
	}, passActionConfirmationObserver())
	if err != nil {
		t.Fatal(err)
	}
	if apply.Existing || apply.Job.Action == nil || apply.Job.Request[ActionConfirmationBindingDigestRequestKey] != issued.BindingDigest ||
		apply.Job.Request[ConfirmationPlanJobRequestKey] != plan.ID {
		t.Fatalf("apply linkage missing: %#v", apply.Job)
	}
	retry, err := jobs.CreateConfirmedActionObserved(ctx, confirmations, CreateSpec{
		WorkspaceID: binding.WorkspaceID, Mutating: true, Request: params,
		Idempotency: IdempotencyInput{Principal: binding.Actor, Key: "apply-key"},
	}, binding.Action, "apply-call-retry", ActionConfirmationApplyInput{
		Token: hostconfirmation.RawToken("hcnf_" + strings.Repeat("A", 43)), Binding: binding,
		ObservedParametersDigest: binding.ParametersDigest, ObservedStateDigest: binding.StateDigest,
		ObservedSummaryDigest: binding.SummaryDigest, ObservedReleaseDigest: binding.ReleaseDigest,
	}, passActionConfirmationObserver())
	if err != nil || !retry.Existing || retry.Job.ID != apply.Job.ID {
		t.Fatalf("idempotent retry = %#v, %v", retry, err)
	}
	jobJournal, err := os.ReadFile(filepath.Join(jobsDir, JournalFilename))
	if err != nil {
		t.Fatal(err)
	}
	if all := string(jobJournal) + fmt.Sprintf("%#v", apply.Job); strings.Contains(all, issued.Token.Value()) {
		t.Fatal("raw token persisted or formatted")
	}
	running, err := jobs.StartActionObserved(ctx, apply.Job.ID, lease, passActionConfirmationObserver())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.ClaimActionConfirmationForExecution(ctx, confirmations, running.ID, running.Action.InvocationID, issued.BindingDigest); err != nil {
		t.Fatalf("claim for execution: %v", err)
	}
	if _, err := jobs.ClaimActionConfirmationForExecution(ctx, confirmations, running.ID, running.Action.InvocationID, issued.BindingDigest); !errors.Is(err, hostconfirmation.ErrConsumed) {
		t.Fatalf("second execution claim error = %v, want consumed", err)
	}
}

func actionConfirmationBinding(plannedAt time.Time) actionabi.ConfirmationBinding {
	return actionabi.ConfirmationBinding{
		ABI: actionabi.Version, PlanJobID: "plan-placeholder", PlanInvocationID: "plan-call-placeholder",
		Action: "incus.uninstall", Actor: "local-owner", WorkspaceID: "main",
		ParametersDigest: strings.Repeat("a", 64), StateDigest: strings.Repeat("b", 64),
		SummaryDigest: strings.Repeat("c", 64), ReleaseDigest: strings.Repeat("d", 64), PlannedAt: plannedAt.UTC(),
	}
}

func createSucceededConfirmationPlan(t *testing.T, ctx context.Context, jobs *Store, lease *ExecutionLease, binding actionabi.ConfirmationBinding) Job {
	t.Helper()
	plan, err := jobs.CreateActionObserved(ctx, CreateSpec{
		WorkspaceID: binding.WorkspaceID, Mutating: false, Request: map[string]any{},
		Idempotency: IdempotencyInput{
			Principal: binding.Actor, Method: "POST", CanonicalPath: "/internal/actions/incus.uninstall.plan",
			Key: "plan-key", RequestDigest: DigestRequest([]byte("{}")),
		},
	}, "incus.uninstall.plan", "plan-call-1", passActionConfirmationObserver())
	if err != nil {
		t.Fatal(err)
	}
	running, err := jobs.StartActionObserved(ctx, plan.Job.ID, lease, passActionConfirmationObserver())
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	value, _ := json.Marshal(map[string]any{ActionConfirmationPlanResultKey: map[string]any{
		"parameters_digest": binding.ParametersDigest, "state_digest": binding.StateDigest,
		"summary_digest": binding.SummaryDigest, "release_digest": binding.ReleaseDigest,
	}})
	done, err := jobs.CompleteActionObserved(ctx, lease, actionabi.Event{
		ABI: actionabi.Version, JobID: running.ID, InvocationID: running.Action.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: value},
	}, passActionConfirmationObserver())
	if err != nil {
		t.Fatal(err)
	}
	return done
}

func passActionConfirmationObserver() JobCommitObserver {
	return JobCommitObserverFunc(func(context.Context, JobCommitIntent) error { return nil })
}
