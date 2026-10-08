package runner

// TEST_CASES: TEMP-T-008

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"gopkg.in/yaml.v3"
)

func temporaryStopFaultFixture(t *testing.T) (*app, string, string) {
	t.Helper()
	temporaryTestApp(t) // Bind private filesystem/Docker test observations.
	a, release, log := stopBarrierFixture(t, false)
	canonical, err := filepath.EvalSymlinks(a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.workspace, a.base = canonical, stateDir(canonical)
	a.env["ANAS_DEPLOYMENT_ID"] = "previous"
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		t.Fatal(err)
	}
	registry.Transition = &TemporaryTransition{FromDeployment: "previous", TargetDeployment: "candidate", FromRoot: filepath.Join(canonical, "tmp"), TargetRoot: filepath.Join(canonical, "new-temp"), Phase: "stopping"}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	return a, release, log
}

func TestTemporaryStopHookWriteAheadPrecedesEffectsAndClearsOnSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "failure", false: "success"}[fail], func(t *testing.T) {
			a, release, _ := temporaryStopFaultFixture(t)
			dir := filepath.Join(release, "worker")
			body := "#!/bin/sh\ncat > /dev/null\ncp '" + temporaryStatePath(a.base) + "' observed-state.yml\n"
			if fail {
				body += "exit 17\n"
			} else {
				body += "printf '{}'\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "hook"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			err := a.beforeStopModule(release, "worker")
			if (err != nil) != fail {
				t.Fatalf("stop Hook result: %v", err)
			}
			var observed temporaryRegistry
			if err := readYAML(filepath.Join(dir, "observed-state.yml"), &observed); err != nil {
				t.Fatal(err)
			}
			if observed.Transition.Phase != "cleanup_pending" || observed.Transition.CleanupModule != "worker" || observed.Transition.CleanupDeployment != "previous" {
				t.Fatalf("Hook effects preceded durable cleanup intent: %+v", observed.Transition)
			}
			registry, err := a.loadTemporaryRegistry(false)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if registry.Transition.Phase != "cleanup_pending" || registry.Transition.CleanupModule != "worker" {
					t.Fatal("failed Hook lost its cleanup evidence")
				}
				before, _ := os.ReadFile(temporaryStatePath(a.base))
				if err := recoverTemporaryTransition(a.base, a.compose, activateOptions{}); err == nil || !strings.Contains(err.Error(), "unconfirmed") {
					t.Fatalf("failed cleanup was automatically resumed: %v", err)
				}
				after, _ := os.ReadFile(temporaryStatePath(a.base))
				if string(before) != string(after) {
					t.Fatal("recovery rewrote uncertain cleanup evidence")
				}
			} else if registry.Transition.Phase != "stopping" || registry.Transition.CleanupModule != "" {
				t.Fatal("successful cleanup did not clear its pending marker")
			}
		})
	}
}

func TestTemporaryPendingStopSurvivesReopenAndCannotBeBlessedByRecoveryReport(t *testing.T) {
	a, _, log := temporaryStopFaultFixture(t)
	marked, err := a.beginTemporaryStopCleanup("worker")
	if err != nil || !marked {
		t.Fatalf("write cleanup marker: %v %v", marked, err)
	}
	reopened := *a
	if err := recoverTemporaryTransition(a.base, a.compose, activateOptions{}); err == nil {
		t.Fatal("interrupted cleanup auto-started previous deployment")
	}
	if err := startDeployment(&reopened, "irrelevant", reopened.order, false); err == nil || !strings.Contains(err.Error(), "unconfirmed") {
		t.Fatalf("in-process recovery bypassed persistent cleanup marker: %v", err)
	}
	recordTemporaryRecoveryPhase(&reopened, &CLIError{Detail: map[string]any{"recovery": []map[string]any{{"phase": "previous_restore", "status": "succeeded"}}}})
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil || registry.Transition.Phase != "cleanup_pending" || registry.Transition.CleanupModule != "worker" {
		t.Fatalf("recovery report erased pending stop: %+v %v", registry, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("recovery executed a container or Hook effect")
	}
}

func TestTemporaryCopiedRuntimeNeverRecoversOrStopsSource(t *testing.T) {
	a, observation := temporaryTestApp(t)
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	observation.Containers = []temporaryDockerContainer{temporaryTestContainer(a, registry.Leases[0])}
	registry.Transition = &TemporaryTransition{FromDeployment: "source-active", TargetDeployment: "source-candidate", Phase: "cleanup_pending", CleanupModule: "demo", CleanupDeployment: "source-active"}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(a.base, &activeDeploymentState{APIVersion: activeStateVersion, ActiveDeployment: "source-active", RuntimeStatus: "running"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(temporaryStatePath(a.base))
	if err != nil {
		t.Fatal(err)
	}
	clone, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := copyDir(a.base, stateDir(clone)); err != nil {
		t.Fatal(err)
	}
	plan, err := temporarySwitchPlan(clone, filepath.Join(clone, "tmp"), []string{"demo"})
	if err != nil || plan.Required || len(plan.StopModules) != 0 {
		t.Fatalf("copy planned source cleanup: %+v %v", plan, err)
	}
	// Recovery must not even query Docker while detaching copied source state.
	previousObserver := temporaryDockerObservation
	temporaryDockerObservation = func(*app) (temporaryDockerSnapshot, error) {
		t.Fatal("copy recovery queried source Docker state")
		return temporaryDockerSnapshot{}, errors.New("unreachable")
	}
	defer func() { temporaryDockerObservation = previousObserver }()
	if err := recoverTemporaryTransition(stateDir(clone), a.compose, activateOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *a
	b.workspace, b.base = clone, stateDir(clone)
	fresh, err := b.loadTemporaryRegistry(false)
	if err != nil || fresh.Identity.ID == registry.Identity.ID || len(fresh.Leases) != 0 || fresh.Transition != nil || fresh.AppliedRoot != "" {
		t.Fatalf("copy adopted source temporary authority: %+v %v", fresh, err)
	}
	active, err := loadActiveState(b.base)
	if err != nil || active.ActiveDeployment != "" || active.RuntimeStatus != "stopped" {
		t.Fatalf("copy retained running source deployment: %+v %v", active, err)
	}
	if _, err := os.Stat(filepath.Join(b.base, "temp", "foreign-active-"+registry.Identity.ID+".yml")); err != nil {
		t.Fatal("copied active metadata was not preserved", err)
	}
	if err := recoverTemporaryTransition(b.base, b.compose, activateOptions{}); err != nil {
		t.Fatal("detached copy is not restartable", err)
	}
	after, err := os.ReadFile(temporaryStatePath(a.base))
	if err != nil || string(after) != string(before) || len(observation.Containers) != 1 {
		t.Fatal("copy recovery changed source registration or containers")
	}
	if _, err := os.Stat(registry.Leases[0].Path); err != nil {
		t.Fatal("copy recovery changed source temporary content", err)
	}
}

func TestTemporaryRecoveryRequiresExplicitSuccessfulRestoreEvidence(t *testing.T) {
	a, _, _ := temporaryStopFaultFixture(t)
	for _, report := range []error{errors.New("untyped failure"), &CLIError{Code: "failed"}, &CLIError{Detail: map[string]any{"recovery": []map[string]any{}}}} {
		recordTemporaryRecoveryPhase(a, report)
		registry, err := a.loadTemporaryRegistry(false)
		if err != nil || registry.Transition.Phase != "recovery_failed" {
			t.Fatalf("missing recovery evidence treated as restored: %+v %v", registry, err)
		}
	}
	recordTemporaryRecoveryPhase(a, &CLIError{Detail: map[string]any{"recovery": []map[string]any{{"phase": "candidate_stop", "status": "succeeded"}, {"phase": "previous_restore", "status": "succeeded"}}}})
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil || registry.Transition.Phase != "restored" {
		t.Fatalf("verified recovery not recorded: %+v %v", registry, err)
	}
}

func TestTemporaryUnknownTransitionPhasePreservesEvidence(t *testing.T) {
	a, _, _ := temporaryStopFaultFixture(t)
	registry, _ := a.loadTemporaryRegistry(false)
	registry.Transition.Phase = "newer-unsupported-phase"
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(temporaryStatePath(a.base))
	if err := recoverTemporaryTransition(a.base, a.compose, activateOptions{}); err == nil {
		t.Fatal("unknown phase automatically resumed")
	}
	after, _ := os.ReadFile(temporaryStatePath(a.base))
	if string(before) != string(after) {
		t.Fatal("unknown phase evidence changed")
	}
}

func TestTemporaryCandidateReleaseChecksUnregisteredWrongMountContainers(t *testing.T) {
	a, observation := temporaryTestApp(t)
	if _, err := a.loadTemporaryRegistry(true); err != nil {
		t.Fatal(err)
	}
	container := temporaryDockerContainer{ID: "unverified-wrong-mount"}
	container.Config.Labels = map[string]string{"com.docker.compose.project": "anas_demo", "com.docker.compose.service": "app"}
	observation.Containers = []temporaryDockerContainer{container}
	if err := a.confirmTemporaryCandidateReleased(); err == nil {
		t.Fatal("container without successful lease registration was overlooked")
	}
	observation.Containers = nil
	if err := a.confirmTemporaryCandidateReleased(); err != nil {
		t.Fatal(err)
	}
}

func TestTemporaryCandidateFailurePreservesPreviousLeaseForCompensation(t *testing.T) {
	a, observation := temporaryTestApp(t)
	a.env["TEMP_PATH"] = filepath.Join(a.workspace, "old-temp")
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	old := registry.Leases[0]
	observation.Containers = []temporaryDockerContainer{temporaryTestContainer(a, old)}
	if err := a.verifyModuleTemporaryStorage("demo"); err != nil {
		t.Fatal(err)
	}
	const sentinel = "preserve-this-old-content"
	if err := os.WriteFile(filepath.Join(old.Path, "rollback-sentinel"), []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	// The old deployment was stopped with retention before a candidate's up
	// failed. Both generations are registered, but neither has a user now.
	observation.Containers = nil
	candidate := *a
	candidate.env = cloneMap(a.env)
	candidate.env["TEMP_PATH"] = filepath.Join(a.workspace, "new-temp")
	candidate.artifactRoot = filepath.Join(a.base, "deployments", "candidate", "modules")
	mod := a.reg["demo"]
	mod.SourceDir = filepath.Join(candidate.artifactRoot, "demo")
	candidate.reg = map[string]Module{"demo": mod}
	if err := os.MkdirAll(mod.SourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"docker-compose.yml": "services: {}\n", ".env": "CONTAINER_PREFIX=anas_\n"} {
		if err := os.WriteFile(filepath.Join(mod.SourceDir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := filepath.Join(a.workspace, "fake-compose")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	candidate.compose = compose.CLI{Bin: []string{command}}
	previousOwners := inspectComposeProjectOwners
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	t.Cleanup(func() { inspectComposeProjectOwners = previousOwners })
	if err := candidate.prepareModuleTemporaryStorage(mod); err != nil {
		t.Fatal(err)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil {
		t.Fatal(err)
	}
	registry.Transition = &TemporaryTransition{Phase: "starting", FromDeployment: old.DeploymentID, TargetDeployment: "candidate", FromRoot: old.Root, TargetRoot: candidate.env["TEMP_PATH"]}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	failure := failTemporaryActivation(a.base, "candidate", "start_failed", errors.New("synthetic up failure"), &candidate, candidate.artifactRoot, nil, "", false)
	if failure.Code != "start_failed" {
		t.Fatal(failure)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil || len(registry.Leases) != 2 || registry.Leases[0].State != "active" || registry.Leases[1].State != "released" {
		t.Fatalf("candidate cleanup released compensation authority: %+v %v", registry, err)
	}
	// The previous start must reuse the exact old lease, not allocate another
	// generation whose empty directory hides preserved old application content.
	if err := a.prepareModuleTemporaryStorage(a.reg["demo"]); err != nil {
		t.Fatal(err)
	}
	environment, err := a.temporaryComposeEnvironment("demo")
	if err != nil || environment[temporaryDirectoryEnvironmentKey(old.Name)] != old.Path {
		t.Fatalf("compensation selected a new directory: %+v %v", environment, err)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil || len(registry.Leases) != 2 {
		t.Fatal("compensation allocated an unnecessary generation", err)
	}
	if content, err := os.ReadFile(filepath.Join(old.Path, "rollback-sentinel")); err != nil || string(content) != sentinel {
		t.Fatal("candidate cleanup changed old application content", err)
	}
}

func TestTemporaryPendingMarkerStateCannotMasqueradeAsRestartablePhase(t *testing.T) {
	a, _, _ := temporaryStopFaultFixture(t)
	if _, err := a.beginTemporaryStopCleanup("worker"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(temporaryStatePath(a.base))
	var legacy struct {
		Transition struct {
			Phase string `yaml:"phase"`
		} `yaml:"transition"`
	}
	if yaml.Unmarshal(body, &legacy) != nil || legacy.Transition.Phase != "cleanup_pending" {
		t.Fatal("a reader ignoring optional fields could resume unconfirmed cleanup")
	}
}

func TestTemporaryPreparedPlanRecoveryDoesNotRestartUntouchedServices(t *testing.T) {
	a, _ := temporaryTestApp(t)
	registry, err := a.loadTemporaryRegistry(true)
	if err != nil {
		t.Fatal(err)
	}
	registry.Transition = &TemporaryTransition{FromDeployment: "previous", TargetDeployment: "candidate", Phase: "prepared"}
	if err := a.saveTemporaryRegistry(registry); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(a.base, &activeDeploymentState{APIVersion: activeStateVersion, ActiveDeployment: "previous", RuntimeStatus: "running"}); err != nil {
		t.Fatal(err)
	}
	// No deployment artifacts or executable are provided: recovery must only
	// abandon the plan, not invoke startup or require the old storage root.
	if err := recoverTemporaryTransition(a.base, a.compose, activateOptions{}); err != nil {
		t.Fatal(err)
	}
	registry, err = a.loadTemporaryRegistry(false)
	if err != nil || registry.Transition.Phase != "restored" {
		t.Fatalf("prepared plan not abandoned safely: %+v %v", registry, err)
	}
}
