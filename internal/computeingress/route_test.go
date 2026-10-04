package computeingress

import (
	"strings"
	"testing"
)

func TestRouteFilesAreLeaseScopedAndDeploymentIndependent(t *testing.T) {
	a, _ := policyFixture()
	name := RouteFileName(a, "ci-abc.example.test")
	consumer, resource, grant, ok := ParseRouteFileName(name)
	if !ok || consumer != "forgejo" || resource != "runners" || grant != GrantDigest(a) {
		t.Fatalf("route file %s parsed as %s %s %s %v", name, consumer, resource, grant, ok)
	}
	redeployed := a.Clone()
	redeployed.Deployment = "deployment-two"
	if GrantDigest(redeployed) != GrantDigest(a) {
		t.Fatal("an unchanged declaration lost its routes on redeploy")
	}
	widened := a.Clone()
	widened.Policy.AllowedPorts = []uint16{7000, 8080}
	if GrantDigest(widened) == GrantDigest(a) {
		t.Fatal("a changed declaration kept its routes")
	}
	for _, bad := range []string{"routes.yml", "cert.yml", "../x.yml", "forgejo.runners.yml", strings.Replace(name, ".yml", ".yaml", 1)} {
		if _, _, _, ok := ParseRouteFileName(bad); ok {
			t.Fatalf("%s parsed as a route file", bad)
		}
	}
}

// INCUS-R-054, R-087, R-095: the route is the existing router/service shape,
// with entrypoint and middleware from the frozen grant only.
func TestRenderRouteUsesOnlyTheFrozenGrant(t *testing.T) {
	a, _ := policyFixture()
	host, _ := a.HostForLabel("0123456789abcdef0123456789abcdef")
	body, err := RenderRoute(a, host, "10.101.0.17", 7000)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"rule: \"Host(`" + host + "`)\"", `- "https"`, "tls: {}", `url: "http://10.101.0.17:7000"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("route lacks %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "middlewares") {
		t.Fatal("auth none rendered a middleware")
	}
	a.Policy.Auth = "forward_auth"
	a.ForwardAuth = &ForwardAuth{Provider: "authentik", Middleware: "authentik-forward-auth@file"}
	body, err = RenderRoute(a, host, "10.101.0.17", 7000)
	if err != nil || !strings.Contains(string(body), `- "authentik-forward-auth@file"`) {
		t.Fatalf("forward_auth route = %s, %v", body, err)
	}
	if _, err := RenderRoute(a, "nextcloud.example.test", "10.101.0.17", 7000); err == nil {
		t.Fatal("rendered a host outside the lease namespace")
	}
	if _, err := RenderRoute(a, host, "fd42::17", 7000); err == nil {
		t.Fatal("rendered an IPv6 backend")
	}
}

// INCUS-R-144: only an instance address of the lease subnet is a backend.
func TestBackendMustBeAnInstanceAddressOfTheLease(t *testing.T) {
	if err := ValidateBackend("10.101.0.17", "10.101.0.0/24", "10.101.0.1"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"10.101.0.0", "10.101.0.1", "10.101.0.255", "10.102.0.17", "172.30.0.2", "127.0.0.1", "fd42::17"} {
		if err := ValidateBackend(address, "10.101.0.0/24", "10.101.0.1"); err == nil {
			t.Errorf("%s accepted as a backend", address)
		}
	}
}
