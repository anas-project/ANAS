package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/consoletls"
	"github.com/anas-project/ANAS/internal/tempconsolecert"
)

func TestParseOptionsDefaultsAndExplicitConfig(t *testing.T) {
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.configPath != defaultServiceConfigPath {
		t.Fatalf("config path = %q", opts.configPath)
	}
	opts, err = parseOptions([]string{"--config", "/run/anas/testing.yml"})
	if err != nil || opts.configPath != "/run/anas/testing.yml" {
		t.Fatalf("explicit config = %#v, %v", opts, err)
	}
}

func TestParseOptionsRejectsOverridesAndInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"positional"},
		{"--config", "relative.yml"},
		{"--config", ""},
		{"--listen", "127.0.0.1:8080"},
		{"--workspace", "main=/srv/anas"},
		{"--unknown"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%q) succeeded", args)
		}
	}
}

func TestDirectHostMode(t *testing.T) {
	for _, test := range []struct {
		input consoleconfig.Mode
		want  httpapi.DirectMode
	}{
		{input: consoleconfig.ModeLAN, want: httpapi.DirectModeLAN},
		{input: consoleconfig.ModeLoopback, want: httpapi.DirectModeLoopback},
	} {
		got, err := directHostMode(test.input)
		if err != nil || got != test.want {
			t.Fatalf("directHostMode(%q) = %q, %v", test.input, got, err)
		}
	}
	if _, err := directHostMode("automatic"); err == nil {
		t.Fatal("unsupported mode was accepted")
	}
}

func TestHTTPCapabilityStateMapping(t *testing.T) {
	for _, test := range []struct {
		input consolestate.State
		want  httpapi.ConsoleState
	}{
		{input: consolestate.StateBootstrap, want: httpapi.StateBootstrap},
		{input: consolestate.StateEnrollment, want: httpapi.StateEnrollment},
		{input: consolestate.StateFull, want: httpapi.StateFull},
	} {
		got, err := httpCapabilityState(test.input)
		if err != nil || got != test.want {
			t.Fatalf("httpCapabilityState(%q) = %q, %v", test.input, got, err)
		}
	}
	if _, err := httpCapabilityState("future"); err == nil {
		t.Fatal("unknown persisted state was accepted")
	}
}

func TestDynamicTLSConfigFailsClosedWithoutCertificate(t *testing.T) {
	config := dynamicTLSConfig(nil)
	if config.GetCertificate == nil || config.MinVersion != 0x0303 || !config.SessionTicketsDisabled {
		t.Fatalf("TLS config = %#v", config)
	}
	if _, err := config.GetCertificate(nil); !errors.Is(err, consoletls.ErrNoCertificate) {
		t.Fatalf("GetCertificate error = %v", err)
	}
}

func TestConfiguredEnrollmentOriginUsesCanonicalLegoNameAndManagementPort(t *testing.T) {
	for _, test := range []struct {
		port int
		want string
	}{
		{port: 443, want: "https://anas.example.test"},
		{port: 8080, want: "https://anas.example.test:8080"},
	} {
		got, err := configuredEnrollmentOrigin(consoleconfig.Config{
			Port: test.port, TLS: consoleconfig.TLSConfig{Lego: &consoleconfig.LegoTLSPaths{BaseDomain: "example.test"}},
		})
		if err != nil || got != test.want {
			t.Fatalf("configuredEnrollmentOrigin(port=%d) = %q, %v; want %q", test.port, got, err, test.want)
		}
	}
	if _, err := configuredEnrollmentOrigin(consoleconfig.Config{Port: 8080}); err == nil {
		t.Fatal("configuration without lego produced an enrollment origin")
	}
}

func TestAdvanceToEnrollmentIfReadyCommitsStateAndNarrowsCredential(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	writer, err := audit.Open(consoleStore)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	stateStore, err := consolestate.Open(context.Background(), consoleStore, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	currentState := func(ctx context.Context) (consoleauth.ConsoleState, error) {
		state, err := stateStore.Current(ctx)
		if err != nil {
			return "", err
		}
		return authCapabilityState(state)
	}
	authStore, err := consoleauth.Open(consoleStore, consoleaudit.AuthSink{Writer: writer, Actor: "test"}, consoleauth.StoreOptions{CurrentState: currentState})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := authStore.IssueBootstrapToken(context.Background(), consoleauth.IssueBootstrapTokenRequest{
		TransactionID: consoleBootstrapTransactionID, State: consoleauth.StateBootstrap,
		AllowedRoutes: []string{"/api/v1/workspaces/{ws}/config"},
	})
	if err != nil {
		t.Fatal(err)
	}
	readyTarget := func(context.Context) (httpapi.EnrollmentTarget, error) {
		return httpapi.EnrollmentTarget{Origin: "https://anas.example.test:8080", SPKISHA256: strings.Repeat("ab", 32)}, nil
	}
	advanced, err := advanceToEnrollmentIfReady(context.Background(), stateStore, authStore, readyTarget)
	if err != nil || !advanced {
		t.Fatalf("advanceToEnrollmentIfReady = %v, %v", advanced, err)
	}
	if state, err := stateStore.Current(context.Background()); err != nil || state != consolestate.StateEnrollment {
		t.Fatalf("capability state = %q, %v", state, err)
	}
	session, err := authStore.ExchangeBootstrapToken(context.Background(), consoleauth.ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != consoleauth.StateEnrollment || !reflect.DeepEqual(session.AllowedRoutes, consoleauth.EnrollmentRecoveryRoutePatterns()) {
		t.Fatalf("promoted bootstrap session = %#v", session)
	}
}

func TestAdvanceToEnrollmentIfReadyWaitsForValidatedCertificate(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	writer, err := audit.Open(consoleStore)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	stateStore, err := consolestate.Open(context.Background(), consoleStore, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	authStore, err := consoleauth.Open(consoleStore, consoleaudit.AuthSink{Writer: writer, Actor: "test"}, consoleauth.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := advanceToEnrollmentIfReady(context.Background(), stateStore, authStore, func(context.Context) (httpapi.EnrollmentTarget, error) {
		return httpapi.EnrollmentTarget{}, consoletls.ErrNoCertificate
	})
	if err != nil || advanced {
		t.Fatalf("advance without certificate = %v, %v", advanced, err)
	}
	if state, err := stateStore.Current(context.Background()); err != nil || state != consolestate.StateBootstrap {
		t.Fatalf("capability state = %q, %v", state, err)
	}
}

func TestTemporaryCertificateIsNotAnEnrollmentTarget(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	generated, err := tempconsolecert.Generate(tempconsolecert.Options{
		Directory: directory, DNSNames: []string{"bootstrap.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := consoletls.NewManager(consoletls.Options{Temporary: &consoletls.Candidate{
		CertificatePath: generated.CertificatePath, PrivateKeyPath: generated.PrivateKeyPath,
		IssuerPath: generated.CertificatePath, TrustBundlePath: generated.CertificatePath,
		Source: consoletls.SourceTemporary, RequiredDNSNames: []string{"bootstrap.example.test"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := currentLegoEnrollmentTarget(context.Background(), manager, "https://anas.example.test:8080"); err == nil {
		t.Fatal("temporary certificate was accepted as an enrollment target")
	}
	material, err := currentConsoleCertificate(context.Background(), manager)
	if err != nil || material.Issuer != httpapi.CertificateIssuerTemporary || len(material.InternalCAPEM) != 0 {
		t.Fatalf("temporary certificate status = %#v, %v", material, err)
	}
	withoutTLS, err := currentConsoleCertificate(context.Background(), nil)
	if err != nil || withoutTLS.Issuer != httpapi.CertificateIssuerNone {
		t.Fatalf("certificate status without TLS = %#v, %v", withoutTLS, err)
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	got := []time.Duration{server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout, server.IdleTimeout}
	want := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	if !reflect.DeepEqual(got, want) || server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("server limits = %v, %d", got, server.MaxHeaderBytes)
	}
}

func TestRunConfiguredInitializesSecurityStoresBeforeWildcardListen(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	listenErr := errors.New("listener fixture stopped startup")
	listenCalls := 0
	err := runConfiguredWithListener(context.Background(), consoleconfig.Config{
		Mode: consoleconfig.ModeLAN, Port: 8080, ConsoleStore: consoleStore,
	}, nil, func(_ context.Context, network, address string) (net.Listener, error) {
		listenCalls++
		if network != "tcp4" || address != "0.0.0.0:8080" {
			t.Fatalf("first listener = %s %s", network, address)
		}
		if _, statErr := os.Stat(filepath.Join(consoleStore, audit.Filename)); statErr != nil {
			t.Fatalf("audit was not initialized before listen: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(consoleStore, consolestate.StateFileName)); statErr != nil {
			t.Fatalf("capability state was not initialized before listen: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(consoleStore, consolejobs.JournalFilename)); statErr != nil {
			t.Fatalf("job journal was not initialized before listen: %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(consoleStore, consolejobs.ExecutionLeaseFilename)); statErr != nil {
			t.Fatalf("job execution lease was not initialized before listen: %v", statErr)
		}
		return nil, listenErr
	})
	if !errors.Is(err, listenErr) || listenCalls != 1 {
		t.Fatalf("runConfiguredWithListener = %v, listen calls %d", err, listenCalls)
	}
}

func TestRunConfiguredRestartUsesPersistedCapabilityState(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	writer, err := audit.Open(consoleStore)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := consolestate.Open(context.Background(), consoleStore, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.Transition(context.Background(), consolestate.StateEnrollment, "certificate-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.Transition(context.Background(), consolestate.StateFull, "local-owner"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listeners := make(chan *pipeListener, 1)
	done := make(chan error, 1)
	go func() {
		done <- runConfiguredWithListener(ctx, consoleconfig.Config{
			Mode: consoleconfig.ModeLoopback, Port: 8080, ConsoleStore: consoleStore,
		}, nil, func(_ context.Context, network, _ string) (net.Listener, error) {
			if network == "tcp6" {
				return nil, syscall.EAFNOSUPPORT
			}
			listener := newPipeListener("127.0.0.1:8080")
			listeners <- listener
			return listener, nil
		})
	}()

	var listener *pipeListener
	select {
	case listener = <-listeners:
	case err := <-done:
		t.Fatalf("daemon stopped before listening: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not open the direct listener")
	}
	serverConnection, clientConnection := net.Pipe()
	defer clientConnection.Close()
	listener.connections <- &addressedConn{
		Conn: serverConnection, local: testAddr("127.0.0.1:8080"), remote: testAddr("127.0.0.1:54321"),
	}
	if err := clientConnection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(clientConnection, "GET /api/v1/system HTTP/1.1\r\nHost: 127.0.0.1:8080\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(clientConnection), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("full-state plaintext /system = %d, want 404", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop after cancellation")
	}
}

func TestRunConfiguredDoesNotListenWhenAuditStoreIsUnsafe(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	if err := os.Mkdir(consoleStore, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(consoleStore, 0o755); err != nil {
		t.Fatal(err)
	}
	listenCalls := 0
	err := runConfiguredWithListener(context.Background(), consoleconfig.Config{
		Mode: consoleconfig.ModeLAN, Port: 8080, ConsoleStore: consoleStore,
	}, nil, func(context.Context, string, string) (net.Listener, error) {
		listenCalls++
		return nil, errors.New("must not be called")
	})
	if err == nil || listenCalls != 0 {
		t.Fatalf("unsafe audit startup error = %v, listen calls %d", err, listenCalls)
	}
}

func TestRunConfiguredDoesNotListenWhenCapabilityStateIsCorrupt(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	writer, err := audit.Open(consoleStore)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleStore, consolestate.StateFileName), []byte(`{"api_version":"future","state":"bootstrap"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	listenCalls := 0
	err = runConfiguredWithListener(context.Background(), consoleconfig.Config{
		Mode: consoleconfig.ModeLAN, Port: 8080, ConsoleStore: consoleStore,
	}, nil, func(context.Context, string, string) (net.Listener, error) {
		listenCalls++
		return nil, errors.New("must not be called")
	})
	if err == nil || !strings.Contains(err.Error(), "initialize console capability state") || listenCalls != 0 {
		t.Fatalf("corrupt state startup error = %v, listen calls %d", err, listenCalls)
	}
}

func TestRunConfiguredDoesNotListenWhenJobJournalIsCorrupt(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	store, err := consolejobs.Open(consoleStore, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consoleStore, consolejobs.JournalFilename), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listenCalls := 0
	err = runConfiguredWithListener(context.Background(), consoleconfig.Config{
		Mode: consoleconfig.ModeLAN, Port: 8080, ConsoleStore: consoleStore,
	}, nil, func(context.Context, string, string) (net.Listener, error) {
		listenCalls++
		return nil, errors.New("must not be called")
	})
	if err == nil || !strings.Contains(err.Error(), "initialize console jobs") || listenCalls != 0 {
		t.Fatalf("corrupt job journal startup error = %v, listen calls %d", err, listenCalls)
	}
}

func TestRunConfiguredDoesNotInterruptActiveExecutorWhenLeaseIsHeld(t *testing.T) {
	consoleStore := filepath.Join(t.TempDir(), "console")
	lease, err := consolejobs.AcquireExecutionLease(context.Background(), consoleStore)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	store, err := consolejobs.Open(consoleStore, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), consolejobs.CreateSpec{
		Kind: "apply", WorkspaceID: "main", Mutating: true,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: "local-owner", Method: http.MethodPost,
			CanonicalPath: "/api/v1/workspaces/main/actions/apply",
			Key:           "active-executor", RequestDigest: consolejobs.DigestRequest([]byte("{}")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(context.Background(), created.Job.ID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	listenCalls := 0
	err = runConfiguredWithListener(ctx, consoleconfig.Config{
		Mode: consoleconfig.ModeLAN, Port: 8080, ConsoleStore: consoleStore,
	}, nil, func(context.Context, string, string) (net.Listener, error) {
		listenCalls++
		return nil, errors.New("must not be called")
	})
	if !errors.Is(err, context.DeadlineExceeded) || listenCalls != 0 {
		t.Fatalf("contended daemon startup error = %v, listen calls %d", err, listenCalls)
	}
	job, getErr := store.Get(context.Background(), created.Job.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if job.Status != consolejobs.StatusRunning {
		t.Fatalf("contended daemon changed active job to %s", job.Status)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

type blockingListener struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{closed: make(chan struct{})}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	<-listener.closed
	return nil, errors.New("listener closed")
}

func (listener *blockingListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (*blockingListener) Addr() net.Addr { return testAddr("127.0.0.1:0") }

type pipeListener struct {
	connections chan net.Conn
	closed      chan struct{}
	once        sync.Once
	address     net.Addr
}

func newPipeListener(address string) *pipeListener {
	return &pipeListener{
		connections: make(chan net.Conn, 1),
		closed:      make(chan struct{}),
		address:     testAddr(address),
	}
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *pipeListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (listener *pipeListener) Addr() net.Addr { return listener.address }

type addressedConn struct {
	net.Conn
	local  net.Addr
	remote net.Addr
}

func (connection *addressedConn) LocalAddr() net.Addr  { return connection.local }
func (connection *addressedConn) RemoteAddr() net.Addr { return connection.remote }

type testAddr string

func (address testAddr) Network() string { return "tcp" }
func (address testAddr) String() string  { return string(address) }
