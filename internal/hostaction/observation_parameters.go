package hostaction

import (
	"encoding/json"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// ObservationScopeMatchesWorkspace prevents a job authorized for one workspace
// from selecting another workspace's installed observation scope.
func ObservationScopeMatchesWorkspace(action string, parameters json.RawMessage, workspace string) bool {
	if action == ActionObserverPlan {
		var p incusprovision.ObserverConfigurationRequest
		return strictDecode(parameters, &p) == nil && p.Validate() == nil && p.WorkspaceID == workspace
	}
	if action == ActionObserverApply {
		var p ObserverApplyParameters
		return strictDecode(parameters, &p) == nil && p.Request.Validate() == nil && p.Binding.Validate() == nil &&
			p.Schema == parameterSchema && p.Request.WorkspaceID == workspace && p.Binding.WorkspaceID == workspace
	}
	if action != ActionObserveHTTP {
		return true
	}
	var p incusingresshost.ProjectionRequest
	return strictDecode(parameters, &p) == nil && p.Validate() == nil && p.ScopeID == workspace
}
