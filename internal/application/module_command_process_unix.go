//go:build unix

package application

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureModuleCommandProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return terminateModuleCommandProcess(command) }
	command.WaitDelay = 5 * time.Second
}

func terminateModuleCommandProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
