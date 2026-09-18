package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// captureConfig is operator input, never a consumer publication request. Both
// sockets must belong to the same disposable lab host/network namespace.
// This collector uses GET only; it does not grant a read-only identity to the
// caller and must not be mounted into a consumer or production watcher.
type captureConfig struct {
	IncusSocket     string   `json:"incus_socket"`
	DockerSocket    string   `json:"docker_socket"`
	Deployment      string   `json:"deployment"`
	Consumer        string   `json:"consumer"`
	Resource        string   `json:"resource"`
	Project         string   `json:"project"`
	Instance        string   `json:"instance"`
	InstancePrefix  string   `json:"instance_prefix"`
	Interface       string   `json:"interface"`
	GuestDevice     string   `json:"guest_device"`
	GuestInterface  string   `json:"guest_interface"`
	DockerContainer string   `json:"docker_container"`
	DockerNetwork   string   `json:"docker_network"`
	AllowedPorts    []uint16 `json:"allowed_ports"`
	GuestPort       uint16   `json:"guest_port"`
	Host            string   `json:"host"`
}

type captureEvidence struct {
	CapturedAt         time.Time `json:"captured_at"`
	ReobserveAfter     time.Time `json:"reobserve_after"`
	IncusVersion       string    `json:"incus_version"`
	DockerVersion      string    `json:"docker_version"`
	DockerContainer    string    `json:"docker_container"`
	DockerNetwork      string    `json:"docker_network"`
	SourceNamespace    string    `json:"source_namespace"`
	CollectorNamespace string    `json:"collector_namespace"`
	Samples            int       `json:"samples"`
}

type socketReader struct {
	client *http.Client
	incus  bool
}

func newSocketReader(path string, incus bool) (*socketReader, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("capture requires absolute canonical lab socket paths")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("capture socket is unavailable")
	}
	for _, system := range []string{"/run/docker.sock", "/var/run/docker.sock", "/var/lib/incus/unix.socket", "/var/snap/lxd/common/lxd/unix.socket"} {
		if resolved == system {
			return nil, fmt.Errorf("capture refuses a default system daemon socket")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("capture input is not a Unix socket")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", resolved)
	}}
	return &socketReader{incus: incus, client: &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (r *socketReader) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://lab"+path, nil)
	if err != nil {
		return fmt.Errorf("invalid capture API path")
	}
	response, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("capture API read failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("capture API returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return fmt.Errorf("capture API response exceeds limit or cannot be read")
	}
	if r.incus {
		var envelope struct {
			Type     string          `json:"type"`
			Metadata json.RawMessage `json:"metadata"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Type != "sync" {
			return fmt.Errorf("invalid Incus capture response")
		}
		raw = envelope.Metadata
	}
	if json.Unmarshal(raw, out) != nil {
		return fmt.Errorf("invalid capture API metadata")
	}
	return nil
}

type incusInstance struct {
	Name            string                       `json:"name"`
	Type            string                       `json:"type"`
	Status          string                       `json:"status"`
	Config          map[string]string            `json:"config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
}
type incusNetworkState struct {
	Hwaddr    string `json:"hwaddr"`
	HostName  string `json:"host_name"`
	State     string `json:"state"`
	Addresses []struct {
		Family  string `json:"family"`
		Address string `json:"address"`
		Scope   string `json:"scope"`
	} `json:"addresses"`
}
type dockerEndpoint struct {
	NetworkID   string `json:"NetworkID"`
	EndpointID  string `json:"EndpointID"`
	IPAddress   string `json:"IPAddress"`
	IPPrefixLen int    `json:"IPPrefixLen"`
	Gateway     string `json:"Gateway"`
	MacAddress  string `json:"MacAddress"`
}
type dockerContainer struct {
	ID    string `json:"Id"`
	State struct {
		Running    bool
		Paused     bool
		Restarting bool
		Pid        int
	}
	NetworkSettings struct{ Networks map[string]dockerEndpoint }
}
type dockerNetwork struct {
	ID      string `json:"Id"`
	Name    string
	Driver  string
	Scope   string
	Options map[string]string
	IPAM    struct {
		Config []struct {
			Subnet  string
			Gateway string
		}
	}
	Containers map[string]struct {
		EndpointID  string
		MacAddress  string
		IPv4Address string
	}
}
type linkInfo struct {
	IfIndex   int    `json:"ifindex"`
	LinkIndex int    `json:"link_index"`
	IfName    string `json:"ifname"`
	Address   string `json:"address"`
	Master    string `json:"master"`
	LinkInfo  struct {
		Kind string `json:"info_kind"`
	} `json:"linkinfo"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("capture command output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func readLinks(ctx context.Context, pid int) ([]linkInfo, error) {
	args := []string{"-j", "-d", "address", "show"}
	program := "ip"
	if pid > 0 {
		program = "nsenter"
		args = append([]string{"--target", strconv.Itoa(pid), "--net", "--", "ip"}, args...)
	}
	cmd := exec.CommandContext(ctx, program, args...)
	var output boundedOutput
	cmd.Stdout = &output
	// No command output, environment or daemon record is included in errors.
	if cmd.Run() != nil {
		return nil, fmt.Errorf("capture could not read network links")
	}
	var links []linkInfo
	if json.Unmarshal(output.Bytes(), &links) != nil {
		return nil, fmt.Errorf("invalid network link capture")
	}
	return links, nil
}
func namespaceOf(pid int) (string, error) {
	p := "/proc/self/ns/net"
	if pid > 0 {
		p = fmt.Sprintf("/proc/%d/ns/net", pid)
	}
	n, err := os.Readlink(p)
	if err != nil {
		return "", fmt.Errorf("capture network namespace unavailable")
	}
	return n, nil
}

func capture(ctx context.Context, c captureConfig) (observation, captureEvidence, error) {
	fail := func(s string) (observation, captureEvidence, error) {
		return observation{}, captureEvidence{}, fmt.Errorf("%s", s)
	}
	if runtime.GOOS != "linux" {
		return fail("capture requires Linux on the disposable lab host")
	}
	id64 := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if !id64.MatchString(c.DockerContainer) || !id64.MatchString(c.DockerNetwork) {
		return fail("capture requires full Docker container and network IDs")
	}
	if c.Deployment == "" || len(c.Deployment) > 128 || (c.Interface != computeclient.InterfaceContainer && c.Interface != computeclient.InterfaceVM) || len(c.Instance) > 63 || !strings.HasPrefix(c.Instance, c.InstancePrefix) || !regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`).MatchString(c.InstancePrefix) {
		return fail("invalid capture lease identity")
	}
	if !idPattern.MatchString(c.Project) || !idPattern.MatchString(c.Consumer) || !idPattern.MatchString(c.Resource) || !regexp.MustCompile(`^anas-[a-z0-9-]+$`).MatchString(c.Instance) || !ifacePattern.MatchString(c.GuestDevice) || !ifacePattern.MatchString(c.GuestInterface) {
		return fail("invalid capture selection")
	}
	incus, err := newSocketReader(c.IncusSocket, true)
	if err != nil {
		return observation{}, captureEvidence{}, err
	}
	defer incus.client.CloseIdleConnections()
	docker, err := newSocketReader(c.DockerSocket, false)
	if err != nil {
		return observation{}, captureEvidence{}, err
	}
	defer docker.client.CloseIdleConnections()
	var server struct {
		Environment struct {
			ServerVersion   string `json:"server_version"`
			ServerClustered bool   `json:"server_clustered"`
		} `json:"environment"`
	}
	if err = incus.get(ctx, "/1.0", &server); err != nil {
		return fail("cannot identify Incus daemon")
	}
	if server.Environment.ServerClustered || server.Environment.ServerVersion == "" {
		return fail("capture requires an identified single-host Incus daemon")
	}
	var version struct{ Version string }
	if err = docker.get(ctx, "/version", &version); err != nil {
		return fail("cannot identify Docker daemon")
	}
	if version.Version == "" {
		return fail("Docker version is missing")
	}
	first, ns, err := captureSample(ctx, c, incus, docker)
	if err != nil {
		return observation{}, captureEvidence{}, err
	}
	second, nsAgain, err := captureSample(ctx, c, incus, docker)
	if err != nil {
		return observation{}, captureEvidence{}, err
	}
	if !reflect.DeepEqual(first, second) || ns != nsAgain {
		return fail("capture identity or topology changed between samples")
	}
	own, err := namespaceOf(0)
	if err != nil {
		return observation{}, captureEvidence{}, err
	}
	now := time.Now().UTC()
	evidence := captureEvidence{CapturedAt: now, ReobserveAfter: now.Add(30 * time.Second), IncusVersion: server.Environment.ServerVersion, DockerVersion: version.Version, DockerContainer: c.DockerContainer, DockerNetwork: c.DockerNetwork, SourceNamespace: ns, CollectorNamespace: own, Samples: 2}
	return second, evidence, nil
}

func captureSample(ctx context.Context, c captureConfig, incus, docker *socketReader) (observation, string, error) {
	fail := func(s string) (observation, string, error) { return observation{}, "", fmt.Errorf("%s", s) }
	bridge := computeclient.NetworkName(c.Project)
	var project struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	if err := incus.get(ctx, "/1.0/projects/"+c.Project, &project); err != nil {
		return fail("cannot read selected Incus project")
	}
	if project.Name != c.Project || project.Config["restricted"] != "true" || project.Config["features.networks"] != "false" || project.Config["restricted.networks.access"] != bridge || project.Config["restricted.devices.nic"] != "managed" {
		return fail("Incus project does not enforce the expected network scope")
	}
	if c.Interface == computeclient.InterfaceContainer && project.Config["restricted.containers.privilege"] != "unprivileged" {
		return fail("container lease does not enforce unprivileged guests")
	}
	var guestNetwork struct {
		Name    string            `json:"name"`
		Type    string            `json:"type"`
		Managed bool              `json:"managed"`
		Status  string            `json:"status"`
		Config  map[string]string `json:"config"`
	}
	if err := incus.get(ctx, "/1.0/networks/"+bridge+"?project=default", &guestNetwork); err != nil {
		return fail("cannot read lease bridge")
	}
	if guestNetwork.Name != bridge || guestNetwork.Type != "bridge" || !guestNetwork.Managed || guestNetwork.Status != "Created" || guestNetwork.Config["user.anas.consumer"] != c.Consumer || guestNetwork.Config["user.anas.sandbox"] != c.Project || guestNetwork.Config["bridge.external_interfaces"] != "" {
		return fail("lease bridge ownership or state mismatch")
	}
	subnet, err := netip.ParsePrefix(guestNetwork.Config["ipv4.address"])
	if err != nil || !subnet.Addr().Is4() {
		return fail("lease bridge has no IPv4 subnet")
	}
	var instance incusInstance
	path := "/1.0/instances/" + c.Instance
	if err = incus.get(ctx, path+"?project="+c.Project, &instance); err != nil {
		return fail("cannot read selected guest")
	}
	expectedType := "container"
	if c.Interface == computeclient.InterfaceVM {
		expectedType = "virtual-machine"
	}
	if instance.Name != c.Instance || instance.Type != expectedType || instance.Status != "Running" || instance.Config["volatile.uuid"] == "" {
		return fail("guest identity or running state mismatch")
	}
	nic := instance.ExpandedDevices[c.GuestDevice]
	count := 0
	for _, d := range instance.ExpandedDevices {
		if d["type"] == "nic" {
			count++
		}
	}
	if count != 1 || nic["type"] != "nic" || nic["network"] != bridge || nic["parent"] != "" || nic["nictype"] != "" {
		return fail("guest does not have exactly one lease-managed NIC")
	}
	expectedMAC := nic["hwaddr"]
	if expectedMAC == "" {
		expectedMAC = instance.Config["volatile."+c.GuestDevice+".hwaddr"]
	}
	if !macPattern.MatchString(strings.ToLower(expectedMAC)) || (nic["name"] != "" && nic["name"] != c.GuestInterface) {
		return fail("guest managed NIC identity is incomplete")
	}
	var state struct {
		Status  string                       `json:"status"`
		Network map[string]incusNetworkState `json:"network"`
	}
	if err = incus.get(ctx, path+"/state?project="+c.Project, &state); err != nil {
		return fail("cannot read guest network state")
	}
	guestNIC := state.Network[c.GuestInterface]
	if !strings.EqualFold(expectedMAC, guestNIC.Hwaddr) || state.Status != "Running" || guestNIC.State != "up" || !ifacePattern.MatchString(guestNIC.HostName) {
		return fail("guest NIC is not running on a known host interface")
	}
	guestIP := ""
	for _, a := range guestNIC.Addresses {
		if a.Family != "inet" || a.Scope != "global" {
			continue
		}
		ip, e := netip.ParseAddr(a.Address)
		if e != nil || !subnet.Contains(ip) {
			continue
		}
		if guestIP != "" {
			return fail("guest NIC has ambiguous IPv4 allocations")
		}
		guestIP = ip.String()
	}
	if guestIP == "" {
		return fail("guest NIC has no lease IPv4 address")
	}
	var leases []struct {
		Address string `json:"address"`
		Hwaddr  string `json:"hwaddr"`
	}
	if err = incus.get(ctx, "/1.0/networks/"+bridge+"/leases?project=default", &leases); err != nil {
		return fail("cannot read managed IP allocations")
	}
	allocations := 0
	for _, l := range leases {
		if l.Address == guestIP {
			if !strings.EqualFold(l.Hwaddr, guestNIC.Hwaddr) {
				return fail("guest IP is allocated to another NIC")
			}
			allocations++
		}
	}
	if allocations != 1 {
		return fail("guest IP does not have a unique managed allocation")
	}
	var container dockerContainer
	if err = docker.get(ctx, "/containers/"+c.DockerContainer+"/json", &container); err != nil {
		return fail("cannot read selected Traefik container")
	}
	if container.ID != c.DockerContainer || !container.State.Running || container.State.Paused || container.State.Restarting || container.State.Pid <= 1 {
		return fail("Traefik container is not stably running")
	}
	ns, err := namespaceOf(container.State.Pid)
	if err != nil {
		return observation{}, "", err
	}
	own, err := namespaceOf(0)
	if err != nil || ns == own {
		return fail("Traefik must use its own network namespace")
	}
	var ingress dockerNetwork
	if err = docker.get(ctx, "/networks/"+c.DockerNetwork, &ingress); err != nil {
		return fail("cannot read Docker ingress network")
	}
	if ingress.ID != c.DockerNetwork || ingress.Driver != "bridge" || ingress.Scope != "local" || ingress.Options["com.docker.network.bridge.default_bridge"] == "true" {
		return fail("ingress must be a local Docker bridge")
	}
	endpoint, ok := container.NetworkSettings.Networks[ingress.Name]
	if !ok || endpoint.NetworkID != ingress.ID || endpoint.EndpointID == "" {
		return fail("Traefik is not attached to the selected ingress network")
	}
	for _, n := range container.NetworkSettings.Networks {
		if ip, e := netip.ParseAddr(n.IPAddress); e == nil && subnet.Contains(ip) {
			return fail("Traefik is attached to the guest subnet")
		}
	}
	member, ok := ingress.Containers[container.ID]
	if !ok || member.EndpointID != endpoint.EndpointID || !strings.EqualFold(member.MacAddress, endpoint.MacAddress) || member.IPv4Address != fmt.Sprintf("%s/%d", endpoint.IPAddress, endpoint.IPPrefixLen) {
		return fail("Docker endpoint membership mismatch")
	}
	ingressSubnet := ""
	for _, s := range ingress.IPAM.Config {
		p, e := netip.ParsePrefix(s.Subnet)
		ip, ipErr := netip.ParseAddr(endpoint.IPAddress)
		if e == nil && ipErr == nil && p.Contains(ip) {
			if ingressSubnet != "" || s.Gateway != endpoint.Gateway || p.Bits() != endpoint.IPPrefixLen {
				return fail("ambiguous ingress IPAM")
			}
			ingressSubnet = p.Masked().String()
		}
	}
	if ingressSubnet == "" {
		return fail("ingress endpoint has no matching IPAM allocation")
	}
	ingressBridge := ingress.Options["com.docker.network.bridge.name"]
	if ingressBridge == "" {
		ingressBridge = "br-" + ingress.ID[:12]
	}
	hostLinks, err := readLinks(ctx, 0)
	if err != nil {
		return observation{}, "", err
	}
	sourceLinks, err := readLinks(ctx, container.State.Pid)
	if err != nil {
		return observation{}, "", err
	}
	bridges := map[string]bool{}
	guestAttached := false
	for _, l := range hostLinks {
		if l.LinkInfo.Kind == "bridge" {
			bridges[l.IfName] = true
		}
		if l.IfName == guestNIC.HostName && l.Master == bridge {
			guestAttached = true
		}
	}
	if !bridges[bridge] || !bridges[ingressBridge] || !guestAttached {
		return fail("observed bridges or guest attachment do not exist in the collector namespace")
	}
	sourceIface, veth := "", ""
	for _, l := range sourceLinks {
		for _, a := range l.AddrInfo {
			if a.Family != "inet" || a.Local != endpoint.IPAddress || a.PrefixLen != endpoint.IPPrefixLen {
				continue
			}
			if sourceIface != "" || !strings.EqualFold(l.Address, endpoint.MacAddress) {
				return fail("ambiguous Traefik source interface")
			}
			sourceIface = l.IfName
			for _, h := range hostLinks {
				if h.IfIndex == l.LinkIndex && h.LinkIndex == l.IfIndex && h.Master == ingressBridge && h.LinkInfo.Kind == "veth" {
					if veth != "" {
						return fail("ambiguous Traefik veth peer")
					}
					veth = h.IfName
				}
			}
		}
	}
	if sourceIface == "" || veth == "" {
		return fail("cannot prove the bidirectional Traefik veth attachment")
	}
	nsAfter, err := namespaceOf(container.State.Pid)
	if err != nil || nsAfter != ns {
		return fail("Traefik namespace changed during capture")
	}
	o := observation{Deployment: c.Deployment, Consumer: c.Consumer, Resource: c.Resource, Project: c.Project, Instance: c.Instance, InstanceUUID: instance.Config["volatile.uuid"], Interface: c.Interface, State: state.Status, NetworkOwner: c.Consumer, InstanceProject: c.Project, InstancePrefix: c.InstancePrefix, GuestBridge: bridge, GuestSubnet: subnet.Masked().String(), GuestIP: guestIP, AllocationIP: guestIP, AllocationMAC: strings.ToLower(guestNIC.Hwaddr), GuestMAC: strings.ToLower(guestNIC.Hwaddr), IngressBridge: ingressBridge, TraefikVeth: veth, IngressSubnet: ingressSubnet, TraefikIP: endpoint.IPAddress, IngressGateway: endpoint.Gateway, TraefikInterface: sourceIface, AllowedPorts: c.AllowedPorts, GuestPort: c.GuestPort, Host: c.Host}
	o.GuestHostInterface = guestNIC.HostName
	o.InstanceGeneration = instance.Config["volatile.uuid.generation"]
	o.DockerContainerID = container.ID
	o.DockerEndpointID = endpoint.EndpointID
	if _, err = generate(o); err != nil {
		return observation{}, "", err
	}
	return o, ns, nil
}
