package runner

import (
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// ingressApp is a compute app whose leases declare HTTP ingress, with the
// deployment's base domain set and resource secrets materialized.
func ingressApp(t *testing.T, ingress map[string]map[string]any) *app {
	t.Helper()
	consumers := map[string]string{}
	for consumer := range ingress {
		consumers[consumer] = "anas-" + consumer + "-runners"
	}
	a := computeApp(t, consumers)
	for consumer, declaration := range ingress {
		if declaration != nil {
			declareHTTPPublication(a.reg[consumer].Resources[0].Spec, declaration)
			module := a.reg[consumer]
			module.Resources[0].HTTPRequestOwner = "65532:65532"
			a.reg[consumer] = module
		}
	}
	a.env["BASE_DOMAIN"] = "example.test"
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	return a
}

// declareHTTPPublication writes publish.http and the published ingress tier
// it requires (INCUS-R-132).
func declareHTTPPublication(spec map[string]any, declaration map[string]any) {
	spec["network"] = map[string]any{"ingress": "published"}
	spec["publish"] = map[string]any{"http": declaration}
}

func httpIngress(mode, prefix string, ports ...any) map[string]any {
	return map[string]any{"allowed_ports": ports, "domain": map[string]any{"mode": mode, "prefix": prefix}}
}

func frozenIngress(t *testing.T, a *app, consumer string) (*ResourceRequest, *computeingress.Authorization) {
	t.Helper()
	for i := range a.resourceRequests {
		if a.resourceRequests[i].Consumer == consumer {
			return &a.resourceRequests[i], a.resourceRequests[i].ComputeIngress
		}
	}
	t.Fatalf("no resource request for %s", consumer)
	return nil, nil
}

// INCUS-R-053/R-062: the declaration is frozen into the deployment with the
// lease it belongs to, and later drift of either side is refused.
func TestComputeIngressFreezesTheLeaseAuthorization(t *testing.T) {
	a := ingressApp(t, map[string]map[string]any{"forgejo": httpIngress("random", "ci", 8080, 80)})
	if err := a.prepareComputeIngress("dep-1"); err != nil {
		t.Fatal(err)
	}
	r, grant := frozenIngress(t, a, "forgejo")
	if grant == nil {
		t.Fatal("ingress declaration was not frozen")
	}
	want := computeingress.Authorization{
		Schema: computeingress.Schema, Deployment: "dep-1", Consumer: "forgejo", Resource: "runners",
		Provider: "incus", Interface: "incus_vm", Project: "anas-forgejo-runners", InstancePrefix: "anas-forgejo-",
		LeaseSecretRef: computeLeaseSecretKey("forgejo", "runners"), BaseDomain: "example.test",
		Policy: computeingress.Policy{AllowedPorts: []uint16{80, 8080}, Auth: "none", Domain: computeingress.Domain{Mode: "random", Prefix: "ci"}},
	}
	if grant.Deployment != want.Deployment || grant.Consumer != want.Consumer || grant.Resource != want.Resource ||
		grant.Provider != want.Provider || grant.Interface != want.Interface || grant.Project != want.Project ||
		grant.InstancePrefix != want.InstancePrefix || grant.LeaseSecretRef != want.LeaseSecretRef ||
		grant.BaseDomain != want.BaseDomain || grant.ForwardAuth != nil || grant.Schema != want.Schema ||
		!slices.Equal(grant.Policy.AllowedPorts, want.Policy.AllowedPorts) || grant.Policy.Auth != "none" || grant.Policy.Domain != want.Policy.Domain {
		t.Fatalf("frozen authorization = %+v", grant)
	}
	if err := validateFrozenComputeIngress(*r, "dep-1"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ResourceRequest){
		"other deployment": func(r *ResourceRequest) { r.ComputeIngress.Deployment = "dep-2" },
		"other consumer":   func(r *ResourceRequest) { r.ComputeIngress.Consumer = "other" },
		"other naming key": func(r *ResourceRequest) { r.LeaseSecretKey = computeLeaseSecretKey("forgejo", "other") },
		"other interface":  func(r *ResourceRequest) { r.ComputeIngress.Interface = "incus_container" },
		"widened ports":    func(r *ResourceRequest) { declareHTTPPublication(r.Spec, httpIngress("random", "ci", 80, 8080, 9090)) },
		"moved project":    func(r *ResourceRequest) { r.Spec["sandbox"] = "anas-other-runners" },
		"dropped decl":     func(r *ResourceRequest) { delete(r.Spec, "publish") },
		"auth added later": func(r *ResourceRequest) { r.ComputeIngress.Policy.Auth = "forward_auth" },
		"unsorted ports":   func(r *ResourceRequest) { r.ComputeIngress.Policy.AllowedPorts = []uint16{8080, 80} },
	} {
		t.Run(name, func(t *testing.T) {
			copied := *r
			copied.ComputeIngress = r.ComputeIngress.Clone()
			copied.Spec = map[string]any{}
			for key, value := range r.Spec {
				copied.Spec[key] = value
			}
			mutate(&copied)
			if err := validateFrozenComputeIngress(copied, "dep-1"); err == nil {
				t.Fatal("drifted HTTP authorization was accepted")
			}
		})
	}
}

func TestComputeWithoutIngressCarriesNoAuthorization(t *testing.T) {
	a := ingressApp(t, map[string]map[string]any{"forgejo": nil})
	if err := a.prepareComputeIngress("dep-1"); err != nil {
		t.Fatal(err)
	}
	r, grant := frozenIngress(t, a, "forgejo")
	if grant != nil {
		t.Fatal("a lease without ingress received an HTTP authorization")
	}
	if err := validateFrozenComputeIngress(*r, "dep-1"); err != nil {
		t.Fatal(err)
	}
	stray := *r
	stray.ComputeIngress = &computeingress.Authorization{Schema: computeingress.Schema, Deployment: "dep-1"}
	if err := validateFrozenComputeIngress(stray, "dep-1"); err == nil {
		t.Fatal("an authorization without an ingress declaration was accepted")
	}
	object := ResourceRequest{Consumer: "vikunja", ID: "files", Contract: "object_storage", ComputeIngress: stray.ComputeIngress}
	if err := validateFrozenComputeIngress(object, "dep-1"); err == nil {
		t.Fatal("an HTTP authorization on a non-compute resource was accepted")
	}
}

func TestComputeIngressRejectsInvalidDeclarations(t *testing.T) {
	for name, declaration := range map[string]map[string]any{
		"no ports":         httpIngress("fixed", "ci"),
		"port zero":        httpIngress("fixed", "ci", 0),
		"duplicate port":   httpIngress("fixed", "ci", 80, 80),
		"unknown auth":     {"allowed_ports": []any{80}, "auth": "basic", "domain": map[string]any{"mode": "fixed", "prefix": "ci"}},
		"unknown mode":     httpIngress("wildcard", "ci", 80),
		"uppercase prefix": httpIngress("fixed", "CI", 80),
		"long random":      httpIngress("random", strings.Repeat("a", 31), 80),
		"unknown field":    {"allowed_ports": []any{80}, "domain": map[string]any{"mode": "fixed", "prefix": "ci"}, "tls": "off"},
	} {
		t.Run(name, func(t *testing.T) {
			a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners"})
			declareHTTPPublication(a.reg["forgejo"].Resources[0].Spec, declaration)
			a.reg["forgejo"].Resources[0].HTTPRequestOwner = "65532:65532"
			a.env["BASE_DOMAIN"] = "example.test"
			// Either the spec gate or the freeze must refuse it.
			if err := a.materializeResourceSecrets(); err == nil {
				if err := a.prepareComputeIngress("dep-1"); err == nil {
					t.Fatal("invalid ingress declaration was frozen")
				}
			}
		})
	}
	a := ingressApp(t, map[string]map[string]any{"forgejo": httpIngress("fixed", "ci", 80)})
	a.env["BASE_DOMAIN"] = "localhost"
	if err := a.prepareComputeIngress("dep-1"); err == nil {
		t.Fatal("ingress was frozen without a usable base domain")
	}
}

// INCUS-R-095: forward_auth binds the middleware its resolved provider owns;
// neither the consumer nor an unrelated module can supply it.
func TestComputeIngressForwardAuthNeedsItsBoundProvider(t *testing.T) {
	declaration := map[string]any{"allowed_ports": []any{80}, "auth": "forward_auth", "domain": map[string]any{"mode": "named", "prefix": "ci"}}
	setup := func(t *testing.T) *app {
		a := ingressApp(t, map[string]map[string]any{"forgejo": declaration})
		a.reg["authentik"] = Module{Name: "authentik", Provides: []ProvidedCapability{{Name: capabilityForwardAuth, Interfaces: []string{interfaceHTTP}}}}
		a.order = append(a.order, "authentik")
		a.resolvedBindings["forgejo"][capabilityForwardAuth] = "authentik"
		a.resolvedBindings["forgejo"][capabilityForwardAuth+".interface"] = interfaceHTTP
		for key, value := range map[string]string{"ANAS_FORWARD_AUTH_MIDDLEWARE": "authentik-forward-auth@file", "ANAS_FORWARD_AUTH_PROVIDER": "authentik"} {
			a.env[key], a.envOwner[key] = value, "authentik"
		}
		return a
	}
	a := setup(t)
	if err := a.prepareComputeIngress("dep-1"); err != nil {
		t.Fatal(err)
	}
	if _, grant := frozenIngress(t, a, "forgejo"); grant.ForwardAuth == nil || grant.ForwardAuth.Provider != "authentik" || grant.ForwardAuth.Middleware != "authentik-forward-auth@file" {
		t.Fatalf("forward_auth was not frozen from the bound provider: %+v", grant.ForwardAuth)
	}
	for name, mutate := range map[string]func(*app){
		"no binding":               func(a *app) { delete(a.resolvedBindings["forgejo"], capabilityForwardAuth) },
		"provider not deployed":    func(a *app) { a.order = a.order[:len(a.order)-1] },
		"provider lacks http":      func(a *app) { a.reg["authentik"].Provides[0].Interfaces = []string{"oidc"} },
		"middleware from consumer": func(a *app) { a.envOwner["ANAS_FORWARD_AUTH_MIDDLEWARE"] = "forgejo" },
		"provider env from other":  func(a *app) { a.env["ANAS_FORWARD_AUTH_PROVIDER"] = "forgejo" },
	} {
		t.Run(name, func(t *testing.T) {
			a := setup(t)
			mutate(a)
			if err := a.prepareComputeIngress("dep-1"); err == nil {
				t.Fatal("forward_auth was frozen without its bound provider's middleware")
			}
		})
	}
}

// INCUS-R-087: lease namespaces may not shadow each other or a deployment
// service, and an opaque file-provider rule cannot be proven disjoint.
func TestComputeIngressNamespacesCannotShadowDeploymentDomains(t *testing.T) {
	a := ingressApp(t, map[string]map[string]any{"forgejo": httpIngress("random", "ci", 80), "agent": httpIngress("named", "ci-agent", 80)})
	if err := a.prepareComputeIngress("dep-1"); err == nil {
		t.Fatal("a random namespace was allowed to contain another lease's names")
	}
	for name, env := range map[string]map[string]string{
		"module domain":     {"VIKUNJA_DOMAIN": "ci.example.test"},
		"literal route":     {"ANAS_TRAEFIK_ROUTE__GRAFANA__RULE": "Host(`ci-7f3a.example.test`)"},
		"opaque route":      {"ANAS_TRAEFIK_ROUTE__GRAFANA__RULE": "HostRegexp(`.+`)"},
		"uppercase literal": {"ANAS_TRAEFIK_ROUTE__GRAFANA__RULE": "Host(`CI.example.test`)"},
	} {
		t.Run(name, func(t *testing.T) {
			a := ingressApp(t, map[string]map[string]any{"forgejo": httpIngress("random", "ci", 80)})
			a.reg["vikunja"] = Module{Name: "vikunja", PublishesDomain: true}
			a.order = append(a.order, "vikunja")
			for key, value := range env {
				a.env[key] = value
			}
			if err := a.prepareComputeIngress("dep-1"); err == nil {
				t.Fatal("lease namespace overlapped a deployment service domain")
			}
		})
	}
	disjoint := ingressApp(t, map[string]map[string]any{"forgejo": httpIngress("fixed", "ci", 80), "agent": httpIngress("random", "agent", 80)})
	disjoint.reg["vikunja"] = Module{Name: "vikunja", PublishesDomain: true}
	disjoint.order = append(disjoint.order, "vikunja")
	disjoint.env["VIKUNJA_DOMAIN"] = "tasks.example.test"
	disjoint.env["ANAS_TRAEFIK_ROUTE__GRAFANA__RULE"] = "Host(`ci-7f3a.example.test`)"
	if err := disjoint.prepareComputeIngress("dep-1"); err != nil {
		t.Fatal("disjoint fixed/random namespaces and literal routes were refused", err)
	}
}
