// TEST_CASES: MAILMOD-T-053
package cloudrelay

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	serversmtp "github.com/emersion/go-smtp"
)

func TestForwardSMTPPreservesClaimAcrossTransfer(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := initializeRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}, "in.example.test")
	if err != nil {
		t.Fatal(err)
	}
	primary, alias := routeFixture("alice@example.test"), routeFixture("alias@example.test")
	if _, err := ledger.Replace(0, routeOp1, []ingress.Route{primary, alias}); err != nil {
		t.Fatal(err)
	}
	ownerOnly := alias
	ownerOnly.OwnerAnchor = routeOp2
	if _, err := ledger.Replace(1, routeOp2, []ingress.Route{primary, ownerOnly}); !errors.Is(err, ErrRouteState) {
		t.Fatal("target collision across historical claims was accepted", err)
	}
	store, err := InitializeOriginalStore(ledger, routeMax+10000, 7)
	if err != nil {
		t.Fatal(err)
	}
	primaryTarget, aliasTarget := ledger.smtpAddress(primary), ledger.smtpAddress(alias)
	if primaryTarget == aliasTarget || !strings.HasSuffix(aliasTarget, "@in.example.test") {
		t.Fatal("destinations are not distinct")
	}
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
	server.Domain, server.MaxMessageBytes, server.MaxRecipients = "relay.example.test", dms.IntakeMaxBytes, 100
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	client, err := smtp.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: "127.0.0.1", RootCAs: roots}); err != nil {
		t.Fatal(err)
	}
	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt(alias.Address); err == nil {
		t.Fatal("public address bypassed forward target")
	}
	if err = client.Rcpt(primaryTarget); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt(aliasTarget); err != nil {
		t.Fatal(err)
	}
	writer, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	body := "From: sender@example.net\r\nTo: forged@foreign.test\r\n\r\nforwarded body\r\n"
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	disk, err := ledger.locked(func(_ int, d routeDisk) (routeDisk, error) { return d, nil })
	if err != nil || len(disk.Reservations) != 1 || len(disk.Reservations[0].Recipients) != 2 {
		t.Fatal("forwarded envelope not stored", err)
	}
	first := disk.Reservations[0]
	if first.Recipients[0].Route != alias || first.Recipients[1].Route != primary {
		t.Fatal("public ownership not retained")
	}
	_, raw, err := store.Read(first.ID)
	if err != nil || !bytes.Equal(raw, []byte(body)) {
		t.Fatal("original changed", err)
	}
	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt(aliasTarget); err != nil {
		t.Fatal(err)
	}
	writer, err = client.Data()
	if err != nil {
		t.Fatal(err)
	}
	next := alias
	next.OwnerAnchor, next.MailboxID, next.ClaimVersion = routeOp2, routeOp3, routeOp1
	if _, err = ledger.Replace(1, routeOp2, []ingress.Route{primary, next}); err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	var smtpErr *textproto.Error
	if err = writer.Close(); !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatal("old target accepted after ownership changed", err)
	}
	if err = client.Mail("sender@example.net"); err != nil {
		t.Fatal(err)
	}
	if err = client.Rcpt(aliasTarget); !errors.As(err, &smtpErr) || smtpErr.Code != 550 {
		t.Fatal("removed destination still active", err)
	}
	newTarget := ledger.smtpAddress(next)
	if newTarget == aliasTarget {
		t.Fatal("transferred claim reused destination")
	}
	if err = client.Rcpt(newTarget); err != nil {
		t.Fatal(err)
	}
	writer, err = client.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(writer, body); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}, "in.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}); err == nil {
		t.Fatal("forward state accepted as direct")
	}
	if _, err = openRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}, "other.example.test"); err == nil {
		t.Fatal("different forward domain accepted")
	}
	reopenedStore, err := OpenOriginalStore(reopened, routeMax+10000, 7)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopenedStore.Store(first.ID, "sender@example.net", []string{aliasTarget, primaryTarget}, []byte(body))
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatal("old accepted message did not replay with original claim", err)
	}
	current, err := reopened.Read()
	if err != nil || current.Sequence != 3 {
		t.Fatal("unexpected acceptance sequence", err)
	}
}

func TestForwardRuntimeSealsDomainAndPreservesLegacy(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, filepath.Join(parent, "source"), "127.0.0.1:18448")
	cfg.SMTPListen, cfg.SMTPHostname = "127.0.0.1:2525", "relay.example.test"
	legacy := writeRuntimeConfig(t, cfg)
	if _, err := LoadRuntimeConfig(legacy); err != nil {
		t.Fatal("legacy runtime rejected", err)
	}
	cfg.ForwardDomain = "in.example.test"
	path := writeRuntimeConfig(t, cfg)
	if got, err := LoadRuntimeConfig(path); err != nil || got.ForwardDomain != cfg.ForwardDomain {
		t.Fatal("forward config rejected", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"forward_domain":`), []byte(`"forward_domain":"other.example.test","forward_domain":`), 1),
		bytes.Replace(raw, []byte(`"forward_domain":`), []byte(`"FORWARD_DOMAIN":`), 1),
		bytes.Replace(raw, []byte(`"forward_domain":"in.example.test"`), []byte(`"forward_domain":null`), 1),
	} {
		if _, err := decodeRuntimeConfig(bad); err == nil {
			t.Fatal("ambiguous forward config accepted")
		}
	}
	if err := InitializeRuntimeFile(path); err != nil {
		t.Fatal("forward initialization", err)
	}
	if err := InitializeRuntimeFile(path); err != nil {
		t.Fatal("forward initialization retry", err)
	}
	opened, err := prepareRuntime(cfg)
	if err != nil {
		t.Fatal("forward restart", err)
	}
	opened.close()
	changed := cfg
	changed.ForwardDomain = "other.example.test"
	if err := initializeRuntime(changed); !errors.Is(err, ErrInitializeConflict) {
		t.Fatal("forward domain changed existing sealed state", err)
	}
	for _, domain := range []string{"example.test", "IN.example.test", strings.Repeat("a", 53) + ".test"} {
		changed = cfg
		changed.ForwardDomain = domain
		if err := ValidateRuntimeConfig(changed); err == nil {
			t.Fatal("invalid forward domain accepted")
		}
	}
	changed = cfg
	changed.SMTPListen = ""
	changed.SMTPHostname = ""
	if err := ValidateRuntimeConfig(changed); err == nil {
		t.Fatal("forwarding without SMTP accepted")
	}
}

func TestForwardConcurrentTransferNeverReassignsOldTarget(t *testing.T) {
	for i := 0; i < 25; i++ {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		ledger, err := initializeRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}, "in.example.test")
		if err != nil {
			t.Fatal(err)
		}
		old := routeFixture("alias@example.test")
		if _, err := ledger.Replace(0, routeOp1, []ingress.Route{old}); err != nil {
			t.Fatal(err)
		}
		next := old
		next.OwnerAnchor, next.MailboxID, next.ClaimVersion = routeOp2, routeOp3, routeOp1
		target := ledger.smtpAddress(old)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var reserved EnvelopeReservation
		var reserveErr, replaceErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			for {
				reserved, reserveErr = ledger.ReserveEnvelope(routeOp3, "", strings.Repeat("a", 64), 10, []string{target})
				if !errors.Is(reserveErr, ErrRouteBusy) {
					return
				}
				runtime.Gosched()
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for {
				_, replaceErr = ledger.Replace(1, routeOp2, []ingress.Route{next})
				if !errors.Is(replaceErr, ErrRouteBusy) {
					return
				}
				runtime.Gosched()
			}
		}()
		close(start)
		wg.Wait()
		if replaceErr != nil || reserveErr != nil && !errors.Is(reserveErr, ErrEnvelopeRecipient) {
			t.Fatal("concurrent transfer failed", reserveErr, replaceErr)
		}
		if reserveErr == nil && (len(reserved.Recipients) != 1 || reserved.Recipients[0].Route != old) {
			t.Fatal("old target delivered to new claim")
		}
		if _, err := ledger.ReserveEnvelope("99999999-9999-4999-8999-999999999999", "", strings.Repeat("b", 64), 10, []string{target}); !errors.Is(err, ErrEnvelopeRecipient) {
			t.Fatal("withdrawn target became active", err)
		}
	}
}
