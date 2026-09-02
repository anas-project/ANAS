//go:build !unix

package processgroup

import (
	"os"
	"os/exec"
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
	command.Cancel = func() error { return TerminateWithGrace(command, grace) }
	command.WaitDelay = grace
}

func Terminate(command *exec.Cmd) error {
	return TerminateWithGrace(command, DefaultGrace)
}

func TerminateWithGrace(command *exec.Cmd, _ time.Duration) error {
	if command == nil || command.Process == nil {
		return os.ErrProcessDone
	}
	return command.Process.Kill()
}
