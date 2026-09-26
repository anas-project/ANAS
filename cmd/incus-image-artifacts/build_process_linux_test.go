//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
)

// Only the test executable has this dispatch. It is not an image builder and
// never installs software, mounts filesystems or reaches a network.
func init() {
	if len(os.Args) > 2 && os.Args[0] == "distrobuilder" {
		if filepath.Base(os.Args[2]) == "chroot-environment.yml" {
			// A test-only child: exercise the actual fixed builder environment
			// after a real chroot without running a package manager, contacting a
			// service or changing the test parent's filesystem view.
			if len(os.Args) < 4 || os.Getenv("HOME") != "/root" || os.Getenv("TMPDIR") != "/tmp" ||
				os.Getenv("HTTP_PROXY") != "" || syscall.Chroot(os.Args[3]) != nil || os.Chdir("/") != nil {
				os.Exit(8)
			}
			file, err := os.CreateTemp("", "anas-maintainer-")
			if err != nil {
				os.Exit(9)
			}
			info, err := file.Stat()
			if err != nil || info.Mode().Perm() != 0600 || file.Close() != nil || os.Remove(file.Name()) != nil {
				os.Exit(10)
			}
			os.Exit(0)
		}
		if filepath.Base(os.Args[2]) == "unsigned-source.yml" {
			fmt.Fprintln(os.Stderr, "W: Cannot check Release signature; keyring file not available /private-keyring-path")
			fmt.Fprintln(os.Stdout, `level=info msg="Packing image"`)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, `level=info msg="Managing packages" private-token=do-not-export`)
		fmt.Fprint(os.Stdout, "unterminated-private-output")
		os.Exit(7)
	}
}

func TestDistrobuilderChrootCanCreatePackageTemporaryFiles(t *testing.T) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Skip("requires root for a child-only chroot with an empty temporary rootfs")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	dir := t.TempDir()
	root := filepath.Join(dir, "rootfs")
	for _, name := range []string{"tmp", "root"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(dir, "host-only-temp"))
	t.Setenv("HTTP_PROXY", "http://caller-proxy.invalid")
	target := computeimage.Target{Architecture: runtime.GOARCH, Interface: "incus_container"}
	program := distroProgram{file: image, target: target}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := program.Build(ctx, computeimage.ArtifactBuildRequest{Target: target, RecipeFile: filepath.Join(dir, "chroot-environment.yml"),
		OutputDirectory: root, SourcesDirectory: dir, CacheDirectory: filepath.Join(dir, "host-only-cache")}); err != nil {
		t.Fatal("the fixed child environment cannot create its private temporary file after chroot", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatal("test child left a temporary file behind", err)
	}
}

func TestDistrobuilderRejectsUnsignedBootstrapEvenWhenChildExitsZero(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	target := computeimage.Target{Architecture: runtime.GOARCH, Interface: "incus_container"}
	program := distroProgram{file: image, target: target}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = program.Build(ctx, computeimage.ArtifactBuildRequest{Target: target, RecipeFile: filepath.Join(dir, "unsigned-source.yml"),
		OutputDirectory: dir, SourcesDirectory: dir, CacheDirectory: dir})
	if !errors.Is(err, computeimage.ErrArtifactBuildIncomplete) || !strings.Contains(err.Error(), "signatures were not verified") || strings.Contains(err.Error(), "private") {
		t.Fatal("successful process exit accepted an unverified base system", err)
	}
}

func TestDistrobuilderProcessReportsOnlyStage(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	target := computeimage.Target{Architecture: runtime.GOARCH, Interface: "incus_container"}
	program := distroProgram{file: image, target: target}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = program.Build(ctx, computeimage.ArtifactBuildRequest{Target: target, RecipeFile: filepath.Join(dir, "recipe.yml"), OutputDirectory: dir, SourcesDirectory: dir, CacheDirectory: dir})
	if !errors.Is(err, computeimage.ErrArtifactBuildIncomplete) || !strings.HasSuffix(err.Error(), "stage: packages") || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), dir) {
		t.Fatal(err)
	}
}
