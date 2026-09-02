package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

func TestMaintenanceTerminalDescriptorsUseRegisteredTypedTargetsWithoutExecuting(t *testing.T) {
	fakeBtrfs(t)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace with ' quote")
	if err := os.MkdirAll(dataDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	seedSnapshotWorkspaceAt(t, workspace)
	if err := os.MkdirAll(userDataDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dataDir(workspace), "marker")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, "target with ' quote")
	if err := os.MkdirAll(targetPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, backupRoot(targetPath, "backup-1"), &backupManifest{
		APIVersion: backupAPIVersion, BackupID: "backup-1", Mode: backupModeCopy, CreatedAt: "2026-01-01T00:00:00Z",
		DeploymentID: snapshot.DeploymentID, Channels: []string{backupChannelData, backupChannelMetadata, backupChannelUserData}, Complete: true,
	})
	service := NewWorkspaceMaintenanceServiceFactory([]application.BackupTarget{{ID: "archive", Path: targetPath}})(workspace, application.NopEventSink{})

	tests := []struct {
		name      string
		request   application.TerminalActionRequest
		wantArgv  []string
		wantData  bool
		wantUsers bool
		wantUndo  bool
	}{
		{
			name:     "snapshot restore defaults to preserving userdata",
			request:  application.TerminalActionRequest{Operation: terminalSnapshotRestore, Snapshot: &application.TerminalSnapshotTarget{ID: snapshot.ID}},
			wantArgv: snapshotRestoreArgv(workspace, snapshot.ID, false), wantData: true, wantUsers: false, wantUndo: true,
		},
		{
			name:     "snapshot delete is terminal only",
			request:  application.TerminalActionRequest{Operation: terminalSnapshotDelete, Snapshot: &application.TerminalSnapshotTarget{ID: snapshot.ID}},
			wantArgv: snapshotDeleteArgv(workspace, snapshot.ID, false), wantData: true, wantUsers: false, wantUndo: false,
		},
		{
			name:     "backup restore uses configured target",
			request:  application.TerminalActionRequest{Operation: terminalBackupRestore, Backup: &application.TerminalBackupTarget{TargetID: "archive", BackupID: "backup-1"}},
			wantArgv: backupRestoreArgv(workspace, targetPath, "backup-1"), wantData: true, wantUsers: true, wantUndo: false,
		},
		{
			name:     "backup verify uses configured target",
			request:  application.TerminalActionRequest{Operation: terminalBackupVerify, Backup: &application.TerminalBackupTarget{TargetID: "archive", BackupID: "backup-1"}},
			wantArgv: backupVerifyArgv(targetPath, "backup-1"), wantData: true, wantUsers: true, wantUndo: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := service.PreviewTerminalAction(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(descriptor.Argv, test.wantArgv) || descriptor.Display != shellJoin(test.wantArgv) {
				t.Fatalf("descriptor argv/display = %#v / %q, want %#v / %q", descriptor.Argv, descriptor.Display, test.wantArgv, shellJoin(test.wantArgv))
			}
			if descriptor.Impact != (application.TerminalActionImpact{Data: test.wantData, UserData: test.wantUsers, Reversible: test.wantUndo}) {
				t.Fatalf("impact = %#v", descriptor.Impact)
			}
			if descriptor.CLIContract == "" {
				t.Fatal("CLI contract reference is empty")
			}
		})
	}
	after, err := os.ReadFile(marker)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("preview executed an operation: marker=%q err=%v", after, err)
	}

	_, err = service.PreviewTerminalAction(context.Background(), application.TerminalActionRequest{
		Operation: terminalBackupVerify, Backup: &application.TerminalBackupTarget{TargetID: "/tmp/client-path", BackupID: "backup-1"},
	})
	assertApplicationErrorCode(t, err, "backup_target_not_found")
	_, err = service.PreviewTerminalAction(context.Background(), application.TerminalActionRequest{Operation: "shell", Snapshot: &application.TerminalSnapshotTarget{ID: snapshot.ID}})
	assertApplicationErrorCode(t, err, "terminal_action_operation_invalid")
}

func TestMaintenanceBackupCreateDescriptorIsBoundToPublicPlan(t *testing.T) {
	workspace, _ := newSnapshotWorkspace(t)
	if err := os.MkdirAll(userDataDir(workspace), 0o700); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "archive")
	if err := os.MkdirAll(targetPath, 0o700); err != nil {
		t.Fatal(err)
	}
	service := NewWorkspaceMaintenanceServiceFactory([]application.BackupTarget{{ID: "archive", Path: targetPath}})(workspace, application.NopEventSink{})
	request := application.BackupPlanRequest{TargetID: "archive", Mode: backupModeCopy, NoStop: true, SkipUserData: true}
	planned, err := service.PlanBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(planned)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), workspace) || strings.Contains(string(body), targetPath) {
		t.Fatalf("public backup plan leaked a host path: %s", body)
	}
	descriptor, err := service.PreviewTerminalAction(context.Background(), application.TerminalActionRequest{
		Operation: terminalBackupCreate,
		Backup:    &application.TerminalBackupTarget{TargetID: request.TargetID, PlanID: planned.PlanID, Mode: request.Mode, NoStop: request.NoStop, SkipUserData: request.SkipUserData},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := backupCreateArgv(workspace, targetPath, backupModeCopy, "", "", true, true)
	if !reflect.DeepEqual(descriptor.Argv, want) || descriptor.Target.BackupPlanID != planned.PlanID {
		t.Fatalf("descriptor = %#v, want argv %#v and plan %q", descriptor, want, planned.PlanID)
	}

	_, err = service.PreviewTerminalAction(context.Background(), application.TerminalActionRequest{
		Operation: terminalBackupCreate,
		Backup:    &application.TerminalBackupTarget{TargetID: request.TargetID, PlanID: planned.PlanID, Mode: request.Mode, NoStop: request.NoStop},
	})
	assertApplicationErrorCode(t, err, "backup_plan_changed")
}

func TestMaintenanceLocalAdminProjectionAndStepUpBindingContainNoSecret(t *testing.T) {
	workspace, _ := newSnapshotWorkspace(t)
	if err := os.WriteFile(filepath.Join(stateDir(workspace), "secrets.yml"), []byte("api_version: anas.secrets/v2\nsecrets:\n  CORE_PASSWORD:\n    value: fixture-secret\n    owner: core\n    kind: local_admin\n    provenance: test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewWorkspaceMaintenanceServiceFactory(nil)(workspace, application.NopEventSink{})
	result, err := service.ListLocalAdmins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accounts) != 1 || result.Accounts[0].TargetID != application.LocalAdminTargetID("core", "primary") {
		t.Fatalf("accounts = %#v", result.Accounts)
	}
	body, err := json.Marshal(result.Accounts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "value") || strings.Contains(string(body), "password") || strings.Contains(string(body), workspace) {
		t.Fatalf("local administrator list leaked secret or path: %s", body)
	}
	digest, err := service.StepUpStateDigest(context.Background(), localAdminRevealAction, result.Accounts[0].TargetID)
	if err != nil || len(digest) != 64 {
		t.Fatalf("step-up digest = %q, %v", digest, err)
	}
	_, err = service.StepUpStateDigest(context.Background(), localAdminRevealAction, "lad_"+strings.Repeat("0", 64))
	assertApplicationErrorCode(t, err, "local_admin_missing")
}

func TestShellJoinQuotesEveryTokenLosslessly(t *testing.T) {
	argv := []string{"anas", "", "plain", "space value", "apostrophe's", "$HOME", "line\nbreak"}
	want := "anas '' plain 'space value' 'apostrophe'\"'\"'s' '$HOME' 'line\nbreak'"
	if got := shellJoin(argv); got != want {
		t.Fatalf("shellJoin = %q, want %q", got, want)
	}
}

func assertApplicationErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	applicationError, ok := application.ErrorOf(err)
	if !ok || applicationError.Code != code {
		t.Fatalf("error = %v, want application code %q", err, code)
	}
}
