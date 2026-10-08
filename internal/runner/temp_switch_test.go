package runner

// TEST_CASES: TEMP-T-008

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/computeingress"
)

func TestTemporarySwitchPreviewIncludesEveryModuleAndBindsDigest(t *testing.T) {
	workspace, id := newSnapshotWorkspace(t)
	base := stateDir(workspace)
	root := filepath.Join(base, "deployments", id)
	manifest, err := loadDeploymentManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ModuleOrder = []string{"provider", "editor", "passive"}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(filepath.Join(root, "modules", globalEnvFile), map[string]string{"TEMP_PATH": filepath.Join(workspace, "tmp")}); err != nil {
		t.Fatal(err)
	}
	same, err := temporarySwitchPlan(workspace, filepath.Join(workspace, "x", "..", "tmp"), []string{"provider", "editor", "passive"})
	if err != nil || same.Required {
		t.Fatalf("same resolved root switched: %#v, %v", same, err)
	}
	changed, err := temporarySwitchPlan(workspace, filepath.Join(workspace, "new-temp"), []string{"provider", "new-editor", "passive"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Required || !changed.SessionInterruption || !reflect.DeepEqual(changed.StopModules, []string{"passive", "editor", "provider"}) || !reflect.DeepEqual(changed.StartModules, []string{"provider", "new-editor", "passive"}) {
		t.Fatalf("incomplete switch impact: %#v", changed)
	}
	plan := application.PlanResult{TempSwitch: same}
	before, _ := deploymentPlanDigest(plan)
	plan.TempSwitch = changed
	after, _ := deploymentPlanDigest(plan)
	if before == after {
		t.Fatal("plan token did not bind switch impact")
	}
	rollback := application.RollbackPreviewResult{TargetDeployment: id, TempSwitch: same}
	before, _ = rollbackPreviewDigest(rollback)
	rollback.TempSwitch = changed
	after, _ = rollbackPreviewDigest(rollback)
	if before == after {
		t.Fatal("rollback token did not bind switch impact")
	}
}

func TestTemporaryHistoricalRollbackPreservesFrozenSourceAndRebinds(t *testing.T) {
	workspace, id := newSnapshotWorkspace(t)
	base := stateDir(workspace)
	root := filepath.Join(base, "deployments", id)
	moduleDir := filepath.Join(root, "modules", "core")
	for _, dir := range []string{moduleDir, filepath.Join(root, "modules")} {
		if err := writeEnv(filepath.Join(dir, map[bool]string{true: ".env", false: globalEnvFile}[dir == moduleDir]), map[string]string{"ANAS_DEPLOYMENT_ID": id, "TEMP_PATH": filepath.Join(workspace, "tmp")}); err != nil {
			t.Fatal(err)
		}
	}
	pathConfig := filepath.Join(moduleDir, "rendered.conf")
	if err := os.WriteFile(pathConfig, []byte(filepath.Join(root, "modules", "core")), 0644); err != nil {
		t.Fatal(err)
	}
	manifest, _ := loadDeploymentManifest(root)
	manifest.Resources = []deploymentResource{{Consumer: "core", ID: "runner", Contract: "compute", Provider: "incus", Interface: "incus_vm", LeaseSecretKey: "ANAS_COMPUTE_RESOURCE__CORE__RUNNER__LEASE_SECRET", ComputeIngress: &computeingress.Authorization{Schema: computeingress.Schema, Deployment: id, Consumer: "core", Resource: "runner", Provider: "incus", Interface: "incus_vm", Project: "anas-test", InstancePrefix: "anas-test-", LeaseSecretRef: "ANAS_COMPUTE_RESOURCE__CORE__RUNNER__LEASE_SECRET", BaseDomain: "example.test", Policy: computeingress.Policy{AllowedPorts: []uint16{7000}, Auth: "none", Domain: computeingress.Domain{Mode: "random", Prefix: "ci"}}}}}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := sealDeployment(root); err != nil {
		t.Fatal(err)
	}
	before, err := normalizedModuleDigest(root, "")
	if err != nil {
		t.Fatal(err)
	}
	newID, err := materializeTemporaryRollback(base, id)
	if err != nil {
		t.Fatal(err)
	}
	if newID == id {
		t.Fatal("historical rollback reused the old identity")
	}
	after, _ := normalizedModuleDigest(root, "")
	if before != after {
		t.Fatal("historical artifact was rewritten")
	}
	nextRoot := filepath.Join(base, "deployments", newID)
	next, err := loadDeploymentManifest(nextRoot)
	if err != nil {
		t.Fatal(err)
	}
	if next.Resources[0].ComputeIngress.Deployment != newID || next.Modules["core"].ArtifactDeployment != newID {
		t.Fatal("candidate retained historical authorization/artifact identity")
	}
	env, _ := parseEnvFile(filepath.Join(nextRoot, "modules", "core", ".env"))
	if env["ANAS_DEPLOYMENT_ID"] != newID || env["TEMP_PATH"] != filepath.Join(workspace, "tmp") {
		t.Fatalf("frozen configuration not retained: %#v", env)
	}
	content, _ := os.ReadFile(filepath.Join(nextRoot, "modules", "core", "rendered.conf"))
	if strings.Contains(string(content), id) || !strings.Contains(string(content), newID) {
		t.Fatalf("candidate artifact path not rebound: %s", content)
	}
	info, _ := os.Stat(filepath.Join(nextRoot, "modules", "core", ".env"))
	if info.Mode().Perm()&0222 != 0 {
		t.Fatal("candidate artifact was not sealed")
	}
}

func TestTemporaryStorageFaultOverridesHealthyProbeAndRecovers(t *testing.T) {
	module := Module{Name: "editor", TemporaryDirectories: []TemporaryDirectory{{Name: "runtime"}}}
	free := uint64(4000)
	storage := TemporaryStorageStatus{DesiredRoot: "/temporary", Directories: []TemporaryDirectoryStatus{{TemporaryLease: TemporaryLease{ID: "lease", Module: "editor", Name: "runtime", Root: "/temporary", State: "active"}, FreeBytes: &free}}, Issues: []TemporaryStorageIssue{{Module: "editor", Name: "runtime", LeaseID: "lease", Code: "temp_low_space", Message: "private host path /secret"}}}
	fault := summarizeModuleTemporaryStorage(module, storage, nil)
	if fault.State != "low_space" || len(fault.Issues) != 1 || fault.FreeBytes == nil {
		t.Fatalf("storage fault omitted: %#v", fault)
	}
	storage.Issues = nil
	recovered := summarizeModuleTemporaryStorage(module, storage, nil)
	if recovered.State != "ok" || len(recovered.Issues) != 0 {
		t.Fatalf("recovered status stayed unhealthy: %#v", recovered)
	}
	unknown := summarizeModuleTemporaryStorage(module, storage, errors.New("Docker query failed"))
	if unknown.State != "unknown" {
		t.Fatal("unknown storage became healthy")
	}
	if summarizeModuleTemporaryStorage(Module{Name: "passive"}, storage, nil) != nil {
		t.Fatal("undeclared Module got storage requirements")
	}
}

func TestTemporaryStorageIsExcludedFromBackupAndSnapshot(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	for _, file := range []string{filepath.Join(workspace, "tmp", "temporary-content"), filepath.Join(stateDir(workspace), "temp", "registry.yml")} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("source-only"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source, err := workspaceBackupSource(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range source.parts {
		if temporaryPathsOverlap(part.src, filepath.Join(workspace, "tmp")) || temporaryPathsOverlap(part.src, filepath.Join(stateDir(workspace), "temp")) {
			t.Fatalf("temporary content entered backup source: %#v", part)
		}
	}
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(snapshotRoot(workspace, meta.ID), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "temporary-content" || entry.Name() == "registry.yml" {
			t.Errorf("source temporary authority entered snapshot: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
