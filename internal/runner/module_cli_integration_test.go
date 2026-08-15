package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/modulepackage"
	"github.com/anas-project/ANAS/internal/modulesource"
	"github.com/anas-project/ANAS/internal/modulestore"
)

const (
	testOCIManifestType    = "application/vnd.oci.image.manifest.v1+json"
	testModuleArtifactType = "application/vnd.anas.module.v1"
	testModuleLayerType    = "application/vnd.anas.module.bundle.v1.tar+gzip"
	testCatalogArtifact    = "application/vnd.anas.module.catalog.v1"
	testCatalogLayer       = "application/vnd.anas.module.catalog.v1+json"
)

type runnerRegistryArtifact struct {
	name           string
	repository     string
	release        string
	bundle         []byte
	layerDigest    string
	manifest       []byte
	manifestDigest string
}

func TestRemoteModuleCLIEndToEndUpdateAndDigestOnlySync(t *testing.T) {
	repositoryRoot := repoRoot(t)
	artifacts := map[string]runnerRegistryArtifact{}
	for _, name := range []string{"lego", "traefik"} {
		output := filepath.Join(t.TempDir(), name+".tar.gz")
		result, err := modulepackage.Build(modulepackage.BuildOptions{
			RepoRoot: repositoryRoot, CatalogPath: filepath.Join(".github", "modules.json"),
			Module: name, Platform: "all", OutputPath: output, SkipHookBuild: true,
		})
		if err != nil {
			t.Fatalf("package %s: %v", name, err)
		}
		bundle, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		layerDigest := runnerSHA256(bundle)
		manifest := runnerManifest(t, testModuleArtifactType, testModuleLayerType, layerDigest, int64(len(bundle)))
		artifacts[name] = runnerRegistryArtifact{
			name: name, repository: result.Metadata.Repository, release: result.Metadata.Release,
			bundle: bundle, layerDigest: layerDigest, manifest: manifest, manifestDigest: runnerSHA256(manifest),
		}
	}

	catalog := modulestore.Catalog{APIVersion: modulestore.CatalogAPIVersion, SourceCommit: "fixture"}
	for _, name := range []string{"lego", "traefik"} {
		artifact := artifacts[name]
		catalog.Modules = append(catalog.Modules, modulestore.CatalogModule{
			Module: name, Repository: artifact.repository,
			Platforms: []string{"linux/amd64", "linux/arm64"}, Release: artifact.release,
		})
	}
	catalogBody, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalogLayerDigest := runnerSHA256(catalogBody)
	catalogManifest := runnerManifest(t, testCatalogArtifact, testCatalogLayer, catalogLayerDigest, int64(len(catalogBody)))
	catalogManifestDigest := runnerSHA256(catalogManifest)
	catalogAvailable := true
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := r.URL.Path
		if strings.HasPrefix(requestPath, "/v2/test/catalog/") {
			if !catalogAvailable {
				http.NotFound(w, r)
				return
			}
			switch requestPath {
			case "/v2/test/catalog/manifests/stable":
				w.Header().Set("Docker-Content-Digest", catalogManifestDigest)
				_, _ = w.Write(catalogManifest)
			case "/v2/test/catalog/blobs/" + catalogLayerDigest:
				_, _ = w.Write(catalogBody)
			default:
				http.NotFound(w, r)
			}
			return
		}
		for _, artifact := range artifacts {
			base := "/v2/test/" + artifact.repository
			switch requestPath {
			case base + "/manifests/" + artifact.release, base + "/manifests/" + artifact.manifestDigest:
				w.Header().Set("Docker-Content-Digest", artifact.manifestDigest)
				_, _ = w.Write(artifact.manifest)
				return
			case base + "/blobs/" + artifact.layerDigest:
				_, _ = w.Write(artifact.bundle)
				return
			case base + "/tags/list":
				_, _ = fmt.Fprintf(w, `{"name":%q,"tags":[%q,"0.0.1-r1"]}`, "test/"+artifact.repository, artifact.release)
				return
			}
		}
		http.NotFound(w, r)
	})

	profile := modulesource.Profile{
		Name: modulesource.Official, Catalog: "oci://registry.test/test/catalog:stable",
		Repository: "oci://registry.test/test/anas-module-{name}",
	}
	originalLookup, originalFactory := lookupModuleSourceProfile, createModuleStore
	lookupModuleSourceProfile = func(raw string) (modulesource.Profile, bool) {
		if modulesource.DefaultName(raw) != modulesource.Official {
			return modulesource.Profile{}, false
		}
		return profile, true
	}
	createModuleStore = func(cacheRoot string) (*modulestore.Store, error) {
		store, err := modulestore.New(cacheRoot)
		if err != nil {
			return nil, err
		}
		store.Client.HTTPClient = runnerHandlerClient(handler)
		store.Client.Credentials = nil
		return store, nil
	}
	defer func() {
		lookupModuleSourceProfile = originalLookup
		createModuleStore = originalFactory
	}()

	prefetchCache := t.TempDir()
	stdout, _, exit := capture(t, "module", "list", "--source", "official", "--cache-dir", prefetchCache, "--json")
	if exit != 0 {
		t.Fatalf("module list exit %d: %s", exit, stdout)
	}
	if modules, ok := requireSingleDocument(t, "module list", stdout)["modules"].([]any); !ok || len(modules) != 2 {
		t.Fatalf("module list output = %s", stdout)
	}
	stdout, _, exit = capture(t, "module", "versions", "traefik", "--source", "official", "--cache-dir", prefetchCache, "--json")
	if exit != 0 || !strings.Contains(stdout, artifacts["traefik"].release) {
		t.Fatalf("module versions exit %d: %s", exit, stdout)
	}
	stdout, _, exit = capture(t, "module", "install", "traefik@"+artifacts["traefik"].release,
		"--source", "official", "--digest", artifacts["traefik"].manifestDigest, "--cache-dir", prefetchCache, "--json")
	if exit != 0 || !strings.Contains(stdout, artifacts["traefik"].manifestDigest) {
		t.Fatalf("module install exit %d: %s", exit, stdout)
	}

	workspace := newWorkspace(t)
	source := filepath.Join(t.TempDir(), "anas.yml")
	configBody := fmt.Sprintf(`module_source: official
modules:
  traefik:
    version: %s
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: UTC
  virtual_domain: true
`, artifacts["traefik"].release)
	if err := os.WriteFile(source, []byte(configBody), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit = capture(t, "config", "import", source, "-w", workspace, "--root", repositoryRoot, "--json")
	if exit != 0 {
		t.Fatalf("config import exit %d: %s", exit, stdout)
	}

	updateCache := t.TempDir()
	stdout, _, exit = capture(t, "module", "update", "-w", workspace, "--cache-dir", updateCache, "--json")
	if exit != 0 {
		t.Fatalf("module update exit %d: %s", exit, stdout)
	}
	requireSingleDocument(t, "module update", stdout)
	lockPath := projectLockPath(workspaceConfigPath(workspace))
	lockBefore, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lego", "traefik"} {
		record, ok := lock.Modules[name]
		if !ok || record.OCIDigest != artifacts[name].manifestDigest || record.ContentDigest == "" || record.Repository != artifacts[name].repository {
			t.Fatalf("lock record %s = %#v", name, record)
		}
		if !strings.Contains(record.Source, "@"+record.OCIDigest) {
			t.Fatalf("lock source %s = %s", name, record.Source)
		}
	}
	view, err := loadWorkspaceModuleView(workspace)
	if err != nil || len(view.Installations) != 2 {
		t.Fatalf("updated view = %#v, %v", view, err)
	}
	if registry, err := loadRegistryDir(view.ModuleRoot); err != nil || registry["traefik"].Name != "traefik" || registry["lego"].Name != "lego" {
		t.Fatalf("updated registry = %#v, %v", registry, err)
	}

	// Recovery must use repository@digest from the lock, even when the moving
	// catalog is unavailable and the replacement cache starts empty.
	catalogAvailable = false
	syncCache := t.TempDir()
	stdout, _, exit = capture(t, "module", "sync", "-w", workspace, "--source", "official", "--cache-dir", syncCache, "--json")
	if exit != 0 {
		t.Fatalf("module sync without catalog exit %d: %s", exit, stdout)
	}
	requireSingleDocument(t, "module sync", stdout)
	syncedView, err := loadWorkspaceModuleView(workspace)
	if err != nil || !strings.HasPrefix(syncedView.ModuleRoot, syncCache+string(filepath.Separator)) {
		t.Fatalf("synced view = %#v, %v", syncedView, err)
	}
	lockAfter, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(lockAfter) != string(lockBefore) {
		t.Fatal("module sync modified config.lock.yml")
	}
	for _, name := range []string{"lego", "traefik"} {
		store, err := createModuleStore(syncCache)
		if err != nil {
			t.Fatal(err)
		}
		if _, cached, err := store.Cached(lock.Modules[name].OCIDigest); err != nil || !cached {
			t.Fatalf("synced cache %s: cached=%t err=%v", name, cached, err)
		}
	}
}

func runnerManifest(t *testing.T, artifactType, layerType, digest string, size int64) []byte {
	t.Helper()
	body, err := json.Marshal(modulestore.Manifest{
		SchemaVersion: 2, MediaType: testOCIManifestType, ArtifactType: artifactType,
		Config: modulestore.Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: "sha256:" + strings.Repeat("0", 64), Size: 2},
		Layers: []modulestore.Descriptor{{MediaType: layerType, Digest: digest, Size: size}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func runnerSHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

type runnerRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn runnerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func runnerHandlerClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		return response, nil
	})}
}
