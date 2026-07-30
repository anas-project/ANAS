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

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	shellInit := fs.String("shell-init", "", `write ANAS_WORKSPACE into the shell profile ("write" or "remove")`)
	yes := fs.Bool("y", false, "accept prompts")
	fs.BoolVar(yes, "yes", false, "accept prompts")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("usage: anas init [PATH] [--shell-init] [-y]")
	}
	target := "."
	if len(positional) == 1 {
		target = positional[0]
	}
	workspace, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	if *shellInit == "remove" {
		return removeShellInit()
	}

	if err := createWorkspace(workspace, *yes); err != nil {
		return err
	}
	fmt.Println(workspace)

	if *shellInit != "" {
		return writeShellInit(workspace, *yes)
	}
	printShellInitHint(workspace)
	return nil
}

func createWorkspace(workspace string, yes bool) error {
	if exists(filepath.Join(workspace, workspaceStateDir)) {
		return fmt.Errorf("%s is already a workspace", workspace)
	}
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return err
	}

	btrfs, err := filesystemIsBtrfs(workspace)
	if err != nil {
		return fmt.Errorf("determine filesystem of %s: %w", workspace, err)
	}
	if !btrfs {
		// Saying "snapshots may not work" would leave the user to discover the
		// real cost on the day an upgrade goes wrong, so spell out what is lost.
		fmt.Fprintf(os.Stderr, `warning: %s is not on Btrfs.
  Snapshots and data restore will be unavailable.
  Cask upgrades will not create an automatic pre-upgrade data snapshot.
  Backups still work, but only in whole-directory copy mode.
`, workspace)
		ok, err := confirm("Continue?", yes)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
	}

	if err := ensureRuntimeLayout(stateDir(workspace)); err != nil {
		return err
	}
	if err := createDataDir(workspace, btrfs); err != nil {
		return err
	}
	if err := writeConfigSkeleton(workspace); err != nil {
		return err
	}
	return nil
}

// createDataDir refuses a symlink or a mount point rather than working around
// them. A symlink makes tar and rsync skip the data unless told otherwise, so
// the backup silently comes out empty; a mount point makes the rename that a
// data restore performs fail with EBUSY. Both break a guarantee quietly, which
// is worse than refusing up front.
func createDataDir(workspace string, btrfs bool) error {
	data := dataDir(workspace)
	if info, err := os.Lstat(data); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; data must be a real directory inside the workspace or backups will not include it", data)
		}
		mount, err := isMountPoint(data)
		if err != nil {
			return err
		}
		if mount {
			return fmt.Errorf("%s is a mount point; data restore renames this directory and cannot do so across a mount", data)
		}
		return nil
	}
	if btrfs {
		// Only the *source* of a snapshot has to be a subvolume, which is why
		// data/ is one and snapshots/ is an ordinary directory.
		if err := runBtrfs("subvolume", "create", data); err != nil {
			return fmt.Errorf("create Btrfs subvolume %s: %w", data, err)
		}
		return nil
	}
	return os.MkdirAll(data, 0755)
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
# Service data always lives at <workspace>/data; there is no data_path setting.
# Back up this whole directory and the deployment is fully recoverable.

modules:
  - samba_dc

global:
  domain: example.test
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
	info, err := os.Stdin.Stat()
	if err != nil {
		return false, err
	}
	// A non-interactive caller must fail immediately rather than block forever
	// waiting on input nobody is there to give.
	if info.Mode()&os.ModeCharDevice == 0 {
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
func writeShellInit(workspace string, yes bool) error {
	path, line, err := shellProfile()
	if err != nil {
		return fmt.Errorf("%w; add ANAS_WORKSPACE=%s to your profile manually", err, workspace)
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	block := shellInitBeginMarker + "\n" + line(workspace) + "\n" + shellInitEndMarker
	current := string(existing)

	if old, ok := extractShellInitBlock(current); ok {
		if old == block {
			fmt.Fprintf(os.Stderr, "%s already points at this workspace\n", path)
			return nil
		}
		fmt.Fprintf(os.Stderr, "%s currently contains:\n%s\n\nreplacing with:\n%s\n", path, old, block)
		ok, err := confirm("Replace?", yes)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("aborted")
		}
		current = replaceShellInitBlock(current, block)
	} else {
		if current != "" && !strings.HasSuffix(current, "\n") {
			current += "\n"
		}
		current += "\n" + block + "\n"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(current), 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s; new shells pick it up, or run: source %s\n", path, path)
	return nil
}

func removeShellInit() error {
	path, _, err := shellProfile()
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	current := string(existing)
	if _, ok := extractShellInitBlock(current); !ok {
		fmt.Fprintf(os.Stderr, "%s has no anas block\n", path)
		return nil
	}
	if err := os.WriteFile(path, []byte(replaceShellInitBlock(current, "")), 0644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "removed the anas block from %s\n", path)
	return nil
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
