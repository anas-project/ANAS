package incusprovision

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

const ForwardingPermissionSchema = "anas.incus-forwarding-permission/v1"

// ForwardingDestination is a deliberately small first production boundary:
// one canonical IPv4 address and one TCP port. It is approved in a host plan,
// not supplied by a consumer or inferred from ingress.allowed_ports. DNS,
// address ranges, UDP and IPv6 need separate authorization and acceptance.
type ForwardingDestination struct {
	IPv4 string `json:"ipv4"`
	Port uint16 `json:"port"`
}

func (d ForwardingDestination) Validate() error {
	ip, err := netip.ParseAddr(d.IPv4)
	if err != nil || !ip.Is4() || ip.String() != d.IPv4 || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || d.Port == 0 {
		return ErrInvalid
	}
	return nil
}

type ForwardingPermissionRequest struct {
	Schema       string                  `json:"schema"`
	WorkspaceID  string                  `json:"workspace_id"`
	Consumer     string                  `json:"consumer"`
	Resource     string                  `json:"resource"`
	Operation    string                  `json:"operation"`
	Destinations []ForwardingDestination `json:"destinations"`
}

func (r ForwardingPermissionRequest) Validate() error {
	if r.Schema != ForwardingPermissionSchema || !pruneIdentifier.MatchString(r.WorkspaceID) ||
		!forwardingLeaseID(r.Consumer) || !forwardingLeaseID(r.Resource) ||
		(r.Operation != "enable" && r.Operation != "disable" && r.Operation != "retire") || len(r.Destinations) > 32 ||
		(r.Operation == "enable" && len(r.Destinations) == 0) || (r.Operation != "enable" && len(r.Destinations) != 0) {
		return ErrInvalid
	}
	seen := map[ForwardingDestination]bool{}
	for _, d := range r.Destinations {
		if d.Validate() != nil || seen[d] {
			return ErrInvalid
		}
		seen[d] = true
	}
	return nil
}

func forwardingLeaseID(value string) bool {
	if value == "" || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func (r ForwardingPermissionRequest) Canonical() (ForwardingPermissionRequest, error) {
	if err := r.Validate(); err != nil {
		return ForwardingPermissionRequest{}, err
	}
	r.Destinations = slices.Clone(r.Destinations)
	if r.Destinations == nil {
		r.Destinations = []ForwardingDestination{}
	}
	slices.SortFunc(r.Destinations, func(a, b ForwardingDestination) int {
		if cmp := strings.Compare(a.IPv4, b.IPv4); cmp != 0 {
			return cmp
		}
		return int(a.Port) - int(b.Port)
	})
	return r, nil
}

func (r ForwardingPermissionRequest) scopeKey() string {
	return digestBytes([]byte(r.WorkspaceID + "\x00" + r.Consumer + "\x00" + r.Resource))[:32]
}

// ForwardingLeaseGrant is derived from trusted Core metadata, the delivered
// lease and local host observations. No caller supplies its project, source IP,
// interface, certificate fingerprint, epoch, ownership ID or bundle digest.
// Possession of this value alone is not authorization; the installed host state
// and the active deployment are rechecked on every reconciliation.
type ForwardingLeaseGrant struct {
	Schema          string                                           `json:"schema"`
	WorkspaceID     string                                           `json:"workspace_id"`
	WorkspaceDigest string                                           `json:"workspace_digest"`
	Epoch           string                                           `json:"epoch"`
	OwnershipID     string                                           `json:"ownership_id"`
	BundleDigest    string                                           `json:"bundle_digest"`
	ServerVersion   string                                           `json:"server_version"`
	Lease           computeingressruntime.IncusLeaseObservationScope `json:"lease"`
	Destinations    []ForwardingDestination                          `json:"destinations"`
	Network         incusingresshost.ForwardingNetworkProof          `json:"network"`
	Routes          []incusingresshost.ForwardingRouteProof          `json:"routes"`
}

func (g ForwardingLeaseGrant) Validate() error {
	r := ForwardingPermissionRequest{Schema: g.Schema, WorkspaceID: g.WorkspaceID, Consumer: g.Lease.Consumer,
		Resource: g.Lease.Resource, Operation: "enable", Destinations: g.Destinations}
	if r.Validate() != nil || g.Lease.Validate() != nil || !strings.HasPrefix(g.WorkspaceDigest, "sha256:") || !digestPattern.MatchString(strings.TrimPrefix(g.WorkspaceDigest, "sha256:")) ||
		!digestPattern.MatchString(g.Epoch) || !digestPattern.MatchString(g.BundleDigest) ||
		!strings.HasPrefix(g.OwnershipID, "anas-incus-") || len(g.OwnershipID) != len("anas-incus-")+32 ||
		len(g.ServerVersion) == 0 || len(g.ServerVersion) > 64 || strings.ContainsAny(g.ServerVersion, " \t\r\n\x00") {
		return ErrInvalid
	}
	for _, char := range strings.TrimPrefix(g.OwnershipID, "anas-incus-") {
		if (char < 'a' || char > 'f') && (char < '0' || char > '9') {
			return ErrInvalid
		}
	}
	canonical, _ := r.Canonical()
	if !slices.Equal(canonical.Destinations, g.Destinations) {
		return ErrInvalid
	}
	if g.Network.Validate() != nil || len(g.Routes) != len(g.Destinations) {
		return ErrInvalid
	}
	for i, route := range g.Routes {
		if route.ValidateFor(g.Network) != nil || route.Destination != g.Destinations[i].IPv4 || route.Port != g.Destinations[i].Port {
			return ErrInvalid
		}
	}
	return nil
}

type ForwardingPermissionBinding struct {
	Schema      string `json:"schema"`
	WorkspaceID string `json:"workspace_id"`
	PlanDigest  string `json:"plan_digest"`
	StateDigest string `json:"state_digest"`
}

func (b ForwardingPermissionBinding) Validate() error {
	if b.Schema != ForwardingPermissionSchema || !pruneIdentifier.MatchString(b.WorkspaceID) ||
		!digestPattern.MatchString(b.PlanDigest) || !digestPattern.MatchString(b.StateDigest) {
		return ErrInvalid
	}
	return nil
}

type ForwardingPermissionPlan struct {
	Schema       string                `json:"schema"`
	WorkspaceID  string                `json:"workspace_id"`
	Consumer     string                `json:"consumer"`
	Resource     string                `json:"resource"`
	Operation    string                `json:"operation"`
	ScopeKey     string                `json:"scope_key"`
	Grant        *ForwardingLeaseGrant `json:"grant,omitempty"`
	GrantDigest  string                `json:"grant_digest,omitempty"`
	StateDigest  string                `json:"state_digest"`
	Digest       string                `json:"digest"`
	Blockers     []string              `json:"blockers"`
	Warnings     []string              `json:"warnings"`
	RequiresRoot bool                  `json:"requires_root"`
	Generation   uint64                `json:"generation"`
}

func (p ForwardingPermissionPlan) Validate() error {
	r := ForwardingPermissionRequest{Schema: p.Schema, WorkspaceID: p.WorkspaceID, Consumer: p.Consumer,
		Resource: p.Resource, Operation: p.Operation}
	if p.Grant != nil {
		r.Destinations = p.Grant.Destinations
	}
	if r.Validate() != nil || p.ScopeKey != r.scopeKey() || !p.RequiresRoot ||
		!digestPattern.MatchString(p.StateDigest) || !digestPattern.MatchString(p.Digest) ||
		p.Generation == 0 || len(p.Blockers) > 32 || len(p.Warnings) > 32 {
		return ErrInvalid
	}
	if p.Operation == "enable" && (p.Grant == nil || p.Grant.Validate() != nil || p.Grant.WorkspaceID != p.WorkspaceID ||
		p.Grant.Lease.Consumer != p.Consumer || p.Grant.Lease.Resource != p.Resource || p.GrantDigest != stableDigest(p.Grant)) {
		return ErrInvalid
	}
	if p.Operation != "enable" && (p.Grant != nil || p.GrantDigest != "") {
		return ErrInvalid
	}
	for _, value := range append(slices.Clone(p.Blockers), p.Warnings...) {
		if !forwardingMessageCode(value) {
			return ErrInvalid
		}
	}
	copy := p
	copy.Digest = ""
	if stableDigest(copy) != p.Digest {
		return ErrInvalid
	}
	return nil
}

func forwardingMessageCode(code string) bool {
	if code == "" || len(code) > 128 {
		return false
	}
	for _, char := range code {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func validateForwardingGrantForRequest(g ForwardingLeaseGrant, r ForwardingPermissionRequest) error {
	if g.Validate() != nil || r.Validate() != nil || r.Operation != "enable" || g.WorkspaceID != r.WorkspaceID ||
		g.Lease.Consumer != r.Consumer || g.Lease.Resource != r.Resource || !slices.Equal(g.Destinations, r.Destinations) {
		return fmt.Errorf("forwarding grant is not the approved resource and destination scope")
	}
	return nil
}
