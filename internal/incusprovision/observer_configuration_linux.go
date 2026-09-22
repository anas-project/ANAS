//go:build linux

package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"gopkg.in/yaml.v3"
)

func openInstalledObserverConfiguration(ctx context.Context, req ObserverConfigurationRequest, write bool) (_ *observerConfigurationSession, result error) {
	if ctx == nil || ctx.Err() != nil || os.Geteuid() != 0 || req.Validate() != nil {
		return nil, ErrBlocked
	}
	files := map[string][]byte{}
	var locks []*pruneWorkspaceLock
	var directory *os.Root
	closeAll := func() error {
		var err error
		if directory != nil {
			err = errors.Join(err, directory.Close())
		}
		for i := len(locks) - 1; i >= 0; i-- {
			err = errors.Join(err, locks[i].close())
		}
		for _, body := range files {
			clear(body)
		}
		return err
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
	lock, err := openExistingObservationLock(ctx, DefaultStatePath+".lock", write)
	if err != nil {
		return nil, err
	}
	locks = append(locks, lock)
	if _, err := read(ServiceConfigPath, 1<<20); err != nil {
		return nil, err
	}
	config, err := loadImagePruneServiceConfig()
	if err != nil || !config.HostActions {
		return nil, ErrBlocked
	}
	workspace := ""
	for _, w := range config.Workspaces {
		if w.ID == req.WorkspaceID {
			if workspace != "" {
				return nil, ErrBlocked
			}
			workspace = w.Path
		}
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return nil, ErrBlocked
	}
	store := newFileStateStore()
	state, err := store.Load(ctx)
	if err != nil || state.Schema != StateSchema || unrecoveredPendingIntent(state) != "" {
		return nil, ErrBlocked
	}
	expectedState := state.digest()
	s := &observerConfigurationSession{state: state, close: closeAll}
	var versionConfig computeingressruntime.IncusObserverConfig
	if req.Operation == "refresh" {
		lock, err := openPruneWorkspaceLock(ctx, filepath.Join(workspace, ".anas", "state", "lock"))
		if err != nil {
			return nil, err
		}
		locks = append(locks, lock)
		if _, err := read(DefaultBundlePath, 2<<20); err != nil {
			return nil, err
		}
		bundle, err := store.ReadBundle(ctx)
		if err != nil || state.Bundle == nil || bundle != *state.Bundle || verifyImagePruneHost(ctx, state) != nil {
			return nil, ErrBlocked
		}
		snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
		if err != nil || len(snapshot.Authorizations) == 0 {
			return nil, ErrBlocked
		}
		base := filepath.Join(workspace, ".anas")
		for _, path := range []string{filepath.Join(base, "state", "active.yml"), filepath.Join(base, "state", "deployments", snapshot.Deployment+".yml")} {
			if _, err := read(path, 256<<10); err != nil {
				return nil, err
			}
		}
		manifestBody, err := read(filepath.Join(base, "deployments", snapshot.Deployment, "deployment.yml"), 4<<20)
		if err != nil {
			return nil, err
		}
		var manifest deployment.Manifest
		if yaml.Unmarshal(manifestBody, &manifest) != nil || manifest.ID != snapshot.Deployment {
			return nil, ErrBlocked
		}
		providers := map[string]bool{}
		for _, grant := range snapshot.Authorizations {
			if providers[grant.Provider] {
				continue
			}
			provider, ok := manifest.Modules[grant.Provider]
			if !ok {
				return nil, ErrBlocked
			}
			artifact := provider.ArtifactDeployment
			if artifact == "" {
				artifact = snapshot.Deployment
			}
			if deployment.ValidateID(artifact) != nil {
				return nil, ErrBlocked
			}
			env, err := read(filepath.Join(base, "deployments", artifact, "modules", grant.Provider, ".env"), 2<<20)
			if err != nil {
				return nil, err
			}
			local, err := pruneProviderMatchesBundle(env, bundle)
			if err != nil || !local {
				return nil, ErrBlocked
			}
			providers[grant.Provider] = true
		}
		versionConfig = computeingressruntime.IncusObserverConfig{Endpoint: "https://127.0.0.1:8443", ServerCertPEM: []byte(bundle.ServerCertificatePEM),
			ClientCertPEM: []byte(bundle.AdminCertificatePEM), ClientKeyPEM: []byte(bundle.AdminPrivateKeyPEM), Authorizations: snapshot.Authorizations}
		version, err := computeingressruntime.ObservePinnedIncusVersion(ctx, versionConfig)
		if err != nil {
			return nil, ErrBlocked
		}
		s.desired = &IngressObservationScope{Schema: IngressObservationScopeSchema, ScopeID: req.WorkspaceID, OwnershipID: state.Ownership.ID,
			BundleDigest: stableDigest(bundle), ServerVersion: version, Snapshot: snapshot}
		if validateObserverDocument(*s.desired) != nil {
			return nil, ErrBlocked
		}
	}
	stamps := map[string]string{}
	for path, body := range files {
		stamps[path] = digestBytes(body)
	}
	s.stamp = stableDigest(stamps)
	s.check = func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, lock := range locks {
			if lock.check() != nil {
				return ErrDrift
			}
		}
		for path, expected := range files {
			actual, err := readObservationFile(path, 4<<20)
			equal := err == nil && bytes.Equal(actual, expected)
			clear(actual)
			if !equal {
				return ErrDrift
			}
		}
		current, err := store.Load(ctx)
		if err != nil || current.digest() != expectedState {
			return ErrDrift
		}
		if s.desired != nil {
			current, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
			if err != nil || !reflect.DeepEqual(current, s.desired.Snapshot) {
				return ErrDrift
			}
		}
		return nil
	}
	name := req.WorkspaceID + ".json"
	var identity os.FileInfo
	openDirectory := func(create bool) error {
		if directory != nil {
			return nil
		}
		if create && ensureTrustedRootDirectory(ingressObservationRoot, 0700) != nil {
			return ErrUnsafeState
		}
		info, err := os.Lstat(ingressObservationRoot)
		if errors.Is(err, os.ErrNotExist) && !create {
			return nil
		}
		if err != nil || !rootOwnedPrivateDir(info) || trustedAncestors(filepath.Join(ingressObservationRoot, name)) != nil {
			return ErrUnsafeState
		}
		root, err := os.OpenRoot(ingressObservationRoot)
		if err != nil {
			return ErrUnsafeState
		}
		opened, err := root.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			root.Close()
			return ErrUnsafeState
		}
		directory, identity = root, info
		return nil
	}
	checkDirectory := func(ctx context.Context) error {
		if s.check(ctx) != nil {
			return ErrDrift
		}
		info, err := os.Lstat(ingressObservationRoot)
		if err != nil || identity == nil || !os.SameFile(identity, info) || !rootOwnedPrivateDir(info) || trustedAncestors(filepath.Join(ingressObservationRoot, name)) != nil {
			return ErrDrift
		}
		return nil
	}
	s.read = func(ctx context.Context) ([]byte, error) {
		if s.check(ctx) != nil || openDirectory(false) != nil {
			return nil, ErrDrift
		}
		if directory == nil {
			return nil, nil
		}
		if checkDirectory(ctx) != nil {
			return nil, ErrDrift
		}
		return readObserverScopeAt(directory, name)
	}
	s.replace = func(ctx context.Context, before, desired []byte) error {
		if !write {
			return ErrBlocked
		}
		if len(before) == 0 && len(desired) == 0 {
			return s.check(ctx)
		}
		if openDirectory(true) != nil {
			return ErrUnsafeState
		}
		return replaceObserverScopeAt(ctx, directory, name, before, desired, checkDirectory)
	}
	s.save = func(ctx context.Context, next State) error {
		if !write || s.check(ctx) != nil {
			return ErrDrift
		}
		if s.desired != nil {
			version, err := computeingressruntime.ObservePinnedIncusVersion(ctx, versionConfig)
			if err != nil || version != s.desired.ServerVersion {
				return ErrDrift
			}
		}
		if err := store.Save(ctx, next); err != nil {
			return err
		}
		current, err := store.Load(ctx)
		if err != nil {
			return err
		}
		compare := current
		compare.UpdatedAt, next.UpdatedAt = time.Time{}, time.Time{}
		a, _ := json.Marshal(compare)
		b, _ := json.Marshal(next)
		if !bytes.Equal(a, b) {
			return ErrDrift
		}
		expectedState = current.digest()
		return nil
	}
	if s.check(ctx) != nil {
		return nil, ErrDrift
	}
	keep = true
	return s, nil
}
