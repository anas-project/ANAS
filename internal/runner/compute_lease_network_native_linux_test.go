//go:build linux

package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/incusprovision"
	"github.com/anas-project/ANAS/internal/modulestore"
	"github.com/anas-project/ANAS/internal/runtimeissues"
	"gopkg.in/yaml.v3"
)

// Lease network acceptance (INCUS-R-112--R-164) on the same disposable VM and
// approved host the Core projection test uses. The VM host plays an Incus host
// that also runs Docker; two network namespaces play a LAN machine and an
// internet host; a Docker container plays another Module; the real Traefik and
// lego Modules run beside five leases of four synthetic consumers. Every
// reachability answer comes from one probe program (lease-net-agent) running
// on the host, in the namespaces, in the container and inside the guests.
//
// The orchestrator runs the stages in order; they share one private state
// file. Each matrix stage records every check, expected and observed, and fails
// only after all of them ran, so one run reports every mismatch at once.

const (
	lnetStateFile  = coreNativeReports + "/private/lease-network-state.json"
	lnetAgent      = coreNativeInputs + "/lease-net-agent"
	lnetAuthHook   = coreNativeInputs + "/lnet-auth-hook"
	lnetGuestAgent = "/usr/local/bin/lease-net-agent"
	lnetRunDir     = "/run/lnet"

	lnetLANNS, lnetWANNS         = "anaslan", "anaswan"
	lnetHostLAN4, lnetLANClient4 = "198.51.100.1", "198.51.100.2"
	lnetHostLAN6, lnetLANClient6 = "2001:db8:51::1", "2001:db8:51::2"
	lnetHostWAN4, lnetWAN4       = "203.0.113.1", "203.0.113.2"
	lnetHostWAN6, lnetWAN6       = "2001:db8:203::1", "2001:db8:203::2"
	lnetLinkLocal                = "169.254.77.2"
	lnetLANExtra                 = "198.51.100.0/24,2001:db8:51::/64"
	lnetLateAddress              = "198.51.100.50"

	lnetModuleNet, lnetModuleSubnet, lnetModuleIP = "lnmod", "192.168.77.0/24", "192.168.77.10"
	lnetLateNet, lnetLateSubnet, lnetLateIP       = "lnlate", "192.168.88.0/24", "192.168.88.10"
	lnetModulePublished                           = 18444
	lnetHostPort                                  = 18446
	lnetTraefikPort                               = 9000
	lnetLANBinding                                = 31001
	lnetConflictPort                              = 31500
	lnetDockerConflictPort                        = 31600
	lnetBaseDomain                                = "core.native.test"
	lnetMirror                                    = "m.daocloud.io/docker.io"
)

type lnetLease struct {
	Consumer    string `json:"consumer"`
	Resource    string `json:"resource"`
	Project     string `json:"project"`
	Bridge      string `json:"bridge"`
	IPv4Subnet  string `json:"ipv4_subnet"`
	IPv4Gateway string `json:"ipv4_gateway"`
	IPv6Subnet  string `json:"ipv6_subnet,omitempty"`
	IPv6Gateway string `json:"ipv6_gateway,omitempty"`
	Egress      string `json:"egress"`
}

type lnetInstance struct {
	Name    string `json:"name"`
	Lease   string `json:"lease"`
	Project string `json:"project"`
	IPv4    string `json:"ipv4"`
	IPv6    string `json:"ipv6,omitempty"`
	Slot    string `json:"slot,omitempty"`
}

type lnetState struct {
	ID              string                   `json:"id"`
	DockerID        string                   `json:"docker_id"`
	Registry        string                   `json:"registry"`
	ContainerPrefix string                   `json:"container_prefix"`
	NetworkPrefix   string                   `json:"network_prefix"`
	AgentImage      string                   `json:"agent_image"`
	Pin             string                   `json:"pin"`
	Deployment      string                   `json:"deployment"`
	HostUplink      string                   `json:"host_uplink"`
	Leases          map[string]*lnetLease    `json:"leases"`
	Instances       map[string]*lnetInstance `json:"instances"`
	Ports           map[string]int           `json:"ports"`
	Traefik         string                   `json:"traefik_container"`
	TraefikAddrs    []string                 `json:"traefik_addresses"`
	Hosts           map[string]string        `json:"hosts"`
	Snapshots       map[string]lnetSnapshot  `json:"snapshots"`
	Revoked         []string                 `json:"revoked"`
	SquatPort       int                      `json:"squat_port,omitempty"`
	ConsoleRoutes   []string                 `json:"console_routes,omitempty"`
	DockerForward   map[string][]string      `json:"docker_forward,omitempty"`
}

func lnetLoad(t *testing.T) *lnetState {
	t.Helper()
	id, _ := coreNativeGuard(t)
	body, err := os.ReadFile(lnetStateFile)
	var state lnetState
	if err != nil || json.Unmarshal(body, &state) != nil || state.ID != id || state.Deployment == "" {
		t.Fatal("completed lease network deployment state required first")
	}
	return &state
}

func lnetSave(t *testing.T, state *lnetState) {
	t.Helper()
	body, err := json.MarshalIndent(state, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(lnetStateFile, append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

// lnetRun runs a fixture command with its own budget. Output goes to the
// private diagnostics only when the command fails.
func lnetRun(t *testing.T, label string, timeout time.Duration, input []byte, args ...string) []byte {
	t.Helper()
	out, err := lnetTry(label, timeout, input, args...)
	if err != nil {
		t.Fatalf("fixture command %s failed: %v", label, err)
	}
	return out
}

// lnetTry is lnetRun for a command whose failure the caller examines.
func lnetTry(label string, timeout time.Duration, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C", "LANG=C"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr coreNativeBuffer
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &stdout, &stderr, 3*time.Second
	err := cmd.Run()
	if err == nil && (stdout.truncated || stderr.truncated) {
		err = errors.New("output exceeded its bound")
	}
	if err != nil {
		_ = os.MkdirAll(coreNativeReports+"/private", 0700)
		body := append(append([]byte{}, stdout.Bytes()...), stderr.Bytes()...)
		_ = os.WriteFile(filepath.Join(coreNativeReports, "private", "lnet-"+label+".log"), body, 0600)
		return stdout.Bytes(), fmt.Errorf("%s: %w", label, err)
	}
	return stdout.Bytes(), nil
}

func lnetStep(t *testing.T, name string, fn func(*testing.T)) {
	t.Helper()
	if !t.Run(name, fn) {
		t.FailNow()
	}
}

func lnetWrite(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
}

// --- probes -------------------------------------------------------------------

type lnetProbe struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	Source     string `json:"source,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty"`
	HostHeader string `json:"host_header,omitempty"`
	Name       string `json:"name,omitempty"`
	Auth       string `json:"auth,omitempty"` // X-Lnet-Auth of an http/https probe
}

type lnetResult struct {
	OK     bool              `json:"ok"`
	Error  string            `json:"error,omitempty"`
	Tag    string            `json:"tag,omitempty"`
	Peer   string            `json:"peer,omitempty"`
	Status int               `json:"status,omitempty"`
	Body   map[string]string `json:"body,omitempty"`
	Answer []string          `json:"answer,omitempty"`
}

// lnetPlace is where a probe runs: the host, a namespace, a guest or a Docker
// container. Guests run the agent inside their own container, so a probe uses
// the guest's own routes, addresses and resolver.
type lnetPlace struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Project string `json:"project,omitempty"`
}

func lnetHost() lnetPlace            { return lnetPlace{Kind: "host", Name: "host"} }
func lnetNS(name string) lnetPlace   { return lnetPlace{Kind: "ns", Name: name} }
func lnetDock(name string) lnetPlace { return lnetPlace{Kind: "docker", Name: name} }

func (s *lnetState) guest(name string) lnetPlace {
	return lnetPlace{Kind: "guest", Name: name, Project: s.Instances[name].Project}
}

func (p lnetPlace) argv() []string {
	switch p.Kind {
	case "host":
		return []string{lnetAgent, "batch"}
	case "ns":
		return []string{"/usr/sbin/ip", "netns", "exec", p.Name, lnetAgent, "batch"}
	case "guest":
		return []string{"/usr/bin/incus", "exec", "--force-noninteractive", "--project", p.Project, p.Name, "--", lnetGuestAgent, "batch"}
	case "docker":
		return []string{"/usr/bin/docker", "exec", "-i", p.Name, "/agent", "batch"}
	}
	return nil
}

func lnetBatch(t *testing.T, place lnetPlace, probes []lnetProbe) map[string]lnetResult {
	t.Helper()
	results, err := lnetTryBatch(place, probes)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func lnetTryBatch(place lnetPlace, probes []lnetProbe) (map[string]lnetResult, error) {
	input, _ := json.Marshal(probes)
	out, err := lnetTry("batch-"+place.Kind+"-"+place.Name, 60*time.Second, input, place.argv()...)
	if err != nil {
		return nil, err
	}
	var results map[string]lnetResult
	if json.Unmarshal(out, &results) != nil || len(results) != len(probes) {
		return nil, fmt.Errorf("probe batch at %s/%s returned an invalid answer", place.Kind, place.Name)
	}
	return results, nil
}

func lnetTCP(host string, port int) lnetProbe { return lnetProbe{Kind: "tcp", Host: host, Port: port} }
func lnetUDP(host string, port int) lnetProbe { return lnetProbe{Kind: "udp", Host: host, Port: port} }

// lnetConnect asks only whether a TCP handshake completes, for a peer that is
// not the probe agent (Traefik's HTTPS entrypoint).
func lnetConnect(host string, port int) lnetProbe {
	return lnetProbe{Kind: "connect", Host: host, Port: port}
}
func lnetDNS(server, name string) lnetProbe {
	return lnetProbe{Kind: "dns", Host: server, Port: 53, Name: name, TimeoutMS: 4000}
}
func lnetWeb(host string, port int, header string) lnetProbe {
	return lnetProbe{Kind: "https", Host: host, Port: port, HostHeader: header, TimeoutMS: 4000}
}

// lnetCheck is one expected answer. Want true needs the listener tagged Tag
// (and, when Peer is set, seeing that source); Want false needs no answer
// from anyone. Record marks an observation the requirements leave open.
type lnetCheck struct {
	Reqs   []string   `json:"reqs"`
	From   lnetPlace  `json:"from"`
	Probe  lnetProbe  `json:"probe"`
	Want   bool       `json:"want"`
	Tag    string     `json:"tag,omitempty"`
	Peer   string     `json:"peer,omitempty"`
	Record bool       `json:"record_only,omitempty"`
	Got    lnetResult `json:"got"`
	Pass   bool       `json:"pass"`
}

type lnetMatrix struct {
	checks []*lnetCheck
}

func (m *lnetMatrix) want(reqs string, from lnetPlace, probe lnetProbe, tag, peer string) {
	m.checks = append(m.checks, &lnetCheck{Reqs: strings.Fields(reqs), From: from, Probe: probe, Want: true, Tag: tag, Peer: peer})
}

func (m *lnetMatrix) deny(reqs string, from lnetPlace, probe lnetProbe) {
	m.checks = append(m.checks, &lnetCheck{Reqs: strings.Fields(reqs), From: from, Probe: probe})
}

func (m *lnetMatrix) record(reqs string, from lnetPlace, probe lnetProbe) {
	m.checks = append(m.checks, &lnetCheck{Reqs: strings.Fields(reqs), From: from, Probe: probe, Record: true})
}

func (c *lnetCheck) judge() bool {
	if c.Record {
		return true
	}
	got := c.Got
	if !c.Want {
		return !got.OK
	}
	if !got.OK {
		return false
	}
	switch c.Probe.Kind {
	case "http", "https":
		return got.Status == 200 && (c.Tag == "" || got.Body["tag"] == c.Tag) && (c.Peer == "" || got.Body["remote"] == c.Peer)
	case "dns":
		return true // any well-formed answer, whatever its code, is the resolver reached
	}
	return (c.Tag == "" || got.Tag == c.Tag) && (c.Peer == "" || got.Peer == c.Peer)
}

// run executes every check, grouped by place, writes the report and fails the
// test once with every mismatch.
func (m *lnetMatrix) run(t *testing.T, report string) {
	t.Helper()
	groups := map[lnetPlace][]*lnetCheck{}
	for i, check := range m.checks {
		check.Probe.ID = strconv.Itoa(i)
		groups[check.From] = append(groups[check.From], check)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failures []string
	for place, checks := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			probes := make([]lnetProbe, len(checks))
			for i, check := range checks {
				probes[i] = check.Probe
			}
			results, err := lnetTryBatch(place, probes)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Sprintf("probe place %s/%s failed: %v", place.Kind, place.Name, err))
				return
			}
			for _, check := range checks {
				check.Got = results[check.Probe.ID]
			}
		}()
	}
	wg.Wait()
	for _, check := range m.checks {
		check.Pass = check.judge()
		if !check.Pass {
			failures = append(failures, fmt.Sprintf("%v %s/%s %s %s:%d want=%v tag=%q peer=%q got=%+v",
				check.Reqs, check.From.Kind, check.From.Name, check.Probe.Kind, check.Probe.Host, check.Probe.Port, check.Want, check.Tag, check.Peer, check.Got))
		}
	}
	body, _ := json.MarshalIndent(m.checks, "", " ")
	lnetWrite(t, filepath.Join(coreNativeReports, "reports", report+".json"), body, 0600)
	if len(failures) > 0 {
		slices.Sort(failures)
		t.Fatalf("%d of %d lease network checks failed:\n%s", len(failures), len(m.checks), strings.Join(failures, "\n"))
	}
}

// --- fixture topology ------------------------------------------------------------

// lnetTopology creates (or, after a reboot, recreates) the two namespaces and
// the long-running listeners. Listeners are transient systemd units, so they
// outlive each test process and vanish with the VM.
func lnetTopology(t *testing.T) {
	t.Helper()
	for _, spec := range []struct{ ns, host, peer, host4, peer4, host6, peer6 string }{
		{lnetLANNS, "lnlan0", "lnlan1", lnetHostLAN4, lnetLANClient4, lnetHostLAN6, lnetLANClient6},
		{lnetWANNS, "lnwan0", "lnwan1", lnetHostWAN4, lnetWAN4, lnetHostWAN6, lnetWAN6},
	} {
		run := func(label string, args ...string) { lnetRun(t, label, 20*time.Second, nil, args...) }
		run("netns-"+spec.ns, "/usr/sbin/ip", "netns", "add", spec.ns)
		run("veth-"+spec.ns, "/usr/sbin/ip", "link", "add", spec.host, "type", "veth", "peer", "name", spec.peer)
		run("move-"+spec.ns, "/usr/sbin/ip", "link", "set", spec.peer, "netns", spec.ns)
		run("addr4-"+spec.ns, "/usr/sbin/ip", "addr", "add", spec.host4+"/24", "dev", spec.host)
		run("addr6-"+spec.ns, "/usr/sbin/ip", "-6", "addr", "add", spec.host6+"/64", "dev", spec.host, "nodad")
		run("up-"+spec.ns, "/usr/sbin/ip", "link", "set", spec.host, "up")
		in := func(label string, args ...string) {
			run(label, append([]string{"/usr/sbin/ip", "netns", "exec", spec.ns}, args...)...)
		}
		in("lo-"+spec.ns, "/usr/sbin/ip", "link", "set", "lo", "up")
		in("peer4-"+spec.ns, "/usr/sbin/ip", "addr", "add", spec.peer4+"/24", "dev", spec.peer)
		in("peer6-"+spec.ns, "/usr/sbin/ip", "-6", "addr", "add", spec.peer6+"/64", "dev", spec.peer, "nodad")
		in("peerup-"+spec.ns, "/usr/sbin/ip", "link", "set", spec.peer, "up")
		in("route4-"+spec.ns, "/usr/sbin/ip", "route", "add", "default", "via", spec.host4)
		in("route6-"+spec.ns, "/usr/sbin/ip", "-6", "route", "add", "default", "via", spec.host6)
		if spec.ns == lnetWANNS {
			// A link-local destination behind the internet stand-in: reachable
			// from the host, never through any lease tier (INCUS-R-113).
			in("linklocal", "/usr/sbin/ip", "addr", "add", lnetLinkLocal+"/16", "dev", spec.peer)
			run("linklocal-route", "/usr/sbin/ip", "route", "add", "169.254.77.0/24", "dev", spec.host)
		}
	}
	if err := os.MkdirAll(lnetRunDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, unit := range []struct {
		name string
		argv []string
	}{
		{"lnet-lan", []string{"/usr/sbin/ip", "netns", "exec", lnetLANNS, lnetAgent, "serve", "--tag", "lan",
			"--tcp", "80,31001", "--udp", "5353", "--hold", "7100,7101", "--status", lnetRunDir + "/lan"}},
		{"lnet-wan", []string{"/usr/sbin/ip", "netns", "exec", lnetWANNS, lnetAgent, "serve", "--tag", "wan",
			"--tcp", "80,31001", "--udp", "5353", "--hold", "7100,7101", "--status", lnetRunDir + "/wan"}},
		{"lnet-host", []string{lnetAgent, "serve", "--tag", "host", "--tcp", strconv.Itoa(lnetHostPort), "--udp", strconv.Itoa(lnetHostPort)}},
	} {
		for _, dir := range []string{lnetRunDir + "/lan", lnetRunDir + "/wan"} {
			_ = os.MkdirAll(dir, 0755)
		}
		lnetRun(t, "unit-"+unit.name, 20*time.Second, nil, append([]string{"/usr/bin/systemd-run", "--unit=" + unit.name, "--collect",
			"--property=Restart=on-failure"}, unit.argv...)...)
	}
	lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP(lnetHostLAN4, lnetHostPort)}, "host")
	lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP(lnetLANClient4, 80)}, "lan")
	lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP(lnetWAN4, 80), lnetTCP(lnetLinkLocal, 80), lnetTCP(lnetWAN6, 80)}, "wan")
}

func lnetWaitListening(t *testing.T, place lnetPlace, probes []lnetProbe, tag string) {
	t.Helper()
	for i := range probes {
		probes[i].ID = strconv.Itoa(i)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		results, err := lnetTryBatch(place, probes)
		ready := err == nil
		for _, probe := range probes {
			ready = ready && results[probe.ID].OK && results[probe.ID].Tag == tag
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("listener %s did not answer at %s: %+v %v", tag, place.Name, results, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// --- module sources ---------------------------------------------------------------

type lnetResourceSpec struct {
	id, sandbox, prefix string
	max                 int
	network, publish    map[string]any
	httpOwner           bool
}

func lnetResource(r lnetResourceSpec) map[string]any {
	spec := map[string]any{
		"sandbox": r.sandbox, "instance_prefix": r.prefix,
		"quota":           map[string]any{"max_instances": r.max, "cpu": 1, "memory_mib": 512, "disk_gib": 4},
		"image_allowlist": []any{map[string]any{"catalog": "anas", "name": "native-core", "revision": "lab-r1"}},
		"credential":      map[string]any{"policy": "generated"},
		"deletion_policy": "retain",
	}
	if r.network != nil {
		spec["network"] = r.network
	}
	if r.publish != nil {
		spec["publish"] = r.publish
	}
	out := map[string]any{"id": r.id, "contract": "compute", "binding": "isolation", "spec": spec}
	if r.httpOwner {
		// The test process writes this lease's HTTP requests as root; a real
		// consumer names its own unprivileged identity here.
		out["http_request_owner"] = "0:0"
	}
	return out
}

func lnetManifest(t *testing.T, name string, resources ...lnetResourceSpec) []byte {
	t.Helper()
	var required []any
	dependencies := map[string]any{"contracts": []any{map[string]any{
		"name": "compute", "version": ">=1.0.0 <2.0.0", "selected_by": "isolation",
		"interfaces": []string{"incus_container"}, "default": "incus_container"}}}
	defaults := map[string]any{"isolation": "auto"}
	types := map[string]any{"isolation": map[string]any{"enum": []string{"auto", "incus_container"}}}
	for _, r := range resources {
		required = append(required, lnetResource(r))
		if http, _ := r.publish["http"].(map[string]any); http["auth"] == "forward_auth" {
			dependencies["requires_capabilities"] = []any{map[string]any{"name": "forward_auth",
				"interface_selected_by": "forward_auth_interface", "interfaces": map[string]any{"any_of": []string{"http"}}}}
			defaults["forward_auth_interface"] = "auto"
			types["forward_auth_interface"] = map[string]any{"enum": []string{"auto", "http"}}
		}
	}
	body, err := yaml.Marshal(map[string]any{
		"api_version": "anas.module/v1", "kind": "Module", "name": name, "version": "0.0.0", "revision": 1, "status": "developing",
		"abi":          map[string]any{"supports": []string{"anas.module-hook/v1"}},
		"runtime":      map[string]any{"type": "compose", "compose_file": "docker-compose.yml"},
		"dependencies": dependencies,
		"config":       map[string]any{"defaults": defaults, "types": types},
		"resources":    map[string]any{"requires": required},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func lnetCompose(name, id string) []byte {
	return []byte(`services:
  consumer:
    image: ${ANAS_IMAGE_REGISTRY}/lnet-consumer:r1
    container_name: ${CONTAINER_PREFIX}` + name + `
    command: [idle]
    user: '65532:65532'
    read_only: true
    cap_drop: [ALL]
    security_opt: ['no-new-privileges:true']
    labels: {dev.anas.native-core: '` + id + `'}
    networks: [business]
networks:
  business:
    name: ${NETWORK_PREFIX}` + name + `
    labels: {dev.anas.native-core: '` + id + `'}
`)
}

// lnauth is a synthetic forward_auth provider: a real provider's shape (a
// Hook that publishes the middleware, Compose labels that define it), with
// lease-net-agent as the gate. It is part of the deployment only while lnlan
// declares forward_auth (INCUS-R-095).
func lnetWriteAuthModule(t *testing.T, moduleRoot, id string) {
	t.Helper()
	manifest, err := yaml.Marshal(map[string]any{
		"api_version": "anas.module/v1", "kind": "Module", "name": "lnauth", "version": "0.0.0", "revision": 1, "status": "developing",
		"abi":          map[string]any{"supports": []string{"anas.module-hook/v1"}},
		"runtime":      map[string]any{"type": "compose", "compose_file": "docker-compose.yml"},
		"capabilities": map[string]any{"provides": []any{map[string]any{"name": "forward_auth", "interfaces": []string{"http"}}}},
		"dependencies": map[string]any{"requires": []string{"traefik"}},
		"config":       map[string]any{"exports": []string{"ANAS_FORWARD_AUTH_*"}},
		"logic":        map[string]any{"hook": map[string]any{"command": []string{"go", "run", "./hook"}, "phases": []string{"calculate"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook, err := os.ReadFile(lnetAuthHook)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(moduleRoot, "lnauth")
	lnetWrite(t, filepath.Join(dir, "module.yml"), manifest, 0600)
	lnetWrite(t, filepath.Join(dir, "hook", "bin", "linux-amd64", "anas-hook"), hook, 0700)
	lnetWrite(t, filepath.Join(dir, "docker-compose.yml"), []byte(`services:
  gate:
    image: ${ANAS_IMAGE_REGISTRY}/lnet-consumer:r1
    container_name: ${CONTAINER_PREFIX}lnauth
    command: [serve, --tag, lnauth, --auth, "9100"]
    user: '65532:65532'
    read_only: true
    cap_drop: [ALL]
    security_opt: ['no-new-privileges:true']
    labels:
      dev.anas.native-core: '`+id+`'
      traefik.enable: 'true'
      anas.traefik.instance: ${NETWORK_PREFIX}traefik
      traefik.http.services.lnet-auth.loadbalancer.server.port: '9100'
      traefik.http.middlewares.lnet-auth.forwardauth.address: http://${CONTAINER_PREFIX}lnauth:9100/
networks:
  default:
    external: true
    name: ${NETWORK_PREFIX}traefik
`), 0600)
}

// lnetSpecs is the declaration under test. Changes to it are expressed as
// options so each stage can render exactly the variant it needs.
type lnetVariant struct {
	withoutSide     bool // INCUS-R-124 (CLI): the second lninet lease is removed
	withoutInet     bool // INCUS-R-124 (console): the lninet module is removed
	lnlanHTTP8081   bool // INCUS-R-147: lnlan's HTTP authorization changes
	sideLAN         bool // INCUS-R-135: lnside changes tier
	withoutLeases   bool // INCUS-R-126: every consumer is removed before uninstall
	lnlanAuth       bool // INCUS-R-095: lnlan's HTTP publication declares forward_auth
	extraBinding    map[string]any
	extraBindingFor string
}

func lnetWriteSources(t *testing.T, moduleRoot, id string, v lnetVariant) {
	t.Helper()
	sideEgress := "internet"
	if v.sideLAN {
		sideEgress = "internet_lan"
	}
	lnlanPorts := []any{8080}
	if v.lnlanHTTP8081 {
		lnlanPorts = []any{8080, 8081}
	}
	lnlanAuth := "none"
	if v.lnlanAuth {
		lnlanAuth = "forward_auth"
	}
	lnlanBindings := []any{map[string]any{"protocol": "tcp", "host_port": lnetLANBinding, "slot": "s1", "guest_port": 7022}}
	lnhostBindings := []any{
		map[string]any{"protocol": "tcp", "host_port": "auto", "slot": "web", "guest_port": 7022},
		map[string]any{"protocol": "udp", "host_port": "auto", "slot": "web", "guest_port": 7053},
	}
	switch v.extraBindingFor {
	case "lnlan":
		lnlanBindings = append(lnlanBindings, v.extraBinding)
	case "lnhost":
		lnhostBindings = append(lnhostBindings, v.extraBinding)
	}
	inet := []lnetResourceSpec{{id: "workers", sandbox: "anas-lninet", prefix: "anas-lninet-", max: 2,
		network: map[string]any{"egress": "internet"}}}
	if !v.withoutSide {
		inet = append(inet, lnetResourceSpec{id: "side", sandbox: "anas-lnside", prefix: "anas-lnside-", max: 1,
			network: map[string]any{"egress": sideEgress}})
	}
	modules := map[string][]lnetResourceSpec{
		"lninet": inet,
		"lnlan": {{id: "workers", sandbox: "anas-lnlan", prefix: "anas-lnlan-", max: 2, httpOwner: true,
			network: map[string]any{"egress": "internet_lan", "module_access": true, "intra_lease": true, "ingress": "published",
				"slots": map[string]any{"s1": map[string]any{"instance": "anas-lnlan-a"}}},
			publish: map[string]any{
				"http":  map[string]any{"allowed_ports": lnlanPorts, "auth": lnlanAuth, "domain": map[string]any{"mode": "named", "prefix": "lnlan"}},
				"ports": lnlanBindings}}},
		"lnhost": {{id: "workers", sandbox: "anas-lnhost", prefix: "anas-lnhost-", max: 1,
			network: map[string]any{"egress": "internet_lan_host", "ingress": "published",
				"slots": map[string]any{"web": map[string]any{"instance": "anas-lnhost-web"}}},
			publish: map[string]any{"ports": lnhostBindings}}},
		"lnmods": {{id: "workers", sandbox: "anas-lnmods", prefix: "anas-lnmods-", max: 1,
			network: map[string]any{"egress": "modules_only"}}},
	}
	for name, resources := range modules {
		lnetWrite(t, filepath.Join(moduleRoot, name, "module.yml"), lnetManifest(t, name, resources...), 0600)
		lnetWrite(t, filepath.Join(moduleRoot, name, "docker-compose.yml"), lnetCompose(name, id), 0600)
	}
	lnetWriteAuthModule(t, moduleRoot, id)
}

func lnetConfig(state *lnetState, v lnetVariant) []byte {
	consumers := "  lninet: {}\n"
	if v.withoutInet {
		consumers = ""
	}
	consumers += "  lnlan: {}\n  lnhost: {}\n  lnmods: {}\n"
	if v.lnlanAuth {
		consumers += "  lnauth: {}\n"
	}
	if v.withoutLeases {
		consumers = ""
	}
	return []byte("modules:\n" + consumers + "  traefik: {}\n" +
		"  incus:\n    config:\n      lan_extra_subnets: '" + lnetLANExtra + "'\n" +
		"global:\n  base_domain: " + lnetBaseDomain + "\n  email: admin@" + lnetBaseDomain + "\n  virtual_domain: true\n" +
		"rollback:\n  snapshot: {backend: none}\n" +
		"env:\n  ANAS_IMAGE_REGISTRY: " + state.Registry + "\n  CONTAINER_PREFIX: " + state.ContainerPrefix +
		"\n  NETWORK_PREFIX: " + state.NetworkPrefix + "\n")
}

func lnetModuleRoot() string { return filepath.Join(coreNativeRoot, "source", "modules") }

// lnetImport writes the variant's sources and config and imports the config.
func lnetImport(t *testing.T, state *lnetState, label string, v lnetVariant) {
	t.Helper()
	lnetWriteSources(t, lnetModuleRoot(), state.ID, v)
	path := filepath.Join(coreNativeRoot, "config-"+label+".yml")
	lnetWrite(t, path, lnetConfig(state, v), 0600)
	// Later variants edit Module sources, and import validates against the
	// lock; relock the current config first or import refuses the stale digest.
	// Before the first apply the config is still the init skeleton.
	if state.Deployment != "" {
		coreNativeCLI(t, "lnet-lock-"+label, "lock", "-w", coreNativeWorkspace, "--root", lnetModuleRoot())
	}
	coreNativeCLI(t, "lnet-import-"+label, "config", "import", path, "-w", coreNativeWorkspace, "--root", lnetModuleRoot())
}

// lnetRenderApply renders with a lock update and applies through the CLI.
func lnetRenderApply(t *testing.T, state *lnetState, label string) string {
	t.Helper()
	result := coreNativeCLI(t, "lnet-render-"+label, "render", "-w", coreNativeWorkspace, "--root", lnetModuleRoot(), "--update-lock")
	var deployment string
	if json.Unmarshal(result["deployment_id"], &deployment) != nil || validateDeploymentID(deployment) != nil {
		t.Fatal("rendered deployment identity invalid")
	}
	lnetRun(t, "apply-"+label, 15*time.Minute, nil, "/usr/local/bin/anas", "apply", "-w", coreNativeWorkspace,
		"--deployment", deployment, "--yes", "--no-snapshot", "--json")
	active, err := loadActiveState(stateDir(coreNativeWorkspace))
	if err != nil || active.ActiveDeployment != deployment {
		t.Fatal("the rendered deployment was not activated", err)
	}
	state.Deployment = deployment
	return deployment
}

// lnetRejectedApply renders and applies a variant that must fail, and returns
// the CLI error code; the active deployment must not change.
func lnetRejectedApply(t *testing.T, state *lnetState, label string, v lnetVariant) string {
	t.Helper()
	before, err := loadActiveState(stateDir(coreNativeWorkspace))
	if err != nil {
		t.Fatal(err)
	}
	lnetImport(t, state, label, v)
	out, applyErr := lnetTry("rejected-"+label, 10*time.Minute, nil, "/usr/local/bin/anas", "apply", "-w", coreNativeWorkspace,
		"--root", lnetModuleRoot(), "--update-lock", "--yes", "--no-snapshot", "--json")
	var failure struct {
		OK    bool                           `json:"ok"`
		Error struct{ Code, Message string } `json:"error"`
	}
	if applyErr == nil || json.Unmarshal(bytes.TrimSpace(out), &failure) != nil || failure.OK {
		t.Fatalf("variant %s was not rejected", label)
	}
	after, err := loadActiveState(stateDir(coreNativeWorkspace))
	if err != nil || after.ActiveDeployment != before.ActiveDeployment {
		t.Fatal("a rejected apply changed the active deployment")
	}
	return failure.Error.Code + ": " + failure.Error.Message
}

// --- reading the product's state ---------------------------------------------------

func lnetResourceState(t *testing.T, consumer, resource string) resourceState {
	t.Helper()
	var state resourceState
	if err := readYAML(filepath.Join(stateDir(coreNativeWorkspace), "state", "resources", consumer+"."+resource+".yml"), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func lnetLeaseEnv(t *testing.T, state *lnetState, consumer string) map[string]string {
	t.Helper()
	env, err := parseEnvFile(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment, "modules", consumer, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func lnetClient(t *testing.T, state *lnetState, consumer, resource string) (*computeclient.Client, computeclient.Lease, map[string]string) {
	t.Helper()
	env := lnetLeaseEnv(t, state, consumer)
	lease, err := computeclient.LeaseFromLookup(func(key string) string { return env[key] }, consumer, resource)
	if err != nil {
		t.Fatal(err)
	}
	// One directory per client: the Incus CLI rewrites config.yml on first
	// use, and the shared client never reuses a directory it did not leave
	// unchanged (consumers get a fresh tmpfs /run on every start).
	dir := filepath.Join(coreNativeReports, "private", "client-"+consumer+"-"+resource+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	client, err := computeclient.NewWithContext(ctx, lease, []string{lnetGuestAgent}, dir)
	if err != nil {
		t.Fatalf("shared client for %s.%s: %v", consumer, resource, err)
	}
	return client, lease, env
}

type lnetInstanceState struct {
	Status  string `json:"status"`
	Pid     int    `json:"pid"`
	Network map[string]struct {
		Addresses []struct {
			Family  string `json:"family"`
			Address string `json:"address"`
			Scope   string `json:"scope"`
		} `json:"addresses"`
	} `json:"network"`
}

func lnetInstanceAddresses(t *testing.T, project, name string, prefer ...string) (string, string, string) {
	t.Helper()
	want6 := ""
	if len(prefer) > 0 {
		want6 = prefer[0]
	}
	var state lnetInstanceState
	if coreNativeQuery(t, http.MethodGet, "/1.0/instances/"+name+"/state?project="+project, &state) != 200 {
		t.Fatalf("instance %s state unreadable", name)
	}
	v4, v6 := "", ""
	for _, address := range state.Network["eth0"].Addresses {
		if address.Scope != "global" {
			continue
		}
		if address.Family == "inet" && v4 == "" {
			v4 = address.Address
		}
		// A slot's fixed IPv6 wins over any other global address the guest
		// configured for itself.
		if address.Family == "inet6" && (v6 == "" || address.Address == want6) {
			v6 = address.Address
		}
	}
	return state.Status, v4, v6
}

func lnetIncus(t *testing.T, label string, input []byte, args ...string) []byte {
	t.Helper()
	return lnetRun(t, label, 90*time.Second, input, append([]string{"/usr/bin/incus"}, args...)...)
}

// lnetGuestService installs the probe agent and a persistent listener unit
// inside a guest. The unit is enabled, so it returns after a host reboot.
func lnetGuestService(t *testing.T, instance *lnetInstance) {
	t.Helper()
	lnetIncus(t, "push-agent-"+instance.Name, nil, "file", "push", "--project", instance.Project, "--mode", "0755",
		lnetAgent, instance.Name+lnetGuestAgent)
	unit := "[Unit]\nDescription=lease network acceptance listener\nAfter=network-online.target\n\n[Service]\nExecStart=" +
		lnetGuestAgent + " serve --tag " + instance.Name + " --tcp 7022,7023 --udp 7053 --http 8080,8081\nRestart=always\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
	path := filepath.Join(coreNativeReports, "private", "lnagent.service")
	lnetWrite(t, path, []byte(unit), 0644)
	lnetIncus(t, "push-unit-"+instance.Name, nil, "file", "push", "--project", instance.Project, "--mode", "0644",
		path, instance.Name+"/etc/systemd/system/lnagent.service")
	lnetIncus(t, "enable-unit-"+instance.Name, nil, "exec", "--force-noninteractive", "--project", instance.Project, instance.Name, "--",
		"systemctl", "enable", "--now", "lnagent.service")
}

// lnetTraefik returns the Traefik container's current addresses.
func lnetTraefikAddresses(t *testing.T, container string) []string {
	t.Helper()
	var inspected []struct {
		Config          struct{ Labels map[string]string }
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
	}
	if json.Unmarshal(lnetRun(t, "traefik-inspect", 30*time.Second, nil, "/usr/bin/docker", "inspect", container), &inspected) != nil || len(inspected) != 1 ||
		inspected[0].Config.Labels[incusprovision.TraefikInstanceLabel] == "" {
		t.Fatal("Traefik container is not the labelled Traefik Module")
	}
	var out []string
	for _, network := range inspected[0].NetworkSettings.Networks {
		if network.IPAddress != "" {
			out = append(out, network.IPAddress)
		}
	}
	slices.Sort(out)
	return out
}

func lnetAddressSet(t *testing.T, name string) []string {
	t.Helper()
	var set struct {
		Addresses []string          `json:"addresses"`
		Config    map[string]string `json:"config"`
	}
	if coreNativeQuery(t, http.MethodGet, "/1.0/network-address-sets/"+name+"?project=default", &set) != 200 {
		t.Fatalf("address set %s is missing", name)
	}
	out := []string{}
	for _, address := range set.Addresses {
		if prefix, err := netip.ParsePrefix(address); err == nil && prefix.IsSingleIP() {
			address = prefix.Addr().String()
		}
		out = append(out, address)
	}
	slices.Sort(out)
	return out
}

// lnetWaitTraefikSet waits for hostd to write Traefik's current addresses.
func lnetWaitTraefikSet(t *testing.T, state *lnetState) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		want := lnetTraefikAddresses(t, state.Traefik)
		got := lnetAddressSet(t, incusprovision.TraefikAddressSet)
		if len(want) > 0 && slices.Equal(want, got) {
			state.TraefikAddrs = want
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("anas-traefik %v does not follow Traefik %v (INCUS-R-118)", got, want)
		}
		time.Sleep(2 * time.Second)
	}
}

func lnetHostdInvocations(t *testing.T, action string) int {
	t.Helper()
	entries, err := os.ReadDir("/var/lib/anas-hostd/invocations")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("/var/lib/anas-hostd/invocations", entry.Name()))
		var record struct {
			Action string `json:"action"`
		}
		if err == nil && json.Unmarshal(body, &record) == nil && record.Action == action {
			count++
		}
	}
	return count
}

func lnetPortsFile(t *testing.T) []incusprovision.PortBinding {
	t.Helper()
	body, err := os.ReadFile(incusprovision.PortBindingsPath)
	var file struct {
		Schema   string                       `json:"schema"`
		Bindings []incusprovision.PortBinding `json:"bindings"`
	}
	if err != nil || json.Unmarshal(body, &file) != nil || file.Schema != incusprovision.PortBindingsSchema {
		t.Fatal("hostd port binding record unreadable")
	}
	return file.Bindings
}

// lnetWaitPorts waits until hostd has applied exactly the expected bindings:
// the record, the nft maps and every hold unit.
func lnetWaitPorts(t *testing.T, want map[string]int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		problem := lnetPortsProblem(t, want)
		if problem == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("port bindings not applied by hostd (INCUS-R-152): %s", problem)
		}
		time.Sleep(2 * time.Second)
	}
}

// lnetPortsProblem compares hostd's record and the kernel with want, keyed
// "<lease>/<protocol>/<guest port>" with the host port as value.
func lnetPortsProblem(t *testing.T, want map[string]int) string {
	t.Helper()
	if _, err := os.Stat(incusprovision.PortBindingsPath); err != nil {
		return "no port binding record"
	}
	got := map[string]int{}
	for _, binding := range lnetPortsFile(t) {
		got[binding.Lease+"/"+binding.Protocol+"/"+strconv.Itoa(binding.GuestPort)] = binding.HostPort
	}
	if !mapsEqual(got, want) {
		return fmt.Sprintf("record %v want %v", got, want)
	}
	ruleset := string(lnetRun(t, "nft-ports", 20*time.Second, nil, "/usr/sbin/nft", "list", "table", "inet", incusprovision.PortTable))
	for key, port := range want {
		protocol := strings.Split(key, "/")[1]
		if !regexp.MustCompile(`\b` + strconv.Itoa(port) + ` : `).MatchString(ruleset) {
			return "nft map lacks " + key
		}
		active, _ := lnetTry("hold-"+key, 10*time.Second, nil, "/usr/bin/systemctl", "is-active",
			"anas-port-"+protocol+"@"+strconv.Itoa(port)+".socket", "anas-port-"+protocol+"@"+strconv.Itoa(port)+".service")
		if strings.TrimSpace(string(active)) != "active\nactive" {
			return "hold units for " + key + " are not active"
		}
	}
	return ""
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func lnetIssues(t *testing.T, path string) []runtimeissues.Issue {
	t.Helper()
	issues, err := runtimeissues.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func lnetOpenIssue(issues []runtimeissues.Issue, key, source string) bool {
	return slices.ContainsFunc(issues, func(i runtimeissues.Issue) bool {
		return i.Key == key && i.Open() && (source == "" || i.Source == source)
	})
}

func lnetWaitIssue(t *testing.T, path, key, source string, open bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if lnetOpenIssue(lnetIssues(t, path), key, source) == open {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime issue %s (source %q) open=%v not observed within %s", key, source, open, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// --- firewall snapshot (INCUS-R-135) -------------------------------------------------

// lnetSnapshot is what INCUS-R-135 freezes around a publication, a revocation
// or a tier change: the host ruleset outside Incus's own table and the port
// binding table, the routes outside Incus guest veths, and permanent
// neighbours. Counters and route expiry are not state.
type lnetSnapshot struct {
	Ruleset   string   `json:"ruleset"`
	Routes    []string `json:"routes"`
	Neighbors string   `json:"neighbors"`
}

// Guest (Incus, 8 hex) and container (Docker, 7 hex) host-side interfaces
// come and go with their workloads.
var lnetVethPattern = regexp.MustCompile(`^veth[0-9a-f]{7,8}$`)

// Docker writes its own rules and routes for each Module network, and Compose
// recreates or removes that network when the Module's source changes or the
// Module leaves the deployment. Those belong to Docker, not to the lease
// network (INCUS-R-135), and are left out.
var lnetDockerBridge = regexp.MustCompile(`br-[0-9a-f]{12}`)

func lnetTakeSnapshot(t *testing.T) lnetSnapshot {
	t.Helper()
	var kept []string
	skip := false
	for _, line := range strings.Split(string(lnetRun(t, "nft-ruleset", 30*time.Second, nil, "/usr/sbin/nft", "-s", "list", "ruleset")), "\n") {
		if strings.HasPrefix(line, "table ") {
			// Incus's own tables (inet, and bridge for per-instance NIC
			// filters) and the port binding table are excluded (INCUS-R-135).
			skip = strings.HasSuffix(line, " incus {") || line == "table inet "+incusprovision.PortTable+" {"
		}
		if !skip && !lnetDockerBridge.MatchString(line) {
			kept = append(kept, line)
		}
		if line == "}" {
			skip = false
		}
	}
	var routes []string
	for _, family := range []string{"-4", "-6"} {
		var parsed []map[string]any
		if json.Unmarshal(lnetRun(t, "routes"+family, 20*time.Second, nil, "/usr/sbin/ip", "-j", family, "route", "show", "table", "all"), &parsed) != nil {
			t.Fatal("route table unreadable")
		}
		for _, route := range parsed {
			device, _ := route["dev"].(string)
			if lnetVethPattern.MatchString(device) || lnetDockerBridge.MatchString(device) {
				continue
			}
			delete(route, "expires")
			delete(route, "cache")
			// A bridge whose last instance stopped is linkdown; link state is
			// not a routing change.
			if flags, ok := route["flags"].([]any); ok {
				route["flags"] = slices.DeleteFunc(flags, func(flag any) bool { return flag == "linkdown" })
			}
			body, _ := json.Marshal(route)
			routes = append(routes, string(body))
		}
	}
	slices.Sort(routes)
	neighbors := string(lnetRun(t, "neigh", 20*time.Second, nil, "/usr/sbin/ip", "neigh", "show", "nud", "permanent"))
	return lnetSnapshot{Ruleset: strings.Join(kept, "\n"), Routes: routes, Neighbors: neighbors}
}

func lnetSameSnapshot(t *testing.T, label string, before, after lnetSnapshot) {
	t.Helper()
	if before.Ruleset == after.Ruleset && slices.Equal(before.Routes, after.Routes) && before.Neighbors == after.Neighbors {
		return
	}
	body, _ := json.MarshalIndent(map[string]lnetSnapshot{"before": before, "after": after}, "", " ")
	lnetWrite(t, filepath.Join(coreNativeReports, "reports", "snapshot-diff-"+label+".json"), body, 0600)
	t.Fatalf("host firewall, routes or neighbours changed across %s (INCUS-R-135)", label)
}

// --- stage 1: deploy ------------------------------------------------------------------

func TestNativeLeaseNetworkDeploy(t *testing.T) {
	id, _ := coreNativeGuard(t)
	if _, err := os.Lstat(coreNativeRoot); !os.IsNotExist(err) {
		t.Fatal("fresh experiment root required")
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir(coreNativeWorkspace), "state", "deployments")); err != nil || len(entries) != 0 {
		t.Fatal("the registered workspace must be freshly initialized")
	}
	for _, dir := range []string{coreNativeRoot, coreNativeReports + "/private", coreNativeReports + "/reports"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	suffix := strings.TrimPrefix(id, "anas-incus-host-")
	state := &lnetState{ID: id, Registry: "anas-lnet-" + suffix, ContainerPrefix: "lnet" + suffix + "_", NetworkPrefix: "lnet" + suffix + "_",
		Leases: map[string]*lnetLease{}, Instances: map[string]*lnetInstance{}, Ports: map[string]int{}, Hosts: map[string]string{},
		Snapshots: map[string]lnetSnapshot{}}
	var bundle incusprovision.ConnectionBundle

	lnetStep(t, "approved_host_and_dedicated_docker", func(t *testing.T) {
		body, err := os.ReadFile(incusprovision.DefaultBundlePath)
		if err != nil || json.Unmarshal(body, &bundle) != nil || bundle.Architecture != "amd64" {
			t.Fatal("installed host connection was not established by the approved actions")
		}
		var info struct{ ID, DockerRootDir, ServerVersion string }
		if json.Unmarshal(lnetRun(t, "docker-info", 30*time.Second, nil, "/usr/bin/docker", "info", "--format", "{{json .}}"), &info) != nil ||
			info.DockerRootDir != "/var/lib/anas-host-provision-test" || info.ID == "" {
			t.Fatal("not the dedicated VM Docker daemon")
		}
		state.DockerID = info.ID
		if len(bytes.TrimSpace(lnetRun(t, "docker-empty", 30*time.Second, nil, "/usr/bin/docker", "ps", "-aq"))) != 0 {
			t.Fatal("preexisting containers are not test targets")
		}
		lnetWrite(t, filepath.Join(coreNativeReports, "reports", "docker-version.json"),
			[]byte(`{"server_version":"`+info.ServerVersion+`"}`), 0600)
		route := strings.Fields(string(lnetRun(t, "uplink", 10*time.Second, nil, "/usr/sbin/ip", "-4", "route", "get", "192.0.2.1")))
		for i := range route {
			if route[i] == "src" && i+1 < len(route) {
				state.HostUplink = route[i+1]
			}
		}
		if net := netip.MustParseAddr(state.HostUplink); !net.Is4() {
			t.Fatal("host uplink address unknown")
		}
	})

	lnetStep(t, "lan_internet_and_module_stand_ins", func(t *testing.T) {
		lnetTopology(t)
		// The agent doubles as the Docker transport image for the Module
		// stand-in and the consumers' idle service.
		state.AgentImage = coreNativeImage(t, id, "lease-net-agent", "/agent", state.Registry+"/lnet-consumer:r1")
		lnetRun(t, "module-network", 30*time.Second, nil, "/usr/bin/docker", "network", "create", "--subnet", lnetModuleSubnet,
			"--label", "dev.anas.native-core="+id, lnetModuleNet)
		lnetRun(t, "module-stand-in", 60*time.Second, nil, "/usr/bin/docker", "run", "-d", "--name", "lnmodule",
			"--restart", "unless-stopped", "--label", "dev.anas.native-core="+id, "--network", lnetModuleNet, "--ip", lnetModuleIP,
			"-p", strconv.Itoa(lnetModulePublished)+":9001/tcp", "-p", strconv.Itoa(lnetModulePublished)+":9001/udp",
			"--entrypoint", "/agent", state.AgentImage, "serve", "--tag", "module", "--tcp", "9001,9002", "--udp", "9001")
		lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP(lnetModuleIP, 9002), lnetTCP(lnetHostLAN4, lnetModulePublished)}, "module")
	})

	var entry computeimage.Entry
	lnetStep(t, "module_sources_images_and_bootable_guest", func(t *testing.T) {
		if err := copyDir(coreNativeInputs+"/bundle", filepath.Join(coreNativeRoot, "source")); err != nil {
			t.Fatal(err)
		}
		moduleRoot := lnetModuleRoot()
		provider := filepath.Join(moduleRoot, "incus")
		coreNativeImage(t, id, "provisioner", "/usr/local/bin/anas-incus-provisioner", state.Registry+"/"+coreNativeComposeImage(t, provider, "anas-incus-provisioner"))
		// The real Traefik and lego Modules, built from their own Dockerfiles
		// through the mainland mirror the product's build speedup names.
		for _, build := range []struct {
			module, context string
			args            []string
		}{
			{"traefik", "traefik", nil},
			{"lego", "lego", []string{"--build-arg", "CHINESE_BUILD_SPEEDUP=true", "--build-arg", "APK_MIRROR_URL=https://mirrors.aliyun.com"}},
		} {
			image := coreNativeComposeImage(t, filepath.Join(moduleRoot, build.module), "anas-"+build.module)
			args := append([]string{"/usr/bin/docker", "build", "--build-arg", "DOCKER_HUB_REGISTRY=" + lnetMirror}, build.args...)
			args = append(args, "--label", "dev.anas.native-core="+id, "-t", state.Registry+"/"+image, filepath.Join(moduleRoot, build.module, build.context))
			lnetRun(t, "build-"+build.module, 15*time.Minute, nil, args...)
		}
		entry = lnetGuestImage(t, provider)
		state.Pin = entry.Fingerprint
		catalog, _ := json.Marshal([]computeimage.Entry{entry})
		lnetWrite(t, filepath.Join(provider, "images", "catalog.json"), catalog, 0600)
	})

	lnetStep(t, "cli_render_and_apply", func(t *testing.T) {
		lnetImport(t, state, "initial", lnetVariant{})
		lnetRenderApply(t, state, "initial")
		var inspected []struct{ Id string }
		out := lnetRun(t, "traefik-find", 30*time.Second, nil, "/usr/bin/docker", "ps", "-q", "--filter", "label="+incusprovision.TraefikInstanceLabel)
		ids := strings.Fields(string(out))
		if len(ids) != 1 {
			t.Fatalf("expected exactly one Traefik container, found %d", len(ids))
		}
		if json.Unmarshal(lnetRun(t, "traefik-id", 30*time.Second, nil, "/usr/bin/docker", "inspect", ids[0]), &inspected) != nil || len(inspected) != 1 {
			t.Fatal("Traefik container unreadable")
		}
		state.Traefik = inspected[0].Id
	})

	lnetStep(t, "frozen_networks_and_reported_bridges", func(t *testing.T) {
		manifest, err := loadDeploymentManifest(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment))
		if err != nil || len(manifest.Resources) != 5 {
			t.Fatal("five compute leases were not frozen", err)
		}
		wantEgress := map[string]string{"lninet.workers": "internet", "lninet.side": "internet", "lnlan.workers": "internet_lan",
			"lnhost.workers": "internet_lan_host", "lnmods.workers": "modules_only"}
		for _, resource := range manifest.Resources {
			key := resource.Consumer + "." + resource.ID
			network := resource.ComputeNetwork
			if network == nil || network.Egress != wantEgress[key] {
				t.Fatalf("%s did not freeze its egress tier (INCUS-R-112)", key)
			}
			if key == "lnhost.workers" {
				for _, binding := range network.Ports {
					// INCUS-R-153: auto picks from the approved default range.
					if !binding.Auto || binding.HostPort < incusprovision.DefaultPortRangeFirst || binding.HostPort > incusprovision.DefaultPortRangeLast {
						t.Fatalf("auto port binding not resolved inside the approved range: %+v", binding)
					}
					state.Ports[key+"/"+binding.Protocol+"/"+strconv.Itoa(int(binding.GuestPort))] = int(binding.HostPort)
				}
			}
			if key == "lnlan.workers" {
				if len(network.Ports) != 1 || network.Ports[0].HostPort != lnetLANBinding || network.TraefikPort != lnetTraefikPort {
					t.Fatal("lnlan's explicit binding or Traefik port was not frozen")
				}
				state.Ports[key+"/tcp/7022"] = lnetLANBinding
			}
			recorded := lnetResourceState(t, resource.Consumer, resource.ID)
			n := recorded.Actual.ComputeNetwork
			if recorded.Status != "ready" || n == nil || n.Bridge == "" || n.IPv4Subnet == "" || n.IPv4Gateway == "" {
				t.Fatalf("%s has no reported lease network (INCUS-R-159)", key)
			}
			if !strings.HasPrefix(n.Bridge, "lease") {
				t.Fatalf("%s bridge %s lacks the lease prefix (INCUS-R-127)", key, n.Bridge)
			}
			state.Leases[key] = &lnetLease{Consumer: resource.Consumer, Resource: resource.ID, Project: recorded.Actual.Sandbox,
				Bridge: n.Bridge, IPv4Subnet: n.IPv4Subnet, IPv4Gateway: n.IPv4Gateway, IPv6Subnet: n.IPv6Subnet, IPv6Gateway: n.IPv6Gateway,
				Egress: network.Egress}
			for _, slot := range n.Slots {
				state.Instances[slot.Instance] = &lnetInstance{Name: slot.Instance, Lease: key, Project: recorded.Actual.Sandbox, IPv4: slot.IPv4, IPv6: slot.IPv6, Slot: slot.Name}
			}
		}
		if len(state.Ports) != 3 {
			t.Fatalf("expected three frozen port bindings, have %v", state.Ports)
		}
		if state.Leases["lnlan.workers"].IPv6Subnet == "" || state.Instances["anas-lnlan-a"] == nil || state.Instances["anas-lnlan-a"].IPv6 == "" {
			t.Fatal("IPv6 leases and slot addresses are required for the IPv6 binding checks")
		}
	})

	lnetStep(t, "instances_through_shared_client", func(t *testing.T) {
		plan := map[string][]string{
			"lninet.workers": {"anas-lninet-a", "anas-lninet-b"}, "lninet.side": {"anas-lnside-a"},
			"lnlan.workers": {"anas-lnlan-a", "anas-lnlan-b"}, "lnhost.workers": {"anas-lnhost-web"}, "lnmods.workers": {"anas-lnmods-a"},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		for key, names := range plan {
			lease := state.Leases[key]
			client, _, _ := lnetClient(t, state, lease.Consumer, lease.Resource)
			for _, name := range names {
				if err := client.Create(ctx, computeclient.InstanceSpec{ID: name, Image: state.Pin, WorkloadID: name, CPU: 1, MemoryMiB: 512, DiskGiB: 4}); err != nil {
					t.Fatalf("create %s: %v", name, err)
				}
				if err := client.Start(ctx, name); err != nil {
					t.Fatalf("start %s: %v", name, err)
				}
				if state.Instances[name] == nil {
					state.Instances[name] = &lnetInstance{Name: name, Lease: key, Project: lease.Project}
				}
			}
		}
		deadline := time.Now().Add(3 * time.Minute)
		for _, instance := range state.Instances {
			lease := state.Leases[instance.Lease]
			for {
				status, v4, v6 := lnetInstanceAddresses(t, instance.Project, instance.Name, instance.IPv6)
				ready := status == "Running" && v4 != "" && (lease.IPv6Subnet == "" || v6 != "")
				if ready {
					if instance.Slot != "" && (v4 != instance.IPv4 || v6 != instance.IPv6) {
						// INCUS-R-155, R-156: the slot's fixed addresses, not DHCP's choice.
						t.Fatalf("slot instance %s has %s/%s, reserved %s/%s", instance.Name, v4, v6, instance.IPv4, instance.IPv6)
					}
					instance.IPv4, instance.IPv6 = v4, v6
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("instance %s did not get its DHCP addresses (INCUS-R-122): %s %s %s", instance.Name, status, v4, v6)
				}
				time.Sleep(2 * time.Second)
			}
			subnet := netip.MustParsePrefix(lease.IPv4Subnet)
			if !subnet.Contains(netip.MustParseAddr(instance.IPv4)) {
				t.Fatalf("instance %s address outside its lease subnet", instance.Name)
			}
		}
		for _, instance := range state.Instances {
			lnetGuestService(t, instance)
		}
		for _, instance := range state.Instances {
			lnetWaitListening(t, state.guest(instance.Name), []lnetProbe{lnetTCP("127.0.0.1", 7023)}, instance.Name)
		}
	})

	lnetStep(t, "traefik_address_set_follows_container", func(t *testing.T) {
		lnetWaitTraefikSet(t, state)
		if lnetHostdInvocations(t, "incus.traefik.sync") == 0 {
			t.Fatal("no hostd Traefik sync invocation recorded")
		}
	})

	lnetStep(t, "port_bindings_applied_by_hostd", func(t *testing.T) {
		lnetWaitPorts(t, state.Ports)
	})

	lnetStep(t, "http_publication_through_traefik", func(t *testing.T) {
		before := lnetTakeSnapshot(t)
		lnetPublish(t, state, "anas-lnlan-a", 8080, "a")
		after := lnetTakeSnapshot(t)
		lnetSameSnapshot(t, "http-publication", before, after)
		state.Snapshots["deployed"] = after
	})
	lnetSave(t, state)
}

// lnetGuestImage turns the delivered Debian container image (metadata and
// rootfs tarballs from images.linuxcontainers.org) into the split squashfs
// form Core supplies, and places it in the Provider's artifact tree.
func lnetGuestImage(t *testing.T, provider string) computeimage.Entry {
	t.Helper()
	dir := filepath.Join(coreNativeRoot, "guest-image")
	rootfs := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(rootfs, 0755); err != nil {
		t.Fatal(err)
	}
	lnetRun(t, "guest-rootfs", 10*time.Minute, nil, "/usr/bin/tar", "--numeric-owner", "-xJf", coreNativeInputs+"/guest-root.tar.xz", "-C", rootfs)
	squash := filepath.Join(dir, "rootfs.squashfs")
	lnetRun(t, "guest-squashfs", 15*time.Minute, nil, "/usr/bin/mksquashfs", rootfs, squash, "-noappend", "-no-progress")
	metadata, err := os.ReadFile(coreNativeInputs + "/guest-incus.tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, err := os.ReadFile(squash)
	if err != nil {
		t.Fatal(err)
	}
	entry := computeimage.Entry{Catalog: "anas", Name: "native-core", Revision: "lab-r1",
		Target:       computeimage.Target{Architecture: "amd64", Interface: "incus_container"},
		Fingerprint:  sha256Hex(append(append([]byte{}, metadata...), rootBytes...)),
		RecipeDigest: sha256Hex([]byte("images.linuxcontainers.org debian/trixie default, repacked as squashfs for the lease network fixture"))}
	writeProviderArtifactFixture(t, provider, entry, metadata, rootBytes)
	return entry
}

// lnetPublish submits an HTTP publication through the shared client's
// publisher and waits until Traefik serves it, as the host sees it.
func lnetPublish(t *testing.T, state *lnetState, instance string, port uint16, label string) string {
	t.Helper()
	lease := state.Leases[state.Instances[instance].Lease]
	client, l, env := lnetClient(t, state, lease.Consumer, lease.Resource)
	requestDir := computeHTTPRequestDir(stateDir(coreNativeWorkspace), lease.Consumer, lease.Resource)
	config, ok, err := computeclient.HTTPPublicationFromLookup(func(key string) string { return env[key] }, l, lease.Consumer, lease.Resource, requestDir)
	if err != nil || !ok {
		t.Fatalf("HTTP publication projection for %s: ok=%v %v", instance, ok, err)
	}
	publisher, err := client.OpenHTTPPublisher(config)
	if err != nil {
		t.Fatalf("open HTTP publisher: %v", err)
	}
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publication, err := publisher.PublishPort(ctx, instance, port, computeclient.PublishOptions{Label: label})
	if err != nil {
		t.Fatalf("publish %s:%d: %v", instance, port, err)
	}
	host := strings.TrimPrefix(publication.RequestedURL(), "https://")
	state.Hosts[instance+"/"+strconv.Itoa(int(port))] = host
	lnetWaitServed(t, host, instance, true, 60*time.Second)
	return host
}

// lnetWaitServed waits until Traefik on the host does (or does not) route host
// to the instance.
func lnetWaitServed(t *testing.T, host, instance string, served bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		results, err := lnetTryBatch(lnetHost(), []lnetProbe{{ID: "0", Kind: "https", Host: "127.0.0.1", Port: lnetTraefikPort, HostHeader: host, TimeoutMS: 4000}})
		got := err == nil && results["0"].OK && results["0"].Status == 200 && results["0"].Body["tag"] == instance
		if got == served {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Traefik route %s -> %s served=%v not reached within %s: %+v %v", host, instance, served, within, results, err)
		}
		time.Sleep(time.Second)
	}
}

// lnetWaitStatus waits until Traefik on the host answers host with status,
// from the instance (or the lnauth gate) named by tag; auth is sent as
// X-Lnet-Auth when set.
func lnetWaitStatus(t *testing.T, host, auth string, status int, tag string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		results, err := lnetTryBatch(lnetHost(), []lnetProbe{{ID: "0", Kind: "https", Host: "127.0.0.1", Port: lnetTraefikPort,
			HostHeader: host, Auth: auth, TimeoutMS: 4000}})
		if err == nil && results["0"].OK && results["0"].Status == status && results["0"].Body["tag"] == tag {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Traefik %s (auth %q) did not answer %d from %s within %s: %+v %v", host, auth, status, tag, within, results, err)
		}
		time.Sleep(time.Second)
	}
}

// lnetRouteHost reads the Host of a mediator route body.
func lnetRouteHost(t *testing.T, body []byte) string {
	t.Helper()
	match := regexp.MustCompile("Host\\(`([^`]+)`\\)").FindSubmatch(body)
	if match == nil {
		t.Fatal("route without a Host rule")
	}
	return string(match[1])
}

func lnetRouteFiles(t *testing.T, state *lnetState) []string {
	t.Helper()
	manifest, err := loadDeploymentManifest(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment))
	if err != nil {
		t.Fatal(err)
	}
	dynamic, ok := traefikDynamicDir(stateDir(coreNativeWorkspace), manifest)
	if !ok {
		t.Fatal("active deployment has no Traefik dynamic directory")
	}
	entries, err := os.ReadDir(filepath.Join(dynamic, computeingress.RouteDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if _, _, _, ok := computeingress.ParseRouteFileName(entry.Name()); ok {
			names = append(names, entry.Name())
		}
	}
	return names
}

// --- stage 2: reachability matrix ---------------------------------------------------------

func TestNativeLeaseNetworkMatrix(t *testing.T) {
	state := lnetLoad(t)
	lnetStep(t, "static_forwarding_rules_after_docker", func(t *testing.T) {
		for _, tool := range []string{"/usr/sbin/iptables", "/usr/sbin/ip6tables"} {
			listing := string(lnetRun(t, "forward-"+filepath.Base(tool), 20*time.Second, nil, tool, "-w", "5", "-S", "FORWARD"))
			if !lnetForwardOrdered(listing, tool == "/usr/sbin/iptables") {
				lnetWrite(t, filepath.Join(coreNativeReports, "reports", "forward-"+filepath.Base(tool)+".txt"), []byte(listing), 0600)
				t.Fatalf("%s FORWARD lacks the six ordered lease rules after Docker's (INCUS-R-126)", tool)
			}
		}
	})
	lnetStep(t, "reachability_matrix", func(t *testing.T) {
		m := lnetDeployedMatrix(state)
		m.run(t, "matrix-deployed")
	})
	lnetStep(t, "port_binding_holds_and_host_addresses", func(t *testing.T) {
		lnetPortHostChecks(t, state)
	})
	lnetSave(t, state)
}

// lnetForwardOrdered requires the six lease rules once each, in order, after
// every Docker FORWARD rule. requireDocker is false for ip6tables under a
// Docker that leaves IPv6 to its userland proxy (docker.io 26 by default).
func lnetForwardOrdered(listing string, requireDocker bool) bool {
	lastDocker, previous := -1, -1
	lines := strings.Split(listing, "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == "-A" && fields[1] == "FORWARD" && fields[len(fields)-2] == "-j" && strings.HasPrefix(fields[len(fields)-1], "DOCKER") {
			lastDocker = i
		}
	}
	previous = lastDocker
	for n := 1; n <= 6; n++ {
		found := -1
		for i, line := range lines {
			if strings.Contains(line, "anas-lease-forward-"+strconv.Itoa(n)) {
				if found >= 0 {
					return false
				}
				found = i
			}
		}
		if found <= previous {
			return false
		}
		previous = found
	}
	return lastDocker >= 0 || !requireDocker
}

func lnetForwardPolicy(listing string) string {
	for _, line := range strings.Split(listing, "\n") {
		if strings.HasPrefix(line, "-P FORWARD ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// lnetDeployedMatrix is every reachability question the deployed policy
// answers. Addresses: the LAN client and internet host sit behind the host's
// LAN and internet veths; the Module stand-in is a Docker container that also
// publishes a port on the host; every lease has its own bridge gateway.
func lnetDeployedMatrix(state *lnetState) *lnetMatrix {
	m := &lnetMatrix{}
	g := func(name string) lnetPlace { return state.guest(name) }
	ip := func(name string) string { return state.Instances[name].IPv4 }
	ip6 := func(name string) string { return state.Instances[name].IPv6 }
	gw := func(lease string) string { return state.Leases[lease].IPv4Gateway }
	lnhostTCP, lnhostUDP := state.Ports["lnhost.workers/tcp/7022"], state.Ports["lnhost.workers/udp/7053"]
	lnlanHost := state.Hosts["anas-lnlan-a/8080"]
	hostAddrs := func(lease string) []string { return []string{lnetHostLAN4, state.HostUplink, gw(lease)} }

	// INCUS-R-113: internet tier.
	for _, guest := range []string{"anas-lninet-a", "anas-lnside-a"} {
		lease := state.Instances[guest].Lease
		m.want("INCUS-R-113", g(guest), lnetTCP(lnetWAN4, 80), "wan", lnetHostWAN4)
		m.want("INCUS-R-113", g(guest), lnetUDP(lnetWAN4, 5353), "wan", "")
		m.want("INCUS-R-113 INCUS-R-044", g(guest), lnetTCP(lnetWAN6, 80), "wan", lnetHostWAN6)
		m.deny("INCUS-R-113", g(guest), lnetTCP(lnetLANClient4, 80))
		m.deny("INCUS-R-113", g(guest), lnetTCP(lnetLANClient6, 80))
		m.deny("INCUS-R-113 INCUS-R-120", g(guest), lnetDNS("10.0.2.3", "example.com"))
		for _, address := range hostAddrs(lease) {
			m.deny("INCUS-R-113", g(guest), lnetTCP(address, lnetHostPort))
		}
		m.deny("INCUS-R-113", g(guest), lnetTCP(lnetHostLAN4, lnetModulePublished))
		m.deny("INCUS-R-113", g(guest), lnetTCP(lnetModuleIP, 9001))
		m.deny("INCUS-R-113", g(guest), lnetTCP(lnetLinkLocal, 80))
		m.deny("INCUS-R-117", g(guest), lnetConnect(gw(lease), lnetTraefikPort))
		m.deny("INCUS-R-117", g(guest), lnetConnect(lnetHostLAN4, lnetTraefikPort))
		m.deny("INCUS-R-137", g(guest), lnetWeb(gw(lease), lnetTraefikPort, lnlanHost))
		m.want("INCUS-R-122", g(guest), lnetDNS(gw(lease), "example.com"), "", "")
	}
	// INCUS-R-123: intra_lease off in lninet, on in lnlan; port isolation
	// does not touch egress or DNS.
	m.deny("INCUS-R-123", g("anas-lninet-a"), lnetTCP(ip("anas-lninet-b"), 7022))
	m.deny("INCUS-R-123", g("anas-lninet-b"), lnetTCP(ip("anas-lninet-a"), 7022))
	m.deny("INCUS-R-123", g("anas-lninet-a"), lnetTCP(ip6("anas-lninet-b"), 7022))
	m.want("INCUS-R-113", g("anas-lninet-b"), lnetTCP(lnetWAN4, 80), "wan", lnetHostWAN4)
	m.want("INCUS-R-123", g("anas-lnlan-a"), lnetTCP(ip("anas-lnlan-b"), 7022), "anas-lnlan-b", ip("anas-lnlan-a"))
	m.want("INCUS-R-123", g("anas-lnlan-b"), lnetTCP(ip("anas-lnlan-a"), 7022), "anas-lnlan-a", ip("anas-lnlan-b"))

	// INCUS-R-114: internet_lan tier.
	for _, guest := range []string{"anas-lnlan-a", "anas-lnlan-b"} {
		m.want("INCUS-R-114", g(guest), lnetTCP(lnetWAN4, 80), "wan", lnetHostWAN4)
		m.want("INCUS-R-114", g(guest), lnetTCP(lnetLANClient4, 80), "lan", lnetHostLAN4)
		m.want("INCUS-R-114", g(guest), lnetTCP(lnetLANClient6, 80), "lan", lnetHostLAN6)
		m.want("INCUS-R-114 INCUS-R-120", g(guest), lnetDNS("10.0.2.3", "example.com"), "", "")
		for _, address := range hostAddrs("lnlan.workers") {
			m.deny("INCUS-R-114", g(guest), lnetTCP(address, lnetHostPort))
		}
		m.deny("INCUS-R-114", g(guest), lnetTCP(lnetHostLAN4, lnetModulePublished))
		m.deny("INCUS-R-114", g(guest), lnetTCP(lnetModuleIP, 9001))
		m.deny("INCUS-R-114", g(guest), lnetTCP(lnetLinkLocal, 80))
		// INCUS-R-117: module_access on.
		m.want("INCUS-R-117", g(guest), lnetConnect(gw("lnlan.workers"), lnetTraefikPort), "", "")
		m.want("INCUS-R-122", g(guest), lnetDNS(gw("lnlan.workers"), "example.com"), "", "")
	}
	// INCUS-R-137: a lease with module_access reaches another lease's HTTP
	// publication only through Traefik.
	m.want("INCUS-R-137", g("anas-lnlan-b"), lnetWeb(gw("lnlan.workers"), lnetTraefikPort, lnlanHost), "anas-lnlan-a", gw("lnlan.workers"))

	// INCUS-R-115: internet_lan_host tier.
	web1 := "anas-lnhost-web"
	m.want("INCUS-R-115", g(web1), lnetTCP(lnetWAN4, 80), "wan", lnetHostWAN4)
	m.want("INCUS-R-115", g(web1), lnetTCP(lnetLANClient4, 80), "lan", "")
	for _, address := range hostAddrs("lnhost.workers") {
		m.want("INCUS-R-115", g(web1), lnetTCP(address, lnetHostPort), "host", "")
	}
	m.want("INCUS-R-115", g(web1), lnetUDP(lnetHostLAN4, lnetHostPort), "host", "")
	m.want("INCUS-R-115", g(web1), lnetTCP(lnetHostLAN4, lnetModulePublished), "module", "")
	m.want("INCUS-R-115", g(web1), lnetUDP(lnetHostLAN4, lnetModulePublished), "module", "")
	m.deny("INCUS-R-115 INCUS-R-126", g(web1), lnetTCP(lnetModuleIP, 9002))
	m.record("INCUS-R-115", g(web1), lnetTCP(lnetModuleIP, 9001)) // depends on the Docker version (2026-09-30 probe)
	m.deny("INCUS-R-115", g(web1), lnetTCP(lnetLinkLocal, 80))
	m.want("INCUS-R-122", g(web1), lnetDNS(gw("lnhost.workers"), "example.com"), "", "")

	// INCUS-R-116: modules_only reaches Traefik and nothing else.
	mods := "anas-lnmods-a"
	m.want("INCUS-R-116", g(mods), lnetConnect(gw("lnmods.workers"), lnetTraefikPort), "", "")
	m.want("INCUS-R-116", g(mods), lnetConnect(lnetHostLAN4, lnetTraefikPort), "", "")
	m.want("INCUS-R-116 INCUS-R-137", g(mods), lnetWeb(gw("lnmods.workers"), lnetTraefikPort, lnlanHost), "anas-lnlan-a", gw("lnlan.workers"))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetWAN4, 80))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetWAN6, 80))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetLANClient4, 80))
	m.deny("INCUS-R-116", g(mods), lnetDNS("10.0.2.3", "example.com"))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetHostLAN4, lnetHostPort))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetHostLAN4, lnetModulePublished))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetModuleIP, 9001))
	m.deny("INCUS-R-116", g(mods), lnetTCP(lnetLinkLocal, 80))
	m.want("INCUS-R-122", g(mods), lnetDNS(gw("lnmods.workers"), "example.com"), "", "")

	// INCUS-R-136: no lease reaches another directly, whatever its tier, the
	// same consumer's other lease included, nor through a port binding.
	for _, pair := range [][2]string{
		{"anas-lninet-a", "anas-lnside-a"}, {"anas-lnside-a", "anas-lninet-a"}, {"anas-lninet-a", "anas-lnlan-a"},
		{"anas-lnlan-a", "anas-lninet-a"}, {"anas-lnhost-web", "anas-lnlan-a"}, {"anas-lnhost-web", "anas-lnlan-b"},
		{"anas-lnlan-a", "anas-lnhost-web"}, {"anas-lnmods-a", "anas-lnlan-a"},
		// lnhost allows the private ranges and was ensured before lnmods
		// existed, so only lnmods's own ACL refuses this one.
		{"anas-lnhost-web", "anas-lnmods-a"},
	} {
		m.deny("INCUS-R-136", g(pair[0]), lnetTCP(ip(pair[1]), 7022))
		m.deny("INCUS-R-136", g(pair[0]), lnetTCP(ip6(pair[1]), 7022))
	}
	m.deny("INCUS-R-136", g("anas-lnhost-web"), lnetTCP(lnetHostLAN4, lnetLANBinding))
	m.deny("INCUS-R-136", g("anas-lnlan-a"), lnetTCP(lnetHostLAN4, lnhostTCP))
	// Same lease through its own host port: the ACL decides traffic inside the
	// bridge too, so with intra_lease on it is reachable (2026-10-08; the
	// 2026-09-30 probe without intra_lease found it blocked).
	m.want("INCUS-R-123", g("anas-lnlan-b"), lnetTCP(lnetHostLAN4, lnetLANBinding), "anas-lnlan-a", "")

	// INCUS-R-130, R-133: inbound is refused unless published.
	lan, dock := lnetNS(lnetLANNS), lnetDock("lnmodule")
	for _, guest := range []string{"anas-lninet-a", "anas-lnside-a", "anas-lnmods-a", "anas-lnlan-b"} {
		m.deny("INCUS-R-130", lan, lnetTCP(ip(guest), 7022))
		m.deny("INCUS-R-130", lan, lnetUDP(ip(guest), 7053))
		m.deny("INCUS-R-130", dock, lnetTCP(ip(guest), 7022))
		m.deny("INCUS-R-130", lan, lnetTCP(ip6(guest), 7022))
	}
	m.deny("INCUS-R-130", lnetNS(lnetWANNS), lnetTCP(ip("anas-lninet-a"), 7022))
	m.want("INCUS-R-133", lan, lnetTCP(ip("anas-lnlan-a"), 7022), "anas-lnlan-a", lnetLANClient4)
	m.deny("INCUS-R-133", lan, lnetTCP(ip("anas-lnlan-a"), 7023))
	m.deny("INCUS-R-133", lan, lnetTCP(ip("anas-lnlan-a"), 8080))
	m.deny("INCUS-R-133", dock, lnetTCP(ip("anas-lnlan-a"), 8080))
	m.deny("INCUS-R-133", lan, lnetUDP(ip("anas-lnhost-web"), 7022))
	m.want("INCUS-R-133", lan, lnetUDP(ip("anas-lnhost-web"), 7053), "anas-lnhost-web", lnetLANClient4)
	m.deny("INCUS-R-133", lan, lnetTCP(ip("anas-lnhost-web"), 8080))

	// INCUS-R-150, R-151, R-157, R-138: port bindings on every host address.
	m.want("INCUS-R-150 INCUS-R-138 INCUS-R-152", lan, lnetTCP(lnetHostLAN4, lnetLANBinding), "anas-lnlan-a", lnetLANClient4)
	m.want("INCUS-R-150 INCUS-R-138", lan, lnetTCP(lnetHostLAN4, lnhostTCP), "anas-lnhost-web", lnetLANClient4)
	m.want("INCUS-R-150 INCUS-R-138", lan, lnetUDP(lnetHostLAN4, lnhostUDP), "anas-lnhost-web", lnetLANClient4)
	m.want("INCUS-R-157", lan, lnetTCP(lnetHostLAN6, lnhostTCP), "anas-lnhost-web", lnetLANClient6)
	m.want("INCUS-R-157", lan, lnetUDP(lnetHostLAN6, lnhostUDP), "anas-lnhost-web", lnetLANClient6)
	m.want("INCUS-R-157", lan, lnetTCP(lnetHostLAN6, lnetLANBinding), "anas-lnlan-a", lnetLANClient6)
	m.want("INCUS-R-151", lan, lnetTCP(state.HostUplink, lnetLANBinding), "anas-lnlan-a", lnetLANClient4)
	m.want("INCUS-R-151", lan, lnetTCP(lnetHostWAN4, lnetLANBinding), "anas-lnlan-a", lnetLANClient4)
	m.want("INCUS-R-151", lnetNS(lnetWANNS), lnetTCP(lnetHostWAN4, lnhostTCP), "anas-lnhost-web", lnetWAN4)
	m.want("INCUS-R-151", lnetHost(), lnetTCP(lnetHostLAN4, lnetLANBinding), "anas-lnlan-a", "")
	m.deny("INCUS-R-151", lnetHost(), lnetTCP("127.0.0.1", lnetLANBinding)) // the hold answers nothing
	m.want("INCUS-R-151", lnetHost(), lnetTCP(lnetWAN4, lnetLANBinding), "wan", lnetHostWAN4)
	m.want("INCUS-R-151", g("anas-lnlan-a"), lnetTCP(lnetWAN4, lnetLANBinding), "wan", lnetHostWAN4)
	m.want("INCUS-R-151", dock, lnetTCP("192.168.77.1", lnetLANBinding), "anas-lnlan-a", "")
	m.deny("INCUS-R-133", lan, lnetTCP(lnetHostLAN4, 31999)) // nothing bound there

	// INCUS-R-133, R-163: Traefik reaches the HTTP publication; the guest sees
	// the lease gateway and the client in X-Forwarded-For.
	m.want("INCUS-R-133 INCUS-R-163", lnetHost(), lnetWeb("127.0.0.1", lnetTraefikPort, lnlanHost), "anas-lnlan-a", gw("lnlan.workers"))
	m.want("INCUS-R-133", lan, lnetWeb(lnetHostLAN4, lnetTraefikPort, lnlanHost), "anas-lnlan-a", gw("lnlan.workers"))
	// INCUS-R-053: the internet reaches it through Traefik's public HTTPS entry
	// on both families; the guest backend stays IPv4.
	wan := lnetNS(lnetWANNS)
	m.want("INCUS-R-053", wan, lnetWeb(lnetHostWAN4, lnetTraefikPort, lnlanHost), "anas-lnlan-a", gw("lnlan.workers"))
	m.want("INCUS-R-053", wan, lnetWeb(lnetHostWAN6, lnetTraefikPort, lnlanHost), "anas-lnlan-a", gw("lnlan.workers"))
	return m
}

// lnetPortHostChecks covers what a probe matrix cannot: the holds against
// host processes and Docker (INCUS-R-160), and an address the host gains
// after the bindings were applied (INCUS-R-151).
func lnetPortHostChecks(t *testing.T, state *lnetState) {
	t.Helper()
	lnhostUDP := state.Ports["lnhost.workers/udp/7053"]
	results := lnetBatch(t, lnetHost(), []lnetProbe{
		{ID: "tcp", Kind: "bind", Name: "tcp", Port: lnetLANBinding},
		{ID: "udp", Kind: "bind", Name: "udp", Port: lnhostUDP},
	})
	if results["tcp"].OK || results["udp"].OK || results["tcp"].Error != "in_use" || results["udp"].Error != "in_use" {
		t.Fatalf("a host process could bind a held port (INCUS-R-160): %+v", results)
	}
	if out, err := lnetTry("docker-publish-held", 60*time.Second, nil, "/usr/bin/docker", "run", "-d", "--name", "lnet-held",
		"--label", "dev.anas.native-core="+state.ID, "-p", strconv.Itoa(lnetLANBinding)+":9001", "--entrypoint", "/agent", state.AgentImage, "idle"); err == nil {
		t.Fatalf("Docker published a held port (INCUS-R-160): %s", out)
	}
	_, _ = lnetTry("docker-publish-held-rm", 30*time.Second, nil, "/usr/bin/docker", "rm", "-f", "lnet-held")
	// Traffic to the hold itself must not wear it out: 300 datagrams and 300
	// connections through loopback, which the port table never rewrites.
	var spam []lnetProbe
	for i := 0; i < 300; i++ {
		spam = append(spam, lnetProbe{ID: "u" + strconv.Itoa(i), Kind: "udp", Host: "127.0.0.1", Port: lnhostUDP, TimeoutMS: 300},
			lnetProbe{ID: "t" + strconv.Itoa(i), Kind: "tcp", Host: "127.0.0.1", Port: lnetLANBinding, TimeoutMS: 300})
	}
	lnetBatch(t, lnetHost(), spam)
	if problem := lnetPortsProblem(t, state.Ports); problem != "" {
		t.Fatalf("holds did not survive traffic (INCUS-R-160): %s", problem)
	}
	results = lnetBatch(t, lnetHost(), []lnetProbe{{ID: "udp", Kind: "bind", Name: "udp", Port: lnhostUDP}})
	if results["udp"].OK {
		t.Fatal("UDP hold released after traffic (INCUS-R-160)")
	}
	// INCUS-R-151: a host address added after the bindings were applied.
	lnetRun(t, "late-address", 10*time.Second, nil, "/usr/sbin/ip", "addr", "add", lnetLateAddress+"/24", "dev", "lnlan0")
	defer lnetTry("late-address-del", 10*time.Second, nil, "/usr/sbin/ip", "addr", "del", lnetLateAddress+"/24", "dev", "lnlan0")
	results = lnetBatch(t, lnetNS(lnetLANNS), []lnetProbe{{ID: "late", Kind: "tcp", Host: lnetLateAddress, Port: lnetLANBinding}})
	if !results["late"].OK || results["late"].Tag != "anas-lnlan-a" {
		t.Fatalf("a binding did not cover a host address added later (INCUS-R-151): %+v", results["late"])
	}
}

// --- stage 3: runtime changes -------------------------------------------------------------

func lnetSystemctl(t *testing.T, args ...string) {
	t.Helper()
	lnetRun(t, "systemctl-"+strings.Join(args, "-"), 90*time.Second, nil, append([]string{"/usr/bin/systemctl"}, args...)...)
}

// lnetDockerEvent starts and ends one short container, which anasd sees as a
// container start and answers with a port binding check (INCUS-R-162).
func lnetDockerEvent(t *testing.T, state *lnetState) {
	t.Helper()
	lnetRun(t, "docker-event", 60*time.Second, []byte("[]"), "/usr/bin/docker", "run", "--rm", "-i",
		"--label", "dev.anas.native-core="+state.ID, "--entrypoint", "/agent", state.AgentImage, "batch")
}

// lnetWaitActivationSync waits until an incus.ports.sync that started after
// the workspace's latest activation has finished.
func lnetWaitActivationSync(t *testing.T) {
	t.Helper()
	info, err := os.Stat(activeStatePath(stateDir(coreNativeWorkspace)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		entries, err := os.ReadDir("/var/lib/anas-hostd/invocations")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			body, err := os.ReadFile(filepath.Join("/var/lib/anas-hostd/invocations", entry.Name()))
			var record struct {
				Action     string    `json:"action"`
				StartedAt  time.Time `json:"started_at"`
				FinishedAt time.Time `json:"finished_at"`
			}
			if err == nil && json.Unmarshal(body, &record) == nil && record.Action == "incus.ports.sync" &&
				record.StartedAt.After(info.ModTime()) && !record.FinishedAt.IsZero() {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("anasd queued no port sync after the latest activation (INCUS-R-152)")
		}
		time.Sleep(2 * time.Second)
	}
}

func lnetWaitInvocations(t *testing.T, action string, more int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for lnetHostdInvocations(t, action) < more {
		if time.Now().After(deadline) {
			t.Fatalf("hostd recorded no new %s invocation", action)
		}
		time.Sleep(2 * time.Second)
	}
}

// lnetSubmit writes an HTTP publication request without waiting for it.
func lnetSubmit(t *testing.T, state *lnetState, instance string, port uint16, label string) string {
	t.Helper()
	lease := state.Leases[state.Instances[instance].Lease]
	client, l, env := lnetClient(t, state, lease.Consumer, lease.Resource)
	requestDir := computeHTTPRequestDir(stateDir(coreNativeWorkspace), lease.Consumer, lease.Resource)
	config, ok, err := computeclient.HTTPPublicationFromLookup(func(key string) string { return env[key] }, l, lease.Consumer, lease.Resource, requestDir)
	if err != nil || !ok {
		t.Fatal("HTTP publication projection missing")
	}
	publisher, err := client.OpenHTTPPublisher(config)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publication, err := publisher.PublishPort(ctx, instance, port, computeclient.PublishOptions{Label: label})
	if err != nil {
		t.Fatalf("publish %s:%d: %v", instance, port, err)
	}
	host := strings.TrimPrefix(publication.RequestedURL(), "https://")
	state.Hosts[instance+"/"+strconv.Itoa(int(port))] = host
	return host
}

// The rules in which ensure lists the other leases' subnets. A lease created
// later joins that list at this lease's next ensure, and the host's addresses
// gain each new bridge's gateway, so a lease's policy is compared without the
// first and with the second masked (INCUS-R-140); lnetOtherLeasesListed
// checks the list itself.
const (
	lnetOtherLeasesEgress  = "ANAS lease: other leases are never reachable"
	lnetOtherLeasesIngress = "ANAS lease ingress: never from a lease"
)

type lnetRawACL struct {
	Egress  []map[string]string `json:"egress"`
	Ingress []map[string]string `json:"ingress"`
}

func lnetReadACL(t *testing.T, bridge string) lnetRawACL {
	t.Helper()
	var acl lnetRawACL
	if coreNativeQuery(t, http.MethodGet, "/1.0/network-acls/"+bridge+"?project=default", &acl) != 200 {
		t.Fatalf("lease ACL %s missing", bridge)
	}
	return acl
}

func lnetACL(t *testing.T, bridge string) string {
	t.Helper()
	acl := lnetReadACL(t, bridge)
	acl.Egress = slices.DeleteFunc(acl.Egress, func(r map[string]string) bool { return r["description"] == lnetOtherLeasesEgress })
	acl.Ingress = slices.DeleteFunc(acl.Ingress, func(r map[string]string) bool { return r["description"] == lnetOtherLeasesIngress })
	for _, r := range acl.Egress {
		if strings.HasSuffix(r["description"], ": not the host") || strings.HasSuffix(r["description"], ": host addresses") {
			r["destination"] = "<host addresses>"
		}
	}
	body, _ := json.Marshal(acl)
	return string(body)
}

// lnetOtherLeasesListed requires each lease's drop rules to name only other
// lease bridges on the daemon, in the lease's own families, never its own,
// and, for a lease ensured since the last lease was created, all of them
// (INCUS-R-136). Core ensures only the consumers an apply activates, so an
// unchanged lease may still miss a newer one; the newer one refuses it. A
// modules_only lease without published ingress writes no such rule.
func lnetOtherLeasesListed(t *testing.T, state *lnetState, complete bool, keys ...string) {
	t.Helper()
	var networks []struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	if coreNativeQuery(t, http.MethodGet, "/1.0/networks?project=default&recursion=1", &networks) != 200 {
		t.Fatal("daemon networks unreadable")
	}
	for _, key := range keys {
		lease := state.Leases[key]
		var want []string
		for _, n := range networks {
			if n.Name == lease.Bridge || n.Config["user.anas.consumer"] == "" || n.Config["user.anas.sandbox"] == "" {
				continue
			}
			for _, family := range []string{"ipv4", "ipv6"} {
				if family == "ipv6" && lease.IPv6Subnet == "" {
					continue
				}
				if prefix, err := netip.ParsePrefix(n.Config[family+".address"]); err == nil {
					want = append(want, prefix.Masked().String())
				}
			}
		}
		acl := lnetReadACL(t, lease.Bridge)
		var egress, ingress []string
		for _, r := range acl.Egress {
			if r["description"] == lnetOtherLeasesEgress {
				egress = lnetSubjects(r["destination"])
			}
		}
		for _, r := range acl.Ingress {
			if r["description"] == lnetOtherLeasesIngress {
				ingress = lnetSubjects(r["source"])
			}
		}
		slices.Sort(want)
		if lease.Egress == "modules_only" && egress == nil && ingress == nil {
			continue
		}
		matches := func(listed []string) bool {
			if complete {
				return slices.Equal(listed, want)
			}
			for _, subnet := range listed {
				if !slices.Contains(want, subnet) {
					return false
				}
			}
			return true
		}
		if lease.Egress != "modules_only" && !matches(egress) || ingress != nil && !matches(ingress) {
			t.Fatalf("lease %s lists other leases egress %v ingress %v, want %v (INCUS-R-136)", key, egress, ingress, want)
		}
	}
}

func lnetSubjects(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	slices.Sort(out)
	return out
}

func lnetACLs(t *testing.T, state *lnetState, keys ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, key := range keys {
		out[key] = lnetACL(t, state.Leases[key].Bridge)
	}
	return out
}

func lnetSameACLs(t *testing.T, label string, before, after map[string]string) {
	t.Helper()
	for key, acl := range before {
		if after[key] != acl {
			t.Fatalf("%s changed lease %s's network policy (INCUS-R-140)", label, key)
		}
	}
}

// lnetHold starts a long-lived connection from a guest to a hold server and
// waits until it answers heartbeats.
func lnetHold(t *testing.T, state *lnetState, guest string, port int) {
	t.Helper()
	instance := state.Instances[guest]
	lnetIncus(t, "hold-"+guest, nil, "exec", "--force-noninteractive", "--project", instance.Project, guest, "--",
		"systemd-run", "--unit=lnhold", "--collect", lnetGuestAgent, "hold-client", lnetWAN4, strconv.Itoa(port))
	deadline := time.Now().Add(30 * time.Second)
	for {
		status := lnetHoldStatus(t, port)
		if status.Pongs >= 3 && time.Since(time.UnixMilli(status.LastPongMilli)) < 2*time.Second {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hold connection from %s never established: %+v", guest, status)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

type lnetHoldState struct {
	Peer          string `json:"peer"`
	LastPongMilli int64  `json:"last_pong_unix_ms"`
	Pongs         int    `json:"pongs"`
	Closed        bool   `json:"closed"`
}

func lnetHoldStatus(t *testing.T, port int) lnetHoldState {
	t.Helper()
	var status lnetHoldState
	body, err := os.ReadFile(filepath.Join(lnetRunDir, "wan", "hold-"+strconv.Itoa(port)+".json"))
	if err == nil {
		_ = json.Unmarshal(body, &status)
	}
	return status
}

// lnetHoldEnded requires that the guest stopped answering: no heartbeat
// answered for longer than three seconds and none afterwards.
func lnetHoldEnded(t *testing.T, port int) {
	t.Helper()
	time.Sleep(4 * time.Second)
	first := lnetHoldStatus(t, port)
	time.Sleep(3 * time.Second)
	second := lnetHoldStatus(t, port)
	if second.Pongs != first.Pongs || time.Since(time.UnixMilli(second.LastPongMilli)) < 3*time.Second {
		t.Fatalf("an established lease connection still answers after revocation (INCUS-R-124): %+v", second)
	}
}

// lnetRevoked checks a removed lease: confirmed revocation, certificate gone,
// instances stopped, ACL empty, and a guest started again by the
// administrator still cannot reach anything (INCUS-R-124).
func lnetRevoked(t *testing.T, state *lnetState, key string, guests []string, oldEnv map[string]string) {
	t.Helper()
	lease := state.Leases[key]
	recorded := lnetResourceState(t, lease.Consumer, lease.Resource)
	if recorded.Revocation != "confirmed" || recorded.Status != "retained" {
		t.Fatalf("%s revocation not confirmed: status=%s revocation=%s", key, recorded.Status, recorded.Revocation)
	}
	fingerprint := lnetLeaseFingerprint(t, oldEnv, lease.Consumer, lease.Resource)
	if coreNativeQuery(t, http.MethodGet, "/1.0/certificates/"+fingerprint, nil) != 404 {
		t.Fatalf("%s certificate is still trusted", key)
	}
	var acl struct {
		Egress  []json.RawMessage `json:"egress"`
		Ingress []json.RawMessage `json:"ingress"`
	}
	coreNativeQuery(t, http.MethodGet, "/1.0/network-acls/"+lease.Bridge+"?project=default", &acl)
	if len(acl.Egress) != 0 || len(acl.Ingress) != 0 {
		t.Fatalf("%s ACL still carries rules after revocation", key)
	}
	for _, guest := range guests {
		if status, _, _ := lnetInstanceAddresses(t, lease.Project, guest); status != "Stopped" {
			t.Fatalf("%s still %s after revocation", guest, status)
		}
	}
	// The administrator may start a retained guest; the closed ACL still
	// refuses every new connection it opens.
	guest := guests[0]
	lnetIncus(t, "admin-start-"+guest, nil, "start", "--project", lease.Project, guest)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		status, v4, _ := lnetInstanceAddresses(t, lease.Project, guest)
		if status == "Running" && v4 != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not come back for the closed-policy check", guest)
		}
		time.Sleep(2 * time.Second)
	}
	m := &lnetMatrix{}
	m.deny("INCUS-R-124", state.guest(guest), lnetTCP(lnetWAN4, 80))
	m.deny("INCUS-R-124", state.guest(guest), lnetTCP(lnetWAN6, 80))
	m.deny("INCUS-R-124", state.guest(guest), lnetTCP(lnetLANClient4, 80))
	m.run(t, "revoked-"+strings.ReplaceAll(key, ".", "-"))
	lnetIncus(t, "admin-stop-"+guest, nil, "stop", "--force", "--project", lease.Project, guest)
	state.Revoked = append(state.Revoked, key)
}

func lnetLeaseFingerprint(t *testing.T, env map[string]string, consumer, resource string) string {
	t.Helper()
	lease, err := computeclient.LeaseFromLookup(func(key string) string { return env[key] }, consumer, resource)
	if err != nil {
		t.Fatal(err)
	}
	body, err := base64.StdEncoding.DecodeString(lease.ClientCertB64)
	block, _ := pem.Decode(body)
	if err != nil || block == nil {
		t.Fatal("lease client certificate unreadable")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:])
}

// lnetSurvivors re-asks the questions whose answer a change to one lease
// must not alter (INCUS-R-140).
func lnetSurvivors(state *lnetState, report string, t *testing.T) {
	t.Helper()
	m := &lnetMatrix{}
	lan := lnetNS(lnetLANNS)
	m.want("INCUS-R-140", lan, lnetTCP(lnetHostLAN4, lnetLANBinding), "anas-lnlan-a", lnetLANClient4)
	m.want("INCUS-R-140", lan, lnetTCP(lnetHostLAN4, state.Ports["lnhost.workers/tcp/7022"]), "anas-lnhost-web", lnetLANClient4)
	if state.SquatPort == 0 {
		m.want("INCUS-R-140", lan, lnetUDP(lnetHostLAN4, state.Ports["lnhost.workers/udp/7053"]), "anas-lnhost-web", lnetLANClient4)
	}
	m.want("INCUS-R-140", state.guest("anas-lnmods-a"), lnetConnect(state.Leases["lnmods.workers"].IPv4Gateway, lnetTraefikPort), "", "")
	m.deny("INCUS-R-140", state.guest("anas-lnmods-a"), lnetTCP(lnetWAN4, 80))
	m.want("INCUS-R-140", state.guest("anas-lnhost-web"), lnetTCP(lnetHostLAN4, lnetHostPort), "host", "")
	m.want("INCUS-R-140", state.guest("anas-lnlan-a"), lnetTCP(lnetLANClient4, 80), "lan", "")
	if !slices.Contains(state.Revoked, "lninet.workers") {
		m.want("INCUS-R-140", state.guest("anas-lninet-a"), lnetTCP(lnetWAN4, 80), "wan", "")
		m.deny("INCUS-R-140", state.guest("anas-lninet-a"), lnetTCP(lnetLANClient4, 80))
	}
	for key, host := range state.Hosts {
		instance := strings.Split(key, "/")[0]
		if strings.HasPrefix(instance, "anas-lnlan-") {
			m.want("INCUS-R-140", lnetHost(), lnetWeb("127.0.0.1", lnetTraefikPort, host), instance, "")
		}
	}
	m.run(t, report)
}

func TestNativeLeaseNetworkRuntime(t *testing.T) {
	state := lnetLoad(t)
	traefikNetwork := state.NetworkPrefix + "traefik"

	lnetStep(t, "traefik_address_change_and_anasd_start", func(t *testing.T) {
		old := lnetTraefikAddresses(t, state.Traefik)
		lnetRun(t, "traefik-stop", 60*time.Second, nil, "/usr/bin/docker", "stop", state.Traefik)
		lnetRun(t, "traefik-squat", 60*time.Second, nil, "/usr/bin/docker", "run", "-d", "--name", "lnet-squat",
			"--label", "dev.anas.native-core="+state.ID, "--network", traefikNetwork, "--entrypoint", "/agent", state.AgentImage, "idle")
		lnetRun(t, "traefik-start", 60*time.Second, nil, "/usr/bin/docker", "start", state.Traefik)
		current := lnetTraefikAddresses(t, state.Traefik)
		if slices.Equal(old, current) {
			t.Fatalf("Traefik kept %v; the address change this step needs did not happen", old)
		}
		lnetWaitTraefikSet(t, state)
		m := &lnetMatrix{}
		m.want("INCUS-R-118", state.guest("anas-lnmods-a"), lnetConnect(state.Leases["lnmods.workers"].IPv4Gateway, lnetTraefikPort), "", "")
		m.want("INCUS-R-118", lnetHost(), lnetWeb("127.0.0.1", lnetTraefikPort, state.Hosts["anas-lnlan-a/8080"]), "anas-lnlan-a", "")
		m.run(t, "traefik-moved")
		lnetRun(t, "traefik-squat-rm", 60*time.Second, nil, "/usr/bin/docker", "rm", "-f", "lnet-squat")
		// anasd triggers both syncs when it starts.
		traefik, ports := lnetHostdInvocations(t, "incus.traefik.sync"), lnetHostdInvocations(t, "incus.ports.sync")
		lnetSystemctl(t, "restart", "anasd.service")
		lnetWaitInvocations(t, "incus.traefik.sync", traefik+1)
		lnetWaitInvocations(t, "incus.ports.sync", ports+1)
		lnetWaitTraefikSet(t, state)
	})

	lnetStep(t, "late_docker_network_is_not_lan", func(t *testing.T) {
		lnetRun(t, "late-network", 30*time.Second, nil, "/usr/bin/docker", "network", "create", "--subnet", lnetLateSubnet,
			"--label", "dev.anas.native-core="+state.ID, lnetLateNet)
		lnetRun(t, "late-container", 60*time.Second, nil, "/usr/bin/docker", "run", "-d", "--name", "lnlate",
			"--label", "dev.anas.native-core="+state.ID, "--network", lnetLateNet, "--ip", lnetLateIP,
			"--entrypoint", "/agent", state.AgentImage, "serve", "--tag", "late", "--tcp", "9001")
		lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP(lnetLateIP, 9001)}, "late")
		m := &lnetMatrix{}
		m.deny("INCUS-R-120", state.guest("anas-lnlan-a"), lnetTCP(lnetLateIP, 9001))
		m.deny("INCUS-R-120", state.guest("anas-lninet-a"), lnetTCP(lnetLateIP, 9001))
		m.deny("INCUS-R-115 INCUS-R-126", state.guest("anas-lnhost-web"), lnetTCP(lnetLateIP, 9001))
		m.run(t, "late-docker-network")
		lnetRun(t, "late-container-rm", 60*time.Second, nil, "/usr/bin/docker", "rm", "-f", "lnlate")
		lnetRun(t, "late-network-rm", 30*time.Second, nil, "/usr/bin/docker", "network", "rm", lnetLateNet)
	})

	lnetStep(t, "mediator_down_routes_stay_then_reconcile", func(t *testing.T) {
		hostA := state.Hosts["anas-lnlan-a/8080"]
		lnetSystemctl(t, "stop", "anasd.service")
		lnetWaitServed(t, hostA, "anas-lnlan-a", true, 10*time.Second)
		hostB := lnetSubmit(t, state, "anas-lnlan-b", 8080, "b")
		time.Sleep(8 * time.Second)
		lnetWaitServed(t, hostB, "anas-lnlan-b", false, 5*time.Second)
		routes := lnetRouteFiles(t, state)
		if len(routes) != 1 {
			t.Fatalf("expected the one published route, found %v", routes)
		}
		manifest, _ := loadDeploymentManifest(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment))
		dynamic, _ := traefikDynamicDir(stateDir(coreNativeWorkspace), manifest)
		routeDir := filepath.Join(dynamic, computeingress.RouteDirectory)
		// While the mediator is down: lose a valid route and gain an orphan
		// that Traefik loads. The restarted mediator must undo both.
		orphanHost := "lnlan-orphan." + lnetBaseDomain
		orphan := "lnlan.workers." + strings.Repeat("0", 16) + "." + strings.Repeat("f", 16) + ".yml"
		body := "http:\n  routers:\n    lnet-orphan:\n      rule: \"Host(`" + orphanHost + "`)\"\n      service: lnet-orphan\n      entryPoints:\n        - \"https\"\n      tls: {}\n" +
			"  services:\n    lnet-orphan:\n      loadBalancer:\n        servers:\n          - url: \"http://" + state.Instances["anas-lnlan-b"].IPv4 + ":8080\"\n"
		lnetWrite(t, filepath.Join(routeDir, orphan), []byte(body), 0600)
		if err := os.Remove(filepath.Join(routeDir, routes[0])); err != nil {
			t.Fatal(err)
		}
		if err := touchRouteReload(dynamic); err != nil {
			t.Fatal(err)
		}
		lnetWaitServed(t, orphanHost, "anas-lnlan-b", true, 30*time.Second)
		lnetWaitServed(t, hostA, "anas-lnlan-a", false, 30*time.Second)
		lnetSystemctl(t, "start", "anasd.service")
		lnetWaitServed(t, hostA, "anas-lnlan-a", true, 30*time.Second)
		lnetWaitServed(t, hostB, "anas-lnlan-b", true, 30*time.Second)
		lnetWaitServed(t, orphanHost, "anas-lnlan-b", false, 30*time.Second)
		if routes := lnetRouteFiles(t, state); len(routes) != 2 || slices.Contains(routes, orphan) {
			t.Fatalf("restarted mediator left routes %v (INCUS-R-145)", routes)
		}
	})

	lnetStep(t, "request_files_cannot_widen_publication", func(t *testing.T) {
		// A compromised consumer writes its request directory directly, past
		// the shared client. The mediator in anasd checks every request
		// against the frozen authorization, and only its own output reaches
		// Traefik (INCUS-R-087, R-088, R-095).
		hostA, hostB := state.Hosts["anas-lnlan-a/8080"], state.Hosts["anas-lnlan-b/8080"]
		suffix := hostA[strings.Index(hostA, ".")+1:]
		before := lnetRouteFiles(t, state)
		dir := computeHTTPRequestDir(stateDir(coreNativeWorkspace), "lnlan", "workers")
		b := state.Instances["anas-lnlan-b"].IPv4
		request := func(instance, address string, port int, label, extra string) []byte {
			return []byte(fmt.Sprintf(`{"instance":%q,"address":%q,"port":%d,"label":%q%s}`, instance, address, port, label, extra))
		}
		bad := map[string][]byte{
			"x-port.json":    request("anas-lnlan-b", b, 9999, "xport", ""),
			"x-foreign.json": request("anas-lnlan-b", state.Instances["anas-lnhost-web"].IPv4, 8080, "xforeign", ""),
			"x-gateway.json": request("anas-lnlan-b", state.Leases["lnlan.workers"].IPv4Gateway, 8080, "xgateway", ""),
			"x-prefix.json":  request("anas-lnhost-web", b, 8080, "xprefix", ""),
			"x-label.json":   request("anas-lnlan-b", b, 8080, "xlabel.evil.example", ""),
			"x-fields.json": request("anas-lnlan-b", b, 8080, "xfields",
				`,"auth":"none","middlewares":["lnet@file"],"entryPoints":["lnet"],"domain":"evil.example","url":"http://`+lnetWAN4+`/"`),
			"x-route.yml": []byte("http:\n  routers:\n    xroute:\n      rule: \"Host(`xroute." + suffix + "`)\"\n      service: xroute\n" +
				"  services:\n    xroute:\n      loadBalancer:\n        servers:\n          - url: \"http://" + b + ":8080\"\n"),
		}
		// A valid request reached only through a symbolic or a second hard
		// link is not read.
		outside := filepath.Join(filepath.Dir(dir), "lnet-outside.json")
		lnetWrite(t, outside, request("anas-lnlan-b", b, 8080, "xlink", ""), 0600)
		names := []string{"x-link.json", "x-hard.json"}
		for name, body := range bad {
			lnetWrite(t, filepath.Join(dir, name), body, 0600)
			names = append(names, name)
		}
		if os.Symlink(outside, filepath.Join(dir, "x-link.json")) != nil || os.Link(outside, filepath.Join(dir, "x-hard.json")) != nil {
			t.Fatal("link fixtures")
		}
		defer func() {
			for _, name := range names {
				_ = os.Remove(filepath.Join(dir, name))
			}
			_ = os.Remove(outside)
		}()
		since := time.Now().Add(-time.Second)
		time.Sleep(12 * time.Second) // at least three mediator passes
		for _, host := range []string{"xport." + suffix, "xforeign." + suffix, "xgateway." + suffix, "xprefix." + suffix,
			"xlabel.evil.example", "xfields." + suffix, "evil.example", "xroute." + suffix, "xlink." + suffix} {
			lnetWaitServed(t, host, "anas-lnlan-b", false, 5*time.Second)
		}
		lnetWaitServed(t, hostA, "anas-lnlan-a", true, 5*time.Second)
		lnetWaitServed(t, hostB, "anas-lnlan-b", true, 5*time.Second)
		if after := lnetRouteFiles(t, state); !slices.Equal(after, before) {
			t.Fatalf("raw requests changed the published routes: %v -> %v", before, after)
		}
		journal := string(lnetRun(t, "anasd-journal", 20*time.Second, nil, "/usr/bin/journalctl", "-u", "anasd.service", "--no-pager",
			"-o", "cat", "--since", "@"+strconv.FormatInt(since.Unix(), 10)))
		for _, want := range []string{"x-port.json: port not declared", "x-foreign.json: address outside the lease subnet",
			"x-gateway.json: address outside the lease subnet", "x-prefix.json: instance outside the lease prefix"} {
			if !strings.Contains(journal, want) {
				t.Fatalf("the mediator did not report %q", want)
			}
		}
		// Authentication, middleware and entrypoint come from the frozen
		// declaration only: lnlan declared auth none.
		manifest, _ := loadDeploymentManifest(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment))
		dynamic, _ := traefikDynamicDir(stateDir(coreNativeWorkspace), manifest)
		routeDir := filepath.Join(dynamic, computeingress.RouteDirectory)
		for _, name := range before {
			body, err := os.ReadFile(filepath.Join(routeDir, name))
			if err != nil || bytes.Contains(body, []byte("middlewares")) || !bytes.Contains(body, []byte(`- "https"`)) {
				t.Fatalf("route %s does not carry only the declared entrypoint and authentication", name)
			}
		}
		// Only root writes the Traefik dynamic directory: a consumer's own
		// identity cannot add a route beside the mediator's.
		for _, target := range []string{dynamic, routeDir} {
			if out, err := lnetTry("intruder-"+filepath.Base(target), 10*time.Second, nil, "/usr/bin/setpriv", "--reuid=65532", "--regid=65532",
				"--clear-groups", "/usr/bin/touch", filepath.Join(target, "lnet-intruder.yml")); err == nil {
				t.Fatalf("an unprivileged consumer identity wrote %s: %s", target, out)
			}
		}
	})

	lnetStep(t, "explicit_port_conflicts_fail_apply", func(t *testing.T) {
		lnetRun(t, "squat-31500", 20*time.Second, nil, "/usr/bin/systemd-run", "--unit=lnet-squat31500", "--collect",
			lnetAgent, "serve", "--tag", "squat", "--tcp", strconv.Itoa(lnetConflictPort))
		lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP("127.0.0.1", lnetConflictPort)}, "squat")
		binding := func(port int) map[string]any {
			return map[string]any{"protocol": "tcp", "host_port": port, "slot": "web", "guest_port": 7023}
		}
		message := lnetRejectedApply(t, state, "host-process", lnetVariant{extraBindingFor: "lnhost", extraBinding: binding(lnetConflictPort)})
		if !strings.Contains(message, strconv.Itoa(lnetConflictPort)) {
			t.Fatalf("host process conflict rejected for another reason: %s", message)
		}
		key := runtimeissues.WorkspacePath(coreNativeWorkspace)
		if !lnetOpenIssue(lnetIssues(t, key), "incus.port-binding/lnhost.workers/tcp/"+strconv.Itoa(lnetConflictPort), runtimeissues.SourceApply) {
			t.Fatal("explicit port conflict was not recorded as a runtime issue (INCUS-R-154)")
		}
		message = lnetRejectedApply(t, state, "lease-conflict", lnetVariant{extraBindingFor: "lnhost", extraBinding: binding(lnetLANBinding)})
		if !strings.Contains(message, "INCUS-R-138") {
			t.Fatalf("binding another lease's port rejected for another reason: %s", message)
		}
		lnetRun(t, "docker-publish-31600", 60*time.Second, nil, "/usr/bin/docker", "run", "-d", "--name", "lnet-dockpub",
			"--label", "dev.anas.native-core="+state.ID, "-p", strconv.Itoa(lnetDockerConflictPort)+":9001",
			"--entrypoint", "/agent", state.AgentImage, "serve", "--tag", "dockpub", "--tcp", "9001")
		message = lnetRejectedApply(t, state, "docker-conflict", lnetVariant{extraBindingFor: "lnhost", extraBinding: binding(lnetDockerConflictPort)})
		if !strings.Contains(message, strconv.Itoa(lnetDockerConflictPort)) {
			t.Fatalf("Docker publication conflict rejected for another reason: %s", message)
		}
		lnetRun(t, "docker-publish-31600-rm", 60*time.Second, nil, "/usr/bin/docker", "rm", "-f", "lnet-dockpub")
		lnetSystemctl(t, "stop", "lnet-squat31500.service")
	})

	lnetStep(t, "auto_port_skips_taken_ports", func(t *testing.T) {
		// INCUS-R-154, R-153: with six of every seven ports in the approved
		// range taken by a host process, an auto binding must still land on
		// a free port, and the lease's earlier auto ports stay.
		ready := lnetRunDir + "/occupy.ready"
		_ = os.Remove(ready)
		lnetRun(t, "occupy", 20*time.Second, nil, "/usr/bin/systemd-run", "--unit=lnet-occupy", "--collect", lnetAgent, "occupy",
			strconv.Itoa(incusprovision.DefaultPortRangeFirst), strconv.Itoa(incusprovision.DefaultPortRangeLast), "7", ready)
		defer lnetTry("occupy-stop", 30*time.Second, nil, "/usr/bin/systemctl", "stop", "lnet-occupy.service")
		deadline := time.Now().Add(60 * time.Second)
		for {
			body, err := os.ReadFile(ready)
			if count, convErr := strconv.Atoi(strings.TrimSpace(string(body))); err == nil && convErr == nil {
				if count < 2000 {
					t.Fatalf("the occupant took only %d ports", count)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the port occupant did not start")
			}
			time.Sleep(500 * time.Millisecond)
		}
		lnetImport(t, state, "auto-skip", lnetVariant{extraBindingFor: "lnhost",
			extraBinding: map[string]any{"protocol": "tcp", "host_port": "auto", "slot": "web", "guest_port": 7023}})
		lnetRenderApply(t, state, "auto-skip")
		lnetSystemctl(t, "stop", "lnet-occupy.service")
		manifest, err := loadDeploymentManifest(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment))
		if err != nil {
			t.Fatal(err)
		}
		chosen := 0
		for _, resource := range manifest.Resources {
			if resource.Consumer+"."+resource.ID != "lnhost.workers" || resource.ComputeNetwork == nil {
				continue
			}
			for _, binding := range resource.ComputeNetwork.Ports {
				key := "lnhost.workers/" + binding.Protocol + "/" + strconv.Itoa(int(binding.GuestPort))
				switch {
				case binding.Protocol == "tcp" && binding.GuestPort == 7023:
					if !binding.Auto {
						t.Fatal("the extra binding was not frozen as auto")
					}
					chosen = int(binding.HostPort)
				case state.Ports[key] != int(binding.HostPort):
					t.Fatalf("auto binding %s moved from %d to %d", key, state.Ports[key], binding.HostPort)
				}
			}
		}
		if chosen%7 != 0 || chosen < incusprovision.DefaultPortRangeFirst || chosen > incusprovision.DefaultPortRangeLast {
			t.Fatalf("auto picked %d, a port the occupant held or outside the range", chosen)
		}
		want := maps.Clone(state.Ports)
		want["lnhost.workers/tcp/7023"] = chosen
		lnetWaitPorts(t, want)
		m := &lnetMatrix{}
		m.want("INCUS-R-154 INCUS-R-153", lnetNS(lnetLANNS), lnetTCP(lnetHostLAN4, chosen), "anas-lnhost-web", lnetLANClient4)
		m.run(t, "auto-skip")
		lnetImport(t, state, "auto-skip-undo", lnetVariant{})
		lnetRenderApply(t, state, "auto-skip-undo")
		lnetWaitPorts(t, state.Ports)
	})

	lnetStep(t, "forward_auth_frozen_at_apply", func(t *testing.T) {
		// INCUS-R-095, R-087: lnlan's deployment switches its HTTP
		// publication to forward_auth. The mediator renders every route with
		// the bound provider's middleware, and nothing a consumer writes at
		// run time takes it off again.
		hostA, hostB := state.Hosts["anas-lnlan-a/8080"], state.Hosts["anas-lnlan-b/8080"]
		suffix := hostA[strings.Index(hostA, ".")+1:]
		lnetImport(t, state, "forward-auth", lnetVariant{lnlanAuth: true})
		lnetRenderApply(t, state, "forward-auth")
		for _, host := range []string{hostA, hostB} {
			lnetWaitStatus(t, host, "", http.StatusUnauthorized, "lnauth", 90*time.Second)
		}
		lnetWaitStatus(t, hostA, "allow", http.StatusOK, "anas-lnlan-a", 10*time.Second)
		lnetWaitStatus(t, hostB, "allow", http.StatusOK, "anas-lnlan-b", 10*time.Second)
		routes := lnetRouteFiles(t, state)
		manifest, _ := loadDeploymentManifest(filepath.Join(stateDir(coreNativeWorkspace), "deployments", state.Deployment))
		dynamic, _ := traefikDynamicDir(stateDir(coreNativeWorkspace), manifest)
		routeDir := filepath.Join(dynamic, computeingress.RouteDirectory)
		if len(routes) != 2 {
			t.Fatalf("forward_auth routes %v", routes)
		}
		for _, name := range routes {
			body, err := os.ReadFile(filepath.Join(routeDir, name))
			if err != nil || !bytes.Contains(body, []byte("middlewares:\n        - \"lnet-auth@docker\"\n")) || !bytes.Contains(body, []byte(`- "https"`)) {
				t.Fatalf("route %s lacks the frozen forward_auth middleware", name)
			}
		}
		// A request that asks for no authentication, or names another
		// middleware, is not read; a plain request for a new label is
		// published behind the gate like the others.
		dir := computeHTTPRequestDir(stateDir(coreNativeWorkspace), "lnlan", "workers")
		b := state.Instances["anas-lnlan-b"].IPv4
		files := map[string]string{
			"x-noauth.json": fmt.Sprintf(`{"instance":"anas-lnlan-b","address":%q,"port":8080,"label":"xnoauth","auth":"none"}`, b),
			"x-mw.json":     fmt.Sprintf(`{"instance":"anas-lnlan-b","address":%q,"port":8080,"label":"xmw","middlewares":[]}`, b),
			"x-gated.json":  fmt.Sprintf(`{"instance":"anas-lnlan-b","address":%q,"port":8080,"label":"xgated"}`, b),
		}
		for name, body := range files {
			lnetWrite(t, filepath.Join(dir, name), []byte(body), 0600)
		}
		defer func() {
			for name := range files {
				_ = os.Remove(filepath.Join(dir, name))
			}
		}()
		var gated string
		deadline := time.Now().Add(30 * time.Second)
		for gated == "" {
			for _, name := range lnetRouteFiles(t, state) {
				if !slices.Contains(routes, name) {
					gated = name
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("the mediator did not publish a valid request under forward_auth")
			}
			time.Sleep(time.Second)
		}
		if routes := lnetRouteFiles(t, state); len(routes) != 3 {
			t.Fatalf("raw requests without authentication were published: %v", routes)
		}
		body, err := os.ReadFile(filepath.Join(routeDir, gated))
		if err != nil || !bytes.Contains(body, []byte(`"lnet-auth@docker"`)) {
			t.Fatal("a run-time request was published without the frozen middleware")
		}
		gatedHost := lnetRouteHost(t, body)
		if !strings.HasSuffix(gatedHost, "."+suffix) {
			t.Fatalf("gated route host %q outside the lease's domain", gatedHost)
		}
		lnetWaitStatus(t, gatedHost, "", http.StatusUnauthorized, "lnauth", 10*time.Second)
		lnetWaitStatus(t, gatedHost, "allow", http.StatusOK, "anas-lnlan-b", 10*time.Second)
		for name := range files {
			_ = os.Remove(filepath.Join(dir, name))
		}
		// Back to the declaration every other step expects.
		lnetImport(t, state, "forward-auth-undo", lnetVariant{})
		lnetRenderApply(t, state, "forward-auth-undo")
		lnetWaitServed(t, hostA, "anas-lnlan-a", true, 90*time.Second)
		lnetWaitServed(t, hostB, "anas-lnlan-b", true, 30*time.Second)
		lnetWaitServed(t, gatedHost, "anas-lnlan-b", false, 30*time.Second)
	})

	lnetStep(t, "binding_drift_recorded_by_anasd", func(t *testing.T) {
		// The sync anasd queues for the previous step's activation would
		// otherwise repair the drift introduced here before anasd checks.
		lnetWaitActivationSync(t)
		issues := runtimeissues.WorkspacePath(coreNativeWorkspace)
		tcpKey := "incus.port-binding/lnlan.workers/tcp/" + strconv.Itoa(lnetLANBinding)
		udpPort := state.Ports["lnhost.workers/udp/7053"]
		udpKey := "incus.port-binding/lnhost.workers/udp/" + strconv.Itoa(udpPort)
		lnetRun(t, "drift-map", 20*time.Second, nil, "/usr/sbin/nft", "delete", "element", "inet", incusprovision.PortTable, "tcp4",
			"{ "+strconv.Itoa(lnetLANBinding)+" }")
		lnetDockerEvent(t, state)
		lnetWaitIssue(t, issues, tcpKey, runtimeissues.SourceAnasd, true, 90*time.Second)
		lnetSystemctl(t, "stop", "anas-port-udp@"+strconv.Itoa(udpPort)+".service", "anas-port-udp@"+strconv.Itoa(udpPort)+".socket")
		lnetDockerEvent(t, state)
		lnetWaitIssue(t, issues, udpKey, runtimeissues.SourceAnasd, true, 90*time.Second)
		// anasd repairs nothing; its start triggers the sync that does.
		lnetSystemctl(t, "restart", "anasd.service")
		lnetWaitPorts(t, state.Ports)
		lnetDockerEvent(t, state)
		lnetWaitIssue(t, issues, tcpKey, runtimeissues.SourceAnasd, false, 90*time.Second)
		lnetWaitIssue(t, issues, udpKey, runtimeissues.SourceAnasd, false, 90*time.Second)
	})

	lnetStep(t, "admin_forward_rules_keep_precedence", func(t *testing.T) {
		// INCUS-R-105, R-126: an administrator's own FORWARD rule above the
		// ANAS rules keeps deciding, also after the boot unit re-applies
		// them, and Docker's FORWARD policy stays.
		rules := map[string][]string{
			"/usr/sbin/iptables":  {"-i", "lease+", "-d", lnetWAN4 + "/32", "-p", "tcp", "--dport", "80", "-m", "comment", "--comment", "lnet-admin", "-j", "DROP"},
			"/usr/sbin/ip6tables": {"-i", "lease+", "-d", lnetWAN6 + "/128", "-p", "tcp", "--dport", "80", "-m", "comment", "--comment", "lnet-admin", "-j", "DROP"},
		}
		policy := map[string]string{}
		for tool, rule := range rules {
			policy[tool] = lnetForwardPolicy(string(lnetRun(t, "forward-"+filepath.Base(tool), 20*time.Second, nil, tool, "-w", "5", "-S", "FORWARD")))
			lnetRun(t, "admin-insert-"+filepath.Base(tool), 20*time.Second, nil, append([]string{tool, "-w", "5", "-I", "FORWARD", "1"}, rule...)...)
			defer lnetTry("admin-delete-"+filepath.Base(tool), 20*time.Second, nil, append([]string{tool, "-w", "5", "-D", "FORWARD"}, rule...)...)
		}
		check := func(label string, admin bool) {
			m := &lnetMatrix{}
			guest := state.guest("anas-lninet-a")
			if admin {
				m.deny("INCUS-R-105 INCUS-R-126", guest, lnetTCP(lnetWAN4, 80))
				m.deny("INCUS-R-105 INCUS-R-126", guest, lnetTCP(lnetWAN6, 80))
			} else {
				m.want("INCUS-R-105", guest, lnetTCP(lnetWAN4, 80), "wan", "")
				m.want("INCUS-R-105", guest, lnetTCP(lnetWAN6, 80), "wan", "")
			}
			m.want("INCUS-R-105", guest, lnetTCP(lnetWAN4, lnetLANBinding), "wan", "")
			m.run(t, label)
		}
		check("admin-rule", true)
		lnetSystemctl(t, "restart", incusprovision.NetworkUnitName)
		for tool := range rules {
			listing := string(lnetRun(t, "forward-reapplied-"+filepath.Base(tool), 20*time.Second, nil, tool, "-w", "5", "-S", "FORWARD"))
			first := ""
			for _, line := range strings.Split(listing, "\n") {
				if strings.HasPrefix(line, "-A FORWARD ") {
					first = line
					break
				}
			}
			if !strings.Contains(first, "lnet-admin") || !lnetForwardOrdered(listing, tool == "/usr/sbin/iptables") ||
				lnetForwardPolicy(listing) != policy[tool] {
				lnetWrite(t, filepath.Join(coreNativeReports, "reports", "forward-admin-"+filepath.Base(tool)+".txt"), []byte(listing), 0600)
				t.Fatalf("%s: re-applying the ANAS rules moved the administrator's rule or Docker's policy", tool)
			}
		}
		check("admin-rule-after-reapply", true)
		for tool, rule := range rules {
			lnetRun(t, "admin-delete-now-"+filepath.Base(tool), 20*time.Second, nil, append([]string{tool, "-w", "5", "-D", "FORWARD"}, rule...)...)
		}
		check("admin-rule-removed", false)
	})

	lnetStep(t, "cli_activation_prunes_changed_routes", func(t *testing.T) {
		others := lnetACLs(t, state, "lninet.workers", "lnhost.workers", "lnmods.workers")
		oldRoutes := lnetRouteFiles(t, state)
		before := lnetTakeSnapshot(t)
		lnetSystemctl(t, "stop", "anasd.service")
		lnetImport(t, state, "authorization-change", lnetVariant{lnlanHTTP8081: true, sideLAN: true})
		lnetRenderApply(t, state, "authorization-change")
		if routes := lnetRouteFiles(t, state); len(routes) != 0 {
			t.Fatalf("activation kept routes of a changed authorization with the mediator down (INCUS-R-147): %v", routes)
		}
		lnetWaitServed(t, state.Hosts["anas-lnlan-a/8080"], "anas-lnlan-a", false, 30*time.Second)
		after := lnetTakeSnapshot(t)
		lnetSameSnapshot(t, "authorization-and-tier-change", before, after)
		lnetSameACLs(t, "an lnlan and lnside change", others, lnetACLs(t, state, "lninet.workers", "lnhost.workers", "lnmods.workers"))
		// This apply re-ensures the changed consumers' leases, which now also
		// list the leases created after them.
		lnetOtherLeasesListed(t, state, true, "lninet.workers", "lninet.side", "lnlan.workers")
		lnetOtherLeasesListed(t, state, false, "lnhost.workers", "lnmods.workers")
		m := &lnetMatrix{}
		m.want("INCUS-R-125 INCUS-R-114", state.guest("anas-lnside-a"), lnetTCP(lnetLANClient4, 80), "lan", "")
		m.deny("INCUS-R-114", state.guest("anas-lnside-a"), lnetTCP(lnetHostLAN4, lnetHostPort))
		m.run(t, "tier-change")
		lnetSystemctl(t, "start", "anasd.service")
		lnetWaitServed(t, state.Hosts["anas-lnlan-a/8080"], "anas-lnlan-a", true, 30*time.Second)
		lnetWaitServed(t, state.Hosts["anas-lnlan-b/8080"], "anas-lnlan-b", true, 30*time.Second)
		routes := lnetRouteFiles(t, state)
		if len(routes) != 2 || slices.Contains(routes, oldRoutes[0]) {
			t.Fatalf("mediator did not republish under the new authorization: %v", routes)
		}
		lnetWaitPorts(t, state.Ports)
		if lnetOpenIssue(lnetIssues(t, runtimeissues.WorkspacePath(coreNativeWorkspace)), "incus.port-binding/lnhost.workers/tcp/"+strconv.Itoa(lnetConflictPort), "") {
			t.Fatal("a successful apply left the earlier port conflict open (INCUS-R-154)")
		}
		lnetSurvivors(state, "survivors-authorization-change", t)
	})

	lnetStep(t, "cli_revocation_ends_connections", func(t *testing.T) {
		oldEnv := lnetLeaseEnv(t, state, "lninet")
		lnetHold(t, state, "anas-lnside-a", 7100)
		others := lnetACLs(t, state, "lninet.workers", "lnlan.workers", "lnhost.workers", "lnmods.workers")
		before := lnetTakeSnapshot(t)
		lnetImport(t, state, "remove-side", lnetVariant{lnlanHTTP8081: true, withoutSide: true})
		lnetRenderApply(t, state, "remove-side")
		lnetHoldEnded(t, 7100)
		lnetRevoked(t, state, "lninet.side", []string{"anas-lnside-a"}, oldEnv)
		lnetSameSnapshot(t, "cli-revocation", before, lnetTakeSnapshot(t))
		lnetSameACLs(t, "revoking lninet.side", others, lnetACLs(t, state, "lninet.workers", "lnlan.workers", "lnhost.workers", "lnmods.workers"))
		lnetSurvivors(state, "survivors-cli-revocation", t)
	})
	lnetSave(t, state)
}

// --- stage 4: console revocation --------------------------------------------------------------

// TestNativeLeaseNetworkBeforeConsole prepares the console revocation: a held
// connection from the lease about to be removed, and the workspace
// configuration without its module. The orchestrator then plans and applies
// through the console.
func TestNativeLeaseNetworkBeforeConsole(t *testing.T) {
	state := lnetLoad(t)
	lnetStep(t, "held_connection_and_removed_module_config", func(t *testing.T) {
		lnetHold(t, state, "anas-lninet-a", 7101)
		state.Snapshots["before-console"] = lnetTakeSnapshot(t)
		lnetWrite(t, filepath.Join(coreNativeReports, "private", "env-before-console.json"), lnetMarshal(lnetLeaseEnv(t, state, "lninet")), 0600)
		acls, _ := json.Marshal(lnetACLs(t, state, "lnhost.workers", "lnmods.workers"))
		lnetWrite(t, filepath.Join(coreNativeReports, "private", "acls-before-console.json"), acls, 0600)
		// The same console apply narrows lnlan's HTTP authorization back to
		// 8080, so its routes must be replaced (INCUS-R-147).
		state.ConsoleRoutes = lnetRouteFiles(t, state)
		lnetImport(t, state, "console-remove-inet", lnetVariant{withoutSide: true, withoutInet: true})
		// The console finds Module sources through the workspace's Module
		// view; the CLI steps passed this lab root with --root instead.
		if err := saveWorkspaceModuleView(coreNativeWorkspace, modulestore.View{APIVersion: "anas.module-view/v1", ModuleRoot: lnetModuleRoot()}); err != nil {
			t.Fatal(err)
		}
	})
	lnetSave(t, state)
}

func lnetMarshal(value any) []byte {
	body, _ := json.Marshal(value)
	return body
}

func TestNativeLeaseNetworkAfterConsole(t *testing.T) {
	state := lnetLoad(t)
	lnetStep(t, "console_revocation_ends_connections", func(t *testing.T) {
		active, err := loadActiveState(stateDir(coreNativeWorkspace))
		if err != nil || active.ActiveDeployment == state.Deployment {
			t.Fatal("the console apply did not activate a new deployment")
		}
		state.Deployment = active.ActiveDeployment
		var oldEnv map[string]string
		var acls map[string]string
		envBody, _ := os.ReadFile(filepath.Join(coreNativeReports, "private", "env-before-console.json"))
		aclBody, _ := os.ReadFile(filepath.Join(coreNativeReports, "private", "acls-before-console.json"))
		if json.Unmarshal(envBody, &oldEnv) != nil || json.Unmarshal(aclBody, &acls) != nil {
			t.Fatal("pre-console evidence unreadable")
		}
		lnetHoldEnded(t, 7101)
		lnetRevoked(t, state, "lninet.workers", []string{"anas-lninet-a", "anas-lninet-b"}, oldEnv)
		lnetSameSnapshot(t, "console-revocation", state.Snapshots["before-console"], lnetTakeSnapshot(t))
		lnetSameACLs(t, "revoking lninet.workers", acls, lnetACLs(t, state, "lnhost.workers", "lnmods.workers"))
		// INCUS-R-147: the console activation removed every route of lnlan's
		// changed authorization, and the ACL admits Traefik on 8080 only.
		lnetWaitServed(t, state.Hosts["anas-lnlan-a/8080"], "anas-lnlan-a", true, 60*time.Second)
		lnetWaitServed(t, state.Hosts["anas-lnlan-b/8080"], "anas-lnlan-b", true, 60*time.Second)
		routes := lnetRouteFiles(t, state)
		if len(routes) != 2 || slices.ContainsFunc(routes, func(r string) bool { return slices.Contains(state.ConsoleRoutes, r) }) {
			t.Fatalf("console activation kept routes of the changed authorization: %v (before %v)", routes, state.ConsoleRoutes)
		}
		traefikPorts := ""
		for _, r := range lnetReadACL(t, state.Leases["lnlan.workers"].Bridge).Ingress {
			if r["source"] == "$anas-traefik" {
				traefikPorts = r["destination_port"]
			}
		}
		if traefikPorts != "8080" {
			t.Fatalf("lnlan admits Traefik on %q after the console narrowed it to 8080", traefikPorts)
		}
		lnetWaitPorts(t, state.Ports)
		lnetSurvivors(state, "survivors-console-revocation", t)
	})
	lnetSave(t, state)
}

// --- stage 5: host restart ----------------------------------------------------------------------

// TestNativeLeaseNetworkBeforeReboot installs a unit that takes one bound
// port before the holds at the next boot (INCUS-R-161).
func TestNativeLeaseNetworkBeforeReboot(t *testing.T) {
	state := lnetLoad(t)
	lnetStep(t, "early_port_squatter_installed", func(t *testing.T) {
		port := state.Ports["lnhost.workers/udp/7053"]
		state.SquatPort = port
		unit := "[Unit]\nDescription=lease network acceptance: takes a bound port first at boot\nDefaultDependencies=no\n" +
			"Before=sockets.target anas-port-udp@" + strconv.Itoa(port) + ".socket\n\n[Service]\nExecStart=" + lnetAgent +
			" serve --tag squatter --udp " + strconv.Itoa(port) + "\n\n[Install]\nWantedBy=sockets.target\n"
		lnetWrite(t, "/etc/systemd/system/lnet-squat.service", []byte(unit), 0644)
		lnetSystemctl(t, "daemon-reload")
		lnetSystemctl(t, "enable", "lnet-squat.service")
		state.TraefikAddrs = lnetTraefikAddresses(t, state.Traefik)
	})
	lnetSave(t, state)
}

func TestNativeLeaseNetworkAfterReboot(t *testing.T) {
	state := lnetLoad(t)
	port := state.SquatPort
	lnetStep(t, "services_back_after_restart", func(t *testing.T) {
		lnetTopology(t)
		deadline := time.Now().Add(5 * time.Minute)
		for _, guest := range []string{"anas-lnlan-a", "anas-lnlan-b", "anas-lnhost-web", "anas-lnmods-a"} {
			instance := state.Instances[guest]
			for {
				status, v4, _ := lnetInstanceAddresses(t, instance.Project, guest)
				if status == "Running" && v4 == instance.IPv4 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s did not return after the host restart: %s %s", guest, status, v4)
				}
				time.Sleep(3 * time.Second)
			}
			lnetWaitListening(t, state.guest(guest), []lnetProbe{lnetTCP("127.0.0.1", 7023)}, guest)
		}
		lnetWaitListening(t, lnetHost(), []lnetProbe{lnetTCP(lnetModuleIP, 9002)}, "module")
	})
	lnetStep(t, "squatted_binding_left_out_and_recorded", func(t *testing.T) {
		active, _ := lnetTry("squat-active", 10*time.Second, nil, "/usr/bin/systemctl", "is-active", "lnet-squat.service")
		if strings.TrimSpace(string(active)) != "active" {
			t.Fatal("the early squatter did not keep its port")
		}
		hold, _ := lnetTry("squat-hold", 10*time.Second, nil, "/usr/bin/systemctl", "is-active", "anas-port-udp@"+strconv.Itoa(port)+".socket")
		if strings.TrimSpace(string(hold)) == "active" {
			t.Fatal("the hold took a port another process held first")
		}
		deadline := time.Now().Add(2 * time.Minute)
		for {
			issues := append(lnetIssues(t, runtimeissues.HostPath), lnetIssues(t, runtimeissues.WorkspacePath(coreNativeWorkspace))...)
			if slices.ContainsFunc(issues, func(i runtimeissues.Issue) bool {
				return i.Open() && strings.HasPrefix(i.Key, incusprovision.PortBindingIssuePrefix) && strings.HasSuffix(i.Key, "lnhost.workers/udp/"+strconv.Itoa(port))
			}) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the squatted binding was not recorded as a runtime issue (INCUS-R-161)")
			}
			time.Sleep(3 * time.Second)
		}
		m := &lnetMatrix{}
		lan := lnetNS(lnetLANNS)
		m.want("INCUS-R-161", lan, lnetUDP(lnetHostLAN4, port), "squatter", lnetLANClient4)
		m.want("INCUS-R-161", lan, lnetTCP(lnetHostLAN4, state.Ports["lnhost.workers/tcp/7022"]), "anas-lnhost-web", lnetLANClient4)
		m.want("INCUS-R-161", lan, lnetTCP(lnetHostLAN4, lnetLANBinding), "anas-lnlan-a", lnetLANClient4)
		m.want("INCUS-R-161 INCUS-R-157", lan, lnetTCP(lnetHostLAN6, lnetLANBinding), "anas-lnlan-a", lnetLANClient6)
		m.run(t, "after-reboot-bindings")
	})
	lnetStep(t, "policy_and_traefik_after_restart", func(t *testing.T) {
		for _, tool := range []string{"/usr/sbin/iptables", "/usr/sbin/ip6tables"} {
			if !lnetForwardOrdered(string(lnetRun(t, "forward-after-reboot", 20*time.Second, nil, tool, "-w", "5", "-S", "FORWARD")), tool == "/usr/sbin/iptables") {
				t.Fatalf("%s lease rules not restored after Docker's at boot (INCUS-R-126)", tool)
			}
		}
		lnetWaitTraefikSet(t, state)
		m := &lnetMatrix{}
		m.want("INCUS-R-118", state.guest("anas-lnmods-a"), lnetConnect(state.Leases["lnmods.workers"].IPv4Gateway, lnetTraefikPort), "", "")
		m.deny("INCUS-R-116", state.guest("anas-lnmods-a"), lnetTCP(lnetWAN4, 80))
		m.want("INCUS-R-113", state.guest("anas-lnlan-a"), lnetTCP(lnetWAN4, 80), "wan", "")
		m.deny("INCUS-R-130", lnetNS(lnetLANNS), lnetTCP(state.Instances["anas-lnlan-b"].IPv4, 7022))
		m.run(t, "after-reboot-policy")
		lnetSurvivors(state, "survivors-after-reboot", t)
	})
	lnetSave(t, state)
}

// --- stage 7: uninstall ---------------------------------------------------------------

// TestNativeLeaseNetworkTeardown removes every consumer through the CLI and
// then, as an operator, the projects, bridges and ACLs that revocation keeps
// (INCUS-R-111), so the approved uninstall can run.
func TestNativeLeaseNetworkTeardown(t *testing.T) {
	state := lnetLoad(t)
	lnetStep(t, "consumers_removed_through_cli", func(t *testing.T) {
		lnetSystemctl(t, "disable", "--now", "lnet-squat.service")
		lnetImport(t, state, "without-leases", lnetVariant{withoutSide: true, withoutInet: true, withoutLeases: true})
		lnetRenderApply(t, state, "without-leases")
		for _, key := range []string{"lnlan.workers", "lnhost.workers", "lnmods.workers"} {
			lease := state.Leases[key]
			if recorded := lnetResourceState(t, lease.Consumer, lease.Resource); recorded.Revocation != "confirmed" {
				t.Fatalf("%s revocation not confirmed: %s", key, recorded.Revocation)
			}
		}
		// The removed declarations took their routes and port bindings along,
		// the bindings once anasd's sync for this activation has run.
		lnetWaitActivationSync(t)
		if routes := lnetRouteFiles(t, state); len(routes) != 0 {
			t.Fatalf("routes left after every publication was removed: %v", routes)
		}
		if body := lnetRun(t, "port-maps-empty", 20*time.Second, nil, "/usr/sbin/nft", "-j", "list", "table", "inet", incusprovision.PortTable); bytes.Contains(body, []byte(`"elem"`)) {
			t.Fatal("port maps still forward after every binding was removed (INCUS-R-152)")
		}
		if held := lnetEnabledHolds(t); len(held) != 0 {
			t.Fatalf("holds left after every binding was removed: %v", held)
		}
	})
	lnetStep(t, "operator_removes_retained_leases", func(t *testing.T) {
		projects, bridges := map[string]bool{}, map[string]bool{}
		for _, lease := range state.Leases {
			projects[lease.Project], bridges[lease.Bridge] = true, true
		}
		for project := range projects {
			var instances, images, profiles []string
			coreNativeQuery(t, http.MethodGet, "/1.0/instances?project="+project, &instances)
			for _, url := range instances {
				lnetIncus(t, "delete-"+lnetObjectName(url), nil, "delete", "--force", "--project", project, lnetObjectName(url))
			}
			coreNativeQuery(t, http.MethodGet, "/1.0/images?project="+project, &images)
			for _, url := range images {
				lnetIncus(t, "image-delete-"+lnetObjectName(url), nil, "image", "delete", "--project", project, lnetObjectName(url))
			}
			coreNativeQuery(t, http.MethodGet, "/1.0/profiles?project="+project, &profiles)
			for _, url := range profiles {
				if name := lnetObjectName(url); name != "default" {
					lnetIncus(t, "profile-delete-"+name, nil, "profile", "delete", "--project", project, name)
				}
			}
			lnetIncus(t, "project-delete-"+project, nil, "project", "delete", project)
		}
		for bridge := range bridges {
			lnetIncus(t, "network-delete-"+bridge, nil, "network", "delete", bridge)
			lnetIncus(t, "acl-delete-"+bridge, nil, "network", "acl", "delete", bridge)
		}
		var left []string
		coreNativeQuery(t, http.MethodGet, "/1.0/instances?all-projects=true", &left)
		if len(left) != 0 {
			t.Fatalf("instances left: %v", left)
		}
		state.DockerForward = map[string][]string{}
		for _, tool := range []string{"/usr/sbin/iptables", "/usr/sbin/ip6tables"} {
			listing := string(lnetRun(t, "forward-before-uninstall-"+filepath.Base(tool), 20*time.Second, nil, tool, "-w", "5", "-S", "FORWARD"))
			if !strings.Contains(listing, "anas-lease-forward-1") {
				t.Fatalf("%s lease rules missing before uninstall", tool)
			}
			state.DockerForward[tool] = lnetOtherForward(listing)
		}
	})
	lnetSave(t, state)
}

// lnetObjectName is the last path element of an Incus object URL, which
// carries ?project= outside the default project.
func lnetObjectName(url string) string {
	path, _, _ := strings.Cut(url, "?")
	return filepath.Base(path)
}

// lnetOtherForward is the FORWARD chain without the ANAS lease rules and
// without Docker's rules for single bridges, which come and go with the
// networks Docker manages. That includes br-anas-ctrl, the Incus control
// network the uninstall removes on purpose.
func lnetOtherForward(listing string) []string {
	var out []string
	for _, line := range strings.Split(listing, "\n") {
		if (strings.HasPrefix(line, "-A FORWARD ") || strings.HasPrefix(line, "-P FORWARD ")) &&
			!strings.Contains(line, "anas-lease-forward") && !lnetDockerBridge.MatchString(line) && !strings.Contains(line, " br-anas-ctrl ") {
			out = append(out, line)
		}
	}
	return out
}

func lnetEnabledHolds(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("/etc/systemd/system/sockets.target.wants")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "anas-port-") {
			out = append(out, entry.Name())
		}
	}
	return out
}

// TestNativeLeaseNetworkAfterUninstall checks that the approved uninstall
// removed everything the lease network put on the host and nothing else
// (INCUS-R-126, R-105).
func TestNativeLeaseNetworkAfterUninstall(t *testing.T) {
	state := lnetLoad(t)
	lnetStep(t, "host_network_rules_removed", func(t *testing.T) {
		for _, tool := range []string{"/usr/sbin/iptables", "/usr/sbin/ip6tables"} {
			listing := string(lnetRun(t, "forward-uninstalled-"+filepath.Base(tool), 20*time.Second, nil, tool, "-w", "5", "-S", "FORWARD"))
			if strings.Contains(listing, "anas-lease-forward") {
				t.Fatalf("%s still carries lease rules after uninstall (INCUS-R-126)", tool)
			}
			if got := lnetOtherForward(listing); !slices.Equal(got, state.DockerForward[tool]) {
				lnetWrite(t, filepath.Join(coreNativeReports, "reports", "forward-uninstalled-"+filepath.Base(tool)+".txt"), []byte(listing), 0600)
				t.Fatalf("%s: uninstall changed rules it does not own (INCUS-R-105)", tool)
			}
		}
		if out, err := lnetTry("port-table-gone", 10*time.Second, nil, "/usr/sbin/nft", "list", "table", "inet", incusprovision.PortTable); err == nil {
			t.Fatalf("port binding table left after uninstall: %s", out)
		}
		if held := lnetEnabledHolds(t); len(held) != 0 {
			t.Fatalf("holds left after uninstall: %v", held)
		}
		units, _ := filepath.Glob("/etc/systemd/system/anas-port-*")
		for _, path := range append(units, incusprovision.NetworkUnitPath, incusprovision.PortBindingsPath) {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("%s left after uninstall", path)
			}
		}
		// Docker and the Modules it runs are not part of the uninstall.
		var inspected []struct{ State struct{ Running bool } }
		if json.Unmarshal(lnetRun(t, "traefik-after-uninstall", 30*time.Second, nil, "/usr/bin/docker", "inspect", state.Traefik), &inspected) != nil ||
			len(inspected) != 1 || !inspected[0].State.Running {
			t.Fatal("uninstall stopped the Traefik container")
		}
	})
}
