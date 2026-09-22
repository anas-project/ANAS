//go:build linux

package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
	"gopkg.in/yaml.v3"
)

const ingressObservationRoot = "/etc/anas/incus-ingress/observers"

func openInstalledIngressObservation(ctx context.Context, req incusingresshost.ProjectionRequest) (_ *ingressObservationSession, result error) {
	if ctx == nil || os.Geteuid() != 0 || req.Validate() != nil {
		return nil, ErrBlocked
	}
	// All paths originate in fixed installation files. This read-only action
	// never creates state, a lock file, a scope, a credential or a service.
	files := map[string][]byte{}
	read := func(path string, limit int64) ([]byte, error) {
		body, err := readObservationFile(path, limit)
		if err == nil {
			files[path] = body
		}
		return body, err
	}
	body, err := read(filepath.Join(ingressObservationRoot, req.ScopeID+".json"), 1<<20)
	if err != nil {
		return nil, ErrBlocked
	}
	var scope IngressObservationScope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&scope) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrBlocked
	}
	canonical, err := json.Marshal(scope)
	if err != nil || !bytes.Equal(bytes.TrimSpace(body), canonical) {
		return nil, ErrBlocked
	}
	grant, err := validateObservationScope(scope, req)
	if err != nil {
		return nil, err
	}
	if _, err := read(ServiceConfigPath, 1<<20); err != nil {
		return nil, err
	}
	config, err := loadImagePruneServiceConfig()
	if err != nil || !config.HostActions {
		return nil, ErrBlocked
	}
	workspace := ""
	for _, w := range config.Workspaces {
		if w.ID == req.ScopeID {
			if workspace != "" {
				return nil, ErrBlocked
			}
			workspace = w.Path
		}
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || deployment.ValidateID(req.Deployment) != nil {
		return nil, ErrBlocked
	}
	var locks []*pruneWorkspaceLock
	closeAll := func() error {
		var err error
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
	// Same lock order as host prune: host state, then workspace. Both are
	// existing shared locks, so no installer or apply mutation can overlap.
	for _, path := range []string{DefaultStatePath + ".lock", filepath.Join(workspace, ".anas", "state", "lock")} {
		lock, err := openPruneWorkspaceLock(ctx, path)
		if err != nil {
			return nil, err
		}
		locks = append(locks, lock)
	}
	for _, path := range []string{DefaultStatePath, DefaultBundlePath} {
		if _, err := read(path, 2<<20); err != nil {
			return nil, err
		}
	}
	store := newFileStateStore()
	state, err := store.Load(ctx)
	if err != nil || unrecoveredPendingIntent(state) != "" || state.Bundle == nil || state.Ownership.ID != scope.OwnershipID ||
		stableDigest(*state.Bundle) != scope.BundleDigest || !observerScopeEnabled(state, req.ScopeID, canonical) {
		return nil, ErrBlocked
	}
	bundle, err := store.ReadBundle(ctx)
	if err != nil || bundle != *state.Bundle || verifyImagePruneHost(ctx, state) != nil {
		return nil, ErrBlocked
	}
	base := filepath.Join(workspace, ".anas")
	for _, path := range []string{filepath.Join(base, "state", "active.yml"), filepath.Join(base, "state", "deployments", req.Deployment+".yml")} {
		if _, err := read(path, 256<<10); err != nil {
			return nil, err
		}
	}
	manifestBody, err := read(filepath.Join(base, "deployments", req.Deployment, "deployment.yml"), 4<<20)
	if err != nil {
		return nil, err
	}
	var manifest deployment.Manifest
	if yaml.Unmarshal(manifestBody, &manifest) != nil || manifest.ID != req.Deployment {
		return nil, ErrBlocked
	}
	provider, ok := manifest.Modules[grant.Provider]
	if !ok {
		return nil, ErrBlocked
	}
	artifact := provider.ArtifactDeployment
	if artifact == "" {
		artifact = req.Deployment
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
	check := func(ctx context.Context) error {
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
			matches := err == nil && bytes.Equal(actual, expected)
			clear(actual)
			if !matches {
				return ErrDrift
			}
		}
		snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
		if err != nil || !reflect.DeepEqual(snapshot, scope.Snapshot) {
			return ErrDrift
		}
		return ctx.Err()
	}
	if check(ctx) != nil {
		return nil, ErrDrift
	}
	reader, err := computeingressruntime.NewIncusFactReader(computeingressruntime.IncusObserverConfig{
		Endpoint: "https://127.0.0.1:8443", ServerCertPEM: []byte(bundle.ServerCertificatePEM),
		ClientCertPEM: []byte(bundle.AdminCertificatePEM), ClientKeyPEM: []byte(bundle.AdminPrivateKeyPEM),
		ServerVersion: scope.ServerVersion, Authorizations: scope.Snapshot.Authorizations,
	})
	if err != nil {
		return nil, ErrBlocked
	}
	keep = true
	return &ingressObservationSession{scope: scope, grant: grant, observe: reader.ObserveHostHTTP, check: check,
		close: func() error { reader.CloseIdleConnections(); return closeAll() }}, nil
}

// Descriptor-pinned, nonblocking reads refuse links, FIFOs, shared writes and
// path replacement. A root administrator is trusted; consumer-owned ancestors
// are not. The caller rechecks captured bytes around every external observation.
func readObservationFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || limit <= 0 || limit > 4<<20 || trustedAncestors(path) != nil {
		return nil, ErrUnsafeState
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || !rootOwnedExecutable(before) || before.Mode().Perm()&0022 != 0 || before.Size() <= 0 || before.Size() > limit {
		return nil, ErrUnsafeState
	}
	file, err := openRootFileNoFollow(path)
	if err != nil {
		return nil, ErrUnsafeState
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameFileIdentity(before, opened) {
		return nil, ErrUnsafeState
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	after, e1 := file.Stat()
	linked, e2 := os.Lstat(path)
	if err != nil || e1 != nil || e2 != nil || int64(len(body)) != before.Size() || !sameFileIdentity(before, after) ||
		!sameFileIdentity(before, linked) || trustedAncestors(path) != nil {
		clear(body)
		return nil, ErrUnsafeState
	}
	return body, nil
}
