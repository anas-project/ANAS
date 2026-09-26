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

func distrobuilderEnvironment() []string {
	// Maintainer scripts inherit this environment AFTER distrobuilder chroots.
	// The host's private archive/cache paths do not exist in that filesystem:
	// exporting them as TMPDIR broke Debian's apparmor postinst at mktemp.
	// Keep HOME/TMPDIR meaningful on either side of chroot, while explicit
	// --cache-dir/--sources-dir/output arguments retain private build storage.
	// No caller environment, extra host mount or package-script bypass is used.
	return []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "HOME=/root", "TMPDIR=/tmp"}
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
