package incusprovision

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// INCUS-R-118, R-126: configure installs the lease network policy's host side
// after the control listener, and records each piece as owned.
func TestConfigureInstallsLeaseNetworkPolicy(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive = true, true
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"lease-forwarding", "traefik-address-set", "port-binding-range", "network-restore-unit"} {
		if !containsStep(plan.Steps, PhaseConfigure, id) {
			t.Fatalf("plan omits %s: %#v", id, plan.Steps)
		}
	}
	if !slices.ContainsFunc(plan.Steps, func(s Step) bool { return strings.Contains(s.Effect, "30000-32767") }) {
		t.Fatalf("plan does not show the default port range: %#v", plan.Steps)
	}
	result, err := backend.Configure(ctx, Request{}, bind(plan, PhaseConfigure))
	if err != nil || result.Disposition != "configured" {
		t.Fatalf("configure = %#v, %v", result, err)
	}
	o := store.state.Ownership
	if !o.LeaseForwarding || !o.TraefikAddressSet || !o.NetworkPolicy || !o.NetworkUnit || o.PortRangeFirst != 30000 || o.PortRangeLast != 32767 {
		t.Fatalf("ownership = %#v", o)
	}
	listener := slices.Index(rt.calls, "control-listener")
	for _, call := range []string{"lease-forwarding", "traefik-set", "network-policy", "network-unit"} {
		if i := slices.Index(rt.calls, call); i < listener {
			t.Fatalf("%s ran before the control listener: %v", call, rt.calls)
		}
		if !hasReceipt(store.state, map[string]string{"lease-forwarding": "configure.lease-forwarding", "traefik-set": "configure.traefik-address-set",
			"network-policy": "configure.port-binding-range", "network-unit": "configure.network-restore-unit"}[call], "ok") {
			t.Fatalf("no receipt for %s", call)
		}
	}
	// A later sync needs this approval and nothing else.
	synced, err := backend.SyncTraefik(ctx)
	if err != nil || !synced.Approved || len(synced.Addresses) != 1 {
		t.Fatalf("sync after approval = %#v, %v", synced, err)
	}
}

func TestConfigureKeepsLeaseForwardingOwnershipWhenItFails(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PackagesInstalledByANAS: true, ManagedPackages: []string{"incus", "incus-base"}, IncusServiceByANAS: true}}}
	rt := newFakeRuntime(t)
	rt.obs.PackageInstalled, rt.obs.IncusDaemonActive = true, true
	rt.fail["lease-forwarding"] = errors.New("iptables failed")
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Configure(ctx, Request{}, bind(plan, PhaseConfigure)); !errors.Is(err, ErrExternalEffects) {
		t.Fatalf("configure = %v", err)
	}
	if !store.state.Ownership.LeaseForwarding || store.state.Ownership.NetworkUnit {
		t.Fatalf("a partial effect must stay owned and later steps must not run: %#v", store.state.Ownership)
	}
}

// HOSTACT-R-014: a sync without the configure approval is refused.
func TestTraefikSyncRequiresConfigureApproval(t *testing.T) {
	store := &memoryStore{state: State{Schema: StateSchema}}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	if _, err := backend.SyncTraefik(context.Background()); !errors.Is(err, ErrUnconfirmed) {
		t.Fatalf("unapproved sync = %v", err)
	}
	if slices.Contains(rt.calls, "traefik-sync") {
		t.Fatal("unapproved sync wrote the address set")
	}
}

func TestUninstallRemovesLeaseNetworkPolicyBootUnitFirst(t *testing.T) {
	ctx := context.Background()
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{
		ExternalDaemonPreserved: true, ControlListener: true, FirewallRules: true,
		LeaseForwarding: true, NetworkUnit: true, TraefikAddressSet: true, NetworkPolicy: true, PortRangeFirst: 30000, PortRangeLast: 32767,
	}}}
	rt := newFakeRuntime(t)
	backend := newBackendForTest(store, rt)
	plan, err := backend.Plan(ctx, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Uninstall(ctx, Request{}, bind(plan, PhaseUninstall)); err != nil {
		t.Fatal(err)
	}
	unit := slices.Index(rt.calls, "remove-network-unit")
	for _, call := range []string{"remove-lease-forwarding", "remove-traefik-set", "remove-network-policy", "remove-firewall"} {
		if i := slices.Index(rt.calls, call); i < 0 || i < unit {
			t.Fatalf("%s missing or before the boot unit removal: %v", call, rt.calls)
		}
	}
	o := store.state.Ownership
	if o.LeaseForwarding || o.NetworkUnit || o.TraefikAddressSet || o.NetworkPolicy || o.PortRangeFirst != 0 {
		t.Fatalf("ownership after uninstall = %#v", o)
	}
}

func TestPortRangeRequestIsNormalized(t *testing.T) {
	r, err := Request{}.normalized()
	if err != nil || r.PortRangeFirst != DefaultPortRangeFirst || r.PortRangeLast != DefaultPortRangeLast {
		t.Fatalf("default range = %d-%d, %v", r.PortRangeFirst, r.PortRangeLast, err)
	}
	if r, err := (Request{PortRangeFirst: 40000, PortRangeLast: 40099}).normalized(); err != nil || r.PortRangeFirst != 40000 {
		t.Fatalf("custom range = %#v, %v", r, err)
	}
	for _, bad := range []Request{{PortRangeFirst: 80, PortRangeLast: 90}, {PortRangeFirst: 40000, PortRangeLast: 30000}, {PortRangeFirst: 40000, PortRangeLast: 70000}, {PortRangeFirst: 40000}} {
		if _, err := bad.normalized(); err == nil {
			t.Fatalf("accepted range %d-%d", bad.PortRangeFirst, bad.PortRangeLast)
		}
	}
	if _, err := networkPolicyBody(1000, 2000); err == nil {
		t.Fatal("policy body accepted a privileged range")
	}
}

// The readback requires every rule once, in order, after Docker's jumps.
func TestLeaseForwardingReadbackChecksOrder(t *testing.T) {
	rules := func(order ...int) string {
		var lines []string
		for _, n := range order {
			lines = append(lines, "-A FORWARD -i lease+ -m comment --comment anas-lease-forward-"+string(rune('0'+n))+" -j ACCEPT")
		}
		return strings.Join(lines, "\n")
	}
	docker := "-P FORWARD DROP\n-A FORWARD -j DOCKER-USER\n-A FORWARD -j DOCKER-FORWARD\n"
	if !leaseForwardingOrdered(docker + rules(1, 2, 3, 4, 5, 6) + "\n-A FORWARD -j REJECT") {
		t.Fatal("ordered rules after Docker were rejected")
	}
	for name, listing := range map[string]string{
		"missing rule":      docker + rules(1, 2, 3, 4, 6),
		"out of order":      docker + rules(1, 3, 2, 4, 5, 6),
		"duplicate":         docker + rules(1, 2, 3, 4, 5, 6, 6),
		"before Docker":     rules(1, 2, 3, 4, 5, 6) + "\n" + docker,
		"docker26 after us": "-A FORWARD -j DOCKER-USER\n" + rules(1, 2, 3, 4, 5, 6) + "\n-A FORWARD -o docker0 -j DOCKER",
		"empty":             "",
	} {
		if leaseForwardingOrdered(listing) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDockerContainerReadsAreTheTwoListings(t *testing.T) {
	parsed, err := url.Parse("http://docker" + traefikContainersPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/v1.44/containers/json" || parsed.Query().Get("filters") != `{"label":["anas.traefik.instance"],"status":["running"]}` {
		t.Fatalf("listing = %s ? %s", parsed.Path, parsed.Query().Get("filters"))
	}
	if !allowedDockerNetworkRequest(http.MethodGet, traefikContainersPath) {
		t.Fatal("the Traefik listing is not allowed")
	}
	// The plain running-container listing is the port bindings' Docker read.
	if !allowedDockerNetworkRequest(http.MethodGet, dockerContainersPath) || allowedDockerNetworkRequest(http.MethodPost, dockerContainersPath) {
		t.Fatal("the published-port listing is not a read-only allowed request")
	}
	for _, path := range []string{"/v1.44/containers/json?all=1", "/v1.44/containers/abc/json", "/v1.44/containers/abc/kill"} {
		if allowedDockerNetworkRequest(http.MethodGet, path) || allowedDockerNetworkRequest(http.MethodPost, path) {
			t.Fatalf("%s is allowed", path)
		}
	}
}

func TestTraefikAddressesComeFromLabelledRunningContainers(t *testing.T) {
	body := `[
	 {"Id":"a","State":"running","Labels":{"anas.traefik.instance":"anas_traefik"},
	  "NetworkSettings":{"Networks":{"anas_traefik":{"IPAddress":"172.30.0.2","GlobalIPv6Address":"fd00:30::2"},"other":{"IPAddress":"172.31.0.4","GlobalIPv6Address":""}}}},
	 {"Id":"b","State":"exited","Labels":{"anas.traefik.instance":"x"},"NetworkSettings":{"Networks":{"n":{"IPAddress":"172.32.0.9"}}}},
	 {"Id":"c","State":"running","Labels":{},"NetworkSettings":{"Networks":{"n":{"IPAddress":"172.33.0.9"}}}}
	]`
	client := &dockerClient{socket: "/unused", transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1.44/containers/json" {
			t.Fatalf("unexpected Docker request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: ioNopCloser(body), Header: http.Header{}}, nil
	})}
	addresses, err := client.traefikAddresses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(addresses, ",") != "172.30.0.2/32,172.31.0.4/32,fd00:30::2/128" {
		t.Fatalf("addresses = %v", addresses)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ioNopCloser(body string) io.ReadCloser { return io.NopCloser(strings.NewReader(body)) }

func TestRestoreHostNetworkReappliesOnlyOwnedRules(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		ownership Ownership
		disabled  bool
		want      []string
	}{
		"both":     {Ownership{FirewallRules: true, ControlSubnet: "10.77.0.0/24", ControlGateway: "10.77.0.1", LeaseForwarding: true}, false, []string{"restore-firewall", "lease-forwarding"}},
		"none":     {Ownership{}, false, nil},
		"disabled": {Ownership{FirewallRules: true, LeaseForwarding: true}, true, nil},
	} {
		t.Run(name, func(t *testing.T) {
			store := &memoryStore{state: State{Schema: StateSchema, Ownership: tc.ownership, Disabled: tc.disabled}}
			rt := newFakeRuntime(t)
			if err := newBackendForTest(store, rt).RestoreHostNetwork(ctx); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rt.calls, tc.want) {
				t.Fatalf("calls = %v, want %v", rt.calls, tc.want)
			}
		})
	}
}
