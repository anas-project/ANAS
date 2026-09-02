package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type lifecycleHTTPRequest struct {
	Modules              []string  `json:"modules"`
	ExpectedDeploymentID string    `json:"expected_deployment_id,omitempty"`
	ExpectedDigest       string    `json:"expected_digest,omitempty"`
	ConfirmedModules     *[]string `json:"confirmed_modules,omitempty"`
}

type lifecyclePreviewResponse struct {
	APIVersion  string              `json:"api_version"`
	WorkspaceID string              `json:"workspace_id"`
	Preview     lifecyclePreviewDTO `json:"preview"`
}

type lifecyclePreviewDTO struct {
	DeploymentID     string                      `json:"deployment_id"`
	Action           application.LifecycleAction `json:"action"`
	RequestedModules []string                    `json:"requested_modules"`
	AffectedModules  []string                    `json:"affected_modules"`
	Digest           string                      `json:"digest"`
}

type lifecycleJobResponse struct {
	APIVersion string        `json:"api_version"`
	Job        jobSummaryDTO `json:"job"`
	Existing   bool          `json:"existing"`
}

type rollbackHTTPRequest struct {
	DeploymentID             string    `json:"deployment_id"`
	ExpectedActiveDeployment string    `json:"expected_active_deployment,omitempty"`
	ExpectedDigest           string    `json:"expected_digest,omitempty"`
	AllowRisky               *bool     `json:"allow_risky,omitempty"`
	ConfirmedDeploymentID    string    `json:"confirmed_deployment_id,omitempty"`
	ConfirmedGuardedChanges  *[]string `json:"confirmed_guarded_changes,omitempty"`
}

type rollbackPreviewResponse struct {
	APIVersion  string             `json:"api_version"`
	WorkspaceID string             `json:"workspace_id"`
	Preview     rollbackPreviewDTO `json:"preview"`
}

type rollbackPreviewDTO struct {
	ActiveDeployment string   `json:"active_deployment"`
	TargetDeployment string   `json:"target_deployment"`
	GuardedChanges   []string `json:"guarded_changes"`
	DataTouched      bool     `json:"data_touched"`
	Digest           string   `json:"digest"`
}

func (h *handler) moduleLifecycle(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	action := application.LifecycleAction(params["action"])
	kind, ok := lifecycleJobKind(action)
	if !ok {
		writeProblem(w, http.StatusNotFound, "lifecycle_action_not_found", "lifecycle action was not found")
		return
	}
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
	service := h.deploymentHTTP.serviceFactory(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment lifecycle is unavailable")
		return
	}
	var request lifecycleHTTPRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	if request.Modules == nil {
		writeProblem(w, http.StatusBadRequest, "lifecycle_request_invalid", "modules must be an explicit array; use an empty array for the whole deployment")
		return
	}
	bound := request.ExpectedDeploymentID != "" || request.ExpectedDigest != "" || request.ConfirmedModules != nil
	if !bound {
		preview, err := service.PreviewLifecycle(r.Context(), application.LifecyclePreviewRequest{Action: action, Modules: request.Modules})
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, lifecyclePreviewResponse{
			APIVersion: APIVersion, WorkspaceID: workspaceID, Preview: normalizeLifecyclePreview(preview),
		})
		return
	}
	if request.ExpectedDeploymentID == "" || !validSHA256Hex(request.ExpectedDigest) || request.ConfirmedModules == nil {
		writeProblem(w, http.StatusPreconditionRequired, "lifecycle_confirmation_required", "lifecycle execution requires a complete dependency-chain confirmation")
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
	canonicalPath := "/api/v1/workspaces/" + workspaceID + "/modules/actions/" + string(action)
	idempotency := consolejobs.IdempotencyInput{
		Principal: principal.ID, Method: http.MethodPost, CanonicalPath: canonicalPath,
		Key: idempotencyKey, RequestDigest: consolejobs.DigestRequest(canonicalBody),
	}
	if existing, found, err := h.deploymentHTTP.store.LookupIdempotency(r.Context(), workspaceID, idempotency); err != nil {
		writeDeploymentStoreError(w, err)
		return
	} else if found {
		writeLifecycleJob(w, existing, true)
		return
	}
	preview, err := service.PreviewLifecycle(r.Context(), application.LifecyclePreviewRequest{Action: action, Modules: request.Modules})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if preview.DeploymentID != request.ExpectedDeploymentID || preview.Digest != request.ExpectedDigest || !sameStrings(preview.AffectedModules, *request.ConfirmedModules) {
		writeProblem(w, http.StatusConflict, "lifecycle_preview_changed", "active deployment or lifecycle dependency chain changed after preview")
		return
	}
	execution := application.LifecycleRequest{
		Action: action, Modules: preview.RequestedModules, ExpectedDeploymentID: preview.DeploymentID,
		ExpectedDigest: preview.Digest, ExpectedModules: preview.AffectedModules,
	}
	payload, err := deploymentJSONMap(execution)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	created, err := h.deploymentHTTP.store.CreateOrGetObserved(r.Context(), consolejobs.CreateSpec{
		Kind: kind, WorkspaceID: workspaceID, Mutating: true, Request: payload,
		Idempotency: idempotency,
	}, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageJobCreateAuthorized, PlanDigest: preview.Digest,
		IdentitySource: principal.Source, TransactionID: principal.TransactionID,
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

func (h *handler) rollbackDeployment(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
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
	service := h.deploymentHTTP.serviceFactory(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment rollback is unavailable")
		return
	}
	var request rollbackHTTPRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	if request.DeploymentID == "" {
		writeProblem(w, http.StatusBadRequest, "rollback_request_invalid", "deployment_id must explicitly identify the rollback target")
		return
	}
	bound := request.ExpectedActiveDeployment != "" || request.ExpectedDigest != "" || request.AllowRisky != nil ||
		request.ConfirmedDeploymentID != "" || request.ConfirmedGuardedChanges != nil
	if !bound {
		preview, err := service.PreviewRollback(r.Context(), application.RollbackPreviewRequest{DeploymentID: request.DeploymentID})
		if err != nil {
			writeApplicationError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rollbackPreviewResponse{
			APIVersion: APIVersion, WorkspaceID: workspaceID, Preview: normalizeRollbackPreview(preview),
		})
		return
	}
	if request.ExpectedActiveDeployment == "" || !validSHA256Hex(request.ExpectedDigest) || request.AllowRisky == nil ||
		request.ConfirmedDeploymentID == "" || request.ConfirmedGuardedChanges == nil {
		writeProblem(w, http.StatusPreconditionRequired, "rollback_confirmation_required", "rollback requires a complete impact confirmation")
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
	canonicalPath := "/api/v1/workspaces/" + workspaceID + "/actions/rollback"
	idempotency := consolejobs.IdempotencyInput{
		Principal: principal.ID, Method: http.MethodPost, CanonicalPath: canonicalPath,
		Key: idempotencyKey, RequestDigest: consolejobs.DigestRequest(canonicalBody),
	}
	if existing, found, err := h.deploymentHTTP.store.LookupIdempotency(r.Context(), workspaceID, idempotency); err != nil {
		writeDeploymentStoreError(w, err)
		return
	} else if found {
		writeLifecycleJob(w, existing, true)
		return
	}
	preview, err := service.PreviewRollback(r.Context(), application.RollbackPreviewRequest{DeploymentID: request.DeploymentID})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if preview.ActiveDeployment != request.ExpectedActiveDeployment || preview.Digest != request.ExpectedDigest ||
		preview.TargetDeployment != request.ConfirmedDeploymentID || !sameStrings(preview.GuardedChanges, *request.ConfirmedGuardedChanges) {
		writeProblem(w, http.StatusConflict, "rollback_preview_changed", "active deployment or rollback impact changed after preview")
		return
	}
	execution := application.RollbackRequest{
		DeploymentID: preview.TargetDeployment, ExpectedActiveDeployment: preview.ActiveDeployment,
		ExpectedDigest: preview.Digest, AllowRisky: *request.AllowRisky,
	}
	payload, err := deploymentJSONMap(execution)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	created, err := h.deploymentHTTP.store.CreateOrGetObserved(r.Context(), consolejobs.CreateSpec{
		Kind: deploymentaudit.ActionRollback, WorkspaceID: workspaceID, Mutating: true, Request: payload,
		Idempotency: idempotency,
	}, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(deploymentaudit.Event{
		Stage: deploymentaudit.StageJobCreateAuthorized, PlanDigest: preview.Digest,
		IdentitySource: principal.Source, TransactionID: principal.TransactionID,
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

func writeLifecycleJob(w http.ResponseWriter, job consolejobs.Job, existing bool) {
	w.Header().Set("Location", "/api/v1/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, lifecycleJobResponse{APIVersion: APIVersion, Job: newJobSummaryDTO(job), Existing: existing})
}

func lifecycleJobKind(action application.LifecycleAction) (string, bool) {
	switch action {
	case application.LifecycleStart:
		return deploymentaudit.ActionStart, true
	case application.LifecycleStop:
		return deploymentaudit.ActionStop, true
	case application.LifecycleRestart:
		return deploymentaudit.ActionRestart, true
	default:
		return "", false
	}
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func normalizeLifecyclePreview(preview application.LifecyclePreviewResult) lifecyclePreviewDTO {
	return lifecyclePreviewDTO{
		DeploymentID: preview.DeploymentID, Action: preview.Action,
		RequestedModules: nonNilStrings(preview.RequestedModules), AffectedModules: nonNilStrings(preview.AffectedModules),
		Digest: preview.Digest,
	}
}

func normalizeRollbackPreview(preview application.RollbackPreviewResult) rollbackPreviewDTO {
	return rollbackPreviewDTO{
		ActiveDeployment: preview.ActiveDeployment, TargetDeployment: preview.TargetDeployment,
		GuardedChanges: nonNilStrings(preview.GuardedChanges), DataTouched: preview.DataTouched, Digest: preview.Digest,
	}
}
