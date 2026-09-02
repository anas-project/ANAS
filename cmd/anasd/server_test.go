package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consoleconfig"
)

func TestTrustedProxyTLSRequiresCAValidatedPinnedClientIdentity(t *testing.T) {
	ca, caKey := testCertificateAuthority(t)
	allowed := testClientLeaf(t, ca, caKey, 2)
	other := testClientLeaf(t, ca, caKey, 3)
	caPath := filepath.Join(t.TempDir(), "client-ca.crt")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(allowed.RawSubjectPublicKeyInfo)
	config, err := newTrustedProxyTLSConfig(rejectingTLSConfig(), consoleconfig.TrustedProxyConfig{
		ClientCA: caPath, ClientSPKISHA256: []string{hex.EncodeToString(digest[:])},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ClientAuth != tls.RequireAndVerifyClientCert || config.ClientCAs == nil {
		t.Fatalf("trusted proxy TLS policy = auth %v CAs=%v", config.ClientAuth, config.ClientCAs)
	}
	if err := config.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{allowed}}); err != nil {
		t.Fatalf("pinned client rejected: %v", err)
	}
	if err := config.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{other}}); err == nil {
		t.Fatal("unlisted client signed by the same CA was accepted")
	}
	if err := config.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("missing client certificate was accepted")
	}
}

func testCertificateAuthority(t *testing.T) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test client CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, privateKey
}

func testClientLeaf(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, serial int64) *x509.Certificate {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "traefik-client"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func TestServeConsolePortShutsDownAllAddresses(t *testing.T) {
	listeners := []net.Listener{newBlockingListener(), newBlockingListener()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveConsolePort(ctx, listeners, http.NotFoundHandler(), rejectingTLSConfig(), nil)
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dual-stack management servers did not stop")
	}
}

func TestServeConsolePortDispatchesPlainHTTP(t *testing.T) {
	root := newPipeRootListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveConsolePort(ctx, []net.Listener{root}, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.TLS != nil {
				t.Error("plaintext request had TLS state")
			}
			w.WriteHeader(http.StatusNoContent)
		}), rejectingTLSConfig(), nil)
	}()

	client := root.connect(t)
	requestDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("GET /healthz HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"))
		requestDone <- err
	}()
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	count, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(buffer[:count]), "HTTP/1.1 204") {
		t.Fatalf("response = %q", buffer[:count])
	}
	_ = client.Close()
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request write did not finish")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServeConsolePortLetsInflightRequestFinishDuringShutdown(t *testing.T) {
	root := newPipeRootListener()
	ctx, cancel := context.WithCancel(context.Background())
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveConsolePort(ctx, []net.Listener{root}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(requestEntered)
			<-releaseRequest
			w.WriteHeader(http.StatusNoContent)
		}), rejectingTLSConfig(), nil)
	}()

	client := root.connect(t)
	defer client.Close()
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("GET /healthz HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"))
		writeDone <- err
	}()
	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server stopped before in-flight request completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequest)
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 256)
	count, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(buffer[:count]), "HTTP/1.1 204") {
		t.Fatalf("response = %q", buffer[:count])
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop after in-flight request completed")
	}
}

func TestSelectedCertificateBindingSurvivesHotReloadOnKeepAliveConnection(t *testing.T) {
	firstCertificate, firstSPKI := testServerCertificate(t, 1)
	secondCertificate, secondSPKI := testServerCertificate(t, 2)
	var current atomic.Pointer[tls.Certificate]
	current.Store(firstCertificate)
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			certificate := current.Load()
			if err := recordSelectedCertificate(hello, certificate); err != nil {
				return nil, err
			}
			return certificate, nil
		},
	}
	root := newPipeRootListener()
	server := newHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		digest, err := selectedConnectionSPKI(request.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, digest)
	}))
	server.ConnContext = bindSelectedCertificate
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(tls.NewListener(certificateSelectionListener{Listener: root}, tlsConfig))
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("TLS server stopped: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("TLS server did not stop")
		}
	})

	newClient := func() *http.Client {
		return &http.Client{Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, // test-only certificate
			MaxIdleConnsPerHost: 1,
			DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				clientConnection, serverConnection := net.Pipe()
				select {
				case root.connections <- serverConnection:
				case <-ctx.Done():
					_ = clientConnection.Close()
					_ = serverConnection.Close()
					return nil, ctx.Err()
				}
				connection := tls.Client(clientConnection, &tls.Config{
					MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, // test-only certificate
				})
				if err := connection.HandshakeContext(ctx); err != nil {
					_ = connection.Close()
					return nil, err
				}
				return connection, nil
			},
		}}
	}
	client := newClient()
	t.Cleanup(func() { client.Transport.(*http.Transport).CloseIdleConnections() })
	endpoint := "https://anas.test"
	if got := getResponseBody(t, client, endpoint); got != firstSPKI {
		t.Fatalf("first connection SPKI = %q, want %q", got, firstSPKI)
	}
	current.Store(secondCertificate)
	if got := getResponseBody(t, client, endpoint); got != firstSPKI {
		t.Fatalf("keep-alive connection observed reloaded SPKI %q, want handshake SPKI %q", got, firstSPKI)
	}

	newConnectionClient := newClient()
	defer newConnectionClient.Transport.(*http.Transport).CloseIdleConnections()
	if got := getResponseBody(t, newConnectionClient, endpoint); got != secondSPKI {
		t.Fatalf("new connection SPKI = %q, want %q", got, secondSPKI)
	}
}

func testServerCertificate(t *testing.T, serial int64) (*tls.Certificate, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "anas-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(derSubjectPublicKeyInfo(t, der))
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}, hex.EncodeToString(digest[:])
}

func derSubjectPublicKeyInfo(t *testing.T, der []byte) []byte {
	t.Helper()
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate.RawSubjectPublicKeyInfo
}

func getResponseBody(t *testing.T, client *http.Client, endpoint string) string {
	t.Helper()
	response, err := client.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func rejectingTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			return nil, errors.New("no certificate")
		},
	}
}

type pipeRootListener struct {
	connections chan net.Conn
	done        chan struct{}
	closeOnce   sync.Once
}

func newPipeRootListener() *pipeRootListener {
	return &pipeRootListener{connections: make(chan net.Conn), done: make(chan struct{})}
}

func (listener *pipeRootListener) connect(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	select {
	case listener.connections <- server:
		return client
	case <-time.After(time.Second):
		t.Fatal("management listener did not accept test connection")
		return nil
	}
}

func (listener *pipeRootListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.done:
		return nil, net.ErrClosed
	}
}

func (listener *pipeRootListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.done) })
	return nil
}

func (*pipeRootListener) Addr() net.Addr { return pipeRootAddress("pipe") }

type pipeRootAddress string

func (pipeRootAddress) Network() string { return "pipe" }
func (address pipeRootAddress) String() string {
	return string(address)
}
