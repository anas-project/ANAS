// TEST_CASES: MAILMOD-T-077
package ingress

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type strictNoticeRecorder struct{ calls int }

func (s *strictNoticeRecorder) RecordIngressNotice(context.Context, store.IngressNotice) error {
	s.calls++
	return nil
}
func TestNotificationRejectsAmbiguousSignedJSON(t *testing.T) {
	source := "11111111-1111-4111-8111-111111111111"
	generation := "22222222-2222-4222-8222-222222222222"
	notice := "33333333-3333-4333-8333-333333333333"
	mailbox := "44444444-4444-4444-8444-444444444444"
	secret := strings.Repeat("a", 64)
	now := time.Now().UTC()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	body := `{"schema":"anas.mail-notification/v1","source_id":"` + source + `","generation":"` + generation + `","notification_id":"` + notice + `","message_id":"message-1","mailbox_id":"` + mailbox + `"}`
	cases := map[string]string{"duplicate": strings.Replace(body, `"source_id":`, `"source_id":"`+generation+`","source_id":`, 1), "case-variant": strings.Replace(body, `"source_id":`, `"SOURCE_ID":`, 1), "duplicate-case": strings.Replace(body, `"source_id":`, `"SOURCE_ID":"`+generation+`","source_id":`, 1), "missing": strings.Replace(body, `"message_id":"message-1",`, "", 1), "trailing": body + ` {}`}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			key, _ := hex.DecodeString(secret)
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(timestamp + "." + raw))
			r := httptest.NewRequest("POST", "https://mail.example.test/api/ingress/notify", bytes.NewBufferString(raw))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-ANAS-Mail-Time", timestamp)
			r.Header.Set("X-ANAS-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
			record := &strictNoticeRecorder{}
			wake := 0
			handler := NotificationHandler{Store: record, Sources: map[string]NotificationSource{source: {Generation: generation, Secret: secret}}, Now: func() time.Time { return now }, Wake: func(string) { wake++ }}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 400 || record.calls != 0 || wake != 0 {
				t.Fatalf("ambiguous JSON accepted: status=%d calls=%d wakes=%d", w.Code, record.calls, wake)
			}
		})
	}
}
