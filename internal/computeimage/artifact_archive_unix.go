//go:build linux || darwin

package computeimage

import (
	"errors"
	"os"
	"syscall"
)

func artifactArchiveSupported() bool { return true }

func artifactNoFollowFlags() int { return syscall.O_NOFOLLOW | syscall.O_NONBLOCK }

func artifactPrivateDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func artifactOwnedFile(info os.FileInfo, mode os.FileMode, singleLink bool) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Nlink > 0 && (!singleLink || stat.Nlink == 1)
}

func artifactTryLock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return ErrArtifactBusy
	}
	if err != nil {
		return ErrArtifactUnavailable
	}
	return nil
}

func artifactUnlock(file *os.File) error {
	if syscall.Flock(int(file.Fd()), syscall.LOCK_UN) != nil {
		return ErrArtifactUnavailable
	}
	return nil
}
