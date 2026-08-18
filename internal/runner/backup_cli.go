package runner

// `anas backup` — the user-facing surface.
//
// Every subcommand honours docs/contracts/README.md: with --json, stdout holds
// exactly one JSON document and nothing else, progress and warnings go to
// stderr as JSON Lines, and the exit code distinguishes a usage mistake (2)
// from a missing confirmation (3) from an unmet precondition (4).
//
// The interactive form is not a second implementation. It probes with
// `capabilities`, asks about what came back available, and then calls the same
// non-interactive path a script would. The future web adapter will call the
// same typed application use case rather than parse this CLI's JSON. One set
// of rules about what is possible, in one place.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runBackup(args []string) error {
	if len(args) == 0 {
		return runBackupInteractive(nil)
	}
	if strings.HasPrefix(args[0], "-") {
		// A subcommand is a word, never a flag. Falling into the interactive
		// form here would be worse still: `anas backup --json` would start
		// asking questions of a caller that asked for a JSON document.
		return usageErrorf("usage: anas backup capabilities|plan|create|list|restore|verify ... [--json]")
	}
	sub, rest := args[0], args[1:]
	jsonMode := wantsJSON(rest)
	err := dispatchBackup(sub, rest, jsonMode)
	// A reported error has already written its document. Emitting another here
	// would put two JSON values on stdout, which is what the "exactly one
	// document" rule exists to prevent.
	if err != nil && jsonMode && !Reported(err) {
		return emitJSONError(err)
	}
	return err
}

func dispatchBackup(sub string, args []string, jsonMode bool) error {
	switch sub {
	case "capabilities":
		return runBackupCapabilities(args, jsonMode)
	case "plan":
		return runBackupPlan(args, jsonMode)
	case "create":
		return runBackupCreate(args, jsonMode)
	case "list":
		return runBackupList(args, jsonMode)
	case "restore":
		return runBackupRestore(args, jsonMode)
	case "verify":
		return runBackupVerify(args, jsonMode)
	default:
		return usageErrorf("unknown backup command %q; expected capabilities, plan, create, list, restore or verify", sub)
	}
}

// backupFlags carries the options every subcommand shares.
type backupFlags struct {
	fs        *flag.FlagSet
	workspace *string
	to        *string
	json      *bool
	yes       *bool
}

func newBackupFlags(name string) backupFlags {
	fs := flag.NewFlagSet("backup "+name, flag.ContinueOnError)
	f := backupFlags{
		fs:        fs,
		workspace: fs.String("w", "", "workspace path"),
		to:        fs.String("to", "", "destination directory"),
		json:      fs.Bool("json", false, "machine-readable output"),
		yes:       fs.Bool("y", false, "confirm without prompting"),
	}
	fs.StringVar(f.workspace, "workspace", "", "workspace path")
	fs.BoolVar(f.yes, "yes", false, "confirm without prompting")
	return f
}

func (f backupFlags) parse(args []string) ([]string, error) {
	positional, err := parseInterspersed(f.fs, args)
	if err != nil {
		return nil, usageErrorf("%s", err.Error())
	}
	return positional, nil
}

// ---------------------------------------------------------------- capabilities

func runBackupCapabilities(args []string, jsonMode bool) error {
	f := newBackupFlags("capabilities")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas backup capabilities [--to <dest>] [-w <workspace>] [--json]")
	}
	workspace, err := resolveBackupWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	caps, err := probeBackupCapabilities(workspace, *f.to)
	if err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(backupCapabilitiesDocument(caps))
	}
	printBackupCapabilities(caps)
	return nil
}

// backupCapabilitiesDocument shapes the JSON. dest is null rather than absent
// when no destination was given, so a caller always finds the key.
func backupCapabilitiesDocument(caps *backupCapabilities) map[string]any {
	document := map[string]any{
		"api_version": cliAPIVersion, "ok": true,
		"workspace": caps.Workspace, "source": caps.Source,
		"tools": caps.Tools, "privileged": caps.Privileged,
		"estimate": caps.Estimate, "modes": caps.Modes,
	}
	document["dest"] = caps.Dest
	if caps.Recommended != "" {
		document["recommended"] = caps.Recommended
	}
	return document
}

func printBackupCapabilities(caps *backupCapabilities) {
	fmt.Printf("workspace: %s\n", caps.Workspace)
	fmt.Printf("source:    %s", orUnknown(caps.Source.FSType))
	if caps.Source.DataIsSubvolume {
		fmt.Print(", data/ is a subvolume")
	}
	if caps.Source.FSID != "" {
		fmt.Printf(", fsid %s", caps.Source.FSID)
	}
	fmt.Println()
	if caps.Dest != nil {
		fmt.Printf("dest:      %s (%s", caps.Dest.Path, orUnknown(caps.Dest.FSType))
		if caps.Dest.FreeBytes != nil {
			fmt.Printf(", %s free", formatBytes(*caps.Dest.FreeBytes))
		}
		fmt.Println(")")
	}
	fmt.Printf("estimate:  %s (data %s, state %s, deployment %s)\n",
		formatBytes(caps.Estimate.TotalBytes), formatBytes(caps.Estimate.DataBytes),
		formatBytes(caps.Estimate.StateBytes), formatBytes(caps.Estimate.ActiveDeploymentBytes))
	fmt.Printf("privileged: %t   btrfs: %t   rsync: %t\n\n",
		caps.Privileged, caps.Tools["btrfs"], caps.Tools["rsync"])
	for _, mode := range caps.Modes {
		if mode.Available {
			suffix := ""
			if mode.Incremental {
				suffix = fmt.Sprintf("  incremental against %s", strings.Join(mode.Parents, ", "))
			}
			fmt.Printf("  %-10s available%s\n", mode.ID, suffix)
			for _, note := range mode.Notes {
				fmt.Printf("  %-10s   note: %s\n", "", describeBackupNote(note))
			}
			continue
		}
		fmt.Printf("  %-10s unavailable: %s\n", mode.ID, describeBackupReason(mode.Reason))
	}
	if caps.Recommended != "" {
		fmt.Printf("\nrecommended: %s\n", caps.Recommended)
	} else {
		fmt.Printf("\nno mode can run against this destination\n")
	}
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func describeBackupNote(note string) string {
	switch note {
	case noteRestoreRequiresBtrfs:
		return "the result can only be restored onto Btrfs"
	case noteSnapshotsExcluded:
		return "snapshots/ is not carried; it is a copy-on-write reference to the same disk"
	case noteNoIncremental:
		return "every run transfers everything"
	case noteCrashConsistentOnly:
		return "with --no-stop this is only crash-consistent"
	case notePlaintextSecrets:
		return "the backup carries plaintext secrets off this host"
	}
	return note
}

// ---------------------------------------------------------------- plan

func runBackupPlan(args []string, jsonMode bool) error {
	f := newBackupFlags("plan")
	mode := f.fs.String("mode", "", "snapshot, send, send-file or copy")
	snapshotID := f.fs.String("snapshot", "", "back up an existing snapshot")
	parent := f.fs.String("parent", "", "send incrementally against this backup")
	noStop := f.fs.Bool("no-stop", false, "do not stop containers")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas backup plan --to <dest> [--mode <mode>] [--snapshot <id>] [--parent <id>] [--no-stop] [-w <workspace>] [--json]")
	}
	workspace, err := resolveBackupWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	plan, err := buildBackupPlan(workspace, backupOptions{
		dest: *f.to, mode: *mode, snapshotID: *snapshotID,
		parent: *parent, noStop: *noStop, json: jsonMode,
	})
	if err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(backupPlanDocument(plan))
	}
	printBackupPlan(plan)
	return nil
}

func backupPlanDocument(plan *backupPlan) map[string]any {
	return map[string]any{
		"api_version": cliAPIVersion, "ok": true,
		"workspace": plan.Workspace, "mode": plan.Mode, "dest": plan.Dest,
		"incremental": plan.Incremental, "parent": plan.Parent,
		"estimate": plan.Estimate, "includes": plan.Includes, "excludes": plan.Excludes,
		"stop_containers": plan.StopContainers, "containers_to_stop": plan.ContainersToStop,
		"estimated_downtime_seconds": plan.EstimatedDowntimeSeconds,
		"warnings":                   plan.Warnings, "actions": plan.Actions,
	}
}

func printBackupPlan(plan *backupPlan) {
	fmt.Printf("workspace: %s\nmode:      %s\ndest:      %s\n", plan.Workspace, plan.Mode, plan.Dest)
	if plan.Incremental {
		fmt.Printf("parent:    %s\n", plan.Parent)
	}
	fmt.Printf("transfer:  up to %s\n", formatBytes(plan.Estimate.TransferBytes))
	if plan.StopContainers {
		fmt.Printf("downtime:  about %ds, stopping %d module(s)\n",
			plan.EstimatedDowntimeSeconds, len(plan.ContainersToStop))
	} else {
		fmt.Println("downtime:  none; nothing is stopped")
	}
	for _, warning := range plan.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning.Message)
	}
	fmt.Println("\nactions:")
	for _, action := range plan.Actions {
		detail := action.Target
		if action.Count > 0 {
			detail = fmt.Sprintf("%d", action.Count)
		}
		fmt.Printf("  %2d  %-18s %s\n", action.Step, action.Op, detail)
	}
}

// ---------------------------------------------------------------- create

func runBackupCreate(args []string, jsonMode bool) error {
	f := newBackupFlags("create")
	mode := f.fs.String("mode", "", "snapshot, send, send-file or copy")
	snapshotID := f.fs.String("snapshot", "", "back up an existing snapshot")
	parent := f.fs.String("parent", "", "send incrementally against this backup")
	noStop := f.fs.Bool("no-stop", false, "do not stop containers")
	skipUserData := f.fs.Bool("skip-userdata", false, "back up the deployment only, without "+workspaceUserDataDir+"/")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas backup create --to <dest> [--mode <mode>] [--snapshot <id>] [--parent <id>] [--no-stop] [--skip-userdata] [-y] [-w <workspace>] [--json]")
	}
	workspace, err := resolveBackupWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	opts := backupOptions{
		dest: *f.to, mode: *mode, snapshotID: *snapshotID,
		parent: *parent, noStop: *noStop, yes: *f.yes, json: jsonMode,
		skipUserData: *skipUserData,
	}
	announceWorkspace(workspace)
	plan, err := buildBackupPlan(workspace, opts)
	if err != nil {
		return err
	}
	// Two things can need a confirmation, and they are different questions.
	// Stopping services is a real interruption; --no-stop gives up the
	// consistency the stop was buying.
	if opts.noStop {
		if err := confirmDestructive(
			"Back up without stopping the containers, giving up a consistent point in time", opts.yes); err != nil {
			return err
		}
	} else if plan.StopContainers && len(plan.ContainersToStop) > 0 {
		if err := confirmDestructive(
			fmt.Sprintf("Stop %d module(s) for about %ds to take the backup",
				len(plan.ContainersToStop), plan.EstimatedDowntimeSeconds), opts.yes); err != nil {
			return err
		}
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	cleanStaleBackupTemp(plan.Dest)

	outcome, err := createBackup(workspace, plan, opts)
	if err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true,
			"backup_id": outcome.BackupID, "mode": outcome.Mode, "dest": outcome.Dest,
			"incremental": outcome.Incremental, "parent": outcome.Parent,
			"transferred_bytes": outcome.TransferredBytes,
			"started_at":        outcome.StartedAt, "finished_at": outcome.FinishedAt,
			"downtime_seconds": outcome.DowntimeSeconds, "snapshot_id": outcome.SnapshotID,
			"warnings": outcome.Warnings,
		})
	}
	fmt.Printf("backup %s written to %s\nmode: %s   transferred: %s   downtime: %ds\n",
		outcome.BackupID, outcome.Dest, outcome.Mode,
		formatBytes(outcome.TransferredBytes), outcome.DowntimeSeconds)
	if outcome.SnapshotID != "" {
		fmt.Printf("from snapshot: %s\n", outcome.SnapshotID)
	}
	return nil
}

// ---------------------------------------------------------------- list

func runBackupList(args []string, jsonMode bool) error {
	f := newBackupFlags("list")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 || strings.TrimSpace(*f.to) == "" {
		return usageErrorf("usage: anas backup list --to <dest> [--json]")
	}
	dest, err := absoluteDest(*f.to)
	if err != nil {
		return err
	}
	backups, err := listBackups(dest)
	if err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true, "dest": dest, "backups": backups,
		})
	}
	if len(backups) == 0 {
		fmt.Printf("no backups in %s\n", dest)
		return nil
	}
	fmt.Printf("%-30s  %-10s  %-22s  %-10s  %s\n", "ID", "MODE", "CREATED", "SIZE", "FLAGS")
	for _, manifest := range backups {
		flags := []string{}
		if !manifest.Complete {
			flags = append(flags, "incomplete")
		}
		if manifest.Incremental {
			flags = append(flags, "incremental")
		}
		if manifest.ChainBroken {
			flags = append(flags, "chain-broken")
		}
		fmt.Printf("%-30s  %-10s  %-22s  %-10s  %s\n",
			manifest.BackupID, manifest.Mode, manifest.CreatedAt,
			formatBytes(manifest.SizeBytes), strings.Join(flags, ","))
	}
	return nil
}

// ---------------------------------------------------------------- restore

func runBackupRestore(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	from := fs.String("from", "", "directory holding the backups")
	backupID := fs.String("backup-id", "", "which backup to restore")
	dryRun := fs.Bool("dry-run", false, "list what would be replaced")
	jsonFlag := fs.Bool("json", false, "machine-readable output")
	yes := fs.Bool("y", false, "confirm without prompting")
	fs.BoolVar(yes, "yes", false, "confirm without prompting")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	_ = jsonFlag
	if len(positional) != 0 || strings.TrimSpace(*from) == "" {
		return usageErrorf("usage: anas backup restore --from <src> -w <workspace> [--backup-id <id>] [--dry-run] [-y] [--json]")
	}
	// Restoring replaces live data, and ANAS_WORKSPACE set once in a shell
	// profile is the easiest thing to leave stale and pointed somewhere else.
	// Only the flag is accepted.
	workspace, err := resolveWorkspaceStrict(*workspaceFlag, "backup restore")
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	dest, err := absoluteDest(*from)
	if err != nil {
		return err
	}
	announceWorkspace(workspace)
	manifest, all, err := selectBackup(dest, *backupID)
	if err != nil {
		return err
	}
	if *dryRun {
		targets := backupRestoreTargets(workspace, manifest)
		if jsonMode {
			return emitJSON(map[string]any{
				"api_version": cliAPIVersion, "ok": true, "dry_run": true,
				"workspace": workspace, "backup_id": manifest.BackupID,
				"mode": manifest.Mode, "deployment_id": manifest.DeploymentID,
				"would_replace": targets,
			})
		}
		fmt.Printf("restoring %s would replace:\n", manifest.BackupID)
		for _, target := range targets {
			fmt.Printf("  %s\n", target)
		}
		return nil
	}
	if workspaceLooksUsed(workspace) {
		if err := confirmDestructive(
			fmt.Sprintf("Restore %s over %s, replacing its config, secrets and data",
				manifest.BackupID, workspace), *yes); err != nil {
			return err
		}
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	outcome, err := restoreBackup(workspace, dest, manifest, all, jsonMode)
	if err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true,
			"workspace": outcome.Workspace, "backup_id": outcome.BackupID,
			"mode": outcome.Mode, "restored": outcome.Restored, "verify": outcome.Verify,
			"deployment_id": outcome.DeploymentID, "next_steps": outcome.NextSteps,
		})
	}
	fmt.Print(describeRestoreSummary(outcome))
	for _, problem := range outcome.Verify.Problems {
		fmt.Fprintf(os.Stderr, "problem: %s: %s\n", problem.Code, problem.Message)
	}
	return nil
}

// ---------------------------------------------------------------- verify

func runBackupVerify(args []string, jsonMode bool) error {
	f := newBackupFlags("verify")
	backupID := f.fs.String("backup-id", "", "check only this backup")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 || strings.TrimSpace(*f.to) == "" {
		return usageErrorf("usage: anas backup verify --to <dest> [--backup-id <id>] [--json]")
	}
	dest, err := absoluteDest(*f.to)
	if err != nil {
		return err
	}
	all, err := listBackups(dest)
	if err != nil {
		return err
	}
	// Presence is judged over every backup at the destination, even when only
	// one is being checked: a parent outside the filter is still either there
	// or not.
	present := map[string]bool{}
	for _, manifest := range all {
		if manifest.Complete {
			present[manifest.BackupID] = true
		}
	}
	checked := all
	if *backupID != "" {
		if err := validateBackupID(*backupID); err != nil {
			return err
		}
		checked = nil
		for _, manifest := range all {
			if manifest.BackupID == *backupID {
				checked = append(checked, manifest)
			}
		}
		if len(checked) == 0 {
			return preconditionErrorf("backup_missing", "no backup %s in %s", *backupID, dest)
		}
	}
	problems := []backupProblem{}
	for _, manifest := range checked {
		problems = append(problems, verifyBackup(dest, manifest, present)...)
	}
	if jsonMode {
		if err := emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": len(problems) == 0,
			"dest": dest, "checked": len(checked), "problems": problems,
		}); err != nil {
			return err
		}
	} else {
		fmt.Printf("checked %d backup(s) in %s\n", len(checked), dest)
		for _, problem := range problems {
			fmt.Printf("%s\t%s\t%s\n", problem.BackupID, problem.Code, problem.Message)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	// Written to be run from cron, so damage has to be a non-zero exit as well
	// as ok: false in the body. A cron job that only looks at the status is the
	// normal case, and the most common way a backup system fails is that
	// somebody believes there is a backup and there is not.
	return &CLIError{
		Code: "verify_failed", Message: fmt.Sprintf("%d problem(s)", len(problems)),
		Exit: exitFailure, Reported: jsonMode,
	}
}

// ---------------------------------------------------------------- helpers

func resolveBackupWorkspace(explicit string) (string, error) {
	workspace, err := resolveWorkspace(explicit)
	if err != nil {
		return "", usageErrorf("%s", err.Error())
	}
	return workspace, nil
}

func absoluteDest(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", usageErrorf("a destination directory is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", usageErrorf("resolve %s: %v", path, err)
	}
	return absolute, nil
}
