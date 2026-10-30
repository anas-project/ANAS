// TEST_CASES: MAILMOD-T-042
package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func handoffFixture(t *testing.T) (*Journal, *fakeEndpoint, RuntimeRequest) {
	t.Helper()
	path, spec := fixture(t)
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
	return j, cloud, RuntimeRequest{Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, NotificationOrigin: "https://notify.example.test", ZoneID: strings.Repeat("1", 32), RoutingToken: strings.Repeat("R", 32)}
}

func TestRuntimeHandoffMatchesExistingConfiguration(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	req.Proxy = "http://user:proxy-password@proxy.example.test:8080"
	prepared, err := j.prepareRuntime(t.Context(), cloud, req)
	if err != nil {
		t.Fatal(err)
	}
	spec := prepared.Specification()
	var private bytes.Buffer
	if err = prepared.WriteCredentials(&private); err != nil {
		t.Fatal(err)
	}
	descriptions, err := json.Marshal([]ingress.SourceSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := ingress.ParseSources(string(descriptions), private.String(), req.AllowedDomains)
	if err != nil || len(sources) != 1 || sources[0].Credentials.Routing != req.RoutingToken || sources[0].Credentials.Notification == "" || sources[0].Cloudflare.Worker != j.state.Spec.Worker {
		t.Fatal("handoff is not accepted by runtime", err)
	}
	for _, encoded := range []string{string(descriptions), fmt.Sprintf("%v", prepared), fmt.Sprintf("%+v", prepared), fmt.Sprintf("%#v", prepared)} {
		for _, secret := range []string{sources[0].Credentials.Reader, sources[0].Credentials.Control, sources[0].Credentials.Notification, req.RoutingToken, "proxy-password"} {
			if strings.Contains(encoded, secret) {
				t.Fatal("public handoff exposed a credential")
			}
		}
	}
	requestJSON, err := json.Marshal(req)
	if err != nil || strings.Contains(string(requestJSON), req.RoutingToken) || strings.Contains(string(requestJSON), "proxy-password") || strings.Contains(fmt.Sprintf("%+v", req), req.RoutingToken) || strings.Contains(fmt.Sprintf("%#v", req), "proxy-password") {
		t.Fatal("public request formatting exposed a credential", err)
	}
	spec.Domains[0] = "changed.test"
	spec.Cloudflare.Worker = "changed"
	if again := prepared.Specification(); again.Domains[0] != "example.test" || again.Cloudflare.Worker != j.state.Spec.Worker {
		t.Fatal("caller changed private handoff")
	}
	second, err := j.prepareRuntime(t.Context(), cloud, req)
	if err != nil {
		t.Fatal(err)
	}
	var recovered bytes.Buffer
	if err = second.WriteCredentials(&recovered); err != nil || recovered.String() != private.String() || cloud.workerCreates != 1 || cloud.bucketCreates != 1 || cloud.writes != 1 {
		t.Fatal("handoff recovery changed secrets or repeated cloud writes", err)
	}
}

func TestRuntimeHandoffRejectsInvalidInputBeforeCloudWrites(t *testing.T) {
	for _, scenario := range []string{"zone", "routing-token", "domain", "allowed-domain", "proxy", "notification"} {
		t.Run(scenario, func(t *testing.T) {
			j, cloud, req := handoffFixture(t)
			switch scenario {
			case "zone":
				req.ZoneID = "invalid"
			case "routing-token":
				req.RoutingToken = "invalid"
			case "domain":
				req.Domains = []string{"not allowed.test"}
			case "allowed-domain":
				req.AllowedDomains = []string{"another.test"}
			case "proxy":
				req.Proxy = "file:///invalid"
			case "notification":
				req.NotificationOrigin = "http://invalid.test"
			}
			if _, err := j.prepareRuntime(t.Context(), cloud, req); !errors.Is(err, ErrJournal) {
				t.Fatal("invalid request accepted", err)
			}
			if cloud.workerCreates != 0 || cloud.bucketCreates != 0 || cloud.creates != 0 || cloud.writes != 0 || j.state.Stage != "prepared" {
				t.Fatal("invalid handoff created cloud resources")
			}
		})
	}
}

func TestRuntimeHandoffNeverReturnsSecretsOnUnknownOutcome(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	cloud.lost = true
	prepared, err := j.prepareRuntime(t.Context(), cloud, req)
	if !errors.Is(err, ErrUnknown) || prepared.Specification().ID != "" || prepared.WriteCredentials(&bytes.Buffer{}) == nil {
		t.Fatal("unknown cloud outcome returned a handoff", err)
	}
}

func TestRuntimeHandoffRequiresSeparateDeploymentAndRoutingTokens(t *testing.T) {
	j, _, req := handoffFixture(t)
	cloud, err := NewCloudflare(req.RoutingToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = j.PrepareRuntime(t.Context(), cloud, req); !errors.Is(err, ErrJournal) || j.state.Stage != "prepared" {
		t.Fatal("deployment credential reused for routing", err)
	}
}

func TestRuntimeHandoffWithoutNotification(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	req.NotificationOrigin = ""
	prepared, err := j.prepareRuntime(t.Context(), cloud, req)
	if err != nil || prepared.Specification().Notifications {
		t.Fatal("notification should be optional", err)
	}
	var raw bytes.Buffer
	if err = prepared.WriteCredentials(&raw); err != nil {
		t.Fatal(err)
	}
	var credentials map[string]ingress.SourceCredentials
	if err = json.Unmarshal(raw.Bytes(), &credentials); err != nil || credentials[prepared.Specification().ID].Notification != "" {
		t.Fatal("unexpected notification credential", err)
	}
}

func TestRuntimeHandoffMergePreservesDrainingSourcesAndCapacity(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	prepared, err := j.prepareRuntime(t.Context(), cloud, req)
	if err != nil {
		t.Fatal(err)
	}
	old := ingress.RuntimeSource{SourceSpec: ingress.SourceSpec{ID: "22222222-2222-4222-8222-222222222222", Generation: "33333333-3333-4333-8333-333333333333", Provider: "cloud-relay", Mode: "draining", Origin: "https://old.example.test", Domains: []string{"example.test"}, CapacityBytes: 1 << 30, DeliveredDays: 7}, Credentials: ingress.SourceCredentials{Reader: strings.Repeat("d", 64), Control: strings.Repeat("e", 64)}}
	merged, err := prepared.Merge([]ingress.RuntimeSource{old}, req.AllowedDomains, 3<<30)
	if err != nil {
		t.Fatal(err)
	}
	var secret bytes.Buffer
	description, err := json.Marshal(merged.Specifications())
	if err != nil || merged.WriteCredentials(&secret) != nil {
		t.Fatal("cannot encode merged configuration", err)
	}
	sources, err := ingress.ParseSources(string(description), secret.String(), req.AllowedDomains)
	if err != nil || len(sources) != 2 || sources[0].ID != old.ID || sources[1].ID != prepared.Specification().ID {
		t.Fatal("existing source not preserved", err)
	}
	if _, err := prepared.Merge(sources, req.AllowedDomains, 3<<30); err != nil {
		t.Fatal("identical handoff was not idempotent", err)
	}
	if _, err := prepared.Merge([]ingress.RuntimeSource{old}, req.AllowedDomains, 2<<30); !errors.Is(err, ErrConflict) {
		t.Fatal("draining source capacity ignored", err)
	}
	old.Mode = "active"
	if _, err := prepared.Merge([]ingress.RuntimeSource{old}, req.AllowedDomains, 3<<30); !errors.Is(err, ErrConflict) {
		t.Fatal("overlapping active domains accepted", err)
	}
	sources[1].Credentials.Reader = strings.Repeat("f", 64)
	if _, err := prepared.Merge(sources, req.AllowedDomains, 3<<30); !errors.Is(err, ErrConflict) {
		t.Fatal("different source with same ID overwritten", err)
	}
	for _, repr := range []string{fmt.Sprintf("%+v", merged), fmt.Sprintf("%#v", merged), string(description)} {
		if strings.Contains(repr, req.RoutingToken) || strings.Contains(repr, old.Credentials.Reader) {
			t.Fatal("merged public output exposed credentials")
		}
	}
	old.Domains[0] = "changed.test"
	descriptions := merged.Specifications()
	descriptions[0].Domains[0] = "changed-again.test"
	if merged.Specifications()[0].Domains[0] != "example.test" {
		t.Fatal("external mutation changed merged configuration")
	}
}
