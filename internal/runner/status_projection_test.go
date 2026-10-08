package runner

// TEST_CASES: TEMP-T-010
// REQUIREMENTS: TEMP-R-024 TEMP-R-025

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

func TestTemporaryStatusCLIReportsAndClearsStorageProblems(t *testing.T) {
	deployment, runtime := "dep-current", "running"
	status := application.StatusResult{
		Workspace: "/workspace", ActiveDeployment: &deployment, RuntimeStatus: &runtime,
		ModuleRuntime: []application.ModuleRuntimeStatus{{
			Module: "app", Runtime: "running", Health: "unhealthy", Containers: 1,
			TempStorage: &application.ModuleTemporaryStorageStatus{
				State: "low_space", Issues: []application.ModuleTemporaryStorageIssue{{Code: "temp_low_space", Name: "runtime"}},
			},
		}},
	}
	text := workspaceStatusSummary(status)
	if !strings.Contains(text, "temp_storage: low_space") || !strings.Contains(text, "temporary runtime: temp_low_space") {
		t.Fatalf("CLI text hid storage failure: %s", text)
	}
	body, err := json.Marshal(workspaceStatusDocument(status))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temp_storage":{"state":"low_space"`) || !strings.Contains(string(body), `"runtime_status":"running"`) {
		t.Fatalf("CLI JSON omitted live status: %s", body)
	}
	status.ModuleRuntime[0].TempStorage.State = "ok"
	status.ModuleRuntime[0].TempStorage.Issues = nil
	if text := workspaceStatusSummary(status); strings.Contains(text, "temp_low_space") || !strings.Contains(text, "temp_storage: ok") {
		t.Fatalf("recovered storage issue remained visible: %s", text)
	}
	body, err = json.Marshal(workspaceStatusDocument(status))
	if err != nil || !strings.Contains(string(body), `"issues":[]`) {
		t.Fatalf("recovered CLI status must expose an empty issue array: %s, %v", body, err)
	}
}

func TestTemporaryCLIHelpIncludesStatusAndGC(t *testing.T) {
	stdout, _, exit := capture(t, "help", "--json")
	document := requireSingleDocument(t, "help", stdout)
	if exit != 0 {
		t.Fatalf("help exit: %d", exit)
	}
	found := false
	for _, command := range document["commands"].([]any) {
		found = found || command == "temp"
	}
	if !found {
		t.Fatal("help omitted the temporary storage command")
	}
	stdout, _, exit = capture(t, "help")
	if exit != 0 || !strings.Contains(stdout, "anas temp status") || !strings.Contains(stdout, "anas temp gc --dry-run -w WORKSPACE") {
		t.Fatalf("human help omitted temporary storage discovery: %s", stdout)
	}
}

func TestWorkspaceStatusCLIReportsUnknownProbeWithoutPersistedHealth(t *testing.T) {
	deployment, runtime, code := "dep-current", "unknown", "runtime_probe_failed"
	status := application.StatusResult{ActiveDeployment: &deployment, RuntimeStatus: &runtime, RuntimeProbeError: &code}
	text := workspaceStatusSummary(status)
	if !strings.Contains(text, "runtime: unknown") || !strings.Contains(text, "runtime_probe_error: runtime_probe_failed") || strings.Contains(text, "runtime_healthy: true") {
		t.Fatalf("CLI inferred live health from deployment history: %s", text)
	}
}
