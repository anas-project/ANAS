package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type moduleListResponse struct {
	APIVersion       string                    `json:"api_version"`
	WorkspaceID      string                    `json:"workspace_id"`
	ActiveDeployment *string                   `json:"active_deployment"`
	Modules          []application.ModuleState `json:"modules"`
}

type moduleCatalogResponse struct {
	APIVersion  string                          `json:"api_version"`
	WorkspaceID string                          `json:"workspace_id"`
	Catalog     application.ModuleCatalogResult `json:"catalog"`
}

type moduleUpdateHTTPRequest struct {
	Mode    string   `json:"mode"`
	Modules []string `json:"modules,omitempty"`
}

func (h *handler) listModules(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	service, ok := h.workspaceModuleService(w, params["ws"], application.NopEventSink{})
	if !ok {
		return
	}
	result, err := service.ListModules(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	etag, valid := strongConfigETag(result.ConfigValidator)
	if !valid {
		writeProblem(w, http.StatusInternalServerError, "config_validator_invalid", "workspace Module state is unavailable")
		return
	}
	w.Header().Set("ETag", etag)
	if result.Modules == nil {
		result.Modules = []application.ModuleState{}
	}
	for index := range result.Modules {
		result.Modules[index].Dependencies = nonNilStrings(result.Modules[index].Dependencies)
		if result.Modules[index].EntryPoints == nil {
			result.Modules[index].EntryPoints = []application.ModuleManagementSurface{}
		}
	}
	writeJSON(w, http.StatusOK, moduleListResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], ActiveDeployment: result.ActiveDeployment, Modules: result.Modules,
	})
}

func (h *handler) catalogModules(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	query, ok := supportedQuery(w, r, "workspace_id")
	if !ok {
		return
	}
	workspaceID := ""
	if values, present := query["workspace_id"]; present {
		if len(values) != 1 || values[0] == "" {
			writeProblem(w, http.StatusBadRequest, "workspace_id_invalid", "workspace_id must contain one registered workspace ID")
			return
		}
		workspaceID = values[0]
	} else {
		ids := h.registry.IDs()
		if len(ids) != 1 {
			writeProblem(w, http.StatusBadRequest, "workspace_id_required", "workspace_id is required when more than one workspace is registered")
			return
		}
		workspaceID = ids[0]
	}
	service, ok := h.workspaceModuleService(w, workspaceID, application.NopEventSink{})
	if !ok {
		return
	}
	result, err := service.CatalogModules(r.Context(), application.ModuleCatalogRequest{})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if result.Modules == nil {
		result.Modules = []application.ModuleCatalogEntry{}
	}
	for index := range result.Modules {
		result.Modules[index].Platforms = nonNilStrings(result.Modules[index].Platforms)
	}
	writeJSON(w, http.StatusOK, moduleCatalogResponse{APIVersion: APIVersion, WorkspaceID: workspaceID, Catalog: result})
}

func (h *handler) updateModules(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	principal, _, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID := params["ws"]
	if _, ok := h.registry.Resolve(workspaceID); !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	var request moduleUpdateHTTPRequest
	if !h.decodeDeploymentJSON(w, r, &request) {
		return
	}
	var kind string
	var payload map[string]any
	var err error
	switch request.Mode {
	case "sync":
		if len(request.Modules) != 0 {
			writeProblem(w, http.StatusBadRequest, "module_update_request_invalid", "modules is supported only in update mode")
			return
		}
		kind = deploymentaudit.ActionModuleSync
		payload, err = deploymentJSONMap(application.ModuleSyncRequest{})
	case "update":
		kind = deploymentaudit.ActionModuleUpdate
		payload, err = deploymentJSONMap(application.ModuleUpdateRequest{Modules: request.Modules})
	default:
		writeProblem(w, http.StatusBadRequest, "module_update_request_invalid", "mode must be sync or update")
		return
	}
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	h.enqueueModuleJob(w, r, principal, workspaceID, "/api/v1/workspaces/"+workspaceID+"/actions/update-modules", kind, request, payload, deploymentaudit.Event{})
}

func (h *handler) configureModule(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	principal, _, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID := params["ws"]
	if _, ok := h.registry.Resolve(workspaceID); !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	module := params["module"]
	if module == "" || len(module) > 256 || module != strings.ToLower(strings.TrimSpace(module)) {
		writeProblem(w, http.StatusBadRequest, "module_name_invalid", "Module name must be canonical")
		return
	}
	enabled := false
	kind := ""
	switch params["action"] {
	case "enable":
		enabled, kind = true, deploymentaudit.ActionModuleEnable
	case "disable":
		kind = deploymentaudit.ActionModuleDisable
	default:
		writeProblem(w, http.StatusNotFound, "module_action_not_found", "Module action was not found")
		return
	}
	mode, validator, ok := parseConfigPrecondition(w, r)
	if !ok {
		return
	}
	if mode != application.ConfigPreconditionMatch {
		writeProblem(w, http.StatusPreconditionFailed, "config_precondition_failed", "Module actions require a strong configuration ETag")
		return
	}
	var body struct{}
	if !h.decodeDeploymentJSON(w, r, &body) {
		return
	}
	operationID, err := newConfigAuditOperationID()
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "Module configuration audit is unavailable")
		return
	}
	request := application.ModuleEnabledRequest{
		Module: module, Enabled: enabled, ExpectedConfigValidator: validator, OperationID: operationID,
	}
	payload, err := deploymentJSONMap(request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	canonicalPath := "/api/v1/workspaces/" + workspaceID + "/modules/" + module + "/actions/" + params["action"]
	canonicalRequest := struct {
		ExpectedConfigValidator string `json:"expected_config_validator"`
	}{ExpectedConfigValidator: validator}
	h.enqueueModuleJob(w, r, principal, workspaceID, canonicalPath, kind, canonicalRequest, payload, deploymentaudit.Event{})
}

func (h *handler) enqueueModuleJob(w http.ResponseWriter, r *http.Request, principal Principal, workspaceID, canonicalPath, kind string, canonicalRequest any, payload map[string]any, auditEvent deploymentaudit.Event) {
	idempotencyKey, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	canonicalBody, err := json.Marshal(canonicalRequest)
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
	auditEvent.Stage = deploymentaudit.StageJobCreateAuthorized
	auditEvent.IdentitySource = principal.Source
	auditEvent.TransactionID = principal.TransactionID
	created, err := h.deploymentHTTP.store.CreateOrGetObserved(r.Context(), consolejobs.CreateSpec{
		Kind: kind, WorkspaceID: workspaceID, Mutating: true, Request: payload, Idempotency: idempotency,
	}, h.deploymentHTTP.auditObserver(withDeploymentPrincipal(auditEvent, principal)))
	if err != nil {
		writeDeploymentStoreError(w, err)
		return
	}
	if !created.Existing {
		h.deploymentHTTP.notify(workspaceID)
	}
	writeLifecycleJob(w, created.Job, created.Existing)
}

func (h *handler) workspaceModuleService(w http.ResponseWriter, workspaceID string, events application.EventSink) (application.ModuleManagementService, bool) {
	workspacePath, ok := h.registry.Resolve(workspaceID)
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return nil, false
	}
	if h.deploymentHTTP == nil || h.deploymentHTTP.moduleFactory == nil {
		writeProblem(w, http.StatusServiceUnavailable, "module_management_unavailable", "Module management is unavailable")
		return nil, false
	}
	service := h.deploymentHTTP.moduleFactory(workspacePath, events)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "module_management_unavailable", "Module management is unavailable")
		return nil, false
	}
	return service, true
}
