package incusingresshost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// AddressRouting is trusted installation data, never a consumer parameter. Its
// table lives in the HOST namespace, independently of the Traefik /32 table.
// Initial implementation admits container veth links only. It does not reserve
// DHCP addresses: it prevents an old publication from following address reuse.
type AddressRouting struct {
	Table    uint32 `json:"table"`
	Priority uint32 `json:"priority"`
}

const addressRoutingFile = ".address-routing.json"
const addressRoutingSchema = "anas.incus-http-address-routing/v1"
const addressAdmissionBytes = 60 << 10 // Leave room below the 64 KiB durable-file bound for cleanup transitions.

type addressRoutingState struct {
	Schema  string               `json:"schema"`
	Scope   string               `json:"scope"`
	State   string               `json:"state"`
	Holds   []addressRoutingHold `json:"holds"`
	Retired []string             `json:"retired"`
}

type addressRoutingHold struct {
	Target  Target   `json:"target"`
	IfIndex uint32   `json:"ifindex"`
	State   string   `json:"state"`
	Users   []string `json:"users"`
}

type addressCommandExecutor interface {
	run(context.Context, string, []string, []byte) error
	output(context.Context, string, []string) ([]byte, error)
}

// The interface seam is private and used by fault tests. Production construction
// always selects the existing fixed-binary command runner.
type addressRouter struct {
	b        *Backend
	commands addressCommandExecutor
}

func (b *Backend) addressRouter() (*addressRouter, error) {
	if b == nil || b.config.AddressRouting == nil {
		return nil, fmt.Errorf("kernel address routing is not installed")
	}
	c := b.config.AddressRouting
	if c.Table == 0 || c.Table == 253 || c.Table == 254 || c.Table == 255 || c.Table == b.config.RouteTable || c.Priority < 100 || c.Priority >= 32765 {
		return nil, fmt.Errorf("invalid isolated address route table or rule priority")
	}
	var commands addressCommandExecutor = b.runner
	if b.addressCommands != nil {
		commands = b.addressCommands
	}
	return &addressRouter{b: b, commands: commands}, nil
}

func (g *addressRouter) load() (addressRoutingState, error) {
	body, err := g.b.readHostDocument(addressRoutingFile)
	if err != nil {
		return addressRoutingState{}, err
	}
	var state addressRoutingState
	if decodeObservedJSON(body, &state) != nil || state.Schema != addressRoutingSchema || state.Scope != ingressScopeDigest(g.b.config) || len(state.Holds) > 32 || len(state.Retired) > 256 {
		return state, fmt.Errorf("address routing evidence is invalid or belongs to another installation")
	}
	if !slices.Contains([]string{"installing", "installed", "removing", "removed"}, state.State) || state.State != "installed" && len(state.Holds) != 0 {
		return state, fmt.Errorf("invalid address routing transition")
	}
	seen := map[string]bool{}
	for _, token := range state.Retired {
		if !reservationID.MatchString(token) || seen[token] {
			return state, fmt.Errorf("invalid retired address reservation")
		}
		seen[token] = true
	}
	ips := map[string]bool{}
	for _, hold := range state.Holds {
		if g.b.validateTarget(hold.Target) != nil || hold.IfIndex == 0 || len(hold.Users) == 0 || len(hold.Users) > 64 || ips[hold.Target.GuestIP] || !slices.Contains([]string{"holding", "held", "releasing"}, hold.State) {
			return state, fmt.Errorf("invalid address routing hold")
		}
		ips[hold.Target.GuestIP] = true
		for _, token := range hold.Users {
			if !reservationID.MatchString(token) || seen[token] {
				return state, fmt.Errorf("duplicate address reservation")
			}
			seen[token] = true
		}
	}
	if len(seen) > 256 {
		return state, fmt.Errorf("address retirement capacity was overcommitted")
	}
	return state, nil
}

func addressRetirementAvailable(state addressRoutingState) bool {
	count := len(state.Retired)
	for _, hold := range state.Holds {
		count += len(hold.Users)
	}
	return count < 256
}

func addressAdmissionBudget(state addressRoutingState) error {
	body, err := json.Marshal(state)
	if err != nil || len(body) > addressAdmissionBytes {
		return fmt.Errorf("address evidence budget reached; existing cleanup capacity is reserved")
	}
	return nil
}

func (g *addressRouter) save(ctx context.Context, state addressRoutingState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return g.b.writeHostDocument(ctx, addressRoutingFile, body)
}

func (g *addressRouter) ruleArgs(verb string, deny bool) []string {
	c := g.b.config
	p := c.AddressRouting.Priority
	args := []string{"-4", "rule", verb}
	if deny {
		args = append(args, "unreachable")
		p++
	}
	args = append(args, "priority", strconv.FormatUint(uint64(p), 10), "from", c.TraefikSourceIP+"/32", "to", c.GuestSubnet, "iif", c.IngressBridge, "protocol", strconv.Itoa(int(c.RouteProtocol)))
	if !deny {
		args = append(args, "lookup", strconv.FormatUint(uint64(c.AddressRouting.Table), 10))
	}
	return args
}

func (g *addressRouter) fallbackArgs(verb string) []string {
	c := g.b.config
	return []string{"-4", "route", verb, "unreachable", "default", "table", strconv.FormatUint(uint64(c.AddressRouting.Table), 10), "proto", strconv.Itoa(int(c.RouteProtocol)), "metric", "42760"}
}

func (g *addressRouter) routeArgs(verb string, hold addressRoutingHold) []string {
	c := g.b.config
	return []string{"-4", "route", verb, "table", strconv.FormatUint(uint64(c.AddressRouting.Table), 10), hold.Target.GuestIP + "/32", "dev", hold.Target.HostVethName, "scope", "link", "proto", strconv.Itoa(int(c.RouteProtocol))}
}

func (g *addressRouter) neighbourArgs(verb string, hold addressRoutingHold) []string {
	return []string{"-4", "neigh", verb, hold.Target.GuestIP, "lladdr", hold.Target.NICMAC, "nud", "permanent", "dev", hold.Target.HostVethName}
}

func (g *addressRouter) run(ctx context.Context, args []string) error {
	return g.commands.run(ctx, g.b.config.Binaries.IP, args, nil)
}

// All methods ending in Locked run under the same Backend guard as publication
// receipts. Neither network names nor a repeated command create ownership.
func (g *addressRouter) installLocked(ctx context.Context) error {
	state, err := g.load()
	if err == nil {
		if state.State == "installed" {
			_, err = g.observe(ctx, state, true)
			return err
		}
		if state.State != "removed" {
			return fmt.Errorf("address routing has an unresolved installation intent")
		}
	} else if !errors.Is(err, errReceiptMissing) {
		return err
	}
	if err := g.absent(ctx); err != nil {
		return err
	}
	state = addressRoutingState{Schema: addressRoutingSchema, Scope: ingressScopeDigest(g.b.config), State: "installing", Holds: []addressRoutingHold{}, Retired: append([]string{}, state.Retired...)}
	if err := g.save(ctx, state); err != nil {
		return err
	}
	// Deny first. A lookup with an empty table must never fall through to the
	// host's normal bridge route, even during an interrupted installation.
	for _, args := range [][]string{g.ruleArgs("add", true), g.fallbackArgs("add"), g.ruleArgs("add", false)} {
		if err := g.run(ctx, args); err != nil {
			return err
		}
	}
	state.State = "installed"
	if _, err := g.observe(ctx, state, true); err != nil {
		return err
	}
	return g.save(ctx, state)
}

func (g *addressRouter) holdLocked(ctx context.Context, target Target) error {
	if err := g.b.validateFresh(ctx, target); err != nil {
		return err
	}
	state, err := g.load()
	if err != nil || state.State != "installed" {
		return fmt.Errorf("address routing baseline is unavailable")
	}
	if slices.Contains(state.Retired, target.Reservation) {
		return fmt.Errorf("retired address reservation cannot be reused")
	}
	if _, err := g.observe(ctx, state, true); err != nil {
		return err
	}
	for i, hold := range state.Holds {
		if hold.Target.GuestIP != target.GuestIP {
			continue
		}
		if !sameAddressAllocation(hold.Target, target) || hold.State != "held" {
			return fmt.Errorf("address still belongs to another allocation or unresolved effect")
		}
		if err := g.verifyHold(ctx, hold); err != nil {
			return err
		}
		if slices.Contains(hold.Users, target.Reservation) {
			return nil
		}
		if !addressRetirementAvailable(state) {
			return fmt.Errorf("address retirement capacity reached; cleanup remains available")
		}
		if len(hold.Users) >= 64 {
			return fmt.Errorf("address publication capacity reached")
		}
		state.Holds[i].Users = append(state.Holds[i].Users, target.Reservation)
		if err := addressAdmissionBudget(state); err != nil {
			return err
		}
		return g.save(ctx, state)
	}
	if len(state.Holds) >= 32 {
		return fmt.Errorf("address routing capacity reached")
	}
	if !addressRetirementAvailable(state) {
		return fmt.Errorf("address retirement capacity reached; cleanup remains available")
	}
	if err := g.rejectLocalDestination(ctx, target.GuestIP); err != nil {
		return err
	}
	index, err := g.link(ctx, target)
	if err != nil {
		return err
	}
	hold := addressRoutingHold{Target: target, IfIndex: index, State: "holding", Users: []string{target.Reservation}}
	if found, err := g.neighbour(ctx, hold); err != nil || found {
		return fmt.Errorf("existing neighbour cannot be adopted")
	}
	state.Holds = append(state.Holds, hold)
	if err := addressAdmissionBudget(state); err != nil {
		return err
	}
	if err := g.save(ctx, state); err != nil {
		return err
	}
	// An interrupted hold is never retried as a grant. Cleanup needs the same
	// recorded identity; a missing kernel route is not permission to recreate it.
	for _, args := range [][]string{g.neighbourArgs("add", hold), g.routeArgs("add", hold)} {
		if current, err := g.link(ctx, target); err != nil || current != index {
			return fmt.Errorf("guest link changed before address routing effect")
		}
		if err := g.run(ctx, args); err != nil {
			return err
		}
	}
	hold.State = "held"
	state.Holds[len(state.Holds)-1] = hold
	if err := g.verifyHold(ctx, hold); err != nil {
		return err
	}
	if _, err := g.observe(ctx, state, true); err != nil {
		return err
	}
	if err := g.b.validateFresh(ctx, target); err != nil {
		return err
	}
	return g.save(ctx, state)
}

// Reserve retirement capacity before persisting the publication-side attempt.
// No kernel effects occur here, and the shared guard prevents another local
// writer from using the slot before holdLocked records its independent intent.
func (g *addressRouter) admitAttempt(ctx context.Context, target Target) error {
	state, err := g.load()
	if err != nil || state.State != "installed" {
		return fmt.Errorf("address routing admission unavailable")
	}
	if _, err := g.observe(ctx, state, true); err != nil {
		return err
	}
	if slices.Contains(state.Retired, target.Reservation) {
		return fmt.Errorf("retired address reservation cannot be reused")
	}
	for _, hold := range state.Holds {
		if slices.Contains(hold.Users, target.Reservation) {
			if sameAddressAllocation(hold.Target, target) {
				return nil
			}
			return fmt.Errorf("address reservation belongs to a different allocation")
		}
		if hold.Target.GuestIP == target.GuestIP && !sameAddressAllocation(hold.Target, target) {
			return fmt.Errorf("address is still owned by another allocation")
		}
	}
	if !addressRetirementAvailable(state) {
		return fmt.Errorf("address retirement capacity reached")
	}
	state.Retired = append(state.Retired, target.Reservation)
	return addressAdmissionBudget(state)
}

func (g *addressRouter) verifyLocked(ctx context.Context, target Target) error {
	state, err := g.load()
	if err != nil || state.State != "installed" {
		return fmt.Errorf("address routing evidence is unavailable")
	}
	if _, err := g.observe(ctx, state, true); err != nil {
		return err
	}
	for _, hold := range state.Holds {
		if sameAddressAllocation(hold.Target, target) && slices.Contains(hold.Users, target.Reservation) && hold.State == "held" {
			return g.verifyHold(ctx, hold)
		}
	}
	return fmt.Errorf("publication has no live kernel address hold")
}

func (g *addressRouter) releaseLocked(ctx context.Context, target Target) error {
	state, err := g.load()
	if err != nil || state.State != "installed" {
		return fmt.Errorf("address routing evidence is unavailable")
	}
	if slices.Contains(state.Retired, target.Reservation) {
		_, err := g.observe(ctx, state, false)
		return err
	}
	if len(state.Retired) >= 256 {
		return fmt.Errorf("address retirement history requires explicit maintenance")
	}
	routes, err := g.observe(ctx, state, false)
	if err != nil {
		return err
	}
	for i, hold := range state.Holds {
		if !sameAddressAllocation(hold.Target, target) || !slices.Contains(hold.Users, target.Reservation) {
			continue
		}
		if len(hold.Users) > 1 {
			if hold.State != "held" {
				return fmt.Errorf("shared address hold is not ready")
			}
			state.Holds[i].Users = slices.DeleteFunc(hold.Users, func(token string) bool { return token == target.Reservation })
			state.Retired = append(state.Retired, target.Reservation)
			return g.save(ctx, state)
		}
		state.Holds[i].State = "releasing"
		if err := g.save(ctx, state); err != nil {
			return err
		}
		// Deletion is permitted after the guest stopped; it never recreates a
		// route. A replaced device is not touched, even if its name is identical.
		index, exists, err := g.findLink(ctx, target.HostVethName)
		if err != nil {
			return err
		}
		if routes[target.GuestIP] {
			if !exists || index != hold.IfIndex {
				return fmt.Errorf("cannot remove a route attached to a replacement link")
			}
			if err := g.run(ctx, g.routeArgs("del", hold)); err != nil {
				return err
			}
		}
		if exists && index == hold.IfIndex {
			found, err := g.neighbour(ctx, hold)
			if err != nil {
				return err
			}
			if found {
				if err := g.run(ctx, g.neighbourArgs("del", hold)); err != nil {
					return err
				}
			}
			if found, err := g.neighbour(ctx, hold); err != nil || found {
				return fmt.Errorf("address neighbour cleanup is unconfirmed")
			}
		}
		// Link deletion removes its routes and neighbours in the kernel. Do not
		// learn the replacement's identity or delete its unrelated neighbour.
		state.Holds = slices.Delete(state.Holds, i, i+1)
		state.Retired = append(state.Retired, target.Reservation)
		if _, err := g.observe(ctx, state, false); err != nil {
			return err
		}
		return g.save(ctx, state)
	}
	// A validated publication attempt can fail before the kernel-side hold is
	// written (for example an external neighbour appeared). Complete observed
	// inventory proves no unaccounted route was created; only retire this token,
	// never touch the external neighbour or infer ownership from the request.
	publication, err := g.b.loadReceipt(target)
	if err == nil && publication.AddressIntent && !publication.AddressHeld && addressRetirementAvailable(state) {
		state.Retired = append(state.Retired, target.Reservation)
		return g.save(ctx, state)
	}
	return fmt.Errorf("unknown address routing reservation")
}

func (g *addressRouter) removeLocked(ctx context.Context) error {
	state, err := g.load()
	if err != nil {
		return err
	}
	if state.State == "removed" {
		return g.absent(ctx)
	}
	if state.State != "installed" || len(state.Holds) != 0 {
		return fmt.Errorf("address routing still has live holds or unresolved effects")
	}
	if _, err := g.observe(ctx, state, false); err != nil {
		return err
	}
	state.State = "removing"
	if err := g.save(ctx, state); err != nil {
		return err
	}
	// Keep the terminal deny until lookup and private table have disappeared.
	for _, args := range [][]string{g.ruleArgs("del", false), g.fallbackArgs("del"), g.ruleArgs("del", true)} {
		if err := g.run(ctx, args); err != nil {
			return err
		}
	}
	if err := g.absent(ctx); err != nil {
		return err
	}
	state.State = "removed"
	return g.save(ctx, state)
}
