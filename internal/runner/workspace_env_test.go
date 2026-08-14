package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceEnvUsesExplicitDockerHostSocket(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///run/anas-isolated.sock")
	a := &app{workspace: t.TempDir(), env: map[string]string{}, envOwner: map[string]string{}}
	a.applyWorkspaceEnv()
	if got := a.env["DOCKER_SOCKET_PATH"]; got != "/run/anas-isolated.sock" {
		t.Fatalf("DOCKER_SOCKET_PATH = %q", got)
	}
}

func TestConfiguredDockerSocketWinsOverProcessEnvironment(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///run/process.sock")
	a := &app{workspace: t.TempDir(), env: map[string]string{"DOCKER_SOCKET_PATH": "/run/config.sock"}}
	a.applyWorkspaceEnv()
	if got := a.env["DOCKER_SOCKET_PATH"]; got != "/run/config.sock" {
		t.Fatalf("DOCKER_SOCKET_PATH = %q", got)
	}
}

func TestWorkspaceEnvUsesExplicitRuntimeEntryIP(t *testing.T) {
	t.Setenv("ANAS_RUNTIME_ENTRY_IP", "10.251.0.2")
	a := &app{workspace: t.TempDir(), env: map[string]string{}, envOwner: map[string]string{}}
	a.applyWorkspaceEnv()
	if got := a.env["ANAS_RUNTIME_ENTRY_IP"]; got != "10.251.0.2" {
		t.Fatalf("ANAS_RUNTIME_ENTRY_IP = %q", got)
	}
}

func TestModuleRuntimeStatePathIsDeploymentScopedAndRelocatable(t *testing.T) {
	workspace := t.TempDir()
	base := filepath.Join(workspace, ".anas")
	modules := filepath.Join(base, "deployments", "deployment-1", "modules")
	a := &app{base: base, artifactRoot: modules}

	rel := a.moduleRuntimeStatePath("traefik")
	if filepath.IsAbs(rel) {
		t.Fatalf("runtime-state path = %q, want a relocatable relative path", rel)
	}
	moduleDir := filepath.Join(modules, "traefik")
	want := filepath.Join(base, "runtime-state", "deployments", "deployment-1", "traefik")
	if got := filepath.Clean(filepath.Join(moduleDir, filepath.FromSlash(rel))); got != want {
		t.Fatalf("runtime-state path resolves to %q, want %q", got, want)
	}

	restoredWorkspace := t.TempDir()
	restoredModule := filepath.Join(restoredWorkspace, ".anas", "deployments", "deployment-1", "modules", "traefik")
	restoredWant := filepath.Join(restoredWorkspace, ".anas", "runtime-state", "deployments", "deployment-1", "traefik")
	if got := filepath.Clean(filepath.Join(restoredModule, filepath.FromSlash(rel))); got != restoredWant {
		t.Fatalf("relocated path resolves to %q, want %q", got, restoredWant)
	}
}

func TestRuntimeFilesStayOutsideSealedArtifact(t *testing.T) {
	workspace := t.TempDir()
	base := filepath.Join(workspace, ".anas")
	artifact := filepath.Join(base, "deployments", "deployment-1", "modules")
	a := &app{base: base, artifactRoot: artifact}
	env := map[string]string{"ANAS_MODULE_RUNTIME_STATE_PATH": a.moduleRuntimeStatePath("traefik")}
	runtimeRoot := a.resolveModuleRuntimeStatePath("traefik", env)
	if err := applyHookRuntimeFiles(runtimeRoot, map[string]string{"dynamic/auth.yml": "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot, "dynamic", "auth.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(artifact, "traefik", "dynamic", "auth.yml")); !os.IsNotExist(err) {
		t.Fatalf("runtime file entered the deployment artifact: %v", err)
	}
}
