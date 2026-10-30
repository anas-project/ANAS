//go:build linux

// TEST_CASES: MAILMOD-T-083
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/compose"
)

// Explicit isolated-daemon test. It never selects the host's default Docker
// socket and runs only a disposable, networkless container without mounts.
func TestBackupStopConfirmationNative(t *testing.T) {
	if os.Getenv("ANAS_BACKUP_STOP_NATIVE") != "1" {
		t.Skip("explicit isolated backup Docker fixture required")
	}
	const socket = "unix:///run/anas-mail-test-1024.sock"
	const root = "/home/whl/anas-mail-go-test-20261024/docker"
	const image = "ghcr.io/anas-project/anas-postgres:18.4.0-r5"
	const imageID = "sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362"
	if os.Geteuid() != 0 || os.Getenv("DOCKER_HOST") != socket || os.Getenv("DOCKER_CONTEXT") != "" {
		t.Fatal("isolated Docker endpoint and root test user required")
	}
	command := func(args ...string) string {
		t.Helper()
		// Cleanup callbacks run after t.Context is canceled. Keep the bounded
		// Docker cleanup usable so a completed test removes its own container.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("isolated Docker command failed: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if command("info", "--format", "{{.DockerRootDir}}") != root || command("image", "inspect", image, "--format", "{{.Id}}") != imageID {
		t.Fatal("isolated daemon or fixed image differs")
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "anasbk" + hex.EncodeToString(random[:]) + "_"
	project := prefix + "mail"
	workspace := t.TempDir()
	base := filepath.Join(workspace, ".anas")
	release := filepath.Join(base, "deployments", "backup-test", "modules")
	dir := filepath.Join(release, "mail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CONTAINER_PREFIX="+prefix+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config := "services:\n  mail:\n    image: " + image + "\n    entrypoint: [\"/bin/sleep\", \"600\"]\n    network_mode: none\n"
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	composeCommand := func(args ...string) string {
		t.Helper()
		args = append([]string{"compose", "--project-name", project, "--project-directory", dir, "--env-file", filepath.Join(dir, ".env"), "--file", filepath.Join(dir, "docker-compose.yml")}, args...)
		return command(args...)
	}
	t.Cleanup(func() { composeCommand("down", "--volumes", "--remove-orphans") })
	composeCommand("up", "-d", "--no-build")
	cli, err := compose.Detect()
	if err != nil {
		t.Fatal(err)
	}
	a := &app{workspace: workspace, base: base, compose: cli, order: []string{"mail"},
		reg: map[string]Module{"mail": {Name: "mail", RuntimeType: "compose", ComposeFile: "docker-compose.yml"}},
		env: map[string]string{}, envOwner: map[string]string{}, secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}}
	assertState := func(want string) {
		t.Helper()
		out, err := backupComposeObservation(a, dir, "mail", "ps", "--all", "--format", "json")
		if err != nil {
			t.Fatal(err)
		}
		records, err := parseComposePSRecords([]byte(out))
		if err != nil || len(records) != 1 || records[0].State != want {
			t.Fatalf("actual container state not %s: %+v %v", want, records, err)
		}
	}
	assertState("running")
	txn, err := beginContainerTransaction(base, a, release, "backup-test")
	if err != nil {
		t.Fatal("actual stop and readback", err)
	}
	assertState("exited")
	if err = finishContainerTransaction(base, a, release, txn); err != nil {
		t.Fatal("actual restart", err)
	}
	assertState("running")

	for _, mode := range []string{"stop reports success without stopping", "post-stop status unavailable"} {
		t.Run(mode, func(t *testing.T) {
			wrapper := filepath.Join(workspace, "docker-wrapper")
			condition := "*' stop') exit 0 ;;"
			if mode == "post-stop status unavailable" {
				condition = "*' ps --all --format json') exit 91 ;;"
			}
			body := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in %s esac\nexec docker \"$@\"\n", condition)
			if err := os.WriteFile(wrapper, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			wrapped := *a
			wrapped.compose = cli
			wrapped.compose.Bin = []string{wrapper, "compose"}
			txn, err := beginContainerTransaction(base, &wrapped, release, "backup-test")
			if err == nil || txn == nil {
				t.Fatal("unconfirmed real container state permitted capture", err)
			}
			if err := finishContainerTransaction(base, a, release, txn); err != nil {
				t.Fatal("restore after injected failure", err)
			}
			assertState("running")
		})
	}
}
