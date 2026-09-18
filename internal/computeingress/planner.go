package computeingress

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Facts must come from the mediator's observer, never from a consumer request.
// The observer is responsible for daemon pin/identity, restricted-project/NIC
// policy, managed-network ownership and a fresh allocation read. This value is
// an observation, not a reservation or a promise that an IP cannot change.
type Facts struct {
	Project      string
	Interface    string
	InstanceID   string
	InstanceUUID string
	// Incarnation binds the observed generation and last start time. Runtime
	// execution requires it even when a restart preserves UUID, MAC and IP.
	Incarnation   string
	State         string
	NetworkOwner  string
	GuestIP       string
	AllocationIP  string
	GuestMAC      string
	AllocationMAC string
}

type Lease struct {
	Consumer string `json:"consumer"`
	Resource string `json:"resource"`
}

// Publication is a planned target, not an applied-network receipt. In
// particular, constructing it never authorizes IP reuse or restores disk state.
type Publication struct {
	Reservation  string `json:"reservation"`
	Deployment   string `json:"deployment"`
	Lease        Lease  `json:"lease"`
	InstanceID   string `json:"instance_id"`
	InstanceUUID string `json:"instance_uuid"`
	WorkloadID   string `json:"workload_id"`
	GuestPort    uint16 `json:"guest_port"`
	Label        string `json:"label,omitempty"`
	Host         string `json:"host"`
	GuestIP      string `json:"guest_ip"`
	Auth         string `json:"auth"`
	Middleware   string `json:"middleware,omitempty"`
}

// Planner serializes hostname reservations in a single mediator session.
// It starts empty, never imports disk requests or old grants as authority, and
// is discarded on deployment changes. The executor must retain a reservation
// until route/permit/connections are confirmed absent before Retire is called.
type Planner struct {
	mu       sync.Mutex
	active   string
	session  string
	sequence uint64
	grants   map[Lease]*Authorization
	hosts    map[string]Publication
}

func NewPlanner(activeDeployment string, grants []*Authorization, reservedHosts []string) (*Planner, error) {
	if activeDeployment == "" {
		return nil, fmt.Errorf("HTTP mediation requires an active deployment")
	}
	if err := ValidateNamespaces(grants, reservedHosts); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("cannot create HTTP planning session")
	}
	p := &Planner{session: hex.EncodeToString(nonce[:]), active: activeDeployment, grants: map[Lease]*Authorization{}, hosts: map[string]Publication{}}
	for _, a := range grants {
		if a.Deployment != activeDeployment {
			return nil, fmt.Errorf("HTTP authorization does not belong to the active deployment")
		}
		p.grants[Lease{a.Consumer, a.Resource}] = a.Clone()
	}
	return p, nil
}

var macAddress = regexp.MustCompile(`^(?:[a-f0-9]{2}:){5}[a-f0-9]{2}$`)

func (p *Planner) grant(lease Lease, request Request) (*Authorization, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	a := p.grants[lease]
	if a == nil {
		return nil, fmt.Errorf("request directory has no active HTTP authorization")
	}
	if !strings.HasPrefix(request.InstanceID, a.InstancePrefix) || len(request.InstanceID) <= len(a.InstancePrefix) || !slices.Contains(a.Policy.AllowedPorts, request.GuestPort) {
		return nil, fmt.Errorf("HTTP request instance or port is outside its lease")
	}
	return a, nil
}

func (p *Planner) Reserve(lease Lease, request Request, facts Facts, secret string) (Publication, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	empty := Publication{}
	a, err := p.grant(lease, request)
	if err != nil {
		return empty, err
	}
	if request.Action != "publish" {
		return empty, fmt.Errorf("only publish requests reserve HTTP names")
	}
	if facts.Project != a.Project || facts.Interface != a.Interface || facts.InstanceID != request.InstanceID || facts.InstanceUUID == "" || facts.State != "Running" || facts.NetworkOwner != a.Consumer {
		return empty, fmt.Errorf("HTTP target is not the observed running instance of this lease")
	}
	ip, err := netip.ParseAddr(facts.GuestIP)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || facts.GuestIP != facts.AllocationIP || !macAddress.MatchString(facts.GuestMAC) || facts.GuestMAC != facts.AllocationMAC {
		return empty, fmt.Errorf("HTTP target does not match its observed private IPv4 allocation")
	}
	host, err := a.Host(request.WorkloadID, request.Label, secret)
	if err != nil {
		return empty, err
	}
	pub := Publication{Deployment: p.active, Lease: lease, InstanceID: request.InstanceID, InstanceUUID: facts.InstanceUUID, WorkloadID: request.WorkloadID, GuestPort: request.GuestPort, Label: request.Label, Host: host, GuestIP: ip.String(), Auth: a.Policy.Auth}
	if a.ForwardAuth != nil {
		pub.Middleware = a.ForwardAuth.Middleware
	}
	if old, exists := p.hosts[host]; exists {
		pub.Reservation = old.Reservation
		if old == pub {
			return old, nil
		}
		return empty, fmt.Errorf("HTTP hostname is already reserved; retire the old publication before replacing it")
	}
	// One lease/instance/port has one route. Relabelling a request must not
	// orphan its previous host, even if the new name happens to be free.
	for _, old := range p.hosts {
		if old.Lease == lease && old.InstanceID == request.InstanceID && old.GuestPort == request.GuestPort {
			return empty, fmt.Errorf("HTTP instance port already has a reservation")
		}
	}
	p.sequence++
	pub.Reservation = p.session + ":" + strconv.FormatUint(p.sequence, 10)
	p.hosts[host] = pub
	return pub, nil
}

// Withdrawal uses the recorded identity; a stopped/deleted guest cannot be
// required to pass Running checks before its old route can be removed.
func (p *Planner) Withdrawal(lease Lease, request Request) (Publication, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.grant(lease, request); err != nil {
		return Publication{}, false, err
	}
	if request.Action != "revoke" {
		return Publication{}, false, fmt.Errorf("withdrawal requires a revoke request")
	}
	for _, old := range p.hosts {
		if old.Lease == lease && old.InstanceID == request.InstanceID && old.GuestPort == request.GuestPort {
			if old.WorkloadID != request.WorkloadID || old.Label != request.Label {
				return Publication{}, false, fmt.Errorf("HTTP revoke request does not identify the reserved publication")
			}
			return old, true, nil
		}
	}
	return Publication{}, false, nil
}

// Retire is a trusted-executor callback after confirmed cleanup, not a consumer
// request. Full-value matching stops a stale completion from clearing a newer
// reservation at the same hostname.
func (p *Planner) Retire(publication Publication) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, exists := p.hosts[publication.Host]; exists {
		if current != publication {
			return fmt.Errorf("HTTP retirement does not match the current reservation")
		}
		delete(p.hosts, publication.Host)
	}
	return nil
}
