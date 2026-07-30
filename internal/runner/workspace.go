package runner

// A workspace is everything one deployment owns: the user's configuration, the
// business data, the snapshots, and the runtime state. It exists so that a
// backup is a single directory rather than a set of paths the user has to
// remember to collect. Nothing outside the workspace is required to restore it.
//
//	<workspace>/
//	  config.yml          user-maintained desired state
//	  config.lock.yml     resolution lock, derived from the config path
//	  data/               service data; a Btrfs subvolume when possible
//	  snapshots/          point-in-time copies; a plain directory
//	  .anas/              runtime state, 0700
//
// data/ has no configurable location. A deployment whose data lives outside the
// workspace cannot be backed up by copying one directory, which is the whole
// point of the layout; users who need the data on a larger disk put the entire
// workspace there.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// workspaceStateDir holds everything the runner writes for itself. It is
	// deliberately a dotted name: users never edit anything inside it.
	workspaceStateDir = ".anas"
	// workspaceDataDir is where every cask's persistent data lives.
	workspaceDataDir = "data"
	// workspaceSnapshotDir is a plain directory, not a subvolume. Only the
	// *source* of a Btrfs snapshot has to be a subvolume; the destination just
	// needs a parent on the same filesystem.
	workspaceSnapshotDir = "snapshots"
	// workspaceConfigName is the config a workspace uses when none is given.
	workspaceConfigName = "config.yml"
)

// resolveWorkspace locates the workspace to operate on.
//
// The current directory only counts when it already contains a workspace. An
// implicit fallback that accepted any directory would let a mistyped `cd`
// silently build a second parallel deployment somewhere the user never meant,
// which is exactly the failure the explicit forms exist to prevent.
func resolveWorkspace(explicit string) (string, error) {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		return checkWorkspace(trimmed, "-w")
	}
	if env := strings.TrimSpace(os.Getenv("ANAS_WORKSPACE")); env != "" {
		return checkWorkspace(env, "ANAS_WORKSPACE")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine current directory: %w", err)
	}
	if exists(filepath.Join(cwd, workspaceStateDir)) {
		return filepath.Abs(cwd)
	}
	return "", fmt.Errorf("no workspace found in %s; pass -w <workspace>, set ANAS_WORKSPACE, or run `anas init`", cwd)
}

// resolveWorkspaceStrict is for the operations that replace data. An
// environment variable set once in a shell profile is the easiest thing to
// leave stale and pointed at the wrong deployment, so destructive commands
// accept only the flag.
func resolveWorkspaceStrict(explicit string, command string) (string, error) {
	if strings.TrimSpace(explicit) == "" {
		return "", fmt.Errorf("anas %s requires an explicit -w <workspace>; it will not infer one from ANAS_WORKSPACE or the current directory", command)
	}
	return checkWorkspace(explicit, "-w")
}

func checkWorkspace(path, source string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("workspace %s (from %s) is not accessible: %w", abs, source, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %s (from %s) is not a directory", abs, source)
	}
	if !exists(filepath.Join(abs, workspaceStateDir)) {
		return "", fmt.Errorf("%s (from %s) is not a workspace: no %s/ directory; run `anas init %s`", abs, source, workspaceStateDir, abs)
	}
	return abs, nil
}

func stateDir(workspace string) string     { return filepath.Join(workspace, workspaceStateDir) }
func dataDir(workspace string) string      { return filepath.Join(workspace, workspaceDataDir) }
func snapshotsDir(workspace string) string { return filepath.Join(workspace, workspaceSnapshotDir) }

func workspaceConfigPath(workspace string) string {
	return filepath.Join(workspace, workspaceConfigName)
}

// configPathFor lets every command default to the workspace's own config, so
// `-c` is only needed when operating on a config that lives elsewhere.
func configPathFor(workspace, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	return workspaceConfigPath(workspace)
}

// applyWorkspaceEnv injects the values that come from the workspace layout
// rather than from the config file. DATA_PATH is the only one today: casks
// reference it as ${DATA_PATH} in their compose files, but its value is fixed
// by the layout, so the config has no say in it.
func (a *app) applyWorkspaceEnv() {
	if a.workspace == "" {
		return
	}
	if a.env == nil {
		a.env = map[string]string{}
	}
	a.env["DATA_PATH"] = dataDir(a.workspace)
	if a.envOwner != nil {
		a.envOwner["DATA_PATH"] = ""
	}
}

// announceWorkspace prints the resolved workspace before anything is changed.
// Resolution has three sources and the wrong one is invisible until after the
// damage, so every mutating command states which deployment it is about to
// touch. It goes to stderr to keep --json stdout parseable.
func announceWorkspace(workspace string) {
	fmt.Fprintf(os.Stderr, "workspace: %s\n", workspace)
}
