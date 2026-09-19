//go:build linux

package incusingresshost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	installedScopeRoot = "/etc/anas/incus-ingress/scopes"
	trustedIPBinary    = "/usr/sbin/ip"
	trustedNFTBinary   = "/usr/sbin/nft"
	trustedConntrack   = "/usr/sbin/conntrack"
)

// NewLocalInstalledBackend is the production root-side constructor. The caller
// selects only an opaque installed scope id; all paths, binaries and topology
// pins are read from the fixed root-owned installation root. HTTP publishing is
// fail-closed until the installed config advertises the still-unimplemented
// durable allocation, namespace executor and health identity contracts.
func NewLocalInstalledBackend(ctx context.Context, scopeID string, resolver Resolver) (*Backend, error) {
	if resolver == nil || !scopeName.MatchString(scopeID) {
		return nil, fmt.Errorf("invalid installed Incus ingress scope")
	}
	scope, err := readInstalledScope(scopeID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := backendConfig{
		ScopeName: scope.ScopeName, ReceiptDir: scope.ReceiptDir, RouteNetNS: scope.RouteNetNS,
		RouteTable: scope.RouteTable, RouteProtocol: scope.RouteProtocol,
		PermitTable: scope.PermitTable, OriginTable: scope.OriginTable,
		GuestBridge: scope.GuestBridge, IngressBridge: scope.IngressBridge, TraefikVeth: scope.TraefikVeth,
		TraefikInterface: scope.TraefikInterface, TraefikSourceIP: scope.TraefikSourceIP,
		IngressGateway: scope.IngressGateway, GuestSubnet: scope.GuestSubnet, Namespace: scope.Namespace,
		AddressRouting:           scope.AddressRouting,
		PermitTTL:                time.Duration(scope.PermitTTLSeconds) * time.Second,
		CommandTimeout:           time.Duration(scope.CommandTimeoutMS) * time.Millisecond,
		Binaries:                 trustedBinaries{IP: trustedIPBinary, NFT: trustedNFTBinary, Conntrack: trustedConntrack},
		Resolver:                 resolver,
		productionDisabledReason: productionGateReason(scope),
	}
	return newBackend(config)
}

func readInstalledScope(scopeID string) (out InstalledScope, result error) {
	path := filepath.Join(installedScopeRoot, scopeID+".json")
	if filepath.Dir(path) != installedScopeRoot {
		return InstalledScope{}, fmt.Errorf("invalid installed Incus ingress scope")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return InstalledScope{}, fmt.Errorf("read installed Incus ingress scope")
	}
	rootInfo, err := os.Lstat(installedScopeRoot)
	if err != nil {
		return InstalledScope{}, fmt.Errorf("inspect installed Incus ingress scope root")
	}
	if err := validateTrustedDirectory(installedScopeRoot, rootInfo); err != nil {
		return InstalledScope{}, err
	}
	if err := validateRootOwnedImmutable(path, info, false); err != nil {
		return InstalledScope{}, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return InstalledScope{}, fmt.Errorf("open installed Incus ingress scope")
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || validateRootOwnedImmutable(path, opened, false) != nil {
		return InstalledScope{}, fmt.Errorf("installed Incus ingress scope changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(body) == 0 || len(body) > 65536 {
		return InstalledScope{}, fmt.Errorf("installed Incus ingress scope is unreadable or oversized")
	}
	var scope InstalledScope
	if err := decodeObservedJSON(body, &scope); err != nil {
		return InstalledScope{}, fmt.Errorf("installed Incus ingress scope JSON is invalid")
	}
	after, err := os.Lstat(path)
	rootAfter, rootErr := os.Lstat(installedScopeRoot)
	if err != nil || rootErr != nil || !os.SameFile(opened, after) || !os.SameFile(rootInfo, rootAfter) ||
		after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || validateRootOwnedImmutable(path, after, false) != nil {
		return InstalledScope{}, fmt.Errorf("installed Incus ingress scope changed while reading")
	}
	if scope.Schema != installedScopeSchema || scope.ScopeID != scopeID || scope.PublicationEnabled {
		return InstalledScope{}, fmt.Errorf("installed Incus ingress scope is not enabled for production publishing")
	}
	return scope, nil
}

func productionGateReason(scope InstalledScope) string {
	if !scope.PublicationEnabled {
		return "installed scope has not enabled production HTTP publishing"
	}
	if scope.ProductionGate.AllocationContract != "durable-incus-dhcp-or-veth-incarnation-v1" {
		return "durable guest address allocation contract is not installed"
	}
	if scope.ProductionGate.NamespaceExecutor != "opened-netns-fd-v1" {
		return "opened namespace execution contract is not installed"
	}
	if scope.ProductionGate.NFTBaseline != "anas-owned-nft-json-v1" {
		return "owned nft baseline contract is not installed"
	}
	if scope.ProductionGate.ProbeIdentity != "frozen-app-health-identity-v1" {
		return "trusted application health identity is not installed"
	}
	return ""
}
