package ingress

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type noticeRecorder struct {
	calls    int
	failure  error
	last     store.IngressNotice
	recorded bool
}

func (s *noticeRecorder) RecordIngressNotice(_ context.Context, n store.IngressNotice) error {
	s.calls++
	s.last = n
	if s.failure != nil {
		return s.failure
	}
	s.recorded = true
	return nil
}
func TestNotificationDurableHintPrecedesAcknowledgementAndCarriesNoAuthority(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const generation = "22222222-2222-4222-8222-222222222222"
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	input := notificationInput{Schema: notificationSchema, SourceID: source, Generation: generation, ID: "33333333-3333-4333-8333-333333333333", MessageID: "message-1", MailboxID: "44444444-4444-4444-8444-444444444444"}
	secret := strings.Repeat("a", 64)
	request := func(body []byte, when time.Time) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://mail.example.test/api/ingress/notify", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-ANAS-Mail-Time", strconv.FormatInt(when.Unix(), 10))
		key, _ := hex.DecodeString(secret)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(r.Header.Get("X-ANAS-Mail-Time") + "."))
		mac.Write(body)
		r.Header.Set("X-ANAS-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
		s := &noticeRecorder{}
		w := httptest.NewRecorder()
		wakes := 0
		h := NotificationHandler{Store: s, Sources: map[string]NotificationSource{source: {Generation: generation, Secret: secret}}, Now: func() time.Time { return now }, Wake: func(id string) {
			if id != source || !s.recorded {
				t.Error("wake preceded durable hint")
			}
			wakes++
		}}
		h.ServeHTTP(w, r)
		if when.Equal(now) {
			if w.Code != 202 || s.calls != 1 || wakes != 1 || s.last.MailboxID != input.MailboxID || s.last.MessageID != input.MessageID || len(s.last.BodySHA256) != 64 {
				t.Fatal("notification hint", w.Code)
			}
			var result map[string]any
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result["accepted"] != true || result["notification_id"] != input.ID {
				t.Fatal("receipt")
			}
		}
		if !when.Equal(now) && (w.Code != 401 || s.calls != 0 || wakes != 0) {
			t.Fatal("expired/future signal accepted", w.Code)
		}
		return w
	}
	raw, _ := json.Marshal(input)
	request(raw, now)
	request(raw, now.Add(-301*time.Second))
	request(raw, now.Add(301*time.Second))
}
func TestNotificationRejectsForgeryUnknownFieldsAndStorageFailuresWithoutEcho(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const generation = "22222222-2222-4222-8222-222222222222"
	now := time.Now().UTC()
	secret := strings.Repeat("a", 64)
	input := notificationInput{Schema: notificationSchema, SourceID: source, Generation: generation, ID: "33333333-3333-4333-8333-333333333333", MessageID: "message-1", MailboxID: "44444444-4444-4444-8444-444444444444"}
	for _, mode := range []string{"signature", "duplicate-header", "unknown-source", "generation", "unknown-field", "query", "origin", "cookie", "storage", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			body, _ := json.Marshal(input)
			if mode == "unknown-field" {
				body = append(body[:len(body)-1], []byte(`,"password":"private-input-value"}`)...)
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/ingress/notify", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-ANAS-Mail-Time", strconv.FormatInt(now.Unix(), 10))
			key, _ := hex.DecodeString(secret)
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(r.Header.Get("X-ANAS-Mail-Time") + "."))
			mac.Write(body)
			r.Header.Set("X-ANAS-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
			s := &noticeRecorder{}
			wakes := 0
			h := NotificationHandler{Store: s, Sources: map[string]NotificationSource{source: {Generation: generation, Secret: secret}}, Wake: func(string) { wakes++ }}
			expected := 401
			switch mode {
			case "signature":
				r.Header.Set("X-ANAS-Mail-Signature", strings.Repeat("f", 64))
			case "duplicate-header":
				r.Header.Add("X-ANAS-Mail-Signature", r.Header.Get("X-ANAS-Mail-Signature"))
			case "unknown-source":
				h.Sources = nil
			case "generation":
				h.Sources[source] = NotificationSource{Generation: input.MailboxID, Secret: secret}
			case "unknown-field":
				expected = 400
			case "query":
				r.URL.RawQuery = "owner=foreign"
				expected = 400
			case "origin":
				r.Header.Set("Origin", "https://foreign.example")
				expected = 400
			case "cookie":
				r.Header.Set("Cookie", "session=foreign")
				expected = 400
			case "storage":
				s.failure = errors.New("private database credential")
				expected = 503
			case "conflict":
				s.failure = store.ErrIngressChanged
				expected = 409
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != expected || wakes != 0 || strings.Contains(w.Body.String(), "private") {
				t.Fatal("unsafe notification result", mode, w.Code)
			}
			if mode != "storage" && mode != "conflict" && s.calls != 0 {
				t.Fatal("invalid hint reached database")
			}
		})
	}
}
