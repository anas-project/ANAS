//go:build linux

package runner

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// currentPIDSpace names the PID space this process lives in: the boot, and the
// PID namespace within it. Both are required. The boot ID is shared by every
// container on a host, so on its own it would let a reader in one container
// look up a writer's PID in another and find it "gone".
func currentPIDSpace() (bootID, namespace string) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", ""
	}
	link, err := os.Readlink("/proc/self/ns/pid")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "", ""
	}
	return strings.TrimSpace(string(boot)), link
}

func inspectProcess(pid int) processProbe {
	if pid <= 0 {
		return processProbe{}
	}
	// EPERM means it exists and belongs to another user, which is still alive.
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return processProbe{status: processGone}
	} else if err != nil && !errors.Is(err, syscall.EPERM) {
		return processProbe{}
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		// hidepid, or it exited just now. Either way, alive is the safe answer.
		return processProbe{status: processRunning}
	}
	state, start, ok := parseProcStat(string(stat))
	if !ok {
		return processProbe{status: processRunning}
	}
	// A zombie has exited; only its parent has not collected it yet.
	if state == 'Z' || state == 'X' {
		return processProbe{status: processGone}
	}
	return processProbe{status: processRunning, start: start}
}
