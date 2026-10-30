package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
)

// A probe is an explicit, bounded observation from this mail server. It is
// not a public-internet readiness check and never sends an SMTP transaction.
var vpsSMTPProbeSlot = make(chan struct{}, 1)

var probeExcluded = func() []netip.Prefix {
	values := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"}
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}()

func publicProbeIP(ip net.IP) (netip.Addr, bool) {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return address, false
	}
	for _, prefix := range probeExcluded {
		if prefix.Contains(address) {
			return address, false
		}
	}
	return address, true
}

type smtpProbeAddress struct {
	Address string `json:"address"`
	Status  string `json:"status"`
	TLS     string `json:"tls,omitempty"`
}
type smtpProbeFamily struct {
	Status    string             `json:"status"`
	Addresses []smtpProbeAddress `json:"addresses"`
}

func smtpProbe(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), address netip.Addr, host string, roots *x509.CertPool) (string, string) {
	return smtpProbePort(ctx, dial, address, host, 25, "starttls", roots)
}

func smtpProbePort(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), address netip.Addr, host string, port int, mode string, roots *x509.CertPool) (string, string) {
	conn, err := dial(ctx, "tcp", net.JoinHostPort(address.String(), strconv.Itoa(port)))
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return "refused", ""
		}
		return "unknown", ""
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, ok := ctx.Deadline()
	if !ok || conn.SetDeadline(deadline) != nil {
		return "unknown", ""
	}
	wire := conn
	if mode == "tls" {
		secure := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: roots})
		if err = secure.HandshakeContext(ctx); err != nil {
			var certificate *tls.CertificateVerificationError
			if errors.As(err, &certificate) {
				return "unknown", "invalid-certificate"
			}
			return "unknown", "unknown"
		}
		wire = secure
	}
	// Limit even malformed or multiline greetings, and never expose banner text.
	buffer := bufio.NewReader(io.LimitReader(wire, 8192))
	reader := textproto.NewReader(buffer)
	code, _, err := reader.ReadResponse(0)
	if err != nil {
		return "unknown", ""
	}
	if code != 220 {
		return "unexpected-banner", ""
	}
	if mode == "tls" {
		return "smtp-greeting", "verified"
	}
	// Send no sender, recipient or message. A greeting remains an observation
	// even when a later TLS exchange is incomplete or rejected.
	if host == "" || buffer.Buffered() != 0 {
		return "smtp-greeting", "unknown"
	}
	if _, err = io.WriteString(conn, "EHLO anas.invalid\r\n"); err != nil {
		return "smtp-greeting", "unknown"
	}
	_, capabilities, err := reader.ReadResponse(250)
	if err != nil {
		return "smtp-greeting", "unknown"
	}
	advertised := false
	for _, line := range strings.Split(capabilities, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.EqualFold(fields[0], "STARTTLS") {
			advertised = true
		}
	}
	if !advertised {
		return "smtp-greeting", "not-advertised"
	}
	if buffer.Buffered() != 0 {
		return "smtp-greeting", "unknown"
	}
	if _, err = io.WriteString(conn, "STARTTLS\r\n"); err != nil {
		return "smtp-greeting", "unknown"
	}
	if _, _, err = reader.ReadResponse(220); err != nil || buffer.Buffered() != 0 {
		return "smtp-greeting", "unknown"
	}
	tlsConn := tls.Client(conn, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: roots})
	if err = tlsConn.HandshakeContext(ctx); err != nil {
		var certificate *tls.CertificateVerificationError
		if errors.As(err, &certificate) {
			return "smtp-greeting", "invalid-certificate"
		}
		return "smtp-greeting", "unknown"
	}
	return "smtp-greeting", "verified"
}

func (s *loginHandler) setupVPSSMTPProbe(w http.ResponseWriter, r *http.Request) {
	s.setupSMTPProbe(w, r, false)
}

func (s *loginHandler) setupOutboundSMTPProbe(w http.ResponseWriter, r *http.Request) {
	s.setupSMTPProbe(w, r, true)
}

func (s *loginHandler) setupSMTPProbe(w http.ResponseWriter, r *http.Request, outbound bool) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != s.origin.String() {
		loginError(w, 403, "invalid-origin")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		loginError(w, 400, "invalid-request")
		return
	}
	var input struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		TLS  string `json:"tls"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	fields := "host"
	if outbound {
		fields = "host port tls"
	}
	if err != nil || !uniqueInstallJSON(raw, fields) || json.Unmarshal(raw, &input) != nil || outbound && (input.Port < 1 || input.Port > 65535 || input.TLS != "tls" && input.TLS != "starttls") {
		loginError(w, 400, "invalid-request")
		return
	}
	if !outbound {
		input.Port, input.TLS = 25, "starttls"
	}
	host, err := directorymail.Domain(input.Host, "")
	if err != nil || host != input.Host || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		loginError(w, 400, "invalid-host")
		return
	}
	select {
	case vpsSMTPProbeSlot <- struct{}{}:
		defer func() { <-vpsSMTPProbeSlot }()
	default:
		loginError(w, 429, "probe-busy")
		return
	}
	lookup := s.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIP
	}
	dial := s.probeDial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	type familyResult struct {
		family string
		value  smtpProbeFamily
	}
	results := make(chan familyResult, 2)
	for _, family := range []string{"ip4", "ip6"} {
		go func(family string) {
			value := smtpProbeFamily{Status: "unknown", Addresses: []smtpProbeAddress{}}
			ips, e := lookup(ctx, family, host)
			var dnsErr *net.DNSError
			if errors.As(e, &dnsErr) && dnsErr.IsNotFound && !dnsErr.IsTimeout && !dnsErr.IsTemporary {
				value.Status = "not-found"
			} else if e == nil && len(ips) > 0 && len(ips) <= 8 {
				seen := map[netip.Addr]bool{}
				for _, ip := range ips {
					address, allowed := publicProbeIP(ip)
					if !address.IsValid() || (family == "ip4") != address.Is4() {
						value.Addresses = []smtpProbeAddress{}
						results <- familyResult{family, value}
						return
					}
					if !seen[address] {
						seen[address] = true
						status := "non-public"
						if allowed {
							status = "pending"
						}
						value.Addresses = append(value.Addresses, smtpProbeAddress{Address: address.String(), Status: status})
					}
				}
				sort.Slice(value.Addresses, func(i, j int) bool { return value.Addresses[i].Address < value.Addresses[j].Address })
				out := make(chan struct {
					index  int
					status string
					tls    string
				}, len(value.Addresses))
				pending := 0
				for i, item := range value.Addresses {
					if item.Status != "pending" {
						continue
					}
					pending++
					go func(i int, address string) {
						probeCtx, stopProbe := context.WithTimeout(ctx, 3*time.Second)
						defer stopProbe()
						status, tlsStatus := smtpProbePort(probeCtx, dial, netip.MustParseAddr(address), host, input.Port, input.TLS, nil)
						out <- struct {
							index  int
							status string
							tls    string
						}{i, status, tlsStatus}
					}(i, item.Address)
				}
				for pending > 0 {
					select {
					case response := <-out:
						value.Addresses[response.index].Status = response.status
						value.Addresses[response.index].TLS = response.tls
						pending--
					case <-ctx.Done():
						pending = 0
					}
				}
				for i := range value.Addresses {
					if value.Addresses[i].Status == "pending" {
						value.Addresses[i].Status = "unknown"
					}
				}
				value.Status = "observed"
			}
			results <- familyResult{family, value}
		}(family)
	}
	families := map[string]smtpProbeFamily{"ip4": {Status: "unknown", Addresses: []smtpProbeAddress{}}, "ip6": {Status: "unknown", Addresses: []smtpProbeAddress{}}}
	for pending := 2; pending > 0; {
		select {
		case value := <-results:
			families[value.family] = value.value
			pending--
		case <-ctx.Done():
			pending = 0
		}
	}
	current, currentGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if current.Anchor != person.Anchor || currentGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	response := map[string]any{"host": host, "ipv4": families["ip4"], "ipv6": families["ip6"], "checked_at": time.Now().UTC().Format(time.RFC3339)}
	if outbound {
		response["port"], response["tls"] = input.Port, input.TLS
	}
	reply(w, 200, response)
}
