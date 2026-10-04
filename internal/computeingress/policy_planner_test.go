package computeingress

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func policyFixture() (*Authorization, Request) {
	a := &Authorization{Schema: Schema, Deployment: "deployment-one", Consumer: "forgejo", Resource: "runners", Provider: "incus", Interface: "incus_vm", Project: "anas-forgejo-runners", InstancePrefix: "anas-fj-", LeaseSecretRef: "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET", BaseDomain: "example.test", Policy: Policy{AllowedPorts: []uint16{7000}, Auth: "none", Domain: Domain{Mode: "random", Prefix: "ci"}}}
	r := Request{Instance: "anas-fj-job1", Address: "10.101.0.17", Port: 7000}
	return a, r
}

func policyFixtureSecret() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

// INCUS-R-064/R-065/R-070/R-071: a stable workload has a stable per-lease
// HMAC name. Host prediction is deliberately not an authorization mechanism.
func TestPolicyNameDerivationAndLabelBoundaries(t *testing.T) {
	a, _ := policyFixture()
	host, err := a.Host("job:123", "", policyFixtureSecret())
	if err != nil || host != "ci-c365aa17bb5855aee4f588b7170c9240.example.test" {
		t.Fatalf("unexpected HMAC name: %s, %v", host, err)
	}
	for _, secret := range []string{"", "not-base64", policyFixtureSecret() + "\n", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := a.Host("job:123", "", secret); err == nil {
			t.Fatal("accepted a noncanonical naming key")
		}
	}
	other, err := a.Host("job:124", "", policyFixtureSecret())
	if err != nil || other == host {
		t.Fatal("different workload reused a name")
	}
	if _, err := a.Host("job:123", "api", policyFixtureSecret()); err == nil {
		t.Fatal("random mode accepted caller label")
	}
	a.Policy.Domain.Mode = "fixed"
	if host, err := a.Host("job:123", "", ""); err != nil || host != "ci.example.test" {
		t.Fatalf("fixed mode: %v", err)
	}
	a.Policy.Domain.Mode = "named"
	if host, err := a.Host("job:123", "api", ""); err != nil || host != "ci-api.example.test" {
		t.Fatalf("named mode: %v", err)
	}
	for _, label := range []string{"", "API", "admin.example.test", "-admin", "admin-", strings.Repeat("x", 61)} {
		if _, err := a.Host("job:123", label, ""); err == nil {
			t.Fatalf("unsafe label accepted: %q", label)
		}
	}
}

// INCUS-R-086/R-088/R-095: bypassing the consumer library does not add any
// target IP, origin, auth, middleware or lease identity to the request schema.
func TestHTTPRequestsRejectPrivilegeAndParserAmbiguity(t *testing.T) {
	const valid = `{"instance":"anas-fj-job1","address":"10.101.0.17","port":7000}`
	if _, err := ParseRequest([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"auth downgrade":    strings.Replace(valid, `"instance":`, `"auth":"none","instance":`, 1),
		"target URL":        strings.Replace(valid, `"instance":`, `"url":"http://127.0.0.1/","instance":`, 1),
		"lease claim":       strings.Replace(valid, `"instance":`, `"project":"default","instance":`, 1),
		"duplicate":         strings.Replace(valid, `"port":7000`, `"port":7000,"port":22`, 1),
		"escaped duplicate": strings.Replace(valid, `"port":7000`, `"port":7000,"p\u006frt":22`, 1),
		"case alias":        strings.Replace(valid, `"port"`, `"Port"`, 1),
		"IPv6 target":       strings.Replace(valid, `10.101.0.17`, `fd42::17`, 1),
		"host name target":  strings.Replace(valid, `10.101.0.17`, `example.test`, 1),
		"padded address":    strings.Replace(valid, `10.101.0.17`, `010.101.0.17`, 1),
		"null":              strings.Replace(valid, "7000", "null", 1),
		"zero":              strings.Replace(valid, "7000", "0", 1),
		"fraction":          strings.Replace(valid, "7000", "7.5", 1),
		"overflow":          strings.Replace(valid, "7000", "65536", 1),
		"trailing":          valid + `{}`, "oversize": valid + strings.Repeat(" ", MaxRequestBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRequest([]byte(body)); err == nil {
				t.Fatal("untrusted request accepted")
			}
		})
	}
	if policy, err := ParseSpec(map[string]any{}); err != nil || policy != nil {
		t.Fatalf("omitted publish: %v", err)
	}
	if policy, err := ParseSpec(map[string]any{"publish": map[string]any{"ports": []any{}}}); err != nil || policy != nil {
		t.Fatalf("publish without http: %v", err)
	}
	for _, raw := range []any{nil, map[string]any{}, "", []any{}} {
		if _, err := ParseSpec(map[string]any{"publish": map[string]any{"http": raw}}); err == nil {
			t.Fatal("empty or null publish.http accepted")
		}
	}
	if _, err := ParseSpec(map[string]any{"publish": "http"}); err == nil {
		t.Fatal("non-object publish accepted")
	}
}

func TestHTTPNamespacesCannotOverlapLeasesOrExistingServices(t *testing.T) {
	a, _ := policyFixture()
	b := a.Clone()
	b.Consumer, b.Resource, b.LeaseSecretRef = "ai_agent", "workers", "ANAS_COMPUTE_RESOURCE__AI_AGENT__WORKERS__LEASE_SECRET"
	b.Policy.Domain = Domain{Mode: "fixed", Prefix: "ci-admin"}
	if err := ValidateNamespaces([]*Authorization{a, b}, nil); err == nil {
		t.Fatal("overlapping namespaces accepted")
	}
	if err := ValidateNamespaces([]*Authorization{a}, []string{"ci-preview.example.test"}); err == nil {
		t.Fatal("existing service shadowed")
	}
	b.Policy.Domain.Prefix = "agent"
	if err := ValidateNamespaces([]*Authorization{a, b}, []string{"nextcloud.example.test"}); err != nil {
		t.Fatal(err)
	}
}

// INCUS-R-065, R-086, R-087: the consumer's derived label names exactly the
// host it predicted, and the mediator, which holds no key, only ever returns
// hosts inside the lease namespace.
func TestMediatorNamingMatchesConsumerPrediction(t *testing.T) {
	a, _ := policyFixture()
	label, err := a.Policy.RandomLabel("job:123", policyFixtureSecret())
	if err != nil {
		t.Fatal(err)
	}
	predicted, _ := a.Host("job:123", "", policyFixtureSecret())
	if host, err := a.HostForLabel(label); err != nil || host != predicted {
		t.Fatalf("mediator host %q, consumer predicted %q: %v", host, predicted, err)
	}
	for _, bad := range []string{"", "api", strings.ToUpper(label), label + "0", "x." + label} {
		if _, err := a.HostForLabel(bad); err == nil {
			t.Fatalf("random mode accepted label %q", bad)
		}
	}
	a.Policy.Domain.Mode = "named"
	if host, err := a.HostForLabel("api"); err != nil || host != "ci-api.example.test" {
		t.Fatalf("named host = %q, %v", host, err)
	}
	if _, err := a.HostForLabel("API.evil"); err == nil {
		t.Fatal("named mode accepted a label that leaves the namespace")
	}
	a.Policy.Domain.Mode = "fixed"
	if host, err := a.HostForLabel(""); err != nil || host != "ci.example.test" {
		t.Fatalf("fixed host = %q, %v", host, err)
	}
	if _, err := a.HostForLabel("api"); err == nil {
		t.Fatal("fixed mode accepted a label")
	}
	if _, err := a.Policy.RandomLabel("job:123", policyFixtureSecret()); err == nil {
		t.Fatal("a fixed policy derived a random label")
	}
}
