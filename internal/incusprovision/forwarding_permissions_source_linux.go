//go:build linux

package incusprovision

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

// The caller holds the EXISTING host state lock before entering. Only fixed
// installation files select the workspace, artifact paths, daemon and keys.
// The session retains its workspace read lock through the last external effect.
func openInstalledForwardingLease(ctx context.Context, request ForwardingPermissionRequest, state State, expected *ForwardingLeaseGrant) (_ *forwardingLeaseSession, result error) {
	if ctx == nil || ctx.Err() != nil || os.Geteuid() != 0 || request.Validate() != nil || request.Operation != "enable" ||
		state.Schema != StateSchema || state.Disabled || state.Bundle == nil || verifyImagePruneHost(ctx, state) != nil {
		return nil, ErrBlocked
	}
	files := map[string][]byte{}
	var lock *pruneWorkspaceLock
	var reader *computeingressruntime.IncusFactReader
	closeAll := func() error {
		if reader != nil {
			reader.CloseIdleConnections()
		}
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
		if candidate.ID == request.WorkspaceID {
			if workspace != "" {
				return nil, ErrBlocked
			}
			workspace = candidate.Path
		}
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return nil, ErrBlocked
	}
	lock, err = openPruneWorkspaceLock(ctx, filepath.Join(workspace, ".anas", "state", "lock"))
	if err != nil {
		return nil, err
	}
	if _, err := read(DefaultBundlePath, 2<<20); err != nil {
		return nil, err
	}
	bundle, err := newFileStateStore().ReadBundle(ctx)
	if err != nil || bundle != *state.Bundle {
		return nil, ErrBlocked
	}
	snapshot, err := deployment.NewReader(workspace).ActiveComputeResources(ctx)
	if err != nil {
		return nil, ErrBlocked
	}
	resource, err := selectForwardingResource(snapshot, request)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(workspace, ".anas")
	for _, path := range []string{filepath.Join(base, "state", "active.yml"), filepath.Join(base, "state", "deployments", snapshot.Deployment+".yml")} {
		if _, err := read(path, 256<<10); err != nil {
			return nil, err
		}
	}
	manifest, err := read(filepath.Join(base, "deployments", snapshot.Deployment, "deployment.yml"), 4<<20)
	if err != nil || "sha256:"+digestBytes(manifest) != snapshot.ManifestDigest {
		return nil, ErrDrift
	}
	providerArtifact, err := forwardingArtifact(snapshot, resource.Provider)
	if err != nil {
		return nil, err
	}
	provider, err := read(filepath.Join(base, "deployments", providerArtifact, "modules", resource.Provider, ".env"), 2<<20)
	if err != nil {
		return nil, err
	}
	if local, err := pruneProviderMatchesBundle(provider, bundle); err != nil || !local {
		return nil, ErrBlocked
	}
	consumerArtifact, err := forwardingArtifact(snapshot, resource.Consumer)
	if err != nil {
		return nil, err
	}
	consumer, err := read(filepath.Join(base, "deployments", consumerArtifact, "modules", resource.Consumer, ".env"), 2<<20)
	if err != nil {
		return nil, err
	}
	scope, err := forwardingLeaseFromDelivery(snapshot, resource, consumer, bundle)
	if err != nil {
		return nil, err
	}
	observerConfig := computeingressruntime.IncusObserverConfig{Endpoint: "https://127.0.0.1:8443",
		ServerCertPEM: []byte(bundle.ServerCertificatePEM), ClientCertPEM: []byte(bundle.AdminCertificatePEM),
		ClientKeyPEM: []byte(bundle.AdminPrivateKeyPEM), LeaseScopes: []computeingressruntime.IncusLeaseObservationScope{scope}}
	version, err := computeingressruntime.ObservePinnedIncusVersion(ctx, observerConfig)
	if err != nil {
		return nil, ErrBlocked
	}
	observerConfig.ServerVersion = version
	reader, err = computeingressruntime.NewIncusFactReader(observerConfig)
	if err != nil {
		return nil, ErrBlocked
	}
	network, err := reader.ObserveLeaseNetwork(ctx, scope)
	if err != nil {
		return nil, ErrBlocked
	}
	proof, err := incusingresshost.ObserveForwardingBridge(ctx, network.BridgeName, network.BridgeCIDR)
	if err != nil {
		return nil, ErrBlocked
	}
	grant := ForwardingLeaseGrant{Schema: ForwardingPermissionSchema, WorkspaceID: request.WorkspaceID,
		WorkspaceDigest: snapshot.WorkspaceDigest, Epoch: snapshot.Epoch, OwnershipID: state.Ownership.ID,
		BundleDigest: stableDigest(bundle), ServerVersion: version, Lease: scope, Destinations: request.Destinations, Network: proof}
	for _, destination := range request.Destinations {
		route, err := incusingresshost.ObserveForwardingRoute(ctx, proof, destination.IPv4, destination.Port)
		if err != nil {
			return nil, ErrBlocked
		}
		grant.Routes = append(grant.Routes, route)
	}
	if grant.Validate() != nil || (expected != nil && !reflect.DeepEqual(grant, *expected)) {
		return nil, ErrDrift
	}
	stamps := map[string]string{}
	for path, body := range files {
		stamps[path] = digestBytes(body)
	}
	session := &forwardingLeaseSession{grant: grant, workspace: workspace, reader: reader, stamp: stableDigest(stamps), close: closeAll}
	session.check = func(checkCtx context.Context) error {
		if checkCtx == nil || checkCtx.Err() != nil || lock.check() != nil {
			return ErrDrift
		}
		for path, expectedBytes := range files {
			actual, err := readObservationFile(path, 4<<20)
			equal := err == nil && bytes.Equal(actual, expectedBytes)
			clear(actual)
			if !equal {
				return ErrDrift
			}
		}
		current, err := deployment.NewReader(workspace).ActiveComputeResources(checkCtx)
		if err != nil || current.Epoch != snapshot.Epoch || current.WorkspaceDigest != snapshot.WorkspaceDigest {
			return ErrDrift
		}
		return forwardingTargetsOutsideManagedNetworks(checkCtx, request.Destinations, bundle)
	}
	if session.check(ctx) != nil {
		return nil, ErrDrift
	}
	keep = true
	return session, nil
}

func forwardingTargetsOutsideManagedNetworks(ctx context.Context, destinations []ForwardingDestination, bundle ConnectionBundle) error {
	cidrs, err := newLocalRuntime().incus.listNetworkCIDRs(ctx)
	if err != nil || len(cidrs) > 1024 {
		return ErrBlocked
	}
	cidrs = append(cidrs, bundle.ControlSubnet)
	for _, raw := range cidrs {
		if strings.TrimSpace(raw) != raw {
			return ErrBlocked
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return ErrBlocked
		}
		for _, destination := range destinations {
			address, err := netip.ParseAddr(destination.IPv4)
			if err != nil || prefix.Contains(address) {
				return ErrBlocked
			}
		}
	}
	return nil
}
