package incusprovision

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/runtimeissues"
)

// fakePortHost is a host with two workspaces' frozen state, one lease bridge
// and in-memory holds, maps and applied list.
type fakePortHost struct {
	files    map[string]string
	bridges  map[string][]netip.Prefix
	holds    map[string]bool // enabled and active
	busy     map[string]bool // taken by a host process
	docker   map[string]bool
	elements []portElement
	applied  []PortBinding
	chain    bool
	holdFail map[string]bool
	calls    []string
}

const fixtureManifest = `api_version: anas.dev/deployment/v1
id: 20261003T000000Z-00000001
resources:
  - consumer: forgejo
    id: runners
    contract: compute
    compute_network:
      egress: internet
      ingress: published
      slots: [{name: dev, instance: anas-fj-dev}]
      ports:
        - {protocol: tcp, host_port: 30022, slot: dev, guest_port: 22}
        - {protocol: udp, host_port: 30053, slot: dev, guest_port: 53}
`

const fixtureLeaseState = `actual:
  lease_network:
    bridge: lease120067207a
    ipv4_subnet: 10.101.0.0/24
    ipv4_gateway: 10.101.0.1
    slots: [{name: dev, instance: anas-fj-dev, ipv4: 10.101.0.254, ipv6: "fd42:1::fe"}]
`

func newFakePortHost() *fakePortHost {
	return &fakePortHost{
		files: map[string]string{
			"/srv/a/.anas/state/active.yml":                                     "active_deployment: 20261003T000000Z-00000001\n",
			"/srv/a/.anas/deployments/20261003T000000Z-00000001/deployment.yml": fixtureManifest,
			"/srv/a/.anas/state/resources/forgejo.runners.yml":                  fixtureLeaseState,
		},
		bridges: map[string][]netip.Prefix{"lease120067207a": {netip.MustParsePrefix("10.101.0.1/24"), netip.MustParsePrefix("fd42:1::1/64")}},
		holds:   map[string]bool{}, busy: map[string]bool{}, docker: map[string]bool{}, holdFail: map[string]bool{}, chain: true,
	}
}

func (f *fakePortHost) host() portHost {
	return portHost{
		workspaces: func() ([]consoleconfig.Workspace, error) {
			return []consoleconfig.Workspace{{ID: "main", Path: "/srv/a"}, {ID: "empty", Path: "/srv/b"}}, nil
		},
		readFile: func(path string, _ int64) ([]byte, error) {
			body, ok := f.files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(body), nil
		},
		bridge: func(name string) ([]netip.Prefix, error) {
			prefixes, ok := f.bridges[name]
			if !ok {
				return nil, errors.New("no such interface")
			}
			return prefixes, nil
		},
		enabledHolds: func() (map[string]bool, error) {
			out := map[string]bool{}
			for key, on := range f.holds {
				out[key] = on
			}
			return out, nil
		},
		holdActive: func(_ context.Context, protocol string, port int) bool {
			return f.holds[(PortBinding{Protocol: protocol, HostPort: port}).key()]
		},
		portFree: func(protocol string, port int) error {
			if f.busy[(PortBinding{Protocol: protocol, HostPort: port}).key()] {
				return ErrBlocked
			}
			return nil
		},
		dockerPorts: func(context.Context) (map[string]bool, error) { return f.docker, nil },
		hold: func(_ context.Context, protocol string, port int) error {
			key := (PortBinding{Protocol: protocol, HostPort: port}).key()
			f.calls = append(f.calls, "hold "+key)
			if f.holdFail[key] {
				return ErrExternalEffects
			}
			f.holds[key] = true
			return nil
		},
		release: func(_ context.Context, protocol string, port int) error {
			key := (PortBinding{Protocol: protocol, HostPort: port}).key()
			f.calls = append(f.calls, "release "+key)
			delete(f.holds, key)
			return nil
		},
		chainCurrent: func(context.Context) (bool, error) { return f.chain, nil },
		installChain: func(context.Context) error {
			f.calls = append(f.calls, "install chain")
			f.chain, f.elements = true, nil
			return nil
		},
		loadElements: func(_ context.Context, script string) error {
			f.calls = append(f.calls, "load elements")
			f.elements = nil
			for _, line := range strings.Split(script, "\n") {
				var e portElement
				if _, err := fmtSscanElement(line, &e); err == nil {
					f.elements = append(f.elements, e)
				}
			}
			sortElements(f.elements)
			return nil
		},
		readElements: func(context.Context) ([]portElement, error) { return slices.Clone(f.elements), nil },
		loadApplied:  func() ([]PortBinding, error) { return slices.Clone(f.applied), nil },
		saveApplied: func(bindings []PortBinding) error {
			f.applied = slices.Clone(bindings)
			return nil
		},
	}
}

// fmtSscanElement parses the "add element" lines portElementsScript writes.
func fmtSscanElement(line string, e *portElement) (int, error) {
	fields := strings.Fields(line)
	// add element inet anas_incus_ports tcp4 { 30022 : 10.101.0.254 . 22 }
	if len(fields) != 12 || fields[0] != "add" || fields[3] != PortTable {
		return 0, errors.New("not an element")
	}
	e.Map, e.Address = fields[4], fields[8]
	if _, err := fmtSscan(fields[6], &e.Port); err != nil {
		return 0, err
	}
	return fmtSscan(fields[10], &e.Target)
}

func fmtSscan(value string, out *int) (int, error) {
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return 1, nil
}

// INCUS-R-152, R-158, R-160: the sync reads the frozen deployment itself,
// holds each port before forwarding it, replaces the maps in one go and
// releases the holds of bindings that are gone.
func TestPortSyncHoldsThenForwardsFrozenBindings(t *testing.T) {
	f := newFakePortHost()
	f.holds["tcp/30999"] = true // a binding the deployment no longer declares
	result, err := f.host().syncPortBindings(context.Background(), DefaultPortRangeFirst, DefaultPortRangeLast)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bindings) != 2 || len(result.Rejected) != 0 {
		t.Fatalf("result = %+v", result)
	}
	want := []portElement{
		{Map: "tcp4", Port: 30022, Address: "10.101.0.254", Target: 22},
		{Map: "tcp6", Port: 30022, Address: "fd42:1::fe", Target: 22},
		{Map: "udp4", Port: 30053, Address: "10.101.0.254", Target: 53},
		{Map: "udp6", Port: 30053, Address: "fd42:1::fe", Target: 53},
	}
	if !slices.Equal(f.elements, want) {
		t.Fatalf("elements = %+v", f.elements)
	}
	if strings.Join(f.calls, ";") != "hold tcp/30022;hold udp/30053;load elements;release tcp/30999" {
		t.Fatalf("order = %v", f.calls)
	}
	if len(f.applied) != 2 || f.applied[0].Workspace != "main" || f.applied[0].Lease != "forgejo.runners" {
		t.Fatalf("applied = %+v", f.applied)
	}
	// A repeated sync changes nothing and takes no new hold.
	f.calls = nil
	if _, err := f.host().syncPortBindings(context.Background(), DefaultPortRangeFirst, DefaultPortRangeLast); err != nil || strings.Join(f.calls, ";") != "load elements" {
		t.Fatalf("repeat = %v, %v", f.calls, err)
	}
}

// INCUS-R-158: an entry that fails a check never takes effect; the others do.
func TestPortSyncRejectsEntriesThatFailAChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		setup  func(*fakePortHost)
		first  int
		reason string
	}{
		"outside range":   {func(*fakePortHost) {}, 30030, "outside_approved_range"},
		"host process":    {func(f *fakePortHost) { f.busy["tcp/30022"] = true }, DefaultPortRangeFirst, "port_in_use"},
		"docker":          {func(f *fakePortHost) { f.docker["tcp/30022"] = true }, DefaultPortRangeFirst, "port_published_by_docker"},
		"bridge missing":  {func(f *fakePortHost) { delete(f.bridges, "lease120067207a") }, DefaultPortRangeFirst, "lease_bridge_missing"},
		"slot is gateway": {func(f *fakePortHost) { f.bridges["lease120067207a"][0] = netip.MustParsePrefix("10.101.0.254/24") }, DefaultPortRangeFirst, "address_outside_lease"},
		"other subnet": {func(f *fakePortHost) {
			f.bridges["lease120067207a"][0] = netip.MustParsePrefix("10.102.0.1/24")
		}, DefaultPortRangeFirst, "address_outside_lease"},
		"not a lease bridge": {func(f *fakePortHost) {
			f.files["/srv/a/.anas/state/resources/forgejo.runners.yml"] = strings.Replace(fixtureLeaseState, "lease120067207a", "docker0", 1)
		}, DefaultPortRangeFirst, "not_a_lease_bridge"},
		"hold fails": {func(f *fakePortHost) { f.holdFail["tcp/30022"] = true }, DefaultPortRangeFirst, "hold_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakePortHost()
			tc.setup(f)
			result, err := f.host().syncPortBindings(context.Background(), tc.first, DefaultPortRangeLast)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, r := range result.Rejected {
				found = found || (r.Reason == tc.reason && r.key() == "tcp/30022")
			}
			if !found {
				t.Fatalf("rejected = %+v", result.Rejected)
			}
			for _, e := range f.elements {
				if e.Map[:3] == "tcp" {
					t.Fatalf("a rejected binding is forwarded: %+v", f.elements)
				}
			}
			if tc.reason == "hold_failed" && f.holds["tcp/30022"] {
				t.Fatal("a failed hold was left behind")
			}
		})
	}
	// Another workspace's binding on the same port loses; the first one stays.
	f := newFakePortHost()
	f.files["/srv/b/.anas/state/active.yml"] = "active_deployment: 20261003T000000Z-00000002\n"
	f.files["/srv/b/.anas/deployments/20261003T000000Z-00000002/deployment.yml"] = strings.Replace(fixtureManifest, "00000001", "00000002", 1)
	f.files["/srv/b/.anas/state/resources/forgejo.runners.yml"] = fixtureLeaseState
	result, err := f.host().syncPortBindings(context.Background(), DefaultPortRangeFirst, DefaultPortRangeLast)
	if err != nil || len(result.Bindings) != 2 || len(result.Rejected) != 2 || result.Rejected[0].Reason != "port_taken_by_other_binding" || result.Bindings[0].Workspace != "empty" {
		t.Fatalf("two workspaces = %+v, %v", result, err)
	}
}

// INCUS-R-161: at boot only bindings whose hold came up are forwarded again.
func TestPortRestoreLeavesOutPortsTakenFirst(t *testing.T) {
	f := newFakePortHost()
	f.chain = false
	f.applied = []PortBinding{
		{Workspace: "main", Lease: "forgejo.runners", Protocol: "tcp", HostPort: 30022, IPv4: "10.101.0.254", GuestPort: 22},
		{Workspace: "main", Lease: "forgejo.runners", Protocol: "udp", HostPort: 30053, IPv4: "10.101.0.254", GuestPort: 53},
	}
	f.holds["tcp/30022"] = true
	rejected, err := f.host().restorePortBindings(context.Background())
	if err != nil || len(rejected) != 1 || rejected[0].Reason != "hold_lost_at_boot" || rejected[0].key() != "udp/30053" {
		t.Fatalf("rejected = %+v, %v", rejected, err)
	}
	if !slices.Equal(f.elements, []portElement{{Map: "tcp4", Port: 30022, Address: "10.101.0.254", Target: 22}}) || f.calls[0] != "install chain" {
		t.Fatalf("elements = %+v, calls = %v", f.elements, f.calls)
	}
}

// INCUS-R-162: the check changes nothing and reports lost holds, Docker
// conflicts and drifted maps.
func TestPortCheckReportsWithoutRepairing(t *testing.T) {
	f := newFakePortHost()
	if _, err := f.host().syncPortBindings(context.Background(), DefaultPortRangeFirst, DefaultPortRangeLast); err != nil {
		t.Fatal(err)
	}
	if _, findings, err := f.host().checkPortBindings(context.Background()); err != nil || len(findings) != 0 {
		t.Fatalf("healthy = %+v, %v", findings, err)
	}
	delete(f.holds, "udp/30053")
	f.docker["tcp/30022"] = true
	f.elements = f.elements[:1]
	f.calls = nil
	_, findings, err := f.host().checkPortBindings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	reasons := []string{}
	for _, finding := range findings {
		reasons = append(reasons, finding.Binding.key()+" "+finding.Reason)
	}
	if strings.Join(reasons, ";") != "tcp/30022 port_published_by_docker;tcp/30022 rules_drifted;udp/30053 hold_lost;udp/30053 rules_drifted" || len(f.calls) != 0 {
		t.Fatalf("findings = %v, calls = %v", reasons, f.calls)
	}
}

func TestPortElementsScriptAndReadback(t *testing.T) {
	elements := []portElement{{Map: "tcp4", Port: 30022, Address: "10.101.0.254", Target: 22}, {Map: "tcp6", Port: 30022, Address: "fd42:1::fe", Target: 22}}
	script := portElementsScript(elements)
	for _, line := range []string{"flush map inet anas_incus_ports udp6", "add element inet anas_incus_ports tcp6 { 30022 : fd42:1::fe . 22 }"} {
		if !strings.Contains(script, line) {
			t.Fatalf("script lacks %q:\n%s", line, script)
		}
	}
	listing := `{"nftables":[{"metainfo":{"json_schema_version":1}},
	 {"table":{"family":"inet","name":"anas_incus_ports","handle":1,"comment":"anas-port-bindings"}},
	 {"map":{"family":"inet","name":"tcp4","table":"anas_incus_ports","type":"inet_service","map":["ipv4_addr","inet_service"],"elem":[[30022,{"concat":["10.101.0.254",22]}]]}},
	 {"map":{"family":"inet","name":"tcp6","table":"anas_incus_ports","type":"inet_service","map":["ipv6_addr","inet_service"],"elem":[[{"elem":{"val":30022,"comment":"x"}},{"concat":["fd42:0001::00fe",22]}]]}},
	 {"map":{"family":"inet","name":"udp4","table":"anas_incus_ports","type":"inet_service","map":["ipv4_addr","inet_service"]}},
	 {"map":{"family":"inet","name":"udp6","table":"anas_incus_ports","type":"inet_service","map":["ipv6_addr","inet_service"]}},
	 {"chain":{"family":"inet","table":"anas_incus_ports","name":"bind"}},
	 {"chain":{"family":"inet","table":"anas_incus_ports","name":"pre","type":"nat","hook":"prerouting"}},
	 {"chain":{"family":"inet","table":"anas_incus_ports","name":"out","type":"nat","hook":"output"}},
	 {"rule":{"family":"inet","table":"anas_incus_ports","chain":"bind"}},{"rule":{"family":"inet","table":"anas_incus_ports","chain":"bind"}},
	 {"rule":{"family":"inet","table":"anas_incus_ports","chain":"bind"}},{"rule":{"family":"inet","table":"anas_incus_ports","chain":"bind"}},
	 {"rule":{"family":"inet","table":"anas_incus_ports","chain":"pre"}},
	 {"rule":{"family":"inet","table":"anas_incus_ports","chain":"out"}},{"rule":{"family":"inet","table":"anas_incus_ports","chain":"out"}},{"rule":{"family":"inet","table":"anas_incus_ports","chain":"out"}}
	]}`
	got, err := parsePortElements([]byte(listing))
	if err != nil || !slices.Equal(got, elements) {
		t.Fatalf("readback = %+v, %v", got, err)
	}
	if !portChainListingCurrent([]byte(listing)) {
		t.Fatal("the fixed table was not recognized")
	}
	if portChainListingCurrent([]byte(strings.Replace(listing, `{"rule":{"family":"inet","table":"anas_incus_ports","chain":"pre"}},`, "", 1))) {
		t.Fatal("a table missing its prerouting jump was accepted")
	}
	if portChainListingCurrent([]byte(strings.Replace(listing, `"comment":"anas-port-bindings"`, `"comment":"other"`, 1))) {
		t.Fatal("a table ANAS did not mark was accepted")
	}
	for _, want := range []string{"fib daddr type local jump bind", "ip daddr 127.0.0.0/8 return", "ip6 daddr ::1 return", "dnat ip6 addr . port to udp dport map @udp6"} {
		if !strings.Contains(portChainRuleset, want) {
			t.Fatalf("ruleset lacks %q", want)
		}
	}
}

// The hold units: Accept=no with a resident holder (INCUS-R-160), and
// systemctl accepts only a matching socket/service pair.
func TestPortHoldUnitsAndTheirCommands(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		socket := portUnitTemplates["anas-port-"+protocol+"@.socket"]
		service := portUnitTemplates["anas-port-"+protocol+"@.service"]
		if !strings.Contains(socket, "Accept=no") || !strings.Contains(socket, "WantedBy=sockets.target") || !strings.Contains(socket, "Before=docker.service") ||
			!strings.Contains(service, "ExecStart=/usr/bin/sleep infinity") || !strings.Contains(service, "DynamicUser=yes") || !strings.Contains(service, "Restart=always") {
			t.Fatalf("%s units:\n%s\n%s", protocol, socket, service)
		}
	}
	if !strings.Contains(portUnitTemplates["anas-port-udp@.socket"], "ListenDatagram=%i") || !strings.Contains(portUnitTemplates["anas-port-tcp@.socket"], "ListenStream=%i") {
		t.Fatal("hold sockets listen on the wrong kind of socket")
	}
	for _, args := range [][]string{
		{"enable", "--now", "anas-port-tcp@30022.socket", "anas-port-tcp@30022.service"},
		{"disable", "--now", "anas-port-udp@30053.socket", "anas-port-udp@30053.service"},
		{"is-active", "--quiet", "anas-port-tcp@30022.socket", "anas-port-tcp@30022.service"},
	} {
		if _, _, err := fixedCommandSpec(fixedSystemctl, args); err != nil {
			t.Fatalf("%v refused: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"enable", "--now", "anas-port-tcp@30022.socket", "anas-port-tcp@30023.service"},
		{"enable", "--now", "anas-port-tcp@30022.socket", "anas-port-udp@30022.service"},
		{"enable", "--now", "anas-port-tcp@30022.service", "anas-port-tcp@30022.socket"},
		{"enable", "--quiet", "anas-port-tcp@30022.socket", "anas-port-tcp@30022.service"},
		{"is-active", "--now", "anas-port-tcp@30022.socket", "anas-port-tcp@30022.service"},
		{"enable", "--now", "anas-port-tcp@70000.socket", "anas-port-tcp@70000.service"},
		{"enable", "--now", "ssh.socket", "ssh.service"},
		{"stop", "--now", "anas-port-tcp@30022.socket", "anas-port-tcp@30022.service"},
	} {
		if _, _, err := fixedCommandSpec(fixedSystemctl, args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestPublishedPortsComeFromRunningContainers(t *testing.T) {
	body := `[{"Ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8080,"Type":"tcp"},{"PrivatePort":53,"Type":"udp"}]},
	 {"Ports":[{"IP":"::","PrivatePort":53,"PublicPort":30053,"Type":"udp"}]}]`
	client := &dockerClient{socket: "/unused", transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://docker"+dockerContainersPath {
			t.Fatalf("unexpected Docker request %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: ioNopCloser(body), Header: http.Header{}}, nil
	})}
	ports, err := client.publishedPorts(context.Background())
	if err != nil || len(ports) != 2 || !ports["tcp/8080"] || !ports["udp/30053"] {
		t.Fatalf("ports = %v, %v", ports, err)
	}
}

// The sync action needs configure's approval; rejections become host
// runtime issues and resolve when they clear.
func TestSyncPortsRequiresApprovalAndRecordsRejections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime-issues.json")
	previous := hostIssues
	t.Cleanup(func() { hostIssues = previous })
	hostIssues = func() *runtimeissues.Store { return runtimeissues.Open(path, nil) }

	rt := newFakeRuntime(t)
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{NetworkPolicy: true, PortRangeFirst: 30000, PortRangeLast: 30100}}}
	if result, err := newBackendForTest(store, rt).SyncPorts(ctx); !errors.Is(err, ErrUnconfirmed) || result.Approved {
		t.Fatalf("unapproved = %+v, %v", result, err)
	}
	store.state.Ownership.PortBindings = true
	rt.portRejections = []PortRejection{{PortBinding: PortBinding{Workspace: "main", Lease: "forgejo.runners", Protocol: "tcp", HostPort: 30022}, Reason: "port_in_use"}}
	if _, err := newBackendForTest(store, rt).SyncPorts(ctx); err != nil || !slices.Contains(rt.calls, "ports-sync 30000-30100") {
		t.Fatalf("sync = %v, calls %v", err, rt.calls)
	}
	issues, err := runtimeissues.Load(path)
	if err != nil || len(issues) != 1 || issues[0].Key != "incus.port-binding/main/forgejo.runners/tcp/30022" || !issues[0].Open() || issues[0].Source != runtimeissues.SourceHostd {
		t.Fatalf("issues = %+v, %v", issues, err)
	}
	rt.portRejections = nil
	if _, err := newBackendForTest(store, rt).SyncPorts(ctx); err != nil {
		t.Fatal(err)
	}
	if issues, _ = runtimeissues.Load(path); issues[0].Open() {
		t.Fatal("a cleared rejection stayed open")
	}
}

func TestPortBindingsAreConfiguredRestoredAndUninstalled(t *testing.T) {
	ctx := context.Background()
	previous := hostIssues
	t.Cleanup(func() { hostIssues = previous })
	hostIssues = func() *runtimeissues.Store { return runtimeissues.Open(filepath.Join(t.TempDir(), "issues.json"), nil) }
	store := &memoryStore{state: State{Schema: StateSchema, Ownership: Ownership{PortBindings: true}}}
	rt := newFakeRuntime(t)
	if err := newBackendForTest(store, rt).RestoreHostNetwork(ctx); err != nil || !slices.Equal(rt.calls, []string{"restore-port-bindings"}) {
		t.Fatalf("restore = %v, %v", rt.calls, err)
	}
}
