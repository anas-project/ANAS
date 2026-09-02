package runner

// REQUIREMENTS: CONSOLE-R-133

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

// TestMaintenanceRealBtrfsTerminalDescriptorE2E is enabled only by the remote
// server wrapper. The wrapper supplies a real Btrfs workspace with a running
// Docker deployment, one snapshot, and a parser-only backup manifest.
func TestMaintenanceRealBtrfsTerminalDescriptorE2E(t *testing.T) {
	workspace := os.Getenv("ANAS_M4_E2E_WORKSPACE")
	target := os.Getenv("ANAS_M4_E2E_TARGET")
	snapshotID := os.Getenv("ANAS_M4_E2E_SNAPSHOT")
	anasBinary := os.Getenv("ANAS_M4_E2E_ANAS")
	if workspace == "" || target == "" || snapshotID == "" || anasBinary == "" {
		t.Skip("real Btrfs M4 fixture is not configured")
	}
	for _, path := range []string{workspace, target, anasBinary} {
		if !filepath.IsAbs(path) {
			t.Fatalf("fixture path is not absolute: %q", path)
		}
	}

	service := NewWorkspaceMaintenanceServiceFactory([]application.BackupTarget{{ID: "archive", Path: target}})(workspace, application.NopEventSink{})
	planRequest := application.BackupPlanRequest{TargetID: "archive", Mode: backupModeSnapshot, NoStop: true}
	plan, err := service.PlanBackup(context.Background(), planRequest)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := []application.TerminalActionDescriptor{}
	for _, request := range []application.TerminalActionRequest{
		{Operation: terminalSnapshotRestore, Snapshot: &application.TerminalSnapshotTarget{ID: snapshotID}},
		{Operation: terminalSnapshotDelete, Snapshot: &application.TerminalSnapshotTarget{ID: snapshotID}},
		{Operation: terminalBackupCreate, Backup: &application.TerminalBackupTarget{
			TargetID: "archive", PlanID: plan.PlanID, Mode: planRequest.Mode, NoStop: planRequest.NoStop,
		}},
		{Operation: terminalBackupRestore, Backup: &application.TerminalBackupTarget{TargetID: "archive", BackupID: "backup-fixture"}},
		{Operation: terminalBackupVerify, Backup: &application.TerminalBackupTarget{TargetID: "archive", BackupID: "backup-fixture"}},
	} {
		descriptor, err := service.PreviewTerminalAction(context.Background(), request)
		if err != nil {
			t.Fatalf("preview %s: %v", request.Operation, err)
		}
		assertDescriptorDisplayRoundTrips(t, descriptor)
		descriptors = append(descriptors, descriptor)
	}

	dataMarker := filepath.Join(workspace, "data", "m4-marker")
	userMarker := filepath.Join(workspace, "userdata", "m4-user-marker")
	dataBefore := mustReadFile(t, dataMarker)
	userBefore := mustReadFile(t, userMarker)

	restoreDryRun := runDescriptorCLI(t, anasBinary, descriptors[0].Argv, "--dry-run", "--json")
	if !bytes.Contains(restoreDryRun, []byte(`"dry_run": true`)) || !bytes.Contains(restoreDryRun, []byte(`"would_replace"`)) {
		t.Fatalf("snapshot restore dry-run = %s", restoreDryRun)
	}
	backupDryRun := runDescriptorCLI(t, anasBinary, descriptors[3].Argv, "--dry-run", "--json")
	if !bytes.Contains(backupDryRun, []byte(`"dry_run": true`)) || !bytes.Contains(backupDryRun, []byte(`"would_replace"`)) {
		t.Fatalf("backup restore dry-run = %s", backupDryRun)
	}

	snapshotManifest := filepath.Join(workspace, "snapshots", snapshotID, "snapshot.yml")
	assertDescriptorNeedsConfirmation(t, anasBinary, descriptors[1].Argv)
	if _, err := os.Stat(snapshotManifest); err != nil {
		t.Fatalf("snapshot delete descriptor changed its target: %v", err)
	}
	targetBefore, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	assertDescriptorNeedsConfirmation(t, anasBinary, descriptors[2].Argv)
	targetAfter, err := os.ReadDir(target)
	if err != nil || len(targetAfter) != len(targetBefore) {
		t.Fatalf("backup create descriptor changed the target: before=%d after=%d err=%v", len(targetBefore), len(targetAfter), err)
	}

	// Exercise the real verify parser without allowing the read-only verifier to
	// run. flag.ErrHelp exits through the CLI usage path before destination IO.
	helpArgs := replaceDescriptorExecutable(anasBinary, descriptors[4].Argv)
	helpArgs = append(helpArgs, "--help")
	help := exec.Command(helpArgs[0], helpArgs[1:]...)
	if output, err := help.CombinedOutput(); err == nil || !strings.Contains(string(output), "backup verify") {
		t.Fatalf("backup verify descriptor parser fixture = %v, %s", err, output)
	}

	if got := mustReadFile(t, dataMarker); !bytes.Equal(got, dataBefore) {
		t.Fatalf("descriptor validation changed data/: %q -> %q", dataBefore, got)
	}
	if got := mustReadFile(t, userMarker); !bytes.Equal(got, userBefore) {
		t.Fatalf("descriptor validation changed userdata/: %q -> %q", userBefore, got)
	}
}

func assertDescriptorDisplayRoundTrips(t *testing.T, descriptor application.TerminalActionDescriptor) {
	t.Helper()
	command := exec.Command("/bin/sh", "-c", "set -- "+descriptor.Display+"; printf '%s\\0' \"$@\"")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("parse descriptor display %q: %v", descriptor.Display, err)
	}
	got := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	want := make([][]byte, len(descriptor.Argv))
	for index := range descriptor.Argv {
		want[index] = []byte(descriptor.Argv[index])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("display tokens = %q, want %q", got, want)
	}
}

func runDescriptorCLI(t *testing.T, binary string, argv []string, suffix ...string) []byte {
	t.Helper()
	args := append(replaceDescriptorExecutable(binary, argv), suffix...)
	command := exec.Command(args[0], args[1:]...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("descriptor CLI %q: %v\n%s", args, err, output)
	}
	return output
}

func assertDescriptorNeedsConfirmation(t *testing.T, binary string, argv []string) {
	t.Helper()
	args := replaceDescriptorExecutable(binary, argv)
	command := exec.Command(args[0], args[1:]...)
	command.Stdin = strings.NewReader("")
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 3 {
		t.Fatalf("descriptor %q executed or bypassed confirmation: %v\n%s", args, err, output)
	}
}

func replaceDescriptorExecutable(binary string, argv []string) []string {
	result := append([]string{}, argv...)
	result[0] = binary
	return result
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
