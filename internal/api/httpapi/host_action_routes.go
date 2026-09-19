package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

const hostPreflightRoute = "/api/v1/workspaces/{ws}/host/actions/incus.status"

// HostActionOptions exposes only queue admission. The callback must belong to
// the daemon-lifetime service; no executor or socket is owned by an HTTP request.
type HostActionOptions struct {
	InvokePreflight           func(context.Context, string, string, string) (consolejobs.CreateResult, error)
	InvokePlan                func(context.Context, string, string, string, json.RawMessage, string) (consolejobs.CreateResult, error)
	InvokeImagePrunePlan      func(context.Context, string, string, string) (consolejobs.CreateResult, error)
	IssueConfirmation         func(context.Context, string, string, string, string) (hostconfirmation.IssueResult, error)
	InvokeConfirmed           func(context.Context, string, string, string, string, json.RawMessage, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error)
	InvokeImagePruneConfirmed func(context.Context, string, string, string, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error)
}

type hostActionPlanHTTPRequest struct {
	Request json.RawMessage `json:"request"`
}

type hostActionConfirmHTTPRequest struct {
	PlanJobID string `json:"plan_job_id"`
	Action    string `json:"action"`
}

type hostActionApplyHTTPRequest struct {
	PlanJobID         string          `json:"plan_job_id"`
	ConfirmationToken string          `json:"confirmation_token"`
	Parameters        json.RawMessage `json:"parameters"`
}

type hostActionPruneApplyHTTPRequest struct {
	PlanJobID         string `json:"plan_job_id"`
	ConfirmationToken string `json:"confirmation_token"`
}

type hostActionConfirmationResponse struct {
	APIVersion    string    `json:"api_version"`
	Token         string    `json:"token"`
	BindingDigest string    `json:"binding_digest"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func (h *handler) invokeHostPreflight(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	principal, state, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	if state != StateFull || principal.Role != "owner" {
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
		return
	}
	workspace := params["ws"]
	if _, ok := h.registry.Resolve(workspace); !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	// This compiled action takes NO parameters. Do not decode permissively:
	// null, duplicate keys, case aliases, arbitrary paths and trailing frames
	// must never become an empty request. Whitespace around {} is allowed.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	defer clear(body)
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeProblem(w, http.StatusBadRequest, "invalid_json", "request must be an empty JSON object")
		}
		return
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) != nil || !bytes.Equal(compact.Bytes(), []byte("{}")) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request must be an empty JSON object")
		return
	}
	key, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	created, err := h.hostActions.InvokePreflight(r.Context(), principal.ID, workspace, key)
	if err != nil {
		switch {
		case errors.Is(err, hostaction.ErrDenied):
			writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
		case errors.Is(err, hostaction.ErrAudit), errors.Is(err, hostaction.ErrUnavailable), errors.Is(err, consolejobs.ErrActionContainment), errors.Is(err, consolejobs.ErrActionExecutionBlocked):
			writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action admission is unavailable")
		default:
			writeDeploymentStoreError(w, err)
		}
		return
	}
	// Use the existing value-free public projection, never job.Request, actor,
	// paths, release policy, or credentials. This is admission, not execution.
	w.Header().Set("Location", "/api/v1/jobs/"+created.Job.ID)
	writeJSON(w, http.StatusAccepted, jobDetailResponse{APIVersion: APIVersion, Job: newJobDetailDTO(created.Job)})
}

func (h *handler) invokeHostActionPlan(w http.ResponseWriter, r *http.Request, params map[string]string) {
	principal, workspace, ok := h.hostActionPrincipal(w, r, params)
	if !ok {
		return
	}
	if h.hostActions == nil || h.hostActions.InvokePlan == nil {
		writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action admission is unavailable")
		return
	}
	action := "incus." + params["phase"] + ".plan"
	if spec, exists := hostaction.LookupAction(action); !exists || spec.PlanFor == "" {
		writeProblem(w, http.StatusNotFound, "not_found", "host action was not found")
		return
	}
	var body hostActionPlanHTTPRequest
	if !decodeStrictHostActionJSON(w, r, &body) {
		return
	}
	request, err := decodeIncusProvisionRequest(body.Request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request is invalid")
		return
	}
	parameters, err := json.Marshal(hostaction.IncusPlanParameters{Schema: "anas.host-action.incus/v1", Request: request})
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request is invalid")
		return
	}
	parameters, err = hostaction.CanonicalParameters(action, parameters)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "host action request is invalid")
		return
	}
	key, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	created, err := h.hostActions.InvokePlan(r.Context(), principal.ID, workspace, action, parameters, key)
	h.writeHostActionCreate(w, r, created, err)
}

func (h *handler) invokeHostImagePrunePlan(w http.ResponseWriter, r *http.Request, params map[string]string) {
	principal, workspace, ok := h.hostActionPrincipal(w, r, params)
	if !ok {
		return
	}
	if h.hostActions == nil || h.hostActions.InvokeImagePrunePlan == nil {
		writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action admission is unavailable")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	defer clear(body)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request must be an empty JSON object")
		return
	}
	var compact bytes.Buffer
	if json.Compact(&compact, body) != nil || !bytes.Equal(compact.Bytes(), []byte("{}")) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request must be an empty JSON object")
		return
	}
	key, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	created, err := h.hostActions.InvokeImagePrunePlan(r.Context(), principal.ID, workspace, key)
	h.writeHostActionCreate(w, r, created, err)
}

func (h *handler) issueHostActionConfirmation(w http.ResponseWriter, r *http.Request, params map[string]string) {
	principal, workspace, ok := h.hostActionPrincipal(w, r, params)
	if !ok {
		return
	}
	if h.hostActions == nil || h.hostActions.IssueConfirmation == nil {
		writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action confirmation is unavailable")
		return
	}
	var body hostActionConfirmHTTPRequest
	if !decodeStrictHostActionJSON(w, r, &body) {
		return
	}
	if body.PlanJobID == "" || !hostaction.IsApplyAction(body.Action) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "confirmation request is invalid")
		return
	}
	issued, err := h.hostActions.IssueConfirmation(r.Context(), principal.ID, workspace, body.PlanJobID, body.Action)
	if err != nil {
		h.writeHostActionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, hostActionConfirmationResponse{APIVersion: APIVersion, Token: issued.Token.Value(), BindingDigest: issued.BindingDigest, ExpiresAt: issued.ExpiresAt})
}

func (h *handler) invokeHostActionApply(w http.ResponseWriter, r *http.Request, params map[string]string) {
	principal, workspace, ok := h.hostActionPrincipal(w, r, params)
	if !ok {
		return
	}
	if h.hostActions == nil || h.hostActions.InvokeConfirmed == nil {
		writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action admission is unavailable")
		return
	}
	action := "incus." + params["phase"]
	if !hostaction.IsApplyAction(action) {
		writeProblem(w, http.StatusNotFound, "not_found", "host action was not found")
		return
	}
	var body hostActionApplyHTTPRequest
	if !decodeStrictHostActionJSON(w, r, &body) {
		return
	}
	if body.PlanJobID == "" || body.ConfirmationToken == "" || len(body.Parameters) == 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "apply request is invalid")
		return
	}
	canonical, err := hostaction.CanonicalParameters(action, body.Parameters)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "host action request is invalid")
		return
	}
	key, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	created, err := h.hostActions.InvokeConfirmed(r.Context(), principal.ID, workspace, action, body.PlanJobID, canonical, hostconfirmation.RawToken(body.ConfirmationToken), key)
	h.writeHostActionCreate(w, r, created, err)
}

func (h *handler) invokeHostImagePruneApply(w http.ResponseWriter, r *http.Request, params map[string]string) {
	principal, workspace, ok := h.hostActionPrincipal(w, r, params)
	if !ok {
		return
	}
	if h.hostActions == nil || h.hostActions.InvokeImagePruneConfirmed == nil {
		writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action admission is unavailable")
		return
	}
	var body hostActionPruneApplyHTTPRequest
	if !decodeStrictHostActionJSON(w, r, &body) {
		return
	}
	if body.PlanJobID == "" || body.ConfirmationToken == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "apply request is invalid")
		return
	}
	key, ok := deploymentIdempotencyKey(w, r)
	if !ok {
		return
	}
	created, err := h.hostActions.InvokeImagePruneConfirmed(r.Context(), principal.ID, workspace, body.PlanJobID, hostconfirmation.RawToken(body.ConfirmationToken), key)
	h.writeHostActionCreate(w, r, created, err)
}

func (h *handler) hostActionPrincipal(w http.ResponseWriter, r *http.Request, params map[string]string) (principal Principal, workspace string, ok bool) {
	if _, ok = supportedQuery(w, r); !ok {
		return principal, "", false
	}
	principal, state, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return principal, "", false
	}
	if state != StateFull || principal.Role != "owner" {
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
		return principal, "", false
	}
	workspace = params["ws"]
	if _, ok = h.registry.Resolve(workspace); !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return principal, "", false
	}
	return principal, workspace, true
}

func decodeStrictHostActionJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<10))
	defer clear(body)
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeProblem(w, http.StatusBadRequest, "invalid_json", "request could not be read")
		}
		return false
	}
	if actionabi.DecodeTypedObject(body, out) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request is invalid")
		return false
	}
	return true
}

func decodeIncusProvisionRequest(body json.RawMessage) (incusprovision.Request, error) {
	var request incusprovision.Request
	if actionabi.DecodeTypedObject(body, &request) != nil {
		return incusprovision.Request{}, hostaction.ErrRequest
	}
	return request, nil
}

func (h *handler) writeHostActionCreate(w http.ResponseWriter, r *http.Request, created consolejobs.CreateResult, err error) {
	if err != nil {
		h.writeHostActionError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+created.Job.ID)
	writeJSON(w, http.StatusAccepted, jobDetailResponse{APIVersion: APIVersion, Job: newJobDetailDTO(created.Job)})
}

func (h *handler) writeHostActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, hostaction.ErrDenied):
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
	case errors.Is(err, hostaction.ErrRequest), errors.Is(err, consolejobs.ErrConfirmationInvalid):
		writeProblem(w, http.StatusBadRequest, "invalid_json", "host action request is invalid")
	case errors.Is(err, hostconfirmation.ErrExpired):
		writeProblem(w, http.StatusConflict, "confirmation_expired", "host action confirmation expired")
	case errors.Is(err, hostconfirmation.ErrConsumed):
		writeProblem(w, http.StatusConflict, "confirmation_consumed", "host action confirmation was already consumed")
	case errors.Is(err, hostaction.ErrAudit), errors.Is(err, hostaction.ErrUnavailable), errors.Is(err, consolejobs.ErrActionContainment), errors.Is(err, consolejobs.ErrActionExecutionBlocked):
		writeProblem(w, http.StatusServiceUnavailable, "host_actions_unavailable", "host action admission is unavailable")
	default:
		writeDeploymentStoreError(w, err)
	}
}
