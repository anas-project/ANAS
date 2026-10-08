package runner

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type recoveryMount struct {
	Target   string          `json:"target"`
	Children []recoveryMount `json:"children"`
}

func validateRecoveryMounts(data string, payload []byte) error {
	var mounts struct {
		Filesystems []recoveryMount `json:"filesystems"`
	}
	if err := json.Unmarshal(payload, &mounts); err != nil {
		return fmt.Errorf("cannot inspect recovery mount coverage: %w", err)
	}
	if len(mounts.Filesystems) == 0 {
		return fmt.Errorf("recovery mount inventory is empty")
	}
	data = filepath.Clean(data)
	var check func([]recoveryMount) error
	check = func(entries []recoveryMount) error {
		for _, entry := range entries {
			if strings.HasPrefix(filepath.Clean(entry.Target), data+string(filepath.Separator)) {
				return fmt.Errorf("data contains an external mount %s which its Btrfs snapshot does not capture", entry.Target)
			}
			if err := check(entry.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return check(mounts.Filesystems)
}

func validateRecoverySymlinks(data string) error {
	return filepath.WalkDir(data, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("cannot qualify recovery symlink %s: %w", path, err)
			}
			if resolved != data && !strings.HasPrefix(resolved, data+string(filepath.Separator)) {
				return fmt.Errorf("data symlink %s escapes the ANAS recovery tree", path)
			}
		}
		return nil
	})
}

func (a *app) validatePostgresRecoveryData(workspace string) error {
	data := dataDir(workspace)
	mounts := externalCommandContext(a.subprocessContext(), "findmnt", "--json", "--submounts", "--target", data)
	mounts.Env = a.commandEnvironment(nil)
	payload, err := mounts.Output()
	if err != nil {
		return fmt.Errorf("cannot qualify data mount coverage: %w", err)
	}
	if err := validateRecoveryMounts(data, payload); err != nil {
		return err
	}
	nested := externalCommandContext(a.subprocessContext(), "btrfs", "subvolume", "list", "-o", data)
	nested.Env = a.commandEnvironment(nil)
	out, err := nested.Output()
	if err != nil {
		return fmt.Errorf("cannot qualify nested subvolume coverage: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("data contains nested Btrfs subvolumes; its parent snapshot does not capture them")
	}
	return validateRecoverySymlinks(data)
}
