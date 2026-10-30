// TEST_CASES: MAILMOD-T-053
package cloudrelay

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	serversmtp "github.com/emersion/go-smtp"
	"golang.org/x/sys/unix"
)

func TestSMTPBoundaryCandidate(t *testing.T) {
	ledger, store, _ := originalFixture(t, routeMax+10000)
	certPEM, keyPEM := runtimeCertificate(t)
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(certPEM)) {
		t.Fatal("test CA")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := serversmtp.NewServer(smtpBackend{store: store})
	server.Domain = "relay.example.test"
	server.MaxMessageBytes = dms.IntakeMaxBytes
	server.MaxRecipients = 100
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	connect := func() *smtp.Client {
		t.Helper()
		c, e := smtp.Dial(listener.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		if e = c.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: "127.0.0.1", RootCAs: roots}); e != nil {
			t.Fatal(e)
		}
		return c
	}
	client := connect()
	defer client.Close()
	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt("no-one@example.test"); err == nil {
		t.Fatal("unknown recipient accepted")
	}
	if err = client.Rcpt("ALIAS@EXAMPLE.TEST"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt("alias@example.test"); err != nil {
		t.Fatal(err)
	}
	writer, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	body := "From: sender@example.net\r\nTo: forged@example.test\r\n\r\nfirst\r\n"
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("store must precede 250: %v", err)
	}
	disk, err := ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(disk.Reservations) != 1 || disk.Reservations[0].State != "ready" || len(disk.Reservations[0].Recipients) != 1 || disk.Reservations[0].Recipients[0].Route != routeFixture("alias@example.test") {
		t.Fatalf("accepted envelope: %+v %v", disk.Reservations, err)
	}
	_, stored, err := store.Read(disk.Reservations[0].ID)
	if err != nil || string(stored) != body {
		t.Fatalf("stored bytes %q: %v", stored, err)
	}
	newRoute := routeFixture("alias@example.test")
	newRoute.OwnerAnchor, newRoute.MailboxID, newRoute.ClaimVersion = routeOp2, routeOp3, routeOp1
	if err = client.Mail(""); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt("alias@example.test"); err != nil {
		t.Fatal(err)
	}
	writer, err = client.Data()
	if err != nil {
		t.Fatal(err)
	}
	// RCPT succeeded for the former binding, but the message is accepted only
	// after DATA and must use the binding serialized with the durable write.
	if _, err = ledger.Replace(1, routeOp2, []ingress.Route{routeFixture("alice@example.test"), newRoute}); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err = ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(disk.Reservations) != 2 || disk.Reservations[0].Recipients[0].Route == disk.Reservations[1].Recipients[0].Route || disk.Reservations[1].Recipients[0].Route != newRoute {
		t.Fatalf("transfer boundary: %+v %v", disk.Reservations, err)
	}
	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt("alice@example.test"); err != nil {
		t.Fatal(err)
	}
	writer, err = client.Data()
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	err = writer.Close()
	unix.Flock(lock, unix.LOCK_UN)
	var smtpErr *textproto.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 451 || strings.Contains(err.Error(), "sender@example.net") {
		t.Fatalf("storage failure must be temporary and private: %v", err)
	}
	disk, err = ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(disk.Reservations) != 2 {
		t.Fatalf("failed transaction recorded: %v", err)
	}

	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt("alice@example.test"); err != nil {
		t.Fatal(err)
	}
	writer, err = client.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Replace(2, routeOp3, []ingress.Route{newRoute}); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	err = writer.Close()
	if !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatalf("removed route must not receive data: %v", err)
	}
	disk, err = ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(disk.Reservations) != 2 {
		t.Fatalf("removed recipient recorded: %v", err)
	}
}

type failingSMTPReader struct{ err error }

func (r failingSMTPReader) Read([]byte) (int, error) { return 0, r.err }

func TestSMTPDataReadErrorsDoNotStore(t *testing.T) {
	ledger, store, _ := originalFixture(t, routeMax+1000)
	s := &smtpSession{store: store, recipients: []string{"alice@example.test"}}
	if err := s.Data(failingSMTPReader{serversmtp.ErrDataTooLarge}); err != smtpOversize {
		t.Fatalf("oversized message: %v", err)
	}
	if err := s.Data(failingSMTPReader{io.ErrUnexpectedEOF}); err != smtpTemporary {
		t.Fatalf("incomplete message: %v", err)
	}
	disk, err := ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(disk.Reservations) != 0 {
		t.Fatalf("failed reads stored data: %v", err)
	}
}
