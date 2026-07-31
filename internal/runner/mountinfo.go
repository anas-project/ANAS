package runner

// Identifying which filesystem a path is on, without privilege.
//
// Backup mode selection turns on one question: are the source and the
// destination the same Btrfs filesystem? Get it wrong in the permissive
// direction and `btrfs subvolume snapshot` is attempted across filesystems and
// fails; get it wrong in the restrictive direction and the cheapest mode is
// never offered.
//
// Two obvious answers are both wrong.
//
// st_dev is wrong because Btrfs hands every subvolume its own anonymous device
// number, so two directories on one filesystem report different st_dev. That is
// the trap the contract calls out, and it looks right in casual testing because
// a workspace whose data/ is not a subvolume does compare equal.
//
// statfs(2)'s f_fsid is also wrong, which the contract does not say. Btrfs mixes
// the subvolume's root objectid into it:
//
//	buf->f_fsid.val[0] ^= objectid >> 32;
//	buf->f_fsid.val[1] ^= objectid;
//
// Measured on ln.hlong.wang (kernel 5.15), /data and the subvolume
// /data/.../ws/data report 38df694b8bbdc98e and 38df680d8bbdc98e — equal in the
// low half, different in the high. So f_fsid identifies a subvolume, not a
// filesystem, and comparing it would reject a destination on the very same disk.
//
// The identity that is actually stable is the filesystem UUID, and it is
// readable without privilege by joining two world-readable sources: the mount
// table names the block device backing a path, and /sys/fs/btrfs/<uuid>/devices/
// lists the devices belonging to each Btrfs filesystem. `btrfs filesystem show`
// would answer directly but needs root, like every other tree-search ioctl.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// mountEntry is the part of a /proc/self/mountinfo line this package needs.
type mountEntry struct {
	MountPoint string
	FSType     string
	Source     string
}

// readMounts parses /proc/self/mountinfo. On a host without it — macOS, where
// none of the Btrfs paths can run anyway — the result is empty and every caller
// degrades to "unknown" rather than to a wrong answer.
func readMounts() []mountEntry {
	raw, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	entries := []mountEntry{}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		// Optional tagged fields sit between the mount options and a lone "-",
		// and there may be any number of them, so the separator has to be
		// located rather than assumed at a fixed index.
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		entries = append(entries, mountEntry{
			MountPoint: unescapeMountField(fields[4]),
			FSType:     fields[sep+1],
			Source:     unescapeMountField(fields[sep+2]),
		})
	}
	return entries
}

// unescapeMountField undoes the octal escaping the kernel applies to spaces,
// tabs, newlines and backslashes in mount paths.
func unescapeMountField(field string) string {
	if !strings.Contains(field, `\`) {
		return field
	}
	var b strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+3 < len(field) {
			if n, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(field[i])
	}
	return b.String()
}

// mountEntryFor returns the mount whose mount point is the longest prefix of
// path. Later lines win at equal length, which is what makes an over-mount
// shadow the filesystem underneath it.
func mountEntryFor(path string) (mountEntry, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return mountEntry{}, false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	best := mountEntry{}
	bestLen := -1
	for _, entry := range readMounts() {
		if !pathWithin(abs, entry.MountPoint) {
			continue
		}
		if len(entry.MountPoint) >= bestLen {
			best, bestLen = entry, len(entry.MountPoint)
		}
	}
	return best, bestLen >= 0
}

// pathWithin reports whether path is root or lies under it, comparing whole
// path components so that /datastore is not treated as being under /data.
func pathWithin(path, root string) bool {
	if root == "/" {
		return true
	}
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// filesystemName returns the mount table's name for the filesystem holding
// path, or "" when it cannot be determined.
func filesystemName(path string) string {
	entry, ok := mountEntryFor(path)
	if !ok {
		return ""
	}
	return entry.FSType
}

// pathIsMountPoint reports whether path is itself a mount point.
//
// It cannot be answered by comparing st_dev with the parent's, which is the
// usual trick: on Btrfs the parent of a subvolume already differs in st_dev, so
// that test calls every subvolume a mount point. A subvolume is not a mount
// point, and the distinction matters because only a real mount makes the
// restore path's rename(2) return EBUSY.
func pathIsMountPoint(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	for _, entry := range readMounts() {
		if entry.MountPoint == abs {
			return true
		}
	}
	return false
}

// btrfsFilesystemID returns the UUID of the Btrfs filesystem holding path, or
// "" when path is not on Btrfs or the sysfs mapping is unavailable.
func btrfsFilesystemID(path string) string {
	entry, ok := mountEntryFor(path)
	if !ok || entry.FSType != "btrfs" {
		return ""
	}
	device := entry.Source
	// A device-mapper or by-uuid path has to be resolved to the kernel name,
	// because that is what sysfs uses.
	if resolved, err := filepath.EvalSymlinks(device); err == nil {
		device = resolved
	}
	name := filepath.Base(device)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join("/sys/fs/btrfs", "*", "devices", name))
	if err != nil || len(matches) == 0 {
		return ""
	}
	// /sys/fs/btrfs/<uuid>/devices/<name>
	return filepath.Base(filepath.Dir(filepath.Dir(matches[0])))
}

// sameBtrfsFilesystem reports whether two paths live on one Btrfs filesystem.
//
// The UUID comparison is the answer whenever both are known. When sysfs cannot
// supply one — an unusual container mount namespace, say — it falls back to
// asking the filesystem directly with a reflink, which succeeds only within a
// single Btrfs and is the operation the cheap modes actually depend on. A
// probe is better than a guess here: guessing "same" attempts a snapshot that
// fails, and guessing "different" hides the fastest mode.
func sameBtrfsFilesystem(source, dest string) bool {
	sourceID, destID := btrfsFilesystemID(source), btrfsFilesystemID(dest)
	if sourceID != "" && destID != "" {
		return sourceID == destID
	}
	return reflinkProbe(source, dest)
}

// reflinkProbe asks the filesystem whether two directories are on one Btrfs, by
// attempting the operation that only succeeds within one. It is the fallback
// for when sysfs cannot name the filesystem, and it writes one small temporary
// file into each directory.
func reflinkProbe(source, dest string) bool {
	from, err := os.CreateTemp(source, ".anas-reflink-probe-")
	if err != nil {
		return false
	}
	defer os.Remove(from.Name())
	_, _ = from.WriteString("anas")
	_ = from.Close()
	to := filepath.Join(dest, filepath.Base(from.Name())+".dest")
	defer os.Remove(to)
	return exec.Command("cp", "--reflink=always", from.Name(), to).Run() == nil
}
