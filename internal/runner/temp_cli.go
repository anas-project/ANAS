package runner

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

func runTemp(args []string, jsonMode bool) error {
	if len(args) == 0 || (args[0] != "status" && args[0] != "gc") {
		return usageErrorf("usage: anas temp status|gc -w <workspace> [--dry-run] [--json]")
	}
	action := args[0]
	flags := flag.NewFlagSet("temp "+action, flag.ContinueOnError)
	workspaceFlag := flags.String("w", "", "workspace path (required for GC)")
	flags.StringVar(workspaceFlag, "workspace", "", "workspace path")
	dryRun := flags.Bool("dry-run", false, "show safe reclamation candidates without deleting")
	flags.Bool("json", jsonMode, "output JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return usageErrorf("%s", err)
	}
	if flags.NArg() != 0 {
		return usageErrorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if action == "status" && *dryRun {
		return usageErrorf("--dry-run belongs to temp gc")
	}
	var workspace string
	var err error
	if action == "gc" {
		workspace, err = resolveWorkspaceStrict(*workspaceFlag, "temp gc")
	} else {
		workspace, err = resolveWorkspace(*workspaceFlag)
	}
	if err != nil {
		return usageErrorf("%s", err)
	}
	base := stateDir(workspace)
	unlock, err := acquireRuntimeLockContext(context.Background(), base)
	if err != nil {
		return preconditionErrorf("temp_lock_failed", "%s", err)
	}
	defer unlock()
	cfg, err := config.Load(workspaceConfigPath(workspace))
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err)
	}
	cli, err := detectComposeForExecution(context.Background(), false)
	if err != nil {
		return preconditionErrorf("compose_missing", "%s", err)
	}
	a := &app{workspace: workspace, base: base, cfg: cfg, compose: cli}
	if action == "gc" && !*dryRun && exists(temporaryStatePath(base)) {
		registry, err := a.loadTemporaryRegistry(false)
		if err != nil {
			return failuref("temp_storage_failed", "%s", err)
		}
		if transition := registry.Transition; transition != nil && transition.Phase != "committed" && transition.Phase != "cleanup_deferred" && transition.Phase != "complete" && transition.Phase != "restored" {
			return preconditionErrorf("temp_recovery_required", "run apply or start to reconcile the interrupted temporary path transition before GC")
		}
		if err := a.reconcileTemporaryStorage(); err != nil {
			return failuref("temp_reconcile_failed", "%s", err)
		}
	}
	var status TemporaryStorageStatus
	if action == "gc" {
		status, err = a.gcTemporaryStorage(*dryRun)
	} else {
		status, err = a.temporaryStorageStatus()
	}
	if err != nil {
		return failuref("temp_storage_failed", "%s", err)
	}
	if jsonMode {
		body, err := json.Marshal(status)
		if err != nil {
			return err
		}
		var fields map[string]any
		if err := json.Unmarshal(body, &fields); err != nil {
			return err
		}
		return emitOK(fields)
	}
	fmt.Printf("workspace: %s\ntemporary root: %s\napplied root: %s\n", workspace, status.DesiredRoot, status.AppliedRoot)
	for _, directory := range status.Directories {
		state := directory.State
		if directory.Reclaimable {
			state += "; reclaimable"
		}
		fmt.Printf("%s/%s/%s %s %d bytes [%s]\n", directory.Module, directory.Service, directory.Name, directory.Path, directory.BytesUsed, state)
		fmt.Printf("  capacity: %s\n", temporaryCapacityText(directory))
		if len(directory.Blockers) != 0 {
			fmt.Printf("  blocked: %s\n", strings.Join(directory.Blockers, ", "))
		}
	}
	for _, issue := range status.Issues {
		fmt.Printf("issue %s: %s\n", issue.Code, issue.Message)
	}
	return nil
}

func temporaryCapacityText(directory TemporaryDirectoryStatus) string {
	bytes := "unknown"
	if directory.FreeBytes != nil {
		bytes = fmt.Sprintf("%d bytes available", *directory.FreeBytes)
	}
	inodes := "not applicable (dynamic inode allocation)"
	if directory.FreeInodes != nil {
		inodes = fmt.Sprintf("%d inodes available", *directory.FreeInodes)
	} else if directory.FreeBytes == nil || directory.Filesystem.Type != "btrfs" {
		inodes = "inodes unknown"
	}
	return bytes + "; " + inodes
}
