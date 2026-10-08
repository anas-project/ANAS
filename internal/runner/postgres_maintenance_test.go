package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func postgresMaintenanceFixture() (*deploymentManifest, *deploymentManifest) {
	current := &deploymentManifest{ID: "before", ModuleOrder: []string{"db", "iam", "photos"}, Modules: map[string]deploymentModule{"db": {Name: "db", Version: "18.4.0", Revision: 4, RenderDigest: "old"}}, Resources: []deploymentResource{
		{Consumer: "photos", Contract: "relational_database", Interface: "postgres", Provider: "db", Spec: map[string]any{"postgres": map[string]any{"extensions": []any{"vector"}}}},
		{Consumer: "iam", Contract: "relational_database", Interface: "postgres", Provider: "db"},
	}}
	target := &deploymentManifest{ID: "after", ModuleOrder: current.ModuleOrder, Modules: map[string]deploymentModule{"db": {Name: "db", Version: "18.4.0", Revision: 4, RenderDigest: "new"}}, Resources: current.Resources}
	return current, target
}

func TestPostgresMaintenanceIncludesOrdinaryConsumersAndRevisionChanges(t *testing.T) {
	current, target := postgresMaintenanceFixture()
	providers := postgresMaintenanceProviders(current, target)
	if !providers["db"] || !reflect.DeepEqual(postgresMaintenanceConsumers(current, target, providers), []string{"iam", "photos"}) {
		t.Fatal("shared ordinary consumer omitted", providers)
	}
	trigger := deploymentSnapshotTrigger(current, target)
	if trigger == nil || !strings.Contains(trigger.detail, "consumers=iam,photos") || !strings.Contains(trigger.detail, "full workspace") {
		t.Fatal(trigger)
	}
	target.Modules["db"] = current.Modules["db"]
	if len(postgresMaintenanceProviders(current, target)) != 0 {
		t.Fatal("unchanged apply became maintenance")
	}
	target.Modules["db"] = deploymentModule{Name: "db", Version: "18.4.0", Revision: 5, RenderDigest: "old"}
	if !postgresMaintenanceProviders(current, target)["db"] {
		t.Fatal("release revision ignored")
	}
	if len(postgresMaintenanceProviders(nil, target)) != 0 {
		t.Fatal("fresh install needs a nonexistent recovery point")
	}
}

func TestPostgresMaintenanceCannotBypassRecoveryPoint(t *testing.T) {
	current, target := postgresMaintenanceFixture()
	_, err := snapshotBeforeApply(t.TempDir(), activateOptions{noSnapshot: true, yes: true}, nil, "", current, target)
	var cli *CLIError
	if !errors.As(err, &cli) || cli.Code != "postgres_recovery_point_required" {
		t.Fatal(err)
	}
}

func TestPostgresBackupCannotTakeLiveNoStopCopy(t *testing.T) {
	workspace, _ := newSnapshotWorkspace(t)
	if _, err := buildBackupPlan(workspace, backupOptions{dest: t.TempDir(), mode: backupModeCopy, noStop: true}); err != nil {
		t.Fatalf("ordinary backup unexpectedly restricted: %v", err)
	}
	artifact, _ := recoveryImageFixture(t, workspace, "image-is-not-read-by-plan")
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	// Retained extension binaries require the same protection even when no
	// current application requests an extension or database.
	manifest.Resources = nil
	module := manifest.Modules["photos"]
	module.Providers = []ContractProvider{{Name: "relational_database", Interface: "postgres"}}
	manifest.Modules["photos"] = module
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = buildBackupPlan(workspace, backupOptions{dest: t.TempDir(), mode: backupModeCopy, noStop: true})
	failure, ok := err.(*CLIError)
	if !ok || failure.Code != "postgres_backup_quiesce_required" {
		t.Fatalf("live PG backup escaped consistent-capture guard: %v", err)
	}
}

func TestPostgresMaintenanceProtectsRetainedExtensionsAfterDeclarationRemoval(t *testing.T) {
	current, target := postgresMaintenanceFixture()
	for i := range current.Resources {
		current.Resources[i].Spec = nil
	}
	if !postgresMaintenanceProviders(current, target)["db"] {
		t.Fatal("ordinary resource bypassed recovery after extension request removal")
	}
	if trigger := deploymentSnapshotTrigger(current, target); trigger == nil {
		t.Fatal("retained extension could change without an ANAS recovery point")
	}
}

func TestPostgresMaintenanceProtectsProviderAfterEveryConsumerWasRemoved(t *testing.T) {
	current, target := postgresMaintenanceFixture()
	for _, manifest := range []*deploymentManifest{current, target} {
		manifest.Resources = nil
		module := manifest.Modules["db"]
		module.Providers = []ContractProvider{{Name: "relational_database", Interface: "postgres"}}
		manifest.Modules["db"] = module
	}
	if !postgresMaintenanceProviders(current, target)["db"] || deploymentSnapshotTrigger(current, target) == nil {
		t.Fatal("retained database lost maintenance protection when all consumers disappeared")
	}
	a := &app{order: []string{"db"}, reg: map[string]Module{"db": {ContractProviders: []ContractProvider{{Name: "relational_database", Interface: "postgres"}}}}}
	plans := map[string]map[string]string{}
	a.addPostgresMaintenancePlan(plans)
	if plans["db"]["extension_upgrade"] == "" || plans["db"]["database_consumers"] != "" {
		t.Fatal("unbound provider maintenance is absent from its plan", plans)
	}
}

func TestPostgresMaintenanceRuntimeFlagIsScoped(t *testing.T) {
	a := &app{postgresMaintenance: map[string]bool{"db": true}}
	in := map[string]string{"ANAS_POSTGRES_EXTENSION_MAINTENANCE": "true", "OTHER": "retained"}
	if got := a.postgresMaintenanceHookEnv("photos", in); got["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] != "" || got["OTHER"] != "retained" {
		t.Fatal(got)
	}
	if got := a.postgresMaintenanceHookEnv("db", nil); got["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] != "true" {
		t.Fatal(got)
	}
	if in["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] != "true" {
		t.Fatal("mutated frozen environment")
	}
}

func TestPostgresMaintenanceIntentBlocksRestartAfterCrash(t *testing.T) {
	base := t.TempDir()
	active := &activeDeploymentState{APIVersion: activeStateVersion, ActiveDeployment: "before", RuntimeStatus: "running"}
	if err := recordPostgresMaintenanceIntent(base, active, "after"); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadActiveState(base)
	if err != nil || loaded.RuntimeStatus != "stopped" {
		t.Fatal(loaded, err)
	}
	if pending, err := postgresMaintenancePending(base, "before"); err != nil || pending != "after" {
		t.Fatal(pending, err)
	}
	if err := startDeployment(&app{base: base}, "", nil, false); err == nil || !strings.Contains(err.Error(), "retry frozen deployment after") {
		t.Fatal("old artifact resumed after uncertain maintenance", err)
	}
}

func TestPostgresMaintenanceFailureNeverRestoresOldImage(t *testing.T) {
	candidate, candidateRoot, _ := stopBarrierFixture(t, false)
	previous, previousRoot, _ := stopBarrierFixture(t, false)
	candidate.postgresMaintenance = map[string]bool{"db": true}
	err := activationFailure(t.TempDir(), "candidate", "start_failed", errors.New("extension failed"), candidate, candidateRoot, previous, previousRoot, false)
	results, ok := err.Detail["recovery"].([]map[string]any)
	if !ok || len(results) != 2 || results[1]["status"] != "failed" || !strings.Contains(results[1]["message"].(string), "shared data may have changed") {
		t.Fatal(err.Detail)
	}
}

func TestPostgresUncertainDataBlocksStaleBackupCompensation(t *testing.T) {
	for _, restoring := range []bool{false, true} {
		t.Run(fmt.Sprint("restore=", restoring), func(t *testing.T) {
			workspace, id := newSnapshotWorkspace(t)
			base := stateDir(workspace)
			active, err := loadActiveState(base)
			if err != nil {
				t.Fatal(err)
			}
			if restoring {
				err = beginDataRestoreGuard(base, id, "snapshot:failed-data-restore")
			} else {
				err = recordPostgresMaintenanceIntent(base, active, "failed-pg-candidate")
			}
			if err != nil {
				t.Fatal(err)
			}
			txn := &containerTransaction{ID: "stale-backup", DeploymentID: id, Modules: []string{"database"}, State: containerTransactionStopped}
			if err := writeContainerTransaction(base, txn); err != nil {
				t.Fatal(err)
			}
			// No Compose endpoint is available: crossing the guard would try
			// to run a real command instead of returning the durable cause.
			err = finishContainerTransaction(base, &app{base: base}, "", txn)
			code := "postgres_recovery_required"
			if restoring {
				code = "data_restore_incomplete"
			}
			failure, ok := err.(*CLIError)
			if !ok || failure.Code != code || !exists(transactionPath(base, txn.ID)) {
				t.Fatalf("unsafe backup compensation: err=%v, transaction retained=%v", err, exists(transactionPath(base, txn.ID)))
			}
		})
	}
}

func TestPostgresMaintenanceRefusesDowngradeAndMajorMigration(t *testing.T) {
	current, target := postgresMaintenanceFixture()
	for _, version := range []string{"17.8.0", "19.0.0", "invalid"} {
		target.Modules["db"] = deploymentModule{Version: version, RenderDigest: "new"}
		if err := postgresMaintenanceTransitionAllowed(current, target, map[string]bool{"db": true}); err == nil {
			t.Fatal("allowed unsafe data open", version)
		}
	}
	target.Modules["db"] = deploymentModule{Version: "18.4.0", Revision: 4, RenderDigest: "new"}
	if err := postgresMaintenanceTransitionAllowed(current, target, map[string]bool{"db": true}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRecoveryCoverageRejectsNestedMountAndEscapingSymlink(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{}`, `invalid`, fmt.Sprintf(`{"filesystems":[{"target":"/","children":[{"target":%q}]}]}`, filepath.Join(data, "media"))} {
		if err := validateRecoveryMounts(data, []byte(payload)); err == nil {
			t.Fatal("incomplete mount coverage accepted", payload)
		}
	}
	if err := validateRecoveryMounts(data, []byte(`{"filesystems":[{"target":"/"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(data, "media")); err != nil {
		t.Fatal(err)
	}
	if err := validateRecoverySymlinks(data); err == nil {
		t.Fatal("symlink data omitted by snapshot")
	}
}
