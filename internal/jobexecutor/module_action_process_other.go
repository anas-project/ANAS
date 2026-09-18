//go:build !linux

package jobexecutor

import (
	"context"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

type moduleActionProgram struct{}

func (*moduleActionProgram) Close() error { return nil }

func prepareModuleActionProgram(ModuleActionDefinition) (*moduleActionProgram, error) {
	return nil, ErrModuleActionUnavailable
}

func runModuleActionProcess(context.Context, ModuleActionDefinition, *moduleActionProgram, actionabi.Request, ActionRecorderOptions, <-chan struct{}) (consolejobs.Job, error) {
	return consolejobs.Job{}, ErrModuleActionUnavailable
}
