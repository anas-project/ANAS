package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/modulestore"
)

func TestWorkspaceModuleViewLoadsSymlinkedImmutableBundles(t *testing.T) {
	repository := repoRoot(t)
	viewRoot := filepath.Join(t.TempDir(), "view")
	moduleRoot := filepath.Join(viewRoot, "modules")
	contractRoot := filepath.Join(viewRoot, "contracts")
	if err := os.MkdirAll(moduleRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(contractRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repository, "modules", "traefik"), filepath.Join(moduleRoot, "traefik")); err != nil {
		t.Fatal(err)
	}
	contracts, err := os.ReadDir(filepath.Join(repository, "contracts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range contracts {
		if !entry.IsDir() {
			continue
		}
		if err := os.Symlink(filepath.Join(repository, "contracts", entry.Name()), filepath.Join(contractRoot, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	view := modulestore.View{APIVersion: "anas.module-view/v1", Digest: "sha256:test", ModuleRoot: moduleRoot}
	if err := saveWorkspaceModuleView(workspace, view); err != nil {
		t.Fatal(err)
	}
	located, err := locateModuleRootForWorkspace("", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if located != moduleRoot {
		t.Fatalf("located = %s, want %s", located, moduleRoot)
	}
	registry, err := loadRegistryDir(located)
	if err != nil {
		t.Fatal(err)
	}
	traefik, ok := registry["traefik"]
	if !ok {
		t.Fatal("symlinked Module was not loaded")
	}
	resolved, _ := filepath.EvalSymlinks(filepath.Join(repository, "modules", "traefik"))
	if traefik.SourceDir != resolved {
		t.Fatalf("SourceDir = %s, want %s", traefik.SourceDir, resolved)
	}
	if _, err := loadContractRegistry(located); err != nil {
		t.Fatalf("load symlinked contracts: %v", err)
	}

	explicit, err := locateModuleRootForWorkspace(filepath.Join(repository, "modules"), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if explicit == moduleRoot {
		t.Fatal("workspace view overrode explicit --module-root")
	}
}

func TestLockUpdatePreservesRegistryIdentityForWorkspaceView(t *testing.T) {
	repository := repoRoot(t)
	moduleRoot := filepath.Join(repository, "modules")
	registry, err := loadRegistryDir(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(stateDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	view := modulestore.View{
		APIVersion: "anas.module-view/v1", Digest: "sha256:test", ModuleRoot: moduleRoot,
		Installations: map[string]modulestore.Installation{
			"traefik": {
				Name: "traefik", Path: registry["traefik"].SourceDir,
				Repository:         "anas-module-traefik",
				ImmutableReference: "oci://registry.test/anas-module-traefik@sha256:" + strings.Repeat("0", 64),
				OCIDigest:          "sha256:" + strings.Repeat("0", 64), ContentDigest: "sha256:" + strings.Repeat("0", 64),
			},
		},
	}
	if err := saveWorkspaceModuleView(workspace, view); err != nil {
		t.Fatal(err)
	}
	lock := &moduleLock{}
	app := &app{workspace: workspace, reg: registry, order: []string{"traefik"}, contracts: map[string]Contract{}}
	if err := app.updateModuleLock(lock, false); err != nil {
		t.Fatal(err)
	}
	record := lock.Modules["traefik"]
	if record.Repository != "anas-module-traefik" || !strings.HasPrefix(record.Source, "oci://") {
		t.Fatalf("lock record = %#v", record)
	}
}
