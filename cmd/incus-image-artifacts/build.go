package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
)

type preparedDistrobuilder interface {
	io.Closer
	Build(context.Context, computeimage.ArtifactBuildRequest) error
}

// The command shape is fixed. No caller-supplied flags, import destination,
// aliases, shell or environment additions reach the release builder.
func distrobuilderArgs(ctx context.Context, request computeimage.ArtifactBuildRequest) ([]string, error) {
	architecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[request.Target.Architecture]
	if architecture == "" || (request.Target.Interface != "incus_container" && request.Target.Interface != "incus_vm") {
		return nil, computeimage.ErrArtifactInvalid
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 {
		return nil, computeimage.ErrArtifactInvalid
	}
	seconds := int64(time.Until(deadline)/time.Second) + 1
	args := []string{"build-incus", request.RecipeFile, request.OutputDirectory,
		"--type=split", "--compression=xz", "--cleanup=true",
		"--cache-dir=" + request.CacheDirectory, "--sources-dir=" + request.SourcesDirectory,
		"--options=image.architecture=" + architecture, fmt.Sprintf("--timeout=%d", seconds),
	}
	if request.Target.Interface == "incus_vm" {
		args = append(args, "--vm")
	}
	return args, nil
}
