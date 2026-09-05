package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

// This file is the shape a new console use case should take: the whole
// invocation path -- resolution, validation, locking, executor protocol -- lives
// in internal/application, and both the CLI and this adapter call it. Nothing
// here reaches into internal/runner.

// moduleCommandInvokeHTTPRequest binds an invocation to the exact descriptor the
// client read from the command endpoints. Parameters stay a typed map validated
// by the shared application service; the adapter never builds argv or an
// environment of its own.
type moduleCommandInvokeHTTPRequest struct {
	CommandDigest string         `json:"command_digest"`
	Parameters    map[string]any `json:"parameters,omitempty"`
	StepUpProof   string         `json:"step_up_proof,omitempty"`
}

// invokeModuleCommand completes the boundary ModuleCommandService documents:
// the console runs a Module command through the same resolution, validation and
// locking the CLI runs, as an audited persistent job rather than synchronously
// inside the request.
func (h *handler) invokeModuleCommand(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	principal, _, ok := h.jobRequestPrincipal(w, r)
	if !ok {
		return
	}
	workspaceID := params["ws"]
	if _, resolved := h.registry.Resolve(workspaceID); !resolved {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	var body moduleCommandInvokeHTTPRequest
	if !h.decodeDeploymentJSON(w, r, &body) {
		return
	}
	if body.CommandDigest == "" {
		writeProblem(w, http.StatusPreconditionRequired, "module_command_digest_required",
			"invocation must carry the command_digest returned by the command endpoints")
		return
	}

	// Read the frozen descriptor before authorizing. Risk and availability are
	// properties of the active deployment, not of the request, so the gate a
	// destructive command needs cannot be chosen by the caller.
	service, ok := h.workspaceService(w, workspaceID)
	if !ok {
		return
	}
	effective, err := service.GetModuleCommand(r.Context(), application.GetModuleCommandRequest{
		Module: params["module"], Command: params["command"],
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if effective.Command.Digest != body.CommandDigest {
		writeProblem(w, http.StatusPreconditionFailed, "module_command_changed",
			"the Module command descriptor changed; read the command again")
		return
	}
	if !effective.Available {
		writeProblem(w, http.StatusConflict, "module_command_unavailable", "the Module command is not available")
		return
	}

	targetID := effective.Module + "." + effective.Command.ID
	if effective.Command.Risk == moduleCommandRiskDestructive {
		if body.StepUpProof == "" {
			writeProblem(w, http.StatusPreconditionRequired, "step_up_required",
				"a destructive Module command requires a step-up proof")
			return
		}
		if !validDeploymentStepUpProof(body.StepUpProof) {
			writeProblem(w, http.StatusBadRequest, "step_up_invalid", "step-up proof format is invalid")
			return
		}
		stateDigest := moduleCommandStepUpStateDigest(workspaceID, targetID, effective.DeploymentID, effective.Command.Digest)
		if !h.consumeActionStepUp(w, r, deploymentaudit.ActionModuleCommandInvoke, workspaceID, targetID, stateDigest, body.StepUpProof) {
			return
		}
	} else if body.StepUpProof != "" {
		// Refusing an unnecessary proof keeps a single-use credential from being
		// spent on an operation that never needed it.
		writeProblem(w, http.StatusBadRequest, "step_up_request_invalid",
			"this Module command does not accept a step-up proof")
		return
	}

	request := application.InvokeModuleCommandRequest{
		Module: effective.Module, Command: effective.Command.ID,
		Parameters: body.Parameters, CommandDigest: effective.Command.Digest,
	}
	payload, err := deploymentJSONMap(request)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return
	}
	canonicalPath := "/api/v1/workspaces/" + workspaceID + "/modules/" + effective.Module +
		"/commands/" + effective.Command.ID + "/actions/invoke"
	h.enqueueModuleJob(w, r, principal, workspaceID, canonicalPath,
		deploymentaudit.ActionModuleCommandInvoke, request, payload, deploymentaudit.Event{TargetID: targetID})
}

const moduleCommandRiskDestructive = "destructive"

// moduleCommandStepUpStateDigest binds a proof to the deployment and descriptor
// that were live when it was issued, so a redeploy or a Module upgrade between
// issuance and use invalidates it.
func moduleCommandStepUpStateDigest(workspaceID, targetID, deploymentID, commandDigest string) string {
	body, _ := json.Marshal([]string{
		deploymentaudit.ActionModuleCommandInvoke, workspaceID, targetID, deploymentID, commandDigest,
	})
	return consolejobs.DigestRequest(body)
}
