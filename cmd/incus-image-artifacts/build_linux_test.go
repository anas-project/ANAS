//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
	"golang.org/x/sys/unix"
)

// Only seals and parses the test executable; never starts distrobuilder,
// elevates privileges, downloads a rootfs or creates mounts.
func TestDistrobuilderPinUsesSealedNativeELF(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path := filepath.Join(t.TempDir(), "trusted-builder")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), source)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("fixture copy: %v %v", copyErr, closeErr)
	}
	pin := hex.EncodeToString(hash.Sum(nil))
	sealed, err := openDistrobuilder(context.Background(), path, pin)
	if err != nil {
		t.Fatal(err)
	}
	defer sealed.Close()
	seals, err := unix.FcntlInt(sealed.Fd(), unix.F_GET_SEALS, 0)
	want := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if err != nil || seals&want != want {
		t.Fatalf("missing execution seals: %d %v", seals, err)
	}
	if _, err := sealed.WriteAt([]byte("changed"), 0); err == nil {
		t.Fatal("measured program remained writable")
	}
	if err := os.WriteFile(path, []byte("replaced original binary"), 0700); err != nil {
		t.Fatal(err)
	}
	magic := make([]byte, 4)
	if _, err := sealed.ReadAt(magic, 0); err != nil || string(magic) != "\x7fELF" {
		t.Fatal("pathname mutation changed sealed program")
	}
	if _, err := openDistrobuilder(context.Background(), path, pin); err == nil {
		t.Fatal("modified original accepted with old pin")
	}
}

func TestDistrobuilderPinRejectsScriptsWritableFilesAndSymlinks(t *testing.T) {
	base := t.TempDir()
	body := []byte("#!/bin/sh\n" + strings.Repeat("# never execute\n", 8))
	digest := sha256.Sum256(body)
	pin := hex.EncodeToString(digest[:])
	path := filepath.Join(base, "script")
	if err := os.WriteFile(path, body, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := openDistrobuilder(context.Background(), path, pin); err == nil {
		t.Fatal("script accepted as pinned ELF")
	}
	link := filepath.Join(base, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openDistrobuilder(context.Background(), link, pin); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := openDistrobuilder(context.Background(), path, pin); err == nil {
		t.Fatal("writable executable accepted")
	}
}

func TestForgejoRunnerInputRequiresPinnedNativeELF(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path := filepath.Join(t.TempDir(), "forgejo-runner")
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), source)
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("fixture copy: %v %v", copyErr, closeErr)
	}
	pin := hex.EncodeToString(hash.Sum(nil))
	input, err := prepareForgejoRunnerInput(context.Background(), path, pin, computeimage.Target{Architecture: runtime.GOARCH, Interface: "incus_container"})
	if err != nil {
		t.Fatal(err)
	}
	if input.Name != "forgejo-runner" || input.Path != path || input.SHA256 != pin {
		t.Fatalf("input = %+v", input)
	}
	if _, err := prepareForgejoRunnerInput(context.Background(), path, strings.Repeat("b", 64), computeimage.Target{Architecture: runtime.GOARCH, Interface: "incus_container"}); err == nil {
		t.Fatal("wrong runner digest accepted")
	}
}
