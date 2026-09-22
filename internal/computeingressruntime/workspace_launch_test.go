package computeingressruntime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func launchRequest(r *WorkspaceReaders, options WorkspaceControllerOptions) HostWorkspaceStart {
	return HostWorkspaceStart{ScopeID: r.delivery.wire.Host.ScopeID, Workspace: r.workspace, RequestRegistry: r.registry,
		ReaderCredentials: r.delivery.path, Renderer: r.Renderer, Controller: options,
		Observation: r.Source.Facts.(*HostProjectionReader).client}
}

func TestWorkspaceLaunchRejectsScopeMismatchAndOccupiedCoordinator(t *testing.T) {
	r, options := workspaceLaunchFixture(t, "valid")
	coordinator, err := NewControllerCoordinator([]string{"main", "other"})
	if err != nil {
		t.Fatal(err)
	}
	request := launchRequest(r, options)
	request.ScopeID = "other"
	if _, err = coordinator.StartHostWorkspace(context.Background(), request); !errors.Is(err, ErrWorkspaceLaunch) {
		t.Fatal("cross-scope launch", err)
	}
	request.ScopeID = "main"
	gate, err := coordinator.BeginChange(context.Background(), []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.StartHostWorkspace(context.Background(), request); !errors.Is(err, ErrControllerChange) {
		t.Fatal("configuration fence bypassed", err)
	}
	if err = gate.Release(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = coordinator.StartHostWorkspace(ctx, request); err == nil {
		t.Fatal("canceled launch")
	}
	body, err := os.ReadFile(filepath.Join(r.workspace, ".anas/state/lock"))
	if err != nil || len(body) != 0 {
		t.Fatal("rejected startup wrote fence", err)
	}
}

func TestWorkspaceLaunchRevalidatesBeforeJournalAndClosesUnusedReaders(t *testing.T) {
	for _, changed := range []string{"script", "registry", "lease", "state"} {
		t.Run(changed, func(t *testing.T) {
			r, options := workspaceLaunchFixture(t, "valid")
			owner, err := r.NewControllerService(options)
			if err != nil {
				t.Fatal(err)
			}
			path := r.Renderer.Entrypoint
			switch changed {
			case "registry":
				path = filepath.Join(r.registry, "registry.json")
			case "lease":
				registry, err := expectedRegistry(r.delivery.wire.Scope)
				if err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(r.registry, registry.Bindings[0].Directory)
			case "state":
				path = options.Store.(FileStateStore).Directory
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(path, path+".old"); err != nil {
				t.Fatal(err)
			}
			if info.IsDir() {
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				body, err := os.ReadFile(path + ".old")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, body, info.Mode().Perm()); err != nil {
					t.Fatal(err)
				}
			}
			if err = owner.Run(context.Background()); !errors.Is(err, ErrWorkspaceLaunch) {
				t.Fatal("late replacement admitted", err)
			}
			if !r.closed.Load() || owner.Phase() != ControllerFailed {
				t.Fatal("failed acquisition leaked readers or claimed stopped")
			}
			body, err := os.ReadFile(filepath.Join(r.workspace, ".anas/state/lock"))
			if err != nil || len(body) != 0 {
				t.Fatal("failed preflight wrote lifetime marker", err)
			}
			if _, err = os.Stat(filepath.Join(options.Store.(FileStateStore).Directory, executorStateFile)); !os.IsNotExist(err) {
				t.Fatal("failed preflight created journal", err)
			}
		})
	}
}

func TestWorkspaceLaunchRejectsSharedFilesAndMutableSource(t *testing.T) {
	for _, scenario := range []string{"credential-hardlink", "script-hardlink", "registry-hardlink", "script-writable", "source", "renderer"} {
		t.Run(scenario, func(t *testing.T) {
			r, options := workspaceLaunchFixture(t, "valid")
			path := r.delivery.path
			switch scenario {
			case "script-hardlink", "script-writable":
				path = r.Renderer.Entrypoint
			case "registry-hardlink":
				path = filepath.Join(r.registry, "registry.json")
			}
			switch scenario {
			case "source":
				r.Source.RequestRegistry = filepath.Dir(path)
			case "renderer":
				r.Renderer.Directory = t.TempDir()
			case "script-writable":
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Link(path, filepath.Join(t.TempDir(), "link")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.NewControllerService(options); err == nil {
				t.Fatal("untrusted startup input admitted")
			}
		})
	}
}

func TestWorkspaceLaunchConcurrentConstructionClaimsOneReaderSet(t *testing.T) {
	r, options := workspaceLaunchFixture(t, "valid")
	var wait sync.WaitGroup
	var admitted atomic.Int32
	for range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := r.NewControllerService(options); err == nil {
				admitted.Add(1)
			}
		}()
	}
	wait.Wait()
	if admitted.Load() != 1 {
		t.Fatal("duplicate reader owners", admitted.Load())
	}
}

// Actual pinned HTTPS/BasicAuth reader and empty request registry. The API data
// is synthetic, and host/probe adapters are explicit fixtures. No mount, guest,
// real Traefik or Incus process is involved in this lifecycle acceptance test.
func TestWorkspaceLaunchRunsRecoveryAndDrainsThroughOriginalAssembly(t *testing.T) {
	r, options := workspaceLaunchFixture(t, "valid")
	var calls atomic.Int32
	started := time.Now().Add(-time.Minute).UTC()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		user, password, ok := request.BasicAuth()
		if request.Method != http.MethodGet || !ok || user != "fixture" || password != "test-only" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"Version": "3.7.10", "startDate": started})
		case "/api/rawdata":
			host, err := url.Parse("https://" + request.Host)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"routers":     map[string]any{"api@file": map[string]any{"status": "enabled", "rule": "Host(`" + host.Hostname() + "`)", "service": "api@internal", "entryPoints": []string{"https"}, "using": []string{"https"}, "middlewares": []string{"auth@file"}, "tls": map[string]any{}}},
				"middlewares": map[string]any{"auth@file": map[string]any{"status": "enabled", "basicAuth": map[string]any{"users": []string{"fixture:synthetic-hash"}}}},
				"services":    map[string]any{}, "tcpRouters": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	installation := ReaderInstallation{Host: r.delivery.wire.Host, Traefik: TraefikReaderConfig{Endpoint: server.URL,
		ServerCertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Username: "fixture", Password: "test-only", APIRouter: "api@file"}}
	credentials := filepath.Join(filepath.Dir(r.delivery.path), "launch-readers.json")
	if err := WriteReaderCredentials(context.Background(), credentials, r.delivery.wire.Scope, installation, nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	request := launchRequest(r, options)
	request.ReaderCredentials = credentials
	request.Controller.OperationTimeout, request.Controller.CleanupTimeout = 5*time.Second, 5*time.Second
	coordinator, err := NewControllerCoordinator([]string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner, err := coordinator.StartHostWorkspace(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	// Drain even when a failed assertion leaves the test early.
	defer func() {
		cancel()
		wait, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = owner.Stop(wait)
	}()
	select {
	case <-owner.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("assembled owner not ready", owner.Phase())
	}
	if calls.Load() < 6 {
		t.Fatal("readiness bypassed real HTTPS inventory")
	}
	if _, err := coordinator.StartHostWorkspace(ctx, request); !errors.Is(err, ErrControllerChange) {
		t.Fatal("second owner admitted", err)
	}
	readers := owner.resources.(*WorkspaceReaders)
	// Input disappearance invalidates publication, not independent cleanup.
	if err = os.Rename(request.RequestRegistry, request.RequestRegistry+".retired"); err != nil {
		t.Fatal(err)
	}
	if err = readers.Source.Configuration(context.Background(), readers.delivery.wire.Scope); err == nil {
		t.Fatal("lost request registry did not revoke publication")
	}
	if err := stopControllerService(t, owner); err != nil {
		t.Fatal(err)
	}
	if owner.Phase() != ControllerStopped {
		t.Fatal(owner.Phase())
	}
	if !readers.closed.Load() || readers.incus != nil {
		t.Fatal("host-only resources not released")
	}
	marker, err := os.ReadFile(filepath.Join(request.Workspace, ".anas/state/lock"))
	if err != nil || len(marker) != 0 {
		t.Fatal("successful managed shutdown did not clear fence", err)
	}
}

// Uses actual registered Core metadata, private reader delivery and filesystem
// objects. No host effects or application probe is executed by these preflights.
func workspaceLaunchFixture(t *testing.T, layout string) (*WorkspaceReaders, WorkspaceControllerOptions) {
	t.Helper()
	_, workspace, registry, credentials, scope, installation, keys := hostDeliveryFixture(t, false)
	directory := filepath.Join(workspace, ".anas", "state", "http-ingress")
	routes := filepath.Join(workspace, "traefik-routes")
	bin := filepath.Join(workspace, "trusted-bin")
	for _, path := range []string{directory, routes, bin} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	entrypoint := filepath.Join(bin, "anas-entrypoint.sh")
	body, err := os.ReadFile("../../modules/traefik/traefik/anas-entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(entrypoint, body, 0500); err != nil {
		t.Fatal(err)
	}
	switch layout {
	case "state-request":
		directory = registry
	case "state-credentials":
		directory = filepath.Dir(credentials)
	case "route-request":
		routes = registry
	case "route-state":
		routes = directory
	case "credentials-in-request":
		credentials = filepath.Join(registry, "readers.json")
	case "request-in-state":
		directory = workspace
	case "script-in-request":
		entrypoint = filepath.Join(registry, "anas-entrypoint.sh")
		if err = os.WriteFile(entrypoint, body, 0500); err != nil {
			t.Fatal(err)
		}
	case "alias":
		alias := filepath.Join(workspace, "alias")
		if err = os.Symlink(workspace, alias); err != nil {
			t.Fatal(err)
		}
		directory = filepath.Join(alias, "requests")
	}
	if err = WriteReaderCredentials(context.Background(), credentials, scope, installation, keys, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r, err := OpenHostWorkspaceReaders(context.Background(), workspace, registry, credentials,
		FileRouteRenderer{Directory: routes, Entrypoint: entrypoint}, fixtureHostClient(scope.Authorizations[0], &calls, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	effects, _, _ := newExecutorFixture()
	return r, WorkspaceControllerOptions{Host: effects, Probe: effects, Store: FileStateStore{Directory: directory},
		Interval: time.Hour, OperationTimeout: time.Second, CleanupTimeout: time.Second}
}

func TestWorkspaceControllerRejectsOverlappingRuntimeDirectories(t *testing.T) {
	for _, layout := range []string{"state-request", "state-credentials", "route-request", "route-state", "credentials-in-request", "request-in-state", "script-in-request", "alias"} {
		t.Run(layout, func(t *testing.T) {
			r, options := workspaceLaunchFixture(t, layout)
			if _, err := r.NewControllerService(options); err == nil {
				t.Fatal("unsafe runtime directory layout admitted")
			}
			body, err := os.ReadFile(filepath.Join(r.Source.Workspace, ".anas", "state", "lock"))
			if err != nil || len(body) != 0 {
				t.Fatal("preflight changed workspace fence", err)
			}
		})
	}
}

func TestWorkspaceControllerPinsRendererSelectedBeforeStart(t *testing.T) {
	for _, changed := range []string{"script-bytes", "script-inode", "route-directory"} {
		t.Run(changed, func(t *testing.T) {
			r, options := workspaceLaunchFixture(t, "valid")
			owner, err := r.NewControllerService(options)
			if err != nil {
				t.Fatal(err)
			}
			renderer := owner.controller.Executor.Renderer.(FileRouteRenderer)
			if changed == "route-directory" {
				if err = os.Rename(renderer.Directory, renderer.Directory+".old"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(renderer.Directory, 0700); err != nil {
					t.Fatal(err)
				}
				if root, closeRoot, err := renderer.open(); err == nil {
					closeRoot()
					_ = root
					t.Fatal("replacement route directory adopted")
				}
				return
			}
			body, err := os.ReadFile(renderer.Entrypoint)
			if err != nil {
				t.Fatal(err)
			}
			if changed == "script-inode" {
				if err = os.Rename(renderer.Entrypoint, renderer.Entrypoint+".old"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.Chmod(renderer.Entrypoint, 0600); err != nil {
					t.Fatal(err)
				}
				body = append(body, []byte("\n# changed renderer\n")...)
			}
			if err = os.WriteFile(renderer.Entrypoint, body, 0500); err != nil {
				t.Fatal(err)
			}
			_, _, target := newExecutorFixture()
			if _, _, err = renderer.renderedRoute(context.Background(), target); err == nil {
				t.Fatal("changed startup renderer executed")
			}
		})
	}
}
