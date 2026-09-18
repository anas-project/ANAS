//go:build linux

package jobexecutor

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func openModuleActionProc() (*os.File, error) {
	proc, err := os.OpenFile("/proc", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrModuleActionContainment
	}
	if err := checkModuleActionProc(proc); err != nil {
		_ = proc.Close()
		return nil, err
	}
	return proc, nil
}

// Pin the procfs view before exec, require the current PID namespace, and reject
// hidepid views rather than interpreting invisible group members as absent.
// This is not isolation from an administrator changing the mount namespace.
func checkModuleActionProc(proc *os.File) error {
	if proc == nil {
		return ErrModuleActionContainment
	}
	var fs unix.Statfs_t
	if unix.Fstatfs(int(proc.Fd()), &fs) != nil || uint64(fs.Type) != uint64(unix.PROC_SUPER_MAGIC) {
		return ErrModuleActionContainment
	}
	pinned, err := proc.Stat()
	current, currentErr := os.Stat("/proc")
	if err != nil || currentErr != nil || !os.SameFile(pinned, current) {
		return ErrModuleActionContainment
	}
	var self [32]byte
	n, err := unix.Readlinkat(int(proc.Fd()), "self", self[:])
	if err != nil || string(self[:n]) != strconv.Itoa(os.Getpid()) {
		return ErrModuleActionContainment
	}
	body, err := readModuleActionProc(proc, "self/mountinfo", 2<<20)
	if err != nil || !moduleActionProcMountVisible(body) {
		return ErrModuleActionContainment
	}
	return nil
}

func moduleActionProcMountVisible(body []byte) bool {
	found := false
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != "/proc" {
			continue
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if found || separator < 0 || len(fields) != separator+4 || fields[3] != "/" || fields[separator+1] != "proc" {
			return false
		}
		for _, option := range strings.Split(fields[5]+","+fields[separator+3], ",") {
			if strings.HasPrefix(option, "hidepid=") && option != "hidepid=0" {
				return false
			}
		}
		found = true
	}
	return found
}

func readModuleActionProc(proc *os.File, name string, limit int64) ([]byte, error) {
	fd, err := unix.Openat(int(proc.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "action-process-state")
	defer file.Close()
	var fs unix.Statfs_t
	if unix.Fstatfs(fd, &fs) != nil || uint64(fs.Type) != uint64(unix.PROC_SUPER_MAGIC) {
		return nil, ErrModuleActionContainment
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, ErrModuleActionContainment
	}
	return body, nil
}

// moduleActionGroupAlive is a bounded drain check, not a sandbox. The leader
// must be exited but UNREAPED so its PID/PGID cannot be reused while inspecting
// and signalling the group. Trusted executors must not daemonize, change groups,
// elevate privilege or move children out of the supervisor's PID namespace.
func moduleActionGroupAlive(ctx context.Context, proc *os.File, leader int) (bool, error) {
	if ctx == nil || ctx.Err() != nil || leader <= 1 || checkModuleActionProc(proc) != nil {
		return false, ErrModuleActionContainment
	}
	read := func(pid int) (byte, int, error) {
		body, err := readModuleActionProc(proc, strconv.Itoa(pid)+"/stat", 4096)
		if err != nil {
			return 0, 0, err
		}
		return parseModuleActionProcessState(body, pid)
	}
	leaderExited := func() bool {
		state, group, err := read(leader)
		return err == nil && group == leader && (state == 'Z' || state == 'X' || state == 'x')
	}
	if !leaderExited() {
		return false, ErrModuleActionContainment
	}
	// A new open file description is essential: each scan starts at offset 0,
	// without sharing the directory cursor of a previous or concurrent scan.
	fd, err := unix.Openat(int(proc.Fd()), ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false, ErrModuleActionContainment
	}
	directory := os.NewFile(uintptr(fd), "action-process-inventory")
	defer directory.Close()
	count := 0
	for {
		if ctx.Err() != nil {
			return false, ErrModuleActionContainment
		}
		names, err := directory.Readdirnames(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return false, ErrModuleActionContainment
		}
		count += len(names)
		if count > 131072 {
			return false, ErrModuleActionContainment
		}
		for _, name := range names {
			pid, parseErr := strconv.Atoi(name)
			if parseErr != nil || pid <= 0 || strconv.Itoa(pid) != name || pid == leader {
				continue
			}
			if ctx.Err() != nil {
				return false, ErrModuleActionContainment
			}
			state, group, readErr := read(pid)
			if errors.Is(readErr, os.ErrNotExist) || errors.Is(readErr, unix.ESRCH) {
				continue // An unrelated process may disappear during enumeration.
			}
			if readErr != nil {
				return false, ErrModuleActionContainment
			}
			if group == leader && state != 'Z' && state != 'X' && state != 'x' {
				return true, nil
			}
		}
		if errors.Is(err, io.EOF) {
			if ctx.Err() != nil || !leaderExited() || checkModuleActionProc(proc) != nil {
				return false, ErrModuleActionContainment
			}
			return false, nil
		}
	}
}

func parseModuleActionProcessState(body []byte, expectedPID int) (byte, int, error) {
	// comm may contain spaces, parentheses and newlines. Numeric fields follow
	// its LAST closing parenthesis; splitting the complete line is incorrect.
	text := string(body)
	open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if open < 2 || close <= open || close+2 >= len(text) || text[open-1] != ' ' || text[close+1] != ' ' {
		return 0, 0, ErrModuleActionContainment
	}
	pid, err := strconv.Atoi(strings.TrimSpace(text[:open]))
	fields := strings.Fields(text[close+1:])
	if err != nil || pid != expectedPID || len(fields) < 3 || len(fields[0]) != 1 || !strings.ContainsAny(fields[0], "RSDZTtXxKWPI") {
		return 0, 0, ErrModuleActionContainment
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil || group < 0 {
		return 0, 0, ErrModuleActionContainment
	}
	return fields[0][0], group, nil
}
