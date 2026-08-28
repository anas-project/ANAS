package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
)

const oauth2ProxyBinary = "/bin/oauth2-proxy"

const healthCheckFlag = "--health-check="

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

	args := append([]string{oauth2ProxyBinary}, os.Args[1:]...)
	args = append(args, trusted...)
	if err := syscall.Exec(oauth2ProxyBinary, args, os.Environ()); err != nil {
		fatalf("start oauth2-proxy: %v", err)
	}
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
