package cloudrelay

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// ensureStandardLibexec creates only the conventional libexec directory under
// an already validated, trusted /usr/local. It never follows symlinks, changes
// existing permissions, or removes an existing directory.
func ensureStandardLibexec(parentPath string) error {
	parent, err := managedDirectory(parentPath, 0)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	created := false
	if err = unix.Mkdirat(parent, "libexec", 0755); err == nil {
		created = true
	} else if !errors.Is(err, unix.EEXIST) {
		return ErrPublishUnknown
	}
	child, err := unix.Openat(parent, "libexec", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrPublishConflict
	}
	defer unix.Close(child)
	if created && unix.Fchmod(child, 0755) != nil {
		return ErrPublishUnknown
	}
	var st unix.Stat_t
	if unix.Fstat(child, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&07000 != 0 || st.Mode&0022 != 0 || st.Mode&0111 != 0111 {
		return ErrPublishConflict
	}
	if unix.Fsync(child) != nil || unix.Fsync(parent) != nil {
		return ErrPublishUnknown
	}
	return nil
}

func ensureRootLibexec() error {
	if err := verifyRootAncestors("/usr/local"); err != nil {
		return err
	}
	return ensureStandardLibexec("/usr/local")
}
