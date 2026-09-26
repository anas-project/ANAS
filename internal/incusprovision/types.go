// Package incusprovision implements the trusted Incus host provisioning action
// backend. Public requests are typed plans and confirmed plan bindings; callers
// never provide commands, argv, scripts, executable paths, firewall text or
// root callbacks.
package incusprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/anas-project/ANAS/internal/incushost"
)

const (
	Schema       = "anas.incus-host-provision/v1"
	StateSchema  = "anas.incus-host-state/v1"
	BundleSchema = "anas.incus-connection-bundle/v1"

	DefaultStatePath       = "/var/lib/anas/incus-host/state.json"
	DefaultBundlePath      = "/var/lib/anas/incus-host/connection.json"
	DefaultRelayConfigPath = "/etc/anas/incus-control-relay.json"

	IncusHTTPSAddress  = "127.0.0.1:8443"
	ControlNetworkName = "anas-incus-control"
	StoragePoolName    = "anas-btrfs"
	ManagementCertName = "anas-host-provisioning"
	RelayServiceName   = "anas-incus-control-relay.service"
	RelayBinaryPath    = "/usr/local/lib/anas/anas-incus-control-relay"
	RelayUserName      = "anas-incus-relay"
	RelayGroupName     = "anas-incus-relay"

	// InstallTimeout bounds the complete confirmed install action, not an
	// individual query. It accommodates the two bounded APT calls (15m each),
	// the packaged Incus startup (11m including systemctl), and 4m of readback
	// and persistence allowance. Caller cancellation can always shorten it.
	InstallTimeout = 45 * time.Minute
)

var (
	ErrInvalid         = errors.New("invalid Incus host provisioning request")
	ErrDrift           = errors.New("Incus host provisioning plan drifted; inspect and confirm a fresh plan")
	ErrUnconfirmed     = errors.New("Incus host provisioning requires a confirmed plan binding")
	ErrBlocked         = errors.New("Incus host provisioning is blocked by current host state")
	ErrUnsupported     = errors.New("Incus host provisioning is unsupported on this host")
	ErrUnsafeState     = errors.New("Incus host provisioning state is unavailable or unsafe")
	ErrIncomplete      = errors.New("Incus host provisioning observation is incomplete")
	ErrExternalEffects = errors.New("Incus host provisioning effect could not be confirmed; inspect and recover before retrying")

	digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Request is intentionally small and typed. It chooses a supported isolation
// tier and a compiled package mirror preference; it does not name paths,
// commands, subnets, firewall rules, package names, repositories or daemon
// endpoints. Uninstall always removes the packages ANAS recorded as its own.
type Request struct {
	Skip           bool   `json:"skip,omitempty"`
	Interface      string `json:"interface,omitempty"`
	StorageSizeGiB int    `json:"storage_size_gib,omitempty"`
	ChineseSpeedup bool   `json:"chinese_speedup,omitempty"`
}

func (r Request) normalized() (Request, error) {
	if r.Interface == "" {
		r.Interface = "incus_container"
	}
	if r.Interface != "incus_container" && r.Interface != "incus_vm" {
		return Request{}, ErrInvalid
	}
	if r.StorageSizeGiB == 0 {
		r.StorageSizeGiB = 64
	}
	if r.StorageSizeGiB < 16 || r.StorageSizeGiB > 4096 {
		return Request{}, ErrInvalid
	}
	return r, nil
}

type Phase string

const (
	PhaseInstall   Phase = "install"
	PhaseConfigure Phase = "configure"
	PhaseEnroll    Phase = "enroll"
	PhaseUninstall Phase = "uninstall"
)

func (p Phase) valid() bool {
	return p == PhaseInstall || p == PhaseConfigure || p == PhaseEnroll || p == PhaseUninstall
}

// Binding is the second confirmation object supplied by the parent action
// owner. Every mutating phase reobserves the host and recomputes this digest.
type Binding struct {
	Schema      string `json:"schema"`
	PlanDigest  string `json:"plan_digest"`
	Phase       Phase  `json:"phase"`
	Destructive bool   `json:"destructive"`
}

func (b Binding) validate(phase Phase, digest string) error {
	if b.Schema != Schema || !phase.valid() || b.Phase != phase || !b.Destructive || !digestPattern.MatchString(b.PlanDigest) {
		return ErrUnconfirmed
	}
	if b.PlanDigest != digest {
		return ErrDrift
	}
	return nil
}

type Plan struct {
	Schema            string           `json:"schema"`
	Digest            string           `json:"digest"`
	Request           Request          `json:"request"`
	ObservationDigest string           `json:"observation_digest"`
	StateDigest       string           `json:"state_digest"`
	Preflight         incushost.Report `json:"preflight"`
	Disposition       string           `json:"disposition"`
	ComputeReady      bool             `json:"compute_ready"`
	Steps             []Step           `json:"steps"`
	Blockers          []string         `json:"blockers,omitempty"`
	Warnings          []string         `json:"warnings,omitempty"`
}

type Step struct {
	Phase       Phase  `json:"phase"`
	ID          string `json:"id"`
	Effect      string `json:"effect"`
	Owned       bool   `json:"owned"`
	Destructive bool   `json:"destructive"`
	Skipped     bool   `json:"skipped,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type InspectResult struct {
	Schema      string      `json:"schema"`
	ObservedAt  time.Time   `json:"observed_at"`
	Observation Observation `json:"observation"`
	State       PublicState `json:"state"`
	Plan        Plan        `json:"plan"`
}

type ApplyResult struct {
	Schema          string              `json:"schema"`
	Phase           Phase               `json:"phase"`
	PlanDigest      string              `json:"plan_digest"`
	Disposition     string              `json:"disposition"`
	ComputeReady    bool                `json:"compute_ready"`
	ConnectionReady bool                `json:"connection_ready,omitempty"`
	Receipts        []Receipt           `json:"receipts"`
	Blockers        []string            `json:"blockers,omitempty"`
	Unsupported     []UnsupportedOutput `json:"unsupported,omitempty"`
}

type UnsupportedOutput struct {
	Feature string `json:"feature"`
	Reason  string `json:"reason"`
}

type Observation struct {
	Preflight             incushost.Report      `json:"preflight"`
	Forwarding            ForwardingObservation `json:"forwarding"`
	PackageInstalled      bool                  `json:"package_installed"`
	ExistingPackages      []string              `json:"existing_packages"`
	InstalledPackages     []string              `json:"installed_packages"`
	IncusDaemonActive     bool                  `json:"incus_daemon_active"`
	IncusHTTPSLoopback    bool                  `json:"incus_https_loopback"`
	StoragePoolExists     bool                  `json:"storage_pool_exists"`
	DockerNetworkExists   bool                  `json:"docker_network_exists"`
	FirewallInstalled     bool                  `json:"firewall_installed"`
	RelayInstalled        bool                  `json:"relay_installed"`
	RelayBinaryInstalled  bool                  `json:"relay_binary_installed"`
	ManagementTrusted     bool                  `json:"management_trusted"`
	EndpointVerified      bool                  `json:"connection_verified"`
	RunningManagedGuests  int                   `json:"running_managed_guests"`
	ExternalCIDRs         []string              `json:"external_cidrs,omitempty"`
	DockerCIDRs           []string              `json:"docker_cidrs,omitempty"`
	IncusCIDRs            []string              `json:"incus_cidrs,omitempty"`
	ControlSubnet         string                `json:"control_subnet,omitempty"`
	ControlGateway        string                `json:"control_gateway,omitempty"`
	ControlInterfaceName  string                `json:"control_interface_name,omitempty"`
	ControlInterfaceIndex int                   `json:"control_interface_index,omitempty"`
	ControlNetworkID      string                `json:"control_network_id,omitempty"`
	RelayUID              uint32                `json:"relay_uid,omitempty"`
	RelayGID              uint32                `json:"relay_gid,omitempty"`
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
