// TEST_CASES: MAILMOD-T-055
package cloudrelay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/smtp"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
	"golang.org/x/sys/unix"
)

func TestSMTPRuntimeDeliveryNotificationAndRestart(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	registrar := &recordedNotices{notices: map[string]store.IngressNotice{}}
	var wake atomic.Int32
	nas := httptest.NewTLSServer(ingress.NotificationHandler{Store: registrar, Sources: map[string]ingress.NotificationSource{routeSource: {Generation: routeGeneration, Secret: strings.Repeat("c", 64)}}, Wake: func(id string) { registrar.wake(id); wake.Add(1) }})
	defer nas.Close()
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, dir, httpsListener.Addr().String())
	cfg.SMTPListen, cfg.SMTPHostname = smtpListener.Addr().String(), "relay.example.test"
	cfg.NotificationOrigin, cfg.NotificationSecret = nas.URL, strings.Repeat("c", 64)
	cfg.NotificationCA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: nas.Certificate().Raw}))
	loaded, err := LoadRuntimeConfig(writeRuntimeConfig(t, cfg))
	if err != nil {
		t.Fatal(err)
	}
	p, err := prepareRuntime(loaded)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.serve(ctx, httpsListener, smtpListener, nil) }()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(cfg.TLSCertificate)) {
		t.Fatal("test CA")
	}
	client, err := smtp.Dial(smtpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err = client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.SMTPHostname, RootCAs: roots}); err != nil {
		t.Fatal(err)
	}
	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt("alias@example.test"); err != nil {
		t.Fatal(err)
	}
	w, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	body := "From: sender@example.net\r\nTo: forged@example.test\r\n\r\nhello\r\n"
	if _, err = io.WriteString(w, body); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = client.Quit()
	source, err := ingress.NewSourceClient(ingress.SourceConfig{ID: routeSource, Generation: routeGeneration, Origin: "https://" + httpsListener.Addr().String(), Token: cfg.Reader, ControlToken: cfg.Control, Domains: cfg.Domains, AdditionalCA: []byte(cfg.TLSCertificate)})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	deadline := time.Now().Add(5 * time.Second)
	var page ingress.SourcePage
	for {
		page, err = source.List(context.Background(), "")
		if err == nil && len(page.Messages) == 1 && wake.Load() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("source or notice unavailable: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var got bytes.Buffer
	if _, err = source.Download(context.Background(), page.Messages[0], &got); err != nil || got.String() != body || page.Messages[0].Metadata.AcceptedAddress != "alias@example.test" {
		t.Fatalf("source content/target: %v", err)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	p.close()
	p, err = prepareRuntime(loaded)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer p.close()
	reservation, stored, err := p.store.Read(strings.SplitN(page.Messages[0].Metadata.MessageID, "-", 2)[1])
	if err != nil || reservation.State != "ready" || string(stored) != body {
		t.Fatalf("restart original: %v", err)
	}
}

func TestSMTPRuntimePortConflictReleasesResources(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer smtpListener.Close()
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsAddress := httpsListener.Addr().String()
	_ = httpsListener.Close()
	cfg := runtimeFixture(t, dir, httpsAddress)
	cfg.SMTPListen, cfg.SMTPHostname = smtpListener.Addr().String(), "relay.example.test"
	if err = RunRuntimeFile(context.Background(), writeRuntimeConfig(t, cfg), nil); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("port conflict: %v", err)
	}
	httpsListener, err = net.Listen("tcp", httpsAddress)
	if err != nil {
		t.Fatalf("HTTPS listener leaked: %v", err)
	}
	_ = httpsListener.Close()
	p, err := prepareRuntime(cfg)
	if err != nil {
		t.Fatalf("source lock leaked: %v", err)
	}
	p.close()
}

func TestSMTPRuntimeFileBindsAndStopsBothListeners(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpsAddress, smtpAddress := httpsListener.Addr().String(), smtpListener.Addr().String()
	_ = httpsListener.Close()
	_ = smtpListener.Close()
	cfg := runtimeFixture(t, dir, httpsAddress)
	cfg.SMTPListen, cfg.SMTPHostname = smtpAddress, "relay.example.test"
	path := writeRuntimeConfig(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunRuntimeFile(ctx, path, nil) }()
	deadline := time.Now().Add(5 * time.Second)
	var c *smtp.Client
	for {
		c, err = smtp.Dial(smtpAddress)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("SMTP did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = c.Close()
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{httpsAddress, smtpAddress} {
		l, e := net.Listen("tcp", addr)
		if e != nil {
			t.Fatalf("listener leaked: %v", e)
		}
		_ = l.Close()
	}
	p, err := prepareRuntime(cfg)
	if err != nil {
		t.Fatalf("source lock leaked: %v", err)
	}
	p.close()
}

func TestSMTPRuntimeStopsPartialBDAT(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, dir, httpsListener.Addr().String())
	cfg.SMTPListen, cfg.SMTPHostname = smtpListener.Addr().String(), "relay.example.test"
	p, err := prepareRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.serve(ctx, httpsListener, smtpListener, nil) }()
	conn, err := net.Dial("tcp", smtpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := textproto.NewReader(bufio.NewReader(conn))
	if _, _, err = r.ReadResponse(220); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"EHLO client.example.test", "MAIL FROM:<sender@example.net>", "RCPT TO:<alice@example.test>"} {
		if _, err = fmt.Fprintf(conn, "%s\r\n", command); err != nil {
			t.Fatal(err)
		}
		if _, _, err = r.ReadResponse(250); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = io.WriteString(conn, "BDAT 3\r\nabc"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.ReadResponse(250); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("partial BDAT prevented shutdown")
	}
	p.close()
	state, err := p.store.ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(state.Reservations) != 0 {
		t.Fatalf("partial BDAT accepted: %v", err)
	}
	p, err = prepareRuntime(cfg)
	if err != nil {
		t.Fatalf("lock not released on shutdown: %v", err)
	}
	p.close()
}

func TestSMTPRuntimeLimitsConnectionsAndReleasesSlot(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	httpsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, dir, httpsListener.Addr().String())
	cfg.SMTPListen, cfg.SMTPHostname = smtpListener.Addr().String(), "relay.example.test"
	p, err := prepareRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.serve(ctx, httpsListener, smtpListener, nil) }()
	var clients []net.Conn
	defer func() {
		for _, c := range clients {
			_ = c.Close()
		}
		cancel()
		<-done
		p.close()
	}()
	for i := 0; i < smtpConnectionLimit; i++ {
		c, e := net.Dial("tcp", smtpListener.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		clients = append(clients, c)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, e = textproto.NewReader(bufio.NewReader(c)).ReadResponse(220); e != nil {
			t.Fatal(e)
		}
	}
	extra, err := net.Dial("tcp", smtpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	clients = append(clients, extra)
	reader := textproto.NewReader(bufio.NewReader(extra))
	_ = extra.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, _, err = reader.ReadResponse(220); err == nil {
		t.Fatal("connection beyond limit received a greeting")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected connection to wait for a slot: %v", err)
	}
	_ = clients[0].Close()
	_ = extra.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = reader.ReadResponse(220); err != nil {
		t.Fatalf("released slot did not accept waiting connection: %v", err)
	}
}

func TestSMTPRuntimeBoundsDataAndWaitsOnShutdown(t *testing.T) {
	_, source, _ := originalFixture(t, routeMax+1000)
	gate := &smtpDataGate{slots: make(chan struct{}, 1)}
	first := &smtpSession{store: source, dataGate: gate, recipients: []string{"alice@example.test"}}
	second := &smtpSession{store: source, dataGate: gate, recipients: []string{"alice@example.test"}}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- first.Data(reader) }()
	deadline := time.Now().Add(3 * time.Second)
	for len(gate.slots) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(gate.slots) != 1 {
		t.Fatal("first DATA did not acquire slot")
	}
	if err := second.Data(strings.NewReader("another message")); err != smtpTemporary {
		t.Fatalf("concurrent DATA was not limited: %v", err)
	}
	stopped := make(chan struct{})
	go func() { gate.stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("shutdown did not wait for active DATA")
	case <-time.After(50 * time.Millisecond):
	}
	_ = writer.CloseWithError(io.ErrUnexpectedEOF)
	if err := <-done; err != smtpTemporary {
		t.Fatalf("interrupted DATA result: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if err := second.Data(strings.NewReader("after shutdown")); err != smtpTemporary {
		t.Fatalf("shutdown allowed new DATA: %v", err)
	}
}

func TestSMTPRuntimeRecipientWaitsForBriefLock(t *testing.T) {
	ledger, source, _ := originalFixture(t, routeMax+1000)
	dir, err := ledger.openDir()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := unix.Openat(dir, "routes.lock", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	unix.Close(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(lock)
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	s := &smtpSession{store: source}
	done := make(chan error, 1)
	go func() { done <- s.Rcpt("alice@example.test", nil) }()
	time.Sleep(75 * time.Millisecond)
	_ = unix.Flock(lock, unix.LOCK_UN)
	if err = <-done; err != nil {
		t.Fatalf("brief contention rejected valid recipient: %v", err)
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	err = s.Rcpt("alice@example.test", nil)
	_ = unix.Flock(lock, unix.LOCK_UN)
	if err != smtpTemporary {
		t.Fatalf("persistent contention must be temporary: %v", err)
	}
}

func TestSMTPRuntimeExactSizeBoundary(t *testing.T) {
	_, source, _ := originalFixture(t, routeMax+dms.IntakeMaxBytes+100)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := runtimeCertificate(t)
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	server, limited, tracked, gate := newSMTPRuntime(source, "relay.example.test", cert, listener)
	done := make(chan error, 1)
	go func() { done <- server.Serve(limited) }()
	defer func() {
		_ = limited.Close()
		tracked.closeConnections()
		<-done
		gate.stop()
		_ = server.Shutdown(context.Background())
	}()
	client, err := smtp.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	block := strings.Repeat("x", 1022) + "\r\n"
	send := func(over bool) error {
		if e := client.Mail("sender@example.net"); e != nil {
			return e
		}
		if e := client.Rcpt("alice@example.test"); e != nil {
			return e
		}
		w, e := client.Data()
		if e != nil {
			return e
		}
		for i := 0; i < dms.IntakeMaxBytes/len(block); i++ {
			if _, e = io.WriteString(w, block); e != nil {
				return e
			}
		}
		if over {
			if _, e = io.WriteString(w, "x"); e != nil {
				return e
			}
		}
		return w.Close()
	}
	if err = send(false); err != nil {
		t.Fatalf("exact 25 MiB must be accepted: %v", err)
	}
	err = send(true)
	var smtpErr *textproto.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 552 {
		t.Fatalf("over 25 MiB must be rejected: %v", err)
	}
	state, err := source.ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(state.Reservations) != 1 || state.Reservations[0].Bytes != dms.IntakeMaxBytes {
		t.Fatalf("size boundary persisted wrong data: %v", err)
	}
}
