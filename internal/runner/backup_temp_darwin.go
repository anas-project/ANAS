package runner

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// currentPIDSpace names the boot session. Darwin has no PID namespaces, so
// every process on one boot shares one PID space.
func currentPIDSpace() (bootID, namespace string) {
	session, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", ""
	}
	return strings.TrimSpace(session), ""
}

// darwinZombie is SZOMB from <sys/proc.h>.
const darwinZombie = 5

func inspectProcess(pid int) processProbe {
	if pid <= 0 {
		return processProbe{}
	}
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return processProbe{status: processGone}
	} else if err != nil && !errors.Is(err, syscall.EPERM) {
		return processProbe{}
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(info.Proc.P_pid) != pid {
		return processProbe{status: processRunning}
	}
	if info.Proc.P_stat == darwinZombie {
		return processProbe{status: processGone}
	}
	started := info.Proc.P_starttime
	return processProbe{status: processRunning, start: fmt.Sprintf("%d.%06d", started.Sec, started.Usec)}
}
