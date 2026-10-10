package hostaction

import (
	"context"
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

// inspectOnlyBackend plans the way the real backend does: its plan carries
// the normalized request, with the default port range filled in.
type inspectOnlyBackend struct{ incusBackend }

func (inspectOnlyBackend) Inspect(_ context.Context, request incusprovision.Request) (incusprovision.InspectResult, error) {
	if request.PortRangeFirst == 0 && request.PortRangeLast == 0 {
		request.PortRangeFirst, request.PortRangeLast = incusprovision.DefaultPortRangeFirst, incusprovision.DefaultPortRangeLast
	}
	return incusprovision.InspectResult{Schema: incusprovision.Schema,
		Plan: incusprovision.Plan{Schema: incusprovision.Schema, Request: request, Digest: strings.Repeat("d", 64), StateDigest: strings.Repeat("e", 64)}}, nil
}

// A plan requested without a port range (the CLI and console default) must
// project: the confirmed parameters are the plan's normalized request. Found
// on the 2026-10-08 lease network host, where every Incus plan job ended as
// execution_containment_lost.
func TestProvisionPlanConfirmsTheNormalizedRequest(t *testing.T) {
	release := ReleaseIdentity{Version: "1.2.3", Commit: strings.Repeat("a", 40)}
	spec, _ := LookupAction(ActionInstallPlan)
	value, err := buildProvisionPlanValue(context.Background(), inspectOnlyBackend{}, spec,
		incusprovision.Request{Interface: "incus_container", StorageSizeGiB: 24}, release)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	e := actionabi.Event{ABI: actionabi.Version, JobID: "job", InvocationID: "call", Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}}
	projected, err := ProjectActionEvent(ActionInstallPlan, e)
	if err != nil {
		t.Fatalf("plan without an explicit port range did not project: %v", err)
	}
	var out struct {
		Parameters IncusApplyParameters `json:"parameters"`
	}
	if json.Unmarshal(projected.Result.Value, &out) != nil || out.Parameters.Request.PortRangeFirst != incusprovision.DefaultPortRangeFirst ||
		out.Parameters.Request.PortRangeLast != incusprovision.DefaultPortRangeLast {
		t.Fatalf("confirmed parameters do not name the approved range: %+v", out.Parameters.Request)
	}
}
