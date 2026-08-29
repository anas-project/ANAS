// Package consolelistener turns the static anasd service policy into concrete
// IPv4 and IPv6 listeners. It never inspects interfaces or managed services.
package consolelistener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"

	"github.com/anas-project/ANAS/internal/consoleconfig"
)

type Spec struct {
	Network      string
	Address      string
	IPv6Optional bool
}

func Specs(mode consoleconfig.Mode, port int) ([]Spec, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("management port must be between 1 and 65535")
	}
	portValue := strconv.Itoa(port)
	switch mode {
	case consoleconfig.ModeLAN:
		return []Spec{
			{Network: "tcp4", Address: net.JoinHostPort("0.0.0.0", portValue)},
			{Network: "tcp6", Address: net.JoinHostPort("::", portValue), IPv6Optional: true},
		}, nil
	case consoleconfig.ModeLoopback:
		return []Spec{
			{Network: "tcp4", Address: net.JoinHostPort("127.0.0.1", portValue)},
			{Network: "tcp6", Address: net.JoinHostPort("::1", portValue), IPv6Optional: true},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported direct-listener mode %q", mode)
	}
}

type ListenFunc func(context.Context, string, string) (net.Listener, error)

// ListenAll opens every required address. Lack of IPv6 support is the only
// optional failure; any other error closes listeners already opened and fails
// startup so the configured exposure is never silently narrowed.
func ListenAll(ctx context.Context, specs []Spec, listen ListenFunc) ([]net.Listener, error) {
	if listen == nil {
		config := &net.ListenConfig{}
		listen = config.Listen
	}
	listeners := make([]net.Listener, 0, len(specs))
	closeAll := func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}
	for _, spec := range specs {
		listener, err := listen(ctx, spec.Network, spec.Address)
		if err != nil {
			if spec.IPv6Optional && ipv6Unavailable(err) {
				continue
			}
			closeAll()
			return nil, fmt.Errorf("listen on %s %s: %w", spec.Network, spec.Address, err)
		}
		listeners = append(listeners, listener)
	}
	if len(listeners) == 0 {
		return nil, errors.New("service configuration produced no usable listeners")
	}
	return listeners, nil
}

func ipv6Unavailable(err error) bool {
	return errors.Is(err, syscall.EAFNOSUPPORT) ||
		errors.Is(err, syscall.EPROTONOSUPPORT) ||
		errors.Is(err, syscall.EADDRNOTAVAIL)
}
