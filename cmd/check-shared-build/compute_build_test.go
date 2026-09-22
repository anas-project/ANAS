package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Compile the actual reduced build input, not the whole checkout. Whole-tree
// builds can hide a transitive dependency missing from all image manifests.
// This is an offline Go build, not a Docker image or running guest acceptance.
func TestComputeConsumersBuildFromDeclaredSharedPathsOffline(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	data, err := os.ReadFile(filepath.Join(root, ".github/images.json"))
	if err != nil {
		t.Fatal(err)
	}
	var images []imageEntry
	if err := json.Unmarshal(data, &images); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, image := range images {
		if !covers(image.Shared, "internal/computeclient") {
			continue
		}
		count++
		t.Run(image.Module, func(t *testing.T) {
			tree := t.TempDir()
			for _, path := range append(append([]string(nil), image.Shared...), image.Context) {
				source := filepath.Join(root, path)
				err := filepath.WalkDir(source, func(current string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					relative, err := filepath.Rel(root, current)
					if err != nil {
						return err
					}
					target := filepath.Join(tree, relative)
					if entry.IsDir() {
						return os.MkdirAll(target, 0700)
					}
					if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
						return err
					}
					body, err := os.ReadFile(current)
					if err != nil {
						return err
					}
					return os.WriteFile(target, body, 0600)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, arch := range []string{"amd64", "arm64"} {
				t.Run(arch, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", filepath.Join(tree, "consumer-"+arch), "./"+image.Context)
					command.Dir = tree
					command.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("declared build inputs do not compile offline: %v\n%s", err, output)
					}
				})
			}
		})
	}
	if count != 3 {
		t.Fatalf("expected the three compute consumers, found %d", count)
	}
}
