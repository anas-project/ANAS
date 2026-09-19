package computeingress

import (
	"bytes"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
)

func policyFixture() (*Authorization, Request, Facts) {
	a := &Authorization{Schema: Schema, Deployment: "deployment-one", Consumer: "forgejo", Resource: "runners", Provider: "incus", Interface: "incus_vm", Project: "anas-forgejo-runners", InstancePrefix: "anas-fj-", LeaseSecretRef: "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET", BaseDomain: "example.test", Policy: Policy{AllowedPorts: []uint16{7000}, Auth: "none", Domain: Domain{Mode: "random", Prefix: "ci"}}}
	r := Request{Action: "publish", InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
	f := Facts{Project: a.Project, Interface: a.Interface, InstanceID: r.InstanceID, InstanceUUID: "uuid-one", State: "Running", NetworkOwner: a.Consumer, GuestIP: "10.42.0.2", AllocationIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03", AllocationMAC: "00:16:3e:01:02:03"}
	return a, r, f
}

func policyFixtureSecret() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

// INCUS-R-064/R-065/R-070/R-071: a stable workload has a stable per-lease
// HMAC name. Host prediction is deliberately not an authorization mechanism.
func TestPolicyNameDerivationAndLabelBoundaries(t *testing.T) {
	a, _, _ := policyFixture()
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
	const valid = `{"action":"publish","instance_id":"anas-fj-job1","workload_id":"job:123","guest_port":7000}`
	if _, err := ParseRequest([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"auth downgrade":    strings.Replace(valid, `"action":`, `"auth":"none","action":`, 1),
		"target URL":        strings.Replace(valid, `"action":`, `"url":"http://127.0.0.1/","action":`, 1),
		"lease claim":       strings.Replace(valid, `"action":`, `"project":"default","action":`, 1),
		"duplicate":         strings.Replace(valid, `"guest_port":7000`, `"guest_port":7000,"guest_port":22`, 1),
		"escaped duplicate": strings.Replace(valid, `"guest_port":7000`, `"guest_port":7000,"guest\u005fport":22`, 1),
		"case alias":        strings.Replace(valid, `"guest_port"`, `"Guest_Port"`, 1),
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
		t.Fatalf("omitted ingress: %v", err)
	}
	for _, raw := range []any{nil, map[string]any{}, "", []any{}} {
		if _, err := ParseSpec(map[string]any{"ingress": raw}); err == nil {
			t.Fatal("empty or null ingress accepted")
		}
	}
}

func TestHTTPNamespacesCannotOverlapLeasesOrExistingServices(t *testing.T) {
	a, _, _ := policyFixture()
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

func TestPlannerEnforcesObservedAllocationAndFrozenAuthentication(t *testing.T) {
	a, r, f := policyFixture()
	a.Policy.Auth = "forward_auth"
	a.ForwardAuth = &ForwardAuth{Provider: "keycloak", Middleware: "auth@file"}
	for name, mutate := range map[string]func(*Facts){
		"wrong project":        func(f *Facts) { f.Project = "default" },
		"wrong interface":      func(f *Facts) { f.Interface = "incus_container" },
		"wrong owner":          func(f *Facts) { f.NetworkOwner = "other" },
		"stopped":              func(f *Facts) { f.State = "Stopped" },
		"different allocation": func(f *Facts) { f.AllocationIP = "10.42.0.3" },
		"different MAC":        func(f *Facts) { f.AllocationMAC = "00:16:3e:01:02:04" },
		"loopback":             func(f *Facts) { f.GuestIP, f.AllocationIP = "127.0.0.1", "127.0.0.1" },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := NewPlanner(a.Deployment, []*Authorization{a}, nil)
			if err != nil {
				t.Fatal(err)
			}
			facts := f
			mutate(&facts)
			if _, err := p.Reserve(Lease{a.Consumer, a.Resource}, r, facts, policyFixtureSecret()); err == nil {
				t.Fatal("unobserved target accepted")
			}
		})
	}
	p, err := NewPlanner(a.Deployment, []*Authorization{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the source declaration after freezing cannot turn auth off.
	a.Policy.Auth, a.ForwardAuth = "none", nil
	pub, err := p.Reserve(Lease{a.Consumer, a.Resource}, r, f, policyFixtureSecret())
	if err != nil || pub.Auth != "forward_auth" || pub.Middleware != "auth@file" {
		t.Fatalf("frozen auth changed: %#v %v", pub, err)
	}
	r.GuestPort = 22
	if _, err := p.Reserve(Lease{a.Consumer, a.Resource}, r, f, policyFixtureSecret()); err == nil {
		t.Fatal("port outside allowlist accepted")
	}
}

func TestPlannerSerializesReservationsAndRejectsStaleRetirement(t *testing.T) {
	a, r, f := policyFixture()
	p, err := NewPlanner(a.Deployment, []*Authorization{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease := Lease{a.Consumer, a.Resource}
	first, err := p.Reserve(lease, r, f, policyFixtureSecret())
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 32 {
		wait.Go(func() {
			pub, err := p.Reserve(lease, r, f, policyFixtureSecret())
			if err != nil || pub != first {
				t.Errorf("idempotent reservation changed: %v", err)
			}
		})
	}
	wait.Wait()
	if err := p.Retire(first); err != nil {
		t.Fatal(err)
	}
	second, err := p.Reserve(lease, r, f, policyFixtureSecret())
	if err != nil || second.Reservation == first.Reservation {
		t.Fatal("retired token reused")
	}
	if err := p.Retire(first); err == nil {
		t.Fatal("old completion cleared new reservation")
	}
	r.Action = "revoke"
	withdraw, found, err := p.Withdrawal(lease, r)
	if err != nil || !found || withdraw != second {
		t.Fatalf("withdrawal: %v", err)
	}
	r.WorkloadID = "other-job"
	if _, _, err := p.Withdrawal(lease, r); err == nil {
		t.Fatal("revoke accepted different workload identity")
	}
}
