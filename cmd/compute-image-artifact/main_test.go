//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func TestInspectAndVerifyLocalSplitArtifact(t *testing.T) {
	root := t.TempDir()
	metadata := filepath.Join(root, "incus.tar.xz")
	rootfs := filepath.Join(root, "disk.qcow2")
	writeLocalArtifact(t, metadata, []byte("metadata fixture, not a real archive"))
	writeLocalArtifact(t, rootfs, []byte("rootfs fixture, not a real disk image"))
	var output, diagnostics bytes.Buffer
	args := []string{"--metadata", metadata, "--rootfs", rootfs, "--name", "test-guest", "--revision", "r1", "--architecture", "amd64", "--interface", "incus_vm", "--recipe-digest", strings.Repeat("a", 64)}
	if err := run(context.Background(), args, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	release, err := computeimage.DecodeArtifactRelease(output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "artifact.json")
	writeLocalArtifact(t, manifest, output.Bytes())
	verify := []string{"--metadata", metadata, "--rootfs", rootfs, "--verify", manifest, "--architecture", "amd64", "--interface", "incus_vm"}
	output.Reset()
	if err := run(context.Background(), verify, &output, &diagnostics); err == nil {
		t.Fatal("verification trusted the descriptor's own fingerprint")
	}
	verify = append(verify, "--expected-fingerprint", release.Entry.Fingerprint)
	if err := run(context.Background(), verify, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "boot were not tested") {
		t.Fatal("byte verification must not imply boot acceptance")
	}
	writeLocalArtifact(t, rootfs, []byte("different bytes"))
	output.Reset()
	if err := run(context.Background(), verify, &output, &diagnostics); err == nil || output.Len() != 0 {
		t.Fatal("corrupted artifact accepted or produced success output")
	}
}

func TestArtifactInputRejectsNonRegularAndOversizeFiles(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "data")
	writeLocalArtifact(t, file, []byte("12345"))
	if opened, _, err := openArtifactInput(file, 4); err == nil {
		_ = opened.Close()
		t.Fatal("oversize input accepted")
	}
	if opened, _, err := openArtifactInput(root, 10); err == nil {
		_ = opened.Close()
		t.Fatal("directory accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if opened, _, err := openArtifactInput(link, 10); err == nil {
		_ = opened.Close()
		t.Fatal("symbolic link accepted")
	}
}

func TestInspectRefusesToOverwriteExpectedIdentity(t *testing.T) {
	root := t.TempDir()
	metadata, rootfs := filepath.Join(root, "metadata"), filepath.Join(root, "rootfs")
	writeLocalArtifact(t, metadata, []byte("metadata"))
	writeLocalArtifact(t, rootfs, []byte("rootfs"))
	var output, diagnostics bytes.Buffer
	args := []string{"--metadata", metadata, "--rootfs", rootfs, "--name", "test-guest", "--revision", "r1", "--architecture", "arm64", "--interface", "incus_container", "--recipe-digest", strings.Repeat("a", 64), "--expected-fingerprint", strings.Repeat("b", 64)}
	if err := run(context.Background(), args, &output, &diagnostics); err == nil || output.Len() != 0 {
		t.Fatal("existing fingerprint was replaced")
	}
}

func TestArtifactInputDetectsVisibleChanges(t *testing.T) {
	for _, change := range []string{"contents", "permissions"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "metadata")
			writeLocalArtifact(t, path, []byte("original"))
			file, before, err := openArtifactInput(path, 1024)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := artifactInputUnchanged(file, before); err != nil {
				t.Fatalf("unchanged file rejected: %v", err)
			}
			if change == "contents" {
				writeLocalArtifact(t, path, []byte("changed to a different length"))
			} else if err := os.Chmod(path, 0400); err != nil {
				t.Fatal(err)
			}
			if err := artifactInputUnchanged(file, before); err == nil {
				t.Fatal("visible file change was not rejected")
			}
		})
	}
}

func writeLocalArtifact(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}
