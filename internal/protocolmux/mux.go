// Package protocolmux safely separates TLS and plaintext HTTP connections
// accepted on one fixed management port.
package protocolmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultFirstByteTimeout = 5 * time.Second
	defaultMaxConnections   = 256
)

// Protocol identifies the only two protocols accepted by the console port.
// Unknown input is closed before it reaches either HTTP server.
type Protocol string

const (
	ProtocolPlaintext Protocol = "plaintext"
	ProtocolTLS       Protocol = "tls"
)

type Options struct {
	FirstByteTimeout time.Duration
	MaxConnections   int
	// Limiter may be shared by dispatchers for IPv4 and IPv6 listeners so the
	// configured cap applies to the service rather than independently per
	// address. MaxConnections must be zero when Limiter is supplied.
	Limiter  *ConnectionLimiter
	OnReject func(error)
}

type ConnectionLimiter struct {
	slots chan struct{}
}

func NewConnectionLimiter(maxConnections int) (*ConnectionLimiter, error) {
	if maxConnections < 1 {
		return nil, errors.New("connection limit must be positive")
	}
	return &ConnectionLimiter{slots: make(chan struct{}, maxConnections)}, nil
}

func (limiter *ConnectionLimiter) acquire() bool {
	select {
	case limiter.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (limiter *ConnectionLimiter) release() { <-limiter.slots }

// Dispatcher owns the root listener. Plaintext and TLS return child listeners
// suitable for http.Server.Serve and tls.NewListener respectively.
type Dispatcher struct {
	root       net.Listener
	timeout    time.Duration
	limit      *ConnectionLimiter
	onReject   func(error)
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error
	serveState atomic.Bool
	activeMu   sync.Mutex
	active     map[*trackedConn]struct{}
	plain      *childListener
	tls        *childListener
}

func New(root net.Listener, options Options) (*Dispatcher, error) {
	if root == nil {
		return nil, errors.New("root listener is required")
	}
	if options.FirstByteTimeout == 0 {
		options.FirstByteTimeout = defaultFirstByteTimeout
	}
	if options.FirstByteTimeout < 0 {
		return nil, errors.New("first-byte timeout must be positive")
	}
	if options.Limiter != nil && options.MaxConnections != 0 {
		return nil, errors.New("set either Limiter or MaxConnections, not both")
	}
	if options.Limiter == nil {
		if options.MaxConnections == 0 {
			options.MaxConnections = defaultMaxConnections
		}
		var err error
		options.Limiter, err = NewConnectionLimiter(options.MaxConnections)
		if err != nil {
			return nil, err
		}
	}
	dispatcher := &Dispatcher{
		root:     root,
		timeout:  options.FirstByteTimeout,
		limit:    options.Limiter,
		onReject: options.OnReject,
		done:     make(chan struct{}),
		active:   make(map[*trackedConn]struct{}),
	}
	dispatcher.plain = newChildListener(dispatcher, ProtocolPlaintext)
	dispatcher.tls = newChildListener(dispatcher, ProtocolTLS)
	return dispatcher, nil
}

func (dispatcher *Dispatcher) Plaintext() net.Listener { return dispatcher.plain }
func (dispatcher *Dispatcher) TLS() net.Listener       { return dispatcher.tls }

// Serve accepts until ctx is canceled or Close is called. It may be called
// once. Per-connection classifiers are bounded by both a deadline and a global
// connection semaphore; the semaphore remains held until the HTTP server
// closes the handed-off connection.
func (dispatcher *Dispatcher) Serve(ctx context.Context) error {
	if !dispatcher.serveState.CompareAndSwap(false, true) {
		return errors.New("protocol dispatcher Serve called more than once")
	}
	stop := context.AfterFunc(ctx, func() { _ = dispatcher.Close() })
	defer stop()
	for {
		connection, err := dispatcher.root.Accept()
		if err != nil {
			select {
			case <-dispatcher.done:
				return nil
			default:
			}
			return fmt.Errorf("accept console connection: %w", err)
		}
		if dispatcher.limit.acquire() {
			tracked := dispatcher.track(connection)
			if tracked != nil {
				go dispatcher.classify(tracked)
			}
		} else {
			dispatcher.reject(connection, errors.New("console connection limit reached"))
		}
	}
}

func (dispatcher *Dispatcher) Close() error {
	dispatcher.closeOnce.Do(func() {
		close(dispatcher.done)
		dispatcher.closeErr = dispatcher.root.Close()
		if errors.Is(dispatcher.closeErr, net.ErrClosed) {
			dispatcher.closeErr = nil
		}
		dispatcher.activeMu.Lock()
		connections := make([]*trackedConn, 0, len(dispatcher.active))
		for connection := range dispatcher.active {
			connections = append(connections, connection)
		}
		dispatcher.activeMu.Unlock()
		for _, connection := range connections {
			_ = connection.Close()
		}
	})
	return dispatcher.closeErr
}

func (dispatcher *Dispatcher) track(connection net.Conn) *trackedConn {
	var tracked *trackedConn
	tracked = newTrackedConn(connection, func() {
		dispatcher.activeMu.Lock()
		delete(dispatcher.active, tracked)
		dispatcher.activeMu.Unlock()
		dispatcher.limit.release()
	})
	dispatcher.activeMu.Lock()
	select {
	case <-dispatcher.done:
		dispatcher.activeMu.Unlock()
		_ = tracked.Close()
		return nil
	default:
		dispatcher.active[tracked] = struct{}{}
		dispatcher.activeMu.Unlock()
		return tracked
	}
}

func (dispatcher *Dispatcher) classify(connection net.Conn) {
	protocol, prefix, err := classify(connection, dispatcher.timeout)
	if err != nil {
		dispatcher.reject(connection, err)
		return
	}
	if err := connection.SetReadDeadline(time.Time{}); err != nil {
		dispatcher.reject(connection, fmt.Errorf("clear protocol deadline: %w", err))
		return
	}
	wrapped := &prefixConn{Conn: connection, prefix: bytes.NewReader(prefix)}
	var listener *childListener
	if protocol == ProtocolTLS {
		listener = dispatcher.tls
	} else {
		listener = dispatcher.plain
	}
	select {
	case listener.connections <- wrapped:
	case <-listener.done:
		dispatcher.reject(wrapped, fmt.Errorf("%s listener is closed", protocol))
	case <-dispatcher.done:
		_ = wrapped.Close()
	}
}

func (dispatcher *Dispatcher) reject(connection net.Conn, err error) {
	_ = connection.Close()
	if dispatcher.onReject != nil {
		dispatcher.onReject(err)
	}
}

var httpPrefixes = [][]byte{
	[]byte("GET "),
	[]byte("HEAD "),
	[]byte("POST "),
	[]byte("PUT "),
	[]byte("PATCH "),
	[]byte("DELETE "),
	[]byte("OPTIONS "),
}

func classify(connection net.Conn, timeout time.Duration) (Protocol, []byte, error) {
	if err := connection.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", nil, fmt.Errorf("set protocol deadline: %w", err)
	}
	prefix := make([]byte, 0, 8)
	for len(prefix) < cap(prefix) {
		var next [1]byte
		if _, err := connection.Read(next[:]); err != nil {
			return "", prefix, fmt.Errorf("read protocol prefix: %w", err)
		}
		prefix = append(prefix, next[0])
		if tlsComplete, tlsPossible := tlsPrefixState(prefix); tlsComplete {
			return ProtocolTLS, prefix, nil
		} else if tlsPossible {
			continue
		}
		httpComplete, httpPossible := httpPrefixState(prefix)
		if httpComplete {
			return ProtocolPlaintext, prefix, nil
		}
		if !httpPossible {
			return "", prefix, errors.New("unknown protocol on console port")
		}
	}
	return "", prefix, errors.New("unknown protocol on console port")
}

func tlsPrefixState(prefix []byte) (complete, possible bool) {
	if len(prefix) == 0 || prefix[0] != 0x16 {
		return false, false
	}
	if len(prefix) == 1 {
		return false, true
	}
	if prefix[1] != 0x03 {
		return false, false
	}
	if len(prefix) == 2 {
		return false, true
	}
	if prefix[2] > 0x04 {
		return false, false
	}
	if len(prefix) < 5 {
		return false, true
	}
	length := int(prefix[3])<<8 | int(prefix[4])
	return length > 0, false
}

func httpPrefixState(prefix []byte) (complete, possible bool) {
	for _, candidate := range httpPrefixes {
		if bytes.Equal(prefix, candidate) {
			return true, true
		}
		if len(prefix) < len(candidate) && bytes.Equal(prefix, candidate[:len(prefix)]) {
			possible = true
		}
	}
	return false, possible
}

type trackedConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func newTrackedConn(connection net.Conn, release func()) *trackedConn {
	return &trackedConn{Conn: connection, release: release}
}

func (connection *trackedConn) Close() error {
	err := connection.Conn.Close()
	connection.once.Do(connection.release)
	return err
}

type prefixConn struct {
	net.Conn
	prefix *bytes.Reader
}

func (connection *prefixConn) Read(buffer []byte) (int, error) {
	if connection.prefix.Len() > 0 {
		return connection.prefix.Read(buffer)
	}
	return connection.Conn.Read(buffer)
}

type childListener struct {
	parent      *Dispatcher
	protocol    Protocol
	connections chan net.Conn
	done        chan struct{}
	closeOnce   sync.Once
}

func newChildListener(parent *Dispatcher, protocol Protocol) *childListener {
	return &childListener{
		parent:      parent,
		protocol:    protocol,
		connections: make(chan net.Conn),
		done:        make(chan struct{}),
	}
}

func (listener *childListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.done:
		return nil, net.ErrClosed
	case <-listener.parent.done:
		return nil, net.ErrClosed
	}
}

func (listener *childListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.done) })
	return nil
}

func (listener *childListener) Addr() net.Addr { return listener.parent.root.Addr() }
