package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// MaintenanceService is the transport-neutral boundary for the first console
// snapshot, backup, local-administrator, and terminal-preview surface. Host
// paths are selected by the daemon factory; HTTP requests name only registered
// resource IDs and closed enum/boolean options.
type MaintenanceService interface {
	ListSnapshots(context.Context) (SnapshotListResult, error)
	CreateSnapshot(context.Context, SnapshotCreateRequest) (SnapshotRecord, error)
	SetSnapshotPinned(context.Context, SnapshotPinRequest) (SnapshotRecord, error)
	VerifySnapshots(context.Context, SnapshotVerifyRequest) (SnapshotVerifyResult, error)
	PlanBackup(context.Context, BackupPlanRequest) (BackupPlanResult, error)
	ListBackups(context.Context, BackupListRequest) (BackupListResult, error)
	ListLocalAdmins(context.Context) (LocalAdminListResult, error)
	RotateLocalAdmin(context.Context, LocalAdminTarget) (LocalAdminRecord, error)
	RevealLocalAdmin(context.Context, LocalAdminTarget) (LocalAdminCredential, error)
	PreviewTerminalAction(context.Context, TerminalActionRequest) (TerminalActionDescriptor, error)
	StepUpStateDigest(context.Context, string, string) (string, error)
}

type MaintenanceServiceFactory func(workspacePath string, events EventSink) MaintenanceService

type SnapshotProblem struct {
	ID   string `json:"id,omitempty"`
	Code string `json:"code"`
}

type SnapshotRecord struct {
	ID                   string            `json:"id"`
	Kind                 string            `json:"kind"`
	Pinned               bool              `json:"pinned"`
	CreatedAt            string            `json:"created_at"`
	Reason               string            `json:"reason"`
	Label                string            `json:"label"`
	DeploymentID         string            `json:"deployment_id"`
	Complete             bool              `json:"complete"`
	ConfigMatchesCurrent bool              `json:"config_matches_current"`
	SizeBytes            *int64            `json:"size_bytes"`
	Modules              map[string]string `json:"modules"`
	Healthy              bool              `json:"healthy"`
	IncludesUserData     bool              `json:"includes_userdata"`
}

type SnapshotListResult struct {
	Workspace string           `json:"-"`
	KeepAuto  int              `json:"keep_auto"`
	Snapshots []SnapshotRecord `json:"snapshots"`
}

type SnapshotCreateRequest struct {
	Label           string `json:"label,omitempty"`
	IncludeUserData bool   `json:"include_userdata"`
}

type SnapshotPinRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Pinned     bool   `json:"pinned"`
	Label      string `json:"label,omitempty"`
}

type SnapshotVerifyRequest struct {
	SnapshotID string `json:"snapshot_id,omitempty"`
}

type SnapshotVerifyResult struct {
	Workspace string            `json:"-"`
	Checked   int               `json:"checked"`
	Healthy   bool              `json:"healthy"`
	Problems  []SnapshotProblem `json:"problems"`
}

type BackupTarget struct {
	ID        string `json:"id"`
	Path      string `json:"-"`
	Exists    bool   `json:"exists"`
	Writable  bool   `json:"writable"`
	FSType    string `json:"fs_type"`
	FreeBytes *int64 `json:"free_bytes"`
}

type BackupSource struct {
	FSType            string `json:"fs_type"`
	DataIsSubvolume   bool   `json:"data_is_subvolume"`
	DataIsMountpoint  bool   `json:"data_is_mountpoint"`
	DataFullyReadable bool   `json:"data_fully_readable"`
}

type BackupEstimate struct {
	DataBytes             int64 `json:"data_bytes"`
	UserDataBytes         int64 `json:"userdata_bytes"`
	StateBytes            int64 `json:"state_bytes"`
	ActiveDeploymentBytes int64 `json:"active_deployment_bytes"`
	TotalBytes            int64 `json:"total_bytes"`
}

type BackupMode struct {
	ID          string   `json:"id"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
	Incremental bool     `json:"incremental"`
	Parents     []string `json:"parents"`
	Notes       []string `json:"notes"`
}

type BackupCapabilities struct {
	Source      BackupSource    `json:"source"`
	Target      BackupTarget    `json:"target"`
	Tools       map[string]bool `json:"tools"`
	Privileged  bool            `json:"privileged"`
	Estimate    BackupEstimate  `json:"estimate"`
	Modes       []BackupMode    `json:"modes"`
	Recommended string          `json:"recommended"`
}

type BackupPlanRequest struct {
	TargetID       string `json:"target_id"`
	Mode           string `json:"mode,omitempty"`
	SnapshotID     string `json:"snapshot_id,omitempty"`
	ParentBackupID string `json:"parent_backup_id,omitempty"`
	NoStop         bool   `json:"no_stop"`
	SkipUserData   bool   `json:"skip_userdata"`
}

type BackupWarning struct {
	Code string `json:"code"`
}

type BackupAction struct {
	Step   int    `json:"step"`
	Op     string `json:"op"`
	Target string `json:"target,omitempty"`
	Count  int    `json:"count,omitempty"`
}

type BackupPlan struct {
	Mode                     string          `json:"mode"`
	Incremental              bool            `json:"incremental"`
	Parent                   string          `json:"parent,omitempty"`
	TransferBytes            int64           `json:"transfer_bytes"`
	DestFreeAfterBytes       *int64          `json:"dest_free_after_bytes"`
	Includes                 []string        `json:"includes"`
	Excludes                 []string        `json:"excludes"`
	StopContainers           bool            `json:"stop_containers"`
	ContainersToStop         []string        `json:"containers_to_stop"`
	EstimatedDowntimeSeconds int             `json:"estimated_downtime_seconds"`
	Warnings                 []BackupWarning `json:"warnings"`
	Actions                  []BackupAction  `json:"actions"`
}

type BackupPlanResult struct {
	Workspace    string             `json:"-"`
	TargetID     string             `json:"target_id"`
	PlanID       string             `json:"plan_id"`
	Capabilities BackupCapabilities `json:"capabilities"`
	Plan         BackupPlan         `json:"plan"`
}

type BackupListRequest struct {
	TargetID string `json:"target_id"`
}

type BackupRecord struct {
	ID               string            `json:"id"`
	Mode             string            `json:"mode"`
	CreatedAt        string            `json:"created_at"`
	SourceSnapshot   string            `json:"source_snapshot,omitempty"`
	Incremental      bool              `json:"incremental"`
	Parent           string            `json:"parent,omitempty"`
	SizeBytes        int64             `json:"size_bytes"`
	DeploymentID     string            `json:"deployment_id"`
	Modules          map[string]string `json:"modules"`
	Complete         bool              `json:"complete"`
	ChainBroken      bool              `json:"chain_broken"`
	IncludesUserData bool              `json:"includes_userdata"`
}

type BackupListResult struct {
	Workspace string         `json:"-"`
	TargetID  string         `json:"target_id"`
	Backups   []BackupRecord `json:"backups"`
}

type LocalAdminTarget struct {
	Module  string `json:"module"`
	Account string `json:"account"`
}

type LocalAdminRecord struct {
	TargetID string `json:"target_id"`
	Module   string `json:"module"`
	Account  string `json:"account"`
	Purpose  string `json:"purpose"`
	Username string `json:"username"`
	URL      string `json:"url"`
}

// LocalAdminTargetID returns an opaque, stable binding ID for step-up proofs.
// The zero separator makes it unambiguous even if a future account ID contains
// punctuation used by routes or display labels.
func LocalAdminTargetID(module, account string) string {
	digest := sha256.Sum256([]byte(module + "\x00" + account))
	return "lad_" + hex.EncodeToString(digest[:])
}

type LocalAdminCredential struct {
	LocalAdminRecord
	Password string `json:"password"`
}

type LocalAdminListResult struct {
	Workspace string             `json:"-"`
	Accounts  []LocalAdminRecord `json:"accounts"`
}

type TerminalActionRequest struct {
	Operation string                  `json:"operation"`
	Snapshot  *TerminalSnapshotTarget `json:"snapshot,omitempty"`
	Backup    *TerminalBackupTarget   `json:"backup,omitempty"`
}

type TerminalSnapshotTarget struct {
	ID              string `json:"id"`
	RestoreUserData bool   `json:"restore_userdata,omitempty"`
	Force           bool   `json:"force,omitempty"`
}

type TerminalBackupTarget struct {
	TargetID       string `json:"target_id"`
	PlanID         string `json:"plan_id,omitempty"`
	BackupID       string `json:"backup_id,omitempty"`
	Mode           string `json:"mode,omitempty"`
	SnapshotID     string `json:"snapshot_id,omitempty"`
	ParentBackupID string `json:"parent_backup_id,omitempty"`
	NoStop         bool   `json:"no_stop,omitempty"`
	SkipUserData   bool   `json:"skip_userdata,omitempty"`
}

type TerminalActionTarget struct {
	SnapshotID     string `json:"snapshot_id,omitempty"`
	BackupTargetID string `json:"backup_target_id,omitempty"`
	BackupPlanID   string `json:"backup_plan_id,omitempty"`
	BackupID       string `json:"backup_id,omitempty"`
}

type TerminalActionImpact struct {
	Data       bool `json:"data"`
	UserData   bool `json:"userdata"`
	Reversible bool `json:"reversible"`
}

type TerminalActionDescriptor struct {
	Operation   string               `json:"operation"`
	WorkspaceID string               `json:"-"`
	Target      TerminalActionTarget `json:"target"`
	Impact      TerminalActionImpact `json:"impact"`
	Argv        []string             `json:"argv"`
	Display     string               `json:"display"`
	CLIContract string               `json:"cli_contract"`
}
