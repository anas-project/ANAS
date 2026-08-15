package modulestore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/modulepackage"
	"github.com/anas-project/ANAS/internal/modulesource"
	"gopkg.in/yaml.v3"
)

type registryFixture struct {
	profile        modulesource.Profile
	client         *http.Client
	bundle         []byte
	manifestDigest string
}

func TestCatalogVersionsAndVerifiedInstall(t *testing.T) {
	fixture := newRegistryFixture(t)
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Client.HTTPClient = fixture.client
	store.Client.Credentials = nil

	catalog, err := store.FetchCatalog(context.Background(), fixture.profile)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Catalog.Modules[0].Module != "alpha" || catalog.OCIDigest == "" {
		t.Fatalf("catalog = %#v", catalog)
	}
	versions, _, err := store.Versions(context.Background(), fixture.profile, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2.0.0-r1", "1.2.3-r2", "1.2.3-r1"}
	if len(versions) != len(want) {
		t.Fatalf("versions = %#v", versions)
	}
	for i := range want {
		if versions[i].Release != want[i] {
			t.Fatalf("versions[%d] = %s, want %s", i, versions[i].Release, want[i])
		}
	}
	if !versions[1].Current {
		t.Fatal("catalog current release was not marked")
	}

	installed, err := store.Install(context.Background(), fixture.profile, "alpha", "1.2.3-r2", fixture.manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if installed.OCIDigest != fixture.manifestDigest || installed.ContentDigest == "" {
		t.Fatalf("installation = %#v", installed)
	}
	if !strings.Contains(installed.ImmutableReference, "@"+fixture.manifestDigest) {
		t.Fatalf("immutable reference = %s", installed.ImmutableReference)
	}
	lockedProfile := fixture.profile
	lockedProfile.Catalog = "oci://registry.test/test/missing-catalog:stable"
	lockedStore, _ := New(t.TempDir())
	lockedStore.Client.HTTPClient = fixture.client
	lockedStore.Client.Credentials = nil
	locked, err := lockedStore.InstallLocked(context.Background(), lockedProfile, installed.ImmutableReference,
		"alpha", "1.2.3-r2", "anas-module-alpha", fixture.manifestDigest)
	if err != nil || locked.OCIDigest != fixture.manifestDigest {
		t.Fatalf("locked install without catalog = %#v, %v", locked, err)
	}
	if _, err := os.Stat(filepath.Join(installed.Path, "contracts", "identity", "contract.yml")); err != nil {
		t.Fatalf("installed contract: %v", err)
	}
	if _, err := modulepackage.VerifyUnpacked(installed.Path); err != nil {
		t.Fatalf("verify installed package: %v", err)
	}
	second, err := store.Install(context.Background(), fixture.profile, "alpha", "1.2.3-r2", fixture.manifestDigest)
	if err != nil || second.Path != installed.Path {
		t.Fatalf("cached install = %#v, %v", second, err)
	}
	cached, ok, err := store.Cached(fixture.manifestDigest)
	if err != nil || !ok || cached.Path != installed.Path {
		t.Fatalf("cache record = %#v, %t, %v", cached, ok, err)
	}
	view, err := store.BuildView(map[string]Installation{"alpha": installed})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(view.ModuleRoot, "alpha"))
	wantPath, wantErr := filepath.EvalSymlinks(installed.Path)
	if err != nil || wantErr != nil || resolved != wantPath {
		t.Fatalf("view module = %q, %v", resolved, err)
	}
	if _, err := store.BuildView(map[string]Installation{"alpha": installed}); err != nil {
		t.Fatalf("verify existing view: %v", err)
	}
	viewRoot := filepath.Dir(view.ModuleRoot)
	if err := os.Remove(filepath.Join(viewRoot, "contracts", "identity")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BuildView(map[string]Installation{"alpha": installed}); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt view error = %v", err)
	}
}

func TestInstallRejectsWrongLockedDigest(t *testing.T) {
	fixture := newRegistryFixture(t)
	store, _ := New(t.TempDir())
	store.Client.HTTPClient = fixture.client
	store.Client.Credentials = nil
	_, err := store.Install(context.Background(), fixture.profile, "alpha", "1.2.3-r2", "sha256:"+strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "manifest digest mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestCachedRejectsRecordPathsOutsideContentAddressedCache(t *testing.T) {
	fixture := newRegistryFixture(t)
	store, _ := New(t.TempDir())
	store.Client.HTTPClient = fixture.client
	store.Client.Credentials = nil
	installed, err := store.Install(context.Background(), fixture.profile, "alpha", "1.2.3-r2", fixture.manifestDigest)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(store.CacheRoot, "records", "sha256", strings.TrimPrefix(fixture.manifestDigest, "sha256:")+".json")
	body, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record Installation
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatal(err)
	}
	record.BlobPath = filepath.Join(t.TempDir(), "outside")
	body, _ = json.Marshal(record)
	if err := os.WriteFile(recordPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Cached(installed.OCIDigest); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("escaping cache record error = %v", err)
	}
}

func TestExtractBundleRejectsTraversalAndLinks(t *testing.T) {
	for _, test := range []struct {
		name     string
		header   tar.Header
		contents string
	}{
		{name: "parent", header: tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0644, Size: 1}, contents: "x"},
		{name: "absolute", header: tar.Header{Name: "/escape", Typeflag: tar.TypeReg, Mode: 0644, Size: 1}, contents: "x"},
		{name: "symlink", header: tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := archiveWithEntry(t, test.header, test.contents)
			if err := extractBundle(body, t.TempDir()); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestRegistryBearerChallenge(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = io.WriteString(w, `{"token":"fixture-token"}`)
		case "/v2/test/repository/tags/list":
			if r.Header.Get("Authorization") != "Bearer fixture-token" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://registry.test/token",service="fixture",scope="repository:test/repository:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"name":"test/repository","tags":["1.0.0-r1"]}`)
		default:
			http.NotFound(w, r)
		}
	})
	store, _ := New(t.TempDir())
	store.Client.HTTPClient = handlerHTTPClient(handler)
	store.Client.Credentials = nil
	tags, err := store.listTags(context.Background(), "oci://registry.test/test/repository")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "1.0.0-r1" {
		t.Fatalf("tags = %#v", tags)
	}
}

func newRegistryFixture(t *testing.T) registryFixture {
	t.Helper()
	bundle := makeBundle(t)
	layerDigest := sha256Digest(bundle)
	moduleManifest := makeManifest(t, moduleArtifactType, moduleLayerType, layerDigest, int64(len(bundle)))
	moduleManifestDigest := sha256Digest(moduleManifest)
	catalogDocument := Catalog{
		APIVersion: CatalogAPIVersion,
		Modules: []CatalogModule{{
			Module: "alpha", Repository: "anas-module-alpha",
			Platforms: []string{"linux/amd64", "linux/arm64"}, Release: "1.2.3-r2",
		}},
	}
	catalogBody, err := json.Marshal(catalogDocument)
	if err != nil {
		t.Fatal(err)
	}
	catalogLayerDigest := sha256Digest(catalogBody)
	catalogManifest := makeManifest(t, catalogArtifactType, catalogLayerType, catalogLayerDigest, int64(len(catalogBody)))
	catalogManifestDigest := sha256Digest(catalogManifest)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/v2/test/catalog/manifests/stable":
			w.Header().Set("Content-Type", ociManifestType)
			w.Header().Set("Docker-Content-Digest", catalogManifestDigest)
			_, _ = w.Write(catalogManifest)
		case path == "/v2/test/catalog/blobs/"+catalogLayerDigest:
			_, _ = w.Write(catalogBody)
		case path == "/v2/test/anas-module-alpha/tags/list":
			if r.URL.Query().Get("last") == "" {
				w.Header().Set("Link", `</v2/test/anas-module-alpha/tags/list?n=100&last=1.2.3-r2>; rel="next"`)
				_, _ = io.WriteString(w, `{"name":"test/anas-module-alpha","tags":["1.2.3-r1","1.2.3-r2","latest"]}`)
			} else {
				_, _ = io.WriteString(w, `{"name":"test/anas-module-alpha","tags":["2.0.0-r1"]}`)
			}
		case path == "/v2/test/anas-module-alpha/manifests/1.2.3-r2" || path == "/v2/test/anas-module-alpha/manifests/"+moduleManifestDigest:
			w.Header().Set("Content-Type", ociManifestType)
			w.Header().Set("Docker-Content-Digest", moduleManifestDigest)
			_, _ = w.Write(moduleManifest)
		case path == "/v2/test/anas-module-alpha/blobs/"+layerDigest:
			_, _ = w.Write(bundle)
		default:
			http.NotFound(w, r)
		}
	})
	return registryFixture{
		profile: modulesource.Profile{
			Name: "fixture", Catalog: "oci://registry.test/test/catalog:stable",
			Repository: "oci://registry.test/test/anas-module-{name}",
		},
		client: handlerHTTPClient(handler), bundle: bundle, manifestDigest: moduleManifestDigest,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func handlerHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		return response, nil
	})}
}

func makeManifest(t *testing.T, artifactType, layerType, digest string, size int64) []byte {
	t.Helper()
	body, err := json.Marshal(Manifest{
		SchemaVersion: 2, MediaType: ociManifestType, ArtifactType: artifactType,
		Config: Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: "sha256:" + strings.Repeat("0", 64), Size: 2},
		Layers: []Descriptor{{MediaType: layerType, Digest: digest, Size: size}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func makeBundle(t *testing.T) []byte {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"module.yml": `api_version: anas.module/v1
kind: Module
name: alpha
version: 1.2.3
revision: 2
`,
		"docker-compose.yml": "services: {}\n",
		"contracts/identity/contract.yml": `api_version: anas.contract/v1
kind: Contract
name: identity
version: 1.0.0
interfaces: [oidc]
resource: {}
operations: {}
`,
	}
	for name, body := range files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	contentDigest, err := modulepackage.PayloadDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	metadata := modulepackage.PackageMetadata{
		APIVersion: modulepackage.PackageAPIVersion,
		Name:       "alpha", Version: "1.2.3", Revision: 2, Release: "1.2.3-r2",
		Platforms: []string{"linux/amd64", "linux/arm64"}, Repository: "anas-module-alpha",
		Compatibility: modulepackage.CompatibilityMetadata{ModuleAPI: "anas.module/v1"},
		ContentDigest: contentDigest, ContextDigest: "sha256:" + strings.Repeat("1", 64),
	}
	metadataBody, err := yaml.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.yml"), metadataBody, 0644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	gz.Header.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	var paths []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && path != root {
			paths = append(paths, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, filePath := range paths {
		info, err := os.Stat(filePath)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, filePath)
		header, _ := tar.FileInfoHeader(info, "")
		header.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			body, _ := os.ReadFile(filePath)
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func archiveWithEntry(t *testing.T, header tar.Header, contents string) []byte {
	t.Helper()
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&header); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(tw, contents); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestNextLinkRejectsCrossHost(t *testing.T) {
	_, err := nextLink("https://registry.test/v2/repo/tags/list", `<https://evil.test/tags>; rel="next"`)
	if err == nil {
		t.Fatal("cross-host pagination was accepted")
	}
	resolved, err := nextLink("https://registry.test/v2/repo/tags/list", `</v2/repo/tags/list?last=x>; rel="next"`)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(resolved)
	if u.Host != "registry.test" || u.Query().Get("last") != "x" {
		t.Fatalf("next link = %s", resolved)
	}
}
