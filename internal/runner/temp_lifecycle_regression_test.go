package runner

// TEST_CASES: TEMP-T-003, TEMP-T-004, TEMP-T-008

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

func temporaryLifecycleFixture(t *testing.T) (*app, string, string) {
	t.Helper()
	a, _ := temporaryTestApp(t)
	if err := os.MkdirAll(dataDir(a.workspace), 0700); err != nil {
		t.Fatal(err)
	}
	id := seedSnapshotWorkspaceAt(t, a.workspace)
	root := filepath.Join(a.base, "deployments", id, "modules")
	manifest, err := loadDeploymentManifest(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Modules["core"] = deploymentModule{Name: "core", RuntimeType: "compose", ComposeFile: "docker-compose.yml"}
	if err := writeYAMLAtomic(filepath.Join(filepath.Dir(root), "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "core", "docker-compose.yml"), []byte("services:\n  anas_core: {image: test}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(filepath.Join(root, globalEnvFile), map[string]string{"DATA_PATH": dataDir(a.workspace)}); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(filepath.Join(root, "core", ".env"), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "docker.log")
	quotedLog := "'" + strings.ReplaceAll(log, "'", "'\\''") + "'"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nset -e\nprintf '%s\\n' \"$*\" >> "+quotedLog+"\ncase \"$*\" in *'config --services'*) printf 'anas_core\\n';; esac\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := inspectComposeProjectOwners
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	t.Cleanup(func() { inspectComposeProjectOwners = previous })
	return a, id, log
}

func TestTemporaryCommittedCleanupAllowsLifecycleAndAnotherSwitch(t *testing.T) {
	a, id, log := temporaryLifecycleFixture(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	old := registry.Leases[0]
	marker := filepath.Join(old.Path, "retained-content")
	if err := os.WriteFile(marker, []byte("keep until safe GC"), 0600); err != nil {
		t.Fatal(err)
	}
	nextRoot := filepath.Join(a.workspace, "new-temp")
	registry.Leases[0].State = "released"
	registry.AppliedRoot = nextRoot
	registry.Transition = &TemporaryTransition{FromDeployment: old.DeploymentID, TargetDeployment: id, FromRoot: old.Root, TargetRoot: nextRoot, Phase: "committed"}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(filepath.Join(a.base, "deployments", id, "modules", globalEnvFile), map[string]string{"DATA_PATH": dataDir(a.workspace), "TEMP_PATH": nextRoot}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old.Root, old.Root+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	warnings := &recordingWarnings{}
	service := newCLIDeploymentPlanService(a.workspace, workspaceConfigPath(a.workspace), "", warnings)
	for _, action := range []application.LifecycleAction{application.LifecycleStop, application.LifecycleStart, application.LifecycleRestart} {
		if _, err := service.ExecuteLifecycle(context.Background(), application.LifecycleRequest{Action: action}); err != nil {
			t.Fatalf("cleanup blocked %s: %v", action, err)
		}
	}
	body, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(body), "down") {
		t.Fatalf("stop never reached Compose: %s %v", body, err)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil || registry.Transition.Phase != "cleanup_deferred" || registry.Leases[0].State != "released" {
		t.Fatalf("pending cleanup authority lost: %+v %v", registry, err)
	}
	if !contains(warnings.codes, "temp_cleanup_pending") {
		t.Fatal("deferred cleanup warning was omitted")
	}
	// Reapplying the active deployment must also work while the old disk is absent.
	if err := activateDeployment(a.base, id, activateOptions{ctx: context.Background()}); err != nil {
		t.Fatal(err)
	}
	if err := a.beginTemporaryTransition("next-deployment", filepath.Join(a.workspace, "third-temp")); err != nil {
		t.Fatalf("deferred cleanup blocked a later switch: %v", err)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil || registry.Transition.FromDeployment != id || len(registry.Leases) != 1 || registry.Leases[0].ID != old.ID {
		t.Fatalf("new switch discarded old lease: %+v %v", registry, err)
	}
	if err := os.Rename(old.Root+"-unavailable", old.Root); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "keep until safe GC" {
		t.Fatalf("pending content was changed: %q %v", body, err)
	}
	if err := a.recordTemporaryTransitionPhase("restored"); err != nil {
		t.Fatal(err)
	}
	status, err := a.gcTemporaryStorage(true)
	if err != nil || len(status.Directories) != 1 || !status.Directories[0].Reclaimable {
		t.Fatalf("old lease cannot be retried after root returns: %+v %v", status, err)
	}
}

func TestTemporaryCommittedRecoveryStillRejectsUnverifiedTargetMounts(t *testing.T) {
	a, id, _ := temporaryLifecycleFixture(t)
	manifestPath := filepath.Join(a.base, "deployments", id, "deployment.yml")
	manifest, err := loadDeploymentManifest(filepath.Dir(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	module := manifest.Modules["core"]
	module.TemporaryDirectories = cloneTemporaryDirectories(a.reg["demo"].TemporaryDirectories)
	manifest.Modules["core"] = module
	if err := writeYAMLAtomic(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		t.Fatal(err)
	}
	registry.Transition = &TemporaryTransition{FromDeployment: "previous", TargetDeployment: id, Phase: "starting"}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := recoverTemporaryTransition(a.base, a.compose, activateOptions{}); err == nil || !strings.Contains(err.Error(), "reconcile committed temporary mounts") {
		t.Fatalf("unverified target was accepted as deferred cleanup: %v", err)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil || registry.Transition.Phase != "starting" {
		t.Fatalf("failed mount verification changed recovery authority: %+v %v", registry, err)
	}
}

func TestTemporaryLifecyclePreflightUsesSelectionBeforeStopping(t *testing.T) {
	for _, fault := range []string{"capacity", "group-writable-frozen-mode"} {
		t.Run(fault, func(t *testing.T) {
			a, id, log := temporaryLifecycleFixture(t)
			root := filepath.Join(a.base, "deployments", id)
			manifest, err := loadDeploymentManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			declarations := cloneTemporaryDirectories(a.reg["demo"].TemporaryDirectories)
			if fault == "capacity" {
				probe := temporaryFilesystemProbe
				temporaryFilesystemProbe = func(path string) (TemporaryFilesystem, uint64, uint64, error) {
					filesystem, _, _, err := probe(path)
					return filesystem, 1, 1, err
				}
			} else {
				declarations[0].Mode = "0770"
			}
			manifest.ModuleOrder = []string{"demo", "core"}
			manifest.Modules["demo"] = deploymentModule{Name: "demo", RuntimeType: "compose", ComposeFile: "docker-compose.yml", TemporaryDirectories: declarations}
			if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "modules", "demo"), 0700); err != nil {
				t.Fatal(err)
			}
			service := newCLIDeploymentPlanService(a.workspace, workspaceConfigPath(a.workspace), "", nil)
			_, err = service.ExecuteLifecycle(context.Background(), application.LifecycleRequest{Action: application.LifecycleRestart, Modules: []string{"demo"}})
			assertDeploymentApplicationCode(t, err, application.ErrorKindFailedPrecondition, "temp_preflight_failed")
			body, err := os.ReadFile(log)
			if err != nil || strings.Contains(string(body), "down") {
				t.Fatalf("invalid selected storage stopped services: %s %v", body, err)
			}
			for _, action := range []application.LifecycleAction{application.LifecycleStart, application.LifecycleRestart} {
				if _, err := service.ExecuteLifecycle(context.Background(), application.LifecycleRequest{Action: action, Modules: []string{"core"}}); err != nil {
					t.Fatalf("unselected demo storage blocked %s core: %v", action, err)
				}
			}
			body, err = os.ReadFile(log)
			if err != nil || !strings.Contains(string(body), "down") {
				t.Fatalf("valid selected lifecycle never stopped: %s %v", body, err)
			}
		})
	}
}
