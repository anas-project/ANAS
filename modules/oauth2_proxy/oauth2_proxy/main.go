package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const oauth2ProxyBinary = "/bin/oauth2-proxy"

const healthCheckFlag = "--health-check="

const (
	publicBridgeAddress   = "0.0.0.0:4180"
	identityBridgeAddress = "0.0.0.0:4182"
	oauth2ProxyURL        = "http://127.0.0.1:4181"
)

func main() {
	// The upstream image is distroless: no shell, no curl, nothing a
	// CMD-SHELL healthcheck could run. This binary is already the entrypoint,
	// so it is also the only thing available to probe with. The URL comes from
	// the caller rather than being hardcoded here, because the listen address
	// is set by a flag in docker-compose.yml and a second copy of the port in
	// this file would be free to drift from it.
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], healthCheckFlag) {
		if err := probe(strings.TrimPrefix(os.Args[1], healthCheckFlag), 3*time.Second); err != nil {
			fatalf("%v", err)
		}
		return
	}

	host := strings.TrimSpace(os.Getenv("TRAEFIK_HOSTNAME"))
	if host == "" {
		fatalf("TRAEFIK_HOSTNAME is required")
	}

	peer, err := resolveProxyPeer(context.Background(), host, net.DefaultResolver, 30, 2*time.Second)
	if err != nil {
		fatalf("%v", err)
	}
	trusted, err := trustedProxyArgs(peer, os.Getenv("TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS"))
	if err != nil {
		fatalf("%v", err)
	}

	args := append([]string{}, os.Args[1:]...)
	args = append(args, trusted...)
	if err := runProxyBridges(args); err != nil {
		fatalf("%v", err)
	}
}

func runProxyBridges(args []string) error {
	issuer := strings.TrimSpace(os.Getenv("OAUTH2_PROXY_OIDC_ISSUER_URL"))
	group := strings.TrimSpace(os.Getenv("ANAS_PROXY_PLATFORM_ADMIN_GROUP"))
	if issuer == "" || group == "" {
		return fmt.Errorf("OAUTH2_PROXY_OIDC_ISSUER_URL and ANAS_PROXY_PLATFORM_ADMIN_GROUP are required")
	}
	target, _ := url.Parse(oauth2ProxyURL)
	publicProxy := newOAuth2ReverseProxy(target, "", "")
	identityProxy := newOAuth2ReverseProxy(target, issuer, group)
	publicServer := &http.Server{Addr: publicBridgeAddress, Handler: publicProxy, ReadHeaderTimeout: 5 * time.Second}
	identityServer := &http.Server{Addr: identityBridgeAddress, Handler: identityProxy, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := exec.CommandContext(ctx, oauth2ProxyBinary, args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start oauth2-proxy: %w", err)
	}
	results := make(chan error, 3)
	go func() { results <- command.Wait() }()
	go func() { results <- publicServer.ListenAndServe() }()
	go func() { results <- identityServer.ListenAndServe() }()

	var serviceErr error
	select {
	case serviceErr = <-results:
		if serviceErr == nil {
			serviceErr = fmt.Errorf("oauth2-proxy component stopped unexpectedly")
		}
	case <-ctx.Done():
	}
	stop()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = publicServer.Shutdown(shutdownContext)
	_ = identityServer.Shutdown(shutdownContext)
	if command.Process != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
	}
	if ctx.Err() != nil && serviceErr == nil {
		return nil
	}
	return serviceErr
}

var identityResponseHeaders = []string{
	"X-Anas-Identity-Issuer", "X-Anas-Identity-Subject", "X-Anas-Identity-Role",
	"X-Anas-Identity-Group", "X-Anas-Identity-Auth-Time", "X-Anas-Identity-Expires-At",
	"X-Anas-Identity-Assertion",
}

func newOAuth2ReverseProxy(target *url.URL, expectedIssuer, expectedGroup string) *httputil.ReverseProxy {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		for _, name := range identityResponseHeaders {
			request.Header.Del(name)
		}
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		for _, name := range identityResponseHeaders {
			response.Header.Del(name)
		}
		authorizations := response.Header.Values("Authorization")
		response.Header.Del("Authorization")
		if expectedIssuer == "" || response.StatusCode < 200 || response.StatusCode > 299 {
			return nil
		}
		if len(authorizations) != 1 {
			return fmt.Errorf("oauth2-proxy returned an ambiguous bearer ID token")
		}
		claims, assertion, err := verifiedIdentityClaims(authorizations[0], expectedIssuer, expectedGroup)
		if err != nil {
			return err
		}
		response.Header.Set("X-Anas-Identity-Issuer", claims.Issuer)
		response.Header.Set("X-Anas-Identity-Subject", claims.Subject)
		response.Header.Set("X-Anas-Identity-Role", "platform_admin")
		response.Header.Set("X-Anas-Identity-Group", expectedGroup)
		response.Header.Set("X-Anas-Identity-Auth-Time", strconv.FormatInt(claims.AuthTime, 10))
		response.Header.Set("X-Anas-Identity-Expires-At", strconv.FormatInt(claims.ExpiresAt, 10))
		response.Header.Set("X-Anas-Identity-Assertion", assertion)
		return nil
	}
	return proxy
}

type identityClaims struct {
	Issuer    string
	Subject   string
	AuthTime  int64
	ExpiresAt int64
}

func verifiedIdentityClaims(authorization, expectedIssuer, expectedGroup string) (identityClaims, string, error) {
	assertion, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || assertion == "" || strings.ContainsAny(assertion, " \t\r\n,") || len(assertion) > 16384 {
		return identityClaims{}, "", fmt.Errorf("oauth2-proxy did not return one bearer ID token")
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return identityClaims{}, "", fmt.Errorf("oauth2-proxy ID token is malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return identityClaims{}, "", fmt.Errorf("decode oauth2-proxy ID token claims: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return identityClaims{}, "", fmt.Errorf("decode oauth2-proxy ID token claims: %w", err)
	}
	issuer, _ := document["iss"].(string)
	subject, _ := document["sub"].(string)
	authTime, authErr := claimUnixSeconds(document["auth_time"])
	expiresAt, expiryErr := claimUnixSeconds(document["exp"])
	if issuer != expectedIssuer || subject == "" || len(subject) > 512 || strings.TrimSpace(subject) != subject || strings.ContainsAny(subject, "\r\n\x00,") || authErr != nil || expiryErr != nil || expiresAt <= authTime {
		return identityClaims{}, "", fmt.Errorf("oauth2-proxy ID token lacks the required stable identity or timestamps")
	}
	if !claimContainsGroup(document["groups"], expectedGroup) && !claimContainsGroup(document["roles"], expectedGroup) {
		return identityClaims{}, "", fmt.Errorf("oauth2-proxy ID token lacks the resolved platform_admin group")
	}
	return identityClaims{Issuer: issuer, Subject: subject, AuthTime: authTime, ExpiresAt: expiresAt}, assertion, nil
}

func claimUnixSeconds(value any) (int64, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("claim is not a JSON number")
	}
	seconds, err := number.Int64()
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("claim is not positive Unix seconds")
	}
	return seconds, nil
}

func claimContainsGroup(value any, expected string) bool {
	switch groups := value.(type) {
	case string:
		return groups == expected
	case []any:
		for _, value := range groups {
			if group, ok := value.(string); ok && group == expected {
				return true
			}
		}
	}
	return false
}

type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func resolveProxyPeer(ctx context.Context, host string, resolver ipResolver, attempts int, delay time.Duration) (netip.Addr, error) {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		addresses, err := resolver.LookupNetIP(ctx, "ip", host)
		if err == nil {
			for _, address := range addresses {
				if address.Is4() {
					return address.Unmap(), nil
				}
			}
			if len(addresses) > 0 {
				return addresses[0].Unmap(), nil
			}
			err = fmt.Errorf("no addresses returned")
		}
		lastErr = err
		if attempt+1 < attempts {
			time.Sleep(delay)
		}
	}
	return netip.Addr{}, fmt.Errorf("cannot resolve Traefik host %q: %w", host, lastErr)
}

func trustedProxyArgs(peer netip.Addr, upstreams string) ([]string, error) {
	values := []string{netip.PrefixFrom(peer, peer.BitLen()).String()}
	for _, raw := range strings.Split(upstreams, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if address, err := netip.ParseAddr(value); err == nil {
			value = netip.PrefixFrom(address.Unmap(), address.Unmap().BitLen()).String()
		} else if prefix, err := netip.ParsePrefix(value); err == nil {
			value = prefix.Masked().String()
		} else {
			return nil, fmt.Errorf("invalid trusted upstream proxy IP or CIDR %q", value)
		}
		values = append(values, value)
	}

	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, "--trusted-proxy-ip="+value)
	}
	return args, nil
}

// probe reports whether the gate is answering. A gate that is up but not
// serving is the failure that matters here: everything behind it returns 500
// rather than degrading, so "the container is running" is not the question.
func probe(url string, timeout time.Duration) error {
	if url == "" {
		return fmt.Errorf("--health-check requires a URL")
	}
	client := &http.Client{Timeout: timeout}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("health check %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("health check %s: status %d", url, response.StatusCode)
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "anas oauth2-proxy bootstrap: "+format+"\n", args...)
	os.Exit(1)
}
