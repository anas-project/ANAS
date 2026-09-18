//go:build linux

package jobexecutor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const maxModuleActionExecutable = 64 << 20

type moduleActionProgram struct {
	image     *os.File
	directory *os.File
}

func (program *moduleActionProgram) Close() error {
	return errors.Join(program.image.Close(), program.directory.Close())
}

func unprivilegedModuleActionProcess() bool {
	if os.Geteuid() == 0 || os.Geteuid() != os.Getuid() || os.Getegid() != os.Getgid() {
		return false
	}
	// Ambient capabilities require permitted/inheritable capabilities; reject
	// all of these instead of turning the Module adapter into a root mode.
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var capabilities [2]unix.CapUserData
	if unix.Capget(&header, &capabilities[0]) != nil {
		return false
	}
	for _, capability := range capabilities {
		if capability.Effective != 0 || capability.Permitted != 0 || capability.Inheritable != 0 {
			return false
		}
	}
	return true
}

// Pin the deployment directory and execute the exact digest-checked bytes from
// a sealed anonymous file. A pathname/digest check followed by path-based exec
// would let replacement between those steps select a different executable.
// This is not a sandbox: declared locks, policy and credential boundaries still
// belong to the trusted application adapter. There is no disk or shell fallback.
func prepareModuleActionProgram(definition ModuleActionDefinition) (*moduleActionProgram, error) {
	if !unprivilegedModuleActionProcess() {
		return nil, ErrModuleActionUnavailable
	}
	root, err := os.OpenRoot(definition.ModuleRoot)
	if err != nil {
		return nil, ErrModuleActionUnavailable
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, ErrModuleActionUnavailable
	}
	keepDirectory := false
	defer func() {
		if !keepDirectory {
			_ = directory.Close()
		}
	}()
	source, err := root.OpenFile(definition.Executable, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrModuleActionUnavailable
	}
	defer source.Close()
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Size() < 4 || before.Size() > maxModuleActionExecutable || before.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return nil, ErrModuleActionUnavailable
	}
	fd, err := unix.MemfdCreate("anas-module-action", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, ErrModuleActionUnavailable
	}
	image := os.NewFile(uintptr(fd), "anas-module-action")
	keepImage := false
	defer func() {
		if !keepImage {
			_ = image.Close()
		}
	}()
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(image, digest), io.LimitReader(source, maxModuleActionExecutable+1))
	if err != nil || written != before.Size() || written > maxModuleActionExecutable || hex.EncodeToString(digest.Sum(nil)) != definition.ExecutableDigest {
		return nil, ErrModuleActionUnavailable
	}
	after, err := source.Stat()
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, ErrModuleActionUnavailable
	}
	var magic [4]byte
	if _, err := image.ReadAt(magic[:], 0); err != nil || !bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'}) {
		return nil, ErrModuleActionUnavailable
	}
	if image.Chmod(0700) != nil {
		return nil, ErrModuleActionUnavailable
	}
	if _, err := unix.FcntlInt(image.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return nil, ErrModuleActionUnavailable
	}
	if _, err := image.Seek(0, io.SeekStart); err != nil {
		return nil, ErrModuleActionUnavailable
	}
	keepDirectory, keepImage = true, true
	return &moduleActionProgram{image: image, directory: directory}, nil
}
