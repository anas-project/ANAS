// TEST_CASES: MAILMOD-T-007 MAILMOD-T-021
package dms

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/anas-project/ANAS/modules/mail/store"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The actual private HTTP adapter, native SMTP queue, Dovecot storage/Sieve and
// hard quota. Cloud, IAM and source authorization are not part of this fixture.
func TestDMSNativeIntake(t *testing.T) {
	file := os.Getenv("MAIL_DMS_NATIVE_CONFIG")
	if file == "" {
		t.Skip("explicit native DMS fixture required; no real-host pass")
	}
	var cfg struct {
		Token, CAFile string
		IntakeQuota   bool
	}
	raw, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("native intake config unavailable")
	}
	pem, err := os.ReadFile(cfg.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("native trust unavailable")
	}
	client, err := NewClient(ClientConfig{URL: "https://mail.mailtest.invalid:9443", ServiceToken: cfg.Token}, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	_, in := fixture(t)
	id := "f0f0f0f0-f0f0-40f0-80f0-f0f0f0f0f0f0"
	owner := "f1f1f1f1-f1f1-41f1-81f1-f1f1f1f1f1f1"
	in.Projection.Mailbox.ID = id
	in.Projection.Mailbox.OwnerAnchor = owner
	in.Projection.Mailbox.ProtocolLogin = "mbx_" + strings.Repeat("d", 32)
	in.Projection.Mailbox.StorageRef = "store_" + strings.Repeat("e", 32)
	in.Projection.Mailbox.RoundcubeKey = "webmail_" + strings.Repeat("f", 32)
	in.Projection.Resource.ID = id
	in.Projection.Resource.OwnerAnchor = owner
	in.Projection.Resource.DefaultAddress = "native-intake@example.test"
	in.Projection.Mailbox.DefaultAddress = in.Projection.Resource.DefaultAddress
	in.Projection.Resource.Addresses[0].Address = in.Projection.Resource.DefaultAddress
	in.Projection.Fingerprint, _, err = store.ProjectionFingerprint(in.Projection.Resource)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(75 * time.Second)
	for {
		if _, err = client.Apply(t.Context(), in); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native intake target not applied", err)
		}
		time.Sleep(time.Second)
	}
	user := in.Projection.Mailbox.ProtocolLogin + "@" + ProtocolRealm
	limit, err := command(t.Context(), "/usr/sbin/postconf", "-h", "message_size_limit")
	if err != nil || strings.TrimSpace(limit) != "27262976" {
		t.Fatal("native message size is below the fixed 25 MiB intake plus transport headers")
	}
	metadata := func(label, body string) IntakeMetadata {
		sum := sha256.Sum256([]byte(body))
		return IntakeMetadata{SourceID: "f2f2f2f2-f2f2-42f2-82f2-f2f2f2f2f2f2", MessageID: label, MailboxID: id, OwnerAnchor: owner, AcceptedAddress: in.Projection.Resource.DefaultAddress, ClaimVersion: in.Projection.Resource.Version, EnvelopeFrom: "sender@mail.mailtest.invalid", SHA256: hex.EncodeToString(sum[:])}
	}
	message := func(subject string) string {
		return "From: sender@mail.mailtest.invalid\r\nTo: forged@foreign.example\r\nX-ANAS-Ingress-Key: forged\r\n folded-forgery\r\nMessage-ID: <same-message-id@example.net>\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=intake\r\n\r\n--intake\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n实际收件正文\r\n--intake\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\nAAECA/8=\r\n--intake--\r\n"
	}
	wait := func(t *testing.T, m IntakeMetadata) IntakeReceipt {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		var receipt IntakeReceipt
		var err error
		for time.Now().Before(deadline) {
			receipt, err = client.IntakeStatus(t.Context(), m.Key())
			if err == nil {
				return receipt
			}
			if receipt.State == "retryable" || receipt.State == "rejected" {
				t.Fatal("native SMTP submission did not accept the fixture", receipt.State, receipt.Check, receipt.SMTPCode)
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("native intake delivery unconfirmed", receipt.State, receipt.Check, receipt.SMTPCode, err)
		return receipt
	}
	fetch := func(t *testing.T, m IntakeMetadata) []struct{ Mailbox, Text string } {
		t.Helper()
		out, err := command(t.Context(), "/usr/bin/doveadm", "-f", "json", "fetch", "-u", user, "mailbox text", "HEADER", intakeHeader, m.Key())
		var messages []struct{ Mailbox, Text string }
		if err != nil || json.Unmarshal([]byte(out), &messages) != nil {
			t.Fatal("native intake mailbox readback unavailable", err)
		}
		return messages
	}
	t.Run("missing protection blocks submission and receipt confirmation", func(t *testing.T) {
		if !nativeIntakeProtected(t.Context()) {
			t.Fatal("native SMTP protection not configured")
		}
		const path = "/etc/postfix/anas-intake-trusted-headers"
		const held = path + ".native-test-held"
		if _, err := os.Lstat(held); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native protection fault fixture already exists")
		}
		if err := os.Rename(path, held); err != nil {
			t.Fatal("cannot inject missing native protection", err)
		}
		defer func() {
			if err := os.Rename(held, path); err != nil {
				t.Error("native protection fixture could not be restored", err)
			}
		}()
		m := metadata("missing-protection", message("missing protection"))
		r := intakeRecord{Metadata: m, State: "queued"}
		if got := nativeIntakeSubmit(t.Context(), r, user, strings.NewReader(message("missing protection"))); got.State != "retryable" {
			t.Fatal("missing protection allowed cloud submission", got.State)
		}
		if state, _ := nativeIntakeObserve(t.Context(), r, user); state != "unknown" {
			t.Fatal("missing protection allowed a receipt confirmation", state)
		}
	})
	t.Run("filter outage defers intake without storing mail and recovery succeeds", func(t *testing.T) {
		const key = "127.0.0.1:2525/inet/smtpd_milters"
		original, err := command(t.Context(), "/usr/sbin/postconf", "-h", "smtpd_milters")
		if err != nil || strings.TrimSpace(original) == "" || !nativeIntakeProtected(t.Context()) {
			t.Fatal("native filtering protection unavailable")
		}
		expanded, err := command(t.Context(), "/usr/sbin/postconf", "-xh", "smtpd_milters")
		if err != nil {
			t.Fatal("filter configuration unavailable")
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		endpoint := "inet:" + listener.Addr().String()
		// Keep the port occupied but close every accepted connection before the
		// Milter handshake, avoiding dependence on an unrelated unused port.
		go func() {
			for {
				c, e := listener.Accept()
				if e != nil {
					return
				}
				c.Close()
			}
		}()
		defer listener.Close()
		restore := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, args := range [][]string{{"/usr/sbin/postconf", "-e", "smtpd_milters=" + strings.TrimSpace(original)}, {"/usr/sbin/postconf", "-P", key + "=" + strings.TrimSpace(expanded)}, {"/usr/sbin/postfix", "reload"}} {
				if _, err := command(ctx, args[0], args[1:]...); err != nil {
					t.Error("filter restoration failed", err)
				}
			}
		}
		defer restore()
		for _, args := range [][]string{{"/usr/sbin/postconf", "-e", "smtpd_milters=" + endpoint}, {"/usr/sbin/postconf", "-P", key + "=" + endpoint}, {"/usr/sbin/postfix", "reload"}} {
			if _, err := command(t.Context(), args[0], args[1:]...); err != nil {
				t.Fatal("cannot inject isolated filter failure", err)
			}
		}
		raw := message("temporary filter failure")
		m := metadata("native-filter-outage", raw)
		result := nativeIntakeSubmit(t.Context(), intakeRecord{Metadata: m}, user, strings.NewReader(raw))
		if result.State != "retryable" || result.SMTPCode < 400 || result.SMTPCode >= 500 {
			t.Fatal("filter outage did not produce an explicit temporary refusal", result.State, result.Check, result.SMTPCode)
		}
		if len(fetch(t, m)) != 0 {
			t.Fatal("unfiltered message reached mailbox")
		}
		restore()
		if !nativeIntakeProtected(t.Context()) {
			t.Fatal("filter protection not restored")
		}
		m = metadata("native-filter-recovered", raw)
		if _, err := client.Intake(t.Context(), m, strings.NewReader(raw)); err != nil && !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if receipt := wait(t, m); receipt.State != "stored" {
			t.Fatal("recovered filter did not deliver", receipt.State)
		}
		if len(fetch(t, m)) != 1 {
			t.Fatal("recovered filter message missing or duplicated")
		}
	})
	t.Run("external SMTP cannot forge a cloud intake receipt", func(t *testing.T) {
		original := message("never submitted cloud original")
		m := metadata("forged-trace-test", original)
		spoof := "From: attacker@external.example.test\r\nTo: " + in.Projection.Resource.DefaultAddress + "\r\nX-ANAS-Ingress-Key: " + m.Key() + "\r\nx-anas-ingress-key: " + m.Key() + "\r\n folded-forgery\r\nSubject: forged trace\r\n\r\nUntrusted SMTP content.\r\n"
		// Exercise the ordinary SMTP listener without automatic STARTTLS: the
		// isolated Postfix certificate does not name its loopback IP. This also
		// models a remote sender that chooses not to negotiate opportunistic TLS.
		smtpClient, err := smtp.Dial("127.0.0.1:25")
		if err != nil {
			t.Fatal("isolated public SMTP connection failed", err)
		}
		defer smtpClient.Close()
		if err = smtpClient.Hello("attacker.external.example.test"); err != nil {
			t.Fatal("isolated public SMTP greeting failed", err)
		}
		// A null reverse path is valid external SMTP (for example, a DSN) and
		// avoids relying on public DNS inside the isolated network.
		if err = smtpClient.Mail(""); err == nil {
			err = smtpClient.Rcpt(in.Projection.Resource.DefaultAddress)
		}
		if err != nil {
			t.Fatal("isolated public SMTP envelope failed", err)
		}
		data, err := smtpClient.Data()
		if err != nil {
			t.Fatal("isolated public SMTP DATA failed", err)
		}
		if _, err = io.WriteString(data, spoof); err == nil {
			err = data.Close()
		}
		if err != nil {
			t.Fatal("isolated public SMTP submission failed", err)
		}
		// Observe the spoof itself in the mailbox before checking the receipt;
		// an empty queue or delayed index is not proof of safety.
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			found, err := command(t.Context(), "/usr/bin/doveadm", "search", "-u", user, "HEADER", "Subject", "forged trace")
			if err == nil && strings.TrimSpace(found) != "" {
				reserved, err := command(t.Context(), "/usr/bin/doveadm", "search", "-u", user, "HEADER", intakeHeader, m.Key())
				if err != nil || strings.TrimSpace(reserved) != "" {
					t.Fatal("untrusted reserved headers survived SMTP cleanup")
				}
				state, _ := nativeIntakeObserve(t.Context(), intakeRecord{Metadata: m}, user)
				if state == "stored" || state == "processed" {
					t.Fatal("untrusted SMTP message forged a cloud intake receipt")
				}
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("isolated public SMTP processing did not complete")
	})
	t.Run("normal delivery keeps trusted target original MIME and attachment", func(t *testing.T) {
		body := message("ANAS normal intake")
		m := metadata("normal-1", body)
		if _, err := client.Intake(t.Context(), m, strings.NewReader(body)); err != nil && !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if r := wait(t, m); r.State != "stored" && r.State != "processed" {
			t.Fatal("invalid actual receipt")
		}
		messages := fetch(t, m)
		if len(messages) != 1 || messages[0].Mailbox != "INBOX" && messages[0].Mailbox != "Junk" {
			t.Fatal("trusted target did not receive one message under native classification")
		}
		original, _ := mail.ReadMessage(strings.NewReader(body))
		delivered, err := mail.ReadMessage(strings.NewReader(messages[0].Text))
		if err != nil {
			t.Fatal("stored MIME invalid")
		}
		left, _ := io.ReadAll(original.Body)
		right, _ := io.ReadAll(delivered.Body)
		normalize := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
		if normalize(left) != normalize(right) || delivered.Header.Get(intakeHeader) != m.Key() || strings.Contains(messages[0].Text, "folded-forgery") {
			t.Fatal("payload or trusted trace changed")
		}
		saved, err := os.ReadFile(filepath.Join("/var/mail-state/anas-control/intake", m.Key(), "raw.eml"))
		if err != nil || string(saved) != body {
			t.Fatal("archived original was modified")
		}
		intentDir := filepath.Join("/var/mail-state/anas-control/intake", m.Key())
		journal, err := loadIntake(intentDir)
		if err != nil || journal.Metadata != m || !intakeID.MatchString(journal.DeliveryToken) {
			t.Fatal("native delivery intent not persisted", err)
		}
		token := journal.DeliveryToken
		intentInfo, err := os.Lstat(filepath.Join(intentDir, "delivery-intent.json"))
		if err != nil || !intentInfo.Mode().IsRegular() || intentInfo.Mode().Perm() != 0600 {
			t.Fatal("native delivery intent is not private", err)
		}
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := client.Intake(t.Context(), m, strings.NewReader(body))
				failures <- err
			}()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal("concurrent duplicate intake", err)
			}
		}
		if len(fetch(t, m)) != 1 {
			t.Fatal("duplicate cloud fetch made another mailbox copy")
		}
		before, err := command(t.Context(), "/usr/bin/supervisorctl", "-c", "/etc/supervisor/supervisord.conf", "pid", "anas-mail-dms")
		if err != nil || strings.TrimSpace(before) == "" {
			t.Fatal("native adapter PID unavailable")
		}
		if _, err = command(t.Context(), "/usr/bin/supervisorctl", "-c", "/etc/supervisor/supervisord.conf", "restart", "anas-mail-dms"); err != nil {
			t.Fatal("native adapter restart failed", err)
		}
		after, err := command(t.Context(), "/usr/bin/supervisorctl", "-c", "/etc/supervisor/supervisord.conf", "pid", "anas-mail-dms")
		if err != nil || strings.TrimSpace(after) == strings.TrimSpace(before) {
			t.Fatal("native adapter did not actually restart")
		}
		journal, err = loadIntake(intentDir)
		if err != nil || journal.Metadata != m || journal.DeliveryToken != token {
			t.Fatal("native restart did not preserve the same private delivery intent", err)
		}
		wait(t, m)
		if _, err = client.Intake(t.Context(), m, strings.NewReader(body)); err != nil || len(fetch(t, m)) != 1 {
			t.Fatal("native restart lost the intake receipt or repeated delivery", err)
		}
		confirmed := wait(t, m)
		for range 2 {
			released, e := client.ReleaseIntake(t.Context(), confirmed)
			if e != nil || !released.RawReleased {
				t.Fatal("native redundant original release failed", e)
			}
		}
		if _, e := os.Lstat(filepath.Join("/var/mail-state/anas-control/intake", m.Key(), "raw.eml")); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("redundant raw remained", e)
		}
		if _, e := client.Intake(t.Context(), m, strings.NewReader(body)); e != nil || len(fetch(t, m)) != 1 {
			t.Fatal("cleanup changed mailbox or deduplication", e)
		}
	})
	const config = "/etc/dovecot/conf.d/98-anas-intake-test.conf"
	const script = "/var/mail-state/anas-intake-test.sieve"
	for _, path := range []string{config, script} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native filter fixture conflicts with existing file")
		}
	}
	t.Cleanup(func() {
		os.Remove(config)
		os.Remove(script)
		command(context.Background(), "/usr/sbin/dovecot", "reload")
	})
	if err := os.WriteFile(script, []byte(`require ["fileinto"]; if header :is "Subject" "ANAS filter intake" { fileinto "IngressFiltered"; stop; } if header :is "Subject" "ANAS discard intake" { discard; stop; }`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(script, 5000, 5000); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("sieve_script anas_intake_test {\n type = before\n path = "+script+"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"/usr/bin/doveadm", "mailbox", "create", "-u", user, "IngressFiltered"}, {"/usr/bin/doveconf", "-n"}, {"/usr/sbin/dovecot", "reload"}} {
		if _, err := command(t.Context(), args[0], args[1:]...); err != nil {
			t.Fatal("native filter configuration failed", err)
		}
	}
	time.Sleep(500 * time.Millisecond)
	t.Run("native Sieve stores in selected folder", func(t *testing.T) {
		body := message("ANAS filter intake")
		m := metadata("filter-1", body)
		client.Intake(t.Context(), m, strings.NewReader(body))
		wait(t, m)
		rows := fetch(t, m)
		if len(rows) != 1 || rows[0].Mailbox != "IngressFiltered" {
			t.Fatal("native Sieve was bypassed")
		}
	})
	t.Run("native discard is processed without claiming inbox storage", func(t *testing.T) {
		body := message("ANAS discard intake")
		m := metadata("discard-1", body)
		client.Intake(t.Context(), m, strings.NewReader(body))
		if r := wait(t, m); r.State != "processed" {
			t.Fatal("discard was falsely called mailbox storage")
		}
		if len(fetch(t, m)) != 0 {
			t.Fatal("native discard was bypassed")
		}
	})
	if cfg.IntakeQuota {
		t.Run("native hard quota preserves source as unconfirmed", func(t *testing.T) {
			body := "From: sender@mail.mailtest.invalid\r\nTo: forged@foreign.example\r\nSubject: ANAS over quota\r\n\r\n" + strings.Repeat("quota-test-body ", 140000)
			m := metadata("quota-1", body)
			receipt, err := client.Intake(t.Context(), m, strings.NewReader(body))
			if err == nil || receipt.State == "stored" || receipt.State == "processed" {
				t.Fatal("over-quota mail marked delivered")
			}
			deadline := time.Now().Add(25 * time.Second)
			found := false
			for time.Now().Before(deadline) {
				q, _ := command(t.Context(), "/usr/sbin/postqueue", "-j")
				if strings.Contains(strings.ToLower(q), "quota") || strings.Contains(strings.ToLower(q), "mailbox is full") {
					found = true
					break
				}
				// Native policy may bounce an oversized message permanently.
				// This still cannot acknowledge the cloud source as delivered.
				journal, _ := loadIntake(filepath.Join("/var/mail-state/anas-control/intake", m.Key()))
				for _, line := range strings.Split(intakeLogs(), "\n") {
					if journal.QueueID != "" && strings.Contains(line, "]: "+journal.QueueID+": ") && (strings.Contains(strings.ToLower(line), "quota") || strings.Contains(strings.ToLower(line), "mailbox is full")) {
						found = true
					}
				}
				if found {
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
			if !found {
				t.Fatal("native quota failure not observed in the managed queue")
			}
			if len(fetch(t, m)) != 0 {
				t.Fatal("quota failed to prevent storage")
			}
		})
	}
}
