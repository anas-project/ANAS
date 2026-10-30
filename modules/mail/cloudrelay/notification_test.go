// TEST_CASES: MAILMOD-T-039
package cloudrelay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type testRecorder struct {
	saved  bool
	fail   bool
	notice store.IngressNotice
}

func (s *testRecorder) RecordIngressNotice(_ context.Context, notice store.IngressNotice) error {
	if s.fail {
		return errors.New("private failure")
	}
	s.saved = true
	s.notice = notice
	return nil
}

func TestVPSNotificationUsesRealNASHandler(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const generation = "22222222-2222-4222-8222-222222222222"
	notice := Notification{ID: "33333333-3333-4333-8333-333333333333", MessageID: "message-1", MailboxID: "44444444-4444-4444-8444-444444444444"}
	secret := strings.Repeat("c", 64)
	store := &testRecorder{}
	wakes := 0
	server := httptest.NewTLSServer(ingress.NotificationHandler{Store: store, Sources: map[string]ingress.NotificationSource{source: {Generation: generation, Secret: secret}}, Wake: func(id string) {
		if !store.saved || id != source {
			t.Error("wake preceded durable record")
		}
		wakes++
	}})
	defer server.Close()
	cfg := NotificationConfig{Origin: server.URL, SourceID: source, Generation: generation, Secret: secret, Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}
	n, err := newNotifier(cfg, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result := n.Send(t.Context(), notice, time.Now())
	if result.State != "notified" || result.HTTPStatus != 202 || !store.saved || wakes != 1 || store.notice.MailboxID != notice.MailboxID || store.notice.MessageID != notice.MessageID {
		t.Fatal("notification not accepted by NAS", result)
	}
	wrong := cfg
	wrong.Secret = strings.Repeat("d", 64)
	wrongNotifier, err := newNotifier(wrong, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result = wrongNotifier.Send(t.Context(), notice, time.Now())
	if result.State != "configuration-error" || result.HTTPStatus != 401 || wakes != 1 {
		t.Fatal("incorrect signature accepted", result)
	}
	store.fail = true
	store.saved = false
	result = n.Send(t.Context(), notice, time.Now())
	if result.State != "failed" || result.HTTPStatus != 503 || store.saved || wakes != 1 {
		t.Fatal("failure incorrectly acknowledged", result)
	}
}

func TestVPSNotifierRejectsUnsafeConfigurationAndUnprovenReceipts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer server.Close()
	cfg := NotificationConfig{Origin: server.URL, SourceID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", Secret: strings.Repeat("c", 64), Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}
	n, err := newNotifier(cfg, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result := n.Send(t.Context(), Notification{ID: "33333333-3333-4333-8333-333333333333", MessageID: "message-1", MailboxID: "44444444-4444-4444-8444-444444444444"}, time.Now())
	if result.State != "configuration-error" || result.Check != "invalid-receipt" {
		t.Fatal("unproven reply accepted", result)
	}
	for _, mutate := range []func(*NotificationConfig){func(c *NotificationConfig) { c.Secret = c.Reader }, func(c *NotificationConfig) { c.Origin = "http://example.test" }, func(c *NotificationConfig) { c.Origin = "https://example.test/path" }, func(c *NotificationConfig) { c.Origin = "https://example.test:65536" }} {
		bad := cfg
		mutate(&bad)
		if _, err := newNotifier(bad, server.Client().Transport); !errors.Is(err, ErrConfiguration) {
			t.Fatal("unsafe config accepted", err)
		}
	}
}

func TestVPSNotifierRejectsAmbiguousReceipts(t *testing.T) {
	valid := `{"schema":"anas.mail-notification/v1","source_id":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","notification_id":"33333333-3333-4333-8333-333333333333","accepted":true}`
	for _, tc := range []struct {
		name, body, want string
	}{
		{"valid", valid, "notified"},
		{"escaped key", strings.Replace(valid, `"accepted"`, `"\u0061ccepted"`, 1), "notified"},
		{"duplicate", strings.Replace(valid, `"accepted":true`, `"accepted":false,"accepted":true`, 1), "configuration-error"},
		{"escaped duplicate", strings.Replace(valid, `"accepted":true`, `"accepted":false,"\u0061ccepted":true`, 1), "configuration-error"},
		{"case variant", strings.Replace(valid, `"accepted"`, `"Accepted"`, 1), "configuration-error"},
		{"extra", strings.Replace(valid, `"accepted":true`, `"accepted":true,"other":1`, 1), "configuration-error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			cfg := NotificationConfig{Origin: server.URL, SourceID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", Secret: strings.Repeat("c", 64), Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}
			n, err := newNotifier(cfg, server.Client().Transport)
			if err != nil {
				t.Fatal(err)
			}
			result := n.Send(t.Context(), Notification{ID: "33333333-3333-4333-8333-333333333333", MessageID: "message-1", MailboxID: "44444444-4444-4444-8444-444444444444"}, time.Now())
			if result.State != tc.want || (tc.want != "notified" && result.Check != "invalid-receipt") {
				t.Fatalf("unexpected receipt state: %+v", result)
			}
		})
	}
}
