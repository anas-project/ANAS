package httpapi

// TEST_CASES: TEMP-T-010
// REQUIREMENTS: TEMP-R-024 TEMP-R-038

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

func TestTemporarySwitchIsProjectedInPlanAndRollback(t *testing.T) {
	switchPlan := &application.TempSwitchPlan{
		Required: true, StopModules: []string{"app", "db"}, StartModules: []string{"db", "app"}, SessionInterruption: true,
	}
	for _, dto := range []any{
		newDeploymentPlanDTO(application.PlanResult{TempSwitch: switchPlan}),
		normalizeRollbackPreview(application.RollbackPreviewResult{TempSwitch: switchPlan}),
	} {
		body, err := json.Marshal(dto)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{`"temp_switch":`, `"stop_modules":["app","db"]`, `"start_modules":["db","app"]`, `"session_interruption":true`} {
			if !strings.Contains(string(body), expected) {
				t.Fatalf("switch impact is missing from HTTP DTO: %s", body)
			}
		}
	}
}

func TestTemporaryRuntimeStorageContainsOnlyPublicFields(t *testing.T) {
	response := newStatusResponse("main", application.StatusResult{ModuleRuntime: []application.ModuleRuntimeStatus{{
		Module: "app", Health: "healthy", Runtime: "running", Containers: 1,
		TempStorage: &application.ModuleTemporaryStorageStatus{State: "low_space", Issues: []application.ModuleTemporaryStorageIssue{{Code: "temp_low_space", Name: "runtime"}}},
	}}})
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temp_storage":{"state":"low_space","issues":[{"code":"temp_low_space","name":"runtime"}]}`) {
		t.Fatalf("storage problem omitted despite successful application probe: %s", body)
	}
	if strings.Contains(string(body), `"path"`) || strings.Contains(string(body), `"message"`) {
		t.Fatal("runtime storage DTO exposes host details")
	}
}
