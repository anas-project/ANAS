package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/protocolmux"
)

const maximumConsoleConnections = 256

type componentResult struct {
	name string
	err  error
}

type exactSourceListener struct {
	net.Listener
	allowed map[string]struct{}
}

func (listener exactSourceListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		host, _, splitErr := net.SplitHostPort(connection.RemoteAddr().String())
		if splitErr == nil {
			if before, _, found := strings.Cut(host, "%"); found {
				host = before
			}
			if ip := net.ParseIP(host); ip != nil {
				if _, ok := listener.allowed[ip.String()]; ok {
					return connection, nil
				}
			}
		}
		_ = connection.Close()
	}
}

func newTrustedProxyTLSConfig(base *tls.Config, config consoleconfig.TrustedProxyConfig) (*tls.Config, error) {
	if base == nil || base.GetCertificate == nil {
		return nil, errors.New("dynamic TLS configuration is required")
	}
	caPEM, err := os.ReadFile(config.ClientCA)
	if err != nil {
		return nil, fmt.Errorf("read trusted proxy client CA: %w", err)
	}
	if block, _ := pem.Decode(caPEM); block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("trusted proxy client CA contains no PEM certificate")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("trusted proxy client CA could not be parsed")
	}
	allowed := make(map[string]struct{}, len(config.ClientSPKISHA256))
	for _, digest := range config.ClientSPKISHA256 {
		allowed[digest] = struct{}{}
	}
	result := base.Clone()
	result.ClientAuth = tls.RequireAndVerifyClientCert
	result.ClientCAs = pool
	result.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return errors.New("trusted proxy client certificate is missing")
		}
		digest := sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo)
		if _, ok := allowed[hex.EncodeToString(digest[:])]; !ok {
			return errors.New("trusted proxy client certificate is not pinned")
		}
		return nil
	}
	return result, nil
}

func serveTrustedProxyTLS(ctx context.Context, root net.Listener, handler http.Handler, tlsConfig *tls.Config, allowedSources []string, logger *log.Logger) error {
	if root == nil || handler == nil || tlsConfig == nil {
		return errors.New("trusted proxy listener, handler, and TLS configuration are required")
	}
	allowed := make(map[string]struct{}, len(allowedSources))
	for _, source := range allowedSources {
		allowed[source] = struct{}{}
	}
	server := newHTTPServer("", handler)
	if logger != nil {
		server.ErrorLog = logger
	}
	listener := tls.NewListener(exactSourceListener{Listener: root, allowed: allowed}, tlsConfig)
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	select {
	case err := <-result:
		if ctx.Err() == nil {
			return fmt.Errorf("trusted proxy TLS server stopped unexpectedly: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return fmt.Errorf("shut down trusted proxy TLS server: %w", err)
	}
	if err := <-result; err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("trusted proxy TLS server: %w", err)
	}
	return nil
}

type selectedCertificateContextKey struct{}

// certificateSelectionConn binds the exact leaf selected during this TLS
// handshake to every HTTP request subsequently served on the connection. This
// remains stable for keep-alive requests even if the manager hot-reloads a new
// certificate in the meantime. Production disables session tickets because a
// resumed handshake does not select or present a certificate and therefore
// cannot establish this binding for enrollment exchange.
type certificateSelectionConn struct {
	net.Conn
	mu         sync.RWMutex
	spkiSHA256 string
}

func (connection *certificateSelectionConn) setSPKISHA256(value string) {
	connection.mu.Lock()
	connection.spkiSHA256 = value
	connection.mu.Unlock()
}

func (connection *certificateSelectionConn) selectedSPKISHA256() string {
	connection.mu.RLock()
	defer connection.mu.RUnlock()
	return connection.spkiSHA256
}

type certificateSelectionListener struct{ net.Listener }

func (listener certificateSelectionListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &certificateSelectionConn{Conn: connection}, nil
}

func recordSelectedCertificate(hello *tls.ClientHelloInfo, certificate *tls.Certificate) error {
	if certificate == nil || len(certificate.Certificate) == 0 {
		return errors.New("selected TLS certificate has no leaf")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse selected TLS certificate leaf: %w", err)
	}
	digest := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if hello != nil {
		if connection, ok := hello.Conn.(*certificateSelectionConn); ok {
			connection.setSPKISHA256(hex.EncodeToString(digest[:]))
		}
	}
	return nil
}

func bindSelectedCertificate(ctx context.Context, connection net.Conn) context.Context {
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return ctx
	}
	selected, ok := tlsConnection.NetConn().(*certificateSelectionConn)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, selectedCertificateContextKey{}, selected)
}

func selectedConnectionSPKI(ctx context.Context) (string, error) {
	selection, ok := ctx.Value(selectedCertificateContextKey{}).(*certificateSelectionConn)
	if !ok {
		return "", errors.New("request is not bound to a selected TLS certificate")
	}
	digest := selection.selectedSPKISHA256()
	if digest == "" {
		return "", errors.New("TLS certificate selection is unavailable")
	}
	return digest, nil
}

// serveConsolePort runs independent plaintext and TLS HTTP servers behind one
// bounded protocol dispatcher per concrete address. The limiter is shared, so
// dual-stack binding does not double the service-wide connection cap.
func serveConsolePort(ctx context.Context, roots []net.Listener, handler http.Handler, tlsConfig *tls.Config, logger *log.Logger) error {
	if len(roots) == 0 {
		return errors.New("at least one management listener is required")
	}
	for _, root := range roots {
		if root == nil {
			closeRootListeners(roots)
			return errors.New("management listeners must not contain nil")
		}
	}
	if handler == nil || tlsConfig == nil || tlsConfig.GetCertificate == nil {
		return errors.New("HTTP handler and dynamic TLS configuration are required")
	}
	limiter, err := protocolmux.NewConnectionLimiter(maximumConsoleConnections)
	if err != nil {
		return err
	}
	// The dispatcher lifetime is canceled explicitly after HTTP graceful
	// shutdown. Deriving it from ctx would close active requests immediately
	// when the service receives SIGTERM and defeat the shutdown window.
	runContext, cancel := context.WithCancel(context.Background())
	defer cancel()

	plainServer := newHTTPServer("", handler)
	tlsServer := newHTTPServer("", handler)
	tlsServer.ConnContext = bindSelectedCertificate
	if logger != nil {
		plainServer.ErrorLog = logger
		tlsServer.ErrorLog = logger
	}

	dispatchers := make([]*protocolmux.Dispatcher, 0, len(roots))
	results := make(chan componentResult, len(roots)*3)
	for _, root := range roots {
		dispatcher, err := protocolmux.New(root, protocolmux.Options{Limiter: limiter})
		if err != nil {
			closeDispatchers(dispatchers)
			return err
		}
		dispatchers = append(dispatchers, dispatcher)
		address := root.Addr().String()
		go func() {
			results <- componentResult{name: "protocol dispatcher " + address, err: dispatcher.Serve(runContext)}
		}()
		go func() {
			results <- componentResult{name: "plaintext HTTP server " + address, err: plainServer.Serve(dispatcher.Plaintext())}
		}()
		go func() {
			listener := tls.NewListener(certificateSelectionListener{Listener: dispatcher.TLS()}, tlsConfig)
			results <- componentResult{name: "TLS HTTP server " + address, err: tlsServer.Serve(listener)}
		}()
	}

	componentCount := len(roots) * 3
	completed := 0
	var serviceErr error
	select {
	case result := <-results:
		completed++
		if ctx.Err() == nil {
			serviceErr = unexpectedComponentError(result)
		}
	case <-ctx.Done():
	}

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	type shutdownResult struct {
		plain bool
		err   error
	}
	shutdownResults := make(chan shutdownResult, 2)
	go func() { shutdownResults <- shutdownResult{plain: true, err: plainServer.Shutdown(shutdownContext)} }()
	go func() { shutdownResults <- shutdownResult{err: tlsServer.Shutdown(shutdownContext)} }()
	var plainErr, tlsErr error
	for range 2 {
		result := <-shutdownResults
		if result.plain {
			plainErr = result.err
		} else {
			tlsErr = result.err
		}
	}
	if plainErr != nil {
		_ = plainServer.Close()
	}
	if tlsErr != nil {
		_ = tlsServer.Close()
	}
	// Shutdown closes the child listeners first and lets already handed-off
	// requests finish. Only after that grace period do we close classifiers,
	// root listeners, and any non-HTTP/partially classified connections.
	cancel()
	closeDispatchers(dispatchers)

	for completed < componentCount {
		select {
		case result := <-results:
			completed++
			if serviceErr == nil && ctx.Err() == nil {
				serviceErr = unexpectedComponentError(result)
			}
		case <-shutdownContext.Done():
			if serviceErr == nil {
				serviceErr = errors.New("management servers did not stop before the shutdown deadline")
			}
			return serviceErr
		}
	}
	if serviceErr != nil {
		return serviceErr
	}
	if plainErr != nil {
		return fmt.Errorf("shut down plaintext HTTP server: %w", plainErr)
	}
	if tlsErr != nil {
		return fmt.Errorf("shut down TLS HTTP server: %w", tlsErr)
	}
	return nil
}

func closeRootListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		if listener != nil {
			_ = listener.Close()
		}
	}
}

func closeDispatchers(dispatchers []*protocolmux.Dispatcher) {
	for _, dispatcher := range dispatchers {
		_ = dispatcher.Close()
	}
}

func unexpectedComponentError(result componentResult) error {
	if result.err == nil {
		return fmt.Errorf("%s stopped unexpectedly", result.name)
	}
	if errors.Is(result.err, http.ErrServerClosed) || errors.Is(result.err, net.ErrClosed) {
		return fmt.Errorf("%s stopped unexpectedly", result.name)
	}
	return fmt.Errorf("%s: %w", result.name, result.err)
}
