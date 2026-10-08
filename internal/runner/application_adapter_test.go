package runner

// TEST_CASES: TEMP-T-008, TEMP-T-015

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

func TestApplicationCLIErrorPreservesDetailAndEstablishedExitCodes(t *testing.T) {
	for _, test := range []struct {
		kind application.ErrorKind
		code string
		exit int
	}{
		{application.ErrorKindInvalidArgument, "invalid_argument", exitUsage},
		{application.ErrorKindPreconditionRequired, "confirmation_required", exitConfirmation},
		{application.ErrorKindFailedPrecondition, "guarded_changes", exitPrecondition},
		{application.ErrorKindNotFound, "deployment_missing", exitPrecondition},
		{application.ErrorKindInternal, "state_unreadable", exitPrecondition},
		{application.ErrorKindInternal, "stop_failed", exitFailure},
	} {
		t.Run(test.code, func(t *testing.T) {
			detail := map[string]any{"primary": map[string]any{"code": test.code}, "recovery": []map[string]any{{"phase": "previous_restore", "status": "failed"}}}
			err := applicationCLIError(&application.Error{Kind: test.kind, Code: test.code, Message: "failure", Detail: detail})
			cli, ok := err.(*CLIError)
			if !ok || cli.Exit != test.exit || cli.Detail["primary"] == nil || cli.Detail["recovery"] == nil {
				t.Fatalf("application detail or exit code lost: %#v", err)
			}
			cli.Detail["only_cli"] = true
			if detail["only_cli"] != nil {
				t.Fatal("CLI detail aliases the application envelope")
			}
		})
	}
}

// Exercise the actual Main -> application service -> CLI adapter -> JSON
// boundary. A direct activation-helper test cannot catch an adapter dropping
// details while leaving the human-readable primary/recovery text intact.
func TestTemporaryCLIApplyJSONPreservesStopPrimaryAndIndependentRecovery(t *testing.T) {
	for _, restorationFails := range []bool{false, true} {
		name := map[bool]string{false: "restore_succeeded", true: "restore_failed"}[restorationFails]
		t.Run(name, func(t *testing.T) {
			workspace := newWorkspace(t)
			base := stateDir(workspace)
			const previous, candidate, project = "previous", "candidate", "anas_temp_json_test_worker"
			for _, id := range []string{previous, candidate} {
				root := filepath.Join(base, "deployments", id)
				dir := filepath.Join(root, "modules", "worker")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"worker"}, Modules: map[string]deploymentModule{
					"worker": {Name: "worker", RuntimeType: "compose", ComposeFile: "docker-compose.yml", RenderDigest: id},
				}}
				if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
					t.Fatal(err)
				}
				for file, body := range map[string]string{".env": "CONTAINER_PREFIX=anas_temp_json_test_\n", "docker-compose.yml": "services:\n  app:\n    image: fixture\n"} {
					if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
				status := "ready"
				if id == previous {
					status = "active"
				}
				if err := saveDeploymentState(base, deploymentState{ID: id, Status: status}); err != nil {
					t.Fatal(err)
				}
			}
			if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: previous, RuntimeStatus: "running"}); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			log := filepath.Join(bin, "calls")
			marker := filepath.Join(bin, "down-failed")
			up := "exit 0"
			if restorationFails {
				up = "printf 'independent restore failure\\n' >&2; exit 99"
			}
			body := "#!/bin/sh\n" +
				"[ \"$1\" = ps ] && exit 0\n" +
				"[ \"$1 $2\" = 'compose version' ] && exit 0\n" +
				"printf '%s:%s\\n' \"$PWD\" \"$*\" >> '" + log + "'\n" +
				"case \"$*\" in\n" +
				"  *' down') if [ ! -e '" + marker + "' ]; then touch '" + marker + "'; printf 'primary stop failure\\n' >&2; exit 98; fi;;\n" +
				"  *'config --services') printf 'app\\n';;\n" +
				"  *' up '*) " + up + ";;\n" +
				"esac\nexit 0\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			stdout, _, exit := capture(t, "apply", "-w", workspace, "--deployment", candidate, "--yes", "--no-snapshot", "--json")
			if exit != exitFailure {
				t.Fatalf("apply exit = %d, want %d; %s", exit, exitFailure, stdout)
			}
			document := requireFailureDocument(t, "apply stop failure", stdout)
			failure := document["error"].(map[string]any)
			if failure["code"] != "stop_failed" {
				t.Fatalf("recovery replaced primary code: %#v", failure)
			}
			detail, ok := failure["detail"].(map[string]any)
			if !ok {
				t.Fatalf("CLI adapter lost failure detail: %#v", failure)
			}
			primary, ok := detail["primary"].(map[string]any)
			if !ok || primary["code"] != "stop_failed" || !strings.Contains(primary["message"].(string), "primary stop failure") {
				t.Fatalf("lost initiating stop failure: %#v", detail)
			}
			command, ok := primary["command"].(map[string]any)
			if !ok || command["phase"] != "down" || command["exit_code"] != float64(98) || command["project"] != project {
				t.Fatalf("lost Compose primary command: %#v", primary)
			}
			recovery, ok := detail["recovery"].([]any)
			if !ok || len(recovery) != 1 {
				t.Fatalf("lost independent recovery: %#v", detail)
			}
			outcome, ok := recovery[0].(map[string]any)
			status := map[bool]string{false: "succeeded", true: "failed"}[restorationFails]
			if !ok || outcome["phase"] != "previous_restore" || outcome["status"] != status {
				t.Fatalf("recovery outcome = %#v, want previous_restore %s", outcome, status)
			}
			if restorationFails && !strings.Contains(outcome["message"].(string), "independent restore failure") {
				t.Fatalf("restore failure text lost: %#v", outcome)
			}
			active, err := loadActiveState(base)
			if err != nil || active.ActiveDeployment != previous {
				t.Fatalf("failed apply changed active pointer: %#v %v", active, err)
			}
			persisted, err := loadDeploymentState(base, candidate)
			if err != nil || persisted.Status != "failed" || persisted.FailureDetail["primary"] == nil || persisted.FailureDetail["recovery"] == nil {
				t.Fatalf("failure detail not persisted independently: %#v %v", persisted, err)
			}
			calls, err := os.ReadFile(log)
			if err != nil || strings.Count(string(calls), " down\n") != 1 || strings.Count(string(calls), " up ") != 1 || strings.Contains(string(calls), filepath.Join(base, "deployments", candidate, "modules")+string(os.PathSeparator)) {
				t.Fatalf("recovery did not operate only previous deployment: %s (%v)", calls, err)
			}
		})
	}
}
