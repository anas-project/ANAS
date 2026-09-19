package jobexecutor

import (
	"encoding/json"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

func publicParametersFromStoredRequest(action string, request map[string]any) (json.RawMessage, error) {
	version, _ := request["release_version"].(string)
	commit, _ := request["release_commit"].(string)
	if version == "" || commit == "" {
		return nil, hostaction.ErrRequest
	}
	params, ok := request["parameters"].(map[string]any)
	if !ok {
		return nil, hostaction.ErrRequest
	}
	return hostaction.PublicParametersFromJob(action, params)
}

func parametersForExecution(job consolejobs.Job) (json.RawMessage, error) {
	if job.Action == nil {
		return nil, hostaction.ErrDenied
	}
	params, ok := job.Request["parameters"].(map[string]any)
	if !ok {
		return nil, hostaction.ErrDenied
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, hostaction.ErrDenied
	}
	public, err := hostaction.CanonicalParameters(job.Action.Name, body)
	if err != nil {
		return nil, hostaction.ErrDenied
	}
	if !hostaction.IsApplyAction(job.Action.Name) {
		return public, nil
	}
	withPrivate := map[string]any{}
	if err := json.Unmarshal(public, &withPrivate); err != nil {
		return nil, hostaction.ErrDenied
	}
	for _, key := range reservedConfirmationRequestKeys() {
		if value, ok := job.Request[key]; ok {
			withPrivate[key] = value
		}
	}
	body, err = json.Marshal(withPrivate)
	if err != nil {
		return nil, hostaction.ErrDenied
	}
	return hostaction.CanonicalWireParameters(job.Action.Name, body)
}

func publicStoredRequest(request map[string]any) map[string]any {
	public := map[string]any{}
	for key, value := range request {
		if isReservedConfirmationRequestKey(key) {
			continue
		}
		public[key] = value
	}
	return public
}

func reservedConfirmationRequestKeys() []string {
	return []string{
		consolejobs.ActionConfirmationBindingDigestRequestKey,
		consolejobs.ConfirmationPlanJobRequestKey,
		consolejobs.ConfirmationActionRequestKey,
		consolejobs.ActionConfirmationPlanInvocationRequestKey,
		consolejobs.ActionConfirmationParametersDigestRequestKey,
		consolejobs.ActionConfirmationStateDigestRequestKey,
		consolejobs.ActionConfirmationSummaryDigestRequestKey,
		consolejobs.ActionConfirmationReleaseDigestRequestKey,
	}
}

func isReservedConfirmationRequestKey(key string) bool {
	for _, reserved := range reservedConfirmationRequestKeys() {
		if key == reserved {
			return true
		}
	}
	return false
}
