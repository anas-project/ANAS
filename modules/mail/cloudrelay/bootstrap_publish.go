package cloudrelay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// PublishBootstrapBinary installs only this executable at the fixed helper
// path. It does not create an SSH identity, grant sudo, change sshd or start a
// service. A root-owned journal protects retries and refuses unknown state.
func PublishBootstrapBinary() error {
	if os.Geteuid() != 0 {
		return ErrPublishConflict
	}
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return ErrPublishConflict
	}
	defer f.Close()
	binary, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	if err != nil || len(binary) == 0 || len(binary) > 128<<20 {
		return ErrPublishConflict
	}
	if err := ensureRootLibexec(); err != nil {
		return err
	}
	return publishBootstrapBinary("/etc", "/usr/local/libexec", binary)
}

func publishBootstrapBinary(etcPath, libPath string, binary []byte) error {
	if len(binary) == 0 || len(binary) > 128<<20 {
		return ErrPublishConflict
	}
	etc, err := managedDirectory(etcPath, 0)
	if err != nil {
		return err
	}
	defer unix.Close(etc)
	lib, err := managedDirectory(libPath, 0)
	if err != nil {
		return err
	}
	defer unix.Close(lib)
	const name = "anas-mail-relay-bootstrap"
	sum := sha256.Sum256(binary)
	binding := []byte("anas.mail-bootstrap-binary/v1 " + hex.EncodeToString(sum[:]) + "\n")
	journalExists, err := existsAt(etc, name)
	if err != nil {
		return ErrPublishUnknown
	}
	if !journalExists {
		found, e := existsAt(lib, name)
		if e != nil {
			return ErrPublishUnknown
		}
		if found {
			return ErrPublishConflict
		}
		if unix.Mkdirat(etc, name, 0700) != nil {
			return ErrPublishUnknown
		}
		fd, e := unix.Openat(etc, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return ErrPublishUnknown
		}
		e = unix.Fchmod(fd, 0700)
		unix.Close(fd)
		if e != nil || unix.Fsync(etc) != nil {
			return ErrPublishUnknown
		}
	}
	journal, err := managedDirectory(filepath.Join(etcPath, name), 0700)
	if err != nil {
		return err
	}
	defer unix.Close(journal)
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if !journalExists {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	lock, err := unix.Openat(journal, "install.lock", flags, 0600)
	if err != nil {
		return ErrPublishUnknown
	}
	defer unix.Close(lock)
	if !secureNoticeFile(lock, false) || unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrPublishConflict
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	if !journalExists {
		if unix.Fchmod(lock, 0600) != nil {
			return ErrPublishUnknown
		}
		n, e := unix.Write(lock, binding)
		if e != nil || n != len(binding) || unix.Fsync(lock) != nil || unix.Fsync(journal) != nil {
			return ErrPublishUnknown
		}
	} else {
		if _, err = unix.Seek(lock, 0, io.SeekStart); err != nil {
			return ErrPublishUnknown
		}
		copy, err := unix.Dup(lock)
		if err != nil {
			return ErrPublishUnknown
		}
		f := os.NewFile(uintptr(copy), "bootstrap binding")
		actual, e := io.ReadAll(io.LimitReader(f, 129))
		f.Close()
		if e != nil || !hmac.Equal(actual, binding) {
			return ErrPublishConflict
		}
	}
	intent, err := checkActionMarker(journal, "directory.intent", binding)
	if err != nil {
		return err
	}
	done, err := checkActionMarker(journal, "directory.done", binding)
	if err != nil {
		return err
	}
	exists, err := existsAt(lib, name)
	if err != nil {
		return ErrPublishUnknown
	}
	if done {
		if !intent {
			return ErrPublishUnknown
		}
		if !exists {
			return ErrPublishConflict
		}
	} else {
		// An interrupted mkdir is deliberately not adopted, even if it appears
		// empty; it might have been created by an independent administrator.
		if intent {
			return ErrPublishUnknown
		}
		if exists {
			return ErrPublishConflict
		}
		if err = recordActionMarker(journal, "directory.intent", binding); err != nil {
			return err
		}
		if err = unix.Mkdirat(lib, name, 0755); err != nil {
			return ErrPublishUnknown
		}
		fd, e := unix.Openat(lib, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return ErrPublishUnknown
		}
		e = unix.Fchmod(fd, 0755)
		unix.Close(fd)
		if e != nil || unix.Fsync(lib) != nil {
			return ErrPublishUnknown
		}
		if err = recordActionMarker(journal, "directory.done", binding); err != nil {
			return err
		}
	}
	binaryDir, err := managedDirectory(filepath.Join(libPath, name), 0755)
	if err != nil {
		return err
	}
	defer unix.Close(binaryDir)
	return publishBoundFile(journal, binaryDir, "cloudrelay", binary, 0755, binding, "binary")
}
