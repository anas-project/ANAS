//go:build !unix

package application

import (
	"os"
	"os/exec"
	"time"
)

func configureModuleCommandProcess(command *exec.Cmd) {
	command.WaitDelay = 5 * time.Second
}

func terminateModuleCommandProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return os.ErrProcessDone
	}
	return command.Process.Kill()
}
