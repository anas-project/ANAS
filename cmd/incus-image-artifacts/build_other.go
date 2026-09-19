//go:build !linux

package main

import (
	"context"
	"fmt"
	"github.com/anas-project/ANAS/internal/computeimage"
)

func prepareDistrobuilder(context.Context, string, string, computeimage.Target) (preparedDistrobuilder, error) {
	return nil, fmt.Errorf("distrobuilder requires root permission on an isolated native Linux release builder")
}

func prepareForgejoRunnerInput(context.Context, string, string, computeimage.Target) (computeimage.ArtifactBuildInput, error) {
	return computeimage.ArtifactBuildInput{}, fmt.Errorf("forgejo-runner input validation requires an isolated native Linux release builder")
}
