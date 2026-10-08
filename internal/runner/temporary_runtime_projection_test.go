package runner

// TEST_CASES: TEMP-T-010
// REQUIREMENTS: TEMP-R-024

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemporaryRuntimeRetainsStorageFactsAndOtherModulesAfterComposeFailure(t *testing.T) {
	a, _ := temporaryTestApp(t)
	if err := a.preflightTemporaryStorage(a.order); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	id := a.temporaryDeploymentID()
	root := filepath.Join(a.base, "deployments", id)
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"demo", "success"}, Modules: map[string]deploymentModule{}}
	for _, name := range manifest.ModuleOrder {
		dir := filepath.Join(root, "modules", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for file, body := range map[string]string{".env": "CONTAINER_PREFIX=anas_\n", "docker-compose.yml": "services: {}\n"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		manifest.Modules[name] = deploymentModule{Name: name, RuntimeType: "compose", ComposeFile: "docker-compose.yml", TemporaryDirectories: a.reg[name].TemporaryDirectories}
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(filepath.Join(root, "modules", globalEnvFile), map[string]string{"DATA_PATH": dataDir(a.workspace)}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"compose version"*) exit 0 ;;
  *"--project-name anas_success"*) printf '%s\n' '[{"Service":"app","State":"running","Health":"healthy"}]'; exit 0 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	probe := temporaryFilesystemProbe
	temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
		filesystem, _, _, err := probe(path)
		return filesystem, 1, 1, err
	}
	result, err := (workspaceRuntimeProbe{}).InspectRuntime(context.Background(), a.workspace, id)
	if err == nil || len(result.Modules) != 2 {
		t.Fatalf("failed Compose observation discarded module facts: %+v, %v", result, err)
	}
	failed, success := result.Modules[0], result.Modules[1]
	if failed.Runtime != "unknown" || failed.Health != "unhealthy" || failed.TempStorage == nil || failed.TempStorage.State != "low_space" || success.Runtime != "running" || success.Health != "healthy" {
		t.Fatalf("wrong partial observation: %+v", result.Modules)
	}
}

func TestTemporaryRuntimeChecksAppliedRootAndEveryDeclaration(t *testing.T) {
	module := Module{Name: "app", TemporaryDirectories: []TemporaryDirectory{
		{Name: "documents", Service: "app"}, {Name: "cache", Service: "app"},
	}}
	storage := TemporaryStorageStatus{
		AppliedRoot: "/applied", DesiredRoot: "/desired",
		Directories: []TemporaryDirectoryStatus{{TemporaryLease: TemporaryLease{
			ID: "documents", Module: "app", Service: "app", Name: "documents", Root: "/applied", State: "active",
		}}},
	}
	result := summarizeModuleTemporaryStorage(module, storage, nil)
	if result.State != "unavailable" || len(result.Issues) != 1 || result.Issues[0].Name != "cache" || result.Issues[0].Code != "temp_lease_missing" {
		t.Fatalf("a partial allocation hid a missing declaration: %+v", result)
	}
	storage.Directories = append(storage.Directories, TemporaryDirectoryStatus{TemporaryLease: TemporaryLease{
		ID: "cache", Module: "app", Service: "app", Name: "cache", Root: "/applied", State: "active",
	}})
	if result := summarizeModuleTemporaryStorage(module, storage, nil); result.State != "ok" || len(result.Issues) != 0 {
		t.Fatalf("desired config was mistaken for already-applied storage: %+v", result)
	}
}

func TestTemporaryRuntimeDoesNotLeakPrivateIssuesOrRetiredFailures(t *testing.T) {
	module := Module{Name: "app", TemporaryDirectories: []TemporaryDirectory{{Name: "runtime", Service: "app"}}}
	storage := TemporaryStorageStatus{
		AppliedRoot: "/private/root", DesiredRoot: "/private/root",
		Directories: []TemporaryDirectoryStatus{{TemporaryLease: TemporaryLease{
			ID: "current", Module: "app", Service: "app", Name: "runtime", Root: "/private/root", State: "active",
		}}},
		Issues: []TemporaryStorageIssue{
			{Code: "temp_low_space", Module: "app", Name: "runtime", LeaseID: "retired", Message: "/private/root/retired"},
			{Code: "temp_unavailable", Module: "app", Name: "/private/tampered-name", LeaseID: "current", Message: "/private/diagnostic"},
		},
	}
	result := summarizeModuleTemporaryStorage(module, storage, nil)
	if len(result.Issues) != 1 || result.Issues[0].Name != "" || result.State != "unavailable" {
		t.Fatalf("unexpected public issues: %+v", result)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "/private/") || strings.Contains(string(body), "retired") || strings.Contains(string(body), "message") {
		t.Fatalf("private registry diagnostic leaked: %s", body)
	}
}
