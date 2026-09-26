package hostaction

import (
	"encoding/json"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

// ObservationScopeMatchesWorkspace prevents a job authorized for one workspace
// from selecting another workspace's installed observation scope.
func ObservationScopeMatchesWorkspace(action string, parameters json.RawMessage, workspace string) bool {
	if action == ActionForwardingWithdraw {
		var p incusprovision.ForwardingWithdrawalRequest
		return strictDecode(parameters, &p) == nil && p.Validate() == nil && p.WorkspaceID == workspace
	}
	if action == ActionForwardingPlan {
		var p incusprovision.ForwardingPermissionRequest
		return strictDecode(parameters, &p) == nil && p.Validate() == nil && p.WorkspaceID == workspace
	}
	if action == ActionForwardingApply {
		var p ForwardingApplyParameters
		return strictDecode(parameters, &p) == nil && p.Schema == parameterSchema && p.Request.Validate() == nil && p.Binding.Validate() == nil && p.Request.WorkspaceID == workspace && p.Binding.WorkspaceID == workspace
	}
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
