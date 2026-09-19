package hostaction

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestSkipRequestIsOnlyAcceptedForInstallation(t *testing.T) {
	for _, spec := range actionSpecs() {
		if spec.Name == ActionStatus {
			continue
		}
		var parameters any = IncusPlanParameters{Schema: parameterSchema, Request: incusprovision.Request{Skip: true}}
		if spec.Mutating {
			parameters = IncusApplyParameters{Schema: parameterSchema, Request: incusprovision.Request{Skip: true}, Binding: incusprovision.Binding{Schema: incusprovision.Schema, Phase: spec.Phase, PlanDigest: strings.Repeat("a", 64), Destructive: true}}
		}
		body, err := json.Marshal(parameters)
		if err != nil {
			t.Fatal(err)
		}
		_, err = CanonicalParameters(spec.Name, body)
		if (err == nil) != (spec.Phase == incusprovision.PhaseInstall) {
			t.Fatalf("skip handling for %s: %v", spec.Name, err)
		}
	}
}
