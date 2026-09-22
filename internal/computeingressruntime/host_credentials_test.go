package computeingressruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"gopkg.in/yaml.v3"
)

// Actual frozen Core metadata, registry, and private file delivery. There is
// no live deployment, daemon, mount or production authorization in this fixture.
func hostDeliveryFixture(t *testing.T, random bool) (*incusReaderFixture, string, string, string, *deployment.HTTPAuthorizationSnapshot, ReaderInstallation, map[computeingress.Lease]string) {
	t.Helper()
	f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
	grant := f.grant.Clone()
	keys := map[computeingress.Lease]string{}
	if random {
		grant.Policy.Domain.Mode = "random"
		keys[computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	}
	workspace := t.TempDir()
	write := func(relative string, value any) {
		t.Helper()
		path := filepath.Join(workspace, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body, err := yaml.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	write(".anas/state/active.yml", deployment.ActiveState{APIVersion: deployment.StateAPIVersion, ActiveDeployment: grant.Deployment, RuntimeStatus: "running", ActivatedAt: at})
	write(".anas/state/deployments/"+grant.Deployment+".yml", deployment.State{APIVersion: deployment.StateAPIVersion, ID: grant.Deployment, Status: "active", ActivatedAt: at})
	if err := os.WriteFile(filepath.Join(workspace, ".anas/state/lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	refs := []computeimage.Reference{{Fingerprint: strings.Repeat("f", 64)}}
	images, err := computeimage.Freeze(refs, computeimage.Target{Architecture: "amd64", Interface: grant.Interface}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest := deployment.Manifest{APIVersion: deployment.ManifestAPIVersion, ID: grant.Deployment,
		ModuleOrder: []string{grant.Provider, grant.Consumer}, Modules: map[string]deployment.Module{grant.Provider: {Name: grant.Provider}, grant.Consumer: {Name: grant.Consumer}},
		Bindings: map[string]map[string]string{grant.Consumer: {"compute": grant.Provider, "compute.interface": grant.Interface}},
		Resources: []deployment.Resource{{Contract: "compute", Consumer: grant.Consumer, ID: grant.Resource, Provider: grant.Provider, Interface: grant.Interface,
			Spec:          map[string]any{"sandbox": grant.Project, "instance_prefix": grant.InstancePrefix, "ingress": grant.Policy, "image_allowlist": refs},
			ComputeImages: images, ComputeIngress: grant, LeaseSecretKey: grant.LeaseSecretRef}}}
	write(".anas/deployments/"+grant.Deployment+"/deployment.yml", manifest)
	scope, err := deployment.NewReader(workspace).HTTPAuthorizations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(workspace, "requests")
	if err := Register(context.Background(), workspace, registry, scope.Epoch); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(workspace, "reader-private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	installation := ReaderInstallation{Host: &HostReaderBinding{ScopeID: "main", ServerUUID: "11111111-1111-4111-8111-111111111111"},
		Traefik: TraefikReaderConfig{Endpoint: "https://traefik.example.test", ServerCertPEM: f.config.ServerCertPEM, Username: "fixture", Password: "test-only", APIRouter: "api@file"}}
	return f, workspace, registry, filepath.Join(private, "readers.json"), scope, installation, keys
}

func fixtureHostClient(grant *computeingress.Authorization, calls *int, after func()) incusingresshost.ProjectionClient {
	return incusingresshost.ProjectionClient{Invoker: projectedInvokerFunc(func(ctx context.Context, _ string, body []byte) ([]byte, error) {
		*calls++
		var req incusingresshost.ProjectionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		response := incusingresshost.ProjectionResponse{Schema: req.Schema, ObservationID: req.ObservationID, ScopeID: req.ScopeID, Epoch: req.Epoch, Deployment: req.Deployment,
			ServerUUID: "11111111-1111-4111-8111-111111111111",
			Authorized: []incusingresshost.AuthorizedHTTPLease{{Lease: req.Lease, ResourceID: "compute." + grant.Resource, Project: grant.Project, Interface: grant.Interface,
				InstancePrefix: grant.InstancePrefix, AllowedPorts: grant.Policy.AllowedPorts, Auth: grant.Policy.Auth}},
			Identity: incusingresshost.ProjectionHTTPIdentity{Lease: req.Lease, InstanceID: req.InstanceID, WorkloadID: req.WorkloadID,
				InstanceUUID: "22222222-2222-4222-8222-222222222222", Incarnation: strings.Repeat("c", 64), State: "Running", GuestIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03",
				HostVethName: "vethguest0", HostVethMAC: "02:00:00:00:00:10", HostVethPeerIfIndex: 77, GuestPort: req.GuestPort}}
		if after != nil {
			after()
		}
		return json.Marshal(response)
	})}
}

func TestHostReaderDeliveryOmitsIncusCredentialsAndAssemblesCurrentWorkspace(t *testing.T) {
	f, workspace, registry, path, scope, installation, keys := hostDeliveryFixture(t, true)
	ctx := context.Background()
	if err := WriteReaderCredentials(ctx, path, scope, installation, keys, func(c context.Context) error { return StillCurrent(c, workspace, scope) }); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields["incus"] != nil || fields["host_observer"] == nil {
		t.Fatal("host artifact includes Incus credentials")
	}
	if mode, err := os.Stat(path); err != nil || mode.Mode().Perm() != 0400 {
		t.Fatal("private permissions", err)
	}
	installation.Host.ScopeID = "changed-after-delivery"
	grant := scope.Authorizations[0]
	calls := 0
	// Launch admission now verifies actual installed paths, not placeholders.
	renderer := FileRouteRenderer{Directory: t.TempDir(), Entrypoint: filepath.Join(t.TempDir(), "anas-entrypoint.sh")}
	script, err := os.ReadFile("../../modules/traefik/traefik/anas-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(renderer.Entrypoint, script, 0500); err != nil {
		t.Fatal(err)
	}
	client := fixtureHostClient(grant, &calls, nil)
	readers, err := OpenHostWorkspaceReaders(ctx, workspace, registry, path, renderer, client)
	if err != nil {
		t.Fatal(err)
	}
	defer readers.Close()
	if readers.incus != nil {
		t.Fatal("host mode constructed direct Incus reader")
	}
	if _, ok := readers.Source.Facts.(*HostProjectionReader); !ok {
		t.Fatal("host reader not wired")
	}
	if err := readers.Source.Configuration(ctx, scope); err != nil {
		t.Fatal(err)
	}
	lease := computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}
	if value, err := readers.Source.NamingKey(ctx, scope, lease); err != nil || value != keys[lease] {
		t.Fatal("narrow naming-key delivery", err)
	}
	if _, err := readers.Source.Facts.ObserveHTTP(ctx, grant, f.request); err != nil || calls != 1 {
		t.Fatal("host observation not wired", err)
	}
	if _, err := OpenWorkspaceReaders(ctx, workspace, registry, path, renderer); err == nil {
		t.Fatal("host artifact silently used direct transport")
	}
	// A reader set has one lifecycle owner. No default host/probe is invented.
	base, _, _ := newExecutorFixture()
	opts := WorkspaceControllerOptions{Host: base, Probe: base, Store: base, Interval: time.Second, OperationTimeout: time.Second, CleanupTimeout: time.Second}
	if _, err := readers.NewControllerService(opts); !errors.Is(err, ErrWorkspaceIngressState) {
		t.Fatal("workspace owner accepted an unfenced memory journal", err)
	}
	stateDirectory := t.TempDir()
	if err := os.Chmod(stateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	opts.Store = FileStateStore{Directory: stateDirectory}
	owner, err := readers.NewControllerService(opts)
	if err != nil || owner.controller.Source != readers.Source || owner.controller.Executor.Renderer != readers.Renderer {
		t.Fatal("managed owner was not assembled", err)
	}
	if store, ok := owner.controller.Executor.Store.(WorkspaceStateStore); !ok || store.Workspace != workspace {
		t.Fatal("workspace owner was not connected to the actual cross-process fence")
	}
	if _, err := readers.NewControllerService(opts); err == nil {
		t.Fatal("readers have two lifecycle owners")
	}
	if err := readers.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readers.Source.Facts.ObserveHTTP(ctx, grant, f.request); err == nil || calls != 1 {
		t.Fatal("closed readers made another privileged call")
	}
	if _, err := readers.Source.NamingKey(ctx, scope, lease); err == nil {
		t.Fatal("closed delivery still releases naming keys")
	}
}

func TestHostReaderDeliveryRejectsMixedMissingAndDowngradedIdentity(t *testing.T) {
	f, _, _, path, scope, installation, keys := hostDeliveryFixture(t, false)
	ctx := context.Background()
	check := func(context.Context) error { return nil }
	mixed := installation
	mixed.Incus = f.config
	if err := WriteReaderCredentials(ctx, path, scope, mixed, keys, check); err == nil {
		t.Fatal("mixed privileged credentials accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid delivery left a file")
	}
	if err := WriteReaderCredentials(ctx, path, scope, installation, keys, check); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	for name, bad := range map[string][]byte{
		"incus-null":     append([]byte(`{"incus":null,`), body[1:]...),
		"incus-object":   append([]byte(`{"incus":{},`), body[1:]...),
		"wrong-schema":   bytes.Replace(body, []byte(hostCredentialsSchema), []byte(credentialsSchema), 1),
		"missing-pin":    bytes.Replace(body, []byte(installation.Host.ServerUUID), nil, 1),
		"aliased-key":    bytes.Replace(body, []byte(`"scope_id"`), []byte(`"Scope_ID"`), 1),
		"caller-invoker": append([]byte(`{"invoker":"shell",`), body[1:]...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeReaderCredentials(bad, scope); err == nil {
				t.Fatal("invalid transport variant accepted")
			}
		})
	}
}

func TestDirectReaderDeliveryPreservesLegacySchemaWithoutHostFallback(t *testing.T) {
	f, workspace, registry, path, scope, installation, keys := hostDeliveryFixture(t, false)
	installation.Host, installation.Incus = nil, f.config
	installation.Incus.Authorizations = scope.Authorizations
	if err := WriteReaderCredentials(context.Background(), path, scope, installation, keys, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if string(fields["schema"]) != `"`+credentialsSchema+`"` || fields["incus"] == nil || fields["host_observer"] != nil {
		t.Fatal("legacy wire schema changed")
	}
	renderer := FileRouteRenderer{Directory: "/unused/routes", Entrypoint: "/unused/entrypoint"}
	r, err := OpenWorkspaceReaders(context.Background(), workspace, registry, path, renderer)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	calls := 0
	if _, err := OpenHostWorkspaceReaders(context.Background(), workspace, registry, path, renderer, fixtureHostClient(scope.Authorizations[0], &calls, nil)); err == nil {
		t.Fatal("host launch accepted direct credentials")
	}
}

func TestHostReaderRechecksDeliveryAfterInvocationAndOnReplacement(t *testing.T) {
	f, workspace, registry, path, scope, installation, keys := hostDeliveryFixture(t, false)
	ctx := context.Background()
	if err := WriteReaderCredentials(ctx, path, scope, installation, keys, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := fixtureHostClient(scope.Authorizations[0], &calls, func() {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".replacement", body, 0400); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".replacement", path); err != nil {
			t.Fatal(err)
		}
	})
	r, err := OpenHostWorkspaceReaders(ctx, workspace, registry, path, FileRouteRenderer{Directory: "/unused/routes", Entrypoint: "/unused/entrypoint"}, client)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Source.Facts.ObserveHTTP(ctx, scope.Authorizations[0], f.request); err == nil || calls != 1 {
		t.Fatal("replacement during observation accepted")
	}
	if _, err := r.Source.Facts.ObserveHTTP(ctx, scope.Authorizations[0], f.request); err == nil || calls != 1 {
		t.Fatal("changed delivery reached action client")
	}
}

func TestHostReaderResourcesSurviveFailedDrainUntilRecovery(t *testing.T) {
	_, workspace, registry, path, scope, installation, keys := hostDeliveryFixture(t, false)
	ctx := context.Background()
	if err := WriteReaderCredentials(ctx, path, scope, installation, keys, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r, err := OpenHostWorkspaceReaders(ctx, workspace, registry, path, FileRouteRenderer{Directory: "/unused/routes", Entrypoint: "/unused/entrypoint"}, fixtureHostClient(scope.Authorizations[0], &calls, nil))
	if err != nil {
		t.Fatal(err)
	}
	// The real private reader set is lifecycle-owned; external effects are
	// explicit adapters so this is not Traefik or root execution acceptance.
	owner, _, _, effects := controllerServiceFixture(t)
	owner.resources = r
	effects.failRemove.Store(true)
	_, done := startControllerService(t, owner)
	awaitServiceSignal(t, owner.Ready())
	if err := stopControllerService(t, owner); !errors.Is(err, ErrControllerDrain) {
		t.Fatal(err)
	}
	if err := r.Source.Configuration(ctx, scope); err != nil {
		t.Fatal("failed cleanup closed original readers", err)
	}
	effects.failRemove.Store(false)
	if err := owner.RetryDrain(ctx); err != nil {
		t.Fatal(err)
	}
	<-done
	if err := r.Source.Configuration(ctx, scope); err == nil {
		t.Fatal("successful drain did not close readers")
	}
}
