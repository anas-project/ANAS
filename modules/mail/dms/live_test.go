// TEST_CASES: MAILMOD-T-016 MAILMOD-T-019
package dms

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type observedResponseBody struct {
	io.Reader
	io.Closer
}

// Test-only observation of the same response consumed by the real client.
// Never log its body or headers: they may contain identifiers or credentials.
type readbackObservation struct {
	next   http.RoundTripper
	status int
	body   bytes.Buffer
}

func (o *readbackObservation) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := o.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	o.status = response.StatusCode
	response.Body = observedResponseBody{Reader: io.TeeReader(response.Body, &o.body), Closer: response.Body}
	return response, nil
}

// This opt-in test runs inside the isolated, pinned DMS container. It exercises
// the actual adapter process, configuration watcher, Postfix and Dovecot.
func TestDMSNativeReadback(t *testing.T) {
	path := os.Getenv("MAIL_DMS_NATIVE_CONFIG")
	if path == "" {
		t.Skip("explicit private native DMS fixture required; no real-host pass")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private native config required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Token, CAFile      string
		Outbound           bool
		OutboundConfigured bool
	}
	if json.Unmarshal(raw, &cfg) != nil || !serviceKey.MatchString(cfg.Token) || cfg.CAFile == "" {
		t.Fatal("invalid native configuration")
	}
	pem, err := os.ReadFile(cfg.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("native trust unavailable")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: "mail.mailtest.invalid"}}, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	tryRequest := func(t *testing.T, method, path string, value any) (int, []byte, error) {
		t.Helper()
		var body []byte
		if value != nil {
			var err error
			body, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		r, err := http.NewRequestWithContext(t.Context(), method, "https://127.0.0.1:9443"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "mail.mailtest.invalid:9443"
		r.Header.Set("Authorization", "Bearer "+cfg.Token)
		if value != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if err != nil || len(raw) > 65536 {
			t.Fatal("native response invalid")
		}
		return response.StatusCode, raw, nil
	}
	request := func(t *testing.T, method, path string, value any) (int, []byte) {
		t.Helper()
		status, raw, err := tryRequest(t, method, path, value)
		if err != nil {
			t.Fatal("native HTTPS operation failed", err)
		}
		return status, raw
	}
	_, in := fixture(t)
	// Submit all three intents before waiting for the first live confirmation.
	// DMS must not read one startup generation and checkpoint a later generation
	// without noticing the remaining accounts (observed on finance first boot).
	startupRequests := []ApplyRequest{in, secondMailbox(t, in, "4", "startup-second@example.test"), secondMailbox(t, in, "5", "startup-third@example.test")}
	for _, pending := range startupRequests {
		status, _ := request(t, "POST", "/v1/config/apply", pending)
		if status != 200 && status != 503 {
			t.Fatal("startup intent rejected", status)
		}
	}
	apply := func(t *testing.T, in ApplyRequest) {
		t.Helper()
		deadline := time.Now().Add(70 * time.Second)
		status := 0
		for time.Now().Before(deadline) {
			var requestErr error
			status, raw, requestErr = tryRequest(t, "POST", "/v1/config/apply", in)
			if requestErr != nil {
				if !errors.Is(requestErr, context.DeadlineExceeded) || t.Context().Err() != nil {
					t.Fatal("native HTTPS operation failed", requestErr)
				}
				t.Log("native apply timed out; retrying the same idempotent intent")
				time.Sleep(time.Second)
				continue
			}
			if status == 200 {
				var r Receipt
				if json.Unmarshal(raw, &r) != nil || !r.Applied || r.Revision != in.Projection.Revision || r.Fingerprint != in.Projection.Fingerprint {
					t.Fatal("native receipt mismatch")
				}
				return
			}
			if status != 503 {
				t.Fatal("native configuration rejected", status, string(raw))
			}
			time.Sleep(time.Second)
		}
		t.Fatal("DMS live readback did not confirm", status, string(raw))
	}
	if !t.Run("apply and confirm actual userdb and route", func(t *testing.T) {
		for _, pending := range startupRequests {
			apply(t, pending)
		}
		status, body := request(t, "GET", "/v1/config/"+in.Projection.Mailbox.ID, nil)
		if status != 200 {
			t.Fatal("native readback failed", status, string(body))
		}
	}) {
		t.FailNow()
	}
	if os.Getenv("MAIL_DMS_REPEAT_READBACK") == "1" {
		t.Run("repeated readback after startup", func(t *testing.T) {
			for n := range 5 {
				for _, pending := range startupRequests {
					status, body := request(t, "GET", "/v1/config/"+pending.Projection.Mailbox.ID, nil)
					if status != 200 {
						t.Fatalf("readback iteration %d failed: %d %s", n+1, status, body)
					}
				}
			}
		})
		if t.Failed() {
			return
		}
	}
	t.Run("stale revision cannot change DMS", func(t *testing.T) {
		bad := nextRequest(t, in, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
		bad.ExpectedRevision = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
		status, _ := request(t, "POST", "/v1/config/apply", bad)
		if status != 409 {
			t.Fatal("stale revision accepted", status)
		}
	})
	next := nextRequest(t, in, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	next.Projection.Resource.State = "disabled"
	next.Projection.Resource.Addresses = append(next.Projection.Resource.Addresses, next.Projection.Resource.Addresses[0])
	next.Projection.Resource.Addresses[1].Address = "retained@example.test"
	next.Projection.Resource.Addresses[1].Kind = "alias"
	next.Projection.Resource.Addresses[1].State = "retained"
	next.Projection.Fingerprint, _, err = store.ProjectionFingerprint(next.Projection.Resource)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("disabled receiving and retained alias", func(t *testing.T) {
		apply(t, next)
		if err := ProbeRecipient(t.Context(), next.Projection.Resource.DefaultAddress); err != nil {
			t.Fatal("disabled mailbox stopped receiving", err)
		}
	})
	t.Run("drift is never confirmed and retry restores", func(t *testing.T) {
		file := filepath.Join("/tmp/docker-mailserver", "postfix-virtual.cf")
		if err := os.WriteFile(file, []byte("drift@example.test invalid@"+ProtocolRealm+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		status, _ := request(t, "GET", "/v1/config/"+in.Projection.Mailbox.ID, nil)
		if status != 503 {
			t.Fatal("drift reported ready", status)
		}
		control, err := NewClient(ClientConfig{URL: "https://mail.mailtest.invalid:9443", ServiceToken: cfg.Token}, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots})
		if err != nil {
			t.Fatal(err)
		}
		pending, err := control.Read(t.Context(), in.Projection.Mailbox.ID)
		if !errors.Is(err, ErrUnavailable) || pending.Applied || pending.Revision != next.Projection.Revision {
			t.Fatal("unconfirmed revision missing", err)
		}
		newer := nextRequest(t, next, "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
		newer.ExpectedRevision = pending.Revision
		apply(t, newer)
		next = newer
	})
	t.Run("fixed session kick rejects injected arguments", func(t *testing.T) {
		status, _ := request(t, "POST", "/v1/sessions/kick", map[string]string{"session_id": "../anything;command"})
		if status != 409 {
			t.Fatal("untrusted kick argument accepted", status)
		}
		status, _ = request(t, "POST", "/v1/sessions/kick", map[string]string{"session_id": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"})
		if status != 204 {
			t.Fatal("idempotent native kick failed", status)
		}
	})
	t.Run("generated input retains independent storage reference", func(t *testing.T) {
		raw, err := os.ReadFile("/etc/dovecot/userdb")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "userdb_mail_path=/var/mail/"+in.Projection.Mailbox.StorageRef) || strings.Contains(string(raw), in.Projection.Mailbox.OwnerAnchor) {
			t.Fatal("native userdb used public personnel identity")
		}
	})
	t.Run("production TLS client and mailbox-scoped kick", func(t *testing.T) {
		control, err := NewClient(ClientConfig{URL: "https://mail.mailtest.invalid:9443", ServiceToken: cfg.Token}, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots})
		if err != nil {
			t.Fatal(err)
		}
		readback := func(t *testing.T, id string, iteration int) {
			t.Helper()
			observation := &readbackObservation{next: control.http.Transport}
			control.http.Transport = observation
			_, err := control.Read(t.Context(), id)
			control.http.Transport = observation.next
			if err != nil {
				var result struct {
					Check string `json:"check"`
				}
				_ = json.Unmarshal(observation.body.Bytes(), &result)
				allowed := map[string]bool{"native-initialization": true, "native-initialization-timeout": true, "authentication-config": true, "authentication-cache": true, "mailbox-storage": true, "mailbox-quota": true, "address-route": true, "smtp-recipient": true}
				if !allowed[result.Check] {
					result.Check = "unknown"
				}
				t.Fatal("production client readback", "iteration", iteration, err, "HTTP status", observation.status, "failed check", result.Check)
			}
		}
		readback(t, in.Projection.Mailbox.ID, 0)
		if os.Getenv("MAIL_DMS_REPEAT_READBACK") == "1" && !t.Run("repeated production readback after reload", func(t *testing.T) {
			for iteration := 1; iteration <= 5; iteration++ {
				for _, pending := range startupRequests {
					readback(t, pending.Projection.Mailbox.ID, iteration)
				}
			}
		}) {
			return
		}
		status, err := control.OutboundStatus(t.Context())
		want := "not-configured"
		if cfg.Outbound || cfg.OutboundConfigured {
			want = "unverified"
		}
		if err != nil || status != want {
			t.Fatal("actual Postfix outbound observation mismatch", status, err)
		}
		queue, err := control.OutboundQueue(t.Context())
		if err != nil || queue != "none-observed" && queue != "queued-observed" {
			t.Fatal("actual Postfix queue observation unavailable", queue, err)
		}
		if err = control.Kick(t.Context(), "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", in.Projection.Mailbox.ID); err != nil {
			t.Fatal("mailbox-scoped kick", err)
		}
	})
}
