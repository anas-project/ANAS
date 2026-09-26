package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func TestDistrobuilderArgumentsKeepReleaseBuildSeparateFromImport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	for _, arch := range []string{"amd64", "arm64"} {
		for _, iface := range []string{"incus_container", "incus_vm"} {
			r := computeimage.ArtifactBuildRequest{Target: computeimage.Target{Architecture: arch, Interface: iface}, RecipeFile: "/private/recipe.yml", OutputDirectory: "/private/output", CacheDirectory: "/private/cache", SourcesDirectory: "/private/sources"}
			args, err := distrobuilderArgs(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if args[0] != "build-incus" || args[1] != r.RecipeFile || args[2] != r.OutputDirectory || !slices.Contains(args, "--type=split") || !slices.Contains(args, "--compression=xz") || slices.Contains(args, "--vm") != (iface == "incus_vm") {
				t.Fatalf("wrong build command: %v", args)
			}
			for _, arg := range args {
				if strings.Contains(arg, "import") || strings.Contains(arg, "alias") {
					t.Fatal("release build imported into a deployment")
				}
			}
		}
	}
	if _, err := distrobuilderArgs(context.Background(), computeimage.ArtifactBuildRequest{}); err == nil {
		t.Fatal("accepted unbounded/invalid build")
	}
}

func TestDistrobuilderEnvironmentDoesNotExportHostPathsIntoChroot(t *testing.T) {
	t.Setenv("TMPDIR", "/private/operator-temporary")
	t.Setenv("HOME", "/private/operator-home")
	t.Setenv("HTTP_PROXY", "http://private-proxy.invalid")
	r := computeimage.ArtifactBuildRequest{Target: computeimage.Target{Architecture: "amd64", Interface: "incus_container"},
		RecipeFile: "/private/archive/build/recipe.yml", OutputDirectory: "/private/archive/build/output",
		CacheDirectory: "/private/archive/build/cache", SourcesDirectory: "/private/archive/build/sources"}
	want := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "HOME=/root", "TMPDIR=/tmp"}
	if got := distrobuilderEnvironment(); !slices.Equal(got, want) {
		t.Fatal("builder environment leaks host-only paths or caller settings into package maintainer scripts")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	args, err := distrobuilderArgs(ctx, r)
	if err != nil || !slices.Contains(args, "--cache-dir="+r.CacheDirectory) ||
		!slices.Contains(args, "--sources-dir="+r.SourcesDirectory) || args[2] != r.OutputDirectory {
		t.Fatal("chroot-compatible environment lost explicit private build directories", err)
	}
}
