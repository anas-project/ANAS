// TEST_CASES: MAILMOD-T-007
package dms

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// Opt-in security observation: passing means the current API confirmed an
// altered message. This must never be counted as a passing security criterion.
func TestDMSNativeReceiptTraceCounterexample(t *testing.T) {
	if os.Getenv("MAIL_DMS_TRACE_COUNTEREXAMPLE") != "1" || os.Getenv("MAIL_DMS_NATIVE_CONFIG") != "/input/native.json" {
		t.Skip("explicit isolated native trace counterexample required")
	}
	var cfg struct{ Token, CAFile string }
	raw, err := os.ReadFile("/input/native.json")
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("private counterexample configuration unavailable")
	}
	pem, err := os.ReadFile(cfg.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("counterexample trust unavailable")
	}
	client, err := NewClient(ClientConfig{URL: "https://mail.mailtest.invalid:9443", ServiceToken: cfg.Token}, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	_, in := fixture(t)
	in.Projection.Mailbox.ID = "e0e0e0e0-e0e0-40e0-80e0-e0e0e0e0e0e0"
	in.Projection.Mailbox.OwnerAnchor = "e1e1e1e1-e1e1-41e1-81e1-e1e1e1e1e1e1"
	in.Projection.Mailbox.ProtocolLogin = "mbx_" + strings.Repeat("1", 32)
	in.Projection.Mailbox.StorageRef = "store_" + strings.Repeat("2", 32)
	in.Projection.Mailbox.RoundcubeKey = "webmail_" + strings.Repeat("3", 32)
	in.Projection.Mailbox.DefaultAddress = "trace-counterexample@example.test"
	in.Projection.Resource.ID, in.Projection.Resource.OwnerAnchor = in.Projection.Mailbox.ID, in.Projection.Mailbox.OwnerAnchor
	in.Projection.Resource.DefaultAddress = in.Projection.Mailbox.DefaultAddress
	in.Projection.Resource.Addresses[0].Address = in.Projection.Mailbox.DefaultAddress
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
			t.Fatal("counterexample target unavailable", err)
		}
		time.Sleep(time.Second)
	}
	user := in.Projection.Mailbox.ProtocolLogin + "@" + ProtocolRealm
	const original = "From: probe@example.test\r\nTo: trace-counterexample@example.test\r\nSubject: isolated-trace-original\r\n\r\nOriginal synthetic content.\r\n"
	sum := sha256.Sum256([]byte(original))
	m := IntakeMetadata{SourceID: "e2e2e2e2-e2e2-42e2-82e2-e2e2e2e2e2e2", MessageID: "isolated-trace-counterexample", MailboxID: in.Projection.Mailbox.ID, OwnerAnchor: in.Projection.Mailbox.OwnerAnchor, AcceptedAddress: in.Projection.Mailbox.DefaultAddress, ClaimVersion: in.Projection.Resource.Version, SHA256: hex.EncodeToString(sum[:])}
	if m.Validate() != nil || !nativeIntakeProtected(t.Context()) {
		t.Fatal("counterexample metadata or native policy invalid")
	}
	run := func(ctx context.Context, name string, args ...string) (string, error) { return commandWithin(ctx, 15*time.Second, name, args...) }
	prior, err := run(t.Context(), "/usr/sbin/postconf", "-h", "defer_transports")
	if err != nil || strings.TrimSpace(prior) != "" {
		t.Fatal("unexpected native defer policy")
	}
	var queueID string
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if queueID != "" {
			if _, err := run(ctx, "/usr/sbin/postsuper", "-d", queueID); err != nil {
				t.Error("synthetic held queue cleanup failed")
			}
		}
		if _, err := run(ctx, "/usr/sbin/postconf", "-e", "defer_transports ="); err != nil {
			t.Error("native defer policy restoration failed")
		}
		if _, err := run(ctx, "/usr/sbin/postfix", "reload"); err != nil {
			t.Error("native policy reload failed")
		}
	})
	if _, err = run(t.Context(), "/usr/sbin/postconf", "-e", "defer_transports = lmtp"); err != nil {
		t.Fatal("cannot delay synthetic original")
	}
	if _, err = run(t.Context(), "/usr/sbin/postfix", "reload"); err != nil {
		t.Fatal("cannot load temporary delay")
	}
	receipt, err := client.Intake(t.Context(), m, strings.NewReader(original))
	if !errors.Is(err, ErrUnavailable) || receipt.State != "queued" {
		t.Fatal("original was not provably queued", receipt.State, receipt.Check)
	}
	dir := filepath.Join("/var/mail-state/anas-control/intake", m.Key())
	journal, err := loadIntake(dir)
	if err != nil || journal.Metadata != m || !postfixQueueID.MatchString(journal.QueueID) {
		t.Fatal("actual private intent unavailable")
	}
	queueID = journal.QueueID
	if _, err = run(t.Context(), "/usr/sbin/postsuper", "-h", queueID); err != nil {
		t.Fatal("cannot hold synthetic original")
	}
	if _, err = run(t.Context(), "/usr/sbin/postconf", "-e", "defer_transports ="); err != nil {
		t.Fatal("cannot restore normal transport")
	}
	if _, err = run(t.Context(), "/usr/sbin/postfix", "reload"); err != nil {
		t.Fatal("cannot reload normal transport")
	}
	const attack = `import os,pwd,re,smtplib,subprocess,sys
p=pwd.getpwnam('postfix');os.setgroups([]);os.setgid(p.pw_gid);os.setuid(p.pw_uid)
r=subprocess.run(['/usr/sbin/postcat','-h','-q',sys.argv[1]],stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True)
m=re.search(rb'(?m)^X-ANAS-Ingress-Key: ([a-f0-9]{64})\r?$',r.stdout)
if not m:raise SystemExit('synthetic trace unavailable')
body=b'From: probe@example.test\r\nTo: '+sys.argv[2].encode()+b'\r\nX-ANAS-Ingress-Key: '+m.group(1)+b'\r\nSubject: isolated-trace-forged\r\n\r\nAltered synthetic content.\r\n'
with smtplib.SMTP('127.0.0.1',2525,timeout=20) as c:
 c.ehlo('localhost.localdomain')
 if c.sendmail('',[sys.argv[2]],body):raise SystemExit('synthetic submission refused')
`
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err = exec.CommandContext(ctx, "/usr/bin/python3", "-c", attack, queueID, user).Run(); err != nil {
		t.Fatal("same-UID actual intake submission unconfirmed")
	}
	deadline = time.Now().Add(45 * time.Second)
	for {
		out, e := run(t.Context(), "/usr/bin/doveadm", "-f", "json", "fetch", "-u", user, "mailbox text", "HEADER", intakeHeader, m.Key())
		var messages []struct{ Mailbox, Text string }
		if e == nil && json.Unmarshal([]byte(out), &messages) == nil && len(messages) == 1 && strings.Contains(messages[0].Text, "Subject: isolated-trace-forged") && strings.Contains(messages[0].Text, "Altered synthetic content.") && !strings.Contains(messages[0].Text, "Original synthetic content.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("altered message not observed in actual target")
		}
		time.Sleep(250 * time.Millisecond)
	}
	receipt, err = client.IntakeStatus(t.Context(), m.Key())
	if err != nil || receipt.State != "stored" || receipt.SHA256 != m.SHA256 {
		t.Fatal("actual API did not confirm altered message", receipt.State)
	}
	retained, err := os.ReadFile(filepath.Join(dir, "raw.eml"))
	if err != nil || string(retained) != original {
		t.Fatal("private original changed or disappeared")
	}
	t.Log("OBSERVATION: actual intake API confirmed altered mailbox message with original digest while original remained held")
}
