package runner

// TEST_CASES: TEMP-T-018

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/compose"
)

func temporaryImportedWorkspace(t *testing.T) string {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(newWorkspace(t))
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func writeTemporaryImportedBinding(t *testing.T, workspace, id, recordedWorkspace, prefix, tempRoot string) {
	t.Helper()
	root := filepath.Join(stateDir(workspace), "deployments", id)
	if err := os.MkdirAll(filepath.Join(root, "modules", "demo"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"demo"}, Modules: map[string]deploymentModule{
		"demo": {Name: "demo", RuntimeType: "builtin"},
	}}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DATA_PATH": dataDir(recordedWorkspace), "TEMP_PATH": tempRoot, "CONTAINER_PREFIX": prefix, "ANAS_DEPLOYMENT_ID": id}
	for _, path := range []string{filepath.Join(root, "modules", globalEnvFile), filepath.Join(root, "modules", "demo", ".env")} {
		if err := writeEnv(path, env); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTemporaryImportedRuntimeWithoutRegistryPlansNoSourceEffectsAndDetaches(t *testing.T) {
	source, target := temporaryImportedWorkspace(t), temporaryImportedWorkspace(t)
	const imported = "imported-history"
	writeTemporaryImportedBinding(t, source, imported, source, "source_", filepath.Join(source, "tmp"))
	writeTemporaryImportedBinding(t, target, imported, source, "source_", filepath.Join(source, "tmp"))
	for _, workspace := range []string{source, target} {
		if err := saveActiveState(stateDir(workspace), &activeDeploymentState{ActiveDeployment: imported, RuntimeStatus: "stopped"}); err != nil {
			t.Fatal(err)
		}
	}
	beforeTarget := mustReadFile(t, activeStatePath(stateDir(target)))
	beforeSource := mustReadFile(t, activeStatePath(stateDir(source)))
	plan, err := temporarySwitchPlan(target, filepath.Join(target, "external-root"), []string{"demo"})
	if err != nil || plan.Required || plan.SessionInterruption || len(plan.StopModules) != 0 || len(plan.StartModules) != 0 {
		t.Fatalf("import planned source lifecycle effects: %#v %v", plan, err)
	}
	afterPlan := mustReadFile(t, activeStatePath(stateDir(target)))
	if string(afterPlan) != string(beforeTarget) || exists(temporaryStatePath(stateDir(target))) {
		t.Fatal("read-only plan rewrote imported state")
	}
	observer := temporaryDockerObservation
	t.Cleanup(func() { temporaryDockerObservation = observer })
	temporaryDockerObservation = func(*app) (temporaryDockerSnapshot, error) {
		t.Fatal("detaching imported metadata queried source containers")
		return temporaryDockerSnapshot{}, errors.New("unreachable")
	}
	if err := recoverTemporaryTransition(stateDir(target), compose.CLI{}, activateOptions{}); err != nil {
		t.Fatal(err)
	}
	active, err := loadActiveState(stateDir(target))
	if err != nil || active.ActiveDeployment != "" || active.RuntimeStatus != "stopped" {
		t.Fatalf("import kept source runtime authorization: %#v %v", active, err)
	}
	diagnostic, err := os.ReadFile(filepath.Join(stateDir(target), "temp", "foreign-active-imported-"+imported+".yml"))
	if err != nil || string(diagnostic) != string(beforeTarget) || exists(temporaryStatePath(stateDir(target))) {
		t.Fatal("source metadata diagnostic was not retained independently", err)
	}
	afterSource := mustReadFile(t, activeStatePath(stateDir(source)))
	if string(afterSource) != string(beforeSource) {
		t.Fatal("detaching target changed source workspace state")
	}
}

func TestTemporaryImportedRuntimeLifecycleAndHistoricalCLIRequireLocalApply(t *testing.T) {
	source, target := temporaryImportedWorkspace(t), temporaryImportedWorkspace(t)
	const imported = "imported-history"
	writeTemporaryImportedBinding(t, target, imported, source, "source_", filepath.Join(source, "tmp"))
	if err := saveDeploymentState(stateDir(target), deploymentState{ID: imported, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(stateDir(target), &activeDeploymentState{ActiveDeployment: imported, RuntimeStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "unexpected-docker")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\nexit 98\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := mustReadFile(t, activeStatePath(stateDir(target)))
	commands := [][]string{{"start"}, {"stop"}, {"restart"}, {"apply", "--deployment", imported, "-y", "--no-snapshot"}, {"rollback", imported}}
	for _, command := range commands {
		args := append(append([]string{}, command...), "-w", target, "--json")
		stdout, _, exit := capture(t, args...)
		if exit != exitPrecondition {
			t.Fatalf("%v exit=%d, want precondition: %s", command, exit, stdout)
		}
		document := requireFailureDocument(t, "imported runtime", stdout)
		failure := document["error"].(map[string]any)
		if failure["code"] != "deployment_workspace_mismatch" || !strings.Contains(failure["message"].(string), "anas apply") {
			t.Fatalf("%v did not explain local apply: %#v", command, failure)
		}
	}
	service := newCLIDeploymentPlanService(target, workspaceConfigPath(target), "", nil)
	if _, err := service.PreviewLifecycle(context.Background(), application.LifecyclePreviewRequest{Action: application.LifecycleStop}); err == nil {
		t.Fatal("lifecycle preview accepted source runtime")
	}
	if _, err := service.PreviewRollback(context.Background(), application.RollbackPreviewRequest{DeploymentID: imported}); err == nil {
		t.Fatal("historical preview accepted source runtime")
	}
	if _, _, _, err := loadDeploymentApp(stateDir(target), imported, compose.CLI{}); err == nil {
		t.Fatal("shared frozen runtime loader accepted source project")
	}
	if err := stopActiveDeployment(stateDir(target)); err == nil {
		t.Fatal("snapshot stop accepted imported source runtime")
	}
	after := mustReadFile(t, activeStatePath(stateDir(target)))
	if string(before) != string(after) || exists(log) {
		t.Fatal("rejected imported operation changed metadata or invoked Docker")
	}
}

func TestTemporaryImportedRuntimeMainApplyBuildsLocalDeploymentAndKeepsSource(t *testing.T) {
	for _, sourceRunning := range []bool{true, false} {
		t.Run(fmt.Sprintf("source_running_%t", sourceRunning), func(t *testing.T) {
			temporaryImportedMainApply(t, sourceRunning)
		})
	}
}

func temporaryImportedMainApply(t *testing.T, sourceRunning bool) {
	t.Helper()
	source, observation := temporaryTestApp(t)
	const imported, sourceProject, targetProject = "imported-history", "source_demo", "target_demo"
	source.artifactRoot = filepath.Join(source.base, "deployments", imported, "modules")
	source.env["CONTAINER_PREFIX"], source.env["ANAS_DEPLOYMENT_ID"] = "source_", imported
	module := source.reg["demo"]
	module.SourceDir = filepath.Join(source.artifactRoot, "demo")
	source.reg["demo"] = module
	writeTemporaryImportedBinding(t, source.workspace, imported, source.workspace, "source_", filepath.Join(source.workspace, "tmp"))
	if err := source.prepareModuleTemporaryStorage(module); err != nil {
		t.Fatal(err)
	}
	registry, _ := source.loadTemporaryRegistry(false)
	sourceLease := registry.Leases[0]
	sentinel := filepath.Join(sourceLease.Path, "source-sentinel")
	if err := os.WriteFile(sentinel, []byte("source-content"), 0600); err != nil {
		t.Fatal(err)
	}
	sourceContainer := temporaryTestContainer(source, sourceLease)
	sourceContainer.ID = "source-container-id"
	sourceContainer.Config.Labels["com.docker.compose.project"] = sourceProject
	observation.Containers = []temporaryDockerContainer{sourceContainer}
	if err := source.verifyModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	runtimeStatus := "running"
	if !sourceRunning {
		runtimeStatus = "stopped"
		observation.Containers = nil
	}
	if err := saveActiveState(source.base, &activeDeploymentState{ActiveDeployment: imported, RuntimeStatus: runtimeStatus}); err != nil {
		t.Fatal(err)
	}
	beforeSource := mustReadFile(t, temporaryStatePath(source.base))
	beforeActive := mustReadFile(t, activeStatePath(source.base))
	target := temporaryImportedWorkspace(t)
	writeTemporaryImportedBinding(t, target, imported, source.workspace, "source_", filepath.Join(source.workspace, "tmp"))
	hookMarker := filepath.Join(source.workspace, "unexpected-source-before-stop")
	for _, workspace := range []string{source.workspace, target} {
		root := filepath.Join(stateDir(workspace), "deployments", imported)
		manifest, err := loadDeploymentManifest(root)
		if err != nil {
			t.Fatal(err)
		}
		frozenModule := manifest.Modules["demo"]
		frozenModule.TemporaryDirectories = cloneTemporaryDirectories(module.TemporaryDirectories)
		frozenModule.Hook = HookConfig{Command: []string{"sh", "-c", "printf called > '" + hookMarker + "'; printf '{}'"}, Phases: []string{"before_stop"}}
		manifest.Modules["demo"] = frozenModule
		if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveActiveState(stateDir(target), &activeDeploymentState{ActiveDeployment: imported, RuntimeStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	moduleRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(moduleRoot, "contracts"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(moduleRoot, "modules", "demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	moduleYAML := fmt.Sprintf("api_version: anas.module/v1\nkind: Module\nname: demo\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi:\n  supports: [anas.module-hook/v1]\nruntime:\n  type: compose\n  compose_file: docker-compose.yml\ntemporary_directories:\n  - name: documents\n    service: app\n    target: /temporary\n    lifecycle: container\n    uid: %d\n    gid: %d\n    mode: \"0700\"\n    min_free_bytes: 1024\n    min_free_inodes: 2\nupgrade:\n  data_breaking: []\nconfig: {}\n", os.Getuid(), os.Getgid())
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(moduleYAML), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services:\n  app:\n    image: fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	targetRoot := filepath.Join(target, "new-temp")
	configuration := fmt.Sprintf("global:\n  base_domain: example.test\n  email: test@example.invalid\n  container_prefix: target_\n  network_prefix: target_\n  temp_path: %s\nmodules:\n  demo: {}\n", targetRoot)
	if err := os.WriteFile(workspaceConfigPath(target), []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedConfigState(target, "test-imported-runtime"); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log, launched := filepath.Join(bin, "calls"), filepath.Join(bin, "launched-path")
	script := "#!/bin/sh\n" +
		"[ \"$1 $2\" = 'compose version' ] && exit 0\n" +
		"if [ \"$1\" = ps ]; then case \"$*\" in *'label=com.docker.compose.project=" + sourceProject + "'*) printf '%s\\n' '" + module.SourceDir + "';; esac; exit 0; fi\n" +
		"printf '%s:%s\\n' \"$PWD\" \"$*\" >> '" + log + "'\n" +
		"case \"$*\" in\n" +
		" *'config --services') printf 'app\\n';;\n" +
		" *' up '*) printf '%s' \"$ANAS_TEMP_DOCUMENTS\" > '" + launched + "';;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	temporaryDockerObservation = func(a *app) (temporaryDockerSnapshot, error) {
		result := temporaryDockerSnapshot{DaemonID: observation.DaemonID, Containers: append([]temporaryDockerContainer{}, observation.Containers...)}
		if path, err := os.ReadFile(launched); err == nil {
			current, err := a.loadTemporaryRegistry(false)
			if err != nil {
				return result, err
			}
			for _, lease := range current.Leases {
				if lease.Path == string(path) {
					container := temporaryTestContainer(a, lease)
					container.ID = "target-container-id"
					container.Config.Labels["com.docker.compose.project"] = targetProject
					result.Containers = append(result.Containers, container)
				}
			}
		}
		return result, nil
	}
	stdout, stderr, exit := capture(t, "apply", "-w", target, "--root", moduleRoot, "--update-lock", "--no-snapshot", "-y", "--json")
	if exit != 0 {
		t.Fatalf("target apply exit=%d: %s %s", exit, stdout, stderr)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || document["ok"] != true {
		t.Fatalf("actual Main did not return successful local apply: %s %v", stdout, err)
	}
	active, err := loadActiveState(stateDir(target))
	if err != nil || active.ActiveDeployment == "" || active.ActiveDeployment == imported || active.RuntimeStatus != "running" {
		t.Fatalf("target did not get a new running deployment: %#v %v", active, err)
	}
	targetApp, _, _, err := loadDeploymentApp(stateDir(target), active.ActiveDeployment, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	targetRegistry, err := targetApp.loadTemporaryRegistry(false)
	if err != nil || targetRegistry.Identity.ID == registry.Identity.ID || len(targetRegistry.Leases) != 1 || targetRegistry.Leases[0].State != "active" || targetRegistry.Leases[0].Path == sourceLease.Path || targetRegistry.AppliedRoot != targetRoot {
		t.Fatalf("target adopted source leases or omitted local activation: %#v %v", targetRegistry, err)
	}
	calls := mustReadFile(t, log)
	if !strings.Contains(string(calls), "--project-name "+targetProject) || strings.Contains(string(calls), "--project-name "+sourceProject) || strings.Contains(string(calls), "-p "+sourceProject) || strings.Contains(string(calls), " down") {
		t.Fatalf("target attempted source or previous runtime mutation: %s", calls)
	}
	afterSource := mustReadFile(t, temporaryStatePath(source.base))
	afterActive := mustReadFile(t, activeStatePath(source.base))
	content := mustReadFile(t, sentinel)
	if string(afterSource) != string(beforeSource) || string(afterActive) != string(beforeActive) || string(content) != "source-content" || sourceRunning && (len(observation.Containers) != 1 || observation.Containers[0].ID != "source-container-id") || !sourceRunning && len(observation.Containers) != 0 {
		t.Fatal("target apply changed source identity, active pointer, container ID, leases, or contents")
	}
	if exists(hookMarker) {
		t.Fatal("target apply ran the imported source's before_stop Hook")
	}
}

func TestTemporaryWorkspaceBindingKeepsLocalSnapshotAndCanonicalAlias(t *testing.T) {
	workspace := temporaryImportedWorkspace(t)
	const local = "local-history"
	writeTemporaryImportedBinding(t, workspace, local, workspace, "local_", filepath.Join(workspace, "tmp"))
	if err := saveActiveState(stateDir(workspace), &activeDeploymentState{ActiveDeployment: local, RuntimeStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	before := mustReadFile(t, activeStatePath(stateDir(workspace)))
	if err := recoverTemporaryTransition(stateDir(workspace), compose.CLI{}, activateOptions{}); err != nil {
		t.Fatal(err)
	}
	after := mustReadFile(t, activeStatePath(stateDir(workspace)))
	if string(before) != string(after) || exists(filepath.Join(stateDir(workspace), "temp")) {
		t.Fatal("same-workspace snapshot state was detached")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	writeTemporaryImportedBinding(t, workspace, local, alias, "local_", filepath.Join(workspace, "tmp"))
	if foreign, err := deploymentWorkspaceIsForeign(stateDir(workspace), local); err != nil || foreign {
		t.Fatalf("same canonical workspace treated as imported: %v %v", foreign, err)
	}
	if _, _, _, err := loadDeploymentApp(stateDir(workspace), local, compose.CLI{}); err != nil {
		t.Fatalf("local frozen runtime rejected: %v", err)
	}
}

func TestTemporaryWorkspaceBindingKeepsValidLocalLease(t *testing.T) {
	a, observation := temporaryTestApp(t)
	const local = "local-history"
	a.artifactRoot = filepath.Join(a.base, "deployments", local, "modules")
	a.env["ANAS_DEPLOYMENT_ID"] = local
	writeTemporaryImportedBinding(t, a.workspace, local, a.workspace, "anas_", filepath.Join(a.workspace, "tmp"))
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	observation.Containers = []temporaryDockerContainer{temporaryTestContainer(a, registry.Leases[0])}
	if err := a.verifyModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(a.base, &activeDeploymentState{ActiveDeployment: local, RuntimeStatus: "running"}); err != nil {
		t.Fatal(err)
	}
	beforeRegistry := mustReadFile(t, temporaryStatePath(a.base))
	beforeActive := mustReadFile(t, activeStatePath(a.base))
	if err := recoverTemporaryTransition(a.base, compose.CLI{}, activateOptions{}); err != nil {
		t.Fatal(err)
	}
	afterRegistry := mustReadFile(t, temporaryStatePath(a.base))
	afterActive := mustReadFile(t, activeStatePath(a.base))
	if string(beforeRegistry) != string(afterRegistry) || string(beforeActive) != string(afterActive) || exists(filepath.Join(a.base, "temp", "foreign-active-imported-"+local+".yml")) {
		t.Fatal("valid local runtime lease was detached or changed")
	}
}

func TestTemporaryWorkspaceBindingRequiresManagedFrozenBinding(t *testing.T) {
	for _, binding := range []string{"missing-file", "missing-data", "relative/data", "/absolute/not-data"} {
		t.Run(binding, func(t *testing.T) {
			workspace := temporaryImportedWorkspace(t)
			const id = "managed-history"
			writeTemporaryImportedBinding(t, workspace, id, workspace, "anas_", filepath.Join(workspace, "tmp"))
			root := filepath.Join(stateDir(workspace), "deployments", id)
			manifest, err := loadDeploymentManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			module := manifest.Modules["demo"]
			module.TemporaryDirectories = []TemporaryDirectory{{Name: "documents", Service: "app", Target: "/temporary", Lifecycle: "container", UID: os.Getuid(), GID: os.Getgid(), Mode: "0700", MinFreeBytes: 1024, MinFreeInodes: 2}}
			manifest.Modules["demo"] = module
			if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "modules", globalEnvFile)
			if binding == "missing-file" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				env := map[string]string{}
				if binding != "missing-data" {
					env["DATA_PATH"] = binding
				}
				if err := writeEnv(path, env); err != nil {
					t.Fatal(err)
				}
			}
			if err := saveActiveState(stateDir(workspace), &activeDeploymentState{ActiveDeployment: id, RuntimeStatus: "stopped"}); err != nil {
				t.Fatal(err)
			}
			before := mustReadFile(t, activeStatePath(stateDir(workspace)))
			if _, err := deploymentWorkspaceIsForeign(stateDir(workspace), id); err == nil {
				t.Fatal("managed deployment obtained authority without a valid frozen binding")
			}
			if _, _, _, err := loadDeploymentApp(stateDir(workspace), id, compose.CLI{}); err == nil {
				t.Fatal("shared loader accepted a managed deployment without its binding")
			}
			if err := recoverTemporaryTransition(stateDir(workspace), compose.CLI{}, activateOptions{}); err == nil {
				t.Fatal("recovery guessed authority for an unbound managed deployment")
			}
			after := mustReadFile(t, activeStatePath(stateDir(workspace)))
			if string(before) != string(after) || exists(temporaryStatePath(stateDir(workspace))) {
				t.Fatal("rejected managed binding changed authorization")
			}
		})
	}
	workspace := temporaryImportedWorkspace(t)
	const legacy = "legacy-history"
	writeTemporaryImportedBinding(t, workspace, legacy, workspace, "anas_", filepath.Join(workspace, "tmp"))
	if err := os.Remove(filepath.Join(stateDir(workspace), "deployments", legacy, "modules", globalEnvFile)); err != nil {
		t.Fatal(err)
	}
	if foreign, err := deploymentWorkspaceIsForeign(stateDir(workspace), legacy); err != nil || foreign {
		t.Fatalf("legacy artifact without temporary declarations lost compatibility: %v %v", foreign, err)
	}
}
