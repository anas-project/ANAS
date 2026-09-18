package computeingressruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// TraefikProbeIdentity is supplied by a trusted launcher after independently
// resolving the installed Traefik container/process. PID is in the probe's
// visible procfs. Cookie is sampled from a socket in that verified namespace,
// never learned from the first socket this probe happens to create. The probe
// must already run there; it does not setns, run a shell, or need NET_ADMIN.
type TraefikProbeIdentity struct {
	PID             int    `json:"pid"`
	StartTimeTicks  uint64 `json:"start_time_ticks"`
	BootID          string `json:"boot_id"`
	NamespaceDevice uint64 `json:"namespace_device"`
	NamespaceInode  uint64 `json:"namespace_inode"`
	NamespaceCookie uint64 `json:"namespace_cookie"`
	SourceIPv4      string `json:"source_ipv4"`
}

// FixtureHTTPExpectation is administrator-registered prototype input. The
// fixture producer must use a distinct response per instance incarnation and
// port, record it before probing, and keep it outside consumer request dirs.
// A hash first learned from the backend is not an identity expectation.
// Target binds the entire reservation, including epoch/UUID/incarnation/IP/MAC.
type FixtureHTTPExpectation struct {
	Target     PublicationTarget `json:"target"`
	Path       string            `json:"path"`
	BodyBytes  int               `json:"body_bytes"`
	BodySHA256 string            `json:"body_sha256"`
}

// FixtureHTTPProbe deliberately accepts only .example.test fixtures. This
// implements the prototype's independent backend probe, not a universal
// production application health/identity contract or public HTTPS/auth test.
type FixtureHTTPProbe struct {
	identity    TraefikProbeIdentity
	authority   AuthorizationSource
	observer    Observer
	expectation func(context.Context, PublicationTarget) (FixtureHTTPExpectation, error)
}

var _ BackendProbe = (*FixtureHTTPProbe)(nil)
var fixturePath = regexp.MustCompile(`^/[A-Za-z0-9_./-]*$`)
var probeBootID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func NewFixtureHTTPProbe(identity TraefikProbeIdentity, fixtures []FixtureHTTPExpectation, authority AuthorizationSource, observer Observer) (*FixtureHTTPProbe, error) {
	if validateProbeIdentity(identity) != nil || authority == nil || observer == nil || len(fixtures) == 0 || len(fixtures) > 1024 {
		return nil, fmt.Errorf("HTTP fixture probe requires an explicit installed namespace, authority and bounded expectations")
	}
	registered := make(map[PublicationTarget]FixtureHTTPExpectation)
	responses := make(map[string]bool)
	for _, fixture := range fixtures {
		if validateTarget(fixture.Target.Epoch, fixture.Target) != nil || validateFixtureResponse(fixture) != nil {
			return nil, fmt.Errorf("invalid HTTP fixture identity expectation")
		}
		if _, exists := registered[fixture.Target]; exists {
			return nil, fmt.Errorf("duplicate HTTP fixture target")
		}
		if responses[fixture.BodySHA256] {
			return nil, fmt.Errorf("HTTP fixtures require distinct registered identity responses")
		}
		responses[fixture.BodySHA256] = true
		registered[fixture.Target] = fixture
	}
	return &FixtureHTTPProbe{identity: identity, authority: authority, observer: observer,
		expectation: func(ctx context.Context, target PublicationTarget) (FixtureHTTPExpectation, error) {
			fixture, ok := registered[target]
			if !ok {
				return FixtureHTTPExpectation{}, fmt.Errorf("HTTP target has no pre-registered fixture expectation")
			}
			return fixture, ctx.Err()
		},
	}, nil
}

func validateProbeIdentity(identity TraefikProbeIdentity) error {
	source, err := netip.ParseAddr(identity.SourceIPv4)
	if err != nil || !source.Is4() || !source.IsPrivate() || source.String() != identity.SourceIPv4 || identity.PID <= 0 || identity.StartTimeTicks == 0 || identity.NamespaceDevice == 0 || identity.NamespaceInode == 0 || identity.NamespaceCookie == 0 || !probeBootID.MatchString(identity.BootID) {
		return fmt.Errorf("invalid installed HTTP probe identity")
	}
	return nil
}

func validateFixtureResponse(fixture FixtureHTTPExpectation) error {
	if !strings.HasSuffix(fixture.Target.Publication.Host, ".example.test") || len(fixture.Path) > 256 || !fixturePath.MatchString(fixture.Path) || strings.HasPrefix(fixture.Path, "//") || fixture.BodyBytes < 16 || fixture.BodyBytes > 64<<10 || !validEpoch(fixture.BodySHA256) {
		return fmt.Errorf("invalid HTTP fixture response expectation")
	}
	for _, segment := range strings.Split(fixture.Path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("HTTP fixture path cannot contain traversal segments")
		}
	}
	return nil
}

func (p *FixtureHTTPProbe) validate(ctx context.Context, target PublicationTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.authority.ValidateAuthorization(ctx, target); err != nil {
		return fmt.Errorf("HTTP fixture authorization is no longer current")
	}
	if err := p.observer.ValidateTarget(ctx, target); err != nil {
		return fmt.Errorf("HTTP fixture instance identity is no longer current")
	}
	if err := p.authority.ValidateAuthorization(ctx, target); err != nil {
		return fmt.Errorf("HTTP fixture authorization changed during observation")
	}
	return checkProbeNamespace(ctx, p.identity)
}

func (p *FixtureHTTPProbe) ProbeHTTP(ctx context.Context, target PublicationTarget) error {
	if p == nil || p.authority == nil || p.observer == nil || p.expectation == nil || validateTarget(target.Epoch, target) != nil {
		return fmt.Errorf("invalid HTTP fixture probe target")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := p.validate(probeCtx, target); err != nil {
		return err
	}
	fixture, err := p.expectation(probeCtx, target)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(target.Publication.GuestIP, strconv.Itoa(int(target.Publication.GuestPort)))
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1, LocalAddr: &net.TCPAddr{IP: net.ParseIP(p.identity.SourceIPv4)},
		ControlContext: func(ctx context.Context, network, addr string, connection syscall.RawConn) error {
			if network != "tcp4" || addr != address {
				return fmt.Errorf("HTTP fixture dial escaped its authorized IPv4 tuple")
			}
			if err := checkProbeNamespace(ctx, p.identity); err != nil {
				return err
			}
			// Test the socket itself, not merely the goroutine's current OS
			// thread: Go may create sockets on different runtime threads.
			return checkProbeSocket(connection, p.identity.NamespaceCookie)
		},
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
		ResponseHeaderTimeout: 3 * time.Second, MaxResponseHeaderBytes: 8 << 10,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if network != "tcp" || addr != address {
				return nil, fmt.Errorf("HTTP fixture transport escaped its authorized target")
			}
			connection, err := dialer.DialContext(ctx, "tcp4", address)
			if err != nil {
				return nil, fmt.Errorf("HTTP fixture backend connection failed")
			}
			local, localOK := connection.LocalAddr().(*net.TCPAddr)
			remote, remoteOK := connection.RemoteAddr().(*net.TCPAddr)
			if !localOK || !remoteOK || local.IP.String() != p.identity.SourceIPv4 || remote.IP.String() != target.Publication.GuestIP || remote.Port != int(target.Publication.GuestPort) || checkProbeNamespace(ctx, p.identity) != nil {
				connection.Close()
				return nil, fmt.Errorf("HTTP fixture connection identity changed")
			}
			return connection, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+address+fixture.Path, nil)
	if err != nil {
		return fmt.Errorf("cannot construct constrained HTTP fixture request")
	}
	request.Host = target.Publication.Host
	request.Close = true
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Cache-Control", "no-cache, no-store")
	// No identity headers, authentication, cookies or naming key are sent.
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("HTTP fixture request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > int64(fixture.BodyBytes) || response.Header.Get("Content-Encoding") != "" {
		return fmt.Errorf("HTTP fixture did not return the expected bounded unencoded success")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(fixture.BodyBytes)+1))
	if err != nil || len(body) != fixture.BodyBytes {
		return fmt.Errorf("HTTP fixture response is incomplete or has unexpected length")
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != fixture.BodySHA256 {
		return fmt.Errorf("HTTP fixture response does not match its registered identity")
	}
	// A correct body is insufficient if authorization, allocation, process or
	// namespace changed during the exchange. Address retention remains the
	// independently enforced HostActions responsibility throughout this step.
	if err := p.validate(probeCtx, target); err != nil {
		return err
	}
	current, err := p.expectation(probeCtx, target)
	if err != nil || current != fixture {
		return fmt.Errorf("HTTP fixture registration changed during probe")
	}
	return probeCtx.Err()
}
