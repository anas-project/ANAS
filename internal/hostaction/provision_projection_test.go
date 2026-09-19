package hostaction

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestProvisionProjectionRejectsUnknownFieldsAliasesAndFalseReadiness(t *testing.T) {
	value := incusprovision.ApplyResult{Schema: incusprovision.Schema, Phase: incusprovision.PhaseInstall, PlanDigest: strings.Repeat("a", 64), Disposition: "installed", Receipts: []incusprovision.Receipt{}}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	changed := true
	e := actionabi.Event{ABI: actionabi.Version, JobID: "job", InvocationID: "call", Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}}
	if _, err := ProjectActionEvent(ActionInstall, e); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		strings.Replace(string(body), `"schema":`, `"private_key":"secret-marker","schema":`, 1),
		strings.Replace(string(body), `"schema":`, `"Schema":`, 1),
		strings.Replace(string(body), `"compute_ready":false`, `"compute_ready":true`, 1),
		strings.Replace(string(body), `"phase":"install"`, `"phase":"uninstall"`, 1),
		strings.Replace(string(body), `"phase":"install"`, `"phase":"uninstall","phase":"install"`, 1),
		`{}`,
	} {
		e.Result.Value = json.RawMessage(raw)
		if _, err := ProjectActionEvent(ActionInstall, e); err == nil {
			t.Fatal("unapproved provision output accepted")
		}
	}
}
