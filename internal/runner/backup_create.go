package runner

// Planning and running a backup.
//
// The action order is the interesting part, and it is a contract rather than an
// implementation detail: the containers start again *before* the transfer, not
// after it. They only have to be down while the snapshot is taken, because
// everything after that reads from the read-only snapshot and cannot see later
// writes. That turns the outage from "as long as the data takes to move" —
// minutes, and growing with the deployment — into "as long as Btrfs takes to
// make a snapshot", which is seconds and stays seconds.
//
// The order only holds where a snapshot is possible. Without one the copy reads
// the live directory, so the services stay down for its duration; `plan` says
// so in estimated_downtime_seconds rather than quietly reporting the fast
// number on a host that will get the slow one.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/whlsxl/anas/internal/compose"
)

type backupAction struct {
	Step   int    `json:"step"`
	Op     string `json:"op"`
	Target string `json:"target,omitempty"`
	Count  int    `json:"count,omitempty"`
	Method string `json:"method,omitempty"`
}

type backupWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type backupPlanEstimate struct {
	TransferBytes      int64  `json:"transfer_bytes"`
	DestFreeAfterBytes *int64 `json:"dest_free_after_bytes"`
}

type backupPlan struct {
	Workspace                string             `json:"workspace"`
	Mode                     string             `json:"mode"`
	Dest                     string             `json:"dest"`
	Incremental              bool               `json:"incremental"`
	Parent                   string             `json:"parent,omitempty"`
	Estimate                 backupPlanEstimate `json:"estimate"`
	Includes                 []string           `json:"includes"`
	Excludes                 []string           `json:"excludes"`
	StopContainers           bool               `json:"stop_containers"`
	ContainersToStop         []string           `json:"containers_to_stop"`
	EstimatedDowntimeSeconds int                `json:"estimated_downtime_seconds"`
	Warnings                 []backupWarning    `json:"warnings"`
	Actions                  []backupAction     `json:"actions"`

	// Not serialised: what create needs and plan already worked out.
	caps             *backupCapabilities
	snapshotCapable  bool
	useSnapshot      bool
	existingSnapshot *snapshotMeta
	parentManifest   *backupManifest
}

type backupOptions struct {
	dest       string
	mode       string
	snapshotID string
	parent     string
	noStop     bool
	yes        bool
	json       bool
}

// buildBackupPlan validates everything and produces the list of actions without
// performing any of them. `create` runs it first, so the two can never disagree
// about what is about to happen.
func buildBackupPlan(workspace string, opts backupOptions) (*backupPlan, error) {
	if strings.TrimSpace(opts.dest) == "" {
		return nil, usageErrorf("backup requires --to <dest>")
	}
	caps, err := probeBackupCapabilities(workspace, opts.dest)
	if err != nil {
		return nil, err
	}
	mode := opts.mode
	if mode == "" {
		mode = caps.Recommended
		if mode == "" {
			return nil, preconditionErrorf(firstUnavailableReason(caps.Modes),
				"no backup mode can run against %s; `anas backup capabilities --to %s` explains each one",
				caps.Dest.Path, caps.Dest.Path)
		}
	}
	if !validBackupMode(mode) {
		return nil, usageErrorf("unknown backup mode %q; expected snapshot, send, send-file or copy", mode)
	}
	report, ok := backupModeReportFor(caps.Modes, mode)
	if !ok || !report.Available {
		return nil, preconditionErrorf(report.Reason,
			"backup mode %s cannot run here: %s", mode, describeBackupReason(report.Reason))
	}

	plan := &backupPlan{
		Workspace: workspace, Mode: mode, Dest: caps.Dest.Path,
		caps: caps, Warnings: []backupWarning{}, Actions: []backupAction{},
	}
	// Every mode except copy-on-a-plain-filesystem reads from a snapshot. A
	// snapshot is also what makes the short outage possible, so it is taken
	// whenever it can be.
	plan.snapshotCapable = caps.Source.DataIsSubvolume && caps.Tools["btrfs"]
	if mode != backupModeCopy && !plan.snapshotCapable {
		return nil, preconditionErrorf(reasonDataNotSubvolume,
			"mode %s reads from a Btrfs snapshot and %s is not a subvolume", mode, dataDir(workspace))
	}
	plan.useSnapshot = plan.snapshotCapable

	if opts.snapshotID != "" {
		if !plan.snapshotCapable {
			return nil, preconditionErrorf(reasonSourceNotBtrfs,
				"--snapshot names an existing snapshot, and this workspace has no snapshot capability")
		}
		meta, err := loadSnapshot(workspace, opts.snapshotID)
		if err != nil {
			return nil, err
		}
		if !meta.Complete {
			return nil, preconditionErrorf("snapshot_incomplete",
				"snapshot %s never finished being written and must not be backed up", meta.ID)
		}
		plan.existingSnapshot = meta
	}

	if err := resolveBackupParent(workspace, plan, report, opts); err != nil {
		return nil, err
	}

	plan.Includes = []string{"config", "lock", "secrets", "state", "deployment", "data"}
	plan.Excludes = []string{"history_deployments", "caches", "snapshots"}

	// An existing snapshot is already frozen, so nothing has to stop for it.
	plan.StopContainers = plan.existingSnapshot == nil && !opts.noStop
	if plan.StopContainers {
		plan.ContainersToStop = backupContainersToStop(workspace)
	} else {
		plan.ContainersToStop = []string{}
	}
	plan.EstimatedDowntimeSeconds = 0
	if plan.StopContainers {
		plan.EstimatedDowntimeSeconds = estimatedDowntimeSeconds(mode, plan.useSnapshot, caps.Estimate.TotalBytes)
	}

	transfer := caps.Estimate.TotalBytes
	if plan.Incremental {
		// An increment carries only what changed, and nothing here can know
		// that in advance without walking both trees. Reporting the full size
		// would be wrong in the one direction that matters — it would make the
		// destination look too small — so the figure is left as the upper bound
		// it honestly is.
		transfer = caps.Estimate.TotalBytes
	}
	plan.Estimate.TransferBytes = transfer
	if caps.Dest.FreeBytes != nil {
		after := *caps.Dest.FreeBytes - transfer
		plan.Estimate.DestFreeAfterBytes = &after
	}

	for _, note := range report.Notes {
		if note == notePlaintextSecrets {
			plan.Warnings = append(plan.Warnings, backupWarning{
				Code: notePlaintextSecrets,
				Message: "the backup carries config.yml and the generated secret store in plaintext; " +
					"anything that can read " + caps.Dest.Path + " can read every password this deployment uses",
			})
		}
	}
	if opts.noStop {
		plan.Warnings = append(plan.Warnings, backupWarning{
			Code: noteCrashConsistentOnly, Message: crashConsistencyMessage(mode, plan.useSnapshot),
		})
	}
	plan.Actions = backupActions(plan)
	return plan, nil
}

// resolveBackupParent settles whether this backup is incremental and against
// what. An explicit --parent that is not usable is an error rather than a
// silent fall back to a full send: the operator asked for an increment, and
// quietly sending 1.3G instead is not a smaller surprise than failing.
func resolveBackupParent(workspace string, plan *backupPlan, report backupModeReport, opts backupOptions) error {
	if opts.parent == "" {
		return nil
	}
	if plan.Mode != backupModeSend && plan.Mode != backupModeSendFile {
		return usageErrorf("--parent only applies to the send and send-file modes")
	}
	if !containsString(report.Parents, opts.parent) {
		return preconditionErrorf("parent_missing",
			"backup %s cannot be used as a parent: it must be a complete send backup at %s whose source snapshot still exists in %s",
			opts.parent, plan.Dest, snapshotsDir(workspace))
	}
	manifest, err := loadBackupManifest(plan.Dest, opts.parent)
	if err != nil {
		return err
	}
	plan.Incremental = true
	plan.Parent = manifest.BackupID
	plan.parentManifest = manifest
	return nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// backupContainersToStop names the compose projects that would be brought down.
// It is best effort: `plan` has to work on a host where Docker is not running,
// and the deployment's own module list is a truthful answer to "what could be
// running" even when nothing can be asked what actually is.
func backupContainersToStop(workspace string) []string {
	base := stateDir(workspace)
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment == "" {
		return []string{}
	}
	names := []string{}
	if cli, err := compose.Detect(); err == nil {
		if a, casksRoot, _, err := loadDeploymentApp(base, active.ActiveDeployment, cli); err == nil {
			for _, name := range runningCasks(a, casksRoot) {
				names = append(names, "anas_"+name)
			}
			return names
		}
	}
	if manifest, err := loadDeploymentManifest(deploymentArtifactDir(base, active.ActiveDeployment)); err == nil {
		for _, name := range manifest.ModuleOrder {
			if name == "core" {
				continue
			}
			names = append(names, "anas_"+name)
		}
	}
	return names
}

func crashConsistencyMessage(mode string, snapshotCapable bool) string {
	if snapshotCapable && mode != backupModeCopy {
		return "--no-stop was given; the Btrfs snapshot is atomic, so the backup is crash-consistent — " +
			"equivalent to pulling the power, which most services recover from but none guarantee"
	}
	return "--no-stop was given and this backup copies files while they are being written, " +
		"so it captures no single point in time at all; a database copied this way is likely to be unusable"
}

// backupActions is the execution preview. The order and the op names are the
// contract; the step numbers are not, and a caller must not assume they are
// contiguous.
func backupActions(plan *backupPlan) []backupAction {
	actions := []backupAction{}
	step := 0
	add := func(op, target string, count int) {
		step++
		actions = append(actions, backupAction{Step: step, Op: op, Target: target, Count: count})
	}
	add("acquire_lock", filepath.Join(stateDir(plan.Workspace), "state", "lock"), 0)
	if plan.StopContainers {
		add("stop_containers", "", len(plan.ContainersToStop))
	}
	if plan.existingSnapshot != nil {
		add("use_snapshot", snapshotRoot(plan.Workspace, plan.existingSnapshot.ID), 0)
	} else if plan.useSnapshot {
		add("snapshot_data", filepath.Join(snapshotsDir(plan.Workspace), "<new-id>", "data"), 0)
		add("copy_state", filepath.Join(snapshotsDir(plan.Workspace), "<new-id>", "meta"), 0)
		actions = append(actions, backupAction{
			Step: len(actions) + 1, Op: "copy_deployment",
			Target: filepath.Join(snapshotsDir(plan.Workspace), "<new-id>", "deployment"), Method: "reflink",
		})
		step = len(actions)
		add("seal_snapshot", filepath.Join(snapshotsDir(plan.Workspace), "<new-id>"), 0)
	}
	// Before the transfer, deliberately. This is the line that keeps the outage
	// proportional to the snapshot rather than to the data.
	if plan.StopContainers {
		add("start_containers", "", len(plan.ContainersToStop))
	}
	destRoot := backupRoot(plan.Dest, "<new-id>")
	switch plan.Mode {
	case backupModeSendFile:
		add("send_stream", backupStreamPath(destRoot), 0)
		add("send_metadata", backupMetaTarPath(destRoot), 0)
	case backupModeSend:
		add("send_stream", backupDataPath(destRoot), 0)
		add("send_metadata", backupMetaTarPath(destRoot), 0)
	case backupModeSnapshot:
		add("snapshot_data", backupDataPath(destRoot), 0)
		add("copy_state", filepath.Join(destRoot, "meta"), 0)
	default:
		add("copy_files", destRoot, 0)
	}
	add("finalize", backupManifestPath(destRoot), 0)
	return actions
}

func firstUnavailableReason(modes []backupModeReport) string {
	for _, mode := range modes {
		if mode.ID == backupModeCopy && mode.Reason != "" {
			return mode.Reason
		}
	}
	return reasonDestNotWritable
}

// ---------------------------------------------------------------- create

type backupOutcome struct {
	BackupID         string          `json:"backup_id"`
	Mode             string          `json:"mode"`
	Dest             string          `json:"dest"`
	Incremental      bool            `json:"incremental"`
	Parent           string          `json:"parent,omitempty"`
	TransferredBytes int64           `json:"transferred_bytes"`
	StartedAt        string          `json:"started_at"`
	FinishedAt       string          `json:"finished_at"`
	DowntimeSeconds  int             `json:"downtime_seconds"`
	SnapshotID       string          `json:"snapshot_id,omitempty"`
	Warnings         []backupWarning `json:"warnings"`
}

// createBackup runs the plan. The caller must already hold the exclusive lock.
func createBackup(workspace string, plan *backupPlan, opts backupOptions) (*backupOutcome, error) {
	base := stateDir(workspace)
	started := time.Now().UTC()
	for _, warning := range plan.Warnings {
		emitWarning(opts.json, warning.Code, warning.Message)
	}

	source, snapshotID, downtime, err := prepareBackupSource(workspace, base, plan, opts)
	if err != nil {
		return nil, err
	}

	id, err := newBackupID()
	if err != nil {
		return nil, err
	}
	destRoot := backupTempRoot(plan.Dest, id)
	if err := os.MkdirAll(destRoot, 0700); err != nil {
		return nil, failuref("dest_unwritable", "create %s: %v", destRoot, err)
	}
	cleanup := func() { _ = removeBackupTree(destRoot) }

	req := transferRequest{
		source: source, dest: plan.Dest, destRoot: destRoot, mode: plan.Mode,
		parent: plan.parentManifest, workspace: workspace, json: opts.json,
	}
	if plan.parentManifest != nil {
		req.parentSnapshotData = snapshotDataPath(snapshotRoot(workspace, plan.parentManifest.SourceSnapshot))
	}
	result, err := transferBackup(req)
	if err != nil {
		cleanup()
		return nil, err
	}

	emitProgress(opts.json, "finalize", 0, 0, "files")
	manifest := &backupManifest{
		BackupID: id, Mode: plan.Mode, CreatedAt: started.Format(time.RFC3339),
		SourceSnapshot: snapshotID, Incremental: plan.Incremental, Parent: plan.Parent,
		SizeBytes: result.bytes, DeploymentID: source.deploymentID,
		ConfigDigest: source.configDigest, Casks: source.casks,
		Channels: result.channels, Complete: true,
	}
	if err := writeBackupManifest(destRoot, manifest); err != nil {
		cleanup()
		return nil, failuref("finalize_failed", "write the backup manifest: %v", err)
	}
	// The rename is what publishes the backup. Until it happens the directory
	// carries the temporary prefix and no listing will show it, so an
	// interrupted transfer is invisible rather than misleading.
	final := backupRoot(plan.Dest, id)
	if err := os.Rename(destRoot, final); err != nil {
		cleanup()
		return nil, failuref("finalize_failed", "publish the backup: %v", err)
	}

	finished := time.Now().UTC()
	return &backupOutcome{
		BackupID: id, Mode: plan.Mode, Dest: plan.Dest,
		Incremental: plan.Incremental, Parent: plan.Parent,
		TransferredBytes: result.bytes,
		StartedAt:        started.Format(time.RFC3339), FinishedAt: finished.Format(time.RFC3339),
		DowntimeSeconds: downtime, SnapshotID: snapshotID, Warnings: plan.Warnings,
	}, nil
}

// prepareBackupSource stops the services, freezes the source, and starts them
// again — in that order, and with the restart guaranteed by both a deferred
// call and an on-disk transaction.
func prepareBackupSource(workspace, base string, plan *backupPlan, opts backupOptions) (*backupSource, string, int, error) {
	if plan.existingSnapshot != nil {
		return snapshotBackupSource(workspace, plan.existingSnapshot), plan.existingSnapshot.ID, 0, nil
	}

	var txn *containerTransaction
	var a *app
	var casksRoot string
	downtimeStart := time.Now()
	downtime := 0

	if plan.StopContainers {
		active, err := loadActiveState(base)
		if err != nil {
			return nil, "", 0, err
		}
		if active.ActiveDeployment != "" {
			cli, err := compose.Detect()
			if err != nil {
				return nil, "", 0, failuref("compose_missing", "%v", err)
			}
			a, casksRoot, _, err = loadDeploymentApp(base, active.ActiveDeployment, cli)
			if err != nil {
				return nil, "", 0, err
			}
			emitProgress(opts.json, "stop_containers", 0, 0, "containers")
			txn, err = beginContainerTransaction(base, a, casksRoot, active.ActiveDeployment)
			if err != nil {
				// The transaction record already exists, so the restart below
				// still runs and the compensation still applies if this process
				// dies first.
				restartAfterBackup(base, a, casksRoot, txn, opts.json)
				return nil, "", 0, failuref("stop_failed", "%v", err)
			}
		}
	}

	// The restart is deferred rather than written once at the end, so that
	// every error path below goes back through it.
	restarted := false
	restart := func() {
		if restarted {
			return
		}
		restarted = true
		downtime = int(time.Since(downtimeStart).Seconds())
		restartAfterBackup(base, a, casksRoot, txn, opts.json)
	}
	defer restart()

	if !plan.useSnapshot {
		// No snapshot means the copy reads live files, so the services have to
		// stay down until the transfer finishes. The restart still happens, on
		// the deferred path, after transferBackup returns.
		source, err := workspaceBackupSource(workspace)
		if err != nil {
			return nil, "", 0, err
		}
		return source, "", downtime, nil
	}

	emitProgress(opts.json, "snapshot_data", 0, 0, "bytes")
	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: snapshotReasonPreBackup,
		label: "before backing up to " + plan.Dest, json: opts.json,
	})
	if err != nil {
		return nil, "", 0, err
	}
	restart()
	return snapshotBackupSource(workspace, meta), meta.ID, downtime, nil
}

// restartAfterBackup puts back exactly what was stopped. A failure here is
// reported loudly and does not abort: the transaction record survives, and the
// next command to take the exclusive lock tries again.
func restartAfterBackup(base string, a *app, casksRoot string, txn *containerTransaction, jsonMode bool) {
	if txn == nil || a == nil {
		return
	}
	emitProgress(jsonMode, "start_containers", 0, int64(len(txn.Casks)), "containers")
	if err := finishContainerTransaction(base, a, casksRoot, txn); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: could not start the containers this backup stopped: %v\n"+
				"         the record at %s will be retried by the next anas command\n",
			err, transactionPath(base, txn.ID))
	}
}

// emitWarning writes one JSON Lines warning record to stderr, alongside the
// progress records, so a caller sees it in the same stream and in order.
func emitWarning(jsonMode bool, code, message string) {
	if jsonMode {
		fmt.Fprintf(os.Stderr, "{\"type\":\"warning\",\"code\":%q,\"message\":%q}\n", code, message)
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %s\n", message)
}

// describeBackupReason maps a reason code to the sentence a human needs. The
// code is what a caller branches on; this is only ever shown, never parsed.
func describeBackupReason(reason string) string {
	switch reason {
	case reasonDestNotSpecified:
		return "no destination was given"
	case reasonDestNotExist:
		return "the destination directory does not exist"
	case reasonDestNotWritable:
		return "the destination directory cannot be written to"
	case reasonDestNotBtrfs:
		return "the destination is not on Btrfs"
	case reasonDestNotSameFilesystem:
		return "the destination is on a different filesystem from the workspace"
	case reasonSourceNotBtrfs:
		return "the workspace is not on Btrfs, so no snapshot can be taken"
	case reasonDataNotSubvolume:
		return "the data directory is not a Btrfs subvolume"
	case reasonDataIsMountpoint:
		return "the data directory is a mount point, which the restore path cannot rename aside"
	case reasonBtrfsToolMissing:
		return "the btrfs command is not installed"
	case reasonInsufficientPrivilege:
		return "btrfs send needs CAP_SYS_ADMIN, and creating or snapshotting subvolumes does not, so this is a separate gate"
	case reasonInsufficientSpace:
		return "the destination does not have room for the estimated size"
	}
	return reason
}
