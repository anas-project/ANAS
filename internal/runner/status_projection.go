package runner

import (
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
)

func workspaceStatusDocument(status application.StatusResult) map[string]any {
	modules := append([]application.ModuleRuntimeStatus{}, status.ModuleRuntime...)
	for i := range modules {
		if modules[i].TempStorage != nil {
			storage := *modules[i].TempStorage
			storage.Issues = append([]application.ModuleTemporaryStorageIssue{}, storage.Issues...)
			modules[i].TempStorage = &storage
		}
	}
	return map[string]any{
		"workspace": status.Workspace, "active_deployment": status.ActiveDeployment,
		"activated_at": status.ActivatedAt, "verified_at": status.VerifiedAt,
		"previous_deployments": append([]string{}, status.PreviousDeployments...),
		"runtime_status":       status.RuntimeStatus, "runtime_healthy": status.RuntimeHealthy,
		"runtime_probe_error": status.RuntimeProbeError, "module_runtime": modules,
	}
}

func workspaceStatusSummary(status application.StatusResult) string {
	var out strings.Builder
	if status.ActiveDeployment == nil {
		fmt.Fprintln(&out, "active: none")
	} else {
		fmt.Fprintf(&out, "active: %s\nactivated_at: %s\nverified_at: %s\n",
			*status.ActiveDeployment, optionalString(status.ActivatedAt), optionalString(status.VerifiedAt))
	}
	if status.RuntimeStatus != nil {
		fmt.Fprintf(&out, "runtime: %s\n", *status.RuntimeStatus)
	}
	if status.RuntimeHealthy != nil {
		fmt.Fprintf(&out, "runtime_healthy: %t\n", *status.RuntimeHealthy)
	}
	if status.RuntimeProbeError != nil {
		fmt.Fprintf(&out, "runtime_probe_error: %s\n", *status.RuntimeProbeError)
	}
	for _, module := range status.ModuleRuntime {
		fmt.Fprintf(&out, "module %s: runtime=%s health=%s containers=%d\n", module.Module, module.Runtime, module.Health, module.Containers)
		if module.TempStorage != nil {
			fmt.Fprintf(&out, "  temp_storage: %s\n", module.TempStorage.State)
			for _, issue := range module.TempStorage.Issues {
				fmt.Fprintf(&out, "  temporary %s: %s\n", issue.Name, issue.Code)
			}
		}
	}
	if len(status.PreviousDeployments) > 0 {
		fmt.Fprintln(&out, "previous: "+strings.Join(status.PreviousDeployments, ","))
	}
	return out.String()
}
