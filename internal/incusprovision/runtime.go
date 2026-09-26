package incusprovision

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/incushost"
)

const (
	aptGetPath      = "/usr/bin/apt-get"
	systemctlPath   = "/usr/bin/systemctl"
	useraddPath     = "/usr/sbin/useradd"
	nftPath         = "/usr/sbin/nft"
	dpkgQueryPath   = "/usr/bin/dpkg-query"
	aptDir          = "/etc/anas/incus-apt"
	aptSourcesDir   = aptDir + "/sources.list.d"
	aptPrefsDir     = aptDir + "/preferences.d"
	incusUnixSocket = "/var/lib/incus/unix.socket"
	incusServerCert = "/var/lib/incus/server.crt"
	// /var/run is commonly a symlink. Pin the canonical runtime directory
	// rather than weakening the no-symlink ancestor rule for all root I/O.
	dockerUnixSocket = "/run/docker.sock"
)

type APTConfigFile struct {
	Path string
	Body []byte
	Mode os.FileMode
}

type localRuntime struct {
	commands fixedCommands
	incus    *incusUnixClient
	docker   *dockerClient
}

func newLocalRuntime() *localRuntime {
	return &localRuntime{commands: fixedCommands{}, incus: &incusUnixClient{socket: incusUnixSocket}, docker: &dockerClient{socket: dockerUnixSocket}}
}

func (r *localRuntime) Observe(ctx context.Context, request Request, state State) (Observation, error) {
	preflight, err := incushost.LocalPreflight(ctx, incushost.Options{Interface: request.Interface, Skip: request.Skip})
	if err != nil {
		return Observation{}, err
	}
	obs := Observation{Preflight: preflight}
	if request.Skip || preflight.Recipe == nil || len(provisioningHardBlockers(preflight.Blockers)) > 0 {
		return obs, nil
	}
	if preflight.Recipe != nil {
		existing, installed, err := r.observePackages(ctx, *preflight.Recipe, preflight.Facts.Architecture)
		if err != nil {
			return Observation{}, err
		}
		obs.ExistingPackages, obs.InstalledPackages = existing, installed
		obs.PackageInstalled = len(installed) == len(preflight.Recipe.Packages)
	}
	active, err := r.commands.systemctlIsActive(ctx, "incus.service")
	if err != nil && !errors.Is(err, errCommandInactive) {
		return Observation{}, err
	}
	obs.IncusDaemonActive = active
	if obs.IncusDaemonActive {
		server, err := r.incus.getServer(ctx)
		if err != nil {
			return Observation{}, err
		}
		obs.IncusHTTPSLoopback = server.Config["core.https_address"] == IncusHTTPSAddress
		pool, err := r.incus.getStoragePool(ctx, StoragePoolName)
		if errors.Is(err, errIncusNotFound) {
			obs.StoragePoolExists = false
		} else if err != nil {
			return Observation{}, err
		} else if storagePoolOwned(pool, state.Ownership) {
			obs.StoragePoolExists = true
		} else {
			return Observation{}, ErrBlocked
		}
		incusCIDRs, err := r.incus.listNetworkCIDRs(ctx)
		if err != nil {
			return Observation{}, err
		}
		obs.IncusCIDRs = incusCIDRs
	}
	if state.Ownership.ManagementTrust != "" {
		obs.ManagementTrusted, err = r.ReadBackManagementCertificate(ctx, state.Ownership.ManagementTrust)
		if err != nil {
			return Observation{}, err
		}
	}
	network, err := r.docker.inspectNetwork(ctx, ControlNetworkName)
	if errors.Is(err, errIncusNotFound) {
		obs.DockerNetworkExists = false
	} else if err != nil {
		return Observation{}, err
	} else if network.ownedBy(state.Ownership.ID) {
		obs.DockerNetworkExists = true
		obs.ControlNetworkID = network.ID
		obs.ControlInterfaceName = network.Options["com.docker.network.bridge.name"]
		obs.ControlInterfaceIndex = interfaceIndex(obs.ControlInterfaceName)
		for _, cfg := range network.IPAM.Config {
			if cfg.Subnet != "" {
				obs.ControlSubnet = cfg.Subnet
				obs.ControlGateway = cfg.Gateway
			}
		}
	} else {
		return Observation{}, ErrBlocked
	}
	dockerCIDRs, err := r.docker.listNetworkCIDRs(ctx)
	if err != nil {
		return Observation{}, err
	}
	obs.DockerCIDRs = dockerCIDRs
	hostCIDRs, err := hostInterfaceCIDRs()
	if err != nil {
		return Observation{}, err
	}
	obs.ExternalCIDRs = append(obs.ExternalCIDRs, hostCIDRs...)
	obs.ExternalCIDRs = append(obs.ExternalCIDRs, obs.DockerCIDRs...)
	obs.ExternalCIDRs = append(obs.ExternalCIDRs, obs.IncusCIDRs...)
	relayActive, relayErr := r.commands.systemctlIsActive(ctx, RelayServiceName)
	if _, err := os.Stat(DefaultRelayConfigPath); err == nil && relayActive {
		obs.RelayInstalled = true
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Observation{}, err
	} else if relayErr != nil && !errors.Is(relayErr, errCommandInactive) {
		return Observation{}, relayErr
	}
	obs.RelayBinaryInstalled = executableUsable(RelayBinaryPath) == nil
	if state.Ownership.FirewallRules {
		networkPlan, _, err := controlPlans(request, obs, state)
		if err != nil {
			return Observation{}, err
		}
		installed, err := r.controlFirewallInstalled(ctx, networkPlan)
		if err != nil {
			return Observation{}, err
		}
		obs.FirewallInstalled = installed
	}
	if u, err := user.Lookup(RelayUserName); err == nil {
		if id, parseErr := parseUint32(u.Uid); parseErr == nil {
			obs.RelayUID = id
		}
	}
	if g, err := user.LookupGroup(RelayGroupName); err == nil {
		if id, parseErr := parseUint32(g.Gid); parseErr == nil {
			obs.RelayGID = id
		}
	}
	if state.Bundle != nil && state.Credential != nil {
		if err := r.VerifyManagementEndpoint(ctx, *state.Bundle); err == nil {
			obs.EndpointVerified = true
		} else if !errors.Is(err, ErrBlocked) {
			return Observation{}, err
		}
	}
	obs.RunningManagedGuests, err = r.ListRunningManagedGuests(ctx, state.Ownership)
	if err != nil {
		return Observation{}, err
	}
	obs.Forwarding, err = r.observeForwarding(ctx)
	if err != nil {
		return Observation{}, err
	}
	return obs, nil
}

func (r *localRuntime) InstallPackages(ctx context.Context, recipe incushost.Recipe, packages []string, chineseSpeedup bool) error {
	if recipe.PackageManager != "apt" || recipe.Repository != incushost.IncusRepository || !validPackageSubset(recipe, packages) {
		return ErrInvalid
	}
	// This runs inside the already-audited install.packages effect, after the
	// host executor has claimed its approval. A fresh installation must not
	// depend on the operator manually copying the private APT configuration.
	if _, err := WriteAPTConfigFiles("/", recipe, chineseSpeedup); err != nil {
		return err
	}
	if err := verifyAPTPolicyFiles(recipe, chineseSpeedup); err != nil {
		return err
	}
	configPath := aptConfigPathForRecipe(recipe)
	if err := ensureAPTWorkDirs(); err != nil {
		return err
	}
	if err := r.commands.run(ctx, fixedAPT, []string{"-c", configPath, "update"}, isolatedAPTEnv(configPath)); err != nil {
		return err
	}
	args := append([]string{"-c", configPath, "install", "--yes", "--no-install-recommends", "--no-remove", "--no-upgrade"}, packages...)
	return r.commands.run(ctx, fixedAPT, args, isolatedAPTEnv(configPath))
}

func (r *localRuntime) EnableIncus(ctx context.Context) error {
	return r.commands.run(ctx, fixedSystemctl, []string{"enable", "--now", "incus.service"}, nil)
}

func (r *localRuntime) StopIncus(ctx context.Context) error {
	active, err := r.commands.systemctlIsActive(ctx, "incus.service")
	if err != nil && !errors.Is(err, errCommandInactive) {
		return err
	}
	if !active {
		return ctx.Err()
	}
	return r.commands.run(ctx, fixedSystemctl, []string{"stop", "incus.service"}, nil)
}

func (r *localRuntime) ConfigureIncusHTTPS(ctx context.Context) error {
	return r.incus.patchServer(ctx, map[string]string{"core.https_address": IncusHTTPSAddress})
}

func (r *localRuntime) EnsureStoragePool(ctx context.Context, sizeGiB int, ownershipID string) error {
	if ownershipID == "" {
		return ErrInvalid
	}
	expected := Ownership{ID: ownershipID, StoragePool: StoragePoolName, StoragePoolDriver: "btrfs"}
	if pool, err := r.incus.getStoragePool(ctx, StoragePoolName); err == nil {
		if storagePoolOwned(pool, expected) {
			return nil
		}
		return ErrBlocked
	} else if !errors.Is(err, errIncusNotFound) {
		return err
	}
	if err := r.incus.createStoragePool(ctx, StoragePoolName, "btrfs", map[string]string{
		"size": fmt.Sprintf("%dGiB", sizeGiB), "user.anas.owner": "incus-host-provision", "user.anas.owner_id": ownershipID,
	}); err != nil {
		return err
	}
	pool, err := r.incus.getStoragePool(ctx, StoragePoolName)
	if err != nil {
		return err
	}
	if !storagePoolOwned(pool, expected) {
		return ErrExternalEffects
	}
	return nil
}

func (r *localRuntime) EnsureRelayIdentity(ctx context.Context) (uint32, uint32, error) {
	uid, gid, ok := lookupUserGroupIDs(RelayUserName, RelayGroupName)
	if ok {
		return uid, gid, nil
	}
	if err := r.commands.run(ctx, fixedUseradd, []string{"--system", "--user-group", "--no-create-home", "--shell", "/usr/sbin/nologin", RelayUserName}, nil); err != nil {
		return 0, 0, err
	}
	uid, gid, ok = lookupUserGroupIDs(RelayUserName, RelayGroupName)
	if !ok || uid == 0 || gid == 0 {
		return 0, 0, ErrExternalEffects
	}
	return uid, gid, nil
}

func (r *localRuntime) EnsureDockerControlNetwork(ctx context.Context, plan ControlNetworkPlan) error {
	if plan.OwnershipID == "" {
		return ErrInvalid
	}
	if n, err := r.docker.inspectNetwork(ctx, plan.Name); err == nil {
		if !n.ownedBy(plan.OwnershipID) {
			return ErrBlocked
		}
		return nil
	} else if !errors.Is(err, errIncusNotFound) {
		return err
	}
	return r.docker.createControlNetwork(ctx, plan)
}

func (r *localRuntime) ApplyControlFirewall(ctx context.Context, plan ControlNetworkPlan) error {
	rules := nftControlRules(plan)
	if err := r.commands.runWithInput(ctx, fixedNFT, []string{"-c", "-f", "-"}, nil, []byte(rules)); err != nil {
		return err
	}
	if err := r.commands.runWithInput(ctx, fixedNFT, []string{"-f", "-"}, nil, []byte(rules)); err != nil {
		return err
	}
	installed, err := r.controlFirewallInstalled(ctx, plan)
	if err != nil {
		return err
	}
	if !installed {
		return ErrExternalEffects
	}
	return nil
}

func (r *localRuntime) InstallRelayConfig(ctx context.Context, plan RelayInstallPlan) error {
	if plan.RunUID == 0 || plan.RunGID == 0 || plan.InterfaceName == "" || plan.InterfaceIndex <= 0 {
		return ErrInvalid
	}
	if executableUsable(RelayBinaryPath) != nil {
		return ErrBlocked
	}
	config := map[string]any{
		"schema_version": 1, "listen_address": plan.ListenAddress, "control_subnet": plan.ControlSubnet,
		"interface_name": plan.InterfaceName, "interface_index": plan.InterfaceIndex,
		"run_uid": plan.RunUID, "run_gid": plan.RunGID, "max_connections": plan.MaxConnections,
		"idle_timeout_seconds": plan.IdleTimeoutSecs,
	}
	body, err := json.Marshal(config)
	if err != nil {
		return ErrInvalid
	}
	if err := writeRootOnlyFile(DefaultRelayConfigPath, append(body, '\n'), 0644); err != nil {
		return err
	}
	return nil
}

func (r *localRuntime) EnableRelay(ctx context.Context) error {
	return r.commands.run(ctx, fixedSystemctl, []string{"enable", "--now", RelayServiceName}, nil)
}

func (r *localRuntime) TrustManagementCertificate(ctx context.Context, credential Credential) error {
	return r.incus.addCertificate(ctx, credential)
}

func (r *localRuntime) ReadBackManagementCertificate(ctx context.Context, fingerprint string) (bool, error) {
	if !digestPattern.MatchString(fingerprint) {
		return false, ErrInvalid
	}
	certificate, err := r.incus.getCertificate(ctx, fingerprint)
	if errors.Is(err, errIncusNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if validateManagementCertificate(certificate, fingerprint) != nil {
		return false, ErrBlocked
	}
	return true, nil
}

func validateManagementCertificate(certificate incusCertificate, fingerprint string) error {
	if !digestPattern.MatchString(fingerprint) || certificate.Fingerprint != fingerprint || certificate.Name != ManagementCertName || certificate.Type != "client" || certificate.Restricted || len(certificate.Projects) != 0 {
		return ErrBlocked
	}
	block, rest := pem.Decode([]byte(certificate.Certificate))
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return ErrBlocked
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil || digestBytes(parsed.Raw) != fingerprint {
		return ErrBlocked
	}
	return nil
}

func (r *localRuntime) ReadServerCertificatePEM(ctx context.Context) (string, error) {
	body, err := readRootOwnedPublicFile(incusServerCert, 64<<10)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrExternalEffects
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return "", ErrUnsafeState
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert == nil {
		return "", ErrUnsafeState
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})), nil
}

func (r *localRuntime) VerifyManagementEndpoint(ctx context.Context, bundle ConnectionBundle) error {
	gateway, gatewayErr := netip.ParseAddr(bundle.ControlGateway)
	if gatewayErr != nil || !gateway.Is4() || !gateway.IsPrivate() || bundle.Endpoint != "https://"+gateway.String()+":18443" {
		return ErrBlocked
	}
	client, err := newPinnedHTTPSClient(bundle.Endpoint, bundle.ServerCertificatePEM, bundle.AdminCertificatePEM, bundle.AdminPrivateKeyPEM)
	if err != nil {
		return err
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		return ErrBlocked
	}
	// Connections to a local bridge address are delivered through loopback.
	// Pin the probe's source to the owned gateway; this does not establish
	// reachability from a consumer container on the control bridge.
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: net.IP(gateway.AsSlice())}}).DialContext
	defer transport.CloseIdleConnections()
	var out incusServer
	if err := client.do(ctx, "GET", "/1.0", nil, &out); err != nil {
		return err
	}
	if out.Config["core.https_address"] != IncusHTTPSAddress {
		return ErrBlocked
	}
	return nil
}

func (r *localRuntime) ListRunningManagedGuests(ctx context.Context, ownership Ownership) (int, error) {
	if ownership.StoragePool == "" && ownership.DockerNetwork == "" {
		return 0, nil
	}
	instances, err := r.incus.listInstances(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, inst := range instances {
		if strings.EqualFold(inst.Status, "Running") && instanceUsesOwnedResource(inst, ownership) {
			count++
		}
	}
	return count, nil
}

func (r *localRuntime) RemoveRelay(ctx context.Context) error {
	err := r.commands.run(ctx, fixedSystemctl, []string{"disable", "--now", RelayServiceName}, nil)
	removeErr := os.Remove(DefaultRelayConfigPath)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(err, removeErr)
}

func (r *localRuntime) RemoveControlFirewall(ctx context.Context) error {
	return r.commands.runWithInput(ctx, fixedNFT, []string{"-f", "-"}, nil, []byte("delete table inet anas_incus_control\n"))
}

func (r *localRuntime) RemoveDockerControlNetwork(ctx context.Context, name, ownerID, expectedID string) error {
	if name != ControlNetworkName || !digestPattern.MatchString(expectedID) {
		return ErrInvalid
	}
	network, err := r.docker.inspectNetwork(ctx, name)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := unusedControlNetwork(network, ownerID, expectedID); err != nil {
		return err
	}
	return r.docker.deleteNetwork(ctx, network.ID)
}

func (r *localRuntime) RemoveStoragePool(ctx context.Context, name, ownerID string) error {
	if name != StoragePoolName || ownerID == "" {
		return ErrInvalid
	}
	if exists, err := r.checkStoragePoolUnused(ctx, Ownership{ID: ownerID, StoragePool: name}); err != nil || !exists {
		return err
	}
	if err := r.incus.deleteStoragePool(ctx, name); err != nil {
		return err
	}
	_, err := r.incus.getStoragePool(ctx, name)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	return ErrExternalEffects
}

func (r *localRuntime) RemoveManagementCertificate(ctx context.Context, fingerprint string) error {
	if !digestPattern.MatchString(fingerprint) {
		return ErrInvalid
	}
	certificate, err := r.incus.getCertificate(ctx, fingerprint)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if validateManagementCertificate(certificate, fingerprint) != nil {
		return ErrBlocked
	}
	if err := r.incus.deleteCertificate(ctx, fingerprint); err != nil {
		return err
	}
	_, err = r.incus.getCertificate(ctx, fingerprint)
	if errors.Is(err, errIncusNotFound) {
		return nil
	}
	return ErrExternalEffects
}

func (r *localRuntime) RemovePackages(ctx context.Context, recipe incushost.Recipe, packages []string) error {
	if !validPackageSubset(recipe, packages) {
		return ErrInvalid
	}
	// Removal uses dpkg and does not select a download source. Accept either
	// complete compiled policy, regardless of the current speedup preference.
	if err := verifyAPTPolicyFiles(recipe, false); err != nil {
		if speedupErr := verifyAPTPolicyFiles(recipe, true); speedupErr != nil {
			return errors.Join(err, speedupErr)
		}
	}
	// Do not let an apt dependency solution remove additional unapproved
	// host packages. dpkg refuses dependency breakage; no force or purge.
	//
	// The pinned Incus packages own /opt, so removing the last of them makes
	// dpkg rmdir /opt. Under this unit's ProtectSystem=strict the root is read
	// only and that rmdir fails with EROFS, which dpkg treats as fatal. Only
	// this fixed removal therefore runs as a transient unit without
	// ProtectSystem; argv is compiled apart from the recipe's owned package
	// names, and home, /tmp and privilege escalation stay restricted.
	args := append(slices.Clone(dpkgRemoveUnitArgs), packages...)
	return r.commands.run(ctx, fixedDPKGRemove, args, nil)
}

const systemdRunPath = "/usr/bin/systemd-run"

var dpkgRemoveUnitArgs = []string{
	"--quiet", "--wait", "--pipe", "--collect", "--service-type=exec",
	"--property=ProtectHome=yes", "--property=PrivateTmp=yes", "--property=NoNewPrivileges=yes",
	"--property=RuntimeMaxSec=600", "--setenv=DEBIAN_FRONTEND=noninteractive",
	"--", "/usr/bin/dpkg", "--remove", "--",
}

// compiledDPKGRemoveArgs admits only the fixed transient dpkg removal followed
// by distinct Debian package names. systemd-run can start any unit, so no other
// argv may reach it.
func compiledDPKGRemoveArgs(args []string) bool {
	prefix := len(dpkgRemoveUnitArgs)
	if len(args) <= prefix || !slices.Equal(args[:prefix], dpkgRemoveUnitArgs) {
		return false
	}
	packages := args[prefix:]
	for i, name := range packages {
		if !debianPackageName.MatchString(name) || slices.Contains(packages[:i], name) {
			return false
		}
	}
	return true
}

var debianPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,62}$`)

func isolatedAPTEnv(configPath string) []string {
	return []string{"DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none", "APT_CONFIG=" + configPath}
}

type fixedCommands struct{}

type fixedOperation string

const (
	fixedAPT        fixedOperation = "apt"
	fixedSystemctl  fixedOperation = "systemctl"
	fixedUseradd    fixedOperation = "useradd"
	fixedNFT        fixedOperation = "nft"
	fixedDPKGQuery  fixedOperation = "dpkg-query"
	fixedDPKGRemove fixedOperation = "dpkg-remove"
)

var errCommandInactive = errors.New("fixed command reported inactive")

func (fixedCommands) run(ctx context.Context, operation fixedOperation, args []string, env []string) error {
	return fixedCommands{}.runWithInput(ctx, operation, args, env, nil)
}

func (fixedCommands) runWithInput(ctx context.Context, operation fixedOperation, args []string, env []string, input []byte) error {
	executable, timeout, err := fixedCommandSpec(operation, args)
	if err != nil {
		return err
	}
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !fixedExecutable(executable) || executableUsable(executable) != nil {
		return ErrInvalid
	}
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return ErrInvalid
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Env = append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}, env...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		return ErrExternalEffects
	}
	return runCtx.Err()
}

func (c fixedCommands) output(ctx context.Context, operation fixedOperation, args []string, env []string) ([]byte, int, error) {
	executable, timeout, err := fixedCommandSpec(operation, args)
	if err != nil {
		return nil, -1, err
	}
	if ctx == nil {
		return nil, -1, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, -1, err
	}
	if !fixedExecutable(executable) || executableUsable(executable) != nil {
		return nil, -1, ErrInvalid
	}
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, "\x00\r\n") {
			return nil, -1, ErrInvalid
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Env = append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}, env...)
	var stdout, stderr limitedBuffer
	stdout.limit = 1 << 20
	stderr.limit = 16 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	if runCtx.Err() != nil {
		return nil, -1, runCtx.Err()
	}
	if stdout.Truncated() || stderr.Truncated() {
		return nil, -1, ErrExternalEffects
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return stdout.Bytes(), exit.ExitCode(), nil
		}
		return nil, -1, ErrExternalEffects
	}
	return stdout.Bytes(), 0, nil
}

func (c fixedCommands) systemctlIsActive(ctx context.Context, unit string) (bool, error) {
	if unit != "incus.service" && unit != RelayServiceName {
		return false, ErrInvalid
	}
	_, code, err := c.output(ctx, fixedSystemctl, []string{"is-active", "--quiet", unit}, nil)
	if err != nil {
		return false, err
	}
	if code == 0 {
		return true, nil
	}
	if code == 3 || code == 4 {
		return false, errCommandInactive
	}
	return false, ErrExternalEffects
}

func fixedCommandSpec(operation fixedOperation, args []string) (string, time.Duration, error) {
	if operation == fixedSystemctl {
		if len(args) == 2 && args[0] == "stop" && args[1] == "incus.service" {
			return systemctlPath, 2 * time.Minute, nil
		}
		// Queries retain their short budget. Incus' packaged service permits
		// a ten-minute startup; a thirty-second CLI timeout can otherwise
		// abandon an accepted systemd job that subsequently starts the daemon.
		// Do not use --no-block or infer success from a timeout. The caller's
		// shorter deadline/cancellation and the subsequent readback still apply.
		if len(args) != 3 {
			return "", 0, ErrInvalid
		}
		unit := args[2]
		if unit != "incus.service" && unit != RelayServiceName {
			return "", 0, ErrInvalid
		}
		if args[0] == "is-active" && args[1] == "--quiet" {
			return systemctlPath, 30 * time.Second, nil
		}
		if args[1] == "--now" {
			if args[0] == "enable" && unit == "incus.service" {
				return systemctlPath, 11 * time.Minute, nil
			}
			if unit == RelayServiceName && (args[0] == "enable" || args[0] == "disable") {
				return systemctlPath, 2 * time.Minute, nil
			}
		}
		return "", 0, ErrInvalid
	}
	if operation == fixedDPKGRemove && !compiledDPKGRemoveArgs(args) {
		return "", 0, ErrInvalid
	}
	return fixedOperationSpec(operation)
}

func fixedOperationSpec(operation fixedOperation) (string, time.Duration, error) {
	switch operation {
	case fixedAPT:
		return aptGetPath, 15 * time.Minute, nil
	case fixedSystemctl:
		return systemctlPath, 30 * time.Second, nil
	case fixedUseradd:
		return useraddPath, time.Minute, nil
	case fixedNFT:
		return nftPath, 30 * time.Second, nil
	case fixedDPKGQuery:
		return dpkgQueryPath, 30 * time.Second, nil
	case fixedDPKGRemove:
		return systemdRunPath, 11 * time.Minute, nil
	default:
		return "", 0, ErrInvalid
	}
}

func fixedExecutable(path string) bool {
	switch path {
	case aptGetPath, systemctlPath, useraddPath, nftPath, dpkgQueryPath, systemdRunPath:
		return true
	case RelayBinaryPath:
		return true
	default:
		return false
	}
}

func executableUsable(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvalid
	}
	if err := trustedAncestors(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 || !rootOwnedExecutable(info) {
		return ErrInvalid
	}
	return nil
}

type limitedBuffer struct {
	limit int
	body  bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.body.Len()
	if remaining > 0 {
		if len(p) < remaining {
			_, _ = b.body.Write(p)
		} else {
			_, _ = b.body.Write(p[:remaining])
			if len(p) > remaining {
				b.limit = -b.limit
			}
		}
	} else {
		b.limit = -absInt(b.limit)
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	return append([]byte{}, b.body.Bytes()...)
}

func (b *limitedBuffer) Truncated() bool { return b.limit < 0 }

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func nftControlRules(plan ControlNetworkPlan) string {
	return fmt.Sprintf(`table inet anas_incus_control {
 comment "anas-owner=%s"
 chain input {
  type filter hook input priority -5; policy accept;
  iifname "lo" ip daddr 127.0.0.1 tcp dport 8443 accept comment "anas-loopback-probe"
  iifname "lo" ip saddr %s ip daddr %s tcp dport 18443 accept comment "anas-local-relay-probe"
  iifname "%s" ip saddr %s ip daddr %s tcp dport 18443 accept comment "anas-control-relay"
  ip daddr %s tcp dport 18443 drop comment "anas-control-relay-default-deny"
  iifname "%s" drop comment "anas-control-host-default-deny"
 }
 chain forward {
  type filter hook forward priority -5; policy accept;
  iifname "%s" drop comment "anas-control-forward-iif-deny"
  oifname "%s" drop comment "anas-control-forward-oif-deny"
 }
}
`, plan.OwnershipID, plan.Gateway, plan.Gateway, plan.Bridge, plan.Subnet, plan.Gateway, plan.Gateway, plan.Bridge, plan.Bridge, plan.Bridge)
}

func hostInterfaceCIDRs() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, ErrIncomplete
	}
	var out []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, ErrIncomplete
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err == nil && prefix.Addr().Is4() {
				out = append(out, prefix.Masked().String())
			}
		}
	}
	return out, nil
}

func (r *localRuntime) packagesInstalled(ctx context.Context, packages []string) (bool, error) {
	if len(packages) == 0 {
		return false, ErrInvalid
	}
	args := append([]string{"-W", packageRemovalFormat, "--"}, packages...)
	out, code, err := r.commands.output(ctx, fixedDPKGQuery, args, nil)
	if err != nil {
		return false, err
	}
	if code != 0 {
		return false, nil
	}
	installed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			return false, ErrIncomplete
		}
		installed[parts[0]] = strings.HasPrefix(parts[1], "ii")
	}
	for _, pkg := range packages {
		if !installed[pkg] {
			return false, nil
		}
	}
	return true, nil
}

func storagePoolOwned(pool incusStoragePool, ownership Ownership) bool {
	return pool.Name == StoragePoolName && pool.Driver == "btrfs" &&
		pool.Config["user.anas.owner"] == "incus-host-provision" &&
		ownership.ID != "" && pool.Config["user.anas.owner_id"] == ownership.ID
}

func instanceUsesOwnedResource(inst incusInstance, ownership Ownership) bool {
	for _, devices := range []map[string]map[string]string{inst.Devices, inst.ExpandedDevices} {
		for _, device := range devices {
			if ownership.StoragePool != "" && device["pool"] == ownership.StoragePool {
				return true
			}
			if ownership.DockerNetwork != "" && (device["network"] == ownership.DockerNetwork || device["parent"] == ownership.DockerNetwork || (ownership.ControlBridge != "" && device["parent"] == ownership.ControlBridge)) {
				return true
			}
		}
	}
	return false
}

func trustedAncestors(path string) error {
	clean := filepath.Clean(path)
	dir := filepath.Dir(clean)
	for dir != "/" && dir != "." {
		info, err := os.Lstat(dir)
		if err != nil || !rootOwnedPrivateDir(info) {
			return ErrInvalid
		}
		dir = filepath.Dir(dir)
	}
	return nil
}

func readRootOwnedPublicFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, ErrUnsafeState
	}
	if err := trustedAncestors(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || !rootOwnedExecutable(info) {
		return nil, ErrUnsafeState
	}
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, ErrUnsafeState
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, ErrUnsafeState
	}
	after, err := file.Stat()
	if err != nil || !sameFileIdentity(info, after) {
		return nil, ErrUnsafeState
	}
	return body, nil
}

func verifyRootOwnedUnixSocket(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return ErrUnsafeState
	}
	if err := trustedAncestors(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0002 != 0 || !rootOwnedExecutable(info) {
		return ErrUnsafeState
	}
	return nil
}

func verifyAPTPolicyFiles(recipe incushost.Recipe, chineseSpeedup bool) error {
	files, err := CompiledAPTConfigFiles(recipe, chineseSpeedup)
	if err != nil {
		return err
	}
	expected := map[string]bool{}
	for _, file := range files {
		expected[file.Path] = true
		body, err := readRootOwnedPublicFile(file.Path, 64<<10)
		if err != nil {
			return errors.Join(ErrBlocked, err)
		}
		if !bytes.Equal(body, file.Body) {
			return ErrBlocked
		}
	}
	for _, dir := range []string{aptSourcesDir + "/" + recipe.ID, aptPrefsDir + "/" + recipe.ID} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return errors.Join(ErrBlocked, err)
		}
		for _, entry := range entries {
			path := dir + "/" + entry.Name()
			if entry.IsDir() || !expected[path] {
				return ErrBlocked
			}
		}
	}
	entries, err := os.ReadDir(aptDir + "/apt.conf.d")
	if err != nil || len(entries) != 0 {
		return ErrBlocked
	}
	return nil
}

func ensureAPTWorkDirs() error {
	// APT owns its sandbox user's partial subdirectories. Only their protected
	// root-owned parents belong to this preparer, including on subsequent runs.
	for _, dir := range []string{"/var/lib/anas/incus-apt/lists", "/var/cache/anas/incus-apt/archives"} {
		if err := ensureTrustedRootDirectory(dir, 0755); err != nil {
			return ErrUnsafeState
		}
	}
	return nil
}

// zabblyArchiveKey is the signing key of Zabbly's Incus repository, compiled
// in rather than fetched: the key that authenticates the Incus packages must
// not come over the same channel as the packages. Its primary fingerprint is
// zabblyKeyFingerprint (checked by test against the published value).
//
//go:embed zabbly-incus-archive-key.asc
var zabblyArchiveKey []byte

const (
	zabblyKeyFingerprint = "4EFC590696CB15B87C73A3AD82CC8797C838DCFD"
	zabblyOrigin         = "pkgs.zabbly.com"
	zabblyLTS70URI       = "https://" + zabblyOrigin + "/incus/lts-7.0"
	chineseAPTOrigin     = "mirrors.aliyun.com"
)

// incusPackages come only from the pinned Incus source; the distribution's
// own, older incus packages are pinned away so a missing or stale Zabbly
// index cannot quietly fall back to Incus 6.0.
var incusPackages = []string{"incus", "incus-base", "incus-client"}

// zabblySource embeds the key in the deb822 Signed-By field. APT verifies
// signatures as its unprivileged _apt user, which cannot read the root-only
// /etc/anas/incus-apt tree; an inline key needs no separate readable file and
// is covered by the same byte-for-byte check as the source itself.
func zabblySource(codename string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "Types: deb\nURIs: %s\nSuites: %s\nComponents: main\nArchitectures: amd64 arm64\nSigned-By:\n", zabblyLTS70URI, codename)
	for _, line := range strings.Split(strings.TrimRight(string(zabblyArchiveKey), "\n"), "\n") {
		if line == "" {
			line = "."
		}
		b.WriteString(" " + line + "\n")
	}
	return b.Bytes()
}

// CompiledAPTConfigFiles returns the exact root-owned apt configuration files
// the host installer must place under /etc/anas/incus-apt before package
// installation. The backend verifies these files byte-for-byte and never trusts
// arbitrary existing apt sources. The distribution's archive supplies every
// dependency; the pinned Incus 7.0 LTS source supplies only the Incus packages.
// chineseSpeedup switches only the distribution archive to the fixed Aliyun
// mirror. The caller must supply this preference from its confirmed request.
func CompiledAPTConfigFiles(recipe incushost.Recipe, chineseSpeedup bool) ([]APTConfigFile, error) {
	// Recipe identity is compiled installation policy, never a caller-selected
	// source path, package name, suite or repository override.
	rows, err := incushost.Recipes()
	if err != nil {
		return nil, ErrInvalid
	}
	matched := false
	for _, row := range rows {
		want, e1 := json.Marshal(row)
		got, e2 := json.Marshal(recipe)
		if e1 == nil && e2 == nil && bytes.Equal(want, got) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, ErrInvalid
	}
	if recipe.PackageManager != "apt" || recipe.ID == "" || recipe.Codename == "" {
		return nil, ErrInvalid
	}
	if recipe.Distribution != "debian" && recipe.Distribution != "ubuntu" {
		return nil, ErrInvalid
	}
	if recipe.Repository != incushost.IncusRepository {
		return nil, ErrInvalid
	}
	for _, pkg := range recipe.Packages {
		if pkg == "" || strings.ContainsAny(pkg, "\x00\r\n\t /") {
			return nil, ErrInvalid
		}
	}
	configPath := aptConfigPathForRecipe(recipe)
	conf := []byte("Dir \"/\";\n" +
		"Dir::Etc \"/etc/anas/incus-apt\";\n" +
		"Dir::Etc::main \"apt.conf.empty\";\n" +
		"Dir::Etc::parts \"apt.conf.d\";\n" +
		"Dir::Etc::sourcelist \"anas-incus-empty.list\";\n" +
		"Dir::Etc::sourceparts \"sources.list.d/" + recipe.ID + "\";\n" +
		"Dir::Etc::preferences \"preferences\";\n" +
		"Dir::Etc::preferencesparts \"preferences.d/" + recipe.ID + "\";\n" +
		"Dir::State::lists \"/var/lib/anas/incus-apt/lists\";\n" +
		"Dir::Cache::archives \"/var/cache/anas/incus-apt/archives\";\n" +
		"Dir::Cache::srcpkgcache \"/var/cache/anas/incus-apt/srcpkgcache.bin\";\n" +
		"Dir::Cache::pkgcache \"/var/cache/anas/incus-apt/pkgcache.bin\";\n" +
		"DPkg::Pre-Install-Pkgs \"\";\n" +
		"DPkg::Post-Invoke \"\";\n" +
		"APT::Update::Post-Invoke \"\";\n" +
		"APT::Update::Post-Invoke-Success \"\";\n" +
		"APT::Get::List-Cleanup \"0\";\n" +
		"Acquire::AllowInsecureRepositories \"false\";\n" +
		"Acquire::AllowDowngradeToInsecureRepositories \"false\";\n")
	sourcePath := aptSourcesDir + "/" + recipe.ID + "/anas.sources"
	prefsPath := aptPrefsDir + "/" + recipe.ID + "/anas.pref"
	var source []byte
	var origins []string
	switch recipe.Distribution {
	case "debian":
		source = []byte(fmt.Sprintf("Types: deb\nURIs: https://deb.debian.org/debian\nSuites: %s %s-updates\nComponents: main\nArchitectures: amd64 arm64\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n\nTypes: deb\nURIs: https://deb.debian.org/debian-security\nSuites: %s-security\nComponents: main\nArchitectures: amd64 arm64\nSigned-By: /usr/share/keyrings/debian-archive-keyring.gpg\n", recipe.Codename, recipe.Codename, recipe.Codename))
		origins = []string{"deb.debian.org"}
	case "ubuntu":
		source = []byte(fmt.Sprintf("Types: deb\nURIs: https://archive.ubuntu.com/ubuntu\nSuites: %s %s-updates %s-backports\nComponents: main restricted universe multiverse\nArchitectures: amd64\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n\nTypes: deb\nURIs: https://security.ubuntu.com/ubuntu\nSuites: %s-security\nComponents: main restricted universe multiverse\nArchitectures: amd64\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n\nTypes: deb\nURIs: https://ports.ubuntu.com/ubuntu-ports\nSuites: %s %s-updates %s-backports %s-security\nComponents: main restricted universe multiverse\nArchitectures: arm64\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n", recipe.Codename, recipe.Codename, recipe.Codename, recipe.Codename, recipe.Codename, recipe.Codename, recipe.Codename, recipe.Codename))
		origins = []string{"archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com"}
	}
	if chineseSpeedup {
		// All replacements are compiled HTTPS endpoints. Keep suites,
		// architectures and distribution signing keys identical to upstream.
		replacer := strings.NewReplacer(
			"https://deb.debian.org/debian-security", "https://"+chineseAPTOrigin+"/debian-security",
			"https://deb.debian.org/debian", "https://"+chineseAPTOrigin+"/debian",
			"https://archive.ubuntu.com/ubuntu", "https://"+chineseAPTOrigin+"/ubuntu",
			"https://security.ubuntu.com/ubuntu", "https://"+chineseAPTOrigin+"/ubuntu",
			"https://ports.ubuntu.com/ubuntu-ports", "https://"+chineseAPTOrigin+"/ubuntu-ports",
		)
		source = []byte(replacer.Replace(string(source)))
		origins = []string{chineseAPTOrigin}
	}
	source = append(append(source, '\n'), zabblySource(recipe.Codename)...)
	// Package-specific records take precedence over the general ones below.
	var pin bytes.Buffer
	incus := strings.Join(incusPackages, " ")
	fmt.Fprintf(&pin, "Package: %s\nPin: origin \"%s\"\nPin-Priority: 995\n", incus, zabblyOrigin)
	for _, origin := range origins {
		fmt.Fprintf(&pin, "\nPackage: %s\nPin: origin \"%s\"\nPin-Priority: -1\n", incus, origin)
	}
	for _, origin := range origins {
		fmt.Fprintf(&pin, "\nPackage: *\nPin: origin \"%s\"\nPin-Priority: 990\n", origin)
	}
	return []APTConfigFile{
		{Path: configPath, Body: conf, Mode: 0644},
		{Path: aptDir + "/anas-incus-empty.list", Body: []byte("# ANAS Incus provisioning intentionally uses deb822 sources only.\n"), Mode: 0644},
		{Path: aptDir + "/apt.conf.empty", Body: []byte("# ANAS Incus provisioning intentionally disables system apt.conf.\n"), Mode: 0644},
		{Path: sourcePath, Body: source, Mode: 0644},
		{Path: aptDir + "/preferences", Body: []byte("# ANAS Incus provisioning global preferences placeholder.\n"), Mode: 0644},
		{Path: prefsPath, Body: pin.Bytes(), Mode: 0644},
	}, nil
}

func aptConfigPathForRecipe(recipe incushost.Recipe) string {
	return aptDir + "/" + recipe.ID + ".apt.conf"
}

// WriteAPTConfigFiles writes the deterministic apt files under root. Pass "/"
// only from privileged host-action code; tests should pass a temporary root.
func WriteAPTConfigFiles(root string, recipe incushost.Recipe, chineseSpeedup bool) ([]APTConfigFile, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, ErrInvalid
	}
	files, err := CompiledAPTConfigFiles(recipe, chineseSpeedup)
	if err != nil {
		return nil, err
	}
	alternateFiles, err := CompiledAPTConfigFiles(recipe, !chineseSpeedup)
	if err != nil {
		return nil, err
	}
	if root == "/" {
		if os.Getuid() != 0 || os.Geteuid() != 0 {
			return nil, ErrUnsafeState
		}
	}
	for i, file := range files {
		target := filepath.Join(root, strings.TrimPrefix(file.Path, "/"))
		// Only the two compiled variants of this recipe are ours to replace.
		// This permits toggling speedup and resuming interrupted switches;
		// arbitrary drift still blocks every write before any file changes.
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil || !info.Mode().IsRegular() {
			return nil, ErrUnsafeState
		}
		var body []byte
		if root == "/" {
			body, err = readRootOwnedPublicFile(target, 64<<10)
		} else {
			body, err = os.ReadFile(target)
		}
		if err != nil || (!bytes.Equal(body, file.Body) && !bytes.Equal(body, alternateFiles[i].Body)) {
			return nil, ErrUnsafeState
		}
	}
	if root == "/" {
		if err := ensureTrustedRootDirectory(aptDir+"/apt.conf.d", 0755); err != nil {
			return nil, ErrUnsafeState
		}
		if err := trustedAncestors(aptDir + "/apt.conf.d/unused"); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(aptDir + "/apt.conf.d")
		if err != nil || len(entries) != 0 {
			return nil, ErrUnsafeState
		}
	}
	written := make([]APTConfigFile, 0, len(files))
	for _, file := range files {
		target := filepath.Join(root, strings.TrimPrefix(file.Path, "/"))
		var err error
		if root == "/" {
			err = writeRootOnlyFile(target, file.Body, file.Mode)
		} else {
			err = writeAPTConfigFileForRoot(target, file.Body, file.Mode)
		}
		if err != nil {
			return written, err
		}
		written = append(written, APTConfigFile{Path: target, Body: file.Body, Mode: file.Mode})
	}
	return written, nil
}

func writeAPTConfigFileForRoot(path string, body []byte, mode os.FileMode) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(body) == 0 || len(body) > 1<<20 {
		return ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, body, mode)
}

// Validate the observed rules, not their human-readable comments. nft's JSON
// output includes metainfo and may interleave each chain with its rules. Only
// order WITHIN a chain affects this table's verdicts and must match exactly.
func validateNFTControlRulesJSON(body []byte, plan ControlNetworkPlan) (bool, error) {
	var document struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if len(body) > 64<<10 || validateNoDuplicateJSONFields(body) != nil ||
		decodeNFTObject(body, &document, "nftables") != nil || len(document.Nftables) == 0 ||
		plan.OwnershipID == "" || plan.Bridge == "" || plan.Subnet == "" || plan.Gateway == "" {
		return false, ErrBlocked
	}
	expected := expectedNFTRules(plan)
	order := map[string][]string{
		"input":   {"anas-loopback-probe", "anas-local-relay-probe", "anas-control-relay", "anas-control-relay-default-deny", "anas-control-host-default-deny"},
		"forward": {"anas-control-forward-iif-deny", "anas-control-forward-oif-deny"},
	}
	counts := map[string]int{}
	chains := map[string]bool{}
	metaSeen, tableSeen := false, false
	for index, item := range document.Nftables {
		if len(item) != 1 {
			return false, ErrBlocked
		}
		if raw, ok := item["metainfo"]; ok {
			var meta struct {
				Version string `json:"version"`
				Release string `json:"release_name"`
				Schema  int    `json:"json_schema_version"`
			}
			if index != 0 || metaSeen || decodeNFTObject(raw, &meta, "version", "release_name", "json_schema_version") != nil ||
				meta.Schema != 1 || len(meta.Version) > 128 || len(meta.Release) > 128 {
				return false, ErrBlocked
			}
			metaSeen = true
			continue
		}
		if raw, ok := item["table"]; ok {
			var table struct {
				Family  string   `json:"family"`
				Name    string   `json:"name"`
				Comment string   `json:"comment"`
				Handle  uint64   `json:"handle"`
				Flags   []string `json:"flags"`
			}
			if tableSeen || len(chains) != 0 || decodeNFTObject(raw, &table, "family", "name", "comment", "handle", "flags") != nil ||
				table.Family != "inet" || table.Name != "anas_incus_control" || table.Comment != "anas-owner="+plan.OwnershipID || len(table.Flags) != 0 {
				return false, ErrBlocked
			}
			tableSeen = true
			continue
		}
		if raw, ok := item["chain"]; ok {
			var chain struct {
				Family   string `json:"family"`
				Table    string `json:"table"`
				Name     string `json:"name"`
				Type     string `json:"type"`
				Hook     string `json:"hook"`
				Priority *int   `json:"prio"`
				Policy   string `json:"policy"`
				Handle   uint64 `json:"handle"`
			}
			if !tableSeen || decodeNFTObject(raw, &chain, "family", "table", "name", "type", "hook", "prio", "policy", "handle") != nil ||
				chain.Family != "inet" || chain.Table != "anas_incus_control" || chain.Type != "filter" || chain.Policy != "accept" ||
				chain.Priority == nil || *chain.Priority != -5 || chain.Hook != chain.Name || order[chain.Name] == nil || chains[chain.Name] {
				return false, ErrBlocked
			}
			chains[chain.Name] = true
			continue
		}
		if raw, ok := item["rule"]; ok {
			var rule struct {
				Family  string          `json:"family"`
				Table   string          `json:"table"`
				Chain   string          `json:"chain"`
				Comment string          `json:"comment"`
				Expr    json.RawMessage `json:"expr"`
				Handle  uint64          `json:"handle"`
			}
			if decodeNFTObject(raw, &rule, "family", "table", "chain", "comment", "expr", "handle") != nil ||
				!tableSeen || !chains[rule.Chain] || rule.Family != "inet" || rule.Table != "anas_incus_control" ||
				counts[rule.Chain] >= len(order[rule.Chain]) || rule.Comment != order[rule.Chain][counts[rule.Chain]] {
				return false, ErrBlocked
			}
			want := expected[rule.Chain+"|"+rule.Comment]
			if len(want) == 0 || !nftExprsEqual(rule.Expr, want) {
				return false, ErrBlocked
			}
			counts[rule.Chain]++
			continue
		}
		return false, ErrBlocked
	}
	if !tableSeen || !chains["input"] || !chains["forward"] || counts["input"] != len(order["input"]) || counts["forward"] != len(order["forward"]) {
		return false, ErrBlocked
	}
	return true, nil
}

// Map keys are checked before typed decoding: encoding/json's case aliases
// must not make a different schema look canonical. Unknown semantic flags
// (notably a dormant table) cannot be silently discarded.
func decodeNFTObject(body []byte, out any, keys ...string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return ErrBlocked
	}
	for field := range fields {
		allowed := false
		for _, key := range keys {
			if field == key {
				allowed = true
				break
			}
		}
		if !allowed {
			return ErrBlocked
		}
	}
	if json.Unmarshal(body, out) != nil {
		return ErrBlocked
	}
	return nil
}

type nftExpectedExpr struct {
	Kind     string
	MetaKey  string
	Protocol string
	Field    string
	Right    any
}

func expectedNFTRules(plan ControlNetworkPlan) map[string][]nftExpectedExpr {
	return map[string][]nftExpectedExpr{
		"input|anas-local-relay-probe": {
			{Kind: "match", MetaKey: "iifname", Right: "lo"},
			{Kind: "match", Protocol: "ip", Field: "saddr", Right: plan.Gateway},
			{Kind: "match", Protocol: "ip", Field: "daddr", Right: plan.Gateway},
			{Kind: "match", Protocol: "tcp", Field: "dport", Right: float64(18443)},
			{Kind: "accept"},
		},
		"input|anas-loopback-probe": {
			{Kind: "match", MetaKey: "iifname", Right: "lo"},
			{Kind: "match", Protocol: "ip", Field: "daddr", Right: "127.0.0.1"},
			{Kind: "match", Protocol: "tcp", Field: "dport", Right: float64(8443)},
			{Kind: "accept"},
		},
		"input|anas-control-relay": {
			{Kind: "match", MetaKey: "iifname", Right: plan.Bridge},
			{Kind: "match", Protocol: "ip", Field: "saddr", Right: nftPrefixRight(plan.Subnet)},
			{Kind: "match", Protocol: "ip", Field: "daddr", Right: plan.Gateway},
			{Kind: "match", Protocol: "tcp", Field: "dport", Right: float64(18443)},
			{Kind: "accept"},
		},
		"input|anas-control-relay-default-deny": {
			{Kind: "match", Protocol: "ip", Field: "daddr", Right: plan.Gateway},
			{Kind: "match", Protocol: "tcp", Field: "dport", Right: float64(18443)},
			{Kind: "drop"},
		},
		"input|anas-control-host-default-deny": {
			{Kind: "match", MetaKey: "iifname", Right: plan.Bridge},
			{Kind: "drop"},
		},
		"forward|anas-control-forward-iif-deny": {
			{Kind: "match", MetaKey: "iifname", Right: plan.Bridge},
			{Kind: "drop"},
		},
		"forward|anas-control-forward-oif-deny": {
			{Kind: "match", MetaKey: "oifname", Right: plan.Bridge},
			{Kind: "drop"},
		},
	}
}

func nftPrefixRight(cidr string) map[string]any {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil
	}
	return map[string]any{"prefix": map[string]any{"addr": prefix.Masked().Addr().String(), "len": float64(prefix.Bits())}}
}

func nftExprsEqual(raw json.RawMessage, expected []nftExpectedExpr) bool {
	var exprs []map[string]json.RawMessage
	if json.Unmarshal(raw, &exprs) != nil || len(exprs) != len(expected) {
		return false
	}
	for i, expr := range exprs {
		if len(expr) != 1 || !nftExprEqual(expr, expected[i]) {
			return false
		}
	}
	return true
}

func nftExprEqual(expr map[string]json.RawMessage, expected nftExpectedExpr) bool {
	switch expected.Kind {
	case "accept", "drop":
		raw, ok := expr[expected.Kind]
		return ok && string(raw) == "null"
	case "match":
		raw, ok := expr["match"]
		if !ok {
			return false
		}
		var match struct {
			Op    string          `json:"op"`
			Left  json.RawMessage `json:"left"`
			Right any             `json:"right"`
		}
		if decodeNFTObject(raw, &match, "op", "left", "right") != nil || match.Op != "==" {
			return false
		}
		if expected.MetaKey != "" {
			var left struct {
				Meta struct {
					Key string `json:"key"`
				} `json:"meta"`
			}
			var object map[string]json.RawMessage
			var meta map[string]string
			return decodeNFTObject(match.Left, &left, "meta") == nil && json.Unmarshal(match.Left, &object) == nil &&
				decodeNFTObject(object["meta"], &meta, "key") == nil && len(meta) == 1 && left.Meta.Key == expected.MetaKey && nftJSONEqual(match.Right, expected.Right)
		}
		var left struct {
			Payload struct {
				Protocol string `json:"protocol"`
				Field    string `json:"field"`
			} `json:"payload"`
		}
		var object map[string]json.RawMessage
		var payload map[string]string
		return decodeNFTObject(match.Left, &left, "payload") == nil && json.Unmarshal(match.Left, &object) == nil &&
			decodeNFTObject(object["payload"], &payload, "protocol", "field") == nil && len(payload) == 2 &&
			left.Payload.Protocol == expected.Protocol && left.Payload.Field == expected.Field && nftJSONEqual(match.Right, expected.Right)
	default:
		return false
	}
}

func nftJSONEqual(got, want any) bool {
	gotBody, gotErr := json.Marshal(got)
	wantBody, wantErr := json.Marshal(want)
	return gotErr == nil && wantErr == nil && bytes.Equal(gotBody, wantBody)
}

func (r *localRuntime) controlFirewallInstalled(ctx context.Context, plan ControlNetworkPlan) (bool, error) {
	out, code, err := r.commands.output(ctx, fixedNFT, []string{"-j", "list", "table", "inet", "anas_incus_control"}, nil)
	if err != nil {
		return false, err
	}
	if code != 0 {
		return false, nil
	}
	return validateNFTControlRulesJSON(out, plan)
}

func interfaceIndex(name string) int {
	if name == "" {
		return 0
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0
	}
	return iface.Index
}

func lookupUserGroupIDs(userName, groupName string) (uint32, uint32, bool) {
	u, err := user.Lookup(userName)
	if err != nil {
		return 0, 0, false
	}
	g, err := user.LookupGroup(groupName)
	if err != nil {
		return 0, 0, false
	}
	uid, err1 := parseUint32(u.Uid)
	gid, err2 := parseUint32(g.Gid)
	return uid, gid, err1 == nil && err2 == nil && uid != 0 && gid != 0
}

func parseUint32(text string) (uint32, error) {
	var n uint64
	if text == "" || len(text) > 10 {
		return 0, ErrInvalid
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
		n = n*10 + uint64(c-'0')
		if n > 1<<32-1 {
			return 0, ErrInvalid
		}
	}
	return uint32(n), nil
}

type pinnedHTTPSClient struct {
	endpoint string
	http     *http.Client
}

func newPinnedHTTPSClient(endpoint, serverCertPEM, clientCertPEM, clientKeyPEM string) (*pinnedHTTPSClient, error) {
	block, _ := pem.Decode([]byte(serverCertPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, ErrInvalid
	}
	pinned, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, ErrInvalid
	}
	pair, err := tls.X509KeyPair([]byte(clientCertPEM), []byte(clientKeyPEM))
	if err != nil {
		return nil, ErrInvalid
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{
		Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || !bytes.Equal(raw[0], pinned.Raw) {
				return ErrBlocked
			}
			return nil
		},
	}}
	return &pinnedHTTPSClient{endpoint: strings.TrimSuffix(endpoint, "/"), http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *pinnedHTTPSClient) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return ErrInvalid
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, reader)
	if err != nil {
		return ErrInvalid
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrExternalEffects
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return ErrExternalEffects
	}
	return decodeIncusMethodEnvelope(method, res.StatusCode, raw, out)
}
