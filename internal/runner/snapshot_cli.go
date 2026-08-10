package runner

// `anas snapshot` — the user-facing surface of the snapshot subsystem.
//
// Every subcommand honours docs/contracts/README.md: with --json, stdout holds
// exactly one JSON document and nothing else, progress and warnings go to
// stderr, and the exit code distinguishes a usage mistake (2) from a missing
// confirmation (3) from an unmet precondition (4).

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runSnapshot(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		// A subcommand is a word, never a flag. `anas snapshot --json` reached
		// the unknown-subcommand branch and reported "--json" as the name.
		return usageErrorf("usage: anas snapshot list|show|create|restore|pin|unpin|delete|prune|verify|path")
	}
	sub, rest := args[0], args[1:]
	jsonMode := wantsJSON(rest)
	err := dispatchSnapshot(sub, rest, jsonMode)
	// Reported errors have already written their document. Emitting another
	// here would put two JSON values on stdout, which is exactly what the
	// "exactly one document" rule exists to prevent.
	if err != nil && jsonMode && !Reported(err) {
		return emitJSONError(err)
	}
	return err
}

func dispatchSnapshot(sub string, args []string, jsonMode bool) error {
	switch sub {
	case "list":
		return runSnapshotList(args, jsonMode)
	case "show":
		return runSnapshotShow(args, jsonMode)
	case "create":
		return runSnapshotCreate(args, jsonMode)
	case "restore":
		return runSnapshotRestore(args, jsonMode)
	case "pin", "unpin":
		return runSnapshotPin(sub, args, jsonMode)
	case "delete":
		return runSnapshotDelete(args, jsonMode)
	case "prune":
		return runSnapshotPrune(args, jsonMode)
	case "verify":
		return runSnapshotVerify(args, jsonMode)
	case "path":
		return runSnapshotPath(args, jsonMode)
	default:
		return usageErrorf("unknown snapshot command %q", sub)
	}
}

// snapshotFlags carries the options every subcommand shares.
type snapshotFlags struct {
	fs        *flag.FlagSet
	workspace *string
	json      *bool
	yes       *bool
}

func newSnapshotFlags(name string) snapshotFlags {
	fs := flag.NewFlagSet("snapshot "+name, flag.ContinueOnError)
	f := snapshotFlags{
		fs:        fs,
		workspace: fs.String("w", "", "workspace path"),
		json:      fs.Bool("json", false, "machine-readable output"),
		yes:       fs.Bool("y", false, "confirm without prompting"),
	}
	fs.StringVar(f.workspace, "workspace", "", "workspace path")
	fs.BoolVar(f.yes, "yes", false, "confirm without prompting")
	return f
}

func (f snapshotFlags) parse(args []string) ([]string, error) {
	positional, err := parseInterspersed(f.fs, args)
	if err != nil {
		return nil, usageErrorf("%s", err.Error())
	}
	return positional, nil
}

// ---------------------------------------------------------------- list

type snapshotListEntry struct {
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
	Casks                map[string]string `json:"casks,omitempty"`
	Healthy              bool              `json:"healthy"`
}

func runSnapshotList(args []string, jsonMode bool) error {
	f := newSnapshotFlags("list")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas snapshot list [-w <workspace>] [--json]")
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	all, err := listSnapshots(workspace)
	if err != nil {
		return failuref("scan_failed", "%s", err.Error())
	}
	entries := make([]snapshotListEntry, 0, len(all))
	current := currentConfigDigest(workspace)
	for _, meta := range all {
		entries = append(entries, snapshotListEntry{
			ID: meta.ID, Kind: meta.Kind, Pinned: meta.Pinned, CreatedAt: meta.CreatedAt,
			Reason: meta.Reason, Label: meta.Label, DeploymentID: meta.DeploymentID,
			Complete: meta.Complete,
			// False when the config on disk differs from the one the snapshot
			// carries. That is normal, not a fault: a pre-upgrade snapshot
			// captures the old deployment while the user's config file has
			// already moved on to the version they are about to apply.
			ConfigMatchesCurrent: current != "" && current == meta.ConfigDigest,
			// nil, never 0: without Btrfs qgroups there is no measurement, and
			// reporting 0 bytes would be a false one.
			SizeBytes: nil,
			Casks:     meta.Casks,
			Healthy:   len(verifySnapshot(workspace, meta)) == 0,
		})
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true, "workspace": workspace,
			"keep_auto": workspaceKeepAuto(workspace), "snapshots": entries,
		})
	}
	if len(entries) == 0 {
		fmt.Printf("no snapshots in %s\n", snapshotsDir(workspace))
		return nil
	}
	fmt.Printf("%-30s  %-6s  %-6s  %-22s  %s\n", "ID", "KIND", "PINNED", "REASON", "LABEL")
	for _, entry := range entries {
		flags := ""
		if !entry.Complete {
			flags = "  [incomplete]"
		} else if !entry.Healthy {
			flags = "  [damaged]"
		}
		fmt.Printf("%-30s  %-6s  %-6t  %-22s  %s%s\n", entry.ID, entry.Kind, entry.Pinned, entry.Reason, entry.Label, flags)
	}
	fmt.Printf("\nkeep_auto: %d\n", workspaceKeepAuto(workspace))
	return nil
}

// ---------------------------------------------------------------- show

func runSnapshotShow(args []string, jsonMode bool) error {
	f := newSnapshotFlags("show")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas snapshot show <id> [-w <workspace>] [--json]")
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	meta, err := loadSnapshot(workspace, positional[0])
	if err != nil {
		return err
	}
	return reportSnapshot(workspace, meta, jsonMode)
}

func reportSnapshot(workspace string, meta *snapshotMeta, jsonMode bool) error {
	problems := verifySnapshot(workspace, *meta)
	current := currentConfigDigest(workspace)
	matches := current != "" && current == meta.ConfigDigest
	if jsonMode {
		// ok means the command succeeded, not that the snapshot is healthy.
		// Health is reported in problems, and judging it is `verify`'s job —
		// conflating the two would make `show` of a damaged snapshot
		// indistinguishable from `show` of a snapshot that does not exist.
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true, "workspace": workspace,
			"snapshot": meta, "config_matches_current": matches,
			"size_bytes": (*int64)(nil), "problems": problems,
		})
	}
	fmt.Printf("id:            %s\nkind:          %s\npinned:        %t\ncreated_at:    %s\nreason:        %s\n",
		meta.ID, meta.Kind, meta.Pinned, meta.CreatedAt, meta.Reason)
	if meta.Label != "" {
		fmt.Printf("label:         %s\n", meta.Label)
	}
	fmt.Printf("deployment:    %s\ndata:          %s\ncomplete:      %t\nartifact_copy: %s\n",
		meta.DeploymentID, meta.Path, meta.Complete, meta.ArtifactCopy)
	fmt.Printf("config_matches_current: %t\n", matches)
	if len(meta.Casks) > 0 {
		names := make([]string, 0, len(meta.Casks))
		for name := range meta.Casks {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Println("casks:")
		for _, name := range names {
			fmt.Printf("  %s: %s\n", name, meta.Casks[name])
		}
	}
	for _, problem := range problems {
		fmt.Fprintf(os.Stderr, "problem: %s: %s\n", problem.Code, problem.Message)
	}
	return nil
}

// ---------------------------------------------------------------- create

func runSnapshotCreate(args []string, jsonMode bool) error {
	f := newSnapshotFlags("create")
	label := f.fs.String("label", "", "free-text label")
	reason := f.fs.String("reason", snapshotReasonManual, "why the snapshot was taken")
	includeUserData := f.fs.Bool("include-userdata", false, "also capture "+workspaceUserDataDir+"/")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas snapshot create [--label TEXT] [--reason manual] [--include-userdata] [-w <workspace>] [--json]")
	}
	if !validSnapshotReason(*reason) {
		return usageErrorf("unknown snapshot reason %q", *reason)
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	announceWorkspace(workspace)
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindManual, reason: *reason, label: *label, json: jsonMode,
		includeUserData: *includeUserData,
	})
	if err != nil {
		return err
	}
	return reportSnapshot(workspace, meta, jsonMode)
}

// resolveUserDataRestore decides whether a restore also replaces user content.
//
// The default is no, and it is no in every non-interactive case, because the
// files at stake were written after the snapshot and have nothing to do with
// the deployment being restored: rewinding them is a deletion the user did not
// ask for. -y means "do not ask me", not "do the more destructive thing", so it
// selects the same default rather than opting in.
//
// An interactive operator is asked, because they are the one who knows whether
// this is a rollback of a bad deploy (keep the files) or a recovery from a bad
// state (take everything back). The question is only worth asking when the
// snapshot actually holds user content.
func resolveUserDataRestore(requested bool, meta *snapshotMeta, yes bool) (bool, error) {
	captured := meta.capturedTree(snapshotTreeUserData)
	if requested {
		if !captured {
			return false, preconditionErrorf("userdata_not_captured",
				"--restore-userdata was given, but snapshot %s does not contain %s/: %s",
				meta.ID, workspaceUserDataDir, describeUserDataCoverage(meta))
		}
		return true, nil
	}
	if !captured || yes || !isTerminal(os.Stdin.Fd()) {
		return false, nil
	}
	answer, err := confirm(fmt.Sprintf(
		"Snapshot %s also contains %s/. Replace the current user files with it too?",
		meta.ID, workspaceUserDataDir), false)
	if err != nil {
		return false, failuref("stdin_unavailable", "%s", err.Error())
	}
	return answer, nil
}

// describeUserDataCoverage turns the recorded reason into something an operator
// can act on, rather than reporting only that the tree is absent.
func describeUserDataCoverage(meta *snapshotMeta) string {
	reason := ""
	for _, entry := range meta.Coverage {
		if entry.Tree == snapshotTreeUserData {
			reason = entry.Reason
		}
	}
	switch reason {
	case coverageReasonExcluded:
		return "it was taken without --include-userdata"
	case coverageReasonNotSubvolume:
		return userDataDirHint()
	case coverageReasonMissing:
		return "the workspace had no " + workspaceUserDataDir + "/ when it was taken"
	default:
		return "it was taken before user content was captured separately"
	}
}

func userDataDirHint() string {
	return workspaceUserDataDir + "/ is not a Btrfs subvolume, so it cannot be snapshotted; " +
		"back it up with `anas backup` instead"
}

// ---------------------------------------------------------------- restore

func runSnapshotRestore(args []string, jsonMode bool) error {
	f := newSnapshotFlags("restore")
	dryRun := f.fs.Bool("dry-run", false, "list what would be replaced")
	restoreUserData := f.fs.Bool("restore-userdata", false, "also replace "+workspaceUserDataDir+"/ from the snapshot")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas snapshot restore <id> -w <workspace> [--dry-run] [--restore-userdata] [-y] [--json]")
	}
	// Restoring is the one command that replaces live data, and ANAS_WORKSPACE
	// set once in a shell profile is the easiest thing to leave stale and
	// pointed at a different deployment. Only the flag is accepted.
	workspace, err := resolveWorkspaceStrict(*f.workspace, "snapshot restore")
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	announceWorkspace(workspace)
	meta, err := loadSnapshot(workspace, positional[0])
	if err != nil {
		return err
	}
	if *dryRun {
		targets := restoreTargets(workspace, meta, *restoreUserData && meta.capturedTree(snapshotTreeUserData))
		if jsonMode {
			return emitJSON(map[string]any{
				"api_version": cliAPIVersion, "ok": true, "dry_run": true,
				"workspace": workspace, "restore_from": meta.ID,
				"deployment_id": meta.DeploymentID, "would_replace": targets,
			})
		}
		fmt.Printf("restoring %s would replace:\n", meta.ID)
		for _, target := range targets {
			fmt.Printf("  %s\n", target)
		}
		return nil
	}
	if err := confirmDestructive(fmt.Sprintf("Restore %s over %s, replacing its data", meta.ID, workspace), *f.yes); err != nil {
		return err
	}
	wantUserData, err := resolveUserDataRestore(*restoreUserData, meta, *f.yes)
	if err != nil {
		return err
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	outcome, err := restoreSnapshot(workspace, meta, wantUserData, jsonMode)
	if err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true,
			"workspace": outcome.Workspace, "restored_from": outcome.RestoredFrom,
			"pre_restore_snapshot": outcome.PreRestoreSnapshot,
			"restored":             outcome.Restored, "deployment_id": outcome.DeploymentID,
			"next_steps": outcome.NextSteps,
		})
	}
	fmt.Printf("restored %s\npre-restore snapshot: %s\nactive deployment: %s\n\nnext:\n  %s\n",
		outcome.RestoredFrom, outcome.PreRestoreSnapshot, outcome.DeploymentID,
		strings.Join(outcome.NextSteps, "\n  "))
	return nil
}

// ---------------------------------------------------------------- pin/unpin

func runSnapshotPin(sub string, args []string, jsonMode bool) error {
	f := newSnapshotFlags(sub)
	label := f.fs.String("label", "", "free-text label")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas snapshot %s <id> [-w <workspace>] [--json]", sub)
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	meta, err := loadSnapshot(workspace, positional[0])
	if err != nil {
		return err
	}
	meta.Pinned = sub == "pin"
	if *label != "" {
		meta.Label = *label
	}
	if err := writeYAMLAtomic(snapshotMetaFile(snapshotRoot(workspace, meta.ID)), meta, 0600); err != nil {
		return failuref("write_failed", "%s", err.Error())
	}
	if err := rebuildSnapshotIndex(workspace); err != nil {
		return failuref("write_failed", "%s", err.Error())
	}
	return reportSnapshot(workspace, meta, jsonMode)
}

// ---------------------------------------------------------------- delete

func runSnapshotDelete(args []string, jsonMode bool) error {
	f := newSnapshotFlags("delete")
	force := f.fs.Bool("force", false, "delete a pinned snapshot")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas snapshot delete <id> [--force] [-y] [-w <workspace>] [--json]")
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	meta, err := loadSnapshot(workspace, positional[0])
	if err != nil {
		return err
	}
	// Pinning exists to say "do not lose this one". Honouring that against
	// automatic collection but not against a typed delete would make it half a
	// guarantee.
	if meta.Pinned && !*force {
		return preconditionErrorf("snapshot_pinned", "snapshot %s is pinned; pass --force to delete it anyway", meta.ID)
	}
	if err := confirmDestructive(fmt.Sprintf("Delete snapshot %s", meta.ID), *f.yes); err != nil {
		return err
	}
	if err := deleteSnapshot(workspace, meta.ID); err != nil {
		return err
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true, "workspace": workspace, "deleted": meta.ID,
		})
	}
	fmt.Printf("deleted %s\n", meta.ID)
	return nil
}

// ---------------------------------------------------------------- prune

func runSnapshotPrune(args []string, jsonMode bool) error {
	f := newSnapshotFlags("prune")
	dryRun := f.fs.Bool("dry-run", false, "report what would be reclaimed")
	keep := f.fs.Int("keep", -1, "override snapshot.keep_auto")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas snapshot prune [--dry-run] [--keep N] [-y] [-w <workspace>] [--json]")
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	keepAuto := workspaceKeepAuto(workspace)
	if *keep >= 0 {
		keepAuto = *keep
	}
	all, err := listSnapshots(workspace)
	if err != nil {
		return failuref("scan_failed", "%s", err.Error())
	}
	collect, retained, pinned := snapshotsToPrune(all, keepAuto)
	report := func(field string) error {
		listed := make([]map[string]any, 0, len(collect))
		for _, meta := range collect {
			listed = append(listed, map[string]any{
				"id": meta.ID, "kind": meta.Kind, "created_at": meta.CreatedAt, "size_bytes": nil,
			})
		}
		if jsonMode {
			return emitJSON(map[string]any{
				"api_version": cliAPIVersion, "ok": true, "dry_run": *dryRun,
				"keep_auto": keepAuto, field: listed,
				"retained": retained, "pinned_excluded": pinned,
			})
		}
		if len(collect) == 0 {
			fmt.Printf("nothing to reclaim; %d automatic snapshot(s) retained, %d pinned\n", retained, pinned)
			return nil
		}
		for _, meta := range collect {
			fmt.Printf("%s\t%s\t%s\n", field, meta.ID, meta.CreatedAt)
		}
		fmt.Printf("\n%d retained, %d pinned and excluded\n", retained, pinned)
		return nil
	}
	// A dry run is not an optional convenience: before a retention policy runs
	// for the first time the user has to be able to see what it would take.
	if *dryRun {
		return report("would_delete")
	}
	if len(collect) > 0 {
		if err := confirmDestructive(fmt.Sprintf("Delete %d automatic snapshot(s)", len(collect)), *f.yes); err != nil {
			return err
		}
	}
	for _, meta := range collect {
		if err := deleteSnapshot(workspace, meta.ID); err != nil {
			return err
		}
	}
	return report("deleted")
}

// ---------------------------------------------------------------- verify

func runSnapshotVerify(args []string, jsonMode bool) error {
	f := newSnapshotFlags("verify")
	rebuild := f.fs.Bool("rebuild-index", false, "rewrite the derived index from the scan")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return usageErrorf("usage: anas snapshot verify [<id>] [--rebuild-index] [-w <workspace>] [--json]")
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	all, err := listSnapshots(workspace)
	if err != nil {
		return failuref("scan_failed", "%s", err.Error())
	}
	if len(positional) == 1 {
		if err := validateSnapshotID(positional[0]); err != nil {
			return err
		}
		filtered := []snapshotMeta{}
		for _, meta := range all {
			if meta.ID == positional[0] {
				filtered = append(filtered, meta)
			}
		}
		if len(filtered) == 0 {
			return preconditionErrorf("snapshot_missing", "no snapshot %s in %s", positional[0], snapshotsDir(workspace))
		}
		all = filtered
	}
	problems := []snapshotProblem{}
	for _, meta := range all {
		problems = append(problems, verifySnapshot(workspace, meta)...)
	}
	// The index is derived, so a mismatch is a repairable inconsistency rather
	// than damage. It is only meaningful over a full scan.
	rebuilt := false
	if len(positional) == 0 {
		index, err := loadSnapshotIndex(workspace)
		if err != nil || !indexMatchesScan(index, all) {
			if *rebuild {
				if err := rebuildSnapshotIndex(workspace); err != nil {
					return failuref("write_failed", "%s", err.Error())
				}
				rebuilt = true
			} else {
				problems = append(problems, snapshotProblem{
					Code:    "index_stale",
					Message: "state/snapshots.yml does not match the snapshots on disk; rerun with --rebuild-index",
				})
			}
		}
	}
	if jsonMode {
		if err := emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": len(problems) == 0,
			"checked": len(all), "index_rebuilt": rebuilt, "problems": problems,
		}); err != nil {
			return err
		}
	} else {
		fmt.Printf("checked %d snapshot(s)\n", len(all))
		if rebuilt {
			fmt.Println("index rebuilt")
		}
		for _, problem := range problems {
			fmt.Printf("%s\t%s\t%s\n", problem.ID, problem.Code, problem.Message)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	// verify is written to be run from cron, so finding damage has to be a
	// non-zero exit as well as ok: false in the body. A cron job that only
	// looks at the exit status is the normal case, and reporting success while
	// the body says otherwise is how a missing subvolume stays unnoticed until
	// the restore that needed it.
	return &CLIError{
		Code: "verify_failed", Message: fmt.Sprintf("%d problem(s)", len(problems)),
		Exit: exitFailure, Reported: jsonMode,
	}
}

// ---------------------------------------------------------------- path

// runSnapshotPath prints the read-only data directory. A Btrfs snapshot is a
// readable directory in its own right, so recovering one deleted file should
// not require rewinding the entire workspace to get at it.
func runSnapshotPath(args []string, jsonMode bool) error {
	f := newSnapshotFlags("path")
	positional, err := f.parse(args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas snapshot path <id> [-w <workspace>] [--json]")
	}
	workspace, err := resolveSnapshotWorkspace(*f.workspace)
	if err != nil {
		return err
	}
	meta, err := loadSnapshot(workspace, positional[0])
	if err != nil {
		return err
	}
	path := snapshotDataPath(snapshotRoot(workspace, meta.ID))
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if jsonMode {
		return emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": true, "id": meta.ID, "path": path,
		})
	}
	fmt.Println(path)
	return nil
}

// ---------------------------------------------------------------- helpers

func resolveSnapshotWorkspace(explicit string) (string, error) {
	workspace, err := resolveWorkspace(explicit)
	if err != nil {
		return "", usageErrorf("%s", err.Error())
	}
	return workspace, nil
}

// currentConfigDigest returns the digest of the config on disk, or "" when
// there is none to compare against.
func currentConfigDigest(workspace string) string {
	digest, err := fileDigest(workspaceConfigPath(workspace))
	if err != nil {
		return ""
	}
	return digest
}
