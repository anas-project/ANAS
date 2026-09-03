package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type snapshotListResponse struct {
	APIVersion  string                       `json:"api_version"`
	WorkspaceID string                       `json:"workspace_id"`
	KeepAuto    int                          `json:"keep_auto"`
	Snapshots   []application.SnapshotRecord `json:"snapshots"`
	NextCursor  *string                      `json:"next_cursor"`
}

type backupPlanResponse struct {
	APIVersion   string                         `json:"api_version"`
	WorkspaceID  string                         `json:"workspace_id"`
	TargetID     string                         `json:"target_id"`
	PlanID       string                         `json:"plan_id"`
	Capabilities application.BackupCapabilities `json:"capabilities"`
	Plan         application.BackupPlan         `json:"plan"`
}

type backupListResponse struct {
	APIVersion  string                     `json:"api_version"`
	WorkspaceID string                     `json:"workspace_id"`
	TargetID    string                     `json:"target_id"`
	Backups     []application.BackupRecord `json:"backups"`
	NextCursor  *string                    `json:"next_cursor"`
}

type localAdminListResponse struct {
	APIVersion  string                         `json:"api_version"`
	WorkspaceID string                         `json:"workspace_id"`
	Accounts    []application.LocalAdminRecord `json:"accounts"`
	NextCursor  *string                        `json:"next_cursor"`
}

type localAdminRevealRequest struct {
	StepUpProof string `json:"step_up_proof"`
}

type localAdminRevealResponse struct {
	APIVersion  string `json:"api_version"`
	WorkspaceID string `json:"workspace_id"`
	Module      string `json:"module"`
	Account     string `json:"account"`
	Purpose     string `json:"purpose"`
	Username    string `json:"username"`
	URL         string `json:"url"`
	Password    string `json:"password"`
}

type terminalActionPreviewResponse struct {
	APIVersion  string                           `json:"api_version"`
	WorkspaceID string                           `json:"workspace_id"`
	Operation   string                           `json:"operation"`
	Target      application.TerminalActionTarget `json:"target"`
	Impact      application.TerminalActionImpact `json:"impact"`
	Argv        []string                         `json:"argv"`
	Display     string                           `json:"display"`
	CLIContract string                           `json:"cli_contract"`
}

type localMaintenanceStepUpConsumer interface {
	ConsumeLocalStepUp(context.Context, consoleauth.LocalStepUpAuthenticationRequest) (consoleauth.LocalStepUpBinding, error)
}

type proxyMaintenanceStepUpConsumer interface {
	ConsumeProxyStepUp(context.Context, consoleauth.ProxyStepUpAuthenticationRequest) (consoleauth.ProxyStepUpBinding, error)
}

func (h *handler) listSnapshots(w http.ResponseWriter, r *http.Request, params map[string]string) {
	pagination, ok := parseListPagination(w, r)
	if !ok {
		return
	}
	service, ok := h.workspaceMaintenanceService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	result, err := service.ListSnapshots(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if result.Snapshots == nil {
		result.Snapshots = []application.SnapshotRecord{}
	}
	snapshots, next, err := paginateList(result.Snapshots, pagination, "snapshots\x00"+params["ws"], func(snapshot application.SnapshotRecord) string { return snapshot.ID })
	if err != nil {
		writePaginatedListError(w)
		return
	}
	writeJSON(w, http.StatusOK, snapshotListResponse{APIVersion: APIVersion, WorkspaceID: params["ws"], KeepAuto: result.KeepAuto, Snapshots: snapshots, NextCursor: next})
}

func (h *handler) createSnapshot(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	var request application.SnapshotCreateRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	h.enqueueMaintenanceJob(w, r, params["ws"], "/api/v1/workspaces/"+params["ws"]+"/snapshots", deploymentaudit.ActionSnapshotCreate, request, true, "")
}

func (h *handler) snapshotAction(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if _, ok := h.registry.Resolve(params["ws"]); !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	canonicalPath := "/api/v1/workspaces/" + params["ws"] + "/snapshots/" + params["id"] + "/actions/" + params["action"]
	switch params["action"] {
	case "pin", "unpin":
		var body struct {
			Label string `json:"label,omitempty"`
		}
		if !h.decodeDeploymentJSON(w, r, &body) {
			return
		}
		pinned := params["action"] == "pin"
		kind := deploymentaudit.ActionSnapshotUnpin
		if pinned {
			kind = deploymentaudit.ActionSnapshotPin
		}
		h.enqueueMaintenanceJob(w, r, params["ws"], canonicalPath, kind, application.SnapshotPinRequest{SnapshotID: params["id"], Pinned: pinned, Label: body.Label}, true, params["id"])
	case "verify":
		if !h.decodeEmptyDeploymentRequest(w, r) {
			return
		}
		h.enqueueMaintenanceJob(w, r, params["ws"], canonicalPath, deploymentaudit.ActionSnapshotVerify, application.SnapshotVerifyRequest{SnapshotID: params["id"]}, false, params["id"])
	default:
		writeProblem(w, http.StatusNotFound, "snapshot_action_not_found", "snapshot action was not found")
	}
}

func (h *handler) planBackup(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	var request application.BackupPlanRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	service, ok := h.workspaceMaintenanceService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	result, err := service.PlanBackup(r.Context(), request)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backupPlanResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], TargetID: result.TargetID, PlanID: result.PlanID,
		Capabilities: result.Capabilities, Plan: result.Plan,
	})
}

func (h *handler) listBackups(w http.ResponseWriter, r *http.Request, params map[string]string) {
	pagination, ok := parseListPagination(w, r, "target_id")
	if !ok {
		return
	}
	query := r.URL.Query()
	targetIDs := query["target_id"]
	if len(targetIDs) != 1 || targetIDs[0] == "" {
		writeProblem(w, http.StatusBadRequest, "backup_target_invalid", "target_id must contain one registered backup target ID")
		return
	}
	service, ok := h.workspaceMaintenanceService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	result, err := service.ListBackups(r.Context(), application.BackupListRequest{TargetID: targetIDs[0]})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if result.Backups == nil {
		result.Backups = []application.BackupRecord{}
	}
	backups, next, err := paginateList(result.Backups, pagination, "backups\x00"+params["ws"]+"\x00"+result.TargetID, func(backup application.BackupRecord) string { return backup.ID })
	if err != nil {
		writePaginatedListError(w)
		return
	}
	writeJSON(w, http.StatusOK, backupListResponse{APIVersion: APIVersion, WorkspaceID: params["ws"], TargetID: result.TargetID, Backups: backups, NextCursor: next})
}

func (h *handler) listLocalAdmins(w http.ResponseWriter, r *http.Request, params map[string]string) {
	pagination, ok := parseListPagination(w, r)
	if !ok {
		return
	}
	service, ok := h.workspaceMaintenanceService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	result, err := service.ListLocalAdmins(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if result.Accounts == nil {
		result.Accounts = []application.LocalAdminRecord{}
	}
	accounts, next, err := paginateList(result.Accounts, pagination, "local-admins\x00"+params["ws"], func(account application.LocalAdminRecord) string { return account.TargetID })
	if err != nil {
		writePaginatedListError(w)
		return
	}
	writeJSON(w, http.StatusOK, localAdminListResponse{APIVersion: APIVersion, WorkspaceID: params["ws"], Accounts: accounts, NextCursor: next})
}

func (h *handler) rotateLocalAdmin(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if !h.decodeEmptyDeploymentRequest(w, r) {
		return
	}
	target := application.LocalAdminTarget{Module: params["module"], Account: params["account"]}
	path := "/api/v1/workspaces/" + params["ws"] + "/local-admins/" + params["module"] + "/" + params["account"] + "/actions/rotate"
	h.enqueueMaintenanceJob(w, r, params["ws"], path, deploymentaudit.ActionLocalAdminRotate, target, true, params["module"]+"."+params["account"])
}

func (h *handler) revealLocalAdmin(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	var request localAdminRevealRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	if request.StepUpProof == "" {
		writeProblem(w, http.StatusPreconditionRequired, "step_up_required", "credential reveal requires a step-up proof")
		return
	}
	if !validDeploymentStepUpProof(request.StepUpProof) {
		writeProblem(w, http.StatusBadRequest, "step_up_invalid", "step-up proof format is invalid")
		return
	}
	service, ok := h.workspaceMaintenanceService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	targetID := application.LocalAdminTargetID(params["module"], params["account"])
	if !h.consumeMaintenanceStepUp(w, r, service, params["ws"], targetID, request.StepUpProof) {
		request.StepUpProof = ""
		return
	}
	request.StepUpProof = ""
	credential, err := service.RevealLocalAdmin(r.Context(), application.LocalAdminTarget{Module: params["module"], Account: params["account"]})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	defer func() { credential.Password = "" }()
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || h.deploymentHTTP.audit.RecordDeploymentEvent(r.Context(), withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageCredentialRevealAuthorized, Action: deploymentaudit.ActionLocalAdminReveal,
		Actor: principal.ID, WorkspaceID: params["ws"], TargetID: targetID,
	}, principal)) != nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "credential reveal audit is unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, localAdminRevealResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], Module: credential.Module, Account: credential.Account,
		Purpose: credential.Purpose, Username: credential.Username, URL: credential.URL, Password: credential.Password,
	})
}

func (h *handler) previewTerminalAction(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	var request application.TerminalActionRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	service, ok := h.workspaceMaintenanceService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	descriptor, err := service.PreviewTerminalAction(r.Context(), request)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "principal_unavailable", "request principal is unavailable")
		return
	}
	targetID := descriptor.Target.SnapshotID
	if targetID == "" {
		targetID = strings.Join([]string{descriptor.Target.BackupTargetID, descriptor.Target.BackupPlanID, descriptor.Target.BackupID}, ":")
	}
	if err := h.deploymentHTTP.audit.RecordDeploymentEvent(r.Context(), withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageTerminalDescriptorReadyAuthorized, Action: descriptor.Operation,
		Actor: principal.ID, WorkspaceID: params["ws"], TargetID: targetID,
	}, principal)); err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "terminal descriptor audit is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, terminalActionPreviewResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], Operation: descriptor.Operation, Target: descriptor.Target,
		Impact: descriptor.Impact, Argv: descriptor.Argv, Display: descriptor.Display, CLIContract: descriptor.CLIContract,
	})
}

func (h *handler) enqueueMaintenanceJob(w http.ResponseWriter, r *http.Request, workspaceID, canonicalPath, kind string, request any, mutating bool, targetID string) {
	principal, _, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	if _, ok := h.registry.Resolve(workspaceID); !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	idempotencyKey, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	canonicalBody, err := json.Marshal(request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	payload, err := deploymentJSONMap(request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	idempotency := consolejobs.IdempotencyInput{
		Principal: principal.ID, Method: http.MethodPost, CanonicalPath: canonicalPath,
		Key: idempotencyKey, RequestDigest: consolejobs.DigestRequest(canonicalBody),
	}
	if existing, found, lookupErr := h.deploymentHTTP.store.LookupIdempotency(r.Context(), workspaceID, idempotency); lookupErr != nil {
		writeDeploymentStoreError(w, lookupErr)
		return
	} else if found {
		writeLifecycleJob(w, existing, true)
		return
	}
	created, err := h.deploymentHTTP.store.CreateOrGetObserved(r.Context(), consolejobs.CreateSpec{
		Kind: kind, WorkspaceID: workspaceID, Mutating: mutating, Request: payload, Idempotency: idempotency,
	}, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageJobCreateAuthorized, Actor: principal.ID, TargetID: targetID,
	}, principal)))
	if err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	if !created.Existing {
		h.deploymentHTTP.notify(workspaceID)
	}
	writeLifecycleJob(w, created.Job, created.Existing)
}

func (h *handler) workspaceMaintenanceService(w http.ResponseWriter, workspaceID string, events application.EventSink) (application.MaintenanceService, bool) {
	workspacePath, ok := h.registry.Resolve(workspaceID)
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return nil, false
	}
	if h.deploymentHTTP == nil || h.deploymentHTTP.maintenanceFactory == nil {
		writeProblem(w, http.StatusServiceUnavailable, "maintenance_unavailable", "maintenance service is unavailable")
		return nil, false
	}
	service := h.deploymentHTTP.maintenanceFactory(workspacePath, events)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "maintenance_unavailable", "maintenance service is unavailable")
		return nil, false
	}
	return service, true
}

func (h *handler) consumeMaintenanceStepUp(w http.ResponseWriter, r *http.Request, service application.MaintenanceService, workspaceID, targetID, proof string) bool {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return false
	}
	origin, ok := requireSameRequestOrigin(w, r)
	if !ok {
		return false
	}
	stateDigest, err := service.StepUpStateDigest(r.Context(), deploymentaudit.ActionLocalAdminReveal, targetID)
	if err != nil {
		writeApplicationError(w, err)
		return false
	}
	cookieName := localSessionCookie
	if principal.Source == "oidc_proxy" {
		cookieName = proxySessionCookie
	}
	sessionToken, ok := uniqueCookieValue(r, cookieName)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return false
	}
	switch principal.Source {
	case "local":
		consumer, ok := h.deploymentHTTP.stepUp.(localMaintenanceStepUpConsumer)
		if !ok {
			writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "step-up consumption is unavailable")
			return false
		}
		_, err = consumer.ConsumeLocalStepUp(r.Context(), consoleauth.LocalStepUpAuthenticationRequest{
			SessionToken: sessionToken, Origin: origin, Token: proof, Action: deploymentaudit.ActionLocalAdminReveal,
			WorkspaceID: workspaceID, TargetID: targetID, StateDigest: stateDigest,
		})
	case "oidc_proxy":
		consumer, ok := h.deploymentHTTP.stepUp.(proxyMaintenanceStepUpConsumer)
		if !ok {
			writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "proxy step-up consumption is unavailable")
			return false
		}
		_, err = consumer.ConsumeProxyStepUp(r.Context(), consoleauth.ProxyStepUpAuthenticationRequest{
			SessionToken: sessionToken, Origin: origin, Token: proof, Identity: proxyIdentityFromPrincipal(principal),
			Action: deploymentaudit.ActionLocalAdminReveal, WorkspaceID: workspaceID, TargetID: targetID, StateDigest: stateDigest,
		})
	default:
		writeProblem(w, http.StatusForbidden, "step_up_source_invalid", "this authentication source cannot reveal credentials")
		return false
	}
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, consoleauth.ErrSessionUnauthorized), errors.Is(err, consoleauth.ErrCredentialExpired):
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
	case errors.Is(err, consoleauth.ErrOriginMismatch):
		writeProblem(w, http.StatusForbidden, "csrf_mismatch", "request origin validation failed")
	case errors.Is(err, consoleauth.ErrStepUpUnauthorized):
		writeProblem(w, http.StatusConflict, "step_up_invalid", "step-up proof is invalid, expired, used, or no longer matches current state")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "step-up authentication is unavailable")
	}
	return false
}
