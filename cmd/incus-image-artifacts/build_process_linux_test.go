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
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
)

// Only the test executable has this dispatch. It is not an image builder and
// never installs software, mounts filesystems or reaches a network.
func init() {
	if len(os.Args) > 2 && os.Args[0] == "distrobuilder" {
		fmt.Fprintln(os.Stderr, `level=info msg="Managing packages" private-token=do-not-export`)
		fmt.Fprint(os.Stdout, "unterminated-private-output")
		os.Exit(7)
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
