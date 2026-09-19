package hostaction

import (
	"encoding/json"
	"testing"
)

func TestIncusCanonicalParametersSurviveJobMapSerialization(t *testing.T) {
	canonical, err := CanonicalParameters(ActionInstallPlan, []byte(`{"schema":"anas.host-action.incus/v1","request":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(canonical, &stored); err != nil {
		t.Fatal(err)
	}
	// The persistent job store contains map values, whose keys are sorted by
	// encoding/json rather than written in the original struct declaration order.
	fromStore, err := PublicParametersFromJob(ActionInstallPlan, stored)
	if err != nil || string(fromStore) != string(canonical) {
		t.Fatalf("job map broke canonical plan parameters: %v", err)
	}
}
