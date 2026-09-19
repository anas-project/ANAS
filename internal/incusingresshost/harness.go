package incusingresshost

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NativeHarnessConfig describes a dry-run host acceptance harness. It is
// intentionally not a daemon and it refuses default production sockets; callers
// must point it at disposable lab sockets and an empty private state root.
type NativeHarnessConfig struct {
	DockerSocket string
	IncusSocket  string
	StateRoot    string
	Source       string
	DryRun       bool
	TestMarker   string
}

func ValidateNativeHarness(config NativeHarnessConfig) error {
	if !config.DryRun {
		return fmt.Errorf("native ingress harness is dry-run only in repository tests")
	}
	if config.Source == "" {
		return fmt.Errorf("native ingress harness requires a source revision or document identifier")
	}
	if config.TestMarker == "" || !filepath.IsAbs(config.TestMarker) || strings.Contains(config.TestMarker, "..") {
		return fmt.Errorf("native ingress harness requires an explicit parent-created test marker")
	}
	for label, path := range map[string]string{"Docker": config.DockerSocket, "Incus": config.IncusSocket, "state": config.StateRoot} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%s path must be absolute", label)
		}
	}
	for _, denied := range []string{"/var/run/docker.sock", "/run/docker.sock", "/var/lib/incus/unix.socket", "/var/snap/lxd/common/lxd/unix.socket"} {
		if sameCleanPath(config.DockerSocket, denied) || sameCleanPath(config.IncusSocket, denied) {
			return fmt.Errorf("native ingress harness refuses default production Docker/Incus sockets")
		}
	}
	if strings.Contains(config.StateRoot, "..") || config.StateRoot == "/" {
		return fmt.Errorf("native ingress harness requires an explicit private state root")
	}
	if !strings.HasPrefix(filepath.Clean(config.TestMarker), filepath.Clean(config.StateRoot)+string(filepath.Separator)) {
		return fmt.Errorf("native ingress harness test marker must be inside the private state root")
	}
	info, err := os.Lstat(config.TestMarker)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("native ingress harness requires an existing private parent-created test marker")
	}
	return nil
}

func sameCleanPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
