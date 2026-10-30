// TEST_CASES: MAILMOD-T-080
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func nativeVPSIngressRoundTrip(t *testing.T, data store.Store, backend *dms.Client, directory *nativeDirectory, resources []identity.MailboxResource) []string {
	t.Helper()
	external := nativeVPSCrossHostConfig(t)
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		t.Fatal(err)
	}
	uuid[6], uuid[8] = uuid[6]&15|64, uuid[8]&63|128
	source := fmt.Sprintf("%x-%x-%x-%x-%x", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:])
	generation := resources[0].ID
	reader, control, noticeKey := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	if external != nil {
		if external.Generation != generation || os.Getenv("MAIL_NATIVE_VPS_FORWARD_TEST") == "1" || os.Getenv("MAIL_NATIVE_VPS_OFFLINE_TEST") == "1" {
			t.Fatal("unsupported cross-host fixture mode")
		}
		source, reader, control, noticeKey = external.Source, external.Reader, external.Control, external.Notification
	}
	wake := make(chan struct{}, 1)
	notification := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{source: {Generation: generation, Secret: noticeKey}}, Wake: func(string) {
		select {
		case wake <- struct{}{}:
		default:
		}
	}}
	var notify *httptest.Server
	if external == nil {
		notify = httptest.NewTLSServer(notification)
	} else {
		notify = nativeVPSCrossHostNotify(t, "/bridge/notify.sock", notification)
	}
	t.Cleanup(notify.Close)
	notifyCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: notify.Certificate().Raw})
	if external != nil {
		if err := os.WriteFile("/bridge/notify-cert.pem", notifyCA, 0644); err != nil {
			t.Fatal("cross-host notification certificate unavailable")
		}
		nativeVPSWaitSignal(t, "relay-ready")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "isolated relay"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"relay.example.test"}}
	der, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	ephemeralPort := func() string {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		a := l.Addr().String()
		l.Close()
		return a
	}
	httpsAddr, smtpAddr := "", ""
	if external == nil {
		httpsAddr, smtpAddr = ephemeralPort(), ephemeralPort()
	} else {
		httpsAddr, smtpAddr = nativeVPSBridge(t, "relay.sock"), nativeVPSBridge(t, "smtp.sock")
		cert = []byte(external.RelayCA)
	}
	if httpsAddr == smtpAddr {
		t.Fatal("fixture port collision")
	}
	root := t.TempDir()
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	config := cloudrelay.RuntimeConfig{Schema: "anas.mail-vps-runtime/v1", Directory: filepath.Join(root, "source"), SourceID: source, Generation: generation, Domains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7, Listen: httpsAddr, CertificateHost: "127.0.0.1", Reader: reader, Control: control, TLSCertificate: string(cert), TLSPrivateKey: string(certKey), NotificationOrigin: notify.URL, NotificationSecret: noticeKey, NotificationCA: string(notifyCA), SMTPListen: smtpAddr, SMTPHostname: "relay.example.test"}
	forwardMode := os.Getenv("MAIL_NATIVE_VPS_FORWARD_TEST") == "1"
	if forwardMode {
		config.ForwardDomain = "delivery.example.test"
	}
	ctx, cancel := context.WithCancel(t.Context())
	if external == nil {
		raw, marshalErr := json.Marshal(config)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		path := filepath.Join(root, "runtime.json")
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		clear(raw)
		if err = cloudrelay.InitializeRuntimeFile(path); err != nil {
			t.Fatal("initialize real VPS source", err)
		}
		done := make(chan error, 1)
		go func() { done <- cloudrelay.RunRuntimeFile(ctx, path, nil) }()
		t.Cleanup(func() {
			cancel()
			select {
			case e := <-done:
				if e != nil {
					t.Error("relay stop", e)
				}
			case <-time.After(15 * time.Second):
				t.Error("relay stop timeout")
			}
		})
	} else {
		t.Cleanup(cancel)
	}
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: "https://" + httpsAddr, Token: reader, ControlToken: control, Domains: []string{"example.test"}, AdditionalCA: cert})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err = client.Status(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("VPS source did not start", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	candidates := []struct {
		resource identity.MailboxResource
		address  identity.AddressResource
	}{{resources[0], resources[0].Addresses[0]}, {resources[0], resources[0].Addresses[1]}, {resources[1], resources[1].Addresses[0]}}
	entry := ingressEntry{
		client: client,
		spec:   ingress.SourceSpec{ID: source, Generation: generation, Origin: "https://" + httpsAddr, Provider: "cloud-relay", Mode: "active", Domains: []string{"example.test"}},
		routes: &ingress.RouteSync{Ledger: data, Source: client, ID: source, Generation: generation},
	}
	if forwardMode {
		entry.spec.CloudflareForward = &ingress.CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: config.ForwardDomain}
		entry.forward = &forwardFixtureSync{}
	}
	sync := &ingressRuntime{store: data, entries: map[string]ingressEntry{source: entry}}
	if forwardMode {
		// Force a fresh projection, so the native test proves the pending path
		// actually opens this mailbox rather than reusing an applied snapshot.
		updated := resources[0]
		updated.Version = directFixtureID(t)
		directory.mu.Lock()
		directory.resources[updated.ID] = updated
		directory.mu.Unlock()
		before, e := data.MailboxProjection(ctx, updated.ID)
		if e != nil {
			t.Fatal(e)
		}
		staged, e := data.StageMailbox(ctx, updated, before.Revision)
		if e != nil || staged.Applied || staged.Revision == before.Revision {
			t.Fatal("pending fixture did not stage a fresh projection", e)
		}
		coordinator := mailCoordinator{store: data, directory: directory, dms: backend, sources: sync}
		e = coordinator.reconcileMailbox(ctx, identity.MailboxReference{ID: updated.ID, OwnerAnchor: updated.OwnerAnchor})
		projection, readErr := data.MailboxProjection(ctx, updated.ID)
		if !errors.Is(e, ingress.ErrDestinationPending) || readErr != nil || !projection.Applied || projection.Mailbox.State != "active" {
			t.Fatal("native forward pending projection not locally ready", e, readErr)
		}
	} else if err = sync.Synchronize(ctx, nil); err != nil {
		t.Fatal("VPS direct route coordinator", err)
	}
	state, err := client.ReadRoutes(ctx)
	if err != nil || len(state.Bindings) != len(candidates) {
		t.Fatal("VPS routes readback", err)
	}
	for _, candidate := range candidates {
		matched := false
		for _, b := range state.Bindings {
			if b.Address == candidate.address.Address && b.MailboxID == candidate.resource.ID && b.OwnerAnchor == candidate.resource.OwnerAnchor && b.ClaimVersion == candidate.address.Version && b.ClosedSequence == nil {
				matched = true
			}
		}
		if !matched {
			t.Fatal("VPS route identity mismatch")
		}
	}
	t.Cleanup(func() {
		clean, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		for _, q := range []string{`DELETE FROM mail.ingress_notice WHERE source_id=$1`, `DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(clean, q, source); e != nil {
				t.Error("fixture cleanup", e)
			}
		}
	})
	native := &countedNativeIntake{Client: backend}
	stage := filepath.Join(root, "stage")
	if err = os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	consumer := &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: stage, SourceID: source, Generation: generation}
	runCtx, stop := context.WithCancel(t.Context())
	consumerDone := make(chan error, 1)
	go func() { consumerDone <- consumer.Run(runCtx, wake) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-consumerDone:
		case <-time.After(15 * time.Second):
			t.Error("consumer stop timeout")
		}
	})
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("relay trust")
	}
	smtpClient, err := smtp.Dial(smtpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer smtpClient.Close()
	if err = smtpClient.StartTLS(&tls.Config{RootCAs: roots, ServerName: "relay.example.test", MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatal(err)
	}
	subjects := make([]string, 0, 3)
	for i, c := range candidates {
		subject := fmt.Sprintf("ANAS VPS native %s %d", source[:8], i)
		subjects = append(subjects, subject)
		body := "From: probe@mail.mailtest.invalid\r\nTo: forged@foreign.example\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=anas-native-ingress\r\n\r\n--anas-native-ingress\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nNative ingress body\r\n--anas-native-ingress\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=proof.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/w==\r\n--anas-native-ingress--\r\n"
		if err = smtpClient.Mail("probe@mail.mailtest.invalid"); err != nil {
			t.Fatal(err)
		}
		recipient := c.address.Address
		if forwardMode {
			var found bool
			for _, binding := range state.Bindings {
				if binding.Address == c.address.Address && binding.ClosedSequence == nil {
					recipient, err = ingress.ForwardTarget(source, generation, binding.Route, config.ForwardDomain)
					if err != nil {
						t.Fatal(err)
					}
					found = true
				}
			}
			if !found {
				t.Fatal("forward target missing")
			}
		}
		if err = smtpClient.Rcpt(recipient); err != nil {
			t.Fatal(err)
		}
		w, e := smtpClient.Data()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = io.WriteString(w, body); e != nil {
			t.Fatal(e)
		}
		if e = w.Close(); e != nil {
			t.Fatal(e)
		}
	}
	_ = smtpClient.Quit()
	deadline = time.Now().Add(120 * time.Second)
	for {
		var completed, accepted int
		err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, source).Scan(&completed)
		if err != nil {
			t.Fatal(err)
		}
		err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_notice WHERE source_id=$1 AND state='accepted'`, source).Scan(&accepted)
		if err != nil {
			t.Fatal(err)
		}
		if completed == 3 && accepted == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("VPS native import did not converge: complete=%d notices=%d submissions=%d", completed, accepted, native.posts.Load())
		}
		time.Sleep(time.Second)
	}
	if native.posts.Load() != 3 {
		t.Fatal("native duplicate submission", native.posts.Load())
	}
	page, err := client.List(ctx, "")
	if err != nil || len(page.Messages) != 3 {
		t.Fatal("VPS delivery status", err)
	}
	for _, m := range page.Messages {
		if m.Delivery == nil || m.Notification == nil || m.Notification.State != "notified" {
			t.Fatal("delivery or notification not recorded")
		}
	}
	if err = consumer.Poll(ctx); err != nil {
		t.Fatal("repeat poll", err)
	}
	if err = consumer.Work(ctx); err != nil || native.posts.Load() != 3 {
		t.Fatal("replay repeated native delivery", err)
	}
	if os.Getenv("MAIL_NATIVE_VPS_OFFLINE_TEST") == "1" {
		// Stop the notifier and restart the real consumer without a wake channel.
		// Its first scan must complete before the new message is submitted, so
		// only its subsequent one-minute scan can discover that message.
		stop()
		select {
		case <-consumerDone:
		case <-time.After(15 * time.Second):
			t.Fatal("consumer did not stop for offline test")
		}
		notify.Close()
		var previous time.Time
		if err := data.DB.QueryRowContext(ctx, `SELECT last_success FROM mail.ingress_poll WHERE source_id=$1`, source).Scan(&previous); err != nil {
			t.Fatal("previous poll unavailable", err)
		}
		runCtx, stop = context.WithCancel(t.Context())
		defer stop()
		consumerDone = make(chan error, 1)
		go func() { consumerDone <- consumer.Run(runCtx, nil) }()
		deadline = time.Now().Add(20 * time.Second)
		for {
			var scanned bool
			err = data.DB.QueryRowContext(ctx, `SELECT COALESCE(last_success > $2,false) FROM mail.ingress_poll WHERE source_id=$1`, source, previous).Scan(&scanned)
			if err == nil && scanned {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("restarted consumer did not complete initial scan", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		subject := fmt.Sprintf("ANAS VPS offline %s", source[:8])
		body := "From: probe@mail.mailtest.invalid\r\nTo: forged@foreign.example\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=anas-native-ingress\r\n\r\n--anas-native-ingress\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nNative ingress body\r\n--anas-native-ingress\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=proof.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/w==\r\n--anas-native-ingress--\r\n"
		smtpClient, err := smtp.Dial(smtpAddr)
		if err != nil {
			t.Fatal(err)
		}
		defer smtpClient.Close()
		if err = smtpClient.StartTLS(&tls.Config{RootCAs: roots, ServerName: "relay.example.test", MinVersion: tls.VersionTLS12}); err != nil {
			t.Fatal(err)
		}
		if err = smtpClient.Mail("probe@mail.mailtest.invalid"); err != nil {
			t.Fatal(err)
		}
		recipient := candidates[0].address.Address
		if forwardMode {
			for _, binding := range state.Bindings {
				if binding.Address == recipient && binding.ClosedSequence == nil {
					recipient, err = ingress.ForwardTarget(source, generation, binding.Route, config.ForwardDomain)
					if err != nil {
						t.Fatal(err)
					}
					break
				}
			}
		}
		if err = smtpClient.Rcpt(recipient); err != nil {
			t.Fatal(err)
		}
		w, err := smtpClient.Data()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		_ = smtpClient.Quit()
		time.Sleep(5 * time.Second)
		page, err = client.List(ctx, "")
		if err != nil || len(page.Messages) != 4 || native.posts.Load() != 3 {
			t.Fatal("offline message was not retained before periodic scan", err)
		}
		var pending bool
		for _, m := range page.Messages {
			if m.Delivery == nil && m.Notification != nil && m.Notification.State != "notified" && m.Notification.Attempts > 0 {
				pending = true
			}
		}
		if !pending {
			t.Fatal("unconfirmed notification not retained")
		}
		deadline = time.Now().Add(80 * time.Second)
		for native.posts.Load() < 4 && time.Now().Before(deadline) {
			time.Sleep(time.Second)
		}
		if native.posts.Load() != 4 {
			t.Fatal("periodic scan did not deliver offline message")
		}
		for {
			page, err = client.List(ctx, "")
			complete := 0
			if err == nil {
				for _, m := range page.Messages {
					if m.Delivery != nil {
						complete++
					}
				}
			}
			if complete == 4 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("offline delivery acknowledgement missing", err)
			}
			time.Sleep(time.Second)
		}
		if err = consumer.Poll(ctx); err != nil {
			t.Fatal("repeat offline poll", err)
		}
		if err = consumer.Work(ctx); err != nil || native.posts.Load() != 4 {
			t.Fatal("offline replay repeated native delivery", err)
		}
		subjects = append(subjects, subject)
	}
	return subjects
}

// Check both delivered folders in the other mailbox; a positive target alone
// cannot rule out an accidental second delivery to another mailbox.
func nativeVPSSubjectAbsent(t *testing.T, conn net.Conn, subject string) {
	t.Helper()
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	for _, folder := range []string{"INBOX", "Junk"} {
		for _, command := range []string{"SELECT " + folder, "UID SEARCH HEADER Subject " + strconv.Quote(subject)} {
			if _, err := fmt.Fprintf(conn, "ia %s\r\n", command); err != nil {
				t.Fatal(err)
			}
			done := false
			for range 100 {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(line, "* SEARCH") && len(strings.Fields(line)) != 2 {
					t.Fatal("message appeared in another mailbox")
				}
				if strings.HasPrefix(line, "ia ") {
					if !strings.HasPrefix(line, "ia OK") {
						t.Fatal("other mailbox check failed")
					}
					done = true
					break
				}
			}
			if !done {
				t.Fatal("other mailbox check unfinished")
			}
		}
	}
}
