package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

type DirectMode string

const (
	DirectModeLAN      DirectMode = "lan"
	DirectModeLoopback DirectMode = "loopback"
)

// DirectHostPolicy validates the browser Host against the address reached by
// this connection. A wildcard listener is never treated as permission to send
// an arbitrary Host header.
type DirectHostPolicy struct {
	Mode            DirectMode
	AllowedDNSHosts []string
}

// ExactDNSHostPolicy is used behind Traefik, where the public Host names the
// gateway rather than the private listener address. IP Hosts and unlisted DNS
// aliases are always rejected.
type ExactDNSHostPolicy struct {
	AllowedDNSHosts []string
}

func (policy ExactDNSHostPolicy) Allowed(r *http.Request) bool {
	host, _, isIP, ok := splitRequestHost(r.Host)
	if !ok || isIP {
		return false
	}
	canonical := canonicalDNSHost(host)
	for _, allowed := range policy.AllowedDNSHosts {
		if canonical != "" && canonical == canonicalDNSHost(allowed) {
			return true
		}
	}
	return false
}

func (policy DirectHostPolicy) Allowed(r *http.Request) bool {
	requestHost, requestPort, isIP, ok := splitRequestHost(r.Host)
	if !ok {
		return false
	}
	localIP, localPort, localOK := requestLocalAddress(r)
	if requestPort != "" && (!localOK || requestPort != localPort) {
		return false
	}
	if isIP {
		requestIP := net.ParseIP(requestHost)
		if requestIP == nil {
			return false
		}
		if policy.Mode == DirectModeLoopback {
			return requestIP.IsLoopback() && (!localOK || localIP.IsLoopback())
		}
		if policy.Mode != DirectModeLAN || !localOK || localIP.IsUnspecified() {
			return false
		}
		return requestIP.Equal(localIP)
	}
	if !localOK || policy.Mode == DirectModeLoopback && !localIP.IsLoopback() {
		return false
	}
	canonical := canonicalDNSHost(requestHost)
	for _, allowed := range policy.AllowedDNSHosts {
		if canonical != "" && canonical == canonicalDNSHost(allowed) {
			return true
		}
	}
	return false
}

func splitRequestHost(value string) (host, port string, isIP, ok bool) {
	if value == "" {
		return "", "", false, false
	}
	if _, parsed := parseHostIP(value); parsed {
		return strings.Split(value, "%")[0], "", true, true
	}
	host = value
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		host = value[1 : len(value)-1]
		if _, parsed := parseHostIP(host); parsed {
			return strings.Split(host, "%")[0], "", true, true
		}
		return "", "", false, false
	}
	if strings.Contains(value, ":") {
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil || !validNumericPort(port) {
			return "", "", false, false
		}
	}
	if _, parsed := parseHostIP(host); parsed {
		return strings.Split(host, "%")[0], port, true, true
	}
	if canonicalDNSHost(host) == "" {
		return "", "", false, false
	}
	return host, port, false, true
}

func canonicalDNSHost(value string) string {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil {
		return ""
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, char := range label {
			if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return ""
		}
	}
	return value
}

func requestLocalAddress(r *http.Request) (net.IP, string, bool) {
	address, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok || address == nil {
		return nil, "", false
	}
	host, port, err := net.SplitHostPort(address.String())
	if err != nil || !validNumericPort(port) {
		return nil, "", false
	}
	if before, _, found := strings.Cut(host, "%"); found {
		host = before
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() {
		return nil, "", false
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 0 || value > 65535 {
		return nil, "", false
	}
	return ip, port, true
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" || strings.Contains(origin, ",") {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}
