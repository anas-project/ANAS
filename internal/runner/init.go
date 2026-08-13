package runner

// `anas init` creates a workspace. It exists because every other command now
// refuses to invent one: a directory that merely looks empty must never become
// a second parallel deployment by accident, so workspace creation is explicit
// and happens exactly here.

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const (
	shellInitBeginMarker = "# >>> anas workspace >>>"
	shellInitEndMarker   = "# <<< anas workspace <<<"
)

// shellInitAction enumerates the values --shell-init accepts. An unrecognised
// one is a usage error rather than a silent no-op, because "anas init
// --shell-init=yes" quietly doing nothing is indistinguishable from it working.
const (
	shellInitWrite  = "write"
	shellInitRemove = "remove"
)

func runInit(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	shellInit := fs.String("shell-init", "", `write ANAS_WORKSPACE into the shell profile ("write" or "remove")`)
	yes := fs.Bool("y", false, "accept prompts")
	fs.BoolVar(yes, "yes", false, "accept prompts")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	if len(positional) > 1 {
		return usageErrorf("usage: anas init [PATH] [--shell-init write|remove] [-y] [--json]")
	}
	if *shellInit != "" && *shellInit != shellInitWrite && *shellInit != shellInitRemove {
		return usageErrorf("--shell-init accepts %q or %q, got %q", shellInitWrite, shellInitRemove, *shellInit)
	}
	target := "."
	if len(positional) == 1 {
		target = positional[0]
	}
	workspace, err := filepath.Abs(target)
	if err != nil {
		return usageErrorf("resolve %s: %v", target, err)
	}

	if *shellInit == shellInitRemove {
		path, removed, err := removeShellInit()
		if err != nil {
			return err
		}
		if jsonMode {
			return emitOK(map[string]any{
				"shell_init": map[string]any{
					"action": shellInitRemove, "profile": absolutePath(path), "changed": removed,
				},
			})
		}
		return nil
	}

	created, err := createWorkspace(workspace, *yes, jsonMode)
	if err != nil {
		return err
	}

	shellInitResult := map[string]any{"action": "none", "profile": nil, "changed": false}
	if *shellInit == shellInitWrite {
		path, changed, err := writeShellInit(workspace, *yes)
		if err != nil {
			return err
		}
		shellInitResult = map[string]any{
			"action": shellInitWrite, "profile": absolutePath(path), "changed": changed,
		}
	}

	if jsonMode {
		return emitOK(map[string]any{
			"workspace":              workspace,
			"config_path":            workspaceConfigPath(workspace),
			"data_path":              dataDir(workspace),
			"user_data_path":         userDataDir(workspace),
			"snapshots_path":         snapshotsDir(workspace),
			"state_path":             stateDir(workspace),
			"btrfs":                  created.Btrfs,
			"data_is_subvolume":      created.DataIsSubvolume,
			"user_data_is_subvolume": created.UserDataIsSubvolume,
			"snapshots_usable":       created.Btrfs,
			"shell_init":             shellInitResult,
		})
	}
	fmt.Println(workspace)
	if *shellInit == "" {
		printShellInitHint(workspace)
	}
	return nil
}

// workspaceCreation records what init actually got, as opposed to what it
// asked for. Whether data/ became a subvolume decides whether snapshots and
// data restore exist for this workspace at all, so it is a result rather than
// an implementation detail.
type workspaceCreation struct {
	Btrfs           bool
	DataIsSubvolume bool
	// UserDataIsSubvolume decides whether user content can be captured at all.
	// It is reported separately because it is separately consequential: data/
	// being a subvolume makes rollback possible, userdata/ being one makes it
	// possible to back up the files without also making them part of rollback.
	UserDataIsSubvolume bool
}

func createWorkspace(workspace string, yes, jsonMode bool) (workspaceCreation, error) {
	var created workspaceCreation
	if exists(filepath.Join(workspace, workspaceStateDir)) {
		return created, preconditionErrorf("workspace_exists", "%s is already a workspace", workspace)
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return created, failuref("mkdir_failed", "create %s: %v", workspace, err)
	}

	btrfs, err := filesystemIsBtrfs(workspace)
	if err != nil {
		return created, failuref("fstype_unknown", "determine filesystem of %s: %v", workspace, err)
	}
	created.Btrfs = btrfs
	if !btrfs {
		// Saying "snapshots may not work" would leave the user to discover the
		// real cost on the day an upgrade goes wrong, so spell out what is lost.
		emitWarning(jsonMode, "workspace_not_btrfs",
			"%s is not on Btrfs: snapshots and data restore will be unavailable, module upgrades will not create an automatic pre-upgrade data snapshot, and backups can only run in whole-directory copy mode",
			workspace)
		if err := confirmDestructive("Create a workspace with no snapshot capability", yes); err != nil {
			return created, err
		}
	}

	if err := ensureRuntimeLayout(stateDir(workspace)); err != nil {
		return created, failuref("layout_failed", "%s", err.Error())
	}
	subvolume, err := createDataDir(workspace, btrfs)
	if err != nil {
		return created, err
	}
	created.DataIsSubvolume = subvolume
	userSubvolume, err := createUserDataDir(workspace, btrfs)
	if err != nil {
		return created, err
	}
	created.UserDataIsSubvolume = userSubvolume
	if err := writeConfigSkeleton(workspace); err != nil {
		return created, failuref("write_failed", "%s", err.Error())
	}
	return created, nil
}

// createDataDir refuses a symlink or a mount point rather than working around
// them. A symlink makes tar and rsync skip the data unless told otherwise, so
// the backup silently comes out empty; a mount point makes the rename that a
// data restore performs fail with EBUSY. Both break a guarantee quietly, which
// is worse than refusing up front.
func createDataDir(workspace string, btrfs bool) (bool, error) {
	data := dataDir(workspace)
	if info, err := os.Lstat(data); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, preconditionErrorf("data_is_symlink",
				"%s is a symlink; data must be a real directory inside the workspace or backups will not include it", data)
		}
		mount, err := isMountPoint(data)
		if err != nil {
			return false, failuref("stat_failed", "%s", err.Error())
		}
		if mount {
			return false, preconditionErrorf("data_is_mount_point",
				"%s is a mount point; data restore renames this directory and cannot do so across a mount", data)
		}
		// An existing data/ may already be a subvolume from an earlier init.
		return btrfsSubvolumeShow(data) == nil, nil
	}
	if btrfs {
		// Only the *source* of a snapshot has to be a subvolume, which is why
		// data/ is one and snapshots/ is an ordinary directory.
		if err := runBtrfs("subvolume", "create", data); err != nil {
			return false, failuref("subvolume_create_failed", "create Btrfs subvolume %s: %v", data, err)
		}
		return true, nil
	}
	if err := os.MkdirAll(data, 0755); err != nil {
		return false, failuref("mkdir_failed", "create %s: %v", data, err)
	}
	return false, nil
}

// createUserDataDir prepares the tree that holds what people store, which is
// governed by different rules than data/ on both counts createDataDir refuses.
//
// A mount point is allowed, and is the supported way to keep bulk files on a
// second disk: nothing renames this directory, so the EBUSY that blocks a data
// restore cannot arise. A symlink is still refused, for the same reason as
// before -- tar and rsync skip it and the backup comes out quietly empty.
//
// Its own subvolume is what lets a snapshot capture it separately from data/,
// which is what makes "roll the deployment back without rewinding anyone's
// documents" expressible at all. Failing to create one is not fatal: the tree
// still works, it simply cannot be snapshotted, and the caller reports that.
func createUserDataDir(workspace string, btrfs bool) (bool, error) {
	userData := userDataDir(workspace)
	if info, err := os.Lstat(userData); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, preconditionErrorf("userdata_is_symlink",
				"%s is a symlink; user data must be a real directory or a mount point, or backups will not include it", userData)
		}
		return btrfsSubvolumeShow(userData) == nil, nil
	}
	if btrfs {
		if err := runBtrfs("subvolume", "create", userData); err != nil {
			return false, failuref("subvolume_create_failed", "create Btrfs subvolume %s: %v", userData, err)
		}
		return true, nil
	}
	if err := os.MkdirAll(userData, 0755); err != nil {
		return false, failuref("mkdir_failed", "create %s: %v", userData, err)
	}
	return false, nil
}

func isMountPoint(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	return deviceOf(info) != deviceOf(parent), nil
}

func writeConfigSkeleton(workspace string) error {
	path := workspaceConfigPath(workspace)
	if exists(path) {
		return nil
	}
	skeleton := `# anas workspace configuration.
#
# Application state lives at <workspace>/data and the files people store live
# at <workspace>/userdata. They are separate because a rollback replaces the
# first and must never touch the second. Back up this whole directory and the
# deployment is fully recoverable.

modules:
  samba_dc: {}

global:
  # The root every hostname and the Samba realm derive from.
  base_domain: example.test
  email: admin@example.test
  timezone: UTC

env:
  CONTAINER_PREFIX: anas_
  IMAGE_PREFIX: anas_
  NETWORK_PREFIX: anas_
`
	// The config carries user secrets, so it is never world-readable.
	return os.WriteFile(path, []byte(skeleton), 0600)
}

func confirm(prompt string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	// A non-interactive caller must fail immediately rather than block forever
	// waiting on input nobody is there to give. The question is whether stdin
	// is a terminal, which is not the same as whether it is a character
	// device — /dev/null is one and answers nothing.
	if !isTerminal(os.Stdin.Fd()) {
		return false, fmt.Errorf("%s requires -y when stdin is not a terminal", prompt)
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// shellProfile reports where to write and which syntax to use. $SHELL is the
// login shell rather than the running one, but it is the only portable hint,
// and an unrecognised shell falls through to printing instead of guessing.
func shellProfile() (path string, line func(string) string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	sh := filepath.Base(os.Getenv("SHELL"))
	posix := func(ws string) string { return fmt.Sprintf("export ANAS_WORKSPACE=%q", ws) }
	switch sh {
	case "zsh":
		return filepath.Join(home, ".zshrc"), posix, nil
	case "bash":
		// A macOS Terminal tab is a login shell and reads .bash_profile; on
		// Linux the interactive default is .bashrc.
		if isDarwin() {
			return filepath.Join(home, ".bash_profile"), posix, nil
		}
		return filepath.Join(home, ".bashrc"), posix, nil
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"),
			func(ws string) string { return fmt.Sprintf("set -gx ANAS_WORKSPACE %q", ws) }, nil
	default:
		return "", nil, fmt.Errorf("unrecognised shell %q", sh)
	}
}

func printShellInitHint(workspace string) {
	_, line, err := shellProfile()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nSet ANAS_WORKSPACE=%s in your shell profile, or pass -w to each command.\n", workspace)
		return
	}
	fmt.Fprintf(os.Stderr, `
To use this workspace without -w, either cd into it, or add to your shell profile:

  %s

`, line(workspace))
}

// writeShellInit is opt-in because ANAS_WORKSPACE is machine-global: once it is
// in a profile it wins from every directory, which reintroduces exactly the
// "right command, wrong deployment" mistake that requiring an existing .anas/
// in the current directory is there to prevent. It also has no effect on cron
// or systemd units.
// writeShellInit returns the profile it touched and whether it changed it.
// "already correct" and "rewritten" are different answers and a caller that
// cannot tell them apart cannot report what it did.
func writeShellInit(workspace string, yes bool) (string, bool, error) {
	path, line, err := shellProfile()
	if err != nil {
		return "", false, preconditionErrorf("shell_unrecognised",
			"%v; add ANAS_WORKSPACE=%s to your profile manually", err, workspace)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, false, failuref("read_failed", "%s", err.Error())
	}
	block := shellInitBeginMarker + "\n" + line(workspace) + "\n" + shellInitEndMarker
	current := string(existing)

	if old, ok := extractShellInitBlock(current); ok {
		if old == block {
			fmt.Fprintf(os.Stderr, "%s already points at this workspace\n", path)
			return path, false, nil
		}
		fmt.Fprintf(os.Stderr, "%s currently contains:\n%s\n\nreplacing with:\n%s\n", path, old, block)
		if err := confirmDestructive("Replace the anas block in "+path, yes); err != nil {
			return path, false, err
		}
		current = replaceShellInitBlock(current, block)
	} else {
		if current != "" && !strings.HasSuffix(current, "\n") {
			current += "\n"
		}
		current += "\n" + block + "\n"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return path, false, failuref("mkdir_failed", "%s", err.Error())
	}
	if err := os.WriteFile(path, []byte(current), 0644); err != nil {
		return path, false, failuref("write_failed", "%s", err.Error())
	}
	fmt.Fprintf(os.Stderr, "wrote %s; new shells pick it up, or run: source %s\n", path, path)
	return path, true, nil
}

func removeShellInit() (string, bool, error) {
	path, _, err := shellProfile()
	if err != nil {
		return "", false, preconditionErrorf("shell_unrecognised", "%s", err.Error())
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, false, nil
		}
		return path, false, failuref("read_failed", "%s", err.Error())
	}
	current := string(existing)
	if _, ok := extractShellInitBlock(current); !ok {
		fmt.Fprintf(os.Stderr, "%s has no anas block\n", path)
		return path, false, nil
	}
	if err := os.WriteFile(path, []byte(replaceShellInitBlock(current, "")), 0644); err != nil {
		return path, false, failuref("write_failed", "%s", err.Error())
	}
	fmt.Fprintf(os.Stderr, "removed the anas block from %s\n", path)
	return path, true, nil
}

func extractShellInitBlock(content string) (string, bool) {
	start := strings.Index(content, shellInitBeginMarker)
	if start < 0 {
		return "", false
	}
	end := strings.Index(content[start:], shellInitEndMarker)
	if end < 0 {
		return "", false
	}
	return content[start : start+end+len(shellInitEndMarker)], true
}

func replaceShellInitBlock(content, replacement string) string {
	block, ok := extractShellInitBlock(content)
	if !ok {
		return content
	}
	out := strings.Replace(content, block, replacement, 1)
	if replacement == "" {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return out
}

func isDarwin() bool { return runtime.GOOS == "darwin" }

// deviceOf reports the device a path lives on. A directory whose device
// differs from its parent's is a mount point.
func deviceOf(info os.FileInfo) uint64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(st.Dev)
}
