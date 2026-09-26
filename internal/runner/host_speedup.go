package runner

import (
	"context"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
)

// WorkspaceChineseSpeedup resolves the managed desired configuration once for
// an Incus install plan. The frozen boolean crosses the host-action boundary;
// config values and the daemon's environment never reach the privileged APT call.
func WorkspaceChineseSpeedup(ctx context.Context, workspace string) (bool, error) {
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(workspace))
	if err != nil {
		return false, err
	}
	defer unlock()
	snapshot, err := readManagedConfigSnapshot(workspace)
	if err != nil {
		return false, err
	}
	if !snapshot.managed {
		return false, nil
	}
	cfg, err := config.Parse(snapshot.configBytes)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(cfg.BaseEnv()["CHINESE_SPEEDUP"]), "true"), nil
}
