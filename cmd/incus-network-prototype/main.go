// incus-network-prototype captures lab observations and generates network plans.
// Capture runs read-only queries. Request/fixture registration creates lab
// files only; no mode changes networking or enables ingress.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

type observation struct {
	Deployment   string `json:"deployment"`
	Consumer     string `json:"consumer"`
	Resource     string `json:"resource"`
	Project      string `json:"project"`
	Instance     string `json:"instance"`
	InstanceUUID string `json:"instance_uuid"`
	// Additional identity facts populated by live capture. Offline legacy lab
	// observations may omit them; they are not promoted to trusted live evidence.
	GuestHostInterface string `json:"guest_host_interface,omitempty"`
	InstanceGeneration string `json:"instance_generation,omitempty"`
	DockerContainerID  string `json:"docker_container_id,omitempty"`
	DockerEndpointID   string `json:"docker_endpoint_id,omitempty"`
	Interface          string `json:"interface"`
	State              string `json:"state"`
	// These facts must be captured by the lab operator from Docker/Incus,
	// never copied from a consumer's publication request.
	NetworkOwner          string   `json:"network_owner"`
	InstanceProject       string   `json:"instance_project"`
	InstancePrefix        string   `json:"instance_prefix"`
	GuestBridge           string   `json:"guest_bridge"`
	GuestSubnet           string   `json:"guest_subnet"`
	GuestIP               string   `json:"guest_ip"`
	AllocationIP          string   `json:"allocation_ip"`
	AllocationMAC         string   `json:"allocation_mac"`
	GuestMAC              string   `json:"guest_mac"`
	IngressBridge         string   `json:"ingress_bridge"`
	TraefikVeth           string   `json:"traefik_veth"`
	IngressSubnet         string   `json:"ingress_subnet"`
	TraefikIP             string   `json:"traefik_ip"`
	IngressGateway        string   `json:"ingress_gateway"`
	TraefikInterface      string   `json:"traefik_interface"`
	AllowedPorts          []uint16 `json:"allowed_ports"`
	GuestPort             uint16   `json:"guest_port"`
	Host                  string   `json:"host"`
	Auth                  string   `json:"auth,omitempty"`
	ForwardAuthMiddleware string   `json:"forward_auth_middleware,omitempty"`
	// The prototype exercises HTTP data transport only, with no public route.
	// Auth may be supplied by the lab mediation mode; production remains closed.
}

type artifacts struct {
	Firewall    string
	RouteEnv    string
	AddRoute    []string
	DeleteRoute []string
	Revoke      []string
	Conntrack   []string
}

var ifacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var macPattern = regexp.MustCompile(`^(?:[a-f0-9]{2}:){5}[a-f0-9]{2}$`)

func generate(o observation) (artifacts, error) {
	fail := func(s string) (artifacts, error) { return artifacts{}, fmt.Errorf("%s", s) }
	if o.Deployment == "" || !idPattern.MatchString(o.Consumer) || !idPattern.MatchString(o.Resource) || !idPattern.MatchString(o.Project) {
		return fail("missing deployment or invalid lease identity")
	}
	if o.Interface != "incus_vm" && o.Interface != "incus_container" {
		return fail("unsupported compute interface")
	}
	if o.State != "Running" || o.InstanceUUID == "" || o.InstanceProject != o.Project || o.NetworkOwner != o.Consumer {
		return fail("instance is not a running member of the observed lease")
	}
	if !strings.HasPrefix(o.InstancePrefix, "anas-") || !strings.HasPrefix(o.Instance, o.InstancePrefix) || len(o.Instance) <= len(o.InstancePrefix) {
		return fail("instance prefix does not match lease")
	}
	if !macPattern.MatchString(o.GuestMAC) || o.GuestMAC != o.AllocationMAC || o.GuestIP != o.AllocationIP {
		return fail("guest NIC does not match the observed allocation")
	}
	for _, v := range []string{o.GuestBridge, o.IngressBridge, o.TraefikVeth, o.TraefikInterface} {
		if !ifacePattern.MatchString(v) {
			return fail("invalid interface name")
		}
	}
	if o.GuestBridge == o.IngressBridge {
		return fail("guest and ingress bridges must differ")
	}
	guest, err := netip.ParsePrefix(o.GuestSubnet)
	if err != nil || !guest.Addr().Is4() || guest != guest.Masked() {
		return fail("guest subnet must be canonical IPv4")
	}
	ingress, err := netip.ParsePrefix(o.IngressSubnet)
	if err != nil || !ingress.Addr().Is4() || ingress != ingress.Masked() {
		return fail("ingress subnet must be canonical IPv4")
	}
	if guest.Overlaps(ingress) {
		return fail("guest and ingress subnets overlap")
	}
	parseIP := func(value string, subnet netip.Prefix) (netip.Addr, bool) {
		ip, err := netip.ParseAddr(value)
		return ip, err == nil && ip.Is4() && ip.IsPrivate() && subnet.Contains(ip) && ip != subnet.Addr() && ip != lastIPv4(subnet)
	}
	guestIP, ok := parseIP(o.GuestIP, guest)
	if !ok {
		return fail("guest must have a usable private IPv4 allocation")
	}
	source, ok := parseIP(o.TraefikIP, ingress)
	if !ok {
		return fail("Traefik must have a private IPv4 address on ingress bridge")
	}
	gateway, ok := parseIP(o.IngressGateway, ingress)
	if !ok || gateway == source {
		return fail("invalid ingress gateway")
	}
	allowed := false
	for _, port := range o.AllowedPorts {
		if port == o.GuestPort && port > 0 {
			allowed = true
		}
	}
	if !allowed {
		return fail("guest HTTP port is not in the lease allowlist")
	}
	// A reserved domain and non-TLS route make this a lab probe, never a public
	// unauthenticated publishing interface. Production auth stays frozen in M11.
	if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.example\.test$`).MatchString(o.Host) {
		return fail("prototype host must be a single label under example.test")
	}
	if (o.Auth == "" || o.Auth == "none") && o.ForwardAuthMiddleware != "" {
		return fail("unauthenticated lab route cannot carry middleware")
	}
	if o.Auth == "forward_auth" {
		if !computeingress.ValidMiddleware(o.ForwardAuthMiddleware) {
			return fail("lab forward_auth requires a valid frozen middleware")
		}
	} else if o.Auth != "" && o.Auth != "none" {
		return fail("invalid lab HTTP auth policy")
	}
	tuple := fmt.Sprintf("%s . %d", guestIP, o.GuestPort)
	// The tuple remains mandatory even for established replies. Removing it
	// blocks existing flows before any broader Docker/Incus established rule.
	// The bridge hook binds origin to the observed veth, defeating a peer on
	// the ingress bridge that spoofs Traefik's source IP/MAC.
	// ibrname is valid on bridge input/forward, not prerouting. Routed
	// packets addressed to the host gateway traverse bridge input.
	firewall := fmt.Sprintf(`table bridge anas_incus_lab_l2 {
 chain origin {
  type filter hook input priority -200; policy accept;
  meta ibrname "%s" iifname != "%s" drop
 }
}
table inet anas_incus_lab {
 set http_backend {
  type ipv4_addr . inet_service
  flags timeout
  timeout 30s
  elements = { %s timeout 30s }
 }
 chain forward {
  type filter hook forward priority -210; policy accept;
  iifname "%s" oifname "%s" ip saddr %s ip daddr . tcp dport @http_backend ct state new,established accept
  iifname "%s" oifname "%s" ip daddr %s ip saddr . tcp sport @http_backend ct state established accept
  iifname "%s" drop
  oifname "%s" drop
  oifname "%s" drop
 }
}
`, o.IngressBridge, o.TraefikVeth, tuple, o.IngressBridge, o.GuestBridge, source, o.GuestBridge, o.IngressBridge, source, o.IngressBridge, o.IngressBridge, o.GuestBridge)
	route := []string{"ip", "-4", "route", "replace", guestIP.String() + "/32", "via", gateway.String(), "dev", o.TraefikInterface, "src", source.String()}
	middlewareEnv := ""
	if o.Auth == "forward_auth" {
		middlewareEnv = "ANAS_TRAEFIK_ROUTE__INCUS_LAB__MIDDLEWARES=" + o.ForwardAuthMiddleware + "\n"
	}
	return artifacts{
		Firewall:    firewall,
		RouteEnv:    fmt.Sprintf("ANAS_TRAEFIK_ROUTE__INCUS_LAB__RULE=Host(`%s`)\nANAS_TRAEFIK_ROUTE__INCUS_LAB__URL=http://%s:%d\nANAS_TRAEFIK_ROUTE__INCUS_LAB__ENTRYPOINTS=http\nANAS_TRAEFIK_ROUTE__INCUS_LAB__TLS=false\n", o.Host, guestIP, o.GuestPort) + middlewareEnv,
		AddRoute:    route,
		DeleteRoute: []string{"ip", "-4", "route", "del", guestIP.String() + "/32", "via", gateway.String(), "dev", o.TraefikInterface},
		Revoke:      []string{"nft", "delete", "element", "inet", "anas_incus_lab", "http_backend", "{", tuple, "}"},
		Conntrack:   []string{"conntrack", "-D", "-p", "tcp", "--orig-src", source.String(), "--orig-dst", guestIP.String(), "--dport", fmt.Sprint(o.GuestPort)},
	}, nil
}

func lastIPv4(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Addr().As4()
	bits := 32 - prefix.Bits()
	for i := 3; i >= 0 && bits > 0; i-- {
		n := bits
		if n > 8 {
			n = 8
		}
		bytes[i] |= byte((1 << n) - 1)
		bits -= n
	}
	return netip.AddrFrom4(bytes)
}

func run(args []string) error {
	flags := flag.NewFlagSet("incus-network-prototype", flag.ContinueOnError)
	input := flags.String("input", "", "operator-captured lab observation JSON")
	captureInput := flags.String("capture", "", "read-only Linux lab capture configuration JSON")
	probeCapture := flags.String("capture-probe", "", "capture selected lab Traefik probe identity from its existing network namespace")
	prepareFixtures := flags.String("prepare-fixtures", "", "prepare private lab response files and register current fixture identities")
	registerRequests := flags.String("register-requests", "", "register empty lab request directories from an active workspace; requires --out")
	mediationInput := flags.String("mediation", "", "administrator-owned lab authorization and request-directory binding JSON")
	previousInput := flags.String("previous", "", "publication.json for the lab session actually applied")
	withdraw := flags.Bool("withdraw", false, "plan withdrawal only; requires --previous")
	out := flags.String("out", "", "new output directory (must not exist)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *probeCapture != "" {
		if *out == "" || len(flags.Args()) > 0 || *input != "" || *captureInput != "" || *registerRequests != "" || *previousInput != "" || *withdraw || *mediationInput != "" || *prepareFixtures != "" {
			return fmt.Errorf("use --capture-probe lab-config.json --out new-directory alone")
		}
		return captureLabProbe(*probeCapture, *out)
	}
	if *prepareFixtures != "" {
		if *out == "" || len(flags.Args()) > 0 || *input != "" || *captureInput != "" || *registerRequests != "" || *previousInput != "" || *withdraw || *mediationInput != "" {
			return fmt.Errorf("use --prepare-fixtures lab-config.json --out new-absolute-directory alone")
		}
		return prepareLabFixtures(*prepareFixtures, *out)
	}
	if *registerRequests != "" {
		if *out == "" || len(flags.Args()) > 0 || *input != "" || *captureInput != "" || *previousInput != "" || *withdraw || *mediationInput != "" {
			return fmt.Errorf("use --register-requests absolute-workspace --out new-absolute-directory alone")
		}
		return registerLabRequests(*registerRequests, *out)
	}
	if *out == "" || len(flags.Args()) > 0 || (*input != "" && *captureInput != "") || (*withdraw && (*input != "" || *captureInput != "" || *previousInput == "")) || (!*withdraw && *input == "" && *captureInput == "") {
		return fmt.Errorf("use --input observation.json or --capture config.json, optionally --previous publication.json; or --withdraw --previous publication.json; always provide --out new-directory")
	}
	// Check the destination before any capture; writeArtifacts still creates it
	// exclusively to protect against races and accidental reuse.
	if _, err := os.Lstat(*out); !os.IsNotExist(err) {
		return fmt.Errorf("output directory must not exist and its parent must be accessible")
	}
	var previous *publicationRecord
	if *previousInput != "" {
		previous = &publicationRecord{}
		if err := readInput(*previousInput, previous); err != nil {
			return err
		}
		if previous.Schema != publicationSchema {
			return fmt.Errorf("unsupported previous publication schema")
		}
		if err := validateMediatedRecord(previous); err != nil {
			return err
		}
		if _, err := generate(previous.Observation); err != nil {
			return fmt.Errorf("invalid previous publication")
		}
	}
	var mediator *labMediator
	if *mediationInput != "" {
		var err error
		mediator, err = loadMediation(*mediationInput, previous, *withdraw)
		if err != nil {
			return err
		}
	}
	var current *observation
	var evidence *captureEvidence
	captureFailed := false
	if *input != "" {
		current = &observation{}
		if err := readInput(*input, current); err != nil {
			return err
		}
	} else if *captureInput != "" {
		var config captureConfig
		if err := readInput(*captureInput, &config); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		observed, captured, err := capture(ctx, config)
		cancel()
		if err != nil {
			if previous == nil {
				return err
			}
			captureFailed = true
		} else {
			current = &observed
			evidence = &captured
		}
	}
	var mediated *computeingress.Publication
	mediationFailed := false
	if mediator != nil && current != nil {
		var err error
		mediated, err = mediator.authorize(current)
		if err != nil {
			if previous == nil {
				return err
			}
			current = nil
			mediationFailed = true
		}
	}
	authorizationChanged := false
	if err := mediator.revalidateWorkspace(); err != nil {
		if previous == nil {
			return err
		}
		current = nil
		authorizationChanged = true
	}
	plan, record, result, err := planLifecycle(previous, current, evidence)
	if err != nil {
		return err
	}
	if record != nil {
		record.Mediation = mediated
		if mediator != nil && mediator.snapshot != nil {
			record.AuthorizationEpoch = mediator.snapshot.Epoch
		}
	}
	if mediationFailed {
		plan.Reason = "observed_instance_not_authorized"
	}
	if captureFailed {
		plan.Reason = "capture_unavailable_or_unstable"
	}
	if authorizationChanged {
		plan.Reason = "active_authorization_changed"
	}
	files := map[string][]byte{}
	addJSON := func(name string, v any) error {
		body, e := jsonArtifact(v)
		if e == nil {
			files[name] = body
		}
		return e
	}
	if err = addJSON("lifecycle.json", plan); err != nil {
		return err
	}
	if record != nil {
		if err = addJSON("publication.json", record); err != nil {
			return err
		}
		if err = addJSON("observation.json", record.Observation); err != nil {
			return err
		}
		if evidence != nil {
			if err = addJSON("capture-evidence.json", evidence); err != nil {
				return err
			}
		}
	}
	if result != nil {
		files["firewall.nft"] = []byte(result.Firewall)
		files["traefik.env"] = []byte(result.RouteEnv)
		if err = addJSON("operations.json", result); err != nil {
			return err
		}
		if plan.Action == "replace" {
			files["firewall-replace.nft"] = []byte("delete table bridge anas_incus_lab_l2\ndelete table inet anas_incus_lab\n" + result.Firewall)
		}
	}
	return writeArtifacts(*out, files)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
