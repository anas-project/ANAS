package cloudrelay

import (
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

//go:embed packaging/anas-mail-relay.service
var systemdUnit []byte

var ErrPublishConflict = errors.New("relay service publication conflicts with existing state")
var ErrPublishUnknown = errors.New("relay service publication requires recovery")

type systemdLayout struct {
	etc, libexec, units, state string
	otherUnits                 []string
	privateState               string
}

// PublishSystemdFiles installs only the fixed unit, current executable, and a
// private configuration. It does not start/enable systemd or modify DNS. It
// never overwrites existing files; interrupted or unknown writes fail closed.
func PublishSystemdFiles(configPath string) error {
	if os.Geteuid() != 0 {
		return ErrPublishConflict
	}
	const maxBinary = 128 << 20
	executable, err := os.Open("/proc/self/exe")
	if err != nil {
		return ErrPublishConflict
	}
	defer executable.Close()
	binary, err := io.ReadAll(io.LimitReader(executable, maxBinary+1))
	if err != nil || len(binary) == 0 || len(binary) > maxBinary {
		return ErrPublishConflict
	}
	// Validate input before creating the conventional system directory.
	raw, cfg, err := readPrivateInstallInput(configPath)
	if err != nil {
		return err
	}
	defer clear(raw)
	if cfg.Directory != "/var/lib/anas-mail-relay/data/source" || cfg.SMTPListen == "" {
		return ErrPublishConflict
	}
	if _, _, err = prepareRuntimeSecurity(cfg); err != nil {
		return ErrPublishConflict
	}
	if err = ensureRootLibexec(); err != nil {
		return err
	}
	return publishSystemdFiles(systemdLayout{etc: "/etc", libexec: "/usr/local/libexec", units: "/etc/systemd/system", state: "/var/lib", otherUnits: []string{"/run/systemd/system", "/run/systemd/transient", "/usr/lib/systemd/system", "/lib/systemd/system"}, privateState: "/var/lib/private"}, configPath, binary, systemdUnit)
}

// Missing auxiliary lookup directories are harmless; an unsafe or inaccessible
// directory must not be interpreted as an absence of conflicting state.
func checkOptionalConflicts(path string, names ...string) error {
	if path == "" {
		return nil
	}
	fd, err := managedDirectory(path, 0)
	if err != nil {
		if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return ErrPublishConflict
	}
	defer unix.Close(fd)
	for _, name := range names {
		found, e := existsAt(fd, name)
		if e != nil || found {
			return ErrPublishConflict
		}
	}
	return nil
}

func managedDirectory(path string, mode uint32) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, ErrPublishConflict
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 || (mode != 0 && uint32(st.Mode)&0777 != mode) {
		unix.Close(fd)
		return -1, ErrPublishConflict
	}
	return fd, nil
}
func existsAt(dir int, name string) (bool, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
func readPrivateInstallInput(path string) ([]byte, RuntimeConfig, error) {
	if !filepath.IsAbs(path) {
		return nil, RuntimeConfig{}, ErrPublishConflict
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, RuntimeConfig{}, ErrPublishConflict
	}
	f := os.NewFile(uintptr(fd), "private install input")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return nil, RuntimeConfig{}, ErrPublishConflict
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil {
		return nil, RuntimeConfig{}, ErrPublishConflict
	}
	cfg, err := decodeRuntimeConfig(raw)
	if err != nil {
		clear(raw)
		return nil, RuntimeConfig{}, ErrPublishConflict
	}
	return raw, cfg, nil
}
func installBinding(cfg RuntimeConfig, config, binary, unit []byte) ([]byte, error) {
	key, err := hex.DecodeString(cfg.Control)
	if err != nil || len(key) != 32 {
		return nil, ErrPublishConflict
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("anas.mail-systemd-publication/v1\x00"))
	for _, part := range [][]byte{config, binary, unit} {
		sum := sha256.Sum256(part)
		_, _ = mac.Write(sum[:])
	}
	return []byte(hex.EncodeToString(mac.Sum(nil)) + "\n"), nil
}
func verifyInstallFile(dir int, name string, expected []byte, mode uint32) (bool, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, ErrPublishConflict
	}
	f := os.NewFile(uintptr(fd), "managed install file")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || uint32(st.Mode)&0777 != mode || st.Nlink != 1 || st.Size != int64(len(expected)) {
		return false, ErrPublishConflict
	}
	actual, err := io.ReadAll(io.LimitReader(f, int64(len(expected))+1))
	if err != nil || !hmac.Equal(actual, expected) {
		return false, ErrPublishConflict
	}
	return true, nil
}

// Writes staged bytes before publishing with no-replace rename. A complete
// stage can be resumed; an incomplete or foreign stage must be inspected.
func publishInstallFile(dir int, name string, content []byte, mode uint32) error {
	present, err := verifyInstallFile(dir, name, content, mode)
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	stage := "." + name + ".anas-stage"
	fd, err := unix.Openat(dir, stage, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err == nil {
		f := os.NewFile(uintptr(fd), "staged install file")
		if unix.Fchmod(fd, mode) != nil {
			f.Close()
			return ErrPublishUnknown
		}
		n, e := f.Write(content)
		if e != nil || n != len(content) || f.Sync() != nil {
			f.Close()
			return ErrPublishUnknown
		}
		if f.Close() != nil || unix.Fsync(dir) != nil {
			return ErrPublishUnknown
		}
	} else if !errors.Is(err, unix.EEXIST) {
		return ErrPublishUnknown
	}
	valid, err := verifyInstallFile(dir, stage, content, mode)
	if err != nil || !valid {
		return ErrPublishUnknown
	}
	if err = renameDirectoryExclusive(dir, stage, name); err != nil {
		return ErrPublishUnknown
	}
	if unix.Fsync(dir) != nil {
		return ErrPublishUnknown
	}
	return nil
}

// Each publication gets its own durable intent and completion marker. An
// issued operation with neither a complete stage nor its target is unknown;
// a completed target removed by an administrator must never be recreated.
func publishBoundFile(journal, dir int, name string, content []byte, mode uint32, binding []byte, label string) error {
	intentName, doneName := "publish-"+label+".intent", "publish-"+label+".done"
	intent, err := checkActionMarker(journal, intentName, binding)
	if err != nil {
		return err
	}
	done, err := checkActionMarker(journal, doneName, binding)
	if err != nil {
		return err
	}
	if done && !intent {
		return ErrPublishUnknown
	}
	present, err := verifyInstallFile(dir, name, content, mode)
	if err != nil {
		return err
	}
	stage, err := existsAt(dir, "."+name+".anas-stage")
	if err != nil {
		return ErrPublishUnknown
	}
	if done {
		if !present {
			return ErrPublishConflict
		}
		if stage {
			return ErrPublishUnknown
		}
		return nil
	}
	if intent {
		if present {
			if stage {
				return ErrPublishUnknown
			}
		} else {
			if !stage {
				return ErrPublishUnknown
			}
			if err = publishInstallFile(dir, name, content, mode); err != nil {
				return err
			}
		}
	} else {
		if present || stage {
			return ErrPublishConflict
		}
		if err = recordActionMarker(journal, intentName, binding); err != nil {
			return err
		}
		if err = publishInstallFile(dir, name, content, mode); err != nil {
			return err
		}
	}
	return recordActionMarker(journal, doneName, binding)
}
func publishSystemdFiles(layout systemdLayout, configPath string, binary, unit []byte) error {
	if len(binary) == 0 || len(binary) > 128<<20 || len(unit) == 0 || len(unit) > 64<<10 {
		return ErrPublishConflict
	}
	config, cfg, err := readPrivateInstallInput(configPath)
	if err != nil {
		return err
	}
	defer clear(config)
	if cfg.Directory != filepath.Join(layout.state, "anas-mail-relay", "data", "source") || cfg.SMTPListen == "" {
		return ErrPublishConflict
	}
	if _, _, err = prepareRuntimeSecurity(cfg); err != nil {
		return ErrPublishConflict
	}
	binding, err := installBinding(cfg, config, binary, unit)
	if err != nil {
		return err
	}
	etc, err := managedDirectory(layout.etc, 0)
	if err != nil {
		return err
	}
	defer unix.Close(etc)
	lib, err := managedDirectory(layout.libexec, 0)
	if err != nil {
		return err
	}
	defer unix.Close(lib)
	units, err := managedDirectory(layout.units, 0)
	if err != nil {
		return err
	}
	defer unix.Close(units)
	state, err := managedDirectory(layout.state, 0)
	if err != nil {
		return err
	}
	defer unix.Close(state)
	cfgExists, err := existsAt(etc, "anas-mail-relay")
	if err != nil {
		return ErrPublishConflict
	}
	if !cfgExists {
		for _, path := range layout.otherUnits {
			if err = checkOptionalConflicts(path, "anas-mail-relay.service", "anas-mail-relay.service.d"); err != nil {
				return err
			}
		}
		if err = checkOptionalConflicts(layout.privateState, "anas-mail-relay"); err != nil {
			return err
		}
		for _, v := range []struct {
			dir  int
			name string
		}{{lib, "anas-mail-relay"}, {units, "anas-mail-relay.service"}, {units, "anas-mail-relay.service.d"}, {state, "anas-mail-relay"}} {
			yes, e := existsAt(v.dir, v.name)
			if e != nil || yes {
				return ErrPublishConflict
			}
		}
		if err = unix.Mkdirat(etc, "anas-mail-relay", 0700); err != nil {
			return ErrPublishUnknown
		}
		fd, e := unix.Openat(etc, "anas-mail-relay", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return ErrPublishUnknown
		}
		e = unix.Fchmod(fd, 0700)
		unix.Close(fd)
		if e != nil || unix.Fsync(etc) != nil {
			return ErrPublishUnknown
		}
	}
	cfgdir, err := managedDirectory(filepath.Join(layout.etc, "anas-mail-relay"), 0700)
	if err != nil {
		return err
	}
	defer unix.Close(cfgdir)
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	var lock int
	fresh := !cfgExists
	if fresh {
		lock, err = unix.Openat(cfgdir, "install.lock", flags|unix.O_CREAT|unix.O_EXCL, 0600)
	} else {
		lock, err = unix.Openat(cfgdir, "install.lock", flags, 0)
	}
	if err != nil {
		return ErrPublishUnknown
	}
	defer unix.Close(lock)
	if !secureNoticeFile(lock, false) || unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrPublishConflict
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	if fresh {
		if n, e := unix.Write(lock, binding); e != nil || n != len(binding) || unix.Fsync(lock) != nil || unix.Fsync(cfgdir) != nil {
			return ErrPublishUnknown
		}
	} else {
		if _, err = unix.Seek(lock, 0, io.SeekStart); err != nil {
			return ErrPublishUnknown
		}
		f := os.NewFile(uintptr(mustDup(lock)), "install binding")
		if f == nil {
			return ErrPublishUnknown
		}
		actual, e := io.ReadAll(io.LimitReader(f, 129))
		f.Close()
		if e != nil || !hmac.Equal(actual, binding) {
			return ErrPublishConflict
		}
	}
	if yes, e := existsAt(units, "anas-mail-relay.service.d"); e != nil || yes {
		return ErrPublishConflict
	}
	if err = publishBoundFile(cfgdir, cfgdir, "runtime.json", config, 0600, binding, "config"); err != nil {
		return err
	}
	binDirExists, err := existsAt(lib, "anas-mail-relay")
	if err != nil {
		return ErrPublishUnknown
	}
	if !binDirExists {
		done, e := checkActionMarker(cfgdir, "publish-binary.done", binding)
		if e != nil {
			return e
		}
		if done {
			return ErrPublishConflict
		}
		intent, e := checkActionMarker(cfgdir, "publish-binary.intent", binding)
		if e != nil {
			return e
		}
		if intent {
			return ErrPublishUnknown
		}
	}
	if err = unix.Mkdirat(lib, "anas-mail-relay", 0755); err == nil {
		fd, e := unix.Openat(lib, "anas-mail-relay", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return ErrPublishUnknown
		}
		e = unix.Fchmod(fd, 0755)
		unix.Close(fd)
		if e != nil {
			return ErrPublishUnknown
		}
	} else if !errors.Is(err, unix.EEXIST) {
		return ErrPublishUnknown
	}
	if unix.Fsync(lib) != nil {
		return ErrPublishUnknown
	}
	bindir, err := managedDirectory(filepath.Join(layout.libexec, "anas-mail-relay"), 0755)
	if err != nil {
		return err
	}
	defer unix.Close(bindir)
	if err = publishBoundFile(cfgdir, bindir, "cloudrelay", binary, 0755, binding, "binary"); err != nil {
		return err
	}
	return publishBoundFile(cfgdir, units, "anas-mail-relay.service", unit, 0644, binding, "unit")
}
func mustDup(fd int) int {
	copy, err := unix.Dup(fd)
	if err != nil {
		return -1
	}
	return copy
}
