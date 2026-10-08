package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

// These tests use real public Apply/activation and Hook subprocesses with a
// logged Compose fixture. They prove admission/start behavior, not PG damage.
func TestCLIApplyCannotReaddHistoricalPostgresOverRetainedData(t *testing.T) {
	for _, history := range []string{"retained-and-history", "history-only", "resource-only", "missing-history", "provider-renamed"} {
		t.Run(history, func(t *testing.T) {
			testPostgresReadditionAdmission(t, history)
		})
	}
}

func testPostgresReadditionAdmission(t *testing.T, history string) {
	t.Helper()
	workspace := newWorkspace(t)
	base := stateDir(workspace)
	log := filepath.Join(t.TempDir(), "compose.log")
	bin := t.TempDir()
	docker := "#!/bin/sh\nprintf '%s:%s\\n' \"$PWD\" \"$*\" >> '" + log + "'\ncase \"$*\" in *'config --services'*) printf 'server\\nprovision\\n' ;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(docker), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("PHOTOS_DATABASE_PASSWORD", "fixture-application-password", secretMetadata{Owner: "photos", Kind: "generated", Generation: 1})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	const newer = "dep-pg-newer"
	const removed = "dep-pg-removed"
	const historical = "dep-pg-historical"
	newManifest := writePostgresReadditionArtifact(t, base, newer, "2026-10-03T03:00:00Z", true, 5)
	writePostgresReadditionArtifact(t, base, removed, "2026-10-03T04:00:00Z", false, 0)
	writePostgresReadditionArtifact(t, base, historical, "2026-10-03T02:00:00Z", true, 4)
	service := newCLIDeploymentPlanService(workspace, workspaceConfigPath(workspace), "", nil)
	for _, id := range []string{newer, removed} {
		if _, err := service.Apply(context.Background(), application.ApplyRequest{DeploymentID: id, Confirmed: true, NoSnapshot: true}); err != nil {
			t.Fatalf("first install/removal %s: %v", id, err)
		}
	}
	var retained resourceState
	if err := readYAML(filepath.Join(base, "state", "resources", "photos.database.yml"), &retained); err != nil || retained.Status != "retained" {
		t.Fatal("provider removal did not retain its resource", retained, err)
	}
	current, err := loadDeploymentManifest(deploymentArtifactDir(base, removed))
	if err != nil {
		t.Fatal(err)
	}
	target, err := loadDeploymentManifest(deploymentArtifactDir(base, historical))
	if err != nil {
		t.Fatal(err)
	}
	if len(postgresMaintenanceProviders(current, target)) != 0 || len(deploymentPostgresProviders(newManifest)) == 0 {
		t.Fatal("fixture does not exercise absent-from-current provider admission")
	}
	if history == "history-only" || history == "missing-history" {
		if err := os.Remove(filepath.Join(base, "state", "resources", "photos.database.yml")); err != nil {
			t.Fatal(err)
		}
	}
	if history == "resource-only" {
		active, err := loadActiveState(base)
		if err != nil {
			t.Fatal(err)
		}
		active.PreviousDeployments = nil
		if err := saveActiveState(base, active); err != nil {
			t.Fatal(err)
		}
	}
	if history == "missing-history" {
		if err := os.RemoveAll(deploymentArtifactDir(base, newer)); err != nil {
			t.Fatal(err)
		}
	}
	if history == "provider-renamed" {
		module := target.Modules["database"]
		module.Name = "replacement"
		delete(target.Modules, "database")
		target.Modules["replacement"] = module
		target.ModuleOrder[0] = "replacement"
		module = target.Modules["photos"]
		module.Dependencies = []string{"replacement"}
		target.Modules["photos"] = module
		target.Resources[0].Provider = "replacement"
		root := deploymentArtifactDir(base, historical)
		if err := os.Rename(filepath.Join(root, "modules", "database"), filepath.Join(root, "modules", "replacement")); err != nil {
			t.Fatal(err)
		}
		if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), target, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(log, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), application.ApplyRequest{DeploymentID: historical, Confirmed: true, NoSnapshot: true, AllowRisky: true})
	calls, readErr := os.ReadFile(log)
	if err == nil {
		t.Logf("unprotected public Apply admitted the historical PG artifact; Compose calls: %s", calls)
	}
	assertDeploymentApplicationCode(t, err, application.ErrorKindFailedPrecondition, "postgres_restore_required")
	if readErr != nil || strings.Contains(string(calls), "up -d") || strings.Contains(string(calls), "run --rm") {
		t.Fatal("retained PG admission ran containers or provisioning before recovery validation", string(calls), readErr)
	}
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment != removed {
		t.Fatal("rejected readdition changed active deployment", active, err)
	}
}

func TestCLIApplyAllowsFirstPostgresWithNoPriorPostgresRecords(t *testing.T) {
	for _, history := range []string{"fresh-workspace", "non-postgres-history"} {
		t.Run(history, func(t *testing.T) {
			workspace := newWorkspace(t)
			base := stateDir(workspace)
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ncase \"$*\" in *'config --services'*) printf 'server\\nprovision\\n' ;; esac\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			store, err := loadSecretStore(base)
			if err != nil {
				t.Fatal(err)
			}
			store.SetWithMetadata("PHOTOS_DATABASE_PASSWORD", "fixture-application-password", secretMetadata{Owner: "photos", Kind: "generated", Generation: 1})
			if err := store.Save(); err != nil {
				t.Fatal(err)
			}
			service := newCLIDeploymentPlanService(workspace, workspaceConfigPath(workspace), "", nil)
			if history == "non-postgres-history" {
				for _, id := range []string{"dep-non-pg-before", "dep-non-pg-current"} {
					writePostgresReadditionArtifact(t, base, id, "2026-10-03T01:00:00Z", false, 0)
					if _, err := service.Apply(context.Background(), application.ApplyRequest{DeploymentID: id, Confirmed: true, NoSnapshot: true}); err != nil {
						t.Fatal("non-PG fixture activation", err)
					}
				}
				if err := writeYAMLAtomic(filepath.Join(base, "state", "resources", "files.objects.yml"), resourceState{
					APIVersion: resourceStateAPIVersion, Consumer: "files", ResourceID: "objects", Contract: "object_storage", Provider: "store", Interface: "s3", Status: "retained",
				}, 0600); err != nil {
					t.Fatal(err)
				}
			}
			const id = "dep-first-pg"
			writePostgresReadditionArtifact(t, base, id, "2026-10-03T05:00:00Z", true, 5)
			result, err := service.Apply(context.Background(), application.ApplyRequest{DeploymentID: id, Confirmed: true, NoSnapshot: true})
			if err != nil || result.DeploymentID != id {
				t.Fatal("a first PG installation without PG history was rejected", result, err)
			}
			const repeat = "dep-first-pg-repeat"
			repeated := writePostgresReadditionArtifact(t, base, repeat, "2026-10-03T06:00:00Z", true, 5)
			module := repeated.Modules["database"]
			module.RenderDigest = id
			repeated.Modules["database"] = module
			if err := writeYAMLAtomic(filepath.Join(deploymentArtifactDir(base, repeat), "deployment.yml"), repeated, 0600); err != nil {
				t.Fatal(err)
			}
			result, err = service.Apply(context.Background(), application.ApplyRequest{DeploymentID: repeat, Confirmed: true, NoSnapshot: true})
			if err != nil || result.DeploymentID != repeat {
				t.Fatal("repeated apply with PG still active was rejected", result, err)
			}
		})
	}
}

func writePostgresReadditionArtifact(t *testing.T, base, id, created string, withPostgres bool, revision int) *deploymentManifest {
	t.Helper()
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, CreatedAt: created,
		ModuleOrder: []string{"core"}, Modules: map[string]deploymentModule{
			"core": {Name: "core", Version: "1.0.0", RuntimeType: "builtin", RenderDigest: "core"},
		}}
	if withPostgres {
		manifest.ModuleOrder = []string{"database", "photos", "core"}
		manifest.Modules["database"] = deploymentModule{Name: "database", Version: "18.4.0", Revision: revision,
			RuntimeType: "compose", ComposeFile: "docker-compose.yml", RenderDigest: id, EnvPrefix: "POSTGRES",
			Hook: HookConfig{Command: []string{"sh", "hook.sh"}, Phases: []string{"after_start"}},
			Providers: []ContractProvider{{Name: "relational_database", Version: "1.0.0", Interface: "postgres", OperationSvcs: []string{"provision"}, Operations: map[string]ProviderOperation{
				"ensure": {Runtime: "compose_run", Service: "provision", Command: []string{"sh", "provision.sh", "ensure"}},
			}}}}
		manifest.Modules["photos"] = deploymentModule{Name: "photos", Version: "1.0.0", RuntimeType: "builtin", RenderDigest: "photos", Dependencies: []string{"database"}}
		manifest.Resources = []deploymentResource{{Consumer: "photos", ID: "database", Contract: "relational_database", ContractVersion: "1.0.0", Provider: "database", Interface: "postgres", SecretKey: "PHOTOS_DATABASE_PASSWORD", Spec: map[string]any{
			"name": "photos", "principal": "photos", "deletion_policy": "retain", "credential": map[string]any{"policy": "generated"}, "postgres": map[string]any{"extensions": []any{"vector"}},
		}}}
	}
	root := deploymentArtifactDir(base, id)
	for _, name := range manifest.ModuleOrder {
		dir := filepath.Join(root, "modules", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for file, body := range map[string]string{
			".env":               "CONTAINER_PREFIX=pg_readdition_fixture_\nPOSTGRES_HOST=fixture-database\nPOSTGRES_PORT=5432\nPOSTGRES_NETWORK_NAME=fixture-network\n",
			"docker-compose.yml": "services:\n  server:\n    image: fixture-pg-image\n  provision:\n    image: fixture-pg-image\n",
			"hook.sh":            "#!/bin/sh\ncat >/dev/null\nprintf '{}'\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveDeploymentState(base, deploymentState{ID: id, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	return manifest
}
