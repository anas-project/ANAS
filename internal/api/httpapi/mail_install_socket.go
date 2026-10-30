package httpapi

import (
	"net"
	"net/http"
	"strconv"
)

// MailInstallSocketHostPolicy validates the configured HTTPS origin without
// assuming that a Unix socket has a TCP local address. Only loopback IPs or
// explicitly allowed DNS hosts are usable, with the configured console port.
type MailInstallSocketHostPolicy struct {
	Port            int
	AllowedDNSHosts []string
}

func (p MailInstallSocketHostPolicy) Allowed(r *http.Request) bool {
	host, port, isIP, ok := splitRequestHost(r.Host)
	if !ok || p.Port < 1 || p.Port > 65535 || port != "" && port != strconv.Itoa(p.Port) {
		return false
	}
	if isIP {
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	canonical := canonicalDNSHost(host)
	for _, allowed := range p.AllowedDNSHosts {
		if canonical != "" && canonical == canonicalDNSHost(allowed) {
			return true
		}
	}
	return false
}

// MailInstallOnly restricts a physically separate listener to the existing
// mail installation and outbound configuration routes. The wrapped handler still enforces TLS, console
// state, Host policy, service grants, request validation and audit. It must not
// be used as a substitute for a protected listener or for authentication.
func MailInstallOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/api/v1/service/workspaces/{ws}/applications/{application}/"
		params, ok := matchRoutePattern(prefix+"mail-ingress-install", r.URL.Path)
		if !ok {
			params, ok = matchRoutePattern(prefix+"mail-outbound-configure", r.URL.Path)
		}
		if next == nil || r.Method != http.MethodPost || r.URL.RawPath != "" || !ok || params["application"] != "mail" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
