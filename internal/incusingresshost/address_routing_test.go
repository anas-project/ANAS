package incusingresshost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This fake models only the kernel command boundary. It uses native-shaped
// JSON, keeps unrelated routes, and deliberately deletes the held route when
// the original device disappears. It does not prove actual kernel behaviour.
type addressKernelFixture struct {
	g                                *addressRouter
	rules, routes, links, neighbours []map[string]any
	effects                          []string
	fail                             string
}

func (f *addressKernelFixture) output(ctx context.Context, _ string, args []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []map[string]any
	switch strings.Join(args, " ") {
	case "-j -N -4 rule show":
		values = f.rules
	case "-j -N -4 route show table all":
		values = f.routes
	case "-j link show":
		values = f.links
	case "-j -4 neigh show":
		values = f.neighbours
	default:
		values = []map[string]any{}
		if slices.Equal(args[:min(len(args), 6)], []string{"-j", "-d", "link", "show", "dev", testTarget().HostVethName}) {
			for _, link := range f.links {
				if link["ifname"] == args[5] {
					values = append(values, link)
				}
			}
		} else if len(args) == 9 && slices.Equal(args[:6], []string{"-j", "-4", "neigh", "show", "dev", testTarget().HostVethName}) && args[6] == "to" {
			return nil, fmt.Errorf("unexpected argument length")
		} else if len(args) == 8 && slices.Equal(args[:5], []string{"-j", "-4", "neigh", "show", "dev"}) && args[6] == "to" {
			for _, n := range f.neighbours {
				if n["dev"] == args[5] && n["dst"] == args[7] {
					values = append(values, n)
				}
			}
		} else {
			return nil, fmt.Errorf("unexpected observation %v", args)
		}
	}
	return json.Marshal(values)
}

func argValue(args []string, key string) string {
	i := slices.Index(args, key)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func (f *addressKernelFixture) run(ctx context.Context, _ string, args []string, _ []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	effect := strings.Join(args, " ")
	f.effects = append(f.effects, effect)
	if f.fail != "" && strings.Contains(effect, f.fail) {
		return errors.New("injected kernel failure")
	}
	if len(args) < 4 || args[0] != "-4" {
		return errors.New("unexpected mutation")
	}
	verb := args[2]
	if verb != "add" && verb != "del" {
		return errors.New("replacement/flush is forbidden")
	}
	var collection *[]map[string]any
	var value map[string]any
	switch args[1] {
	case "rule":
		priority, _ := strconv.Atoi(argValue(args, "priority"))
		value = map[string]any{"priority": priority, "src": strings.TrimSuffix(argValue(args, "from"), "/32"), "dst": argValue(args, "to"), "iif": argValue(args, "iif"), "protocol": argValue(args, "protocol")}
		if slices.Contains(args, "unreachable") {
			value["action"] = "unreachable"
		} else {
			value["table"] = argValue(args, "lookup")
		}
		collection = &f.rules
	case "route":
		value = map[string]any{"table": argValue(args, "table"), "protocol": argValue(args, "proto"), "flags": []any{}}
		if slices.Contains(args, "unreachable") {
			value["type"], value["dst"], value["metric"] = "7", "default", 42760
		} else {
			value["dst"], value["dev"], value["scope"] = args[5], argValue(args, "dev"), "253"
		}
		collection = &f.routes
	case "neigh":
		value = map[string]any{"dst": args[3], "dev": argValue(args, "dev"), "lladdr": argValue(args, "lladdr"), "state": []any{"PERMANENT"}}
		collection = &f.neighbours
	default:
		return errors.New("unexpected mutation family")
	}
	for i, old := range *collection {
		if reflect.DeepEqual(old, value) {
			if verb == "add" {
				return errors.New("already exists")
			}
			*collection = slices.Delete(*collection, i, i+1)
			return nil
		}
	}
	if verb == "del" {
		return errors.New("not found")
	}
	*collection = append(*collection, value)
	return nil
}

func newAddressFixture(t *testing.T) (*Backend, *addressRouter, *addressKernelFixture, Target) {
	t.Helper()
	b, target, stateDir := testBackend(t)
	b.config.AddressRouting = &AddressRouting{Table: 31000, Priority: 10000}
	writeFile(t, filepath.Join(stateDir, "nft-baseline-inet.json"), nftJSON(baselineObjectsFor(b, "inet")))
	writeFile(t, filepath.Join(stateDir, "nft-baseline-bridge.json"), nftJSON(baselineObjectsFor(b, "bridge")))
	var active struct {
		NFTables []map[string]any `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(activeNFTJSON(target)), &active); err != nil {
		t.Fatal(err)
	}
	objects := baselineObjectsFor(b, "inet")
	for _, entry := range active.NFTables {
		if set, ok := entry["set"]; ok {
			objects = append(objects, map[string]any{"set": set})
		}
	}
	i := 0
	for _, spec := range b.permitRuleSpecs(target) {
		i++
		objects = append(objects, map[string]any{"rule": map[string]any{"family": "inet", "table": b.config.PermitTable, "chain": permitChain, "handle": 100 + i, "comment": spec.Comment, "expr": spec.expressions()}})
	}
	writeFile(t, filepath.Join(stateDir, "nft-active.json"), nftJSON(objects))
	writeFile(t, filepath.Join(stateDir, "nft-active-bridge.json"), activeReplyNFTJSON(b, target, 101))
	// The private fixture's installation projection changes with AddressRouting.
	if err := b.saveBaselineReceipt(context.Background(), baselineReceipt{Schema: baselineReceiptSchema, ScopeDigest: ingressScopeDigest(b.config), State: "installed", InetHandle: 1, BridgeHandle: 1}); err != nil {
		t.Fatal(err)
	}
	g, err := b.addressRouter()
	if err != nil {
		t.Fatal(err)
	}
	f := &addressKernelFixture{g: g,
		rules:  []map[string]any{{"priority": 0, "src": "all", "table": "255"}, {"priority": 32766, "src": "all", "table": "254"}, {"priority": 32767, "src": "all", "table": "253"}},
		routes: []map[string]any{{"dst": "10.42.0.0/24", "dev": "incusbr0", "protocol": "2", "scope": "253", "flags": []any{}}},
		links:  []map[string]any{{"ifname": target.HostVethName, "ifindex": 101, "link_index": target.HostVethPeerIfIndex, "address": target.HostVethMAC, "master": b.config.GuestBridge, "link_type": "ether", "flags": []any{"UP", "LOWER_UP"}, "linkinfo": map[string]any{"info_kind": "veth"}}}, neighbours: []map[string]any{},
	}
	g.commands = f
	b.addressCommands = f
	return b, g, f, target
}

func TestAddressRoutingLifecycleAndPortReferenceCount(t *testing.T) {
	b, g, f, first := newAddressFixture(t)
	ctx := context.Background()
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.effects) != 3 || !strings.Contains(f.effects[0], "rule add unreachable") {
		t.Fatalf("installation did not deny before lookup: %v", f.effects)
	}
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.effects) != 3 {
		t.Fatal("repeat install mutated routing")
	}
	if err := b.HoldAddress(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.GuestPort++
	second.Reservation = strings.Repeat("d", 32) + ":2"
	if err := b.HoldAddress(ctx, second); err != nil {
		t.Fatal(err)
	}
	if len(f.effects) != 5 {
		t.Fatal("multiple ports repeated kernel route creation")
	}
	if err := g.verifyLocked(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := g.removeLocked(ctx); err == nil {
		t.Fatal("removed baseline with live holds")
	}
	if err := g.releaseLocked(ctx, first); err != nil {
		t.Fatal(err)
	}
	if len(f.effects) != 5 {
		t.Fatal("first port release removed shared kernel objects")
	}
	if err := g.releaseLocked(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := g.holdLocked(ctx, first); err == nil {
		t.Fatal("retired reservation was revived")
	}
	if err := g.removeLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.removeLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.routes) != 1 || f.routes[0]["dev"] != "incusbr0" {
		t.Fatal("unrelated main route was modified")
	}
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.holdLocked(ctx, first); err == nil {
		t.Fatal("baseline reinstall erased retired reservation history")
	}
}

func TestAddressRoutingNeverRecreatesDeletedDeviceRoute(t *testing.T) {
	_, g, f, target := newAddressFixture(t)
	ctx := context.Background()
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.holdLocked(ctx, target); err != nil {
		t.Fatal(err)
	}
	before := len(f.effects)
	// Simulate NETDEV_UNREGISTER and a replacement with identical name/MAC.
	f.routes = slices.DeleteFunc(f.routes, func(r map[string]any) bool { return r["dst"] == target.GuestIP+"/32" })
	f.neighbours = []map[string]any{}
	f.links[0]["ifindex"] = 202
	if err := g.verifyLocked(ctx, target); err == nil {
		t.Fatal("replacement satisfied old hold")
	}
	if err := g.holdLocked(ctx, target); err == nil {
		t.Fatal("missing device route was rebuilt")
	}
	if len(f.effects) != before {
		t.Fatal("stale hold performed a kernel mutation")
	}
	if err := g.releaseLocked(ctx, target); err != nil {
		t.Fatal(err)
	}
	if len(f.effects) != before {
		t.Fatal("release touched replacement device")
	}
	if len(f.rules) != 5 || len(f.routes) != 2 {
		t.Fatal("release removed terminal routing fence")
	}
}

func TestAddressRoutingRejectsForeignPolicyAndObjects(t *testing.T) {
	for _, kind := range []string{"earlier", "priority", "table", "neighbour", "route_modifier", "fallback", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			_, g, f, target := newAddressFixture(t)
			ctx := context.Background()
			switch kind {
			case "earlier":
				f.rules = append(f.rules, map[string]any{"priority": 10, "src": "all", "table": "254"})
			case "priority":
				f.rules = append(f.rules, map[string]any{"priority": 10000, "src": "all", "table": "254"})
			case "table":
				f.routes = append(f.routes, map[string]any{"table": "31000", "dst": "default", "dev": "foreign"})
			}
			if slices.Contains([]string{"earlier", "priority", "table"}, kind) {
				if err := g.installLocked(ctx); err == nil || len(f.effects) != 0 {
					t.Fatal("foreign installation was adopted or modified")
				}
				return
			}
			if err := g.installLocked(ctx); err != nil {
				t.Fatal(err)
			}
			if kind == "neighbour" {
				f.neighbours = append(f.neighbours, map[string]any{"dst": target.GuestIP, "dev": target.HostVethName, "lladdr": target.NICMAC, "state": []any{"PERMANENT"}})
				if err := g.holdLocked(ctx, target); err == nil {
					t.Fatal("external matching neighbour adopted")
				}
				return
			}
			if err := g.holdLocked(ctx, target); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "route_modifier":
				f.routes[len(f.routes)-1]["gateway"] = "10.42.0.99"
			case "fallback":
				f.routes[1]["type"] = "throw"
			case "duplicate":
				f.rules = append(f.rules, f.rules[len(f.rules)-1])
			}
			before := len(f.effects)
			if err := g.verifyLocked(ctx, target); err == nil {
				t.Fatal("changed routing scope accepted")
			}
			if err := g.releaseLocked(ctx, target); err == nil {
				t.Fatal("changed objects were deleted")
			}
			if len(f.effects) != before {
				t.Fatal("foreign evidence permitted external mutation")
			}
		})
	}
}

func TestAddressRoutingInterruptedEffectsAreNotRetriedAsGrants(t *testing.T) {
	for _, failure := range []string{"rule add unreachable", "route add unreachable", "neigh add", "route add table", "route del table"} {
		t.Run(failure, func(t *testing.T) {
			_, g, f, target := newAddressFixture(t)
			ctx := context.Background()
			if strings.Contains(failure, "unreachable") {
				f.fail = failure
				if err := g.installLocked(ctx); err == nil {
					t.Fatal("ignored install fault")
				}
				f.fail = ""
				before := len(f.effects)
				if err := g.installLocked(ctx); err == nil {
					t.Fatal("retried unresolved install")
				}
				if len(f.effects) != before {
					t.Fatal("repeated unresolved kernel effect")
				}
				return
			}
			if err := g.installLocked(ctx); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(failure, "del") {
				if err := g.holdLocked(ctx, target); err != nil {
					t.Fatal(err)
				}
				f.fail = failure
				if err := g.releaseLocked(ctx, target); err == nil {
					t.Fatal("ignored cleanup fault")
				}
			} else {
				f.fail = failure
				if err := g.holdLocked(ctx, target); err == nil {
					t.Fatal("ignored grant fault")
				}
			}
			f.fail = ""
			before := len(f.effects)
			if err := g.holdLocked(ctx, target); err == nil {
				t.Fatal("unresolved hold restored access")
			}
			if len(f.effects) != before {
				t.Fatal("unresolved hold repeated a grant")
			}
			if err := g.releaseLocked(ctx, target); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAddressRoutingRetirementCapacityNeverPreventsCleanup(t *testing.T) {
	_, g, _, target := newAddressFixture(t)
	ctx := context.Background()
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := g.load()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 255; i++ {
		state.Retired = append(state.Retired, fmt.Sprintf("%032x:%d", 1, i))
	}
	if err := g.save(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := g.holdLocked(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := g.holdLocked(ctx, target); err != nil {
		t.Fatal("idempotent hold needs no new retirement slot", err)
	}
	second := target
	second.Reservation = strings.Repeat("b", 32) + ":9"
	second.GuestPort++
	if err := g.holdLocked(ctx, second); err == nil {
		t.Fatal("grant overcommitted cleanup history")
	}
	if err := g.releaseLocked(ctx, target); err != nil {
		t.Fatal("capacity prevented cleanup", err)
	}
	state, err = g.load()
	if err != nil || len(state.Retired) != 256 || len(state.Holds) != 0 {
		t.Fatalf("cleanup lost evidence: %v", err)
	}
}

func TestAddressRoutingRejectsHostLocalDestinationAndConfigurationDrift(t *testing.T) {
	b, g, f, target := newAddressFixture(t)
	ctx := context.Background()
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	f.routes = append(f.routes, map[string]any{"table": "255", "type": "2", "dst": target.GuestIP, "dev": "lo"})
	before := len(f.effects)
	if err := g.holdLocked(ctx, target); err == nil {
		t.Fatal("local-table precedence bypassed address routing")
	}
	if len(f.effects) != before {
		t.Fatal("local destination caused a mutation")
	}
	f.routes = f.routes[:len(f.routes)-1]
	if err := g.holdLocked(ctx, target); err != nil {
		t.Fatal(err)
	}
	b.config.AddressRouting.Priority++
	if err := g.verifyLocked(ctx, target); err == nil {
		t.Fatal("installation drift accepted old evidence")
	}
	if err := g.releaseLocked(ctx, target); err == nil {
		t.Fatal("installation drift authorized cleanup")
	}
}

func TestAddressRoutingPublicationAndNFTUseDeviceFence(t *testing.T) {
	b, g, f, target := newAddressFixture(t)
	ctx := context.Background()
	if err := g.installLocked(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.HoldAddress(ctx, target); err != nil {
		t.Fatal(err)
	}
	_, _, rules := b.baselineDefinition("inet")
	if !strings.Contains(rules[0].Text, "ip daddr "+b.config.GuestSubnet+" jump "+permitChain) {
		t.Fatal("device traffic can bypass permit dispatch")
	}
	if !strings.Contains(rules[2].Text, "ip daddr "+b.config.GuestSubnet+" counter drop") {
		t.Fatal("device traffic can bypass default denial")
	}
	if b.permitRuleSpecs(target)[permitComment(target)].OIF != target.HostVethName {
		t.Fatal("permit is not tied to the guest device")
	}
	f.routes = slices.DeleteFunc(f.routes, func(r map[string]any) bool { return r["dst"] == target.GuestIP+"/32" })
	before := len(f.effects)
	if err := b.EnsureGuestRoute(ctx, target); err == nil {
		t.Fatal("missing kernel hold allowed a Traefik route")
	}
	if err := b.EnsureHTTPPermit(ctx, target); err == nil {
		t.Fatal("missing kernel hold allowed an HTTP permit")
	}
	if len(f.effects) != before {
		t.Fatal("lost device route was recreated")
	}
}

func TestAddressRoutingConnectsPublicationAndCleanup(t *testing.T) {
	b, _, _, target := newAddressFixture(t)
	ctx := context.Background()
	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"install", func() error { return b.InstallAddressRouting(ctx) }},
		{"hold", func() error { return b.HoldAddress(ctx, target) }},
		{"route", func() error { return b.EnsureGuestRoute(ctx, target) }},
		{"permit", func() error { return b.EnsureHTTPPermit(ctx, target) }},
		{"inventory", func() error { return b.CheckHTTPArtifacts(ctx, []Target{target}) }},
		{"revoke", func() error { return b.RemoveHTTPPermit(ctx, target) }},
		{"connections", func() error { return b.CloseHTTPConnections(ctx, target) }},
		{"route removal", func() error { return b.RemoveGuestRoute(ctx, target) }},
		{"release", func() error { return b.ReleaseAddress(ctx, target) }},
		{"address removal", func() error { return b.RemoveAddressRouting(ctx) }},
		{"repeat removal", func() error { return b.RemoveAddressRouting(ctx) }},
		{"nft removal", func() error { return b.RemoveBaseline(ctx) }},
	} {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}
}

func TestAddressRoutingFailedHoldUsesNormalWithdrawal(t *testing.T) {
	for _, fail := range []string{"neigh add", "route add table"} {
		t.Run(fail, func(t *testing.T) {
			b, g, f, target := newAddressFixture(t)
			ctx := context.Background()
			if err := b.InstallAddressRouting(ctx); err != nil {
				t.Fatal(err)
			}
			f.fail = fail
			if err := b.HoldAddress(ctx, target); err == nil {
				t.Fatal("failed address effect was reported held")
			}
			f.fail = ""
			// The ordinary executor closes ALL steps after a failure, including
			// ones that never became ready. No special fake-only cleanup path.
			for _, step := range []struct {
				name string
				run  func() error
			}{
				{"permit", func() error { return b.RemoveHTTPPermit(ctx, target) }},
				{"conntrack", func() error { return b.CloseHTTPConnections(ctx, target) }},
				{"route", func() error { return b.RemoveGuestRoute(ctx, target) }},
				{"inventory", func() error { return b.CheckHTTPArtifacts(ctx, []Target{target}) }},
				{"release", func() error { return b.ReleaseAddress(ctx, target) }},
			} {
				if err := step.run(); err != nil {
					t.Fatalf("%s cleanup: %v", step.name, err)
				}
			}
			state, err := g.load()
			if err != nil || len(state.Holds) != 0 || !slices.Contains(state.Retired, target.Reservation) {
				t.Fatalf("incomplete cleanup: %+v %v", state, err)
			}
		})
	}
}

func TestAddressRoutingPreEffectConflictNeverDeletesForeignNeighbour(t *testing.T) {
	b, g, f, target := newAddressFixture(t)
	ctx := context.Background()
	if err := b.InstallAddressRouting(ctx); err != nil {
		t.Fatal(err)
	}
	f.neighbours = append(f.neighbours, map[string]any{"dst": target.GuestIP, "dev": target.HostVethName, "lladdr": target.NICMAC, "state": []any{"PERMANENT"}})
	before := len(f.effects)
	if err := b.HoldAddress(ctx, target); err == nil {
		t.Fatal("adopted foreign matching neighbour")
	}
	for _, run := range []func() error{
		func() error { return b.RemoveHTTPPermit(ctx, target) }, func() error { return b.CloseHTTPConnections(ctx, target) },
		func() error { return b.RemoveGuestRoute(ctx, target) }, func() error { return b.ReleaseAddress(ctx, target) },
	} {
		if err := run(); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.effects) != before || len(f.neighbours) != 1 {
		t.Fatal("pre-effect failure cleanup modified foreign neighbour")
	}
	state, err := g.load()
	if err != nil || !slices.Contains(state.Retired, target.Reservation) {
		t.Fatalf("abandoned token is not retired: %v", err)
	}
}

func TestAddressAdmissionReservesEncodedCleanupSpace(t *testing.T) {
	state := addressRoutingState{Schema: addressRoutingSchema, Scope: strings.Repeat("a", 64), State: "installed"}
	if err := addressAdmissionBudget(state); err != nil {
		t.Fatal(err)
	}
	// JSON escaping, not string length, determines the durable-file budget.
	state.Retired = []string{strings.Repeat("\x00", 11000)}
	if err := addressAdmissionBudget(state); err == nil {
		t.Fatal("admission counted raw characters instead of encoded bytes")
	}
	if addressAdmissionBytes >= 65536-4095 {
		t.Fatal("cleanup transitions have no reserved encoded space")
	}
}
