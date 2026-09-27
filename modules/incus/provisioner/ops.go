package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/computeclient"
)

type lease struct {
	Consumer          string
	Sandbox           string
	StoragePool       string
	NetworkIPv6       bool
	InstancePrefix    string
	MaxInstances      int
	CPU               int
	MemoryMiB         int
	DiskGiB           int
	ImageAllowlist    []string
	ImageArchitecture string
	ImageSupplyFile   string
	ClientCertPEM     []byte
	// Credential is the SHA-256 fingerprint of ClientCertPEM. Core mints one
	// certificate per workspace, consumer and resource, so it is also the
	// identity that owns the lease's project and bridge.
	Credential string
	Isolation  string
	// Daemon is read from the target daemon by ensure and inspect, never
	// taken from the environment: it decides which restriction keys exist.
	Daemon daemonRestrictions
}

// daemonRestrictions records which restriction keys newer than Incus 6.0 LTS
// the target daemon understands, from the API extension each key shipped
// with. The daemon rejects unknown project keys, so a key is written only
// where it exists; where it exists and is left alone, the daemon's default
// applies, and for restricted.virtual-machines.nesting (allow) and
// restricted.storage-pools.access (every pool) that default is permissive.
type daemonRestrictions struct {
	StoragePoolAccess bool // projects_restricted_storage_pool_access, Incus 7.0
	VMNesting         bool // projects_restricted_virtual_machines_nesting, Incus 7.x
}

// readDaemonRestrictions asks the daemon itself. A version string would have
// to be mapped onto keys by hand and would misread LTS backports; the API
// extension list is what the daemon advertises for exactly these keys.
func readDaemonRestrictions(ctx context.Context, c *client) (daemonRestrictions, error) {
	var server struct {
		APIExtensions []string `json:"api_extensions"`
	}
	if err := c.do(ctx, "GET", "/1.0", nil, &server); err != nil {
		return daemonRestrictions{}, fmt.Errorf("read incus API extensions: %w", err)
	}
	if server.APIExtensions == nil {
		return daemonRestrictions{}, fmt.Errorf("incus daemon did not report its API extensions to the provisioning certificate")
	}
	return daemonRestrictions{
		StoragePoolAccess: slices.Contains(server.APIExtensions, "projects_restricted_storage_pool_access"),
		VMNesting:         slices.Contains(server.APIExtensions, "projects_restricted_virtual_machines_nesting"),
	}, nil
}

type project struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Config      map[string]string `json:"config"`
}

type network struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Config      map[string]string `json:"config"`
}

type device map[string]string

type profile struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Config      map[string]string `json:"config"`
	Devices     map[string]device `json:"devices"`
}

type certificate struct {
	Fingerprint string   `json:"fingerprint,omitempty"`
	Certificate string   `json:"certificate,omitempty"`
	Name        string   `json:"name,omitempty"`
	Type        string   `json:"type,omitempty"`
	Restricted  bool     `json:"restricted"`
	Projects    []string `json:"projects"`
}

type inspectResult struct {
	Exists        bool `json:"exists"`
	Ready         bool `json:"ready"`
	Restricted    bool `json:"restricted"`
	QuotaEnforced bool `json:"quota_enforced"`
}

// clearedRestrictions are restriction keys whose absence is either the
// strictest state (idmap ranges, network uplinks/subnets/zones/integrations)
// or inert under the keys projectConfig writes (disk.paths only applies to
// restricted.devices.disk=allow, cluster.groups only to an allowed cluster
// target). ensure removes them from an adopted project and readiness requires
// them absent. Deleting a key the daemon does not know is a no-op.
//
// restricted.images.servers (Incus 7.0) is here although its absence allows
// every image server. In 7.0.1 and 7.5.1 any non-empty value also rejects
// creating an instance from an image already in the project, because that
// request names no server and its empty host is never in the list; and the
// only download it gates is a URL pull, not a simplestreams copy. It cannot
// narrow what a lease boots without breaking the lease, so the pinned
// fingerprint allowlist remains consumer-side (INCUS-R-085).
var clearedRestrictions = []string{
	"restricted.cluster.groups",
	"restricted.devices.disk.paths",
	"restricted.idmap.uid",
	"restricted.idmap.gid",
	"restricted.images.servers",
	"restricted.networks.integrations",
	"restricted.networks.subnets",
	"restricted.networks.uplinks",
	"restricted.networks.zones",
}

// projectConfig maps one lease onto the fence Incus itself will enforce.
//
// Every restriction key the target daemon knows (the Incus 6.0 LTS set plus
// the 7.x keys it advertises in daemonRestrictions) is either written here
// with its strict value or listed in clearedRestrictions. ensure merges an
// existing project's configuration, so a key left to restricted=true's default
// would keep whatever an older project or an operator once set there.
//
// The contract states per-instance limits; Incus states project-wide totals.
// Multiplying by max_instances is what makes the two agree: a lease that may
// run 8 instances of 4 CPUs cannot exceed 32 CPUs in total, whatever the
// consumer asks for.
func projectConfig(l lease) map[string]string {
	config := map[string]string{
		// Ownership markers. Host-side prune and forwarding retirement
		// identify a lease project by consumer and sandbox; the credential
		// marker is what keeps a second workspace with the same sandbox name
		// from adopting it (see verifyProjectOwner).
		"user.anas.consumer": l.Consumer,
		"user.anas.sandbox":  l.Sandbox,
		leaseCredentialKey:   l.Credential,
		"restricted":         "true",
		"features.images":    "true",
		"features.profiles":  "true",
		// Bridge networks live in the default project (Incus 7.3 only
		// supports OVN in network-isolated projects). Restrict access to
		// exactly this lease's provider-owned bridge instead.
		"features.networks":                    "false",
		"restricted.networks.access":           computeclient.NetworkName(l.Sandbox),
		"limits.instances":                     fmt.Sprint(l.MaxInstances),
		"limits.cpu":                           fmt.Sprint(l.MaxInstances * l.CPU),
		"limits.memory":                        fmt.Sprintf("%dMiB", l.MaxInstances*l.MemoryMiB),
		"limits.disk":                          projectDiskLimit(l),
		"restricted.backups":                   "block",
		"restricted.cluster.target":            "block",
		"restricted.containers.interception":   "block",
		"restricted.containers.lowlevel":       "block",
		"restricted.virtual-machines.lowlevel": "block",
		"restricted.containers.nesting":        "block",
		// The system-container tier is only a weaker isolation boundary than a
		// VM, never a weaker privilege boundary. The VM tier states it too: its
		// container limit is zero, but a project that would accept a privileged
		// container is not a fence either tier describes.
		"restricted.containers.privilege": "unprivileged",
		"restricted.devices.disk":         "block",
		"restricted.devices.gpu":          "block",
		"restricted.devices.infiniband":   "block",
		// "block" still permits the root disk; it forbids attaching any other
		// disk, which is what keeps a host path out of a guest.
		"restricted.devices.nic": "managed",
		"restricted.devices.pci": "block",
		// Keep this explicit: ensure merges existing project configuration.
		// Relying on restricted=true's default would preserve an old "allow"
		// value and let the consumer bypass the mediated ingress path.
		"restricted.devices.proxy":        "block",
		"restricted.devices.unix-block":   "block",
		"restricted.devices.unix-char":    "block",
		"restricted.devices.unix-hotplug": "block",
		"restricted.devices.usb":          "block",
		"restricted.snapshots":            "block",
	}
	// The tier is a property of the project, not of the consumer's create
	// call. A zero count for the other instance type makes the daemon refuse
	// it, so a compromised consumer cannot trade the VM tier's own kernel for
	// a system container sharing the host's. The allowed type is written too,
	// so a stale zero from a previous tier cannot survive the merge.
	if l.Daemon.VMNesting {
		// Blocks security.nesting on virtual machines: a VM lease hosts one
		// guest kernel, never a hypervisor for further guests. The VM-tier
		// profile turns it off explicitly, which the daemon then requires.
		config["restricted.virtual-machines.nesting"] = "block"
	}
	if l.Daemon.StoragePoolAccess {
		// The profile's root disk already names this pool; without the key an
		// instance could override its root device onto any other pool.
		config["restricted.storage-pools.access"] = l.StoragePool
	}
	count := fmt.Sprint(l.MaxInstances)
	if l.Isolation == "container" {
		config["limits.containers"], config["limits.virtual-machines"] = count, "0"
		// The advertised system-container tier hosts OCI workloads. Their
		// inner proc/user/mount namespaces are not nested KVM and do not grant
		// host privilege. Low-level config and host devices remain blocked.
		config["restricted.containers.nesting"] = "allow"
	} else {
		config["limits.containers"], config["limits.virtual-machines"] = "0", count
	}
	return config
}

// unmanagedRestrictions names restriction keys that projectConfig neither
// writes nor clears: keys from an Incus release newer than this provider, or a
// 7.x key the daemon does not advertise. Their semantics are unknown here, so
// a project carrying one is refused rather than guessed at. Only key names are
// returned, never values.
func unmanagedRestrictions(config map[string]string, l lease) []string {
	desired := projectConfig(l)
	var keys []string
	for key := range config {
		if strings.HasPrefix(key, "restricted.") && !slices.Contains(clearedRestrictions, key) {
			if _, managed := desired[key]; !managed {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

// leaseCredentialKey records which lease owns a project or bridge: the
// fingerprint of that lease's restricted client certificate. The sandbox name
// alone cannot, because it is fixed in the consumer's manifest and repeats in
// every workspace that installs the same consumer against one daemon.
const leaseCredentialKey = "user.anas.lease_credential"

// verifyProjectOwner decides whether ensure may converge an existing project.
// A project marked for another lease is refused even when that lease's
// certificate is already revoked: revoke keeps the project, and a workspace
// that happens to reuse the sandbox name must not inherit its instances.
// An unmarked project predates these markers, came from a pre-contract
// controller or was made by hand; it is adopted only when no other restricted
// certificate can drive it, which foreignProjectCertificates checks for every
// existing project anyway.
func verifyProjectOwner(config map[string]string, l lease) error {
	for _, key := range []string{"user.anas.consumer", "user.anas.sandbox", leaseCredentialKey} {
		if value := config[key]; value != "" && value != projectConfig(l)[key] {
			return fmt.Errorf("incus project %s belongs to another lease (%s differs); refusing to adopt or modify it", l.Sandbox, key)
		}
	}
	return nil
}

// foreignProjectCertificates lists restricted client certificates, other than
// this lease's own, that can drive the project. One project serves one lease:
// another such certificate is either a second workspace sharing the sandbox
// name or a credential nobody revoked. Unrestricted certificates reach every
// project and are the daemon administrator's; metrics certificates cannot
// change anything. Only fingerprint prefixes are returned.
func foreignProjectCertificates(ctx context.Context, c *client, l lease) ([]string, error) {
	var certificates []certificate
	if err := c.do(ctx, "GET", "/1.0/certificates?recursion=1", nil, &certificates); err != nil {
		return nil, fmt.Errorf("read trusted certificates: %w", err)
	}
	var foreign []string
	for _, cert := range certificates {
		if cert.Type == "client" && cert.Restricted && slices.Contains(cert.Projects, l.Sandbox) && cert.Fingerprint != l.Credential {
			foreign = append(foreign, short(cert.Fingerprint))
		}
	}
	slices.Sort(foreign)
	return foreign, nil
}

// quotaEnforced checks exact requested project limits, not merely nonempty
// strings. Storage admission is separate; neither check proves live enforcement.
func quotaEnforced(config map[string]string, l lease) bool {
	desired := projectConfig(l)
	for _, key := range []string{"limits.instances", "limits.cpu", "limits.memory", "limits.disk"} {
		if config[key] != desired[key] {
			return false
		}
	}
	return true
}

func projectFenceEnforced(config map[string]string, l lease) bool {
	for key, value := range projectConfig(l) {
		if config[key] != value {
			return false
		}
	}
	for _, key := range clearedRestrictions {
		if config[key] != "" {
			return false
		}
	}
	return len(unmanagedRestrictions(config, l)) == 0
}

// ensureNetwork gives the lease its own managed bridge with outbound NAT and no
// inbound path. Egress policy belongs to the provider: an instance that could
// pick its own network could pick one that reaches another lease.
func ensureNetwork(ctx context.Context, c *client, l lease) (string, error) {
	name := computeclient.NetworkName(l.Sandbox)
	desired := desiredNetworkConfig(l)
	path := "/1.0/networks/" + name + "?project=default"
	var current network
	err := c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		if err := verifyNetworkOwner(current, l); err != nil {
			return "", err
		}
		// Incus replaces auto with a concrete subnet. Preserve that subnet
		// across applies instead of renumbering running instances.
		for _, family := range []string{"ipv4", "ipv6"} {
			key := family + ".address"
			if desired[key] == "auto" && current.Config[key] != "" && current.Config[key] != "none" {
				desired[key] = current.Config[key]
			}
		}
		merged := map[string]string{}
		for key, value := range current.Config {
			merged[key] = value
		}
		for key, value := range desired {
			merged[key] = value
		}
		if !l.NetworkIPv6 {
			delete(merged, "ipv6.nat")
		}
		if err := c.do(ctx, "PUT", path, network{Config: merged}, nil); err != nil {
			return "", err
		}
	case isNotFound(err):
		if err := c.do(ctx, "POST", "/1.0/networks?project=default", network{
			Name: name, Type: "bridge", Description: "ANAS compute lease network for " + l.Consumer, Config: desired,
		}, nil); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	var actual network
	if err := c.do(ctx, "GET", path, nil, &actual); err != nil {
		return "", fmt.Errorf("read back lease network: %w", err)
	}
	if err := verifyNetworkConfig(actual, l, desired); err != nil {
		return "", err
	}
	// The source fence needs the concrete subnets the daemon just assigned,
	// and must exist before the bridge can name it.
	actual.Name = name
	acl, err := ensureNetworkACL(ctx, c, l, actual)
	if err != nil {
		return "", err
	}
	attach := bridgeACLConfig(acl)
	if !bridgeACLAttached(actual, attach) {
		merged := map[string]string{}
		for key, value := range actual.Config {
			merged[key] = value
		}
		for key, value := range attach {
			merged[key] = value
		}
		if err := c.do(ctx, "PUT", path, network{Config: merged}, nil); err != nil {
			return "", err
		}
		if err := c.do(ctx, "GET", path, nil, &actual); err != nil {
			return "", fmt.Errorf("read back lease network: %w", err)
		}
	}
	if err := verifyNetworkConfig(actual, l, desired); err != nil {
		return "", err
	}
	if !bridgeACLAttached(actual, attach) {
		return "", fmt.Errorf("lease network %s did not attach its source fence ACL", name)
	}
	return name, nil
}

func bridgeACLAttached(n network, attach map[string]string) bool {
	for key, value := range attach {
		if n.Config[key] != value {
			return false
		}
	}
	return true
}

func desiredNetworkConfig(l lease) map[string]string {
	// Both families are NATed through this same managed bridge, so enabling v6
	// widens what a guest can reach without widening how it gets there. What
	// decides it is the host: a v6 network the host cannot route turns every
	// outbound connection into a timeout before it falls back to v4.
	desired := map[string]string{
		"user.anas.consumer": l.Consumer,
		"user.anas.sandbox":  l.Sandbox,
		leaseCredentialKey:   l.Credential,
		"ipv4.address":       "auto",
		"ipv4.nat":           "true",
		"ipv6.address":       "none",
	}
	if l.NetworkIPv6 {
		desired["ipv6.address"] = "auto"
		desired["ipv6.nat"] = "true"
	}
	return desired
}

func verifyNetworkConfig(actual network, l lease, desired map[string]string) error {
	if err := verifyNetworkOwner(actual, l); err != nil {
		return err
	}
	name := computeclient.NetworkName(l.Sandbox)
	for key, expected := range desired {
		value := actual.Config[key]
		if expected == "auto" {
			if value == "" || value == "none" {
				return fmt.Errorf("lease network %s did not configure %s", name, key)
			}
		} else if value != expected {
			return fmt.Errorf("lease network %s did not apply %s", name, key)
		}
	}
	return nil
}

func verifyNetworkOwner(n network, l lease) error {
	// A bridge from before the credential marker is adopted: ensure reaches
	// the network only after proving the project is this lease's alone.
	if n.Type != "bridge" || n.Config["user.anas.consumer"] != l.Consumer || n.Config["user.anas.sandbox"] != l.Sandbox ||
		(n.Config[leaseCredentialKey] != "" && n.Config[leaseCredentialKey] != l.Credential) {
		return fmt.Errorf("lease network %s is not an owned bridge for %s; refusing to adopt or modify it", computeclient.NetworkName(l.Sandbox), l.Sandbox)
	}
	if n.Config["bridge.external_interfaces"] != "" {
		return fmt.Errorf("lease network %s has unmanaged external interfaces", computeclient.NetworkName(l.Sandbox))
	}
	return nil
}

// ensureProfile owns everything about an instance that is not a numeric limit:
// where its root disk lives and what it is plugged into. The consumer names
// this profile but never writes it, which is what stops a caller attaching a
// host path or a second NIC.
func ensureProfile(ctx context.Context, c *client, l lease, bridge string) error {
	desired := desiredLeaseProfile(l, bridge)
	path := "/1.0/profiles/" + computeclient.ProfileName + "?project=" + l.Sandbox
	var current profile
	err := c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		// Replace rather than merge: a device that drifted onto this profile is
		// exactly what must not survive an ensure.
		return c.do(ctx, "PUT", path, desired, nil)
	case isNotFound(err):
		desired.Name = computeclient.ProfileName
		return c.do(ctx, "POST", "/1.0/profiles?project="+l.Sandbox, desired, nil)
	default:
		return err
	}
}

// vmStateVolumeSize is the filesystem volume Incus attaches to every VM root
// block volume. Project limits.disk counts it on top of the root size
// (size.state, default 500MiB), so a VM lease must budget it or a VM using the
// whole per-instance disk quota can never be created. The profile pins it so
// the accounting cannot drift with a daemon default.
const vmStateVolumeSize = "500MiB"

// projectDiskLimit is max_instances times what one instance may occupy.
func projectDiskLimit(l lease) string {
	if l.Isolation == "vm" {
		return fmt.Sprintf("%dMiB", l.MaxInstances*(l.DiskGiB*1024+500))
	}
	return fmt.Sprintf("%dGiB", l.MaxInstances*l.DiskGiB)
}

func desiredLeaseProfile(l lease, bridge string) profile {
	config := map[string]string{"user.anas.managed": "true"}
	if l.Isolation == "container" {
		config["security.nesting"] = "true"
		config["security.privileged"] = "false"
	} else if l.Daemon.VMNesting {
		// Daemons with restricted.virtual-machines.nesting enable nested
		// virtualization on VMs by default and, once it is blocked, refuse
		// any VM that does not set security.nesting=false explicitly. Before
		// that extension the key is container-only and a VM rejects it.
		config["security.nesting"] = "false"
	}
	root := device{"type": "disk", "path": "/", "pool": l.StoragePool}
	if l.Isolation == "vm" {
		root["size.state"] = vmStateVolumeSize
	}
	return profile{
		Description: "ANAS compute lease profile for " + l.Consumer,
		Config:      config,
		Devices: map[string]device{
			"root": root,
			// Require the daemon's managed-NIC source filters in both tiers.
			// These defaults are not a forwarding grant: an independent host
			// authorizer must still reject live per-instance overrides and pin
			// the actual allocation and kernel interface before any permit.
			// security.ipv6_filtering is deliberately absent: Incus refuses to
			// start any instance using it unless the host has br_netfilter with
			// bridge-nf-call-ip6tables=1, which Docker 28+ no longer loads by
			// default. IPv6 source filtering stays an open gap, not a silent one.
			"eth0": {"type": "nic", "network": bridge,
				"security.mac_filtering": "true", "security.ipv4_filtering": "true"},
		},
	}
}

// verifyProfile reads the profile back and refuses anything beyond the two
// devices this contract describes. The daemon does not enforce "no extra
// devices" on a profile, so this assertion is the only thing that does.
func verifyProfile(ctx context.Context, c *client, l lease, bridge string) error {
	var current profile
	if err := c.do(ctx, "GET", "/1.0/profiles/"+computeclient.ProfileName+"?project="+l.Sandbox, nil, &current); err != nil {
		return fmt.Errorf("read back lease profile: %w", err)
	}
	return verifyProfileConfig(current, l, bridge)
}

func verifyProfileConfig(current profile, l lease, bridge string) error {
	if len(current.Devices) != 2 {
		return fmt.Errorf("lease profile %s carries %d devices, want exactly root and eth0", computeclient.ProfileName, len(current.Devices))
	}
	root, nic := current.Devices["root"], current.Devices["eth0"]
	if root["type"] != "disk" || root["path"] != "/" || root["pool"] != l.StoragePool {
		return fmt.Errorf("lease profile root disk is not the managed pool %s", l.StoragePool)
	}
	if root["source"] != "" {
		return fmt.Errorf("lease profile root disk names a host source")
	}
	if nic["type"] != "nic" || nic["network"] != bridge {
		return fmt.Errorf("lease profile NIC is not attached to the managed network")
	}
	if nic["parent"] != "" || nic["nictype"] != "" {
		return fmt.Errorf("lease profile NIC bypasses the managed network")
	}
	desired := desiredLeaseProfile(l, bridge)
	if !reflect.DeepEqual(current.Config, desired.Config) || !reflect.DeepEqual(current.Devices, desired.Devices) {
		return fmt.Errorf("lease profile contains unapproved configuration or device properties")
	}
	return nil
}

func ensure(ctx context.Context, c *client, l lease) (inspectResult, error) {
	var err error
	if l.Daemon, err = readDaemonRestrictions(ctx, c); err != nil {
		return inspectResult{}, err
	}
	// Refuse before changing projects, networks, profiles or certificate trust.
	supported, err := readQuotaPool(ctx, c, l)
	if err != nil {
		return inspectResult{}, err
	}
	if !supported {
		return inspectResult{}, fmt.Errorf("INCUS_STORAGE_POOL %s must be a Created btrfs or zfs pool for enforced disk quotas", l.StoragePool)
	}
	desired := projectConfig(l)
	description := fmt.Sprintf("ANAS compute lease for %s (%s tier)", l.Consumer, l.Isolation)

	var current project
	err = c.do(ctx, "GET", "/1.0/projects/"+l.Sandbox, nil, &current)
	switch {
	case err == nil:
		// Ownership comes first: a project that belongs to another lease, or
		// that another restricted certificate can drive, is not this lease's
		// to converge. Both refusals happen before any write.
		if err := verifyProjectOwner(current.Config, l); err != nil {
			return inspectResult{}, err
		}
		foreign, err := foreignProjectCertificates(ctx, c, l)
		if err != nil {
			return inspectResult{}, err
		}
		if len(foreign) > 0 {
			return inspectResult{}, fmt.Errorf("incus project %s is also trusted by other restricted certificates (%s); confirm they are unused and remove them, or migrate the project explicitly",
				l.Sandbox, strings.Join(foreign, ", "))
		}
		// Existing network-isolated projects may contain OVN networks or
		// running instances. Changing their feature flag is a migration,
		// never an incidental side effect of ensuring a bridge lease.
		if strings.EqualFold(strings.TrimSpace(current.Config["features.networks"]), "true") {
			return inspectResult{}, fmt.Errorf("incus project %s has features.networks enabled; explicit network migration is required", l.Sandbox)
		}
		if unmanaged := unmanagedRestrictions(current.Config, l); len(unmanaged) > 0 {
			return inspectResult{}, fmt.Errorf("incus project %s carries restriction keys this provider does not manage (%s); remove them or migrate the project explicitly",
				l.Sandbox, strings.Join(unmanaged, ", "))
		}
		// Converge rather than recreate: instances may be running in here.
		// A tightened restriction or tier limit that existing instances
		// violate is refused by the daemon, and that refusal fails ensure.
		merged := map[string]string{}
		for key, value := range current.Config {
			merged[key] = value
		}
		for key, value := range desired {
			merged[key] = value
		}
		for _, key := range clearedRestrictions {
			delete(merged, key)
		}
		if err := c.do(ctx, "PUT", "/1.0/projects/"+l.Sandbox, project{Description: description, Config: merged}, nil); err != nil {
			return inspectResult{}, err
		}
	case isNotFound(err):
		if err := c.do(ctx, "POST", "/1.0/projects", project{Name: l.Sandbox, Description: description, Config: desired}, nil); err != nil {
			return inspectResult{}, err
		}
	default:
		return inspectResult{}, err
	}

	// Read back rather than trusting the write. Every later guarantee in this
	// contract rests on these two flags being true on the daemon's own copy.
	result, err := inspectProject(ctx, c, l)
	if err != nil {
		return inspectResult{}, err
	}
	if !result.Restricted {
		return result, fmt.Errorf("incus project %s is not restricted after ensure", l.Sandbox)
	}
	if !result.QuotaEnforced {
		return result, fmt.Errorf("incus project %s has no enforced quota after ensure", l.Sandbox)
	}
	if !result.Ready {
		return result, fmt.Errorf("incus project %s did not apply the complete project fence or exclusive managed network scope", l.Sandbox)
	}
	result.Ready = false
	// Only now that the fence is proven: give the lease its network and profile,
	// then read the profile back. An instance created before this exists would
	// come up with no root disk and no NIC.
	bridge, err := ensureNetwork(ctx, c, l)
	if err != nil {
		return result, err
	}
	if err := ensureProfile(ctx, c, l, bridge); err != nil {
		return result, err
	}
	if err := verifyProfile(ctx, c, l, bridge); err != nil {
		return result, err
	}
	if err := verifyImages(ctx, c, l); err != nil {
		return result, err
	}
	if err := ensureCertificate(ctx, c, l); err != nil {
		return result, err
	}
	result, err = inspect(ctx, c, l)
	if err != nil {
		return result, err
	}
	if !result.Ready {
		return result, fmt.Errorf("incus lease dependencies changed during ensure")
	}
	return result, nil
}

func ensureCertificate(ctx context.Context, c *client, l lease) error {
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		return fmt.Errorf("consumer client certificate: %w", err)
	}
	fingerprint := certificateFingerprint(parsed)

	var existing certificate
	err = c.do(ctx, "GET", "/1.0/certificates/"+fingerprint, nil, &existing)
	if err == nil {
		// Already trusted. Refuse to continue if the existing entry is wider
		// than this lease: silently accepting an unrestricted certificate here
		// would hand the consumer the whole daemon.
		if !existing.Restricted {
			return fmt.Errorf("incus certificate %s is already trusted without a project restriction", short(fingerprint))
		}
		if len(existing.Projects) != 1 || existing.Projects[0] != l.Sandbox {
			return fmt.Errorf("incus certificate %s is scoped to %v, not to %s alone", short(fingerprint), existing.Projects, l.Sandbox)
		}
		return nil
	}
	if !isNotFound(err) {
		return err
	}
	return c.do(ctx, "POST", "/1.0/certificates", certificate{
		Certificate: base64.StdEncoding.EncodeToString(parsed.Raw),
		Name:        "anas-" + l.Consumer,
		Type:        "client",
		Restricted:  true,
		Projects:    []string{l.Sandbox},
	}, nil)
}

func inspectProject(ctx context.Context, c *client, l lease) (inspectResult, error) {
	var current project
	if err := c.do(ctx, "GET", "/1.0/projects/"+l.Sandbox, nil, &current); err != nil {
		if isNotFound(err) {
			return inspectResult{}, nil
		}
		return inspectResult{}, err
	}
	restricted := strings.EqualFold(strings.TrimSpace(current.Config["restricted"]), "true")
	quota := quotaEnforced(current.Config, l)
	if quota {
		supported, err := readQuotaPool(ctx, c, l)
		if err != nil {
			return inspectResult{}, err
		}
		quota = supported
	}
	return inspectResult{
		Exists:        true,
		Ready:         restricted && quota && projectFenceEnforced(current.Config, l),
		Restricted:    restricted,
		QuotaEnforced: quota,
	}, nil
}

// inspect never repairs, imports, grants trust or reads supply files. Readiness
// requires all live dependencies, not the existence of a restricted project.
func inspect(ctx context.Context, c *client, l lease) (inspectResult, error) {
	var err error
	if l.Daemon, err = readDaemonRestrictions(ctx, c); err != nil {
		return inspectResult{}, err
	}
	result, err := inspectProject(ctx, c, l)
	if err != nil || !result.Ready {
		return result, err
	}
	result.Ready = false
	bridge := computeclient.NetworkName(l.Sandbox)
	var n network
	if err := c.do(ctx, "GET", "/1.0/networks/"+bridge+"?project=default", nil, &n); err != nil {
		if isNotFound(err) {
			return result, nil
		}
		return result, err
	}
	if verifyNetworkConfig(n, l, desiredNetworkConfig(l)) != nil || !bridgeACLAttached(n, bridgeACLConfig(bridge)) {
		return result, nil
	}
	n.Name = bridge
	subnets, err := leaseSubnets(n, l)
	if err != nil {
		return result, nil
	}
	var acl networkACL
	if err := c.do(ctx, "GET", "/1.0/network-acls/"+bridge+"?project=default", nil, &acl); err != nil {
		if isNotFound(err) {
			return result, nil
		}
		return result, err
	}
	if verifyACL(acl, l, subnets) != nil {
		return result, nil
	}
	var p profile
	if err := c.do(ctx, "GET", "/1.0/profiles/"+computeclient.ProfileName+"?project="+l.Sandbox, nil, &p); err != nil {
		if isNotFound(err) {
			return result, nil
		}
		return result, err
	}
	if verifyProfileConfig(p, l, bridge) != nil {
		return result, nil
	}
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		return result, fmt.Errorf("consumer client certificate: %w", err)
	}
	fingerprint := certificateFingerprint(parsed)
	var cert certificate
	if err := c.do(ctx, "GET", "/1.0/certificates/"+fingerprint, nil, &cert); err != nil {
		if isNotFound(err) {
			return result, nil
		}
		return result, err
	}
	if cert.Fingerprint != fingerprint || cert.Type != "client" || !cert.Restricted || len(cert.Projects) != 1 || cert.Projects[0] != l.Sandbox {
		return result, nil
	}
	if foreign, err := foreignProjectCertificates(ctx, c, l); err != nil || len(foreign) > 0 {
		return result, err
	}
	architecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[l.ImageArchitecture]
	imageType := map[string]string{"container": "container", "vm": "virtual-machine"}[l.Isolation]
	if architecture == "" || imageType == "" || len(l.ImageAllowlist) == 0 {
		return result, fmt.Errorf("compute image target and frozen allowlist are required")
	}
	for _, pin := range l.ImageAllowlist {
		if err := verifyImage(ctx, c, l.Sandbox, pin, architecture, imageType); err != nil {
			if isNotFound(err) {
				return result, nil
			}
			return result, err
		}
	}
	result.Ready = true
	return result, nil
}

// revoke withdraws the consumer's certificate but leaves the project standing.
// Removing the project would destroy instances the contract never claimed to
// own; withdrawing trust is the part that actually ends the lease.
func revoke(ctx context.Context, c *client, l lease) error {
	parsed, err := decodeCertificate(l.ClientCertPEM)
	if err != nil {
		return fmt.Errorf("consumer client certificate: %w", err)
	}
	err = c.do(ctx, "DELETE", "/1.0/certificates/"+certificateFingerprint(parsed), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

func isNotFound(err error) bool {
	_, ok := err.(notFoundError)
	return ok
}

func short(fingerprint string) string {
	if len(fingerprint) > 12 {
		return fingerprint[:12]
	}
	return fingerprint
}
