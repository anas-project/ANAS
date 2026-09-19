package incusingresshost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeResolver struct {
	identity Identity
	err      error
}

func (f fakeResolver) ResolveHTTPIdentity(context.Context, Target) (Identity, error) {
	return f.identity, f.err
}

func testTarget() Target {
	return Target{
		Scope:               "scope_one",
		Epoch:               strings.Repeat("a", 64),
		Incarnation:         strings.Repeat("b", 64),
		Reservation:         strings.Repeat("c", 32) + ":1",
		Deployment:          "deployment-one",
		Lease:               Lease{Consumer: "forgejo", Resource: "runners"},
		InstanceID:          "anas-fj-job1",
		InstanceUUID:        "11111111-1111-4111-8111-111111111111",
		GuestPort:           7000,
		GuestIP:             "10.42.0.2",
		NICMAC:              "00:16:3e:01:02:03",
		ServerUUID:          "22222222-2222-4222-8222-222222222222",
		HostVethName:        "vethguest0",
		HostVethMAC:         "02:00:00:00:00:10",
		HostVethPeerIfIndex: 77,
	}
}

func testNamespacePin() RouteNamespacePin {
	return RouteNamespacePin{
		NetNSCookie:         101,
		NetNSDevice:         202,
		NetNSInode:          303,
		DockerContainerID:   strings.Repeat("d", 64),
		DockerStartedAt:     "2026-09-19T00:00:00Z",
		TraefikIfIndex:      12,
		TraefikMAC:          "02:00:00:00:00:20",
		HostVethPeerIfIndex: 99,
		HostVethMAC:         "02:00:00:00:00:21",
	}
}

func testBackend(t *testing.T) (*Backend, Target, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	receipts := filepath.Join(root, "receipts")
	state := filepath.Join(root, "state")
	for _, dir := range []string{bin, receipts, state} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	target := testTarget()
	writeFile(t, filepath.Join(state, "link.json"), linkJSON())
	writeFile(t, filepath.Join(state, "nft-baseline-inet.json"), baselineInetJSON())
	writeFile(t, filepath.Join(state, "nft-baseline-bridge.json"), baselineBridgeJSON())
	writeFile(t, filepath.Join(state, "nft-active.json"), activeNFTJSON(target))
	writeFakeIP(t, filepath.Join(bin, "ip"), state)
	writeFakeNFT(t, filepath.Join(bin, "nft"), state)
	writeFakeConntrack(t, filepath.Join(bin, "conntrack"), state)
	identity := Identity{
		InstanceUUID:        target.InstanceUUID,
		Incarnation:         target.Incarnation,
		GuestIP:             target.GuestIP,
		NICMAC:              target.NICMAC,
		ServerUUID:          target.ServerUUID,
		HostVethName:        target.HostVethName,
		HostVethMAC:         target.HostVethMAC,
		HostVethPeerIfIndex: target.HostVethPeerIfIndex,
		State:               "Running",
	}
	backend, err := newBackend(backendConfig{
		ScopeName:        target.Scope,
		ReceiptDir:       receipts,
		RouteNetNS:       "anas-traefik",
		RouteTable:       172,
		RouteProtocol:    99,
		PermitTable:      "anas_ingress_scope_one",
		OriginTable:      "anas_ingress_scope_one_l2",
		GuestBridge:      "incusbr0",
		IngressBridge:    "br-ingress",
		TraefikVeth:      "vethabc",
		TraefikInterface: "eth1",
		TraefikSourceIP:  "10.231.2.2",
		IngressGateway:   "10.231.2.1",
		GuestSubnet:      "10.42.0.0/24",
		Namespace:        testNamespacePin(),
		PermitTTL:        30 * time.Second,
		CommandTimeout:   5 * time.Second,
		Binaries:         trustedBinaries{IP: filepath.Join(bin, "ip"), NFT: filepath.Join(bin, "nft"), Conntrack: filepath.Join(bin, "conntrack")},
		Resolver:         fakeResolver{identity: identity},
		fixture:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.saveBaselineReceipt(context.Background(), baselineReceipt{Schema: baselineReceiptSchema, ScopeDigest: ingressScopeDigest(backend.config), State: "installed", InetHandle: 1, BridgeHandle: 1}); err != nil {
		t.Fatal(err)
	}
	return backend, target, state
}

func TestBackendAppliesAndCleansPreciseArtifacts(t *testing.T) {
	backend, target, _ := testBackend(t)
	ctx := context.Background()
	if err := backend.HoldAddress(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.EnsureGuestRoute(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.EnsureHTTPPermit(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckHTTPArtifacts(ctx, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if err := backend.RemoveHTTPPermit(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.CloseHTTPConnections(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.RemoveGuestRoute(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.ReleaseAddress(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckHTTPArtifacts(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestBackendRejectsStaleIdentityUnknownArtifactsAndHostileBaseline(t *testing.T) {
	backend, target, state := testBackend(t)
	backend.config.Resolver = fakeResolver{identity: Identity{InstanceUUID: target.InstanceUUID, Incarnation: target.Incarnation, GuestIP: "10.42.0.99", NICMAC: target.NICMAC, ServerUUID: target.ServerUUID, HostVethName: target.HostVethName, HostVethMAC: target.HostVethMAC, HostVethPeerIfIndex: target.HostVethPeerIfIndex, State: "Running"}}
	if err := backend.HoldAddress(context.Background(), target); err == nil {
		t.Fatal("stale IP accepted")
	}
	backend.config.Resolver = fakeResolver{identity: Identity{InstanceUUID: target.InstanceUUID, Incarnation: target.Incarnation, GuestIP: target.GuestIP, NICMAC: target.NICMAC, ServerUUID: target.ServerUUID, HostVethName: target.HostVethName, HostVethMAC: target.HostVethMAC, HostVethPeerIfIndex: target.HostVethPeerIfIndex, State: "Running"}}
	writeFile(t, filepath.Join(state, "nft-inet.json"), unknownNFTJSON())
	if err := backend.CheckHTTPArtifacts(context.Background(), nil); err == nil {
		t.Fatal("unknown nft artifact accepted")
	}
	writeFile(t, filepath.Join(state, "nft-inet.json"), hostileBaselineJSON())
	if err := backend.verifyBaseline(context.Background()); err == nil {
		t.Fatal("baseline proof accepted from hostile comment metadata")
	}
}

func TestBackendFaultInjectionKeepsReceiptsUntilCleanup(t *testing.T) {
	for _, step := range []string{"route-add", "nft-apply", "conntrack-delete", "route-delete"} {
		t.Run(step, func(t *testing.T) {
			backend, target, state := testBackend(t)
			ctx := context.Background()
			if err := backend.HoldAddress(ctx, target); err != nil {
				t.Fatal(err)
			}
			switch step {
			case "route-add":
				fail(t, state, step)
				if err := backend.EnsureGuestRoute(ctx, target); err == nil {
					t.Fatal("route failure ignored")
				}
			case "nft-apply":
				if err := backend.EnsureGuestRoute(ctx, target); err != nil {
					t.Fatal(err)
				}
				fail(t, state, step)
				if err := backend.EnsureHTTPPermit(ctx, target); err == nil {
					t.Fatal("nft failure ignored")
				}
			case "conntrack-delete":
				if err := backend.EnsureGuestRoute(ctx, target); err != nil {
					t.Fatal(err)
				}
				if err := backend.EnsureHTTPPermit(ctx, target); err != nil {
					t.Fatal(err)
				}
				if err := backend.RemoveHTTPPermit(ctx, target); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(state, "conntrack"), conntrackLine(target))
				fail(t, state, step)
				if err := backend.CloseHTTPConnections(ctx, target); err == nil {
					t.Fatal("conntrack failure ignored")
				}
			case "route-delete":
				if err := backend.EnsureGuestRoute(ctx, target); err != nil {
					t.Fatal(err)
				}
				fail(t, state, step)
				if err := backend.RemoveGuestRoute(ctx, target); err == nil {
					t.Fatal("route delete failure ignored")
				}
			}
			if _, err := backend.loadReceipt(target); err != nil {
				t.Fatalf("receipt was not retained after %s: %v", step, err)
			}
		})
	}
}

func TestBackendBlocksForeignRouteAndAllowsLegitimateConnections(t *testing.T) {
	backend, target, state := testBackend(t)
	ctx := context.Background()
	if err := backend.HoldAddress(ctx, target); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(state, "route.json"), `[{"dst":"10.42.0.2","gateway":"10.231.2.9","dev":"eth9","prefsrc":"10.231.2.2","protocol":99}]`)
	if err := backend.EnsureGuestRoute(ctx, target); err == nil {
		t.Fatal("foreign route was overwritten")
	}
	writeFile(t, filepath.Join(state, "route.json"), `[]`)
	if err := backend.EnsureGuestRoute(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.EnsureHTTPPermit(ctx, target); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(state, "conntrack"), conntrackLine(target))
	if err := backend.CheckHTTPArtifacts(ctx, []Target{target}); err != nil {
		t.Fatalf("legitimate owned connection rejected: %v", err)
	}
	if err := backend.RemoveHTTPPermit(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckHTTPArtifacts(ctx, []Target{target}); err == nil {
		t.Fatal("connection after permit removal accepted")
	}
}

func TestBackendSharesGuestRouteAcrossPorts(t *testing.T) {
	backend, first, _ := testBackend(t)
	second := first
	second.Reservation = strings.Repeat("e", 32) + ":2"
	second.GuestPort = 7001
	// A different port shares the same running instance incarnation. A restart
	// is a different allocation and must not inherit this route/permit.
	backend.config.Resolver = multiResolver(map[string]Identity{
		first.Reservation:  identityFor(first),
		second.Reservation: identityFor(second),
	})
	ctx := context.Background()
	for _, target := range []Target{first, second} {
		if err := backend.HoldAddress(ctx, target); err != nil {
			t.Fatal(err)
		}
		if err := backend.EnsureGuestRoute(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.RemoveGuestRoute(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := backend.ReleaseAddress(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckHTTPArtifacts(ctx, []Target{second}); err != nil {
		t.Fatalf("shared route was not attributed to remaining publication: %v", err)
	}
}

type multiResolver map[string]Identity

func (m multiResolver) ResolveHTTPIdentity(_ context.Context, target Target) (Identity, error) {
	return m[target.Reservation], nil
}

func identityFor(target Target) Identity {
	return Identity{
		InstanceUUID:        target.InstanceUUID,
		Incarnation:         target.Incarnation,
		GuestIP:             target.GuestIP,
		NICMAC:              target.NICMAC,
		ServerUUID:          target.ServerUUID,
		HostVethName:        target.HostVethName,
		HostVethMAC:         target.HostVethMAC,
		HostVethPeerIfIndex: target.HostVethPeerIfIndex,
		State:               "Running",
	}
}

func TestCommandOutputOverflowIsAnError(t *testing.T) {
	backend, _, state := testBackend(t)
	writeFile(t, filepath.Join(state, "overflow"), strings.Repeat("x", 1<<20+1))
	if _, err := backend.runner.output(context.Background(), backend.config.Binaries.IP, []string{"overflow"}); err == nil {
		t.Fatal("overflowing command output accepted")
	}
}

func TestNativeHarnessRefusesProductionSocketsAndRequiresMarker(t *testing.T) {
	stateRoot := t.TempDir()
	marker := filepath.Join(stateRoot, ".parent-created")
	writeFile(t, marker, "marker")
	err := ValidateNativeHarness(NativeHarnessConfig{
		DockerSocket: "/var/run/docker.sock",
		IncusSocket:  "/tmp/lab/incus.sock",
		StateRoot:    stateRoot,
		Source:       "dev-docs/plans/incus-module.md",
		DryRun:       true,
		TestMarker:   marker,
	})
	if err == nil {
		t.Fatal("production Docker socket accepted")
	}
	if err := ValidateNativeHarness(NativeHarnessConfig{
		DockerSocket: "/tmp/lab/docker.sock",
		IncusSocket:  "/tmp/lab/incus.sock",
		StateRoot:    stateRoot,
		Source:       "dev-docs/plans/incus-module.md",
		DryRun:       true,
	}); err == nil {
		t.Fatal("missing parent-created marker accepted")
	}
	if err := ValidateNativeHarness(NativeHarnessConfig{
		DockerSocket: "/tmp/lab/docker.sock",
		IncusSocket:  "/tmp/lab/incus.sock",
		StateRoot:    stateRoot,
		Source:       "dev-docs/plans/incus-module.md",
		DryRun:       true,
		TestMarker:   marker,
	}); err != nil {
		t.Fatal(err)
	}
}

func fail(t *testing.T, state, step string) {
	t.Helper()
	writeFile(t, filepath.Join(state, "fail"), step)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

func writeFakeIP(t *testing.T, path, state string) {
	writeExecutable(t, path, `#!/bin/sh
set -eu
state=`+state+`
fail="$state/fail"
if [ "$1" = "overflow" ]; then cat "$state/overflow"; exit 0; fi
failure=''
if [ -f "$fail" ]; then IFS= read -r failure < "$fail" || :; fi
case "$*" in
  *"link show"*) cat "$state/link.json"; exit 0 ;;
  *"route add"*)
    if [ "$failure" = "route-add" ]; then echo fail >&2; exit 2; fi
    printf '[{"dst":"10.42.0.2/32","gateway":"10.231.2.1","dev":"eth1","prefsrc":"10.231.2.2","protocol":99}]\n' > "$state/route.json"; exit 0 ;;
  *"route del"*)
    if [ "$failure" = "route-delete" ]; then echo fail >&2; exit 2; fi
    printf '[]\n' > "$state/route.json"; exit 0 ;;
  *"route show"*) if [ -f "$state/route.json" ]; then cat "$state/route.json"; else printf '[]\n'; fi; exit 0 ;;
esac
printf 'unexpected ip\n' >&2
exit 2
`)
}

func writeFakeNFT(t *testing.T, path, state string) {
	writeExecutable(t, path, `#!/bin/sh
set -eu
state=`+state+`
fail="$state/fail"
if [ "$1" = "-j" ] && [ "$2" = "list" ] && [ "$3" = "tables" ]; then
 if [ -f "$state/tables-absent" ]; then printf '{"nftables":[]}\n'; else printf '{"nftables":[{"table":{"family":"inet","name":"anas_ingress_scope_one","handle":1}},{"table":{"family":"bridge","name":"anas_ingress_scope_one_l2","handle":1}}]}\n'; fi
 exit 0
fi
if [ "$1" = "-c" ]; then
 cat > "$state/nft-check-input"
 if [ -f "$fail" ] && [ "$(cat "$fail")" = "nft-check" ]; then exit 2; fi
 exit 0
fi
if [ "$1" = "-j" ]; then
 case "$*" in
  *"list table bridge"*) if [ -f "$state/nft-bridge.json" ]; then cat "$state/nft-bridge.json"; else cat "$state/nft-baseline-bridge.json"; fi; exit 0 ;;
  *"list table inet"*) if [ -f "$state/nft-inet.json" ]; then cat "$state/nft-inet.json"; else cat "$state/nft-baseline-inet.json"; fi; exit 0 ;;
 esac
fi
if [ "$1" = "-f" ]; then
  script=$(cat)
  if [ -f "$fail" ] && [ "$(cat "$fail")" = "nft-apply" ]; then echo fail >&2; exit 2; fi
  printf '%s\n' "$script" >> "$state/nft-effects"
  case "$script" in
    *"create table"*) rm -f "$state/tables-absent"; cp "$state/nft-baseline-inet.json" "$state/nft-inet.json"; cp "$state/nft-baseline-bridge.json" "$state/nft-bridge.json"; exit 0 ;;
    *"delete table"*) touch "$state/tables-absent"; rm -f "$state/nft-inet.json" "$state/nft-bridge.json"; exit 0 ;;
  esac
  case "$script" in *"delete"*) cp "$state/nft-baseline-inet.json" "$state/nft-inet.json"; cp "$state/nft-baseline-bridge.json" "$state/nft-bridge.json"; exit 0 ;; esac
  cp "$state/nft-active.json" "$state/nft-inet.json"
  if [ -f "$state/nft-active-bridge.json" ]; then cp "$state/nft-active-bridge.json" "$state/nft-bridge.json"; fi
  exit 0
fi
echo unexpected nft >&2
exit 2
`)
}

func writeFakeConntrack(t *testing.T, path, state string) {
	writeExecutable(t, path, `#!/bin/sh
set -eu
state=`+state+`
fail="$state/fail"
if [ "$1" = "-D" ]; then
  if [ -f "$fail" ] && [ "$(cat "$fail")" = "conntrack-delete" ]; then echo fail >&2; exit 2; fi
  rm -f "$state/conntrack"
  exit 0
fi
if [ "$1" = "-L" ]; then
  if [ -f "$fail" ] && [ "$(cat "$fail")" = "conntrack-list" ]; then echo fail >&2; exit 2; fi
  if [ -f "$state/conntrack" ]; then cat "$state/conntrack"; fi
  exit 0
fi
echo unexpected conntrack >&2
exit 2
`)
}

func linkJSON() string {
	body, _ := json.Marshal([]map[string]any{{
		"ifname": "eth1", "ifindex": 12, "address": "02:00:00:00:00:20",
		"netns_cookie": uint64(101), "netns_device": uint64(202), "netns_inode": uint64(303),
		"docker_container_id": strings.Repeat("d", 64), "docker_started_at": "2026-09-19T00:00:00Z",
		"peer_ifindex": 99, "peer_mac": "02:00:00:00:00:21",
		"addr_info": []map[string]any{{"family": "inet", "local": "10.231.2.2", "prefixlen": 24}, {"family": "inet", "local": "10.231.2.1", "prefixlen": 24}},
	}})
	return string(body)
}

func baselineTestBackend() *Backend {
	return &Backend{config: backendConfig{ScopeName: "scope_one", PermitTable: "anas_ingress_scope_one", OriginTable: "anas_ingress_scope_one_l2", IngressBridge: "br-ingress", GuestBridge: "incusbr0", TraefikVeth: "vethabc", TraefikSourceIP: "10.231.2.2", Namespace: testNamespacePin()}}
}
func baselineObjects(family string) []map[string]any {
	return baselineObjectsFor(baselineTestBackend(), family)
}
func baselineObjectsFor(b *Backend, family string) []map[string]any {
	table, chains, rules := b.baselineDefinition(family)
	objects := []map[string]any{{"table": map[string]any{"family": family, "name": table, "handle": 1}}}
	for _, chain := range chains {
		objects = append(objects, map[string]any{"chain": chain})
	}
	for i, r := range rules {
		objects = append(objects, map[string]any{"rule": map[string]any{"family": family, "table": table, "chain": r.Chain, "handle": i + 2, "comment": r.Comment, "expr": r.Expr}})
	}
	return objects
}
func baselineInetJSON() string   { return nftJSON(baselineObjects("inet")) }
func baselineBridgeJSON() string { return nftJSON(baselineObjects("bridge")) }
func hostileBaselineJSON() string {
	objects := baselineObjects("inet")
	objects = append(objects, map[string]any{"rule": map[string]any{"family": "inet", "table": "anas_ingress_scope_one", "chain": "forward", "handle": 999, "comment": "anas:v2:baseline:scope_one:request-deny", "expr": []any{map[string]any{"accept": nil}}}})
	return nftJSON(objects)
}
func unknownNFTJSON() string {
	objects := baselineObjects("inet")
	objects = append(objects, map[string]any{"set": map[string]any{"family": "inet", "table": "anas_ingress_scope_one", "name": "p_unknown", "type": []string{"ipv4_addr", "inet_service"}, "flags": []string{"timeout"}, "timeout": 30}})
	return nftJSON(objects)
}
func activeNFTJSON(target Target) string {
	set, comment := permitSetName(target), permitComment(target)
	objects := baselineObjects("inet")
	objects = append(objects,
		map[string]any{"set": map[string]any{"family": "inet", "table": "anas_ingress_scope_one", "name": set, "type": []string{"ipv4_addr", "inet_service"}, "flags": []string{"timeout"}, "timeout": 30, "elem": []any{map[string]any{"elem": map[string]any{"val": map[string]any{"concat": []any{target.GuestIP, target.GuestPort}}, "timeout": 30, "expires": 29}}}}},
		map[string]any{"rule": permitRule("anas_ingress_scope_one", "br-ingress", "incusbr0", "saddr", "daddr", "dport", "10.231.2.2", set, []string{"new", "established"}, comment, 10)},
		map[string]any{"rule": permitRule("anas_ingress_scope_one", "incusbr0", "br-ingress", "daddr", "saddr", "sport", "10.231.2.2", set, []string{"established"}, comment+":reply", 11)},
	)
	return nftJSON(objects)
}

func nftJSON(items []map[string]any) string {
	body, _ := json.Marshal(map[string]any{"nftables": items})
	return string(body)
}

func permitRule(table, iif, oif, ipField, setIPField, tcpField, ip, set string, states []string, comment string, handle int) map[string]any {
	spec := nftRuleSpec{Comment: comment, Table: table, IIF: iif, OIF: oif, IPField: ipField, IP: ip, Set: set, TCPField: tcpField, States: states, Verdict: "accept"}
	var expressions []any
	body, _ := json.Marshal(spec.expressions())
	_ = json.Unmarshal(body, &expressions)
	return map[string]any{"family": "inet", "table": table, "chain": permitChain, "handle": handle, "comment": comment, "expr": expressions}
}

func conntrackLine(target Target) string {
	return "ipv4 2 tcp 6 431999 ESTABLISHED src=10.231.2.2 dst=" + target.GuestIP + " sport=50000 dport=7000 packets=1 bytes=1 src=" + target.GuestIP + " dst=10.231.2.2 sport=7000 dport=50000 packets=1 bytes=1 mark=0 use=1\n"
}
