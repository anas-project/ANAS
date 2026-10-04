package hostaction

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incushost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// AuditJournal is satisfied by the EXISTING audit.Writer. The future executor
// installation owns its fixed audit scope, never a path supplied by a request.
// No second job/event store is created. Audit is not job completion evidence.
type AuditJournal interface {
	AppendContext(context.Context, audit.Event) (audit.Event, error)
}

// executePreflight runs only the compiled read-only preflight handler. Its context must
// come from the execution owner, NOT from a subscriber/HTTP request. Invocation
// job ids originate in the shared dispatcher; wiring/validating that handoff is
// still required before production use. No root binary/listener is installed.
// The returned terminal is provisional: ActionRecorder must still check actual
// process EOF/exit before committing a job outcome.
func executePreflight(ctx context.Context, call *Invocation, journal AuditJournal) (actionabi.Event, error) {
	return execute(ctx, call, journal, incushost.LocalPreflight)
}

// Test-only dependency injection is kept unexported; neither a manifest nor a
// request can install arbitrary handlers in the privileged registry.
func execute(ctx context.Context, call *Invocation, journal AuditJournal, probe func(context.Context, incushost.Options) (incushost.Report, error)) (actionabi.Event, error) {
	if ctx == nil || call == nil || !call.peer.verified || call.request.Action != ActionStatus || string(call.request.Parameters) != "{}" || journal == nil || probe == nil {
		return actionabi.Event{}, ErrUnavailable
	}
	if ctx.Err() != nil {
		return actionabi.Event{}, ctx.Err()
	}
	if !call.used.CompareAndSwap(false, true) {
		return actionabi.Event{}, ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	event := actionabi.Event{ABI: actionabi.Version, JobID: call.request.JobID, InvocationID: call.request.InvocationID}
	baseAudit := func(kind, outcome string) audit.Event {
		return audit.Event{
			Type: kind, Actor: fmt.Sprintf("uid:%d", call.peer.uid), Outcome: outcome,
			Details: map[string]any{"action": call.request.Action, "job_id": call.request.JobID, "invocation_id": call.request.InvocationID, "peer_uid": call.peer.uid, "peer_gid": call.peer.gid, "peer_pid": call.peer.pid, "parameters": map[string]any{}},
		}
	}
	if _, err := journal.AppendContext(ctx, baseAudit("host_action_started", "")); err != nil {
		return actionabi.Event{}, ErrAudit
	}
	report, probeErr := probe(ctx, incushost.Options{})
	if probeErr == nil {
		probeErr = ctx.Err()
	}
	if probeErr == nil {
		body, err := json.Marshal(report)
		if err == nil {
			changed := false
			event.Type = "result"
			event.Result = &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}
		} else {
			probeErr = err
		}
	}
	if probeErr != nil {
		event.Type = "error"
		event.Error = &actionabi.Failure{Outcome: actionabi.Failed, Code: "host_observation_failed", Message: "Host preflight could not be completed"}
	}
	outcome := actionabi.Succeeded
	if event.Error != nil {
		outcome = event.Error.Outcome
	}
	// Cancellation/timeout must not suppress the audit result. A caller must
	// keep the journal/execution lease alive through this bounded final write.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if _, err := journal.AppendContext(finishCtx, baseAudit("host_action_completed", string(outcome))); err != nil {
		event.Type = "error"
		event.Result = nil
		event.Error = &actionabi.Failure{Outcome: actionabi.Unknown, Code: "host_audit_unconfirmed", Message: "Host action audit completion could not be confirmed"}
		return event, ErrAudit
	}
	if _, err := actionabi.EncodeExecutorEvent(event); err != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	return event, nil
}

type ConfirmationClaimer interface {
	Claim(context.Context, hostconfirmation.ClaimRequest) (hostconfirmation.ClaimReceipt, error)
	Close() error
}

var openConfirmationLedger = func(ctx context.Context, journal AuditJournal) (ConfirmationClaimer, error) {
	return hostconfirmation.OpenProduction(ctx, journal)
}

var newIncusBackend = func() incusBackend {
	return incusprovision.NewLocalBackend()
}

var newImagePruneBackend = func() imagePruneBackend {
	return incusprovision.NewImagePruneBackend()
}

type incusBackend interface {
	SyncTraefik(context.Context) (incusprovision.TraefikSyncResult, error)
	SyncPorts(context.Context) (incusprovision.PortSyncResult, error)
	Inspect(context.Context, incusprovision.Request) (incusprovision.InspectResult, error)
	Install(context.Context, incusprovision.Request, incusprovision.Binding) (incusprovision.ApplyResult, error)
	Configure(context.Context, incusprovision.Request, incusprovision.Binding) (incusprovision.ApplyResult, error)
	Enroll(context.Context, incusprovision.Request, incusprovision.Binding) (incusprovision.ApplyResult, error)
	Uninstall(context.Context, incusprovision.Request, incusprovision.Binding) (incusprovision.ApplyResult, error)
}

type imagePruneBackend interface {
	Plan(context.Context, incusprovision.ImagePruneRequest) (incusprovision.ImagePrunePlanResult, error)
	Apply(context.Context, incusprovision.ImagePruneRequest, incusprovision.ImagePruneBinding) (incusprovision.ImagePruneApplyResult, error)
}

func executeIncusProvision(ctx context.Context, call *Invocation, journal AuditJournal, release ReleaseIdentity) (actionabi.Event, error) {
	if ctx == nil || call == nil || !call.peer.verified || journal == nil {
		return actionabi.Event{}, ErrUnavailable
	}
	spec, ok := LookupAction(call.request.Action)
	if !ok || call.request.Action == ActionStatus {
		return actionabi.Event{}, ErrRequest
	}
	if !call.used.CompareAndSwap(false, true) {
		return actionabi.Event{}, ErrRequest
	}
	timeout := time.Duration(spec.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	event := actionabi.Event{ABI: actionabi.Version, JobID: call.request.JobID, InvocationID: call.request.InvocationID}
	baseAudit := func(kind, outcome string) audit.Event {
		return audit.Event{
			Type: kind, Actor: fmt.Sprintf("uid:%d", call.peer.uid), Outcome: outcome,
			Details: map[string]any{"action": call.request.Action, "job_id": call.request.JobID, "invocation_id": call.request.InvocationID, "peer_uid": call.peer.uid, "peer_gid": call.peer.gid, "peer_pid": call.peer.pid,
				"parameters_digest": consolejobs.DigestRequest(call.request.Parameters)},
		}
	}
	if _, err := journal.AppendContext(ctx, baseAudit("host_action_started", "")); err != nil {
		return actionabi.Event{}, ErrAudit
	}
	backend := newIncusBackend()
	var value any
	changed := spec.Mutating
	var runErr error
	if spec.PlanFor != "" {
		if call.request.Action == ActionImagePrunePlan {
			params, err := DecodeImagePrunePlanParameters(call.request.Action, call.request.Parameters)
			if err != nil {
				runErr = err
			} else {
				value, runErr = buildImagePrunePlanValue(ctx, newImagePruneBackend(), params.WorkspaceID, release)
				changed = false
			}
		} else {
			params, err := DecodePlanParameters(call.request.Action, call.request.Parameters)
			if err != nil {
				runErr = err
			} else {
				value, runErr = buildProvisionPlanValue(ctx, backend, spec, params.Request, release)
				changed = false
			}
		}
	} else if spec.Sync {
		// Approved once by incus.configure; the backend refuses without that
		// approval and derives every address from Docker itself.
		if string(call.request.Parameters) != "{}" {
			runErr = ErrRequest
		} else if call.request.Action == ActionTraefikSync {
			value, runErr = backend.SyncTraefik(ctx)
		} else if call.request.Action == ActionPortsSync {
			value, runErr = backend.SyncPorts(ctx)
		} else {
			runErr = ErrRequest
		}
	} else if spec.Mutating {
		if call.request.Action == ActionImagePrune {
			params, digest, err := DecodeImagePruneApplyParameters(call.request.Action, call.request.Parameters)
			if err != nil {
				runErr = err
			} else if err = claimConsumedImagePruneApproval(ctx, journal, digest, call.request.JobID, call.request.InvocationID, release, params); err != nil {
				runErr = err
			} else {
				value, runErr = newImagePruneBackend().Apply(ctx, params.Request, params.Binding)
			}
		} else {
			params, digest, err := DecodeApplyParameters(call.request.Action, call.request.Parameters)
			if err != nil {
				runErr = err
			} else if err = claimConsumedApproval(ctx, journal, digest, call.request.JobID, call.request.InvocationID, spec, release, params); err != nil {
				runErr = err
			} else {
				value, runErr = runProvisionApply(ctx, backend, spec.Phase, params.Request, params.Binding)
			}
		}
	}
	if runErr == nil {
		runErr = ctx.Err()
	}
	if runErr == nil {
		body, err := json.Marshal(value)
		if err == nil {
			event.Type = "result"
			event.Result = &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}
		} else {
			runErr = err
		}
	}
	if runErr != nil {
		event.Type = "error"
		event.Error = &actionabi.Failure{Outcome: actionabi.Failed, Code: "incus_host_action_failed", Message: "Incus host action could not be completed"}
	}
	outcome := actionabi.Succeeded
	if event.Error != nil {
		outcome = event.Error.Outcome
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if _, err := journal.AppendContext(finishCtx, baseAudit("host_action_completed", string(outcome))); err != nil {
		event.Type = "error"
		event.Result = nil
		event.Error = &actionabi.Failure{Outcome: actionabi.Unknown, Code: "host_audit_unconfirmed", Message: "Host action audit completion could not be confirmed"}
		return event, ErrAudit
	}
	if _, err := actionabi.EncodeExecutorEvent(event); err != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	return event, nil
}

func buildImagePrunePlanValue(ctx context.Context, backend imagePruneBackend, workspaceID string, release ReleaseIdentity) (any, error) {
	if backend == nil || workspaceID == "" || release.Validate() != nil {
		return nil, ErrUnavailable
	}
	request := incusprovision.ImagePruneRequest{Schema: incusprovision.ImagePruneSchema, WorkspaceID: workspaceID}
	plan, err := backend.Plan(ctx, request)
	if err != nil {
		return nil, err
	}
	applyParams := IncusImagePruneApplyParameters{
		Schema:  parameterSchema,
		Request: request,
		Binding: incusprovision.ImagePruneBinding{
			Schema: incusprovision.ImagePruneSchema, PlanDigest: plan.Digest, WorkspaceID: workspaceID,
			Delete: plan.Delete, StateDigest: plan.StateDigest, SummaryDigest: plan.Digest,
		},
	}
	body, err := marshalCanonical(applyParams)
	if err != nil {
		return nil, err
	}
	_, frozen, err := FrozenRequest(ActionImagePrune, release, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"schema": parameterSchema, "action": ActionImagePrune, "plan": plan, "parameters": applyParams,
		consolejobs.ActionConfirmationPlanResultKey: map[string]string{
			"parameters_digest": consolejobsDigest(frozen),
			"state_digest":      plan.StateDigest,
			"summary_digest":    plan.Digest,
			"release_digest":    consolejobsDigest([]byte(release.Version + "/" + release.Commit)),
		},
	}, nil
}

func buildProvisionPlanValue(ctx context.Context, backend incusBackend, spec ActionSpec, request incusprovision.Request, release ReleaseIdentity) (any, error) {
	if backend == nil || spec.PlanFor == "" || release.Validate() != nil {
		return nil, ErrUnavailable
	}
	inspect, err := backend.Inspect(ctx, request)
	if err != nil {
		return nil, err
	}
	applyParams := IncusApplyParameters{
		Schema: parameterSchema, Request: request,
		Binding: incusprovision.Binding{Schema: incusprovision.Schema, PlanDigest: inspect.Plan.Digest, Phase: spec.Phase, Destructive: true},
	}
	body, err := marshalCanonical(applyParams)
	if err != nil {
		return nil, err
	}
	_, frozen, err := FrozenRequest(spec.PlanFor, release, body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"schema": parameterSchema, "action": spec.PlanFor, "phase": spec.Phase,
		"inspect": inspect, "parameters": applyParams,
		consolejobs.ActionConfirmationPlanResultKey: map[string]string{
			"parameters_digest": consolejobsDigest(frozen),
			"state_digest":      inspect.Plan.StateDigest,
			"summary_digest":    inspect.Plan.Digest,
			"release_digest":    consolejobsDigest([]byte(release.Version + "/" + release.Commit)),
		},
	}, nil
}

func runProvisionApply(ctx context.Context, backend incusBackend, phase incusprovision.Phase, request incusprovision.Request, binding incusprovision.Binding) (any, error) {
	if backend == nil {
		return nil, ErrUnavailable
	}
	switch phase {
	case incusprovision.PhaseInstall:
		return backend.Install(ctx, request, binding)
	case incusprovision.PhaseConfigure:
		return backend.Configure(ctx, request, binding)
	case incusprovision.PhaseEnroll:
		return backend.Enroll(ctx, request, binding)
	case incusprovision.PhaseUninstall:
		return backend.Uninstall(ctx, request, binding)
	default:
		return nil, ErrRequest
	}
}

func claimConsumedApproval(ctx context.Context, journal AuditJournal, digest, jobID, invocationID string, spec ActionSpec, release ReleaseIdentity, params IncusApplyParameters) error {
	store, err := openConfirmationLedger(ctx, journal)
	if err != nil {
		return ErrDenied
	}
	receipt, claimErr := store.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: digest, ApplyJobID: jobID, InvocationID: invocationID})
	closeErr := store.Close()
	if claimErr != nil || closeErr != nil {
		return ErrDenied
	}
	if receipt.Action != spec.Name || receipt.ApplyJobID != jobID || receipt.InvocationID != invocationID ||
		receipt.SummaryDigest != params.Binding.PlanDigest || receipt.StateDigest == "" ||
		receipt.ReleaseDigest != consolejobsDigest([]byte(release.Version+"/"+release.Commit)) {
		return ErrDenied
	}
	public, err := marshalCanonical(params)
	if err != nil {
		return ErrDenied
	}
	_, frozen, err := FrozenRequest(spec.Name, release, public)
	if err != nil {
		return ErrDenied
	}
	if consolejobsDigest(frozen) != receipt.ParametersDigest {
		return ErrDenied
	}
	return nil
}

func claimConsumedImagePruneApproval(ctx context.Context, journal AuditJournal, digest, jobID, invocationID string, release ReleaseIdentity, params IncusImagePruneApplyParameters) error {
	store, err := openConfirmationLedger(ctx, journal)
	if err != nil {
		return ErrDenied
	}
	receipt, claimErr := store.Claim(ctx, hostconfirmation.ClaimRequest{BindingDigest: digest, ApplyJobID: jobID, InvocationID: invocationID})
	closeErr := store.Close()
	if claimErr != nil || closeErr != nil {
		return ErrDenied
	}
	if receipt.Action != ActionImagePrune || receipt.ApplyJobID != jobID || receipt.InvocationID != invocationID ||
		receipt.SummaryDigest != params.Binding.PlanDigest || receipt.StateDigest != params.Binding.StateDigest ||
		receipt.ReleaseDigest != consolejobsDigest([]byte(release.Version+"/"+release.Commit)) {
		return ErrDenied
	}
	public, err := marshalCanonical(params)
	if err != nil {
		return ErrDenied
	}
	_, frozen, err := FrozenRequest(ActionImagePrune, release, public)
	if err != nil {
		return ErrDenied
	}
	if consolejobsDigest(frozen) != receipt.ParametersDigest {
		return ErrDenied
	}
	return nil
}

func writeActionEvent(w io.Writer, event actionabi.Event) error {
	frame, err := actionabi.EncodeExecutorEvent(event)
	if err != nil {
		return ErrUnavailable
	}
	if _, err := w.Write(frame); err != nil {
		return ErrUnavailable
	}
	return nil
}

func consolejobsDigest(body []byte) string {
	return consolejobs.DigestRequest(body)
}
