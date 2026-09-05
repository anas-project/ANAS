package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

// The fixture is only useful if the production service accepts it. Building the
// workspace and then driving the real ModuleCommandService over it is what
// keeps the E2E from passing against a workspace the daemon would reject for an
// unrelated reason.
func TestFixtureWorkspaceDrivesTheRealModuleCommandService(t *testing.T) {
	workspace := t.TempDir()
	if err := materialize(workspace); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(ModuleRootFor(workspace), "invocations.log")
	// A value only reachable by inheriting the caller's environment. The service
	// builds the executor environment explicitly, so it must not arrive.
	t.Setenv("ANAS_E2E_LEAK_PROBE", "must-not-reach-the-executor")

	service := application.NewService(workspace)
	listed, err := service.ListModuleCommands(context.Background(), application.ListModuleCommandsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Commands) != 2 {
		t.Fatalf("commands = %d, want the normal and destructive pair", len(listed.Commands))
	}
	safe, err := service.GetModuleCommand(context.Background(), application.GetModuleCommandRequest{
		Module: "demo", Command: "reindex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if safe.Command.Risk != "normal" || !safe.Available {
		t.Fatalf("reindex = risk %q available %v reason %q", safe.Command.Risk, safe.Available, safe.UnavailableReason)
	}

	// A stale digest must be rejected before anything runs; that is the binding
	// the HTTP route depends on.
	if _, err := service.InvokeModuleCommand(context.Background(), application.InvokeModuleCommandRequest{
		Module: "demo", Command: "reindex", CommandDigest: "sha256:deadbeef", Confirmed: true,
	}); err == nil {
		t.Fatal("a stale command digest was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a rejected invocation still ran the executor")
	}

	result, err := service.InvokeModuleCommand(context.Background(), application.InvokeModuleCommandRequest{
		Module: "demo", Command: "reindex", Parameters: map[string]any{"full": true},
		CommandDigest: safe.Command.Digest, Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Result["command"] != "reindex" {
		t.Fatalf("result = %#v", result)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != "reindex\n" {
		t.Fatalf("executor side effect = %q, %v", body, err)
	}
	environment, err := os.ReadFile(filepath.Join(ModuleRootFor(workspace), "last-environment.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(environment), "ANAS_E2E_LEAK_PROBE") {
		t.Fatalf("the caller's environment reached the executor:\n%s", environment)
	}

	// drift() must move the digest the client already holds, or the E2E's 412
	// case would silently test nothing.
	if err := drift(workspace); err != nil {
		t.Fatal(err)
	}
	drifted, err := service.GetModuleCommand(context.Background(), application.GetModuleCommandRequest{
		Module: "demo", Command: "reindex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if drifted.Command.Digest == safe.Command.Digest {
		t.Fatal("drift did not change the frozen descriptor digest")
	}
}
