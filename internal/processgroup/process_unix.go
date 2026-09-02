//go:build unix

// Package processgroup configures context-owned external commands so
// cancellation reaches every descendant instead of only the direct child.
package processgroup

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const DefaultGrace = 2 * time.Second

func Configure(command *exec.Cmd) {
	ConfigureWithGrace(command, DefaultGrace)
}

func ConfigureWithGrace(command *exec.Cmd, grace time.Duration) {
	if command == nil {
		return
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return TerminateWithGrace(command, grace) }
	command.WaitDelay = grace + time.Second
}

func Terminate(command *exec.Cmd) error {
	return TerminateWithGrace(command, DefaultGrace)
}

func TerminateWithGrace(command *exec.Cmd, grace time.Duration) error {
	if command == nil || command.Process == nil {
		return os.ErrProcessDone
	}
	pid := command.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	if grace > 0 {
		timer := time.NewTimer(grace)
		<-timer.C
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
