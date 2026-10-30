// TEST_CASES: MAILMOD-T-083
package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stopped backup must not proceed merely because Compose returned success.
func TestBackupStopRequiresConfirmedStoppedContainers(t *testing.T) {
	for _, tc := range []struct {
		name, response, stop string
		wantError            bool
	}{
		{"exited and created", `printf '%s\n' '{"Service":"mail","State":"exited"}' '{"Service":"unused","State":"created"}'`, "exit 0", false},
		{"JSON array", `printf '%s\n' '[{"Service":"mail","State":"exited"}]'`, "exit 0", false},
		{"no remaining containers", "exit 0", "exit 0", false},
		{"running", `printf '%s\n' '{"Service":"mail","State":"running"}'`, "exit 0", true},
		{"restarting", `printf '%s\n' '{"Service":"mail","State":"restarting"}'`, "exit 0", true},
		{"paused", `printf '%s\n' '{"Service":"mail","State":"paused"}'`, "exit 0", true},
		{"dead", `printf '%s\n' '{"Service":"mail","State":"dead"}'`, "exit 0", true},
		{"missing state", `printf '%s\n' '{"Service":"mail"}'`, "exit 0", true},
		{"missing service", `printf '%s\n' '{"State":"exited"}'`, "exit 0", true},
		{"malformed status", `printf '%s\n' 'not-json'`, "exit 0", true},
		{"trailing malformed status", `printf '%s\n' '[{"Service":"mail","State":"exited"}] broken'`, "exit 0", true},
		{"status unavailable", "exit 19", "exit 0", true},
		{"stop failed", "exit 0", "exit 18", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, release, _ := stopBarrierFixture(t, false)
			a.order = []string{"worker"}
			delete(a.reg, "db")
			if err := os.RemoveAll(filepath.Join(release, "db")); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\ncase \"$*\" in\n  *'ps -q') printf '%s\\n' container1 ;;\n  *'ps --all --format json') " + tc.response + " ;;\n  *'stop') " + tc.stop + " ;;\n  *) exit 90 ;;\nesac\n"
			if err := os.WriteFile(a.compose.Bin[0], []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			txn, err := beginContainerTransaction(a.base, a, release, "test")
			if (err != nil) != tc.wantError || txn == nil {
				t.Fatalf("stop confirmation: txn=%v err=%v", txn, err)
			}
			if err != nil && !strings.Contains(err.Error(), "worker") {
				t.Fatalf("failure does not identify module: %v", err)
			}
		})
	}
}

func TestBackupStopStatusTimeoutAndMissingArtifactFailClosed(t *testing.T) {
	for _, mode := range []string{"status timeout", "stop timeout", "missing artifact", "missing environment", "missing release"} {
		t.Run(mode, func(t *testing.T) {
			a, release, _ := stopBarrierFixture(t, false)
			a.order = []string{"worker"}
			delete(a.reg, "db")
			if err := os.RemoveAll(filepath.Join(release, "db")); err != nil {
				t.Fatal(err)
			}
			if mode == "missing artifact" {
				if err := os.Remove(filepath.Join(release, "worker", "docker-compose.yml")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing environment" {
				if err := os.Remove(filepath.Join(release, "worker", ".env")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing release" {
				if err := os.RemoveAll(release); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasSuffix(mode, "timeout") {
				stop, status := "exit 0", "exit 0"
				if mode == "stop timeout" {
					stop = "exec sleep 20"
				} else {
					status = "exec sleep 20"
				}
				script := "#!/bin/sh\ncase \"$*\" in *'ps -q') printf 'container1\\n' ;; *'ps --all --format json') " + status + " ;; *'stop') " + stop + " ;; *) exit 90 ;; esac\n"
				if err := os.WriteFile(a.compose.Bin[0], []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				a.commandContext = ctx
			}
			_, err := beginContainerTransaction(a.base, a, release, "test")
			if err == nil {
				t.Fatal("unconfirmed state allowed backup capture")
			}
			if strings.HasSuffix(mode, "timeout") && !strings.Contains(err.Error(), "deadline exceeded") {
				t.Fatalf("timeout not surfaced: %v", err)
			}
		})
	}
}

func TestBackupStopRejectsReleaseChangeAndInitialQueryFailure(t *testing.T) {
	for _, mode := range []string{"release changes after stop", "environment disappears after stop", "initial query fails"} {
		t.Run(mode, func(t *testing.T) {
			a, release, _ := stopBarrierFixture(t, false)
			a.order = []string{"worker"}
			delete(a.reg, "db")
			if err := os.RemoveAll(filepath.Join(release, "db")); err != nil {
				t.Fatal(err)
			}
			query, stop := "printf 'container1\\n'", "rm '"+filepath.Join(release, "worker", "docker-compose.yml")+"'"
			if mode == "environment disappears after stop" {
				stop = "rm '" + filepath.Join(release, "worker", ".env") + "'"
			}
			if mode == "initial query fails" {
				query = "exit 91"
			}
			script := "#!/bin/sh\ncase \"$*\" in *'ps -q') " + query + " ;; *'ps --all --format json') exit 0 ;; *'stop') " + stop + " ;; *) exit 90 ;; esac\n"
			if err := os.WriteFile(a.compose.Bin[0], []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			txn, err := beginContainerTransaction(a.base, a, release, "test")
			if err == nil {
				t.Fatal("unknown or changed release allowed backup capture")
			}
			if mode == "initial query fails" && txn != nil {
				t.Fatal("transaction created despite initial query failure")
			}
			if mode != "initial query fails" && txn == nil {
				t.Fatal("changed release discarded recovery transaction")
			}
		})
	}
}
