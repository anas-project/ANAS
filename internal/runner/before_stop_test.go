package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
)

func TestBeforeStopCannotReceiveSecretsFromALaterGeneration(t *testing.T) {
	a, release, _ := stopBarrierFixture(t, false)
	dir := filepath.Join(release, "worker")
	const oldPassword = "frozen-generation-password"
	const newPassword = "subsequent-generation-password"
	a.secrets.values["WORKER_PASSWORD"] = newPassword
	a.secrets.metadata["WORKER_PASSWORD"] = secretMetadata{Owner: "worker"}
	a.env["WORKER_PASSWORD"] = newPassword
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("WORKER_MODE=old-enabled\nWORKER_PASSWORD="+oldPassword+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook"), []byte("#!/bin/sh\ncat > observed-stop-input.json\nprintf '{}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.beforeStopModule(release, "worker"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "observed-stop-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request hookRequest
	if json.Unmarshal(body, &request) != nil || request.Phase != "before_stop" ||
		request.Env["WORKER_PASSWORD"] != oldPassword || len(request.Secrets) != 0 ||
		strings.Contains(string(body), newPassword) {
		t.Fatal("old cleanup received current Secret Store data instead of only its frozen deployment projection")
	}
	if a.secrets.values["WORKER_PASSWORD"] != newPassword || a.env["WORKER_PASSWORD"] != newPassword {
		t.Fatal("stopping the old deployment modified the current desired credential")
	}
}

// A real Hook subprocess, but no Docker operations. The native consumer test
// separately exercises the same stop path with real Compose containers.
func stopBarrierFixture(t *testing.T, fail bool) (*app, string, string) {
	t.Helper()
	root := t.TempDir()
	release, log := filepath.Join(root, "frozen"), filepath.Join(root, "calls")
	command := filepath.Join(root, "compose")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf 'compose:%s\\n' \"$*\" >> '"+log+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	a := &app{base: filepath.Join(root, ".anas"), workspace: root, useFrozenHooks: true,
		compose: compose.CLI{Bin: []string{command}}, reg: map[string]Module{}, order: []string{"db", "worker"},
		env: map[string]string{"WORKER_MODE": "new-disabled"}, envOwner: map[string]string{},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}}
	for _, name := range a.order {
		dir := filepath.Join(release, name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for file, body := range map[string]string{".env": "WORKER_MODE=old-enabled\n", "docker-compose.yml": "services: {}\n"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
		}
		hook := filepath.Join(dir, "hook")
		body := "#!/bin/sh\ninput=$(cat)\ncase \"$input\" in *'\"phase\":\"before_stop\"'*'old-enabled'*) ;; *) exit 13 ;; esac\nprintf 'before:" + name + "\\n' >> '" + log + "'\n"
		if fail && name == "worker" {
			body += "exit 17\n"
		} else {
			body += "printf '{}'\n"
		}
		if err := os.WriteFile(hook, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
		a.reg[name] = Module{Name: name, RuntimeType: "compose", ComposeFile: "docker-compose.yml", SourceDir: dir,
			Hook: HookConfig{Command: []string{hook}, Phases: []string{"before_stop"}}}
	}
	previous := inspectComposeProjectOwners
	previousBounded := inspectComposeProjectOwnersBounded
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	inspectComposeProjectOwnersBounded = func(ctx context.Context, env []string, project string) ([]string, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("stop ownership query has no execution deadline")
		}
		return inspectComposeProjectOwners(project)
	}
	t.Cleanup(func() { inspectComposeProjectOwners = previous; inspectComposeProjectOwnersBounded = previousBounded })
	return a, release, log
}

func TestStopFailureRecoveryDoesNotRestartAnUnconfirmedCleaner(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		base := t.TempDir()
		cause := error(errors.New("ordinary compose failure"))
		if blocked {
			cause = &stopBarrierFailure{Module: "worker", Cause: errors.New("cleanup uncertain")}
		}
		restored := false
		err := recordStopFailure(base, "candidate", cause, func() error { restored = true; return nil })
		state, readErr := loadDeploymentState(base, "candidate")
		if err == nil || readErr != nil || state.Status != "failed" || restored == blocked {
			t.Fatal("stop failure did not preserve primary failure and safe recovery boundary", err, readErr)
		}
	}
}

func TestBackupPendingStopSurvivesReopenWithoutAutomaticStart(t *testing.T) {
	a, release, log := stopBarrierFixture(t, false)
	txn := &containerTransaction{APIVersion: activeStateVersion, ID: "pending-stop", Kind: containerTransactionKind,
		Workspace: a.workspace, DeploymentID: "old", Modules: a.order, State: containerTransactionStopped, CleanupPending: "worker"}
	path := transactionPath(a.base, txn.ID)
	if err := writeYAMLAtomic(path, txn, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if finishContainerTransaction(a.base, a, release, txn) == nil {
		t.Fatal("uncertain cleanup was restarted in-process")
	}
	if compensateContainerTransactionsWithOptions(a.base, runtimeRecoveryOptions{}) == nil {
		t.Fatal("reopened uncertain cleanup was treated as recoverable start")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("recovery removed or rewrote the pending cleanup evidence")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("compensation started containers despite pending cleanup")
	}
}

func TestPendingCleanupUsesANonRestartableJournalState(t *testing.T) {
	base := t.TempDir()
	txn := &containerTransaction{APIVersion: activeStateVersion, ID: "pending-cleanup", Kind: containerTransactionKind,
		Modules: []string{"worker"}, State: containerTransactionStopped}
	if err := txn.setCleanupPending(base, "worker"); err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		State string `yaml:"state"`
	}
	if err := readYAML(transactionPath(base, txn.ID), &legacy); err != nil || legacy.State == containerTransactionStopped {
		t.Fatal("a reader that ignores the new field could automatically restart unconfirmed cleanup", err)
	}
	if err := txn.setCleanupPending(base, ""); err != nil || txn.State != containerTransactionStopped || txn.CleanupPending != "" {
		t.Fatal("verified completion did not restore the ordinary backup recovery state", err)
	}
	// Even a damaged pending record that has lost its optional module field
	// cannot become a silently ignored successful recovery.
	txn.State = "cleanup_pending"
	if err := writeYAMLAtomic(transactionPath(base, txn.ID), txn, 0600); err != nil {
		t.Fatal(err)
	}
	if err := compensateContainerTransactionsWithOptions(base, runtimeRecoveryOptions{}); err == nil {
		t.Fatal("pending journal state was ignored as an unrelated transaction")
	}
}

func TestBackupCleanupMarkerPrecedesEffectsAndIsNotClearedOnFailure(t *testing.T) {
	for _, failure := range []string{"none", "hook", "journal"} {
		t.Run(failure, func(t *testing.T) {
			a, release, log := stopBarrierFixture(t, failure == "hook")
			marks := []string{}
			err := stopModulesWithCleanupMarker(a, release, []string{"worker"}, func(name string) error {
				if len(marks) == 0 {
					if _, e := os.Stat(log); !os.IsNotExist(e) {
						t.Fatal("Hook or Compose ran before durable cleanup intent")
					}
				}
				marks = append(marks, name)
				if failure == "journal" {
					return errors.New("journal unavailable")
				}
				return nil
			})
			if (err != nil) != (failure != "none") || len(marks) == 0 || marks[0] != "worker" ||
				(failure == "none" && (len(marks) != 2 || marks[1] != "")) || (failure != "none" && len(marks) != 1) {
				t.Fatal("cleanup marker did not preserve an interrupted operation", err, marks)
			}
		})
	}
}

func TestBeforeStopRunsWithFrozenInputsBeforeRemoval(t *testing.T) {
	for _, kind := range []string{"selected", "release", "backup"} {
		t.Run(kind, func(t *testing.T) {
			a, release, log := stopBarrierFixture(t, false)
			var err error
			switch kind {
			case "selected":
				err = a.stopModules(release, a.order, false)
			case "release":
				err = a.stopRelease(release, false)
			case "backup":
				err = stopModules(a, release, a.order)
			}
			body, readErr := os.ReadFile(log)
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			if err != nil || readErr != nil || len(lines) != 4 || lines[0] != "before:worker" || lines[2] != "before:db" ||
				!strings.HasPrefix(lines[1], "compose:") || !strings.HasPrefix(lines[3], "compose:") {
				t.Fatal("stop did not retain dependencies through the frozen Hook barrier", err, readErr, string(body))
			}
		})
	}
}

func TestFailedStopBarrierPreservesModuleAndItsDependencies(t *testing.T) {
	for _, kind := range []string{"selected", "release", "backup"} {
		t.Run(kind, func(t *testing.T) {
			a, release, log := stopBarrierFixture(t, true)
			var err error
			switch kind {
			case "selected":
				err = a.stopModules(release, a.order, false)
			case "release":
				err = a.stopRelease(release, false)
			case "backup":
				err = stopModules(a, release, a.order)
			}
			body, readErr := os.ReadFile(log)
			if err == nil || readErr != nil || string(body) != "before:worker\n" {
				t.Fatal("uncertain cleanup allowed Compose removal or stopped a dependency", err, readErr, string(body))
			}
		})
	}
}

func TestStopBarrierIsExplicitAndHonorsSelectionAndCancellation(t *testing.T) {
	if hookSupportsPhase(HookConfig{Command: []string{"legacy-hook"}}, "before_stop") {
		t.Fatal("a legacy no-op branch is not a declared stop barrier")
	}
	if !knownModuleHookPhases["before_stop"] {
		t.Fatal("manifest cannot declare the new fixed lifecycle phase")
	}
	a, release, log := stopBarrierFixture(t, false)
	if err := a.stopModules(release, []string{"worker"}, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(log)
	if err != nil || strings.Contains(string(body), "before:db") {
		t.Fatal("selected stop reached another module")
	}
	before := string(body)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.commandContext = ctx
	if err := a.stopModules(release, []string{"worker"}, false); err == nil {
		t.Fatal("canceled stop succeeded")
	}
	body, err = os.ReadFile(log)
	if err != nil || string(body) != before {
		t.Fatal("canceled stop ran a child")
	}
}

func TestBeforeStopRejectsForeignComposeOwnerBeforeRunningHook(t *testing.T) {
	a, release, log := stopBarrierFixture(t, false)
	foreign := filepath.Join(t.TempDir(), ".anas", "deployments", "old", "modules", "worker")
	inspectComposeProjectOwners = func(string) ([]string, error) { return []string{foreign}, nil }
	if err := a.stopModules(release, []string{"worker"}, false); err == nil {
		t.Fatal("foreign workspace was accepted")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("the stop Hook ran before workspace ownership was verified")
	}
}

func TestBeforeStopRejectsAContainerWithNoWorkspaceLabel(t *testing.T) {
	a, release, log := stopBarrierFixture(t, false)
	inspectComposeProjectOwners = func(string) ([]string, error) { return parseComposeProjectOwners([]byte("\n")), nil }
	if err := a.stopModules(release, []string{"worker"}, false); err == nil {
		t.Fatal("a present container with missing ownership was treated as an empty project")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("unowned container was reached by a Hook or Compose mutation")
	}
}

func TestRemovedModuleCannotBypassItsDeclaredStopBarrier(t *testing.T) {
	a, release, log := stopBarrierFixture(t, true)
	a.order = []string{"db"}
	if err := a.stopRemoved(release); err == nil {
		t.Fatal("removed module bypassed cleanup")
	}
	body, err := os.ReadFile(log)
	if err != nil || string(body) != "before:worker\n" {
		t.Fatal("failed removed-module cleanup proceeded to Compose or network removal", err, string(body))
	}
}

func TestBeforeStopNeverFallsBackToNewEnvironment(t *testing.T) {
	a, release, log := stopBarrierFixture(t, false)
	if err := os.Remove(filepath.Join(release, "worker", ".env")); err != nil {
		t.Fatal(err)
	}
	if err := a.stopModules(release, []string{"worker"}, false); err == nil {
		t.Fatal("missing old projection did not block stopping")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("missing old projection invoked the Hook or Compose")
	}
}

func TestActualStopHookRequiresEmptyResponseAndDoesNotEchoSecrets(t *testing.T) {
	for _, response := range []string{"", "null", `{"unknown":"private-stop-marker"}`,
		`{"env":{"WORKER_MODE":"mutated"}}`, `{"secrets":{"x":"private-stop-marker"}}`,
		`{"warnings":["private-stop-marker"]}`, `{"env":{"x":"private-stop-marker"},"env":{}}`,
		`{} {}`, strings.Repeat("x", 65537)} {
		t.Run(response[:min(len(response), 28)], func(t *testing.T) {
			a, release, log := stopBarrierFixture(t, false)
			dir := filepath.Join(release, "worker")
			if err := os.WriteFile(filepath.Join(dir, "reply"), []byte(response), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hook"), []byte("#!/bin/sh\ncat >/dev/null\nprintf private-stop-marker >&2\ncat reply\n"), 0700); err != nil {
				t.Fatal(err)
			}
			err := a.stopModules(release, []string{"worker"}, false)
			if err == nil || strings.Contains(err.Error(), "private-stop-marker") {
				t.Fatal("stop response was accepted or echoed private material")
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("invalid stop response allowed Compose mutation")
			}
		})
	}
}
