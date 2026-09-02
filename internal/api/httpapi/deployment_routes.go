package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

const (
	defaultMaximumDeploymentRequestBytes int64 = 64 << 10
	maximumDeploymentRequestBytes        int64 = 1 << 20
	defaultConfirmationTTL                     = 5 * time.Minute
	maximumConfirmationTTL                     = 15 * time.Minute
	deploymentTerminalTimeout                  = 10 * time.Second
)

var errDeploymentAuditUnavailable = errors.New("deployment audit unavailable")

type DeploymentJobStore interface {
	LookupIdempotency(context.Context, string, consolejobs.IdempotencyInput) (consolejobs.Job, bool, error)
	CreateOrGetObserved(context.Context, consolejobs.CreateSpec, consolejobs.JobCommitObserver) (consolejobs.CreateResult, error)
	CreateOrGetConfirmedObserved(context.Context, consolejobs.CreateSpec, consolejobs.ConfirmationInput, consolejobs.JobCommitObserver) (consolejobs.CreateResult, error)
	StartObserved(context.Context, string, consolejobs.JobCommitObserver) (consolejobs.Job, error)
	AppendEvent(context.Context, string, consolejobs.EventInput) (consolejobs.Event, error)
	TransitionObserved(context.Context, string, consolejobs.Status, consolejobs.TransitionInput, consolejobs.JobCommitObserver) (consolejobs.Job, error)
}

type DeploymentStepUpAuthenticator interface {
	IssueLocalStepUp(context.Context, consoleauth.LocalStepUpRequest) (consoleauth.LocalStepUpCredential, error)
	AuthenticateLocalStepUp(context.Context, consoleauth.LocalStepUpAuthenticationRequest) (consoleauth.LocalStepUpBinding, error)
}

type ProxyDeploymentStepUpAuthenticator interface {
	IssueProxyStepUp(context.Context, consoleauth.ProxyStepUpRequest) (consoleauth.ProxyStepUpCredential, error)
	AuthenticateProxyStepUp(context.Context, consoleauth.ProxyStepUpAuthenticationRequest) (consoleauth.ProxyStepUpBinding, error)
}

type routeInventoryStepUp struct{}

func (routeInventoryStepUp) IssueLocalStepUp(context.Context, consoleauth.LocalStepUpRequest) (consoleauth.LocalStepUpCredential, error) {
	return consoleauth.LocalStepUpCredential{}, errors.New("route inventory only")
}

func (routeInventoryStepUp) AuthenticateLocalStepUp(context.Context, consoleauth.LocalStepUpAuthenticationRequest) (consoleauth.LocalStepUpBinding, error) {
	return consoleauth.LocalStepUpBinding{}, errors.New("route inventory only")
}

type DeploymentOptions struct {
	PlanFactory     application.DeploymentPlanServiceFactory
	ServiceFactory  application.DeploymentServiceFactory
	ModuleFactory   application.ModuleManagementServiceFactory
	Store           DeploymentJobStore
	Audit           deploymentaudit.Sink
	StepUp          DeploymentStepUpAuthenticator
	Notify          func(string)
	ConfirmationTTL time.Duration
	MaxRequestBytes int64
}

type deploymentHTTPState struct {
	planFactory     application.DeploymentPlanServiceFactory
	serviceFactory  application.DeploymentServiceFactory
	moduleFactory   application.ModuleManagementServiceFactory
	store           DeploymentJobStore
	audit           deploymentaudit.Sink
	stepUp          DeploymentStepUpAuthenticator
	notify          func(string)
	confirmationTTL time.Duration
	maxRequestBytes int64
	now             func() time.Time
}

func newDeploymentHTTPState(options DeploymentOptions) (*deploymentHTTPState, error) {
	if options.PlanFactory == nil {
		return nil, errors.New("deployment plan service factory is required")
	}
	if options.Store == nil {
		return nil, errors.New("deployment job store is required")
	}
	if options.Audit == nil {
		return nil, errors.New("deployment audit sink is required")
	}
	if options.Notify == nil {
		return nil, errors.New("deployment job notifier is required")
	}
	if options.ConfirmationTTL < 0 || options.ConfirmationTTL > maximumConfirmationTTL {
		return nil, errors.New("deployment confirmation TTL must be between 1ns and 15m")
	}
	if options.ConfirmationTTL == 0 {
		options.ConfirmationTTL = defaultConfirmationTTL
	}
	if options.MaxRequestBytes < 0 || options.MaxRequestBytes > maximumDeploymentRequestBytes {
		return nil, errors.New("deployment request limit must be between 1 and 1048576 bytes")
	}
	if options.MaxRequestBytes == 0 {
		options.MaxRequestBytes = defaultMaximumDeploymentRequestBytes
	}
	return &deploymentHTTPState{
		planFactory: options.PlanFactory, store: options.Store, audit: options.Audit, notify: options.Notify,
		serviceFactory:  options.ServiceFactory,
		moduleFactory:   options.ModuleFactory,
		stepUp:          options.StepUp,
		confirmationTTL: options.ConfirmationTTL, maxRequestBytes: options.MaxRequestBytes, now: time.Now,
	}, nil
}

type deploymentPlanDTO struct {
	ConfigValidator            string                                       `json:"config_validator"`
	Digest                     string                                       `json:"digest"`
	Modules                    []string                                     `json:"modules"`
	IAM                        application.PlanIAM                          `json:"iam"`
	ModulePlans                map[string]map[string]string                 `json:"module_plans"`
	CapabilityBindings         map[string]map[string]string                 `json:"capability_bindings"`
	DNSPlatforms               map[string]string                            `json:"dns_platforms"`
	DNSCredentialCompatibility []application.PlanDNSCredentialCompatibility `json:"dns_credential_compatibility"`
	DynamicDNS                 application.PlanDynamicDNS                   `json:"dynamic_dns"`
	ModuleLifecycles           []application.PlanModuleLifecycle            `json:"module_lifecycles"`
}

type deploymentConfirmationDTO struct {
	Token           string    `json:"token"`
	ExpiresAt       time.Time `json:"expires_at"`
	Action          string    `json:"action"`
	PlanJobID       string    `json:"plan_job_id"`
	ConfigValidator string    `json:"config_validator"`
	PlanDigest      string    `json:"plan_digest"`
}

type deploymentPlanResponse struct {
	APIVersion   string                    `json:"api_version"`
	WorkspaceID  string                    `json:"workspace_id"`
	Job          jobSummaryDTO             `json:"job"`
	Plan         deploymentPlanDTO         `json:"plan"`
	Confirmation deploymentConfirmationDTO `json:"confirmation"`
}

type deploymentPlanHTTPRequest struct {
	StepUpProof  string `json:"step_up_proof"`
	DeploymentID string `json:"deployment_id,omitempty"`
}

type deploymentApplyHTTPRequest struct {
	PlanJobID               string `json:"plan_job_id"`
	ConfirmationToken       string `json:"confirmation_token"`
	ExpectedConfigValidator string `json:"expected_config_validator"`
	ExpectedPlanDigest      string `json:"expected_plan_digest"`
	DeploymentID            string `json:"deployment_id,omitempty"`
	Build                   bool   `json:"build"`
	UpdateLock              bool   `json:"update_lock"`
	AllowRisky              *bool  `json:"allow_risky"`
	Snapshot                bool   `json:"snapshot"`
	NoSnapshot              bool   `json:"no_snapshot"`
	StepUpProof             string `json:"step_up_proof,omitempty"`
}

type deploymentApplyResponse struct {
	APIVersion string        `json:"api_version"`
	Job        jobSummaryDTO `json:"job"`
	Existing   bool          `json:"existing"`
}

func (h *handler) planDeployment(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	state, ok := ConsoleStateFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "state_unavailable", "control-plane state is unavailable")
		return
	}
	request, ok := h.decodeDeploymentPlanRequest(w, r, state)
	if !ok {
		return
	}
	defer func() { request.StepUpProof = "" }()
	principal, _, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID := params["ws"]
	workspacePath, ok := h.registry.Resolve(workspaceID)
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	if h.deploymentHTTP == nil || h.deploymentHTTP.planFactory == nil {
		writeProblem(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment planning is unavailable")
		return
	}
	service := h.deploymentHTTP.planFactory(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment planning is unavailable")
		return
	}
	operationID, err := newDeploymentOperationID("plan")
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "jobs_unavailable", "job creation is unavailable")
		return
	}
	canonicalPath := "/api/v1/workspaces/" + workspaceID + "/plans"
	created, err := h.deploymentHTTP.store.CreateOrGetObserved(r.Context(), consolejobs.CreateSpec{
		Kind: deploymentaudit.ActionPlan, WorkspaceID: workspaceID, Mutating: false,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: principal.ID, Method: http.MethodPost, CanonicalPath: canonicalPath,
			Key: operationID, RequestDigest: consolejobs.DigestRequest([]byte("{}")),
		},
	}, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageJobCreateAuthorized, IdentitySource: principal.Source, TransactionID: principal.TransactionID,
	}, principal)))
	if err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	job, err := h.deploymentHTTP.store.StartObserved(r.Context(), created.Job.ID, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageJobStartAuthorized, IdentitySource: principal.Source, TransactionID: principal.TransactionID,
	}, principal)))
	if err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	if _, err := h.deploymentHTTP.store.AppendEvent(r.Context(), job.ID, consolejobs.EventInput{Kind: "started", Data: map[string]any{"kind": deploymentaudit.ActionPlan}}); err != nil {
		writeDeploymentStoreError(w, err)
		return
	}

	plan, planErr := service.Plan(r.Context(), application.PlanRequest{})
	terminalContext, cancelTerminal := context.WithTimeout(context.WithoutCancel(r.Context()), deploymentTerminalTimeout)
	defer cancelTerminal()
	if planErr != nil {
		jobError := deploymentJobError(planErr, "plan_failed")
		if err := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, jobError); err != nil {
			writeDeploymentStoreError(w, err)
			return
		}
		writeApplicationError(w, planErr)
		return
	}
	if !validDeploymentPlanBinding(plan.ConfigValidator, plan.Digest) {
		if err := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, &consolejobs.JobError{Code: "plan_binding_invalid", Message: "deployment plan failed"}); err != nil {
			writeDeploymentStoreError(w, err)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "plan_binding_invalid", "deployment plan is unavailable")
		return
	}
	var stepUpBinding *consoleauth.LocalStepUpBinding
	if state == StateFull {
		if h.deploymentHTTP.stepUp == nil {
			if err := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, &consolejobs.JobError{Code: "step_up_source_invalid", Message: "deployment plan failed"}); err != nil {
				writeDeploymentStoreError(w, err)
				return
			}
			writeProblem(w, http.StatusForbidden, "step_up_source_invalid", "this authentication source cannot use step-up")
			return
		}
		cookieName := localSessionCookie
		if principal.Source == "oidc_proxy" {
			cookieName = proxySessionCookie
		}
		sessionToken, cookieOK := uniqueCookieValue(r, cookieName)
		origin, originErr := canonicalRequestOrigin(r)
		if !cookieOK || originErr != nil {
			if err := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, &consolejobs.JobError{Code: "step_up_invalid", Message: "deployment plan failed"}); err != nil {
				writeDeploymentStoreError(w, err)
				return
			}
			writeProblem(w, http.StatusConflict, "step_up_invalid", "step-up proof is invalid, expired, or no longer matches the plan")
			return
		}
		stateDigest := deploymentStepUpStateDigest(workspaceID, request.DeploymentID, plan.ConfigValidator, plan.Digest)
		var binding consoleauth.LocalStepUpBinding
		var authErr error
		switch principal.Source {
		case "local":
			if principal.ID != consolejobs.PrincipalLocalOwner {
				authErr = consoleauth.ErrStepUpUnauthorized
				break
			}
			binding, authErr = h.deploymentHTTP.stepUp.AuthenticateLocalStepUp(r.Context(), consoleauth.LocalStepUpAuthenticationRequest{
				SessionToken: sessionToken, Origin: origin, Token: request.StepUpProof, Action: deploymentaudit.ActionApply,
				WorkspaceID: workspaceID, TargetID: request.DeploymentID, StateDigest: stateDigest,
			})
		case "oidc_proxy":
			proxyStepUp, available := h.deploymentHTTP.stepUp.(ProxyDeploymentStepUpAuthenticator)
			if !available {
				authErr = errors.New("proxy step-up authentication is unavailable")
				break
			}
			binding, authErr = proxyStepUp.AuthenticateProxyStepUp(r.Context(), consoleauth.ProxyStepUpAuthenticationRequest{
				SessionToken: sessionToken, Origin: origin, Identity: proxyIdentityFromPrincipal(principal), Token: request.StepUpProof,
				Action: deploymentaudit.ActionApply, WorkspaceID: workspaceID, TargetID: request.DeploymentID, StateDigest: stateDigest,
			})
		default:
			authErr = consoleauth.ErrStepUpUnauthorized
		}
		if authErr != nil {
			if err := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, &consolejobs.JobError{Code: "step_up_invalid", Message: "deployment plan failed"}); err != nil {
				writeDeploymentStoreError(w, err)
				return
			}
			if errors.Is(authErr, consoleauth.ErrStepUpUnauthorized) {
				writeProblem(w, http.StatusConflict, "step_up_invalid", "step-up proof is invalid, expired, or no longer matches the plan")
			} else {
				writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "step-up authentication is unavailable")
			}
			return
		}
		stepUpBinding = &binding
	}
	token, err := newDeploymentConfirmationToken()
	if err != nil {
		if finishErr := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, &consolejobs.JobError{Code: "confirmation_unavailable", Message: "deployment plan failed"}); finishErr != nil {
			writeDeploymentStoreError(w, finishErr)
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "confirmation_unavailable", "deployment confirmation is unavailable")
		return
	}
	expiresAt := h.deploymentHTTP.now().UTC().Add(h.deploymentHTTP.confirmationTTL)
	if stepUpBinding != nil && stepUpBinding.ExpiresAt.Before(expiresAt) {
		expiresAt = stepUpBinding.ExpiresAt
	}
	publicPlan := newDeploymentPlanDTO(plan)
	planPayload, err := deploymentJSONMap(publicPlan)
	if err != nil {
		if finishErr := h.deploymentHTTP.failPlanJob(terminalContext, job, principal, &consolejobs.JobError{Code: "response_encoding_failed", Message: "deployment plan failed"}); finishErr != nil {
			writeDeploymentStoreError(w, finishErr)
			return
		}
		writeProblem(w, http.StatusInternalServerError, "response_encoding_failed", "deployment plan could not be encoded")
		return
	}
	confirmationBinding := map[string]any{
		"proof_digest": consolejobs.DigestRequest([]byte(token)), "actor": principal.ID, "identity_source": principal.Source,
		"transaction_id": principal.TransactionID, "action": deploymentaudit.ActionApply, "workspace_id": workspaceID,
		"config_validator": plan.ConfigValidator, "plan_digest": plan.Digest, "expires_at": expiresAt.Format(time.RFC3339Nano),
	}
	if stepUpBinding != nil {
		confirmationBinding["step_up_digest"] = stepUpBinding.Digest
		confirmationBinding["step_up_principal_digest"] = stepUpBinding.SessionDigest
		confirmationBinding["step_up_action"] = stepUpBinding.Action
		confirmationBinding["step_up_workspace_id"] = stepUpBinding.WorkspaceID
		confirmationBinding["step_up_target_id"] = stepUpBinding.TargetID
		confirmationBinding["step_up_state_digest"] = stepUpBinding.StateDigest
		confirmationBinding["step_up_expires_at"] = stepUpBinding.ExpiresAt.Format(time.RFC3339Nano)
	}
	if _, err := h.deploymentHTTP.store.AppendEvent(terminalContext, job.ID, consolejobs.EventInput{
		Kind: "succeeded", Data: map[string]any{"config_validator": plan.ConfigValidator, "plan_digest": plan.Digest},
	}); err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	progress := 100
	completed, err := h.deploymentHTTP.store.TransitionObserved(terminalContext, job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{
		Progress: &progress, Result: map[string]any{"plan": planPayload, "confirmation": confirmationBinding},
	}, h.deploymentHTTP.auditObserver(
		withDeploymentPrincipal(deploymentaudit.Event{
			Stage: deploymentaudit.StageConfirmationIssueAuthorized, IdentitySource: principal.Source,
			TransactionID: principal.TransactionID, PlanJobID: job.ID,
			ConfigValidator: plan.ConfigValidator, PlanDigest: plan.Digest,
		}, principal),
		withDeploymentPrincipal(deploymentaudit.Event{
			Stage: deploymentaudit.StageJobSucceededAuthorized, IdentitySource: principal.Source,
			TransactionID: principal.TransactionID, ConfigValidator: plan.ConfigValidator, PlanDigest: plan.Digest,
		}, principal),
	))
	if err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deploymentPlanResponse{
		APIVersion: APIVersion, WorkspaceID: workspaceID, Job: newJobSummaryDTO(completed), Plan: publicPlan,
		Confirmation: deploymentConfirmationDTO{
			Token: token, ExpiresAt: expiresAt, Action: deploymentaudit.ActionApply, PlanJobID: job.ID,
			ConfigValidator: plan.ConfigValidator, PlanDigest: plan.Digest,
		},
	})
}

func (state *deploymentHTTPState) failPlanJob(ctx context.Context, job consolejobs.Job, principal Principal, jobError *consolejobs.JobError) error {
	if _, err := state.store.AppendEvent(ctx, job.ID, consolejobs.EventInput{
		Kind: "failed", Data: map[string]any{"error": map[string]any{"code": jobError.Code, "message": jobError.Message}},
	}); err != nil {
		return err
	}
	_, err := state.store.TransitionObserved(ctx, job.ID, consolejobs.StatusFailed, consolejobs.TransitionInput{Error: jobError}, state.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageJobFailedAuthorized, IdentitySource: principal.Source,
		TransactionID: principal.TransactionID, FailureCode: jobError.Code,
	}, principal)))
	return err
}

func (h *handler) applyDeployment(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	principal, _, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID := params["ws"]
	if _, registered := h.registry.Resolve(workspaceID); !registered {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	state, stateOK := ConsoleStateFromContext(r.Context())
	if !stateOK {
		writeProblem(w, http.StatusInternalServerError, "state_unavailable", "control-plane state is unavailable")
		return
	}
	request, ok := h.decodeDeploymentApplyRequest(w, r, state)
	if !ok {
		return
	}
	defer func() { request.ConfirmationToken, request.StepUpProof = "", "" }()
	idempotencyKey, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	canonicalBody, err := json.Marshal(request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	defer clear(canonicalBody)
	applyRequest := application.ApplyRequest{
		ExpectedConfigValidator: request.ExpectedConfigValidator, ExpectedPlanDigest: request.ExpectedPlanDigest,
		DeploymentID: request.DeploymentID, Build: request.Build, UpdateLock: request.UpdateLock,
		AllowRisky: *request.AllowRisky, Snapshot: request.Snapshot, NoSnapshot: request.NoSnapshot,
	}
	requestPayload, err := deploymentJSONMap(applyRequest)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	canonicalPath := "/api/v1/workspaces/" + workspaceID + "/actions/apply"
	confirmation := consolejobs.ConfirmationInput{
		PlanJobID: request.PlanJobID, PlanKind: deploymentaudit.ActionPlan, Token: request.ConfirmationToken,
		Actor: principal.ID, IdentitySource: principal.Source, TransactionID: principal.TransactionID,
		Action: deploymentaudit.ActionApply, WorkspaceID: workspaceID,
		ConfigValidator: request.ExpectedConfigValidator, PlanDigest: request.ExpectedPlanDigest,
	}
	if state == StateFull {
		if principal.Source != "local" && principal.Source != "oidc_proxy" || principal.Source == "local" && principal.ID != consolejobs.PrincipalLocalOwner {
			writeProblem(w, http.StatusForbidden, "step_up_source_invalid", "this authentication source cannot use step-up")
			return
		}
		cookieName := localSessionCookie
		if principal.Source == "oidc_proxy" {
			cookieName = proxySessionCookie
		}
		sessionToken, cookieOK := uniqueCookieValue(r, cookieName)
		if !cookieOK {
			writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
			return
		}
		confirmation.StepUp = &consolejobs.StepUpInput{
			Token: request.StepUpProof, SessionDigest: consolejobs.DigestRequest([]byte(sessionToken)), TargetID: request.DeploymentID,
			StateDigest: deploymentStepUpStateDigest(workspaceID, request.DeploymentID, request.ExpectedConfigValidator, request.ExpectedPlanDigest),
		}
		defer func() { confirmation.StepUp.Token = "" }()
	}
	created, err := h.deploymentHTTP.store.CreateOrGetConfirmedObserved(r.Context(), consolejobs.CreateSpec{
		Kind: deploymentaudit.ActionApply, WorkspaceID: workspaceID, Mutating: true, Request: requestPayload,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: principal.ID, Method: http.MethodPost, CanonicalPath: canonicalPath,
			Key: idempotencyKey, RequestDigest: consolejobs.DigestRequest(canonicalBody),
		},
	}, confirmation, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageConfirmationConsumeAndCreateAuthorized, IdentitySource: principal.Source,
		TransactionID: principal.TransactionID, PlanJobID: request.PlanJobID,
		ConfigValidator: request.ExpectedConfigValidator, PlanDigest: request.ExpectedPlanDigest,
	}, principal)))
	if err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+created.Job.ID)
	if !created.Existing {
		h.deploymentHTTP.notify(workspaceID)
	}
	writeJSON(w, http.StatusAccepted, deploymentApplyResponse{
		APIVersion: APIVersion, Job: newJobSummaryDTO(created.Job), Existing: created.Existing,
	})
}

func (state *deploymentHTTPState) auditObserver(events ...deploymentaudit.Event) consolejobs.JobCommitObserver {
	return consolejobs.JobCommitObserverFunc(func(ctx context.Context, intent consolejobs.JobCommitIntent) error {
		for _, event := range events {
			observer := deploymentaudit.ObserveJobCommit(state.audit, event)
			if err := observer.BeforeJobCommit(ctx, intent); err != nil {
				return errors.Join(errDeploymentAuditUnavailable, err)
			}
		}
		return nil
	})
}

func withDeploymentPrincipal(event deploymentaudit.Event, principal Principal) deploymentaudit.Event {
	event.IdentitySource = principal.Source
	event.IdentityIssuer = principal.Issuer
	event.IdentitySubject = principal.Subject
	event.SemanticRole = principal.SemanticRole
	event.DirectoryGroup = principal.DirectoryGroup
	event.TransactionID = principal.TransactionID
	return event
}

func (h *handler) decodeEmptyDeploymentRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return true
	}
	if validateUniqueJSONKeys(body) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be an empty JSON object")
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil || object == nil || len(object) != 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be an empty JSON object")
		return false
	}
	return true
}

func (h *handler) decodeDeploymentPlanRequest(w http.ResponseWriter, r *http.Request, state ConsoleState) (deploymentPlanHTTPRequest, bool) {
	if state != StateFull {
		return deploymentPlanHTTPRequest{}, h.decodeEmptyDeploymentRequest(w, r)
	}
	var request deploymentPlanHTTPRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return deploymentPlanHTTPRequest{}, false
	}
	if request.StepUpProof == "" {
		writeProblem(w, http.StatusPreconditionRequired, "step_up_required", "deployment plan requires a step-up proof")
		return deploymentPlanHTTPRequest{}, false
	}
	if !validDeploymentStepUpProof(request.StepUpProof) {
		writeProblem(w, http.StatusBadRequest, "step_up_invalid", "step-up proof format is invalid")
		return deploymentPlanHTTPRequest{}, false
	}
	if request.DeploymentID != "" && (utf8.RuneCountInString(request.DeploymentID) > 255 || deployment.ValidateID(request.DeploymentID) != nil) {
		writeProblem(w, http.StatusBadRequest, "invalid_deployment_id", "deployment ID is invalid")
		return deploymentPlanHTTPRequest{}, false
	}
	return request, true
}

func (h *handler) decodeDeploymentApplyRequest(w http.ResponseWriter, r *http.Request, state ConsoleState) (deploymentApplyHTTPRequest, bool) {
	var request deploymentApplyHTTPRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return deploymentApplyHTTPRequest{}, false
	}
	if state == StateFull && request.StepUpProof == "" {
		writeProblem(w, http.StatusPreconditionRequired, "step_up_required", "deployment apply requires a step-up proof")
		return deploymentApplyHTTPRequest{}, false
	}
	if state == StateFull && !validDeploymentStepUpProof(request.StepUpProof) || state != StateFull && request.StepUpProof != "" {
		writeProblem(w, http.StatusBadRequest, "step_up_invalid", "step-up proof format is invalid for this request")
		return deploymentApplyHTTPRequest{}, false
	}
	if request.PlanJobID == "" || request.ConfirmationToken == "" ||
		request.ExpectedConfigValidator == "" || request.ExpectedPlanDigest == "" {
		writeProblem(w, http.StatusPreconditionRequired, "confirmation_required", "apply requires a complete plan confirmation")
		return deploymentApplyHTTPRequest{}, false
	}
	if len(request.PlanJobID) > maximumJobIDLength || !validDeploymentConfirmationToken(request.ConfirmationToken) ||
		!validDeploymentPlanBinding(request.ExpectedConfigValidator, request.ExpectedPlanDigest) {
		writeProblem(w, http.StatusBadRequest, "confirmation_invalid", "deployment confirmation binding is invalid")
		return deploymentApplyHTTPRequest{}, false
	}
	if request.AllowRisky == nil {
		writeProblem(w, http.StatusBadRequest, "apply_request_invalid", "apply request omits a required plan binding")
		return deploymentApplyHTTPRequest{}, false
	}
	if request.DeploymentID != "" && (utf8.RuneCountInString(request.DeploymentID) > 255 || deployment.ValidateID(request.DeploymentID) != nil) {
		writeProblem(w, http.StatusBadRequest, "invalid_deployment_id", "deployment ID is invalid")
		return deploymentApplyHTTPRequest{}, false
	}
	if request.Snapshot && request.NoSnapshot {
		writeProblem(w, http.StatusBadRequest, "invalid_snapshot_policy", "snapshot and no_snapshot cannot both be true")
		return deploymentApplyHTTPRequest{}, false
	}
	return request, true
}

func (h *handler) decodeDeploymentJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, h.deploymentHTTP.maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		}
		return false
	}
	defer clear(body)
	if validateUniqueJSONKeys(body) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func deploymentIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 256 {
		writeProblem(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key must contain one opaque value")
		return "", false
	}
	for _, char := range values[0] {
		if char < 0x21 || char > 0x7e {
			writeProblem(w, http.StatusBadRequest, "idempotency_key_invalid", "Idempotency-Key contains invalid characters")
			return "", false
		}
	}
	return values[0], true
}

func writeDeploymentStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDeploymentAuditUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "deployment audit is unavailable")
	case errors.Is(err, consolejobs.ErrIdempotencyConflict):
		writeProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key belongs to a different request")
	case errors.Is(err, consolejobs.ErrConfirmationConsumed):
		writeProblem(w, http.StatusConflict, "confirmation_consumed", "deployment confirmation was already consumed")
	case errors.Is(err, consolejobs.ErrConfirmationInvalid):
		writeProblem(w, http.StatusConflict, "confirmation_invalid", "deployment confirmation is invalid, expired, or no longer matches the plan")
	case errors.Is(err, consolejobs.ErrStepUpConsumed):
		writeProblem(w, http.StatusConflict, "step_up_consumed", "step-up proof was already consumed")
	case errors.Is(err, consolejobs.ErrStepUpInvalid):
		writeProblem(w, http.StatusConflict, "step_up_invalid", "step-up proof is invalid, expired, or no longer matches the plan")
	case errors.Is(err, consolejobs.ErrCapacity):
		w.Header().Set("Retry-After", "1")
		writeProblem(w, http.StatusTooManyRequests, "job_capacity_reached", "job queue capacity was reached")
	case errors.Is(err, consolejobs.ErrWorkspaceBusy), errors.Is(err, consolejobs.ErrCompensationRequired), errors.Is(err, consolejobs.ErrConflict):
		writeProblem(w, http.StatusConflict, "job_conflict", "job state does not permit this operation")
	default:
		writeJobStoreError(w, err)
	}
}

func deploymentJobError(err error, fallback string) *consolejobs.JobError {
	if applicationError, ok := application.ErrorOf(err); ok && applicationError.Code != "" {
		return &consolejobs.JobError{Code: applicationError.Code, Message: "deployment plan failed"}
	}
	return &consolejobs.JobError{Code: fallback, Message: "deployment plan failed"}
}

func newDeploymentOperationID(prefix string) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func newDeploymentConfirmationToken() (string, error) {
	var value [32]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return "cnf_" + hex.EncodeToString(value[:]), nil
}

func validDeploymentPlanBinding(validator, digest string) bool {
	if _, ok := strongConfigETag(validator); !ok || len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func validDeploymentConfirmationToken(token string) bool {
	if len(token) != len("cnf_")+64 || !strings.HasPrefix(token, "cnf_") {
		return false
	}
	proof := token[len("cnf_"):]
	_, err := hex.DecodeString(proof)
	return err == nil && proof == strings.ToLower(proof)
}

func validDeploymentStepUpProof(value string) bool {
	if !strings.HasPrefix(value, "sup_") {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "sup_"))
	return err == nil && len(raw) == 32
}

func deploymentStepUpStateDigest(workspaceID, targetID, validator, planDigest string) string {
	body, _ := json.Marshal([]string{deploymentaudit.ActionApply, workspaceID, targetID, validator, planDigest})
	return consolejobs.DigestRequest(body)
}

func newDeploymentPlanDTO(plan application.PlanResult) deploymentPlanDTO {
	return deploymentPlanDTO{
		ConfigValidator: plan.ConfigValidator, Digest: plan.Digest,
		Modules: nonNilStrings(plan.Modules), IAM: application.PlanIAM{
			Provider: plan.IAM.Provider, Consumers: nonNilPlanIAMConsumers(plan.IAM.Consumers),
		},
		ModulePlans: nonNilNestedStringMap(plan.ModulePlans), CapabilityBindings: nonNilNestedStringMap(plan.CapabilityBindings),
		DNSPlatforms: nonNilStringMap(plan.DNSPlatforms), DNSCredentialCompatibility: nonNilDNSCompatibility(plan.DNSCredentialCompatibility),
		DynamicDNS: application.PlanDynamicDNS{
			Provider: plan.DynamicDNS.Provider, SelfManaged: nonNilStrings(plan.DynamicDNS.SelfManaged), Automatic: plan.DynamicDNS.Automatic,
		},
		ModuleLifecycles: nonNilModuleLifecycles(plan.ModuleLifecycles),
	}
}

func deploymentJSONMap(value any) (map[string]any, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func nonNilStringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func nonNilNestedStringMap(values map[string]map[string]string) map[string]map[string]string {
	if values == nil {
		return map[string]map[string]string{}
	}
	result := make(map[string]map[string]string, len(values))
	for key, value := range values {
		result[key] = nonNilStringMap(value)
	}
	return result
}

func nonNilPlanIAMConsumers(values []application.PlanIAMConsumer) []application.PlanIAMConsumer {
	if values == nil {
		return []application.PlanIAMConsumer{}
	}
	return append([]application.PlanIAMConsumer(nil), values...)
}

func nonNilDNSCompatibility(values []application.PlanDNSCredentialCompatibility) []application.PlanDNSCredentialCompatibility {
	if values == nil {
		return []application.PlanDNSCredentialCompatibility{}
	}
	return append([]application.PlanDNSCredentialCompatibility(nil), values...)
}

func nonNilModuleLifecycles(values []application.PlanModuleLifecycle) []application.PlanModuleLifecycle {
	if values == nil {
		return []application.PlanModuleLifecycle{}
	}
	return append([]application.PlanModuleLifecycle(nil), values...)
}
