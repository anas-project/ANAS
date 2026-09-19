//go:build linux

package consoleconfig

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var errServiceConfig = errors.New("service configuration requires a root-owned, single-link 0640 file for the service primary group and trusted ancestors")

// LoadService preserves the root daemon's existing 0600 policy. A deliberately
// provisioned non-root Linux daemon may read a root-owned 0640 file belonging
// to its primary group. This never accepts a service-writable configuration,
// changes ownership/modes, or selects an identity from file contents.
func LoadService(path string) (Config, error) {
	if os.Getuid() == 0 && os.Geteuid() == 0 {
		return Load(path, RootOwnedFilePolicy())
	}
	if os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() || os.Geteuid() == 0 || os.Getegid() == 0 {
		return Config{}, errServiceConfig
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return Config{}, errServiceConfig
	}
	defer unix.Close(root)
	return loadServiceAt(root, path, 0, uint32(os.Getegid()))
}

// Root/owner are private filesystem-fixture seams, never request/config flags.
func loadServiceAt(root int, path string, owner, group uint32) (Config, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return Config{}, errServiceConfig
	}
	parts := append([]string{"."}, strings.Split(strings.TrimPrefix(path, "/"), "/")...)
	if len(parts) > 64 {
		return Config{}, errServiceConfig
	}
	var files []*os.File
	var stats []unix.Stat_t
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	parent := root
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(parent, part, flags, 0)
		if err != nil {
			return Config{}, errServiceConfig
		}
		files = append(files, os.NewFile(uintptr(fd), "service-config"))
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != owner || st.Mode&07000 != 0 {
			return Config{}, errServiceConfig
		}
		if i < len(parts)-1 {
			if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 {
				return Config{}, errServiceConfig
			}
		} else if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0640 || st.Gid != group || st.Nlink != 1 || st.Size <= 0 || st.Size > maximumConfigBytes {
			return Config{}, errServiceConfig
		}
		stats = append(stats, st)
		parent = fd
	}
	body, err := io.ReadAll(io.LimitReader(files[len(files)-1], maximumConfigBytes+1))
	if err != nil || int64(len(body)) != stats[len(stats)-1].Size {
		return Config{}, errServiceConfig
	}
	defer clear(body)
	for i, f := range files {
		var current, named unix.Stat_t
		leaf := i == len(files)-1
		if unix.Fstat(int(f.Fd()), &current) != nil || !sameServiceConfigStat(stats[i], current, leaf) {
			return Config{}, errServiceConfig
		}
		if i > 0 && (unix.Fstatat(int(files[i-1].Fd()), parts[i], &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameServiceConfigStat(stats[i], named, leaf)) {
			return Config{}, errServiceConfig
		}
	}
	config, err := Parse(body)
	if err != nil {
		return Config{}, err
	}
	if err := validateResolvedStorageBoundary(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func sameServiceConfigStat(a, b unix.Stat_t, leaf bool) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Uid == b.Uid && a.Gid == b.Gid && a.Mode == b.Mode &&
		(!leaf || a.Size == b.Size && a.Nlink == b.Nlink && a.Mtim == b.Mtim && a.Ctim == b.Ctim)
}
