// TEST_CASES: MAILMOD-T-015
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

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// Real DMS/native filtering and both mail stores; address changes are supplied
// through the trusted test control client. It does not prove AD authorization.
func TestDMSNativeTransferredAddressRejectKeepsOldContentPrivate(t *testing.T) {
	path := os.Getenv("MAIL_DMS_NATIVE_CONFIG")
	if path == "" || os.Getenv("MAIL_DMS_TRANSFER_REJECT_TEST") != "1" {
		t.Skip("explicit isolated native transfer/rejection fixture required")
	}
	var cfg struct{ Token, CAFile string }
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("native transfer binding unavailable")
	}
	pem, err := os.ReadFile(cfg.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("native transfer trust unavailable")
	}
	client, err := NewClient(ClientConfig{URL: "https://mail.mailtest.invalid:9443", ServiceToken: cfg.Token}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	alias := "handoff@manage.mailtest.invalid"
	_, old := fixture(t)
	old.Projection.Resource.DefaultAddress = "remaining@manage.mailtest.invalid"
	old.Projection.Mailbox.DefaultAddress = old.Projection.Resource.DefaultAddress
	old.Projection.Resource.Addresses[0].Address = old.Projection.Resource.DefaultAddress
	old.Projection.Resource.Addresses = append(old.Projection.Resource.Addresses, identity.AddressResource{Address: alias, Kind: "alias", State: "active", Version: old.Projection.Resource.Version, OperationID: old.Projection.Resource.OperationID, OperationState: "complete"})
	old.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(old.Projection.Resource)
	apply := func(label string, request ApplyRequest) {
		t.Helper()
		deadline := time.Now().Add(75 * time.Second)
		for {
			receipt, err := client.Apply(t.Context(), request)
			if err == nil {
				if !receipt.Applied || receipt.Revision != request.Projection.Revision || receipt.Fingerprint != request.Projection.Fingerprint {
					t.Fatal(label, "native receipt mismatch")
				}
				return
			}
			if !errors.Is(err, ErrUnavailable) || time.Now().After(deadline) {
				t.Fatal(label, "native configuration not confirmed", err)
			}
			// DMS rebuilds its native maps asynchronously. Repeat the exact
			// persisted operation; a 503 is not an applied receipt.
			time.Sleep(time.Second)
		}
	}
	apply("original mailbox", old)
	// This metadata models a message already accepted by the old cloud route.
	marker := "ANAS private pre-transfer source payload"
	body := "From: " + alias + "\r\nTo: " + alias + "\r\nSubject: " + marker + "\r\nMessage-ID: <transfer-before@manage.mailtest.invalid>\r\n\r\nPrivate original content must remain bound to the old mailbox.\r\n"
	sum := sha256.Sum256([]byte(body))
	metadata := IntakeMetadata{SourceID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", MessageID: "before-transfer", MailboxID: old.Projection.Mailbox.ID, OwnerAnchor: old.Projection.Mailbox.OwnerAnchor, AcceptedAddress: alias, ClaimVersion: old.Projection.Resource.Addresses[1].Version, EnvelopeFrom: alias, SHA256: hex.EncodeToString(sum[:])}
	retired := nextRequest(t, old, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	retired.Projection.Resource.Addresses[1].State = "retained"
	retired.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(retired.Projection.Resource)
	apply("old address withdrawal", retired)
	_, next := fixture(t)
	next.Projection.Mailbox = store.Mailbox{ID: "11111111-1111-4111-8111-111111111111", OwnerAnchor: "22222222-2222-4222-8222-222222222222", ProtocolLogin: "mbx_" + strings.Repeat("d", 32), StorageRef: "store_" + strings.Repeat("e", 32), RoundcubeKey: "webmail_" + strings.Repeat("f", 32), DefaultAddress: alias}
	next.Projection.Resource = identity.MailboxResource{ID: next.Projection.Mailbox.ID, OwnerAnchor: next.Projection.Mailbox.OwnerAnchor, Kind: "primary", DefaultAddress: alias, State: "active", Version: next.Projection.Mailbox.ID, OperationID: next.Projection.Mailbox.OwnerAnchor, OperationState: "complete", Addresses: []identity.AddressResource{{Address: alias, Kind: "primary", State: "active", Version: next.Projection.Mailbox.ID, OperationID: next.Projection.Mailbox.OwnerAnchor, OperationState: "complete"}}}
	next.Projection.Revision = next.Projection.Mailbox.OwnerAnchor
	next.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(next.Projection.Resource)
	apply("new owner", next)
	// AD no longer returns the moved claim in the old box. Reconcile that
	// current snapshot too, preserving all three old mailbox keys.
	oldCurrent := nextRequest(t, retired, "33333333-3333-4333-8333-333333333333")
	oldCurrent.Projection.Resource.Addresses = oldCurrent.Projection.Resource.Addresses[:1]
	oldCurrent.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(oldCurrent.Projection.Resource)
	apply("old mailbox after directory transfer", oldCurrent)
	const config = "/etc/dovecot/conf.d/98-anas-transfer-reject-test.conf"
	const script = "/var/mail-state/anas-transfer-reject-test.sieve"
	for _, name := range []string{config, script} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native rejection fixture path already occupied")
		}
	}
	t.Cleanup(func() {
		os.Remove(config)
		os.Remove(script)
		os.Remove(strings.TrimSuffix(script, ".sieve") + ".svbin")
		command(context.Background(), "/usr/sbin/dovecot", "reload")
	})
	if err = os.WriteFile(script, []byte(`require ["reject"]; if header :is "Subject" "`+marker+`" { reject "ANAS isolated old-target rejection"; stop; }`), 0600); err != nil || os.Chown(script, 5000, 5000) != nil {
		t.Fatal("native rejection filter could not be installed")
	}
	if err = os.WriteFile(config, []byte("sieve_script anas_transfer_reject_test {\n type = before\n path = "+script+"\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"/usr/bin/doveconf", "-n"}, {"/usr/bin/sievec", script}, {"/usr/sbin/dovecot", "reload"}} {
		if _, err = command(t.Context(), args[0], args[1:]...); err != nil {
			t.Fatal("native rejection filter failed validation", err)
		}
	}
	compiled := strings.TrimSuffix(script, ".sieve") + ".svbin"
	if os.Chown(compiled, 5000, 5000) != nil || os.Chmod(compiled, 0600) != nil {
		t.Fatal("compiled native rejection filter unavailable")
	}
	newUser := next.Projection.Mailbox.ProtocolLogin + "@" + ProtocolRealm
	// Prove that the reused literal address now really delivers to the new box.
	canary := exec.CommandContext(t.Context(), "/usr/sbin/sendmail", "-i", "-f", "probe@mail.mailtest.invalid", alias)
	canary.Stdin = strings.NewReader("From: probe@mail.mailtest.invalid\nTo: " + alias + "\nSubject: ANAS new-address routing canary\n\nNative routing proof.\n")
	if canary.Run() != nil {
		t.Fatal("new-address native canary submission failed")
	}
	for deadline := time.Now().Add(25 * time.Second); ; {
		mail, _ := command(t.Context(), "/usr/bin/doveadm", "fetch", "-u", newUser, "text", "ALL")
		if strings.Contains(mail, "ANAS new-address routing canary") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new native owner did not receive the routing canary")
		}
		time.Sleep(200 * time.Millisecond)
	}
	initial, intakeErr := client.Intake(t.Context(), metadata, strings.NewReader(body))
	t.Logf("native intake state=%q check=%q smtp=%d error=%v", initial.State, initial.Check, initial.SMTPCode, intakeErr)
	rejected := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		receipt, _ := client.IntakeStatus(t.Context(), metadata.Key())
		if receipt.State == "rejected" || receipt.State == "processed" {
			rejected = true
		}
		mail, _ := command(t.Context(), "/usr/bin/doveadm", "fetch", "-u", newUser, "text", "ALL")
		if strings.Contains(mail, marker) {
			t.Fatal("late native rejection returned pre-transfer content to the new address owner")
		}
		time.Sleep(250 * time.Millisecond)
	}
	journal, err := loadIntake(filepath.Join("/var/mail-state/anas-control/intake", metadata.Key()))
	filterObserved := false
	for _, line := range strings.Split(intakeLogs(), "\n") {
		if strings.Contains(line, "msgid=<transfer-before@manage.mailtest.invalid>") && (strings.Contains(line, "reject") || strings.Contains(line, "discarded")) {
			filterObserved = true
		}
	}
	oldUser := old.Projection.Mailbox.ProtocolLogin + "@" + ProtocolRealm
	oldCopies, oldErr := command(t.Context(), "/usr/bin/doveadm", "search", "-u", oldUser, "HEADER", intakeHeader, metadata.Key())
	if err != nil || journal.Metadata != metadata || journal.QueueID == "" || !rejected || !filterObserved || oldErr != nil || strings.TrimSpace(oldCopies) != "" {
		t.Fatalf("actual queued old-target rejection was not observed; no privacy pass: journal=%v state=%q check=%q smtp=%d queued=%t rejected=%t", err, journal.State, journal.Check, journal.SMTPCode, journal.QueueID != "", rejected)
	}
	if _, err = os.Stat(filepath.Join("/var/mail-state/anas-control/intake", metadata.Key(), "raw.eml")); err != nil {
		t.Fatal("unconfirmed original was not retained")
	}
}
