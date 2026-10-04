package computeingress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Route files are the HTTP publication mediator's only output (INCUS-R-143):
// one Traefik file-provider document per published host, in a subdirectory of
// Traefik's dynamic directory that nothing else writes. The document has the
// same router/service shape the Traefik Module renders for
// ANAS_TRAEFIK_ROUTE__* declarations; Traefik itself gains no mechanism
// (INCUS-R-054).

const (
	// RouteDirectory is the subdirectory of Traefik's dynamic directory.
	RouteDirectory = "compute-http"
	// RouteReloadFile sits in the watched top-level directory. Traefik
	// watches only that directory, not its subdirectories, so rewriting this
	// file after a change is what makes it reread the routes.
	RouteReloadFile = "compute-http.reload"
)

var routeFileName = regexp.MustCompile(`^([a-z][a-z0-9_]{0,62})\.([a-z][a-z0-9_]{0,62})\.([0-9a-f]{16})\.([0-9a-f]{16})\.yml$`)

// GrantDigest identifies what a lease may publish, independent of which
// deployment froze it: an apply that leaves the declaration alone keeps its
// routes, and one that changes or removes it invalidates them (INCUS-R-147).
func GrantDigest(a *Authorization) string {
	copy := a.Clone()
	copy.Deployment = ""
	body, _ := json.Marshal(copy)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])[:16]
}

func hostDigest(host string) string {
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:])[:16]
}

// RouteFileName names a lease's route for one host.
func RouteFileName(a *Authorization, host string) string {
	return a.Consumer + "." + a.Resource + "." + GrantDigest(a) + "." + hostDigest(host) + ".yml"
}

// ParseRouteFileName returns the lease and grant digest a route file belongs
// to; any other name is not a route file.
func ParseRouteFileName(name string) (consumer, resource, grant string, ok bool) {
	match := routeFileName.FindStringSubmatch(name)
	if match == nil {
		return "", "", "", false
	}
	return match[1], match[2], match[3], true
}

// RenderRoute renders the Traefik document for one publication. Every value
// comes from the frozen authorization except the backend address and port,
// which the mediator has already checked against the lease (INCUS-R-144).
func RenderRoute(a *Authorization, host, address string, port uint16) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if !a.ClaimsHost(host) {
		return nil, fmt.Errorf("HTTP host is outside the lease namespace")
	}
	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is4() || port == 0 {
		return nil, fmt.Errorf("HTTP backend must be an IPv4 address and port")
	}
	name := "anas-compute-" + hostDigest(host)
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by the ANAS compute HTTP publication mediator for lease %s.%s.\n", a.Consumer, a.Resource)
	b.WriteString("http:\n  routers:\n")
	fmt.Fprintf(&b, "    %s:\n", name)
	fmt.Fprintf(&b, "      rule: %s\n", strconv.Quote("Host(`"+host+"`)"))
	fmt.Fprintf(&b, "      service: %s\n", name)
	b.WriteString("      entryPoints:\n        - \"https\"\n")
	if a.ForwardAuth != nil {
		fmt.Fprintf(&b, "      middlewares:\n        - %s\n", strconv.Quote(a.ForwardAuth.Middleware))
	}
	b.WriteString("      tls: {}\n")
	b.WriteString("  services:\n")
	fmt.Fprintf(&b, "    %s:\n      loadBalancer:\n        servers:\n", name)
	fmt.Fprintf(&b, "          - url: %s\n", strconv.Quote("http://"+addr.String()+":"+strconv.Itoa(int(port))))
	return []byte(b.String()), nil
}

// ValidateBackend checks a request's backend against the lease's IPv4 subnet
// and gateway: inside the subnet, and not its network, gateway or broadcast
// address (INCUS-R-144).
func ValidateBackend(address, subnet, gateway string) error {
	addr, err := netip.ParseAddr(address)
	prefix, prefixErr := netip.ParsePrefix(subnet)
	gw, gatewayErr := netip.ParseAddr(gateway)
	if err != nil || prefixErr != nil || gatewayErr != nil || !addr.Is4() || !prefix.Addr().Is4() {
		return fmt.Errorf("HTTP backend or lease subnet is not IPv4")
	}
	prefix = prefix.Masked()
	if !prefix.Contains(addr) || addr == prefix.Addr() || addr == gw || addr == lastIPv4(prefix) {
		return fmt.Errorf("HTTP backend is not an instance address in the lease subnet")
	}
	return nil
}

func lastIPv4(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	host := 32 - p.Bits()
	value := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if host >= 32 {
		value = 0xffffffff
	} else {
		value |= (uint32(1) << host) - 1
	}
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}
