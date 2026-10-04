package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

type mediatorFixture struct {
	base, id string
	grant    *computeingress.Authorization
	manifest *deployment.Manifest
	requests map[string]computeingress.Request
}

func (f *mediatorFixture) reconcile(t *testing.T) ComputeHTTPReconcileResult {
	t.Helper()
	result, err := reconcileComputeHTTPRoutes(f.base, f.manifest, []*computeingress.Authorization{f.grant})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f *mediatorFixture) dynamic() string {
	return filepath.Join(f.base, "runtime-state", "deployments", f.id, "traefik", "dynamic")
}

func (f *mediatorFixture) routes(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(f.dynamic(), computeingress.RouteDirectory))
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(f.dynamic(), computeingress.RouteDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = string(body)
	}
	return out
}

func newMediatorFixture(t *testing.T, mode string) *mediatorFixture {
	t.Helper()
	f := &mediatorFixture{base: filepath.Join(t.TempDir(), ".anas"), id: "20261003T000000Z-00000001", requests: map[string]computeingress.Request{}}
	f.grant = &computeingress.Authorization{Schema: computeingress.Schema, Deployment: f.id, Consumer: "forgejo", Resource: "runners", Provider: "incus",
		Interface: "incus_container", Project: "anas-forgejo-runners", InstancePrefix: "anas-fj-",
		LeaseSecretRef: "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET", BaseDomain: "example.test",
		Policy: computeingress.Policy{AllowedPorts: []uint16{7000}, Auth: "none", Domain: computeingress.Domain{Mode: mode, Prefix: "ci"}}}
	f.manifest = &deploymentManifest{APIVersion: deploymentAPIVersion, ID: f.id,
		Modules:   map[string]deploymentModule{"traefik": {Name: "traefik", ArtifactDeployment: f.id}},
		Resources: []deploymentResource{{Consumer: "forgejo", ID: "runners", Contract: "compute", ComputeIngress: f.grant}}}
	for _, dir := range []string{f.dynamic(), filepath.Join(f.base, "state", "resources")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	state := resourceState{APIVersion: resourceStateAPIVersion, Consumer: "forgejo", ResourceID: "runners", Contract: "compute", Status: "ready",
		Actual: resourceActual{ComputeNetwork: &computeNetworkState{Bridge: "lease120067207a", IPv4Subnet: "10.101.0.0/24", IPv4Gateway: "10.101.0.1"}}}
	if err := writeYAMLAtomic(filepath.Join(f.base, "state", "resources", "forgejo.runners.yml"), state, 0600); err != nil {
		t.Fatal(err)
	}
	previous := readComputeHTTPRequests
	t.Cleanup(func() { readComputeHTTPRequests = previous })
	readComputeHTTPRequests = func(dir string) (map[string]computeingress.Request, error) {
		if dir != computeHTTPRequestDir(f.base, "forgejo", "runners") {
			t.Fatalf("read requests from %s", dir)
		}
		return f.requests, nil
	}
	return f
}

// INCUS-R-144, R-145, R-149: a valid request becomes one route, a request
// that fails any check is skipped, a stale route is removed, and the route
// already published keeps its host.
func TestMediatorPublishesOnlyValidRequests(t *testing.T) {
	f := newMediatorFixture(t, "named")
	f.requests["http-a.json"] = computeingress.Request{Instance: "anas-fj-web", Address: "10.101.0.17", Port: 7000, Label: "web"}
	f.requests["http-port.json"] = computeingress.Request{Instance: "anas-fj-web", Address: "10.101.0.17", Port: 8080, Label: "admin"}
	f.requests["http-subnet.json"] = computeingress.Request{Instance: "anas-fj-web", Address: "172.30.0.2", Port: 7000, Label: "traefik"}
	f.requests["http-gateway.json"] = computeingress.Request{Instance: "anas-fj-web", Address: "10.101.0.1", Port: 7000, Label: "gw"}
	f.requests["http-prefix.json"] = computeingress.Request{Instance: "anas-other-web", Address: "10.101.0.18", Port: 7000, Label: "other"}
	f.requests["http-label.json"] = computeingress.Request{Instance: "anas-fj-web", Address: "10.101.0.17", Port: 7000}
	result := f.reconcile(t)
	routes := f.routes(t)
	if result.Published != 1 || len(routes) != 1 || len(result.Rejected) != 5 || !result.Changed {
		t.Fatalf("result = %+v, routes = %v", result, routes)
	}
	for _, body := range routes {
		if !strings.Contains(body, "Host(`ci-web.example.test`)") || !strings.Contains(body, "http://10.101.0.17:7000") {
			t.Fatalf("route = %s", body)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dynamic(), computeingress.RouteReloadFile)); err != nil {
		t.Fatal("Traefik was not nudged to reread its routes")
	}

	// A second request for the same host from another instance loses to the
	// published one, whatever its file name.
	f.requests["http-0.json"] = computeingress.Request{Instance: "anas-fj-other", Address: "10.101.0.20", Port: 7000, Label: "web"}
	result = f.reconcile(t)
	if result.Changed || !strings.Contains(strings.Join(result.Rejected, ";"), "already published") {
		t.Fatalf("conflict = %+v", result)
	}
	for _, body := range f.routes(t) {
		if !strings.Contains(body, "10.101.0.17") {
			t.Fatal("a second request took over a published host")
		}
	}

	// Withdrawing the request removes the route on the next pass.
	f.requests = map[string]computeingress.Request{}
	result = f.reconcile(t)
	if !result.Changed || len(f.routes(t)) != 0 {
		t.Fatalf("withdrawal = %+v, %v", result, f.routes(t))
	}
}

// INCUS-R-065: a random-mode request is published under the label the
// consumer derived; a label of the wrong shape is refused.
func TestMediatorRandomLabels(t *testing.T) {
	f := newMediatorFixture(t, "random")
	f.requests["http-a.json"] = computeingress.Request{Instance: "anas-fj-job1", Address: "10.101.0.17", Port: 7000, Label: "0123456789abcdef0123456789abcdef"}
	f.requests["http-b.json"] = computeingress.Request{Instance: "anas-fj-job2", Address: "10.101.0.18", Port: 7000, Label: "admin"}
	if result := f.reconcile(t); result.Published != 1 || len(result.Rejected) != 1 {
		t.Fatalf("random = %+v", result)
	}
}

// INCUS-R-147: activation removes the routes of a removed or changed grant
// and keeps those of an unchanged one, in the Traefik directory in use.
func TestActivationPrunesRoutesOfChangedGrants(t *testing.T) {
	f := newMediatorFixture(t, "named")
	f.requests["http-a.json"] = computeingress.Request{Instance: "anas-fj-web", Address: "10.101.0.17", Port: 7000, Label: "web"}
	f.reconcile(t)
	current := f.manifest
	unchanged := *current
	unchanged.ID = "20261003T000000Z-00000002"
	unchanged.Resources = []deploymentResource{current.Resources[0]}
	unchanged.Resources[0].ComputeIngress = f.grant.Clone()
	unchanged.Resources[0].ComputeIngress.Deployment = unchanged.ID
	if err := pruneComputeHTTPRoutes(f.base, current, &unchanged); err != nil || len(f.routes(t)) != 1 {
		t.Fatalf("an unchanged grant lost its route: %v %v", err, f.routes(t))
	}
	changed := unchanged
	changed.Resources = []deploymentResource{unchanged.Resources[0]}
	changed.Resources[0].ComputeIngress = f.grant.Clone()
	changed.Resources[0].ComputeIngress.Policy.Auth = "forward_auth"
	changed.Resources[0].ComputeIngress.ForwardAuth = &computeingress.ForwardAuth{Provider: "authentik", Middleware: "authentik-forward-auth@file"}
	if err := pruneComputeHTTPRoutes(f.base, current, &changed); err != nil || len(f.routes(t)) != 0 {
		t.Fatalf("a changed grant kept its route: %v %v", err, f.routes(t))
	}
	if err := pruneComputeHTTPRoutes(f.base, current, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPRequestDirectoryIsPrivateToTheConsumer(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	request := ResourceRequest{Consumer: "forgejo", ID: "runners", Contract: "compute"}
	if err := a.prepareComputeHTTPRequestDir(request); err == nil {
		t.Fatal("a request directory was created without a declared owner")
	}
	module := a.reg["forgejo"]
	module.Resources[0].HTTPRequestOwner = "65532:65532"
	a.reg["forgejo"] = module
	if err := a.prepareComputeHTTPRequestDir(request); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(computeHTTPRequestDir(a.base, "forgejo", "runners"))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("request directory = %v, %v", info, err)
	}
}
