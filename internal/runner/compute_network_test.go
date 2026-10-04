package runner

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computenet"
	"github.com/anas-project/ANAS/internal/runtimeissues"
)

// networkApp builds a compute app whose leases declare the given network and
// publish blocks, on a host that approved 30000-30009 for port bindings.
func networkApp(t *testing.T, declarations map[string][2]map[string]any) *app {
	t.Helper()
	consumers := map[string]string{}
	for consumer := range declarations {
		consumers[consumer] = "anas-" + strings.ReplaceAll(consumer, "_", "-") + "-runners"
	}
	a := computeApp(t, consumers)
	for consumer, declaration := range declarations {
		spec := a.reg[consumer].Resources[0].Spec
		if declaration[0] != nil {
			spec["network"] = declaration[0]
		}
		if declaration[1] != nil {
			spec["publish"] = declaration[1]
		}
	}
	a.env["INCUS_PORT_BINDING_RANGE"] = "30000-30009"
	a.env["BASE_DOMAIN"] = "example.test"
	a.base = filepath.Join(t.TempDir(), ".anas")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	stubHostPorts(t, nil, nil)
	return a
}

// stubHostPorts makes the given host ports look occupied by a host process
// or by a Docker publication.
func stubHostPorts(t *testing.T, held []string, docker []string) {
	t.Helper()
	previousProbe, previousDocker := hostPortProbe, dockerPublishedPortsFunc
	t.Cleanup(func() { hostPortProbe, dockerPublishedPortsFunc = previousProbe, previousDocker })
	hostPortProbe = func(protocol string, port uint16) error {
		for _, key := range held {
			if key == fmt.Sprintf("%s/%d", protocol, port) {
				return fmt.Errorf("port is already in use on this host")
			}
		}
		return nil
	}
	dockerPublishedPortsFunc = func(*app) (map[string]bool, error) {
		out := map[string]bool{}
		for _, key := range docker {
			out[key] = true
		}
		return out, nil
	}
}

func slotNetwork(slots ...string) map[string]any {
	declared := map[string]any{}
	for _, slot := range slots {
		declared[slot] = map[string]any{"instance": "anas-forgejo-" + slot}
	}
	return map[string]any{"ingress": "published", "slots": declared}
}

func bindings(items ...map[string]any) map[string]any {
	ports := make([]any, len(items))
	for i, item := range items {
		ports[i] = item
	}
	return map[string]any{"ports": ports}
}

func binding(protocol string, host any, slot string, guest int) map[string]any {
	return map[string]any{"protocol": protocol, "host_port": host, "slot": slot, "guest_port": guest}
}

func frozenNetwork(t *testing.T, a *app, consumer string) *computenet.Network {
	t.Helper()
	for _, r := range a.resourceRequests {
		if r.Consumer == consumer {
			return r.ComputeNetwork
		}
	}
	t.Fatalf("no request for %s", consumer)
	return nil
}

// INCUS-R-112, R-131: an omitted declaration freezes the defaults; a declared
// tier and its switches freeze as written.
func TestComputeNetworkFreezesTiers(t *testing.T) {
	a := networkApp(t, map[string][2]map[string]any{
		"forgejo": {nil, nil},
		"agent":   {{"egress": "internet_lan_host", "intra_lease": true}, nil},
	})
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
	if n := frozenNetwork(t, a, "forgejo"); n.Egress != "internet" || n.Ingress != "none" || n.ModuleAccess || n.IntraLease || n.TraefikPort != 0 {
		t.Fatalf("default lease = %+v", n)
	}
	if n := frozenNetwork(t, a, "agent"); n.Egress != "internet_lan_host" || !n.IntraLease {
		t.Fatalf("declared lease = %+v", n)
	}
	for _, r := range a.resourceRequests {
		if err := validateFrozenComputeNetwork(r); err != nil {
			t.Fatal(err)
		}
	}
}

// INCUS-R-117: reaching Modules freezes the Traefik entrypoint port, and is
// refused when the deployment has no Traefik.
func TestComputeNetworkNeedsTraefikForModuleAccess(t *testing.T) {
	a := networkApp(t, map[string][2]map[string]any{"forgejo": {{"module_access": true}, nil}})
	if err := a.prepareComputeNetwork(); err == nil || !strings.Contains(err.Error(), "traefik") {
		t.Fatalf("module_access without Traefik = %v", err)
	}
	a.order = append(a.order, "traefik")
	a.env["TRAEFIK_BASE_PORT"] = "9000"
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
	if n := frozenNetwork(t, a, "forgejo"); n.TraefikPort != 9000 {
		t.Fatalf("frozen Traefik port = %d", n.TraefikPort)
	}
}

// INCUS-R-164: a port binding without hostd fails apply with the reason.
func TestPortBindingsNeedHostd(t *testing.T) {
	for blocker, want := range map[string]string{
		"hostd_missing":       "anas-hostd is not installed",
		"host_not_configured": "incus.configure",
		"remote_daemon":       "not on this host",
		"":                    "did not publish",
	} {
		t.Run(blocker, func(t *testing.T) {
			a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 30001, "dev", 22))}})
			a.env["INCUS_PORT_BINDING_RANGE"] = ""
			a.env["INCUS_PORT_BINDING_BLOCKER"] = blocker
			err := a.prepareComputeNetwork()
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "INCUS-R-164") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	// A lease without port bindings does not need hostd.
	a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), nil}})
	a.env["INCUS_PORT_BINDING_RANGE"] = ""
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
}

// INCUS-R-138, R-154: explicit ports must be inside the approved range, free
// on the host and in Docker, and unique across leases and Traefik.
func TestExplicitPortBindingConflictsFailApply(t *testing.T) {
	for name, setup := range map[string]func(*testing.T) *app{
		"outside range": func(t *testing.T) *app {
			return networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 2222, "dev", 22))}})
		},
		"held by a host process": func(t *testing.T) *app {
			a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 30001, "dev", 22))}})
			stubHostPorts(t, []string{"tcp/30001"}, nil)
			return a
		},
		"published by Docker": func(t *testing.T) *app {
			a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("udp", 30001, "dev", 53))}})
			stubHostPorts(t, nil, []string{"udp/30001"})
			return a
		},
		"two leases": func(t *testing.T) *app {
			return networkApp(t, map[string][2]map[string]any{
				"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 30001, "dev", 22))},
				"agent":   {{"ingress": "published", "slots": map[string]any{"box": map[string]any{"instance": "anas-agent-box"}}}, bindings(binding("tcp", 30001, "box", 22))},
			})
		},
		"Traefik entrypoint": func(t *testing.T) *app {
			a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 30001, "dev", 22))}})
			a.order = append(a.order, "traefik")
			a.env["TRAEFIK_BASE_PORT"] = "30001"
			return a
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := setup(t).prepareComputeNetwork(); err == nil {
				t.Fatal("conflicting port binding was frozen")
			}
		})
	}
	// The same number on the other protocol is a different binding.
	a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 30001, "dev", 22), binding("udp", 30001, "dev", 53))}})
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
}

// INCUS-R-154: a host port taken at apply is recorded as a runtime issue,
// and the next apply that finds it free resolves that record.
func TestExplicitPortConflictIsARuntimeIssue(t *testing.T) {
	a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", 30001, "dev", 22))}})
	stubHostPorts(t, []string{"tcp/30001"}, nil)
	if err := a.prepareComputeNetwork(); err == nil {
		t.Fatal("a taken port was frozen")
	}
	path := filepath.Join(a.base, "state", "runtime-issues.json")
	issues, err := runtimeissues.Load(path)
	if err != nil || len(issues) != 1 || issues[0].Key != "incus.port-binding/forgejo.runners/tcp/30001" || !issues[0].Open() || issues[0].Source != runtimeissues.SourceApply {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	stubHostPorts(t, nil, nil)
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
	if issues, _ = runtimeissues.Load(path); issues[0].Open() {
		t.Fatalf("the conflict was not resolved: %+v", issues)
	}
}

// INCUS-R-153: auto picks a free port from the approved range, records it,
// and keeps it on the next apply; a port this deployment already holds is not
// mistaken for a conflict.
func TestAutoPortBindingIsStableAcrossApplies(t *testing.T) {
	declaration := map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", "auto", "dev", 22), binding("tcp", 30005, "dev", 80))}}
	a := networkApp(t, declaration)
	stubHostPorts(t, []string{"tcp/30000", "tcp/30001", "tcp/30002", "tcp/30003"}, []string{"tcp/30004"})
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
	n := frozenNetwork(t, a, "forgejo")
	var auto computenet.PortBinding
	for _, b := range n.Ports {
		if b.Auto {
			auto = b
		}
	}
	if auto.HostPort < 30006 || auto.HostPort > 30009 {
		t.Fatalf("auto port %d is not a free port in the range", auto.HostPort)
	}
	if err := validateFrozenComputeNetwork(a.resourceRequests[0]); err != nil {
		t.Fatal(err)
	}
	writeActiveComputeDeployment(t, a)

	// Next apply: the chosen port and the explicit one are now held by this
	// deployment's own reservations, which the probe sees as in use.
	next := networkApp(t, declaration)
	next.base = a.base
	stubHostPorts(t, []string{"tcp/30005", fmt.Sprintf("tcp/%d", auto.HostPort)}, nil)
	if err := next.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
	for _, b := range frozenNetwork(t, next, "forgejo").Ports {
		if b.Auto && b.HostPort != auto.HostPort {
			t.Fatalf("auto port moved from %d to %d", auto.HostPort, b.HostPort)
		}
	}
}

func TestAutoPortBindingFailsWhenTheRangeIsFull(t *testing.T) {
	a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", "auto", "dev", 22))}})
	var held []string
	for port := 30000; port <= 30009; port++ {
		held = append(held, fmt.Sprintf("tcp/%d", port))
	}
	stubHostPorts(t, held, nil)
	if err := a.prepareComputeNetwork(); err == nil {
		t.Fatal("an auto port was allocated from a full range")
	}
}

func TestFrozenComputeNetworkDriftIsRefused(t *testing.T) {
	a := networkApp(t, map[string][2]map[string]any{"forgejo": {slotNetwork("dev"), bindings(binding("tcp", "auto", "dev", 22))}})
	if err := a.prepareComputeNetwork(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ResourceRequest){
		"widened tier":       func(r *ResourceRequest) { r.ComputeNetwork.Egress = "internet_lan_host" },
		"intra lease opened": func(r *ResourceRequest) { r.ComputeNetwork.IntraLease = true },
		"retargeted slot":    func(r *ResourceRequest) { r.ComputeNetwork.Slots[0].Instance = "anas-forgejo-other" },
		"unresolved auto":    func(r *ResourceRequest) { r.ComputeNetwork.Ports[0].HostPort = 0 },
		"spec changed":       func(r *ResourceRequest) { r.Spec["network"] = map[string]any{"egress": "modules_only"} },
		"network on a database": func(r *ResourceRequest) {
			r.Contract = "relational_database"
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := a.resourceRequests[0]
			r.ComputeNetwork = r.ComputeNetwork.Clone()
			r.Spec = cloneAnyMap(r.Spec)
			mutate(&r)
			if err := validateFrozenComputeNetwork(r); err == nil {
				t.Fatal("drifted frozen network was accepted")
			}
		})
	}
	legacy := a.resourceRequests[0]
	legacy.ComputeNetwork = nil
	if err := validateFrozenComputeNetwork(legacy); err != nil {
		t.Fatalf("a deployment frozen before network declarations: %v", err)
	}
	if got := frozenComputeNetwork(legacy); got.Egress != "internet" || got.Ingress != "none" {
		t.Fatalf("legacy lease network = %+v", got)
	}
}

// INCUS-R-120: the LAN is the default-route interface's subnets; Docker and
// Incus bridges never are, and loopback and link-local are neither LAN nor a
// host address.
func TestHostLANIsTheDefaultRouteInterfaceOnly(t *testing.T) {
	previous := hostInterfaceAddrs
	t.Cleanup(func() { hostInterfaceAddrs = previous })
	hostInterfaceAddrs = func() ([]hostInterface, error) {
		return []hostInterface{
			{Name: "lo", Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8"), netip.MustParsePrefix("::1/128")}},
			{Name: "eth0", Addrs: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24"), netip.MustParsePrefix("2001:db8:1::10/64"), netip.MustParsePrefix("fe80::1/64")}},
			{Name: "docker0", Addrs: []netip.Prefix{netip.MustParsePrefix("172.17.0.1/16")}},
			{Name: "lease0123456789", Addrs: []netip.Prefix{netip.MustParsePrefix("10.20.30.1/24")}},
		}, nil
	}
	a := &app{env: map[string]string{"INTERFACE": "eth0"}}
	lan, addresses, err := a.hostLANAndAddresses()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lan, ",") != "192.168.1.0/24,2001:db8:1::/64" {
		t.Fatalf("LAN = %v", lan)
	}
	if strings.Join(addresses, ",") != "10.20.30.1/32,172.17.0.1/32,192.168.1.10/32,2001:db8:1::10/128" {
		t.Fatalf("host addresses = %v", addresses)
	}
}

func TestComputeEnsureReportIsValidated(t *testing.T) {
	good := `noise from compose` + "\n" + fakeComputeEnsureReport
	if n, err := parseComputeEnsureResult(good); err != nil || n.Bridge != "lease120067207a" {
		t.Fatalf("report = %+v, %v", n, err)
	}
	for name, report := range map[string]string{
		"empty":           "",
		"not ready":       `{"ready":false,"network":{"bridge":"lease0","ipv4_subnet":"10.0.0.0/24","ipv4_gateway":"10.0.0.1"}}`,
		"no network":      `{"ready":true}`,
		"gateway outside": `{"ready":true,"network":{"bridge":"lease0","ipv4_subnet":"10.0.0.0/24","ipv4_gateway":"10.0.1.1"}}`,
		"unmasked subnet": `{"ready":true,"network":{"bridge":"lease0","ipv4_subnet":"10.0.0.1/24","ipv4_gateway":"10.0.0.1"}}`,
		"bad bridge":      `{"ready":true,"network":{"bridge":"../etc","ipv4_subnet":"10.0.0.0/24","ipv4_gateway":"10.0.0.1"}}`,
		"slot outside":    `{"ready":true,"network":{"bridge":"lease0","ipv4_subnet":"10.0.0.0/24","ipv4_gateway":"10.0.0.1","slots":[{"name":"a","instance":"anas-a","ipv4":"10.9.0.2"}]}}`,
		"v6 slot no v6":   `{"ready":true,"network":{"bridge":"lease0","ipv4_subnet":"10.0.0.0/24","ipv4_gateway":"10.0.0.1","slots":[{"name":"a","instance":"anas-a","ipv4":"10.0.0.250","ipv6":"fd00::1"}]}}`,
	} {
		if _, err := parseComputeEnsureResult(report); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func writeActiveComputeDeployment(t *testing.T, a *app) {
	t.Helper()
	id := "20261003T000000Z-00000001"
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id}
	for _, r := range a.resourceRequests {
		manifest.Resources = append(manifest.Resources, deploymentResource{
			Consumer: r.Consumer, ID: r.ID, Contract: r.Contract, Spec: r.Spec, ComputeNetwork: r.ComputeNetwork.Clone(),
		})
	}
	root := filepath.Join(a.base, "deployments", id)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(a.base, &activeDeploymentState{ActiveDeployment: id}); err != nil {
		t.Fatal(err)
	}
}
