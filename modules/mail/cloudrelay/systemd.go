package cloudrelay

import (
	"context"
	"crypto/x509"
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// RunSystemdRuntime uses only systemd-provided directories and a fixed source
// path. It prepares the source and runs it within the same sandboxed process.
func RunSystemdRuntime(ctx context.Context, credentialDir, stateDir string, report func(string)) error {
	if !filepath.IsAbs(credentialDir) || filepath.Clean(credentialDir) != credentialDir || !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir || strings.Contains(stateDir, ":") {
		return ErrRuntimeConfiguration
	}
	configPath := filepath.Join(credentialDir, "runtime.json")
	cfg, err := LoadRuntimeConfig(configPath)
	if err != nil || cfg.Directory != filepath.Join(stateDir, "data", "source") {
		return ErrRuntimeConfiguration
	}
	// systemd's DynamicUser StateDirectory may itself be a system-managed
	// symlink. Its target must still be a private directory owned by this user.
	parent, err := unix.Open(stateDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrRuntimeUnavailable
	}
	defer unix.Close(parent)
	if !secureNoticeFile(parent, true) {
		return ErrRuntimeUnavailable
	}
	if err = unix.Mkdirat(parent, "data", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return ErrRuntimeUnavailable
	}
	data, err := unix.Openat(parent, "data", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrRuntimeUnavailable
	}
	defer unix.Close(data)
	if !secureNoticeFile(data, true) || unix.Fsync(parent) != nil {
		return ErrRuntimeUnavailable
	}
	roots, _ := x509.SystemCertPool()
	if _, err = prepareInstallRuntime(configPath, cfg, roots); err != nil {
		return err
	}
	return runRuntime(ctx, cfg, report)
}
