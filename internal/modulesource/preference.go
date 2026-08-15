package modulesource

import (
	"os"
	"path/filepath"
	"strings"
)

// InstalledDefaultName resolves the source selected by the installer when a
// command or imported configuration did not choose one explicitly. Explicit
// configuration always wins, so workspaces remain portable after init writes
// the resolved value into their managed config.yml.
func InstalledDefaultName(raw string) string {
	if name := NormalizeName(raw); name != "" {
		return name
	}
	if name := NormalizeName(os.Getenv("ANAS_DEFAULT_SOURCE")); name != "" {
		return name
	}
	if body, err := os.ReadFile(installedSourcePath()); err == nil {
		if name := NormalizeName(string(body)); name != "" {
			return name
		}
	}
	return Official
}

func installedSourcePath() string {
	if path := strings.TrimSpace(os.Getenv("ANAS_SOURCE_CONFIG")); path != "" {
		return path
	}
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		return ""
	}
	return filepath.Join(configDir, "anas", "source")
}
