package application

// TEST_CASES: TEMP-T-010
// REQUIREMENTS: TEMP-R-024

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestTemporaryStatusRetainsModuleIssuesWhenContainerProbeFails(t *testing.T) {
	workspace := t.TempDir()
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "state", "active.yml"), []byte("api_version: anas.state/v2\nactive_deployment: dep-current\nruntime_status: running\nprevious_deployments: []\n"))
	healthy := true
	probe := staticRuntimeProbe{summary: RuntimeSummary{Status: "running", Healthy: &healthy, Modules: []ModuleRuntimeStatus{{
		Module: "office", Runtime: "unknown", Health: "unhealthy",
		TempStorage: &ModuleTemporaryStorageStatus{State: "low_space", Issues: []ModuleTemporaryStorageIssue{{Code: "temp_low_space", Name: "runtime"}}},
	}}}, err: errors.New("private Docker diagnostic")}
	status, err := NewService(workspace).WithRuntimeProbe(probe).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.RuntimeStatus == nil || *status.RuntimeStatus != "unknown" || status.RuntimeHealthy != nil || status.RuntimeProbeError == nil || *status.RuntimeProbeError != "runtime_probe_failed" {
		t.Fatalf("failed probe reported stack health: %+v", status)
	}
	if len(status.ModuleRuntime) != 1 || status.ModuleRuntime[0].TempStorage == nil || status.ModuleRuntime[0].TempStorage.State != "low_space" {
		t.Fatalf("container query failure hid observed storage issues: %+v", status.ModuleRuntime)
	}
}
