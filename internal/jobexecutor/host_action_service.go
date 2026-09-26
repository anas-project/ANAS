package jobexecutor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
)

type HostActionServiceOptions struct {
	Store         *consolejobs.Store
	Lease         *consolejobs.ExecutionLease
	Confirmations *hostconfirmation.Store
	Release       hostaction.ReleaseIdentity
	Journal       hostaction.AuditJournal
	Workspaces    []string
	// Trusted launcher and this queue must share the same coordinator. Nil
	// creates an empty in-process owner set; it is not disk recovery evidence.
	IngressCoordinator *computeingressruntime.ControllerCoordinator
	// Re-resolve the persisted actor, not a remembered HTTP request or cookie.
	// Called under jobs.lock as well; must not reenter Store.
	Authorize func(context.Context, string, string) error
}

type hostActionRuntime interface {
	Run(context.Context) error
	Ready() <-chan struct{}
	ExecutePreflight(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	Stop()
	Close() error
}

// HostActionService is the daemon-owned queue consumer and admission facade.
// It uses the existing journal, lease and broker for compiled host actions.
// HTTP/CLI Invoke only enqueues. No callbacks,
// executables or privileged parameters can be registered through this API.
type HostActionService struct {
	mu                          sync.Mutex
	options                     HostActionServiceOptions
	workspaces                  map[string]bool
	runtime                     hostActionRuntime
	ready                       chan struct{}
	wake                        chan struct{}
	started, stopped, available bool
	closing                     bool
	owner                       context.Context
	ingressChanges              map[string]*hostIngressChange
}

func NewHostActionService(o HostActionServiceOptions) (*HostActionService, error) {
	if o.Store == nil || o.Lease == nil || o.Journal == nil || o.Authorize == nil || o.Release.Validate() != nil || len(o.Workspaces) == 0 || len(o.Workspaces) > 1024 {
		return nil, hostaction.ErrUnavailable
	}
	if o.IngressCoordinator == nil {
		var err error
		o.IngressCoordinator, err = computeingressruntime.NewControllerCoordinator(o.Workspaces)
		if err != nil {
			return nil, hostaction.ErrUnavailable
		}
	}
	if !o.IngressCoordinator.MatchesScopes(o.Workspaces) {
		return nil, hostaction.ErrUnavailable
	}
	s := &HostActionService{options: o, workspaces: map[string]bool{}, ready: make(chan struct{}), wake: make(chan struct{}, 1), ingressChanges: map[string]*hostIngressChange{}}
	for _, id := range o.Workspaces {
		if id == "" || len(id) > 64 || strings.TrimSpace(id) != id || s.workspaces[id] {
			return nil, hostaction.ErrUnavailable
		}
		s.workspaces[id] = true
	}
	b, err := NewHostJobBroker(HostJobBrokerOptions{Store: o.Store, Lease: o.Lease, Release: o.Release, Journal: o.Journal,
		Authorize: func(ctx context.Context, job consolejobs.Job, _ hostaction.PeerIdentity) error {
			if err := s.checkIngressChange(job); err != nil {
				return err
			}
			return s.authorize(ctx, job.CreatedBy, job.WorkspaceID)
		},
	})
	if err != nil {
		return nil, err
	}
	s.runtime = b
	return s, nil
}

func (s *HostActionService) Ready() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.ready
}

// CancelQueuedPreflight is explicit, authorized cancellation only. Running
// preflight has no cooperative-cancel channel; never report it as cancelled
// merely because an HTTP client or subscriber disconnected.
func (s *HostActionService) CancelQueuedPreflight(ctx context.Context, actor, jobID string) (consolejobs.Job, error) {
	if s == nil || ctx == nil {
		return consolejobs.Job{}, hostaction.ErrUnavailable
	}
	job, err := s.options.Store.Get(ctx, jobID)
	if err != nil {
		return consolejobs.Job{}, err
	}
	if !s.requestMatches(job) || s.authorize(ctx, actor, job.WorkspaceID) != nil {
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	if job.Status != consolejobs.StatusQueued {
		return consolejobs.Job{}, consolejobs.ErrConflict
	}
	observer := consolejobs.JobCommitObserverFunc(func(ctx context.Context, i consolejobs.JobCommitIntent) error {
		if s.authorize(ctx, actor, job.WorkspaceID) != nil {
			return hostaction.ErrDenied
		}
		_, err := s.options.Journal.AppendContext(ctx, audit.Event{Type: "host_job_transition", Actor: actor, WorkspaceID: job.WorkspaceID, Outcome: string(i.Next.Status),
			Details: map[string]any{"operation": "cancel_queued", "action": "incus.status", "job_id": job.ID, "invocation_id": job.Action.InvocationID}})
		if err != nil {
			return hostaction.ErrAudit
		}
		return nil
	})
	return s.options.Store.CancelQueuedActionObserved(ctx, job.ID, job.Action.InvocationID, observer)
}

// HostActionRecoveryObserver lets the EXISTING startup recovery journal this
// action even when the optional service is disabled. It neither reauthorizes a
// lost execution nor clears the durable daemon-restarted admission barrier.
func HostActionRecoveryObserver(journal hostaction.AuditJournal) consolejobs.JobCommitObserver {
	return consolejobs.JobCommitObserverFunc(func(ctx context.Context, i consolejobs.JobCommitIntent) error {
		j := i.Next
		if journal == nil || i.Previous == nil || i.Previous.Status != consolejobs.StatusRunning || j.Action == nil ||
			j.Status != consolejobs.StatusInterrupted || j.Error == nil || j.Error.Code != "daemon_restarted" {
			return hostaction.ErrDenied
		}
		_, err := journal.AppendContext(ctx, audit.Event{Type: "host_job_transition", Actor: j.CreatedBy, WorkspaceID: j.WorkspaceID, Outcome: string(j.Status),
			Details: map[string]any{"operation": "recovery", "action": j.Action.Name, "job_id": j.ID, "invocation_id": j.Action.InvocationID}})
		if err != nil {
			return hostaction.ErrAudit
		}
		return nil
	})
}
func (s *HostActionService) authorize(ctx context.Context, actor, workspace string) error {
	if ctx == nil || ctx.Err() != nil || actor == "" || len(actor) > 256 || !s.workspaces[workspace] || s.options.Authorize(ctx, actor, workspace) != nil {
		return hostaction.ErrDenied
	}
	return nil
}
func (s *HostActionService) admission() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.available && !s.stopped
}

// InvokePreflight uses action-scoped retry keys and the Store's full frozen
// request comparison. Actor/transport do not partition keys. Admission and
// retries are authorized again immediately before durable create/join.
func (s *HostActionService) InvokePreflight(ctx context.Context, actor, workspace, key string) (consolejobs.CreateResult, error) {
	return s.Invoke(ctx, actor, workspace, hostaction.ActionStatus, json.RawMessage(`{}`), key)
}

func (s *HostActionService) InvokePlan(ctx context.Context, actor, workspace, action string, parameters json.RawMessage, key string) (consolejobs.CreateResult, error) {
	spec, ok := hostaction.LookupAction(action)
	if !ok || spec.PlanFor == "" {
		return consolejobs.CreateResult{}, hostaction.ErrRequest
	}
	return s.Invoke(ctx, actor, workspace, action, parameters, key)
}

func (s *HostActionService) InvokeImagePrunePlan(ctx context.Context, actor, workspace, key string) (consolejobs.CreateResult, error) {
	parameters, err := hostaction.CanonicalParameters(hostaction.ActionImagePrunePlan, mustMarshalJSON(hostaction.IncusImagePrunePlanParameters{Schema: "anas.host-action.incus/v1", WorkspaceID: workspace}))
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	return s.Invoke(ctx, actor, workspace, hostaction.ActionImagePrunePlan, parameters, key)
}

func (s *HostActionService) IssueConfirmation(ctx context.Context, actor, workspace, planJobID, action string) (hostconfirmation.IssueResult, error) {
	if s == nil || s.options.Confirmations == nil || !s.actionAdmission(action) {
		return hostconfirmation.IssueResult{}, hostaction.ErrUnavailable
	}
	if _, ok := hostaction.LookupAction(action); !ok || !hostaction.IsApplyAction(action) || s.authorize(ctx, actor, workspace) != nil {
		return hostconfirmation.IssueResult{}, hostaction.ErrDenied
	}
	return s.options.Store.IssueActionConfirmation(ctx, s.options.Confirmations, consolejobs.ActionConfirmationIssueInput{
		PlanJobID: planJobID, Action: action, Actor: actor, WorkspaceID: workspace,
	})
}

func (s *HostActionService) InvokeConfirmed(ctx context.Context, actor, workspace, action string, planJobID string, parameters json.RawMessage, token hostconfirmation.RawToken, key string) (consolejobs.CreateResult, error) {
	if s == nil || !s.actionAdmission(action) {
		return consolejobs.CreateResult{}, hostaction.ErrUnavailable
	}
	spec, ok := hostaction.LookupAction(action)
	if !ok || !spec.RequiresConfirm || s.options.Confirmations == nil {
		return consolejobs.CreateResult{}, hostaction.ErrRequest
	}
	if err := s.authorize(ctx, actor, workspace); err != nil {
		return consolejobs.CreateResult{}, err
	}
	canonical, err := hostaction.CanonicalParameters(action, parameters)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	if !hostaction.ObservationScopeMatchesWorkspace(action, canonical, workspace) {
		return consolejobs.CreateResult{}, hostaction.ErrDenied
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return consolejobs.CreateResult{}, hostaction.ErrUnavailable
	}
	invocation := hex.EncodeToString(id[:])
	request, err := HostActionRequest(action, s.options.Release, canonical)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	binding, err := s.confirmationBinding(ctx, actor, workspace, action, planJobID)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	result, err := s.options.Store.CreateConfirmedActionObserved(ctx, s.options.Confirmations, consolejobs.CreateSpec{WorkspaceID: workspace, Request: request,
		Mutating: true, Idempotency: consolejobs.IdempotencyInput{Principal: actor, Method: "POST", CanonicalPath: "/host/" + action, Key: key}},
		action, invocation, consolejobs.ActionConfirmationApplyInput{
			Token: token, Binding: binding, ObservedParametersDigest: binding.ParametersDigest, ObservedStateDigest: binding.StateDigest,
			ObservedSummaryDigest: binding.SummaryDigest, ObservedReleaseDigest: binding.ReleaseDigest,
		}, s.observer())
	return s.finishInvoke(ctx, actor, result, err)
}

func (s *HostActionService) InvokeImagePruneConfirmed(ctx context.Context, actor, workspace, planJobID string, token hostconfirmation.RawToken, key string) (consolejobs.CreateResult, error) {
	parameters, err := s.imagePruneParametersFromPlan(ctx, actor, workspace, planJobID)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	return s.InvokeConfirmed(ctx, actor, workspace, hostaction.ActionImagePrune, planJobID, parameters, token, key)
}

func (s *HostActionService) imagePruneParametersFromPlan(ctx context.Context, actor, workspace, planJobID string) (json.RawMessage, error) {
	plan, err := s.options.Store.Get(ctx, planJobID)
	if err != nil {
		return nil, err
	}
	if plan.Status != consolejobs.StatusSucceeded || plan.Action == nil || plan.Action.Name != hostaction.ActionImagePrunePlan ||
		plan.CreatedBy != actor || plan.WorkspaceID != workspace {
		return nil, consolejobs.ErrConfirmationInvalid
	}
	value := plan.Result
	if nested, ok := value["value"].(map[string]any); ok {
		value = nested
	}
	raw, ok := value["parameters"]
	if !ok {
		return nil, consolejobs.ErrConfirmationInvalid
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil, consolejobs.ErrConfirmationInvalid
	}
	return hostaction.CanonicalParameters(hostaction.ActionImagePrune, body)
}

func (s *HostActionService) confirmationBinding(ctx context.Context, actor, workspace, action, planJobID string) (actionabi.ConfirmationBinding, error) {
	if planJobID == "" {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	plan, err := s.options.Store.Get(ctx, planJobID)
	if err != nil {
		return actionabi.ConfirmationBinding{}, err
	}
	if plan.Status != consolejobs.StatusSucceeded || plan.Action == nil || plan.Action.InvocationID == "" ||
		plan.CreatedBy != actor || plan.WorkspaceID != workspace {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	expectedPlanAction, ok := hostaction.PlanActionFor(action)
	if !ok || plan.Action.Name != expectedPlanAction {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	value := plan.Result
	if nested, ok := value["value"].(map[string]any); ok {
		value = nested
	}
	if valueAction, _ := value["action"].(string); valueAction != action {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	raw, ok := value[consolejobs.ActionConfirmationPlanResultKey].(map[string]any)
	if !ok {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	digest := func(key string) (string, bool) {
		v, ok := raw[key].(string)
		return v, ok && len(v) == 64
	}
	parametersDigest, ok1 := digest("parameters_digest")
	stateDigest, ok2 := digest("state_digest")
	summaryDigest, ok3 := digest("summary_digest")
	releaseDigest, ok4 := digest("release_digest")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	binding := actionabi.ConfirmationBinding{
		ABI: actionabi.Version, PlanJobID: plan.ID, PlanInvocationID: plan.Action.InvocationID,
		Action: action, WorkspaceID: workspace, Actor: actor, ParametersDigest: parametersDigest,
		StateDigest: stateDigest, SummaryDigest: summaryDigest, ReleaseDigest: releaseDigest,
		PlannedAt: plan.CreatedAt.UTC(),
	}
	if binding.Validate() != nil {
		return actionabi.ConfirmationBinding{}, consolejobs.ErrConfirmationInvalid
	}
	return binding, nil
}

func (s *HostActionService) Invoke(ctx context.Context, actor, workspace, action string, parameters json.RawMessage, key string) (consolejobs.CreateResult, error) {
	return s.invoke(ctx, actor, workspace, action, parameters, key, false)
}

func (s *HostActionService) invoke(ctx context.Context, actor, workspace, action string, parameters json.RawMessage, key string, withdrawal bool) (consolejobs.CreateResult, error) {
	if s == nil || !s.actionAdmission(action) {
		return consolejobs.CreateResult{}, hostaction.ErrUnavailable
	}
	spec, ok := hostaction.LookupAction(action)
	if !ok || spec.Mutating && (!withdrawal || action != hostaction.ActionForwardingWithdraw) {
		return consolejobs.CreateResult{}, hostaction.ErrRequest
	}
	if err := s.authorize(ctx, actor, workspace); err != nil {
		return consolejobs.CreateResult{}, err
	}
	canonical, err := hostaction.CanonicalParameters(action, parameters)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	if !hostaction.ObservationScopeMatchesWorkspace(action, canonical, workspace) {
		return consolejobs.CreateResult{}, hostaction.ErrDenied
	}
	request, err := HostActionRequest(action, s.options.Release, canonical)
	if err != nil {
		return consolejobs.CreateResult{}, err
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return consolejobs.CreateResult{}, hostaction.ErrUnavailable
	}
	result, err := s.options.Store.CreateActionWithPolicyObserved(ctx, consolejobs.CreateSpec{WorkspaceID: workspace, Request: request, Mutating: spec.Mutating,
		Idempotency: consolejobs.IdempotencyInput{Principal: actor, Method: "POST", CanonicalPath: "/host/" + action, Key: key}},
		action, hex.EncodeToString(id[:]), spec.Policy, s.observer())
	return s.finishInvoke(ctx, actor, result, err)
}

func (s *HostActionService) finishInvoke(ctx context.Context, actor string, result consolejobs.CreateResult, err error) (consolejobs.CreateResult, error) {
	if err != nil {
		// Conflict ids can refer to a different workspace; keep them private.
		var retry *consolejobs.IdempotencyConflictError
		var inflight *consolejobs.ActionInFlightError
		if errors.As(err, &retry) || errors.As(err, &inflight) {
			return consolejobs.CreateResult{}, consolejobs.ErrConflict
		}
		return consolejobs.CreateResult{}, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	if s.authorize(ctx, actor, result.Job.WorkspaceID) != nil {
		return consolejobs.CreateResult{}, hostaction.ErrDenied
	}
	return result, nil
}

func (s *HostActionService) observer() consolejobs.JobCommitObserver {
	return consolejobs.JobCommitObserverFunc(func(ctx context.Context, i consolejobs.JobCommitIntent) error {
		if i.Next.Action == nil || !s.workspaces[i.Next.WorkspaceID] {
			return hostaction.ErrDenied
		}
		actor := i.Next.CreatedBy
		if i.Operation == consolejobs.JobCommitActionJoin {
			actor = i.Actor
		}
		if i.Operation == consolejobs.JobCommitCreate || i.Operation == consolejobs.JobCommitActionJoin || i.Operation == consolejobs.JobCommitStart {
			if !s.actionAdmission(i.Next.Action.Name) || s.authorize(ctx, actor, i.Next.WorkspaceID) != nil {
				return hostaction.ErrDenied
			}
		}
		if i.Next.Action.Outcome == actionabi.Succeeded && s.authorize(ctx, actor, i.Next.WorkspaceID) != nil {
			return hostaction.ErrDenied
		}
		_, err := s.options.Journal.AppendContext(ctx, audit.Event{Type: "host_job_transition", Actor: actor, WorkspaceID: i.Next.WorkspaceID, Outcome: string(i.Next.Status),
			Details: map[string]any{"operation": string(i.Operation), "action": i.Next.Action.Name, "job_id": i.Next.ID, "invocation_id": i.Next.Action.InvocationID}})
		if err != nil {
			return hostaction.ErrAudit
		}
		return nil
	})
}

func (s *HostActionService) requestMatches(job consolejobs.Job) bool {
	if job.Action == nil || job.Action.ABI != actionabi.Version || !s.workspaces[job.WorkspaceID] {
		return false
	}
	spec, ok := hostaction.LookupAction(job.Action.Name)
	if !ok || job.Mutating != spec.Mutating {
		return false
	}
	parameters, err := publicParametersFromStoredRequest(job.Action.Name, job.Request)
	if err != nil {
		return false
	}
	if !hostaction.ObservationScopeMatchesWorkspace(job.Action.Name, parameters, job.WorkspaceID) {
		return false
	}
	want, _ := HostActionRequest(job.Action.Name, s.options.Release, parameters)
	a, e := json.Marshal(want)
	request := job.Request
	if spec.RequiresConfirm {
		request = publicStoredRequest(job.Request)
	}
	b, f := json.Marshal(request)
	return e == nil && f == nil && bytes.Equal(a, b)
}

func (s *HostActionService) Run(owner context.Context) (result error) {
	if s == nil || owner == nil {
		return hostaction.ErrUnavailable
	}
	s.mu.Lock()
	if s.started || s.stopped {
		s.mu.Unlock()
		return hostaction.ErrUnavailable
	}
	s.started, s.owner = true, owner
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.stopped = true; s.available = false; s.mu.Unlock() }()
	unretain, err := s.options.Lease.Retain()
	if err != nil {
		return hostaction.ErrUnavailable
	}
	defer unretain()
	// SIGTERM closes mutation admission first; old controllers still need the
	// broker/queue/lease for drain. Cancel the runtime only after they stop.
	ctx, cancel := context.WithCancel(context.WithoutCancel(owner))
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.runtime.Run(ctx) }()
	var runtimeErr error
	received := false
	defer func() {
		s.mu.Lock()
		s.available = false
		s.stopped = true
		s.mu.Unlock()
		s.runtime.Stop()
		cancel()
		if !received {
			runtimeErr = <-done
		}
		if runtimeErr != nil {
			result = errors.Join(result, hostaction.ErrUnavailable)
		}
		if err := s.runtime.Close(); err != nil {
			result = errors.Join(result, err)
		}
	}()
	select {
	case <-s.runtime.Ready():
	case runtimeErr = <-done:
		received = true
		if ctx.Err() != nil && runtimeErr == nil {
			return nil
		}
		return hostaction.ErrUnavailable
	case <-owner.Done():
		return nil
	}
	s.mu.Lock()
	s.available = true
	s.mu.Unlock()
	close(s.ready)
	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()
	ownerDone := owner.Done()
	for {
		if owner.Err() != nil {
			s.mu.Lock()
			s.closing = true
			s.mu.Unlock()
			ownerDone = nil // Do not busy-spin on a closed cancellation channel.
			shutdown, err := s.options.IngressCoordinator.BeginShutdown(ctx)
			if err != nil {
				return hostaction.ErrUnavailable
			}
			if ready, err := shutdown.Poll(); ready && err == nil {
				return nil
			}
			// Failure is retained, not silently retried. The trusted owner may
			// call RetryIngressShutdown; readonly cleanup dependencies still run.
		}
		advanced, err := s.advance(ctx)
		if err != nil {
			return err
		}
		if advanced {
			continue
		}
		select {
		case runtimeErr = <-done:
			received = true
			// Both channels may become ready during a normal daemon stop.
			// Do not turn a clean runtime shutdown into a spurious failure.
			if ctx.Err() != nil && runtimeErr == nil {
				return nil
			}
			return hostaction.ErrUnavailable
		case <-ownerDone:
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *HostActionService) advance(ctx context.Context) (bool, error) {
	jobs, err := s.options.Store.List(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		return false, hostaction.ErrUnavailable
	}
	for _, j := range jobs {
		if j.Action != nil && (consolejobs.ActionContainmentLost(j) || (j.Error != nil && j.Error.Code == "daemon_restarted") || (j.Status == consolejobs.StatusRunning)) {
			return false, consolejobs.ErrActionContainment
		}
	}
	if err := s.retireIngressChanges(jobs); err != nil {
		return false, err
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	for _, job := range jobs {
		if job.Action == nil || job.Status != consolejobs.StatusQueued {
			continue
		}
		if _, ok := hostaction.LookupAction(job.Action.Name); !ok {
			continue
		}
		if !s.actionAdmission(job.Action.Name) {
			continue // Shutdown preserves queued configuration jobs, unstarted.
		}
		if !s.workspaces[job.WorkspaceID] {
			return false, hostaction.ErrDenied
		}
		if !s.requestMatches(job) || s.authorize(ctx, job.CreatedBy, job.WorkspaceID) != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			_, err = s.options.Store.RejectQueuedActionObserved(ctx, job.ID, job.Action.InvocationID, s.observer())
			if s.queuedCancellationWon(ctx, job, err) {
				return true, nil
			}
			return err == nil, err
		}
		ready, drainErr := s.prepareIngressChange(ctx, job)
		if !ready {
			// Do not occupy the shared executor or block the queue while old
			// controllers may need it to finish their independent cleanup.
			continue
		}
		if drainErr != nil {
			_, err = s.options.Store.RejectQueuedActionObserved(ctx, job.ID, job.Action.InvocationID, s.observer())
			return err == nil, err
		}
		_, err = s.options.Store.StartActionObserved(ctx, job.ID, s.options.Lease, s.observer())
		if errors.Is(err, consolejobs.ErrWorkspaceBusy) || errors.Is(err, consolejobs.ErrCompensationRequired) || errors.Is(err, consolejobs.ErrCapacity) {
			continue
		}
		if err != nil {
			if s.queuedCancellationWon(ctx, job, err) {
				return true, nil
			}
			if errors.Is(err, hostaction.ErrDenied) && s.shutdownRequested() {
				current, readErr := s.options.Store.Get(ctx, job.ID)
				if readErr == nil && current.Action != nil && current.Action.InvocationID == job.Action.InvocationID && current.Status == consolejobs.StatusQueued && current.StartedAt == nil {
					return false, nil
				}
			}
			return false, err
		}
		finished, runErr := s.runtime.ExecutePreflight(ctx, job.ID, s.observer())
		if finished.ID != job.ID || !moduleActionTerminal(finished.Status) {
			// Once Start committed, an unconfirmed execution cannot be requeued.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalWriteTimeout)
			defer cancel()
			_, commitErr := s.options.Store.CompleteActionObserved(cleanup, s.options.Lease, actionabi.Event{ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "error",
				Error: &actionabi.Failure{Outcome: actionabi.Unknown, Code: consolejobs.ActionContainmentCode, Message: "Host action execution could not be confirmed"}}, s.observer())
			return false, errors.Join(consolejobs.ErrActionContainment, commitErr)
		}
		if runErr != nil {
			return false, runErr
		}
		if err := s.retireIngressChanges([]consolejobs.Job{finished}); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// Cancellation may win jobs.lock after this worker read the queue. That is
// not an execution failure. Only a durable, never-started terminal for the same
// invocation can be skipped; a running or uncertain start is never retried.
func (s *HostActionService) queuedCancellationWon(ctx context.Context, expected consolejobs.Job, cause error) bool {
	if !errors.Is(cause, consolejobs.ErrConflict) {
		return false
	}
	current, err := s.options.Store.Get(ctx, expected.ID)
	return err == nil && current.Action != nil && current.Action.InvocationID == expected.Action.InvocationID && current.StartedAt == nil &&
		(current.Status == consolejobs.StatusCanceled || current.Status == consolejobs.StatusFailed)
}

func mustMarshalJSON(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return body
}
