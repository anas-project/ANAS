//go:build linux

package incusprovision

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"gopkg.in/yaml.v3"
)

// Invoked under the existing host-state lock. Unlike enable, this does not
// require the old credential to authenticate or an active running deployment.
// It requires the SAME registered deployment to be stopped, the lease's
// certificate to be revoked, no alternate credential with its authority, no
// instances/operations and no physical bridge ports. No cleanup is implicit.
func openInstalledForwardingRetirement(ctx context.Context, state State, record ForwardingPermissionRecord) (_ *forwardingRetirementSession, result error) {
	if ctx == nil || ctx.Err() != nil || os.Geteuid() != 0 || !forwardingRetirementReady(record) ||
		state.Bundle == nil || state.Ownership.ID != record.Grant.OwnershipID ||
		stableDigest(*state.Bundle) != record.Grant.BundleDigest || unrecoveredProvisionIntent(state) != "" ||
		verifyImagePruneHost(ctx, state) != nil {
		return nil, ErrBlocked
	}
	files := map[string][]byte{}
	var lock *pruneWorkspaceLock
	closeAll := func() error {
		for _, body := range files {
			clear(body)
		}
		if lock != nil {
			return lock.close()
		}
		return nil
	}
	keep := false
	defer func() {
		if !keep {
			result = errors.Join(result, closeAll())
		}
	}()
	read := func(path string, limit int64) ([]byte, error) {
		body, err := readObservationFile(path, limit)
		if err == nil {
			files[path] = body
		}
		return body, err
	}
	if _, err := read(ServiceConfigPath, 1<<20); err != nil {
		return nil, err
	}
	config, err := loadImagePruneServiceConfig()
	if err != nil || !config.HostActions {
		return nil, ErrBlocked
	}
	workspace := ""
	for _, candidate := range config.Workspaces {
		if candidate.ID == record.Grant.WorkspaceID {
			if workspace != "" {
				return nil, ErrBlocked
			}
			workspace = candidate.Path
		}
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace ||
		"sha256:"+digestBytes([]byte(workspace)) != record.Grant.WorkspaceDigest {
		return nil, ErrBlocked
	}
	lock, err = openPruneWorkspaceLock(ctx, filepath.Join(workspace, ".anas", "state", "lock"))
	if err != nil {
		return nil, err
	}
	if lock.info.Size() != 0 {
		// A retained runtime publisher marker must drain through its own
		// controller. Stopped metadata alone cannot clear that journal fence.
		return nil, ErrBlocked
	}
	body, err := read(filepath.Join(workspace, ".anas", "state", "active.yml"), 256<<10)
	var active deployment.ActiveState
	if err != nil || yaml.Unmarshal(body, &active) != nil || active.APIVersion != deployment.StateAPIVersion ||
		active.ActiveDeployment != record.Grant.Lease.Deployment || active.RuntimeStatus != "stopped" || active.Transaction != "" {
		return nil, ErrBlocked
	}
	if _, err := read(DefaultBundlePath, 2<<20); err != nil {
		return nil, err
	}
	bundle, err := newFileStateStore().ReadBundle(ctx)
	if err != nil || bundle != *state.Bundle {
		return nil, ErrBlocked
	}
	client := &incusUnixClient{socket: incusUnixSocket}
	observe := func(checkCtx context.Context) (string, error) {
		if checkCtx == nil || checkCtx.Err() != nil || lock.check() != nil || verifyImagePruneHost(checkCtx, state) != nil {
			return "", ErrDrift
		}
		for path, expected := range files {
			body, err := readObservationFile(path, 2<<20)
			equal := err == nil && bytes.Equal(expected, body)
			clear(body)
			if !equal {
				return "", ErrDrift
			}
		}
		first, err := observeForwardingRetirement(checkCtx, client, record.Grant, bundle.ManagementFingerprint)
		if err != nil || incusingresshost.CheckForwardingBridgeDrained(checkCtx, record.Grant.Network) != nil {
			return "", ErrBlocked
		}
		second, err := observeForwardingRetirement(checkCtx, client, record.Grant, bundle.ManagementFingerprint)
		if err != nil || first != second {
			return "", ErrDrift
		}
		return first, checkCtx.Err()
	}
	observation, err := observe(ctx)
	if err != nil {
		return nil, err
	}
	stamps := map[string]string{}
	for path, body := range files {
		stamps[path] = digestBytes(body)
	}
	session := &forwardingRetirementSession{stamp: stableDigest([]any{stamps, observation}), close: closeAll}
	session.check = func(checkCtx context.Context) error {
		current, err := observe(checkCtx)
		if err != nil || current != observation {
			return ErrDrift
		}
		return checkCtx.Err()
	}
	keep = true
	return session, nil
}
