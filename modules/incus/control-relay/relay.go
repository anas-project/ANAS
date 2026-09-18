package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

// The installer must establish the dedicated control bridge, source-interface
// firewall restrictions and a dedicated service account BEFORE starting this
// process. An interface address and source CIDR alone do not establish those
// authorizations. This component neither installs nor modifies host networking.
func serveRelay(ctx context.Context, settings relaySettings) error {
	if ctx == nil {
		return errConfiguration
	}
	validated, err := validateRelayConfiguration(settings.relayConfiguration)
	if err != nil || validated.listen != settings.listen || validated.subnet != settings.subnet {
		return errConfiguration
	}
	if err := checkRelayIdentity(settings); err != nil {
		return err
	}
	if err := checkRelayInterface(settings); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	listener, err := net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(settings.listen))
	if err != nil {
		return errListener
	}
	ownedContext, cancel := context.WithCancel(ctx)
	var connections sync.WaitGroup
	defer func() {
		cancel()
		_ = listener.Close()
		connections.Wait()
	}()
	if listener.Addr().(*net.TCPAddr).AddrPort() != settings.listen || checkRelayInterface(settings) != nil {
		return errTopology
	}
	capacity := make(chan struct{}, settings.MaxConnections)
	nextCheck := time.Now().Add(time.Second)
	for {
		if ownedContext.Err() != nil {
			return nil
		}
		if !time.Now().Before(nextCheck) {
			if checkRelayInterface(settings) != nil {
				return errTopology
			}
			nextCheck = time.Now().Add(time.Second)
		}
		if listener.SetDeadline(nextCheck) != nil {
			return errListener
		}
		connection, err := listener.AcceptTCP()
		if err != nil {
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				continue
			}
			if ownedContext.Err() != nil {
				return nil
			}
			return errListener
		}
		peer, ok := connection.RemoteAddr().(*net.TCPAddr)
		if !ok || !settings.acceptsSource(peer.AddrPort().Addr()) {
			_ = connection.Close()
			continue
		}
		select {
		case capacity <- struct{}{}:
			connections.Add(1)
			go func(connection *net.TCPConn) {
				defer connections.Done()
				defer func() { <-capacity }()
				defer connection.Close()
				forwardRelayConnection(ownedContext, connection, time.Duration(settings.IdleTimeoutSeconds)*time.Second)
			}(connection)
		default:
			_ = connection.Close()
		}
	}
}

func checkRelayInterface(settings relaySettings) error {
	binding, err := net.InterfaceByIndex(settings.InterfaceIndex)
	if err != nil || binding.Name != settings.InterfaceName || binding.Flags&net.FlagUp == 0 || binding.Flags&net.FlagLoopback != 0 {
		return errTopology
	}
	addresses, err := binding.Addrs()
	if err != nil || len(addresses) > 256 {
		return errTopology
	}
	found := false
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err != nil {
			return errTopology
		}
		if prefix.Addr() == settings.listen.Addr() && prefix.Masked() == settings.subnet {
			if found {
				return errTopology
			}
			found = true
		}
	}
	if !found {
		return errTopology
	}
	return nil
}

func forwardRelayConnection(ctx context.Context, downstream *net.TCPConn, idle time.Duration) {
	// Literal, compiled-in destination: no DNS, environment proxy, CONNECT,
	// SOCKS, request header, or client-supplied port participates in dialing.
	dialer := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp4", incusLoopbackDestination)
	if err != nil {
		return
	}
	defer connection.Close()
	upstream, ok := connection.(*net.TCPConn)
	if !ok {
		return
	}
	_ = relayTCP(ctx, downstream, upstream, idle)
}

// relayTCP owns both connections and waits for both copy loops to stop. It
// preserves half-close semantics and does not parse, terminate or re-originate
// TLS. Buffers are fixed-size; progress never captures payload or peer names.
func relayTCP(ctx context.Context, downstream, upstream *net.TCPConn, idle time.Duration) error {
	if ctx == nil || downstream == nil || upstream == nil || idle <= 0 {
		return errConfiguration
	}
	closeBoth := func() {
		_ = downstream.Close()
		_ = upstream.Close()
	}
	defer closeBoth()
	var deadlineMu sync.Mutex
	touch := func() error {
		deadlineMu.Lock()
		defer deadlineMu.Unlock()
		deadline := time.Now().Add(idle)
		return errors.Join(downstream.SetDeadline(deadline), upstream.SetDeadline(deadline))
	}
	if err := touch(); err != nil {
		return err
	}
	copyDirection := func(destination, source *net.TCPConn) error {
		buffer := make([]byte, 32<<10)
		for {
			n, readErr := source.Read(buffer)
			if n > 0 {
				if err := touch(); err != nil {
					return err
				}
				for pending := buffer[:n]; len(pending) > 0; {
					written, err := destination.Write(pending)
					if err != nil {
						return err
					}
					if written == 0 {
						return io.ErrShortWrite
					}
					pending = pending[written:]
					if err := touch(); err != nil {
						return err
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				return destination.CloseWrite()
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	done := make(chan error, 2)
	go func() { done <- copyDirection(upstream, downstream) }()
	go func() { done <- copyDirection(downstream, upstream) }()
	for remaining := 2; remaining > 0; remaining-- {
		select {
		case err := <-done:
			if err != nil {
				closeBoth()
				for left := remaining - 1; left > 0; left-- {
					<-done
				}
				return err
			}
		case <-ctx.Done():
			closeBoth()
			for left := remaining; left > 0; left-- {
				<-done
			}
			return ctx.Err()
		}
	}
	return nil
}
