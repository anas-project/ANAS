//go:build !unix

package application

import (
	"os/exec"

	"github.com/anas-project/ANAS/internal/processgroup"
)

func configureModuleCommandProcess(command *exec.Cmd) {
	processgroup.Configure(command)
}

func terminateModuleCommandProcess(command *exec.Cmd) error {
	return processgroup.Terminate(command)
}
