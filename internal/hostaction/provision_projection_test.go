package hostaction

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
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
// project, and the confirmed parameters are the plan's normalized request.
// Found on the 2026-10-08 lease network host and again on finance.hlong.wang
// (2026-10-10, c397e92e), where every Incus plan job ended as
// execution_containment_lost. The parameters an operator sends back from the
// plan must match the confirmed digest, and hostd must execute that range;
// leaving the range out (what the console used to send) must not match.
func TestProvisionPlanConfirmsTheNormalizedRequest(t *testing.T) {
	release := ReleaseIdentity{Version: "1.2.3", Commit: strings.Repeat("a", 40)}
	for _, action := range []string{ActionInstallPlan, ActionConfigurePlan, ActionEnrollPlan, ActionUninstallPlan} {
		spec, _ := LookupAction(action)
		value, err := buildProvisionPlanValue(context.Background(), inspectOnlyBackend{}, spec,
			incusprovision.Request{Interface: "incus_container", StorageSizeGiB: 24}, release)
		if err != nil {
			t.Fatal(action, err)
		}
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		changed := false
		e := actionabi.Event{ABI: actionabi.Version, JobID: "job", InvocationID: "call", Type: "result", Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}}
		projected, err := ProjectActionEvent(action, e)
		if err != nil {
			t.Fatalf("%s: plan without an explicit port range did not project: %v", action, err)
		}
		var out struct {
			Parameters json.RawMessage   `json:"parameters"`
			Confirm    map[string]string `json:"action_confirmation"`
		}
		if err := json.Unmarshal(projected.Result.Value, &out); err != nil {
			t.Fatal(err)
		}
		// anasd's confirmed admission: canonical apply parameters, frozen with
		// the release, must hash to the digest the operator confirmed.
		digestOf := func(parameters json.RawMessage) string {
			canonical, err := CanonicalParameters(spec.PlanFor, parameters)
			if err != nil {
				t.Fatalf("%s: apply rejected the plan's parameters: %v", action, err)
			}
			_, frozen, err := FrozenRequest(spec.PlanFor, release, canonical)
			if err != nil {
				t.Fatal(err)
			}
			return consolejobsDigest(frozen)
		}
		if digestOf(out.Parameters) != out.Confirm["parameters_digest"] {
			t.Fatalf("%s: the plan's own parameters do not match its confirmation digest", action)
		}
		// hostd's side: the private binding digest is stripped and the apply
		// runs exactly the confirmed range.
		var wire map[string]any
		if err := json.Unmarshal(out.Parameters, &wire); err != nil {
			t.Fatal(err)
		}
		wire[consolejobs.ActionConfirmationBindingDigestRequestKey] = strings.Repeat("f", 64)
		wireBody, _ := json.Marshal(wire)
		decoded, _, err := DecodeApplyParameters(spec.PlanFor, wireBody)
		if err != nil || decoded.Request.PortRangeFirst != incusprovision.DefaultPortRangeFirst ||
			decoded.Request.PortRangeLast != incusprovision.DefaultPortRangeLast || decoded.Binding.Phase != spec.Phase {
			t.Fatalf("%s: hostd would not execute the confirmed range: %+v %v", action, decoded.Request, err)
		}
		// The binding is not weakened: the request without the range is a
		// different approval.
		var withoutRange IncusApplyParameters
		if err := json.Unmarshal(out.Parameters, &withoutRange); err != nil {
			t.Fatal(err)
		}
		withoutRange.Request.PortRangeFirst, withoutRange.Request.PortRangeLast = 0, 0
		stripped, _ := json.Marshal(withoutRange)
		if digestOf(stripped) == out.Confirm["parameters_digest"] {
			t.Fatalf("%s: parameters without the confirmed range matched its digest", action)
		}
	}
}
