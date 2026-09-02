package runner

import (
	"context"
	"os/exec"

	"github.com/anas-project/ANAS/internal/processgroup"
)

func externalCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, name, args...)
	processgroup.Configure(command)
	return command
}
