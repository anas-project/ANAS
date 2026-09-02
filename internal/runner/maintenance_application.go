package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
)

const (
	terminalSnapshotRestore = "snapshot.restore"
	terminalSnapshotDelete  = "snapshot.delete"
	terminalBackupCreate    = "backup.create"
	terminalBackupRestore   = "backup.restore"
	terminalBackupVerify    = "backup.verify"
	localAdminRevealAction  = "local_admin.reveal"
)

type workspaceMaintenanceApplication struct {
	workspace     string
	backupTargets map[string]string
	events        application.EventSink
}

var _ application.MaintenanceService = (*workspaceMaintenanceApplication)(nil)

// NewWorkspaceMaintenanceServiceFactory captures the root-managed backup
// target registry. Requests can select an ID, but never replace its host path.
func NewWorkspaceMaintenanceServiceFactory(targets []application.BackupTarget) application.MaintenanceServiceFactory {
	registered := make(map[string]string, len(targets))
	for _, target := range targets {
		if target.ID != "" && filepath.IsAbs(target.Path) {
			registered[target.ID] = filepath.Clean(target.Path)
		}
	}
	return func(workspacePath string, events application.EventSink) application.MaintenanceService {
		copyTargets := make(map[string]string, len(registered))
		for id, path := range registered {
			copyTargets[id] = path
		}
		if events == nil {
			events = application.NopEventSink{}
		}
		return &workspaceMaintenanceApplication{
			workspace: filepath.Clean(workspacePath), backupTargets: copyTargets, events: events,
		}
	}
}

func (service *workspaceMaintenanceApplication) ListSnapshots(ctx context.Context) (application.SnapshotListResult, error) {
	if err := service.validate(); err != nil {
		return application.SnapshotListResult{}, err
	}
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return application.SnapshotListResult{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	all, err := listSnapshots(service.workspace)
	if err != nil {
		return application.SnapshotListResult{}, maintenanceCLIError(failuref("scan_failed", "%s", err.Error()))
	}
	records := make([]application.SnapshotRecord, 0, len(all))
	current := currentConfigDigest(service.workspace)
	for _, meta := range all {
		records = append(records, publicSnapshotRecord(service.workspace, meta, current))
	}
	return application.SnapshotListResult{Workspace: service.workspace, KeepAuto: workspaceKeepAuto(service.workspace), Snapshots: records}, nil
}

func (service *workspaceMaintenanceApplication) CreateSnapshot(ctx context.Context, request application.SnapshotCreateRequest) (application.SnapshotRecord, error) {
	if err := service.validate(); err != nil {
		return application.SnapshotRecord{}, err
	}
	if len(request.Label) > 256 || strings.ContainsAny(request.Label, "\x00\r\n") {
		return application.SnapshotRecord{}, maintenanceError(application.ErrorKindInvalidArgument, "snapshot_label_invalid", "snapshot label is invalid", nil)
	}
	unlock, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, true)
	if err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	meta, err := createSnapshot(service.workspace, snapshotOptions{
		kind: snapshotKindManual, reason: snapshotReasonManual, label: request.Label,
		includeUserData: request.IncludeUserData, ctx: ctx, events: service.events,
		restrictedProcessEnvironment: true,
	})
	if err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(err)
	}
	return publicSnapshotRecord(service.workspace, *meta, currentConfigDigest(service.workspace)), nil
}

func (service *workspaceMaintenanceApplication) SetSnapshotPinned(ctx context.Context, request application.SnapshotPinRequest) (application.SnapshotRecord, error) {
	if err := service.validate(); err != nil {
		return application.SnapshotRecord{}, err
	}
	if err := validateSnapshotID(request.SnapshotID); err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(err)
	}
	if len(request.Label) > 256 || strings.ContainsAny(request.Label, "\x00\r\n") {
		return application.SnapshotRecord{}, maintenanceError(application.ErrorKindInvalidArgument, "snapshot_label_invalid", "snapshot label is invalid", nil)
	}
	unlock, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, true)
	if err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	meta, err := loadSnapshot(service.workspace, request.SnapshotID)
	if err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(err)
	}
	meta.Pinned = request.Pinned
	if request.Label != "" {
		meta.Label = request.Label
	}
	if err := writeYAMLAtomic(snapshotMetaFile(snapshotRoot(service.workspace, meta.ID)), meta, 0o600); err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(failuref("write_failed", "%s", err.Error()))
	}
	if err := rebuildSnapshotIndex(service.workspace); err != nil {
		return application.SnapshotRecord{}, maintenanceCLIError(failuref("write_failed", "%s", err.Error()))
	}
	return publicSnapshotRecord(service.workspace, *meta, currentConfigDigest(service.workspace)), nil
}

func (service *workspaceMaintenanceApplication) VerifySnapshots(ctx context.Context, request application.SnapshotVerifyRequest) (application.SnapshotVerifyResult, error) {
	if err := service.validate(); err != nil {
		return application.SnapshotVerifyResult{}, err
	}
	if request.SnapshotID != "" {
		if err := validateSnapshotID(request.SnapshotID); err != nil {
			return application.SnapshotVerifyResult{}, maintenanceCLIError(err)
		}
	}
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return application.SnapshotVerifyResult{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	all, err := listSnapshots(service.workspace)
	if err != nil {
		return application.SnapshotVerifyResult{}, maintenanceCLIError(failuref("scan_failed", "%s", err.Error()))
	}
	if request.SnapshotID != "" {
		filtered := all[:0]
		for _, meta := range all {
			if meta.ID == request.SnapshotID {
				filtered = append(filtered, meta)
			}
		}
		if len(filtered) == 0 {
			return application.SnapshotVerifyResult{}, maintenanceCLIError(preconditionErrorf("snapshot_missing", "snapshot was not found"))
		}
		all = filtered
	}
	problems := []application.SnapshotProblem{}
	for _, meta := range all {
		for _, problem := range verifySnapshot(service.workspace, meta) {
			problems = append(problems, application.SnapshotProblem{ID: problem.ID, Code: problem.Code})
		}
	}
	return application.SnapshotVerifyResult{
		Workspace: service.workspace, Checked: len(all), Healthy: len(problems) == 0, Problems: problems,
	}, nil
}

func (service *workspaceMaintenanceApplication) PlanBackup(ctx context.Context, request application.BackupPlanRequest) (application.BackupPlanResult, error) {
	if err := service.validate(); err != nil {
		return application.BackupPlanResult{}, err
	}
	if err := maintenanceContextError(ctx); err != nil {
		return application.BackupPlanResult{}, err
	}
	targetPath, err := service.backupTarget(request.TargetID)
	if err != nil {
		return application.BackupPlanResult{}, err
	}
	plan, err := buildBackupPlan(service.workspace, backupOptions{
		dest: targetPath, mode: request.Mode, snapshotID: request.SnapshotID,
		parent: request.ParentBackupID, noStop: request.NoStop, skipUserData: request.SkipUserData,
	})
	if err != nil {
		return application.BackupPlanResult{}, maintenanceCLIError(err)
	}
	caps := publicBackupCapabilities(request.TargetID, plan.caps)
	publicPlan := publicBackupPlan(plan)
	planID, err := backupPlanID(request.TargetID, publicPlan)
	if err != nil {
		return application.BackupPlanResult{}, maintenanceError(application.ErrorKindInternal, "backup_plan_unavailable", "backup plan is unavailable", err)
	}
	return application.BackupPlanResult{
		Workspace: service.workspace, TargetID: request.TargetID, PlanID: planID,
		Capabilities: caps, Plan: publicPlan,
	}, nil
}

func (service *workspaceMaintenanceApplication) ListBackups(ctx context.Context, request application.BackupListRequest) (application.BackupListResult, error) {
	if err := service.validate(); err != nil {
		return application.BackupListResult{}, err
	}
	if err := maintenanceContextError(ctx); err != nil {
		return application.BackupListResult{}, err
	}
	targetPath, err := service.backupTarget(request.TargetID)
	if err != nil {
		return application.BackupListResult{}, err
	}
	all, err := listBackups(targetPath)
	if err != nil {
		return application.BackupListResult{}, maintenanceCLIError(err)
	}
	result := application.BackupListResult{Workspace: service.workspace, TargetID: request.TargetID, Backups: make([]application.BackupRecord, 0, len(all))}
	for _, manifest := range all {
		result.Backups = append(result.Backups, publicBackupRecord(manifest))
	}
	return result, nil
}

func (service *workspaceMaintenanceApplication) ListLocalAdmins(ctx context.Context) (application.LocalAdminListResult, error) {
	if err := service.validate(); err != nil {
		return application.LocalAdminListResult{}, err
	}
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return application.LocalAdminListResult{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	state, err := loadLocalAdminState(stateDir(service.workspace))
	if err != nil {
		return application.LocalAdminListResult{}, maintenanceCLIError(preconditionErrorf("local_admin_state_unreadable", "%s", err.Error()))
	}
	result := application.LocalAdminListResult{Workspace: service.workspace, Accounts: []application.LocalAdminRecord{}}
	for _, record := range sortedLocalAdminRecords(state) {
		result.Accounts = append(result.Accounts, publicLocalAdmin(stateDir(service.workspace), record))
	}
	return result, nil
}

func (service *workspaceMaintenanceApplication) RotateLocalAdmin(ctx context.Context, target application.LocalAdminTarget) (application.LocalAdminRecord, error) {
	if err := service.validate(); err != nil {
		return application.LocalAdminRecord{}, err
	}
	unlock, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, true)
	if err != nil {
		return application.LocalAdminRecord{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	record, err := service.localAdminRecord(target)
	if err != nil {
		return application.LocalAdminRecord{}, err
	}
	length := 24
	if cfg, loadErr := loadWorkspaceConfigForLocalAdmin(service.workspace); loadErr == nil && cfg >= 16 {
		length = cfg
	}
	candidate, err := randomPassword(length)
	if err != nil {
		return application.LocalAdminRecord{}, maintenanceError(application.ErrorKindInternal, "local_admin_random_failed", "generated credential is unavailable", err)
	}
	if err := rotateLocalAdministratorContext(ctx, stateDir(service.workspace), record, candidate, service.events, true); err != nil {
		candidate = ""
		return application.LocalAdminRecord{}, maintenanceCLIError(failuref("local_admin_rotate_failed", "%s", err.Error()))
	}
	candidate = ""
	return publicLocalAdmin(stateDir(service.workspace), record), nil
}

func (service *workspaceMaintenanceApplication) RevealLocalAdmin(ctx context.Context, target application.LocalAdminTarget) (application.LocalAdminCredential, error) {
	if err := service.validate(); err != nil {
		return application.LocalAdminCredential{}, err
	}
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return application.LocalAdminCredential{}, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "%s", err.Error()))
	}
	defer unlock()
	record, err := service.localAdminRecord(target)
	if err != nil {
		return application.LocalAdminCredential{}, err
	}
	secrets, err := loadSecretStore(stateDir(service.workspace))
	if err != nil {
		return application.LocalAdminCredential{}, maintenanceCLIError(preconditionErrorf("secrets_unreadable", "%s", err.Error()))
	}
	password := secrets.values[record.SecretKey]
	if password == "" {
		return application.LocalAdminCredential{}, maintenanceCLIError(preconditionErrorf("secret_missing", "local administrator has no generated password"))
	}
	return application.LocalAdminCredential{LocalAdminRecord: publicLocalAdmin(stateDir(service.workspace), record), Password: password}, nil
}

func (service *workspaceMaintenanceApplication) PreviewTerminalAction(ctx context.Context, request application.TerminalActionRequest) (application.TerminalActionDescriptor, error) {
	if err := service.validate(); err != nil {
		return application.TerminalActionDescriptor{}, err
	}
	if err := maintenanceContextError(ctx); err != nil {
		return application.TerminalActionDescriptor{}, err
	}
	descriptor, err := service.previewTerminalAction(ctx, request)
	if err != nil {
		return application.TerminalActionDescriptor{}, err
	}
	descriptor.Argv = append([]string{}, descriptor.Argv...)
	descriptor.Display = shellJoin(descriptor.Argv)
	return descriptor, nil
}

func (service *workspaceMaintenanceApplication) previewTerminalAction(ctx context.Context, request application.TerminalActionRequest) (application.TerminalActionDescriptor, error) {
	descriptor := application.TerminalActionDescriptor{Operation: request.Operation}
	switch request.Operation {
	case terminalSnapshotRestore, terminalSnapshotDelete:
		if request.Snapshot == nil || request.Backup != nil {
			return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_target_invalid", "snapshot operation requires exactly one snapshot target", nil)
		}
		if err := validateSnapshotID(request.Snapshot.ID); err != nil {
			return descriptor, maintenanceCLIError(err)
		}
		meta, err := loadSnapshot(service.workspace, request.Snapshot.ID)
		if err != nil {
			return descriptor, maintenanceCLIError(err)
		}
		descriptor.Target.SnapshotID = meta.ID
		if request.Operation == terminalSnapshotRestore {
			if request.Snapshot.Force {
				return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_option_invalid", "force is not valid for snapshot restore", nil)
			}
			if request.Snapshot.RestoreUserData && !meta.capturedTree(snapshotTreeUserData) {
				return descriptor, maintenanceCLIError(preconditionErrorf("userdata_not_captured", "snapshot does not contain userdata"))
			}
			descriptor.Impact = application.TerminalActionImpact{Data: true, UserData: request.Snapshot.RestoreUserData, Reversible: true}
			descriptor.Argv = snapshotRestoreArgv(service.workspace, meta.ID, request.Snapshot.RestoreUserData)
			descriptor.CLIContract = "docs/reference/contracts/snapshot.md#anas-snapshot-restore-id"
			return descriptor, nil
		}
		if request.Snapshot.RestoreUserData {
			return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_option_invalid", "restore_userdata is not valid for snapshot delete", nil)
		}
		if meta.Pinned && !request.Snapshot.Force {
			return descriptor, maintenanceCLIError(preconditionErrorf("snapshot_pinned", "snapshot is pinned; force must be explicitly selected"))
		}
		descriptor.Impact = application.TerminalActionImpact{Data: true, UserData: meta.capturedTree(snapshotTreeUserData), Reversible: false}
		descriptor.Argv = snapshotDeleteArgv(service.workspace, meta.ID, request.Snapshot.Force)
		descriptor.CLIContract = "docs/reference/contracts/snapshot.md#anas-snapshot-delete-id"
		return descriptor, nil

	case terminalBackupCreate, terminalBackupRestore, terminalBackupVerify:
		if request.Backup == nil || request.Snapshot != nil {
			return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_target_invalid", "backup operation requires exactly one backup target", nil)
		}
		targetPath, err := service.backupTarget(request.Backup.TargetID)
		if err != nil {
			return descriptor, err
		}
		descriptor.Target.BackupTargetID = request.Backup.TargetID
		if request.Operation == terminalBackupCreate {
			if request.Backup.BackupID != "" || request.Backup.PlanID == "" {
				return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_target_invalid", "backup create requires a backup plan ID and no backup ID", nil)
			}
			planned, err := service.PlanBackup(ctx, application.BackupPlanRequest{
				TargetID: request.Backup.TargetID, Mode: request.Backup.Mode, SnapshotID: request.Backup.SnapshotID,
				ParentBackupID: request.Backup.ParentBackupID, NoStop: request.Backup.NoStop, SkipUserData: request.Backup.SkipUserData,
			})
			if err != nil {
				return descriptor, err
			}
			if request.Backup.PlanID != planned.PlanID {
				return descriptor, maintenanceError(application.ErrorKindFailedPrecondition, "backup_plan_changed", "backup plan is no longer current", nil)
			}
			descriptor.Target.BackupPlanID = planned.PlanID
			descriptor.Impact = application.TerminalActionImpact{Data: true, UserData: !request.Backup.SkipUserData, Reversible: true}
			descriptor.Argv = backupCreateArgv(service.workspace, targetPath, planned.Plan.Mode, request.Backup.SnapshotID, request.Backup.ParentBackupID, request.Backup.NoStop, request.Backup.SkipUserData)
			descriptor.CLIContract = "docs/reference/contracts/backup.md#anas-backup-create"
			return descriptor, nil
		}
		if request.Backup.PlanID != "" || request.Backup.Mode != "" || request.Backup.SnapshotID != "" || request.Backup.ParentBackupID != "" || request.Backup.NoStop || request.Backup.SkipUserData {
			return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_option_invalid", "backup restore or verify contains create-only options", nil)
		}
		if err := validateBackupID(request.Backup.BackupID); err != nil {
			return descriptor, maintenanceCLIError(err)
		}
		manifest, err := loadBackupManifest(targetPath, request.Backup.BackupID)
		if err != nil {
			return descriptor, maintenanceCLIError(err)
		}
		descriptor.Target.BackupID = manifest.BackupID
		includesUserData := stringSliceContains(manifest.Channels, backupChannelUserData)
		if request.Operation == terminalBackupRestore {
			descriptor.Impact = application.TerminalActionImpact{Data: true, UserData: includesUserData, Reversible: false}
			descriptor.Argv = backupRestoreArgv(service.workspace, targetPath, manifest.BackupID)
			descriptor.CLIContract = "docs/reference/contracts/backup.md#anas-backup-restore"
			return descriptor, nil
		}
		descriptor.Impact = application.TerminalActionImpact{Data: true, UserData: includesUserData, Reversible: true}
		descriptor.Argv = backupVerifyArgv(targetPath, manifest.BackupID)
		descriptor.CLIContract = "docs/reference/contracts/backup.md#anas-backup-verify"
		return descriptor, nil
	default:
		return descriptor, maintenanceError(application.ErrorKindInvalidArgument, "terminal_action_operation_invalid", "terminal action operation is not supported", nil)
	}
}

func (service *workspaceMaintenanceApplication) StepUpStateDigest(ctx context.Context, action, targetID string) (string, error) {
	if err := service.validate(); err != nil {
		return "", err
	}
	if err := maintenanceContextError(ctx); err != nil {
		return "", err
	}
	if action != localAdminRevealAction {
		return "", maintenanceError(application.ErrorKindInvalidArgument, "step_up_action_invalid", "step-up action is not supported by maintenance service", nil)
	}
	state, err := loadLocalAdminState(stateDir(service.workspace))
	if err != nil {
		return "", maintenanceCLIError(preconditionErrorf("local_admin_state_unreadable", "%s", err.Error()))
	}
	var record localAdminRecord
	found := false
	for _, candidate := range sortedLocalAdminRecords(state) {
		if application.LocalAdminTargetID(candidate.Module, candidate.ID) == targetID {
			record, found = candidate, true
			break
		}
	}
	if !found {
		return "", maintenanceError(application.ErrorKindNotFound, "local_admin_missing", "local administrator was not found", nil)
	}
	secrets, err := loadSecretStore(stateDir(service.workspace))
	if err != nil {
		return "", maintenanceCLIError(preconditionErrorf("secrets_unreadable", "%s", err.Error()))
	}
	secret := secrets.values[record.SecretKey]
	if secret == "" {
		return "", maintenanceCLIError(preconditionErrorf("secret_missing", "local administrator has no generated password"))
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{service.workspace, action, record.Module, record.ID, record.Username, record.SecretKey, secret}, "\x00")))
	secret = ""
	return hex.EncodeToString(digest[:]), nil
}

func (service *workspaceMaintenanceApplication) validate() error {
	if service == nil || service.workspace == "" || service.workspace == "." || !filepath.IsAbs(service.workspace) {
		return maintenanceError(application.ErrorKindInvalidArgument, "workspace_required", "workspace is required", nil)
	}
	return nil
}

func (service *workspaceMaintenanceApplication) backupTarget(id string) (string, error) {
	if id == "" || len(id) > 64 || strings.TrimSpace(id) != id {
		return "", maintenanceError(application.ErrorKindInvalidArgument, "backup_target_invalid", "backup target ID is invalid", nil)
	}
	path, ok := service.backupTargets[id]
	if !ok {
		return "", maintenanceError(application.ErrorKindNotFound, "backup_target_not_found", "backup target was not found", nil)
	}
	return path, nil
}

func (service *workspaceMaintenanceApplication) localAdminRecord(target application.LocalAdminTarget) (localAdminRecord, error) {
	if target.Module == "" || target.Account == "" || len(target.Module) > 64 || len(target.Account) > 64 ||
		strings.TrimSpace(target.Module) != target.Module || strings.TrimSpace(target.Account) != target.Account {
		return localAdminRecord{}, maintenanceError(application.ErrorKindInvalidArgument, "local_admin_target_invalid", "local administrator target is invalid", nil)
	}
	state, err := loadLocalAdminState(stateDir(service.workspace))
	if err != nil {
		return localAdminRecord{}, maintenanceCLIError(preconditionErrorf("local_admin_state_unreadable", "%s", err.Error()))
	}
	record, err := selectLocalAdmin(sortedLocalAdminRecords(state), target.Module, target.Account)
	if err != nil {
		return localAdminRecord{}, maintenanceError(application.ErrorKindNotFound, "local_admin_missing", "local administrator was not found", err)
	}
	return record, nil
}

func publicSnapshotRecord(workspace string, meta snapshotMeta, currentConfigDigest string) application.SnapshotRecord {
	modules := make(map[string]string, len(meta.Modules))
	for name, release := range meta.Modules {
		modules[name] = release
	}
	return application.SnapshotRecord{
		ID: meta.ID, Kind: meta.Kind, Pinned: meta.Pinned, CreatedAt: meta.CreatedAt, Reason: meta.Reason,
		Label: meta.Label, DeploymentID: meta.DeploymentID, Complete: meta.Complete,
		ConfigMatchesCurrent: currentConfigDigest != "" && currentConfigDigest == meta.ConfigDigest,
		SizeBytes:            nil, Modules: modules, Healthy: len(verifySnapshot(workspace, meta)) == 0,
		IncludesUserData: meta.capturedTree(snapshotTreeUserData),
	}
}

func publicBackupCapabilities(targetID string, caps *backupCapabilities) application.BackupCapabilities {
	target := application.BackupTarget{ID: targetID}
	if caps.Dest != nil {
		target.Exists, target.Writable, target.FSType, target.FreeBytes = caps.Dest.Exists, caps.Dest.Writable, caps.Dest.FSType, caps.Dest.FreeBytes
	}
	modes := make([]application.BackupMode, 0, len(caps.Modes))
	for _, mode := range caps.Modes {
		modes = append(modes, application.BackupMode{ID: mode.ID, Available: mode.Available, Reason: mode.Reason, Incremental: mode.Incremental, Parents: append([]string{}, mode.Parents...), Notes: append([]string{}, mode.Notes...)})
	}
	tools := make(map[string]bool, len(caps.Tools))
	for name, available := range caps.Tools {
		tools[name] = available
	}
	return application.BackupCapabilities{
		Source: application.BackupSource{FSType: caps.Source.FSType, DataIsSubvolume: caps.Source.DataIsSubvolume, DataIsMountpoint: caps.Source.DataIsMountpoint, DataFullyReadable: caps.Source.DataFullyReadable},
		Target: target, Tools: tools, Privileged: caps.Privileged,
		Estimate: application.BackupEstimate{DataBytes: caps.Estimate.DataBytes, UserDataBytes: caps.Estimate.UserDataBytes, StateBytes: caps.Estimate.StateBytes, ActiveDeploymentBytes: caps.Estimate.ActiveDeploymentBytes, TotalBytes: caps.Estimate.TotalBytes},
		Modes:    modes, Recommended: caps.Recommended,
	}
}

func publicBackupPlan(plan *backupPlan) application.BackupPlan {
	warnings := make([]application.BackupWarning, 0, len(plan.Warnings))
	for _, warning := range plan.Warnings {
		warnings = append(warnings, application.BackupWarning{Code: warning.Code})
	}
	actions := make([]application.BackupAction, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		// The CLI plan target is an implementation path. HTTP callers address
		// destinations only through root-configured IDs, so the public action
		// projection intentionally omits it.
		actions = append(actions, application.BackupAction{Step: action.Step, Op: action.Op, Count: action.Count})
	}
	return application.BackupPlan{
		Mode: plan.Mode, Incremental: plan.Incremental, Parent: plan.Parent,
		TransferBytes: plan.Estimate.TransferBytes, DestFreeAfterBytes: plan.Estimate.DestFreeAfterBytes,
		Includes: append([]string{}, plan.Includes...), Excludes: append([]string{}, plan.Excludes...),
		StopContainers: plan.StopContainers, ContainersToStop: append([]string{}, plan.ContainersToStop...),
		EstimatedDowntimeSeconds: plan.EstimatedDowntimeSeconds, Warnings: warnings, Actions: actions,
	}
}

func publicBackupRecord(manifest backupManifest) application.BackupRecord {
	modules := make(map[string]string, len(manifest.Modules))
	for name, release := range manifest.Modules {
		modules[name] = release
	}
	return application.BackupRecord{
		ID: manifest.BackupID, Mode: manifest.Mode, CreatedAt: manifest.CreatedAt, SourceSnapshot: manifest.SourceSnapshot,
		Incremental: manifest.Incremental, Parent: manifest.Parent, SizeBytes: manifest.SizeBytes,
		DeploymentID: manifest.DeploymentID, Modules: modules, Complete: manifest.Complete, ChainBroken: manifest.ChainBroken,
		IncludesUserData: stringSliceContains(manifest.Channels, backupChannelUserData),
	}
}

func publicLocalAdmin(base string, record localAdminRecord) application.LocalAdminRecord {
	return application.LocalAdminRecord{TargetID: application.LocalAdminTargetID(record.Module, record.ID), Module: record.Module, Account: record.ID, Purpose: record.Purpose, Username: record.Username, URL: activeLocalAdminURL(base, record)}
}

func backupPlanID(targetID string, plan application.BackupPlan) (string, error) {
	body, err := json.Marshal(struct {
		TargetID string                 `json:"target_id"`
		Plan     application.BackupPlan `json:"plan"`
	}{TargetID: targetID, Plan: plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return "bkp_" + hex.EncodeToString(digest[:]), nil
}

func snapshotRestoreArgv(workspace, snapshotID string, restoreUserData bool) []string {
	argv := []string{"anas", "snapshot", "restore", snapshotID, "-w", workspace}
	if restoreUserData {
		argv = append(argv, "--restore-userdata")
	}
	return argv
}

func snapshotDeleteArgv(workspace, snapshotID string, force bool) []string {
	argv := []string{"anas", "snapshot", "delete", snapshotID, "-w", workspace}
	if force {
		argv = append(argv, "--force")
	}
	return argv
}

func backupCreateArgv(workspace, targetPath, mode, snapshotID, parentID string, noStop, skipUserData bool) []string {
	argv := []string{"anas", "backup", "create", "--to", targetPath, "--mode", mode, "-w", workspace}
	if snapshotID != "" {
		argv = append(argv, "--snapshot", snapshotID)
	}
	if parentID != "" {
		argv = append(argv, "--parent", parentID)
	}
	if noStop {
		argv = append(argv, "--no-stop")
	}
	if skipUserData {
		argv = append(argv, "--skip-userdata")
	}
	return argv
}

func backupRestoreArgv(workspace, targetPath, backupID string) []string {
	return []string{"anas", "backup", "restore", "--from", targetPath, "--backup-id", backupID, "-w", workspace}
}

func backupVerifyArgv(targetPath, backupID string) []string {
	return []string{"anas", "backup", "verify", "--to", targetPath, "--backup-id", backupID}
}

func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for index, token := range argv {
		if token != "" && strings.IndexFunc(token, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
		}) == -1 {
			quoted[index] = token
			continue
		}
		quoted[index] = "'" + strings.ReplaceAll(token, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

func maintenanceCLIError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return deploymentApplicationErrorFromCLI(err)
}

func maintenanceError(kind application.ErrorKind, code, message string, cause error) error {
	return &application.Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

func maintenanceContextError(ctx context.Context) error {
	if ctx == nil {
		return maintenanceError(application.ErrorKindInvalidArgument, "context_required", "context is required", nil)
	}
	return ctx.Err()
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func loadWorkspaceConfigForLocalAdmin(workspace string) (int, error) {
	cfg, err := configLoadForMaintenance(filepath.Join(workspace, "config.yml"))
	if err != nil {
		return 0, err
	}
	return cfg, nil
}

// Kept as a variable so application tests can avoid constructing an unrelated
// full config document when exercising generated-password policy.
var configLoadForMaintenance = func(path string) (int, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return 0, err
	}
	return cfg.Administration.LocalAccounts.PasswordLength, nil
}
