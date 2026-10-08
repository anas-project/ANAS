//go:build linux

package runner

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func openTemporaryRoot(path string) (*os.Root, *os.File, error) {
	parent, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	defer unix.Close(parent)
	relative := strings.TrimPrefix(path, "/")
	if relative == "" {
		relative = "."
	}
	fd, err := unix.Openat2(parent, relative, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS})
	if err != nil {
		return nil, nil, fmt.Errorf("open temporary directory without following links: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	root, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return root, file, nil
}

func temporaryFileIdentity(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	// Device numbers change across reboot and Btrfs assigns anonymous devices
	// to subvolumes. The registry separately binds this inode to filesystem UUID
	// and mount root, rather than treating st_dev as persistent identity.
	return strconv.FormatUint(stat.Ino, 10)
}

func temporaryFileOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid() && stat.Nlink == 1
}

func inspectTemporaryFilesystem(path string) (TemporaryFilesystem, uint64, uint64, error) {
	handle, err := openTemporaryDirectory(path)
	if err != nil {
		return TemporaryFilesystem{}, 0, 0, err
	}
	defer handle.Close()
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(handle.file.Fd()), &stat); err != nil {
		return TemporaryFilesystem{}, 0, 0, err
	}
	mount, ok := mountEntryFor(path)
	if !ok {
		return TemporaryFilesystem{}, 0, 0, errors.New("temporary filesystem mount identity is unavailable")
	}
	identity := TemporaryFilesystem{Type: mount.FSType, MountPoint: mount.MountPoint, MountRoot: temporaryMountRoot(mount.MountPoint)}
	if identity.MountRoot == "" {
		return TemporaryFilesystem{}, 0, 0, errors.New("temporary filesystem mount root is unavailable")
	}
	if mount.FSType == "btrfs" {
		identity.ID = btrfsFilesystemID(path)
		if identity.ID == "" {
			return TemporaryFilesystem{}, 0, 0, errors.New("Btrfs filesystem UUID is unavailable")
		}
	} else {
		identity.ID = temporaryBlockFilesystemUUID(mount.Source)
		if identity.ID == "" {
			switch mount.FSType {
			case "ext2", "ext3", "ext4", "xfs":
				identity.ID = fmt.Sprintf("fsid:%08x%08x", uint32(stat.Fsid.Val[0]), uint32(stat.Fsid.Val[1]))
			case "tmpfs", "overlay":
				boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
				if err != nil {
					return TemporaryFilesystem{}, 0, 0, err
				}
				identity.ID = fmt.Sprintf("ephemeral:%s:%08x%08x", strings.TrimSpace(string(boot)), uint32(stat.Fsid.Val[0]), uint32(stat.Fsid.Val[1]))
			default:
				return TemporaryFilesystem{}, 0, 0, fmt.Errorf("stable temporary filesystem identity is unsupported for %s", mount.FSType)
			}
		}
	}
	freeBytes := stat.Bavail * uint64(stat.Bsize)
	freeInodes := stat.Ffree
	if mount.FSType == "btrfs" && stat.Files == 0 {
		freeInodes = math.MaxUint64
	} // Btrfs has no fixed inode pool.
	if err := handle.check(); err != nil {
		return TemporaryFilesystem{}, 0, 0, err
	}
	return identity, freeBytes, freeInodes, nil
}

func temporaryMountRoot(mountPoint string) string {
	body, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	root := ""
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 6 && unescapeMountField(fields[4]) == mountPoint {
			root = unescapeMountField(fields[3])
		}
	}
	return root
}

func temporaryBlockFilesystemUUID(source string) string {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir("/dev/disk/by-uuid")
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		candidate, err := filepath.EvalSymlinks(filepath.Join("/dev/disk/by-uuid", entry.Name()))
		if err == nil && candidate == resolved {
			return entry.Name()
		}
	}
	return ""
}

func checkTemporaryFeatures(path string, features []string) error {
	if len(features) == 0 {
		return nil
	}
	handle, err := openTemporaryDirectory(path)
	if err != nil {
		return err
	}
	defer handle.Close()
	for _, feature := range features {
		switch feature {
		case "exec":
			var stat unix.Statfs_t
			if unix.Fstatfs(int(handle.file.Fd()), &stat) != nil || stat.Flags&unix.ST_NOEXEC != 0 {
				return errors.New("temporary filesystem must permit execution")
			}
		case "hardlink":
			id, err := newTemporaryID()
			if err != nil {
				return err
			}
			name := ".anas-link-check-" + id
			file, err := handle.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			file.Close()
			err = handle.root.Link(name, name+"-link")
			handle.root.Remove(name + "-link")
			handle.root.Remove(name)
			if err != nil {
				return fmt.Errorf("temporary filesystem must support hardlinks: %w", err)
			}
		default:
			return fmt.Errorf("unsupported temporary filesystem feature %s", feature)
		}
	}
	return handle.check()
}

func removeTemporaryLeaseDirectory(lease *TemporaryLease) error {
	root, err := openTemporaryDirectory(lease.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(lease.Root, lease.Path)
	if err != nil || strings.HasPrefix(relative, "..") || relative == "." {
		return errors.New("refusing to remove outside a registered instance tree")
	}
	parts := strings.Split(relative, string(filepath.Separator))
	parent := int(root.file.Fd())
	var descriptors []int
	defer func() {
		for _, fd := range descriptors {
			unix.Close(fd)
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		fd, err := temporaryOpenChild(parent, part)
		if err != nil {
			return err
		}
		descriptors = append(descriptors, fd)
		parent = fd
	}
	if err := temporaryRemoveAt(parent, parts[len(parts)-1], lease.DirectoryID, true); err != nil {
		return err
	}
	return root.check()
}

func temporaryOpenChild(parent int, name string) (int, error) {
	return unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV})
}

func confirmTemporaryDirectoryAbsent(lease *TemporaryLease) (bool, error) {
	root, err := openTemporaryDirectory(lease.Root)
	if err != nil {
		return false, err
	}
	defer root.Close()
	relative, err := filepath.Rel(lease.Root, lease.Path)
	if err != nil || strings.HasPrefix(relative, "..") || relative == "." {
		return false, errors.New("temporary absence check escaped its registered root")
	}
	parts := strings.Split(relative, string(filepath.Separator))
	parent := int(root.file.Fd())
	var descriptors []int
	defer func() {
		for _, fd := range descriptors {
			unix.Close(fd)
		}
	}()
	// A missing ancestor is not proof of completed leaf deletion. In
	// particular an absent mount or replaced parent cannot erase authority.
	for _, part := range parts[:len(parts)-1] {
		fd, err := temporaryOpenChild(parent, part)
		if err != nil {
			return false, err
		}
		descriptors = append(descriptors, fd)
		parent = fd
	}
	var info unix.Stat_t
	err = unix.Fstatat(parent, parts[len(parts)-1], &info, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return false, err
	}
	return errors.Is(err, unix.ENOENT), root.check()
}

func temporaryRemoveAt(parent int, name, expectedInode string, preserveMarker bool) error {
	fd, err := temporaryOpenChild(parent, name)
	if err != nil {
		return fmt.Errorf("refusing unsafe temporary directory traversal: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var original unix.Stat_t
	if err := unix.Fstat(fd, &original); err != nil {
		return err
	}
	if expectedInode != "" && strconv.FormatUint(original.Ino, 10) != expectedInode {
		return errors.New("temporary deletion directory identity changed")
	}
	entries, err := file.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if preserveMarker && entry.Name() == ".anas-temp-owner.yml" {
			continue
		}
		var info unix.Stat_t
		if err := unix.Fstatat(fd, entry.Name(), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if info.Mode&unix.S_IFMT == unix.S_IFDIR {
			if err := temporaryRemoveAt(fd, entry.Name(), strconv.FormatUint(info.Ino, 10), false); err != nil {
				return err
			}
		} else {
			// Unlink a symlink itself; never follow its target. File contents are
			// irrelevant to deletion authorization after ownership and refs agree.
			if err := unix.Unlinkat(fd, entry.Name(), 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	var named unix.Stat_t
	if err := unix.Fstatat(parent, name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if original.Ino != named.Ino || original.Dev != named.Dev || named.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("temporary deletion target was replaced")
	}
	var marker []byte
	if preserveMarker {
		markerFD, err := unix.Openat(fd, ".anas-temp-owner.yml", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		markerFile := os.NewFile(uintptr(markerFD), "ownership-marker")
		marker, err = io.ReadAll(io.LimitReader(markerFile, 1<<20))
		markerFile.Close()
		if err != nil {
			return err
		}
		if err := unix.Unlinkat(fd, ".anas-temp-owner.yml", 0); err != nil {
			return err
		}
	}
	if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil {
		// A partial deletion remains retryable. Restore our marker through the
		// still-open original descriptor, never through the possibly replaced path.
		if preserveMarker {
			markerFD, openErr := unix.Openat(fd, ".anas-temp-owner.yml", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0400)
			if openErr == nil {
				markerFile := os.NewFile(uintptr(markerFD), "ownership-marker")
				_, writeErr := markerFile.Write(marker)
				syncErr := markerFile.Sync()
				markerFile.Close()
				err = errors.Join(err, writeErr, syncErr)
			} else {
				err = errors.Join(err, openErr)
			}
		}
		return err
	}
	return unix.Fsync(parent)
}
