// Package incusingresshost implements the concrete Linux host-side HTTP
// ingress mutations for Incus-backed compute publications. It deliberately
// accepts typed targets only; it never accepts argv, nft text, URLs or target
// addresses from a consumer request.
package incusingresshost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Lease struct {
	Consumer string `json:"consumer"`
	Resource string `json:"resource"`
}

// Target is the trusted action input produced by the mediator/executor. The
// backend still treats IP, MAC, UUID and incarnation as expectations and
// re-resolves them through Resolver before every mutation.
type Target struct {
	Scope               string `json:"scope"`
	Epoch               string `json:"epoch"`
	Incarnation         string `json:"incarnation"`
	Reservation         string `json:"reservation"`
	Deployment          string `json:"deployment"`
	Lease               Lease  `json:"lease"`
	InstanceID          string `json:"instance_id"`
	InstanceUUID        string `json:"instance_uuid"`
	GuestPort           uint16 `json:"guest_port"`
	GuestIP             string `json:"guest_ip"`
	NICMAC              string `json:"nic_mac"`
	ServerUUID          string `json:"server_uuid"`
	HostVethName        string `json:"host_veth_name"`
	HostVethMAC         string `json:"host_veth_mac"`
	HostVethPeerIfIndex uint32 `json:"host_veth_peer_ifindex"`
}

type Identity struct {
	InstanceUUID        string `json:"instance_uuid"`
	Incarnation         string `json:"incarnation"`
	GuestIP             string `json:"guest_ip"`
	NICMAC              string `json:"nic_mac"`
	ServerUUID          string `json:"server_uuid"`
	HostVethName        string `json:"host_veth_name"`
	HostVethMAC         string `json:"host_veth_mac"`
	HostVethPeerIfIndex uint32 `json:"host_veth_peer_ifindex"`
	State               string `json:"state"`
}

// Resolver is the exact read-only projection required from the parent hostd
// action registry. It must resolve Scope+Lease+InstanceID+Port from installed
// topology and fresh Incus allocation state, not from consumer input.
type Resolver interface {
	ResolveHTTPIdentity(context.Context, Target) (Identity, error)
}

type backendConfig struct {
	ScopeName                string
	ReceiptDir               string
	RouteNetNS               string
	RouteTable               uint32
	RouteProtocol            uint8
	PermitTable              string
	OriginTable              string
	GuestBridge              string
	IngressBridge            string
	TraefikVeth              string
	TraefikInterface         string
	TraefikSourceIP          string
	IngressGateway           string
	GuestSubnet              string
	Namespace                RouteNamespacePin
	AddressRouting           *AddressRouting
	PermitTTL                time.Duration
	CommandTimeout           time.Duration
	Binaries                 trustedBinaries
	Resolver                 Resolver
	fixture                  bool
	productionDisabledReason string
}

type trustedBinaries struct {
	IP        string
	NFT       string
	Conntrack string
}

// RouteNamespacePin is installed by the root-owned scope configuration. It
// binds the namespace and Traefik-side veth identity that every route and
// firewall effect must recheck. Tests use the same shape in private fixtures.
type RouteNamespacePin struct {
	PID                 int    `json:"pid,omitempty"`
	StartTimeTicks      uint64 `json:"start_time_ticks,omitempty"`
	BootID              string `json:"boot_id,omitempty"`
	NetNSCookie         uint64 `json:"netns_cookie"`
	NetNSDevice         uint64 `json:"netns_device"`
	NetNSInode          uint64 `json:"netns_inode"`
	DockerContainerID   string `json:"docker_container_id"`
	DockerStartedAt     string `json:"docker_started_at"`
	TraefikIfIndex      uint32 `json:"traefik_ifindex"`
	TraefikMAC          string `json:"traefik_mac"`
	HostVethPeerIfIndex uint32 `json:"host_veth_peer_ifindex"`
	HostVethMAC         string `json:"host_veth_mac"`
	HostVethIfIndex     uint32 `json:"host_veth_ifindex,omitempty"`
}

type Backend struct {
	config          backendConfig
	runner          commandRunner
	addressCommands addressCommandExecutor // private fault-injection seam
}

const (
	defaultPermitTTL      = 30 * time.Second
	defaultCommandTimeout = 5 * time.Second
	receiptSchema         = "anas.incus-http-host-receipt/v2"
)

func newBackend(config backendConfig) (*Backend, error) {
	if config.PermitTTL == 0 {
		config.PermitTTL = defaultPermitTTL
	}
	if config.CommandTimeout == 0 {
		config.CommandTimeout = defaultCommandTimeout
	}
	if config.Resolver == nil {
		return nil, fmt.Errorf("ingress host backend requires a fresh identity resolver")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	runner, err := newCommandRunner(config)
	if err != nil {
		return nil, err
	}
	return &Backend{config: config, runner: runner}, nil
}

func (b *Backend) HoldAddress(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		receipt, err := b.prepare(ctx, target)
		if err != nil {
			return err
		}
		if b.config.AddressRouting != nil {
			if err := b.verifyBaseline(ctx); err != nil {
				return err
			}
			if _, err := b.nftInventory(ctx); err != nil {
				return err
			}
			guard, err := b.addressRouter()
			if err != nil {
				return err
			}
			if !receipt.AddressHeld {
				if err := guard.admitAttempt(ctx, target); err != nil {
					return err
				}
				receipt.AddressIntent = true
				receipt.Generation++
				if err := b.saveReceipt(ctx, receipt); err != nil {
					return err
				}
			}
			if err := guard.holdLocked(ctx, target); err != nil {
				return err
			}
		} else if !b.config.fixture {
			return fmt.Errorf("address routing is not installed; refusing a journal-only hold")
		}
		receipt.AddressHeld = true
		receipt.AddressIntent = false
		receipt.Generation++
		return b.saveReceipt(ctx, receipt)
	})
}

func (b *Backend) EnsureGuestRoute(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		receipt, err := b.prepare(ctx, target)
		if err != nil {
			return err
		}
		if err := b.verifyAddressRouting(ctx, target); err != nil {
			return err
		}
		receipt.RouteIntent = true
		receipt.Generation++
		if err := b.saveReceipt(ctx, receipt); err != nil {
			return err
		}
		if err := b.ensureGuestRouteEffect(ctx, target); err != nil {
			return err
		}
		if err := b.confirmRoute(ctx, target, true); err != nil {
			return err
		}
		receipt.RouteReady = true
		receipt.RouteIntent = false
		receipt.Generation++
		return b.saveReceipt(ctx, receipt)
	})
}

func (b *Backend) EnsureHTTPPermit(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		if err := b.ensureProductionOpen(); err != nil {
			return err
		}
		receipt, err := b.prepare(ctx, target)
		if err != nil {
			return err
		}
		if err := b.verifyAddressRouting(ctx, target); err != nil {
			return err
		}
		receipt.PermitIntent = true
		receipt.Generation++
		if err := b.saveReceipt(ctx, receipt); err != nil {
			return err
		}
		if err := b.verifyBaseline(ctx); err != nil {
			return err
		}
		script, err := b.permitCreateScript(ctx, target)
		if err != nil {
			return err
		}
		if err := b.applyNFTScript(ctx, script); err != nil {
			return fmt.Errorf("ensure owned HTTP permit")
		}
		if err := b.confirmPermit(ctx, target, true); err != nil {
			return err
		}
		if err := b.verifyAddressRouting(ctx, target); err != nil {
			return err
		}
		receipt.PermitReady = true
		receipt.PermitIntent = false
		receipt.PermitExpiresAt = time.Now().UTC().Add(b.config.PermitTTL)
		receipt.Generation++
		return b.saveReceipt(ctx, receipt)
	})
}

func (b *Backend) RemoveHTTPPermit(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		receipt, err := b.loadReceipt(target)
		if err != nil {
			return err
		}
		if !receipt.PermitReady && !receipt.PermitIntent {
			return b.confirmPermit(ctx, target, false)
		}
		receipt.PermitIntent = true
		receipt.Generation++
		if err := b.saveReceipt(ctx, receipt); err != nil {
			return err
		}
		script, err := b.permitRemoveScript(ctx, target)
		if err != nil {
			return err
		}
		if err := b.applyNFTScript(ctx, script); err != nil {
			return fmt.Errorf("remove owned HTTP permit")
		}
		if err := b.confirmPermit(ctx, target, false); err != nil {
			return err
		}
		receipt.PermitReady = false
		receipt.PermitIntent = false
		receipt.PermitExpiresAt = time.Time{}
		receipt.Generation++
		return b.saveReceipt(ctx, receipt)
	})
}

func (b *Backend) CloseHTTPConnections(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		if err := b.validateTarget(target); err != nil {
			return err
		}
		if _, err := b.loadReceipt(target); err != nil {
			return err
		}
		return b.closeHTTPConnectionsLocked(ctx, target)
	})
}

func (b *Backend) RemoveGuestRoute(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		receipt, err := b.loadReceipt(target)
		if err != nil {
			return err
		}
		if !receipt.RouteReady && !receipt.RouteIntent {
			shared, err := b.otherReadyRouteReceipt(target)
			if err != nil {
				return err
			}
			if shared {
				return nil
			}
			return b.confirmRoute(ctx, target, false)
		}
		receipt.RouteIntent = true
		receipt.Generation++
		if err := b.saveReceipt(ctx, receipt); err != nil {
			return err
		}
		keepRoute, err := b.otherReadyRouteReceipt(target)
		if err != nil {
			return err
		}
		if !keepRoute {
			count, err := b.routeMatchCount(ctx, target)
			if err != nil {
				return err
			}
			if count > 0 {
				if err := b.withVerifiedRouteNamespace(ctx, func() error {
					return b.runner.run(ctx, b.config.Binaries.IP, b.routeDeleteArgv(target), nil)
				}); err != nil {
					return fmt.Errorf("remove owned guest /32 route")
				}
			}
			if err := b.confirmRoute(ctx, target, false); err != nil {
				return err
			}
		}
		receipt.RouteReady = false
		receipt.RouteIntent = false
		receipt.Generation++
		return b.saveReceipt(ctx, receipt)
	})
}

func (b *Backend) ReleaseAddress(ctx context.Context, target Target) error {
	return b.withGuard(ctx, func() error {
		receipt, err := b.loadReceipt(target)
		if err != nil {
			return err
		}
		if receipt.RouteReady || receipt.PermitReady || receipt.RouteIntent || receipt.PermitIntent {
			return fmt.Errorf("refuse to release ingress address hold before route and permit cleanup")
		}
		candidates, err := b.receiptedTargets()
		if err != nil {
			return err
		}
		if err := b.checkHTTPArtifactsLocked(ctx, candidates); err != nil {
			return fmt.Errorf("retain ingress address hold while artifact inventory is incomplete: %w", err)
		}
		if b.config.AddressRouting != nil {
			guard, err := b.addressRouter()
			if err != nil {
				return err
			}
			if err := guard.releaseLocked(ctx, target); err != nil {
				return err
			}
		}
		return b.removeReceipt(target)
	})
}

func (b *Backend) prepare(ctx context.Context, target Target) (receipt, error) {
	if err := b.validateFresh(ctx, target); err != nil {
		return receipt{}, err
	}
	if err := b.checkAddressOwner(target); err != nil {
		return receipt{}, err
	}
	existing, err := b.loadReceipt(target)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, errReceiptMissing) {
		return receipt{}, err
	}
	return receipt{
		Schema:         receiptSchema,
		Scope:          b.config.ScopeName,
		Target:         target,
		TargetDigest:   targetDigest(target),
		ScopeDigest:    ingressScopeDigest(b.config),
		PermitSet:      permitSetName(target),
		PermitComment:  permitComment(target),
		RouteTable:     b.config.RouteTable,
		RouteProtocol:  b.config.RouteProtocol,
		CreatedAt:      time.Now().UTC(),
		LastObservedAt: time.Now().UTC(),
	}, nil
}

func (b *Backend) validateFresh(ctx context.Context, target Target) error {
	if err := b.validateTarget(target); err != nil {
		return err
	}
	identity, err := b.config.Resolver.ResolveHTTPIdentity(ctx, target)
	if err != nil {
		return fmt.Errorf("resolve fresh Incus HTTP identity: %w", err)
	}
	if identity.State != "Running" || identity.InstanceUUID != target.InstanceUUID || identity.Incarnation != target.Incarnation || identity.GuestIP != target.GuestIP || identity.NICMAC != target.NICMAC || identity.ServerUUID != target.ServerUUID || identity.HostVethName != target.HostVethName || identity.HostVethMAC != target.HostVethMAC || identity.HostVethPeerIfIndex != target.HostVethPeerIfIndex {
		return fmt.Errorf("fresh Incus HTTP identity does not match the trusted target")
	}
	return nil
}

func (b *Backend) validateTarget(target Target) error {
	if err := validateTarget(target); err != nil {
		return err
	}
	if target.Scope != b.config.ScopeName {
		return fmt.Errorf("ingress target is outside the installed scope")
	}
	subnet := netip.MustParsePrefix(b.config.GuestSubnet)
	ip := netip.MustParseAddr(target.GuestIP)
	if !subnet.Contains(ip) {
		return fmt.Errorf("ingress target guest IP is outside the installed guest subnet")
	}
	return nil
}

func validateConfig(c backendConfig) error {
	if !scopeName.MatchString(c.ScopeName) || c.ReceiptDir == "" || c.RouteNetNS == "" || !nftName.MatchString(c.PermitTable) || !nftName.MatchString(c.OriginTable) {
		return fmt.Errorf("invalid ingress host scope configuration")
	}
	if c.RouteTable == 0 || c.RouteTable == 253 || c.RouteTable == 254 || c.RouteTable == 255 || c.RouteProtocol == 0 {
		return fmt.Errorf("ingress host route table/protocol must be explicit and non-default")
	}
	for _, iface := range []string{c.GuestBridge, c.IngressBridge, c.TraefikVeth, c.TraefikInterface} {
		if !ifaceName.MatchString(iface) {
			return fmt.Errorf("invalid ingress host interface pin")
		}
	}
	source, err := netip.ParseAddr(c.TraefikSourceIP)
	if err != nil || !source.Is4() || !source.IsPrivate() || source.String() != c.TraefikSourceIP {
		return fmt.Errorf("invalid ingress Traefik source IPv4")
	}
	gateway, err := netip.ParseAddr(c.IngressGateway)
	if err != nil || !gateway.Is4() || !gateway.IsPrivate() || gateway.String() != c.IngressGateway || gateway == source {
		return fmt.Errorf("invalid ingress gateway IPv4")
	}
	subnet, err := netip.ParsePrefix(c.GuestSubnet)
	if err != nil || !subnet.Addr().Is4() || !subnet.Addr().IsPrivate() || subnet.Bits() < 16 || subnet.Bits() > 30 || subnet.String() != c.GuestSubnet {
		return fmt.Errorf("invalid ingress guest IPv4 subnet")
	}
	if err := validateNamespacePin(c.Namespace); err != nil {
		return err
	}
	if c.PermitTTL < time.Second || c.PermitTTL > time.Minute || c.CommandTimeout < time.Second || c.CommandTimeout > 30*time.Second {
		return fmt.Errorf("invalid ingress host timeout")
	}
	return nil
}

func validateNamespacePin(pin RouteNamespacePin) error {
	if pin.NetNSCookie == 0 || pin.NetNSDevice == 0 || pin.NetNSInode == 0 || !containerID.MatchString(pin.DockerContainerID) || pin.DockerStartedAt == "" || len(pin.DockerStartedAt) > 128 || pin.TraefikIfIndex == 0 || pin.HostVethPeerIfIndex == 0 || !macAddress.MatchString(pin.TraefikMAC) || !macAddress.MatchString(pin.HostVethMAC) {
		return fmt.Errorf("invalid ingress route namespace identity pin")
	}
	return nil
}

func validateTarget(t Target) error {
	if !scopeName.MatchString(t.Scope) || !hex64(t.Epoch) || !hex64(t.Incarnation) || !reservationID.MatchString(t.Reservation) {
		return fmt.Errorf("invalid ingress host target identity")
	}
	if !leaseID.MatchString(t.Lease.Consumer) || !leaseID.MatchString(t.Lease.Resource) || t.Deployment == "" || len(t.Deployment) > 128 || t.InstanceID == "" || len(t.InstanceID) > 128 || !uuidString.MatchString(t.InstanceUUID) || t.GuestPort == 0 || !macAddress.MatchString(t.NICMAC) || !uuidString.MatchString(t.ServerUUID) || !ifaceName.MatchString(t.HostVethName) || !macAddress.MatchString(t.HostVethMAC) || t.HostVethPeerIfIndex == 0 {
		return fmt.Errorf("invalid ingress host target fields")
	}
	ip, err := netip.ParseAddr(t.GuestIP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != t.GuestIP {
		return fmt.Errorf("ingress host target requires canonical private IPv4")
	}
	return nil
}

func targetDigest(target Target) string {
	// Target has only fixed, JSON-serializable fields. Bind the entire DTO so
	// adding a lease or instance identity field cannot silently omit it here.
	body, _ := json.Marshal(target)
	sum := sha256.Sum256(append([]byte("anas.incus-http-target/v2\x00"), body...))
	return hex.EncodeToString(sum[:])
}

func permitSetName(target Target) string {
	sum := sha256.Sum256([]byte(target.Scope + "\x00" + target.Reservation))
	return "p_" + hex.EncodeToString(sum[:10])
}

func permitComment(target Target) string {
	return "anas:v1:" + target.Reservation + ":" + targetDigest(target)[:32]
}

func hex64(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

func (b *Backend) ttlSeconds() string {
	seconds := int(b.config.PermitTTL.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds) + "s"
}

var (
	scopeName     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	leaseID       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	ifaceName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)
	nftName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	reservationID = regexp.MustCompile(`^[a-f0-9]{32}:[1-9][0-9]{0,19}$`)
	macAddress    = regexp.MustCompile(`^(?:[a-f0-9]{2}:){5}[a-f0-9]{2}$`)
	uuidString    = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	containerID   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

func shellQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
