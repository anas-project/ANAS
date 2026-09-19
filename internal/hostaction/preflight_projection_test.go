package hostaction

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/incushost"
)

func TestPreflightProjectionRejectsReadinessAndFreeText(t *testing.T) {
	r, err := reportFixture(context.Background(), incushost.Options{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(r)
	unchanged := false
	e := actionabi.Event{ABI: actionabi.Version, JobID: "j", InvocationID: "i", Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &unchanged, Value: body}}
	if _, err := ProjectPreflightEvent(e); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		strings.Replace(string(body), `"compute_ready":false`, `"compute_ready":true`, 1),
		strings.Replace(string(body), `"runtime_verified":false`, `"runtime_verified":true`, 1),
		strings.Replace(string(body), `"schema":`, `"secret":"private-marker","schema":`, 1),
		strings.Replace(string(body), `"facts":`, `"facts":{},"facts":`, 1),
		`{}`,
	} {
		e.Result.Value = json.RawMessage(body)
		if _, err := ProjectPreflightEvent(e); err == nil {
			t.Fatal("unapproved output accepted")
		}
	}
}
