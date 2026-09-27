package computeingressruntime

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
)

// IncusObserverConfig is supplied by the trusted installer, never a request
// directory. A reader running outside hostd needs a server-enforced read-only
// identity. The compiled host observation action may reuse the root-only
// management credential behind its narrow input/output boundary; it must never
// deliver that credential to the mediator. GET-only code does not make an
// ordinary project-restricted TLS certificate read-only on the daemon.
type IncusObserverConfig struct {
	Endpoint      string `json:"-"`
	ServerCertPEM []byte `json:"-"`
	ClientCertPEM []byte `json:"-"`
	ClientKeyPEM  []byte `json:"-"`
	// ServerVersion pins the exact reported version, not a compatibility claim.
	ServerVersion  string
	Authorizations []*computeingress.Authorization
	LeaseScopes    []IncusLeaseObservationScope
}

func (IncusObserverConfig) String() string     { return "[Incus observer configuration: redacted]" }
func (c IncusObserverConfig) GoString() string { return c.String() }

// IncusFactReader reads only the selected project, managed bridge, instance,
// instance state and bridge leases, plus server identity. It never lists all
// projects/instances or accepts an IP, device name or API path from a request.
type IncusFactReader struct {
	client            *pinnedGETClient
	scopes            map[computeingress.Lease]*computeingress.Authorization
	leaseScopes       map[computeingress.Lease]IncusLeaseObservationScope
	serverVersion     string
	serverFingerprint string
	clientFingerprint string
}

var _ FactReader = (*IncusFactReader)(nil)
var _ Observer = (*IncusFactReader)(nil)

func NewIncusFactReader(config IncusObserverConfig) (*IncusFactReader, error) {
	if len(config.ServerVersion) == 0 || len(config.ServerVersion) > 64 || strings.ContainsAny(config.ServerVersion, " \t\r\n\x00") || len(config.Authorizations)+len(config.LeaseScopes) == 0 || len(config.Authorizations)+len(config.LeaseScopes) > 1024 {
		return nil, fmt.Errorf("Incus observer requires an explicit version and bounded lease scope")
	}
	scopes := make(map[computeingress.Lease]*computeingress.Authorization)
	projects := make(map[string]bool)
	for _, grant := range config.Authorizations {
		if err := grant.Validate(); err != nil {
			return nil, fmt.Errorf("Incus observer has an invalid frozen scope")
		}
		lease := computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}
		if scopes[lease] != nil || projects[grant.Project] {
			return nil, fmt.Errorf("Incus observer has a duplicate lease or project")
		}
		scopes[lease] = grant.Clone()
		projects[grant.Project] = true
	}
	leaseScopes := make(map[computeingress.Lease]IncusLeaseObservationScope)
	for _, scope := range config.LeaseScopes {
		lease := computeingress.Lease{Consumer: scope.Consumer, Resource: scope.Resource}
		if scope.Validate() != nil || projects[scope.Project] || scopes[lease] != nil {
			return nil, fmt.Errorf("Incus observer has an invalid or overlapping lease scope")
		}
		if _, exists := leaseScopes[lease]; exists {
			return nil, fmt.Errorf("Incus observer has a duplicate lease scope")
		}
		projects[scope.Project] = true
		leaseScopes[lease] = scope
	}
	clientCert, err := singleCertificate(config.ClientCertPEM)
	if err != nil || len(config.ClientKeyPEM) == 0 || len(config.ClientKeyPEM) > 64<<10 {
		return nil, fmt.Errorf("Incus observer requires a dedicated client keypair")
	}
	keypair, err := tls.X509KeyPair(config.ClientCertPEM, config.ClientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("Incus observer client keypair is invalid")
	}
	client, err := newPinnedGETClient(config.Endpoint, config.ServerCertPEM, []tls.Certificate{keypair})
	if err != nil {
		return nil, err
	}
	serverCert, err := singleCertificate(config.ServerCertPEM)
	if err != nil {
		return nil, fmt.Errorf("Incus observer server certificate is invalid")
	}
	serverDigest := sha256.Sum256(serverCert.Raw)
	clientDigest := sha256.Sum256(clientCert.Raw)
	return &IncusFactReader{client: client, scopes: scopes, leaseScopes: leaseScopes, serverVersion: config.ServerVersion,
		serverFingerprint: hex.EncodeToString(serverDigest[:]), clientFingerprint: hex.EncodeToString(clientDigest[:])}, nil
}

func (r *IncusFactReader) CloseIdleConnections() {
	if r != nil && r.client != nil {
		r.client.http.CloseIdleConnections()
	}
}

// ValidateTarget supplies the executor's independent instance check. The
// authorization source remains a separate required dependency: an installed
// reader neither proves that Core's epoch is active nor authorizes a hostname.
// No cached successful observation can validate another lifecycle step.
func (r *IncusFactReader) ValidateTarget(ctx context.Context, target PublicationTarget) error {
	if r == nil || validateTarget(target.Epoch, target) != nil {
		return fmt.Errorf("invalid Incus HTTP target")
	}
	p := target.Publication
	grant := r.scopes[p.Lease]
	if grant == nil || grant.Deployment != p.Deployment {
		return fmt.Errorf("HTTP target has no installed Incus observation scope")
	}
	request := computeingress.Request{Action: "publish", InstanceID: p.InstanceID, WorkloadID: p.WorkloadID, GuestPort: p.GuestPort, Label: p.Label}
	facts, err := r.ObserveHTTP(ctx, grant, request)
	if err != nil {
		return err
	}
	return validateTargetFacts(grant, target, facts)
}

// Both direct installed readers and WorkspaceSource use the same comparison.
// Scope and workload ownership are checked by the fact reader, while current
// Core/request/Host authorization is checked independently by the executor.
func validateTargetFacts(grant *computeingress.Authorization, target PublicationTarget, facts computeingress.Facts) error {
	p := target.Publication
	if facts.Project != grant.Project || facts.Interface != grant.Interface || facts.InstanceID != p.InstanceID || facts.InstanceUUID != p.InstanceUUID || facts.Incarnation != target.Incarnation || facts.State != "Running" || facts.NetworkOwner != grant.Consumer || facts.GuestIP != p.GuestIP || facts.AllocationIP != p.GuestIP || facts.GuestMAC != target.NICMAC || facts.AllocationMAC != target.NICMAC {
		return fmt.Errorf("HTTP instance or managed NIC allocation changed")
	}
	return nil
}

func (r *IncusFactReader) ObserveHTTP(ctx context.Context, grant *computeingress.Authorization, request computeingress.Request) (computeingress.Facts, error) {
	observed, err := r.ObserveHostHTTP(ctx, grant, request)
	return observed.Facts, err
}

// ObserveHostHTTP exposes the same validated double sample to the compiled
// host observer. The host must independently check the reported veth against
// the local kernel; a daemon-reported name is not a physical identity proof.
// Management credentials may be used only inside that root-side boundary,
// never delivered to WorkspaceSource or a consumer.
func (r *IncusFactReader) ObserveHostHTTP(ctx context.Context, grant *computeingress.Authorization, request computeingress.Request) (IncusHostObservation, error) {
	empty := IncusHostObservation{}
	if ctx == nil || r == nil || r.client == nil || grant.Validate() != nil || request.Validate() != nil {
		return empty, fmt.Errorf("invalid Incus HTTP observation scope or request")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	scope := r.scopes[computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}]
	// The complete installed grant is immutable, not just project and prefix.
	// In particular, a caller cannot widen ports or replace deployment/auth/Host
	// policy using the same transport credentials. A new epoch needs delivery.
	if scope == nil || !reflect.DeepEqual(scope, grant) || request.Action != "publish" || !strings.HasPrefix(request.InstanceID, scope.InstancePrefix) || len(request.InstanceID) <= len(scope.InstancePrefix) || !slices.Contains(scope.Policy.AllowedPorts, request.GuestPort) {
		return empty, fmt.Errorf("HTTP request is outside the installed Incus observer scope")
	}
	observeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	selection := incusInstanceSelection{Project: scope.Project, Consumer: scope.Consumer, Interface: scope.Interface}
	instanceRequest := IncusInstanceObservationRequest{InstanceID: request.InstanceID, WorkloadID: request.WorkloadID}
	first, err := r.sample(observeCtx, selection, instanceRequest)
	if err != nil {
		if observeCtx.Err() != nil {
			return empty, observeCtx.Err()
		}
		return empty, err
	}
	second, err := r.sample(observeCtx, selection, instanceRequest)
	if err != nil {
		if observeCtx.Err() != nil {
			return empty, observeCtx.Err()
		}
		return empty, err
	}
	if first != second {
		return empty, fmt.Errorf("Incus HTTP identity or allocation changed between observations")
	}
	if err := observeCtx.Err(); err != nil {
		return empty, err
	}
	return second, nil
}

// Only stable identity fields enter the comparison. Traffic counters and other
// guests' DHCP churn must not make two otherwise identical samples unequal.
type incusHTTPSample struct {
	Facts      computeingress.Facts
	ServerName string
	ServerPID  int
	Device     string
	Interface  string
	HostName   string
	BridgeCIDR string
}

// IncusHostObservation contains selected non-secret observations only. It is
// not a durable address reservation, network authorization or raw API result.
type IncusHostObservation = incusHTTPSample

type incusHTTPInstance struct {
	Name            string                       `json:"name"`
	Project         string                       `json:"project"`
	Type            string                       `json:"type"`
	Status          string                       `json:"status"`
	StatusCode      int                          `json:"status_code"`
	LastUsedAt      time.Time                    `json:"last_used_at"`
	Config          map[string]string            `json:"config"`
	ExpandedConfig  map[string]string            `json:"expanded_config"`
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
}

type incusHTTPNIC struct {
	State     string `json:"state"`
	Type      string `json:"type"`
	Hwaddr    string `json:"hwaddr"`
	HostName  string `json:"host_name"`
	Addresses []struct {
		Family  string `json:"family"`
		Scope   string `json:"scope"`
		Address string `json:"address"`
		Netmask string `json:"netmask"`
	} `json:"addresses"`
}

var observedUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var observedInterface = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)
var observedDevice = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func (r *IncusFactReader) sampleLeaseNetwork(ctx context.Context, grant incusInstanceSelection) (IncusLeaseNetworkObservation, error) {
	fail := func(message string) (IncusLeaseNetworkObservation, error) {
		return IncusLeaseNetworkObservation{}, fmt.Errorf("%s", message)
	}
	var server struct {
		Auth           string `json:"auth"`
		APIVersion     string `json:"api_version"`
		AuthUserName   string `json:"auth_user_name"`
		AuthUserMethod string `json:"auth_user_method"`
		Environment    struct {
			Server                 string `json:"server"`
			ServerVersion          string `json:"server_version"`
			ServerClustered        *bool  `json:"server_clustered"`
			ServerName             string `json:"server_name"`
			ServerPID              int    `json:"server_pid"`
			CertificateFingerprint string `json:"certificate_fingerprint"`
		} `json:"environment"`
	}
	if err := r.get(ctx, "/1.0?project="+url.QueryEscape(grant.Project), &server); err != nil {
		return fail("cannot identify the Incus observer connection")
	}
	env := server.Environment
	if server.Auth != "trusted" || server.APIVersion != "1.0" || server.AuthUserMethod != "tls" || server.AuthUserName != r.clientFingerprint || env.Server != "incus" || env.ServerVersion != r.serverVersion || env.ServerClustered == nil || *env.ServerClustered || env.ServerName == "" || len(env.ServerName) > 253 || env.ServerPID <= 0 || env.CertificateFingerprint != r.serverFingerprint {
		return fail("Incus observer server, version or TLS identity does not match")
	}
	bridge := computeclient.NetworkName(grant.Project)
	var project struct {
		Name   string            `json:"name"`
		Config map[string]string `json:"config"`
	}
	if err := r.get(ctx, "/1.0/projects/"+url.PathEscape(grant.Project), &project); err != nil {
		return fail("cannot read the selected Incus project")
	}
	if project.Name != grant.Project || project.Config["restricted"] != "true" || project.Config["features.networks"] != "false" || project.Config["restricted.networks.access"] != bridge || project.Config["restricted.devices.nic"] != "managed" {
		return fail("Incus project no longer enforces the lease network scope")
	}
	if grant.Interface == computeclient.InterfaceContainer && project.Config["restricted.containers.privilege"] != "unprivileged" {
		return fail("Incus container lease no longer requires unprivileged guests")
	}
	var network struct {
		Name    string            `json:"name"`
		Project string            `json:"project"`
		Type    string            `json:"type"`
		Managed bool              `json:"managed"`
		Status  string            `json:"status"`
		Config  map[string]string `json:"config"`
	}
	bridgePath := "/1.0/networks/" + url.PathEscape(bridge)
	if err := r.get(ctx, bridgePath+"?project=default", &network); err != nil {
		return fail("cannot read the selected Incus bridge")
	}
	if network.Name != bridge || (network.Project != "" && network.Project != "default") || network.Type != "bridge" || !network.Managed || network.Status != "Created" || network.Config["user.anas.consumer"] != grant.Consumer || network.Config["user.anas.sandbox"] != grant.Project || network.Config["bridge.external_interfaces"] != "" || network.Config["ipv4.nat"] != "true" || network.Config["ipv4.dhcp"] == "false" {
		return fail("Incus bridge ownership or managed NAT scope changed")
	}
	subnet, err := netip.ParsePrefix(network.Config["ipv4.address"])
	if err != nil || !privateHTTPSubnet(subnet) || subnet.String() != network.Config["ipv4.address"] {
		return fail("Incus bridge requires an explicit private IPv4 subnet")
	}
	return IncusLeaseNetworkObservation{BridgeName: bridge, BridgeCIDR: subnet.String(), ServerName: env.ServerName, ServerPID: env.ServerPID}, nil
}

func (r *IncusFactReader) sample(ctx context.Context, grant incusInstanceSelection, request IncusInstanceObservationRequest) (incusHTTPSample, error) {
	fail := func(message string) (incusHTTPSample, error) { return incusHTTPSample{}, fmt.Errorf("%s", message) }
	observedNetwork, err := r.sampleLeaseNetwork(ctx, grant)
	if err != nil {
		return incusHTTPSample{}, err
	}
	bridge := observedNetwork.BridgeName
	bridgePath := "/1.0/networks/" + url.PathEscape(bridge)
	subnet, err := netip.ParsePrefix(observedNetwork.BridgeCIDR)
	if err != nil {
		return fail("Incus managed bridge observation is invalid")
	}
	instancePath := "/1.0/instances/" + url.PathEscape(request.InstanceID)
	projectQuery := "?project=" + url.QueryEscape(grant.Project)
	var instance incusHTTPInstance
	if err := r.get(ctx, instancePath+projectQuery, &instance); err != nil {
		return fail("cannot read the selected Incus instance")
	}
	wantType := "container"
	if grant.Interface == computeclient.InterfaceVM {
		wantType = "virtual-machine"
	}
	uuid, generation := instance.Config["volatile.uuid"], instance.Config["volatile.uuid.generation"]
	if instance.Name != request.InstanceID || (instance.Project != "" && instance.Project != grant.Project) || instance.Type != wantType || instance.Status != "Running" || instance.StatusCode != 103 || !observedUUID.MatchString(uuid) || !observedUUID.MatchString(generation) || instance.LastUsedAt.IsZero() || instance.LastUsedAt.After(time.Now()) {
		return fail("Incus instance identity, incarnation or running state is incomplete")
	}
	// A consumer may bypass computeclient and forge its request file. Resolve
	// workload ownership from the selected daemon instance, never from the file
	// or a profile-inherited label. These labels are not cross-project auth.
	if instance.Config["user.anas.managed"] != "true" || instance.Config["user.anas.workload"] != request.WorkloadID {
		return fail("Incus HTTP target is not the managed instance of the requested workload")
	}
	if grant.Interface == computeclient.InterfaceContainer && instance.ExpandedConfig["security.privileged"] != "" && instance.ExpandedConfig["security.privileged"] != "false" {
		return fail("Incus HTTP observation refuses a privileged container")
	}
	deviceName := ""
	var device map[string]string
	for name, candidate := range instance.ExpandedDevices {
		if candidate["type"] == "proxy" {
			return fail("Incus HTTP target must not use a proxy device")
		}
		if candidate["type"] != "nic" {
			continue
		}
		if device != nil || !observedDevice.MatchString(name) || candidate["network"] != bridge || candidate["parent"] != "" || candidate["nictype"] != "" || candidate["vlan"] != "" || candidate["vlan.tagged"] != "" {
			return fail("Incus target must have exactly one untagged lease-managed NIC")
		}
		deviceName, device = name, candidate
	}
	if device == nil {
		return fail("Incus target has no managed NIC")
	}
	// A provider-owned profile is not proof about the expanded instance:
	// restricted project clients may still submit device overrides. Inspect
	// the effective NIC on every sample, including renewed observations.
	// IPv6 filtering is not required: Incus only starts it with host
	// br_netfilter, and HTTP publication targets the guest's IPv4 address.
	for _, key := range []string{"security.mac_filtering", "security.ipv4_filtering"} {
		if device[key] != "true" {
			return fail("Incus managed NIC source filtering is absent or overridden")
		}
	}
	expectedMAC := device["hwaddr"]
	if expectedMAC == "" {
		expectedMAC = instance.Config["volatile."+deviceName+".hwaddr"]
	}
	mac, err := canonicalObservedMAC(expectedMAC)
	if err != nil {
		return fail("Incus managed NIC has no unambiguous Ethernet identity")
	}
	var state struct {
		Status     string                  `json:"status"`
		StatusCode int                     `json:"status_code"`
		Network    map[string]incusHTTPNIC `json:"network"`
	}
	if err := r.get(ctx, instancePath+"/state"+projectQuery, &state); err != nil {
		return fail("cannot read the selected Incus instance network state")
	}
	if state.Status != "Running" || state.StatusCode != 103 {
		return fail("Incus HTTP target is no longer running")
	}
	interfaceName := ""
	var nic incusHTTPNIC
	for name, candidate := range state.Network {
		actualMAC, err := canonicalObservedMAC(candidate.Hwaddr)
		if err != nil || actualMAC != mac {
			continue
		}
		if interfaceName != "" || !observedInterface.MatchString(name) || candidate.Type != "broadcast" || candidate.State != "up" || !observedInterface.MatchString(candidate.HostName) || (device["name"] != "" && device["name"] != name) {
			return fail("Incus runtime NIC identity is ambiguous or unavailable")
		}
		interfaceName, nic = name, candidate
	}
	if interfaceName == "" {
		return fail("Incus runtime state does not contain the managed NIC")
	}
	guestIP := ""
	for _, address := range nic.Addresses {
		if address.Family != "inet" || address.Scope != "global" {
			continue
		}
		ip, err := netip.ParseAddr(address.Address)
		if guestIP != "" || err != nil || !ip.Is4() || !ip.IsPrivate() || !subnet.Contains(ip) || ip.String() != address.Address || ip == subnet.Addr() || ip == subnet.Masked().Addr() || ipv4Broadcast(subnet) == ip || address.Netmask != strconv.Itoa(subnet.Bits()) {
			return fail("Incus managed NIC has an ambiguous or out-of-scope IPv4 address")
		}
		guestIP = ip.String()
	}
	if guestIP == "" || (device["ipv4.address"] != "" && device["ipv4.address"] != guestIP) {
		return fail("Incus runtime IPv4 address does not match the managed NIC")
	}
	var leases []struct {
		Address string `json:"address"`
		Hwaddr  string `json:"hwaddr"`
		Type    string `json:"type"`
	}
	if err := r.get(ctx, bridgePath+"/leases?project=default", &leases); err != nil {
		return fail("cannot read the selected Incus bridge allocations")
	}
	allocations := 0
	for _, lease := range leases {
		if lease.Address != guestIP {
			continue
		}
		allocatedMAC, err := canonicalObservedMAC(lease.Hwaddr)
		if err != nil || allocatedMAC != mac || (lease.Type != "static" && lease.Type != "dynamic") {
			return fail("Incus guest address is not allocated to the selected managed NIC")
		}
		allocations++
	}
	if allocations != 1 {
		return fail("Incus guest address has no unique managed allocation")
	}
	// A stop/start may preserve UUID, MAC and IP. Include the last start time
	// and generation in the executor binding so a lost event cannot renew the
	// old reservation after observing a new incarnation.
	identity, _ := json.Marshal([3]string{uuid, generation, instance.LastUsedAt.UTC().Format(time.RFC3339Nano)})
	digest := sha256.Sum256(identity)
	return incusHTTPSample{
		Facts: computeingress.Facts{Project: grant.Project, Interface: grant.Interface, InstanceID: request.InstanceID,
			InstanceUUID: uuid, Incarnation: hex.EncodeToString(digest[:]), State: "Running", NetworkOwner: grant.Consumer,
			GuestIP: guestIP, AllocationIP: guestIP, GuestMAC: mac, AllocationMAC: mac},
		ServerName: observedNetwork.ServerName, ServerPID: observedNetwork.ServerPID, Device: deviceName, Interface: interfaceName,
		HostName: nic.HostName, BridgeCIDR: subnet.String(),
	}, nil
}

func (r *IncusFactReader) get(ctx context.Context, path string, out any) error {
	body, err := r.client.get(ctx, path, 2<<20)
	if err != nil {
		return err
	}
	var response struct {
		Type       string          `json:"type"`
		StatusCode int             `json:"status_code"`
		ErrorCode  int             `json:"error_code"`
		Metadata   json.RawMessage `json:"metadata"`
	}
	if decodeObservedJSON(body, &response) != nil || response.Type != "sync" || response.StatusCode != 200 || response.ErrorCode != 0 || len(response.Metadata) == 0 || string(response.Metadata) == "null" {
		return fmt.Errorf("Incus observer received an incomplete or unsuccessful API envelope")
	}
	return decodeObservedJSON(response.Metadata, out)
}

func canonicalObservedMAC(value string) (string, error) {
	mac, err := net.ParseMAC(value)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || mac.String() == "00:00:00:00:00:00" {
		return "", fmt.Errorf("invalid unicast Ethernet address")
	}
	return mac.String(), nil
}

func privateHTTPSubnet(prefix netip.Prefix) bool {
	if !prefix.IsValid() || !prefix.Addr().Is4() || prefix.Bits() > 30 {
		return false
	}
	for _, network := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		private := netip.MustParsePrefix(network)
		if prefix.Bits() >= private.Bits() && private.Contains(prefix.Addr()) {
			return prefix.Addr() != prefix.Masked().Addr() && prefix.Addr() != ipv4Broadcast(prefix)
		}
	}
	return false
}

func ipv4Broadcast(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Masked().Addr().As4()
	for bit := prefix.Bits(); bit < 32; bit++ {
		bytes[bit/8] |= 1 << (7 - bit%8)
	}
	return netip.AddrFrom4(bytes)
}
