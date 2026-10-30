// TEST_CASES: MAILMOD-T-007
package dms

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNativeIntakeRequiresEffectiveFilterAndTemporaryFailure(t *testing.T) {
	const filters = "inet:localhost:11332"
	const prefix = "127.0.0.1:2525/inet/"
	base := prefix + "cleanup_service_name=anas_intake_cleanup\n" +
		prefix + "smtpd_milters=" + filters + "\n" +
		prefix + "milter_default_action=tempfail\n" +
		prefix + "receive_override_options=\n" +
		"anas_intake_cleanup/unix/header_checks=$anas_intake_header_checks\n" +
		"anas_intake_cleanup/unix/syslog_name=postfix\n"
	for _, tc := range []struct {
		name, global, overrides string
		failed, want            bool
	}{
		{name: "protected", global: filters, overrides: base, want: true},
		{name: "accept on filter failure", global: filters, overrides: strings.Replace(base, "=tempfail", "=accept", 1)},
		{name: "empty filters", global: "", overrides: base},
		{name: "empty endpoint list", global: ", ,", overrides: base},
		{name: "unexpanded filters", global: "$rspamd_milter", overrides: base},
		{name: "per-filter accept", global: "{ inet:localhost:11332, default_action=accept }", overrides: base},
		{name: "changed filters", global: "inet:localhost:11333", overrides: base},
		{name: "disabled filtering", global: filters, overrides: strings.Replace(base, "receive_override_options=", "receive_override_options=no_milters", 1)},
		{name: "missing failure policy", global: filters, overrides: strings.Replace(base, prefix+"milter_default_action=tempfail\n", "", 1)},
		{name: "duplicate empty field", global: filters, overrides: base + prefix + "receive_override_options=\n"},
		{name: "unknown empty field", global: filters, overrides: base + "unknown=\n"},
		{name: "read failure", global: filters, overrides: base, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, name string, args ...string) (string, error) {
				if name != "/usr/sbin/postconf" {
					t.Fatal("unexpected command", name)
				}
				if tc.failed {
					return "", errors.New("read failed")
				}
				if args[0] == "-xh" {
					return tc.global, nil
				}
				return tc.overrides, nil
			}
			if got := nativeIntakeFilterProtected(t.Context(), run); got != tc.want {
				t.Fatalf("protected=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestNativeIntakeFailuresKeepSMTPCodeWithoutLeakingReply(t *testing.T) {
	for _, value := range []struct {
		code      int
		uncertain bool
		state     string
	}{{450, false, "retryable"}, {550, false, "rejected"}, {450, true, "retryable"}, {550, true, "rejected"}} {
		result := nativeIntakeFailure("smtp-sender", &textproto.Error{Code: value.code, Msg: "private-reply-value"}, value.uncertain)
		raw, _ := json.Marshal(result)
		if result.State != value.state || result.SMTPCode != value.code || strings.Contains(string(raw), "private-reply-value") {
			t.Fatal("SMTP outcome lost or leaked")
		}
	}
	if result := nativeIntakeFailure("smtp-ack", io.ErrUnexpectedEOF, true); result.State != "unknown" || result.SMTPCode != 0 {
		t.Fatal("lost acknowledgement became safe retry")
	}
}

func TestNativeIntakeQueueReuseNeverProvesDelivery(t *testing.T) {
	_, m, _ := intakeFixture(t)
	key := m.Key()
	const queueID = "ABC123456"
	const user = "mailbox@example.test"
	trace := "postfix/cleanup[10]: " + queueID + ": warning: header X-ANAS-Ingress-Key: " + key + " from localhost\n"
	for _, status := range []string{"sent", "bounced"} {
		line := "postfix/lmtp[20]: " + queueID + ": to=<" + user + ">, status=" + status + " (unrelated)\n"
		for _, logs := range []string{line + trace, trace + line} {
			run := func(_ context.Context, name string, _ ...string) (string, error) {
				switch name {
				case "/usr/bin/doveadm", "/usr/sbin/postqueue":
					return "", nil
				default:
					t.Fatal("unexpected native command", name)
					return "", errors.New("unexpected command")
				}
			}
			state, observed := observeNativeIntake(t.Context(), intakeRecord{Metadata: m}, user, run, func() string { return logs })
			if state != "unknown" || observed != queueID {
				t.Fatal("reused queue log became a final receipt", status, state, observed)
			}
		}
	}
	for _, mode := range []string{"matching-queue", "different-queue", "stored"} {
		t.Run(mode, func(t *testing.T) {
			run := func(_ context.Context, name string, _ ...string) (string, error) {
				switch name {
				case "/usr/bin/doveadm":
					if mode == "stored" {
						return strings.Repeat("a", 32) + " 1\n", nil
					}
					return "", nil
				case "/usr/sbin/postqueue":
					return `{"queue_id":"ABC123456"}` + "\n", nil
				case "/usr/sbin/postcat":
					if mode == "different-queue" {
						return intakeHeader + ": " + strings.Repeat("f", 64) + "\n", nil
					}
					return intakeHeader + ": " + key + "\n", nil
				default:
					t.Fatal("unexpected native command", name)
					return "", errors.New("unexpected command")
				}
			}
			state, _ := observeNativeIntake(t.Context(), intakeRecord{Metadata: m}, user, run, func() string { return trace })
			want := map[string]string{"matching-queue": "queued", "different-queue": "unknown", "stored": "stored"}[mode]
			if state != want {
				t.Fatal("wrong native evidence state", state, want)
			}
		})
	}
}

func TestIntakeRedundantOriginalRequiresBothConfirmationsAndKeepsReceipt(t *testing.T) {
	a, m, raw := intakeFixture(t)
	submits := 0
	a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
		submits++
		return intakeSubmission{State: "queued", QueueID: "ABC123456"}
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "queued", "ABC123456" }
	r, _ := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	if _, e := a.ReleaseIntake(t.Context(), r, true); !errors.Is(e, ErrConflict) {
		t.Fatal("queued original released", e)
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "stored", "ABC123456" }
	r, e := a.IntakeStatus(t.Context(), m.Key())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.ReleaseIntake(t.Context(), r, false); !errors.Is(e, ErrConflict) {
		t.Fatal("unacknowledged original released", e)
	}
	path := filepath.Join(a.StateDir, "intake", m.Key(), "raw.eml")
	if _, e = os.Stat(path); e != nil {
		t.Fatal("early deletion", e)
	}
	foreign := r
	foreign.MailboxID = m.SourceID
	if _, e = a.ReleaseIntake(t.Context(), foreign, true); !errors.Is(e, ErrConflict) {
		t.Fatal("foreign cleanup", e)
	}
	for range 2 {
		out, e := a.ReleaseIntake(t.Context(), r, true)
		if e != nil || !out.RawReleased {
			t.Fatal("cleanup replay", e)
		}
	}
	if _, e = os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("redundant original retained", e)
	}
	// Terminal replay validates the incoming digest without recreating raw mail
	// or touching the actual native box. Permanent receipt still suppresses it.
	out, e := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	if e != nil || !out.RawReleased || out.State != "stored" || submits != 1 {
		t.Fatal("cleanup forgot deduplication", e, submits)
	}
}

func TestIntakeCleanupRecoversInterruptedUnlinkAndRejectsSymlink(t *testing.T) {
	for _, mode := range []string{"interrupted", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			a, m, raw := intakeFixture(t)
			a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
				return intakeSubmission{State: "queued", QueueID: "ABC123456"}
			}
			a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "stored", "ABC123456" }
			r, e := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(a.StateDir, "intake", m.Key())
			path := filepath.Join(dir, "raw.eml")
			if e = os.Remove(path); e != nil {
				t.Fatal(e)
			}
			if mode == "symlink" {
				target := filepath.Join(t.TempDir(), "outside")
				if os.WriteFile(target, []byte("keep"), 0600) != nil || os.Symlink(target, path) != nil {
					t.Fatal("fixture")
				}
				if _, e = a.ReleaseIntake(t.Context(), r, true); !errors.Is(e, ErrUnavailable) {
					t.Fatal("symlink cleanup accepted", e)
				}
				data, _ := os.ReadFile(target)
				if string(data) != "keep" {
					t.Fatal("outside file changed")
				}
				return
			}
			state, e := loadIntake(dir)
			if e != nil {
				t.Fatal(e)
			}
			state.ReleaseRequested = true
			if saveIntake(dir, state) != nil {
				t.Fatal("intent")
			}
			out, e := a.ReleaseIntake(t.Context(), r, true)
			if e != nil || !out.RawReleased {
				t.Fatal("interrupted cleanup not recoverable", e)
			}
		})
	}
}

func TestLegacyProcessedIntakeCannotConfirmOrRelease(t *testing.T) {
	a, m, raw := intakeFixture(t)
	a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
		return intakeSubmission{State: "queued", QueueID: "ABC123456"}
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "processed", "ABC123456" }
	r, err := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	if !errors.Is(err, ErrUnavailable) || r.State != "unknown" {
		t.Fatal("unverified processed result was accepted", r.State, err)
	}
	dir := filepath.Join(a.StateDir, "intake", m.Key())
	state, err := loadIntake(dir)
	if err != nil {
		t.Fatal(err)
	}
	state.State = "processed" // A record written by the former implementation.
	if err = saveIntake(dir, state); err != nil {
		t.Fatal(err)
	}
	r, err = a.IntakeStatus(t.Context(), m.Key())
	if !errors.Is(err, ErrUnavailable) || r.State != "unknown" {
		t.Fatal("legacy processed result was accepted", r.State, err)
	}
	expected := intakeResult(state)
	if _, err = a.ReleaseIntake(t.Context(), expected, true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("legacy processed receipt released original", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "raw.eml")); err != nil {
		t.Fatal("original lost", err)
	}
	state, err = loadIntake(dir)
	if err != nil || state.State != "processed" || state.ReleaseRequested || state.RawReleased {
		t.Fatal("legacy evidence was overwritten", err)
	}
	// An earlier version may already have released the local copy. Do not
	// recreate it, assert a false success or erase the evidence on replay.
	if err = os.Remove(filepath.Join(dir, "raw.eml")); err != nil {
		t.Fatal(err)
	}
	state.ReleaseRequested, state.RawReleased = true, true
	if err = saveIntake(dir, state); err != nil {
		t.Fatal(err)
	}
	r, err = a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	if !errors.Is(err, ErrUnavailable) || r.State != "unknown" || !r.RawReleased {
		t.Fatal("previous release was hidden or falsely confirmed", r.State, err)
	}
	if _, err = os.Lstat(filepath.Join(dir, "raw.eml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("previously released original was silently recreated", err)
	}
}

func TestLegacyLogRejectionReconcilesWithoutResubmission(t *testing.T) {
	a, m, raw := intakeFixture(t)
	submits := 0
	a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
		submits++
		return intakeSubmission{State: "queued", QueueID: "ABC123456", Check: "native-delivery", SMTPCode: 250}
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "queued", "ABC123456" }
	_, _ = a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	dir := filepath.Join(a.StateDir, "intake", m.Key())
	state, err := loadIntake(dir)
	if err != nil {
		t.Fatal(err)
	}
	state.State = "rejected" // An older log-based observation, not an SMTP 5xx.
	if err = saveIntake(dir, state); err != nil {
		t.Fatal(err)
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "stored", "ABC123456" }
	r, err := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	if err != nil || r.State != "stored" || submits != 1 {
		t.Fatal("legacy log rejection was not reconciled safely", r.State, submits, err)
	}
}

func intakeFixture(t *testing.T) (*Adapter, IntakeMetadata, string) {
	t.Helper()
	a, in := fixture(t)
	if _, err := a.Apply(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	raw := "From: sender@example.net\r\nTo: forged@foreign.example\r\nX-ANAS-Ingress-Key: forged\r\n continued-forgery\r\nSubject: original\r\n\r\nOriginal body.\r\n"
	h := sha256.Sum256([]byte(raw))
	m := IntakeMetadata{SourceID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", MessageID: "message-1", MailboxID: in.Projection.Mailbox.ID, OwnerAnchor: in.Projection.Mailbox.OwnerAnchor, ClaimVersion: in.Projection.Resource.Version, AcceptedAddress: "alice@example.test", EnvelopeFrom: "sender@example.net", SHA256: hex.EncodeToString(h[:])}
	return a, m, raw
}
func TestIntakeArchivesOriginalAndSerializesConcurrentReplays(t *testing.T) {
	a, m, raw := intakeFixture(t)
	submissions := 0
	a.intakeSubmit = func(_ context.Context, record intakeRecord, user string, body io.Reader) intakeSubmission {
		submissions++
		if user != "mbx_"+strings.Repeat("a", 32)+"@"+ProtocolRealm {
			t.Error("public address used for historical routing")
		}
		original, _ := io.ReadAll(body)
		if string(original) != raw {
			t.Error("archived original changed")
		}
		return intakeSubmission{State: "queued", QueueID: "ABC123456"}
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "stored", "ABC123456" }
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, err := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
			if err == nil && receipt.State != "stored" {
				err = ErrUnavailable
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if submissions != 1 {
		t.Fatal("duplicate native queue submission", submissions)
	}
	path := filepath.Join(a.StateDir, "intake", m.Key(), "raw.eml")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != raw {
		t.Fatal("raw source not preserved")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("raw source not private")
	}
	transport, err := intakeTransport(strings.NewReader(raw), m.Key())
	if err != nil {
		t.Fatal(err)
	}
	copy, _ := io.ReadAll(transport)
	if strings.Count(string(copy), intakeHeader+":") != 1 || strings.Contains(string(copy), "forged\r\n") || strings.Contains(string(copy), "continued-forgery") || !strings.HasSuffix(string(copy), "Original body.\r\n") {
		t.Fatal("untrusted reserved header survived transport")
	}
	changed := m
	changed.MailboxID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if changed.Key() == m.Key() {
		t.Fatal("two targets shared a deduplication key")
	}
}
func TestIntakeCrashAfterNativeActionNeverBlindlyResubmits(t *testing.T) {
	a, m, raw := intakeFixture(t)
	count := 0
	a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
		count++
		panic("simulated process interruption")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("fault did not run")
			}
		}()
		a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
	}()
	previous, err := loadIntake(filepath.Join(a.StateDir, "intake", m.Key()))
	if err != nil || previous.State != "submitting" {
		t.Fatal("submission intent was not durable", err)
	}
	a = &Adapter{StateDir: a.StateDir, ConfigDir: a.ConfigDir, CapacityBytes: a.CapacityBytes, startupReady: func(context.Context) error { return nil }, probe: func(context.Context, map[string]entry) error { return nil }, intakeSubmit: func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
		count++
		return intakeSubmission{State: "queued", QueueID: "ABC123456"}
	}, intakeObserve: func(context.Context, intakeRecord, string) (string, string) { return "unknown", "" }}
	for range 2 {
		r, err := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
		if !errors.Is(err, ErrUnavailable) || r.State != "unknown" {
			t.Fatal("unknown action claimed delivery", err)
		}
	}
	if count != 1 {
		t.Fatal("recovery submitted an uncertain message again")
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "stored", "ABC123456" }
	if r, err := a.IntakeStatus(t.Context(), m.Key()); err != nil || r.State != "stored" {
		t.Fatal("actual later receipt was not reconciled", err)
	}
}

func TestIntakeDeliveryIntentIsPrivateAndPersistsBeforeSubmission(t *testing.T) {
	a, m, raw := intakeFixture(t)
	tokens := []string{}
	a.intakeSubmit = func(_ context.Context, record intakeRecord, _ string, _ io.Reader) intakeSubmission {
		persisted, err := loadIntake(filepath.Join(a.StateDir, "intake", m.Key()))
		if err != nil || persisted.State != "submitting" || persisted.Metadata != m || persisted.DeliveryToken != record.DeliveryToken || !intakeID.MatchString(record.DeliveryToken) {
			t.Fatal("exact delivery intent was not persisted before submission", err)
		}
		tokens = append(tokens, record.DeliveryToken)
		if len(tokens) == 1 {
			return intakeSubmission{State: "retryable", Check: "smtp-connect"}
		}
		return intakeSubmission{State: "unknown", Check: "smtp-ack"}
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "unknown", "" }
	for range 3 {
		receipt, err := a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal("unconfirmed submission was reported as delivered", err)
		}
		encoded, _ := json.Marshal(receipt)
		for _, token := range tokens {
			if strings.Contains(string(encoded), token) {
				t.Fatal("private intent token appeared in receipt")
			}
		}
	}
	if len(tokens) != 2 || tokens[0] == tokens[1] {
		t.Fatal("retryable and unknown attempts used incorrect identities", len(tokens))
	}
	state, err := loadIntake(filepath.Join(a.StateDir, "intake", m.Key()))
	dir := filepath.Join(a.StateDir, "intake", m.Key())
	info, statErr := os.Stat(filepath.Join(dir, "delivery-intent.json"))
	if err != nil || state.State != "unknown" || state.DeliveryToken != tokens[1] || statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unknown intent was not preserved privately", err, statErr)
	}
	legacy, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || strings.Contains(string(legacy), tokens[1]) || strings.Contains(string(legacy), "delivery_token") {
		t.Fatal("new private intent changed the legacy intake record", err)
	}
}

func TestIntakeDeliveryIntentRejectsAmbiguousOrUnsafeState(t *testing.T) {
	for _, mode := range []string{"duplicate", "foreign-metadata", "symlink", "world-readable"} {
		t.Run(mode, func(t *testing.T) {
			a, m, raw := intakeFixture(t)
			a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
				return intakeSubmission{State: "unknown", Check: "smtp-ack"}
			}
			a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "unknown", "" }
			_, _ = a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
			dir := filepath.Join(a.StateDir, "intake", m.Key())
			path := filepath.Join(dir, "delivery-intent.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "duplicate":
				data = []byte(strings.TrimSuffix(string(data), "}") + `,"schema":"anas.mail-delivery-intent/v1"}`)
			case "foreign-metadata":
				data = []byte(strings.Replace(string(data), m.SourceID, m.MailboxID, 1))
			case "symlink":
				if err = os.Remove(path); err != nil || os.Symlink(filepath.Join(dir, "state.json"), path) != nil {
					t.Fatal("could not prepare symlink")
				}
			case "world-readable":
				if err = os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "duplicate" || mode == "foreign-metadata" {
				if err = os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = loadIntake(dir); !errors.Is(err, ErrUnavailable) {
				t.Fatal("ambiguous or unsafe intent was accepted", err)
			}
		})
	}
}
func TestIntakeRejectsMetadataDriftCorruptionAndUnsafeRawPath(t *testing.T) {
	for _, mode := range []string{"wrong-hash", "wrong-target", "metadata-drift", "corrupt-staged-raw", "symlink-staged-raw"} {
		t.Run(mode, func(t *testing.T) {
			a, m, raw := intakeFixture(t)
			submits := 0
			a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
				submits++
				return intakeSubmission{State: "queued", QueueID: "ABC123456"}
			}
			a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "queued", "ABC123456" }
			if mode == "metadata-drift" {
				a.Intake(t.Context(), m.Key(), m, strings.NewReader(raw))
				m.EnvelopeFrom = "changed@example.net"
			} else if mode == "corrupt-staged-raw" || mode == "symlink-staged-raw" {
				if _, err := publishIntake(filepath.Join(a.StateDir, "intake"), m, strings.NewReader(raw)); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(a.StateDir, "intake", m.Key(), "raw.eml")
				if mode == "corrupt-staged-raw" {
					os.WriteFile(path, []byte("corrupted"), 0600)
				} else {
					os.Remove(path)
					os.Symlink(filepath.Join(a.StateDir, "state.json"), path)
				}
			}
			if mode == "wrong-target" {
				m.OwnerAnchor = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
			}
			body := raw
			if mode == "wrong-hash" {
				body = "different body"
			}
			before := submits
			if _, err := a.Intake(t.Context(), m.Key(), m, strings.NewReader(body)); err == nil {
				t.Fatal("unsafe intake accepted")
			}
			if submits != before {
				t.Fatal("rejected input reached native SMTP")
			}
		})
	}
}
func TestIntakeHTTPKeepsQueueAcceptanceSeparateFromDelivery(t *testing.T) {
	a, m, raw := intakeFixture(t)
	a.intakeSubmit = func(context.Context, intakeRecord, string, io.Reader) intakeSubmission {
		return intakeSubmission{State: "queued", QueueID: "ABC123456"}
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "queued", "ABC123456" }
	token := strings.Repeat("a", 64)
	h := Handler{Adapter: a, Token: token, Host: "mail.example.test:9443"}
	metadata, _ := json.Marshal(m)
	for _, mode := range []string{"no-auth", "unknown-secret-field", "queued"} {
		r := httptest.NewRequest("POST", "https://mail.example.test:9443/v1/intake/"+m.Key(), strings.NewReader(raw))
		r.Header.Set("Content-Type", "message/rfc822")
		r.Header.Set("X-ANAS-Intake", string(metadata))
		if mode != "no-auth" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if mode == "unknown-secret-field" {
			r.Header.Set("X-ANAS-Intake", `{"password":"never-echo"}`)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := map[string]int{"no-auth": 401, "unknown-secret-field": 400, "queued": 503}[mode]
		if w.Code != expected || strings.Contains(w.Body.String(), "never-echo") {
			t.Fatal("intake boundary", mode, w.Code)
		}
	}
	c := Client{base: "https://mail.example.test:9443", token: token, http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		r.Host = "mail.example.test:9443"
		r.TLS = &tls.ConnectionState{}
		h.ServeHTTP(w, r)
		return w.Result(), nil
	})}}
	if receipt, err := c.Intake(t.Context(), m, strings.NewReader(raw)); !errors.Is(err, ErrUnavailable) || receipt.State != "queued" {
		t.Fatal("queued was treated as delivered", err)
	}
	a.intakeObserve = func(context.Context, intakeRecord, string) (string, string) { return "stored", "ABC123456" }
	if receipt, err := c.IntakeStatus(t.Context(), m.Key()); err != nil || receipt.State != "stored" {
		t.Fatal("confirmed delivery not returned", err)
	}
}
