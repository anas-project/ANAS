package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestObserverHTTPPlanDerivesWorkspaceAndRejectsCallerAuthority(t *testing.T) {
	for _, operation := range []string{"refresh", "disable"} {
		body, err := hostPlanParameters(hostaction.ActionObserverPlan, "main", json.RawMessage(`{"operation":"`+operation+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := hostaction.CanonicalParameters(hostaction.ActionObserverPlan, body)
		if err != nil {
			t.Fatal(err)
		}
		var request incusprovision.ObserverConfigurationRequest
		if err := json.Unmarshal(canonical, &request); err != nil || request.WorkspaceID != "main" || request.Operation != operation {
			t.Fatal("scope was not server-selected", err)
		}
	}
	for _, body := range []string{`{"operation":"refresh","workspace_id":"other"}`, `{"Operation":"disable"}`, `{"operation":"refresh","endpoint":"private-marker"}`, `{"operation":"refresh","operation":"disable"}`} {
		_, err := hostPlanParameters(hostaction.ActionObserverPlan, "main", json.RawMessage(body))
		if err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("caller authority accepted or exposed", err)
		}
	}
}
