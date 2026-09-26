//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
	"golang.org/x/sys/unix"
)

type distroProgram struct {
	file   *os.File
	target computeimage.Target
}

func prepareDistrobuilder(ctx context.Context, path, digest string, target computeimage.Target) (preparedDistrobuilder, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 || target.Architecture != runtime.GOARCH {
		return nil, fmt.Errorf("distrobuilder requires root permission on an isolated native Linux release builder")
	}
	if target.Interface != "incus_vm" && target.Interface != "incus_container" {
		return nil, computeimage.ErrArtifactInvalid
	}
	program, err := openDistrobuilder(ctx, path, digest)
	if err != nil {
		return nil, err
	}
	return &distroProgram{file: program, target: target}, nil
}

func prepareForgejoRunnerInput(ctx context.Context, path, digest string, target computeimage.Target) (computeimage.ArtifactBuildInput, error) {
	if target.Architecture != runtime.GOARCH || (target.Interface != "incus_vm" && target.Interface != "incus_container") {
		return computeimage.ArtifactBuildInput{}, computeimage.ErrArtifactInvalid
	}
	if err := validatePinnedELF(ctx, path, digest, target.Architecture, false); err != nil {
		return computeimage.ArtifactBuildInput{}, err
	}
	return computeimage.ArtifactBuildInput{Name: "forgejo-runner", Path: path, SHA256: digest}, nil
}

func (p *distroProgram) Close() error { return p.file.Close() }

// Native release preparation only. This is deliberately not registered as a
// host action, invoked by an HTTP handler, or called by Provider ensure.
// distrobuilder recipes and the builder must be reviewed as root-capable code.
func (p *distroProgram) Build(ctx context.Context, request computeimage.ArtifactBuildRequest) error {
	if request.Target != p.target {
		return computeimage.ErrArtifactInvalid
	}
	args, err := distrobuilderArgs(ctx, request)
	if err != nil {
		return err
	}
	// Bind execution to the sealed, measured ELF, not a mutable pathname.
	command := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	command.Args[0] = "distrobuilder"
	command.ExtraFiles = []*os.File{p.file}
	command.Dir = filepath.Dir(request.RecipeFile)
	command.Env = distrobuilderEnvironment()
	// Keep only closed diagnostic stage labels. Raw builder records never enter
	// the metadata protocol, files, error strings or deployment logs.
	observation := &buildObservation{}
	command.Stdout, command.Stderr = observation, observation
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	command.WaitDelay = 5 * time.Second
	if err := command.Run(); err != nil {
		return observation.failure()
	}
	if observation.sourceWasUnverified() {
		return observation.failure()
	}
	_ = observation.failure() // Erase any unterminated output after success too.
	return nil
}

// Copy and seal before execution so rename and in-place binary changes cannot
// swap the measured code. A digest is an integrity pin, not provenance: the
// release operator must obtain it from the trusted builder distribution.
func openDistrobuilder(ctx context.Context, path, expected string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !validSHA256(expected) {
		return nil, computeimage.ErrArtifactInvalid
	}
	source, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, computeimage.ErrArtifactUnavailable
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !validPinnedProgramInfo(info, true) {
		return nil, computeimage.ErrArtifactInvalid
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return nil, computeimage.ErrArtifactInvalid
	}
	fd, err := unix.MemfdCreate("anas-distrobuilder", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, computeimage.ErrArtifactUnavailable
	}
	sealed := os.NewFile(uintptr(fd), "anas-distrobuilder")
	keep := false
	defer func() {
		if !keep {
			_ = sealed.Close()
		}
	}()
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	reader := io.LimitReader(source, info.Size()+1)
	var size int64
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			written, err := sealed.Write(buffer[:n])
			if err != nil || written != n {
				return nil, computeimage.ErrArtifactUnavailable
			}
			_, _ = hash.Write(buffer[:n])
			size += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, computeimage.ErrArtifactUnavailable
		}
	}
	after, err := source.Stat()
	if err != nil || size != info.Size() || !os.SameFile(info, after) || info.Size() != after.Size() || info.Mode() != after.Mode() || !info.ModTime().Equal(after.ModTime()) || hex.EncodeToString(hash.Sum(nil)) != expected {
		return nil, computeimage.ErrArtifactUnavailable
	}
	if sealed.Chmod(0500) != nil {
		return nil, computeimage.ErrArtifactUnavailable
	}
	if _, err := unix.FcntlInt(sealed.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return nil, computeimage.ErrArtifactUnavailable
	}
	image, err := elf.NewFile(sealed)
	if err != nil || image.Class != elf.ELFCLASS64 || image.Data != elf.ELFDATA2LSB {
		return nil, computeimage.ErrArtifactInvalid
	}
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if want == 0 || image.Machine != want {
		return nil, computeimage.ErrArtifactInvalid
	}
	keep = true
	return sealed, nil
}

func validatePinnedELF(ctx context.Context, path, expected, arch string, executable bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !validSHA256(expected) {
		return computeimage.ErrArtifactInvalid
	}
	source, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return computeimage.ErrArtifactUnavailable
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !validPinnedProgramInfo(info, executable) {
		return computeimage.ErrArtifactInvalid
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	reader := io.LimitReader(source, info.Size()+1)
	var size int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
			size += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return computeimage.ErrArtifactUnavailable
		}
	}
	after, err := source.Stat()
	if err != nil || size != info.Size() || !os.SameFile(info, after) || info.Size() != after.Size() || info.Mode() != after.Mode() || !info.ModTime().Equal(after.ModTime()) || hex.EncodeToString(hash.Sum(nil)) != expected {
		return computeimage.ErrArtifactUnavailable
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return computeimage.ErrArtifactUnavailable
	}
	image, err := elf.NewFile(source)
	if err != nil || image.Class != elf.ELFCLASS64 || image.Data != elf.ELFDATA2LSB {
		return computeimage.ErrArtifactInvalid
	}
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[arch]
	if want == 0 || image.Machine != want {
		return computeimage.ErrArtifactInvalid
	}
	return nil
}

func validPinnedProgramInfo(info os.FileInfo, executable bool) bool {
	return info.Mode().IsRegular() &&
		info.Size() >= 4096 && info.Size() <= 256<<20 &&
		(!executable || info.Mode().Perm()&0111 != 0) &&
		info.Mode().Perm()&0022 == 0 &&
		info.Mode()&(os.ModeSetuid|os.ModeSetgid) == 0
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
