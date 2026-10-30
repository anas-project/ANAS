package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNotificationAuthenticationAndScope(t *testing.T) {
	cfg := config{Source: "isolated-source", NotifySecret: strings.Repeat("s", 32), Targets: map[string]string{"box-a": "box-a@ingress.invalid"}}
	now := time.Now()
	body := `{"source":"isolated-source","id":"11111111-2222-3333-4444-555555555555","target":"box-a"}`
	for _, tc := range []struct {
		name, body string
		age        time.Duration
		tamper     bool
		want       int
	}{
		{"valid", body, 0, false, 0},
		{"tampered", body, 0, true, 401},
		{"expired", body, -301 * time.Second, false, 401},
		{"future", body, 301 * time.Second, false, 401},
		{"foreign source", strings.ReplaceAll(body, "isolated-source", "foreign"), 0, false, 400},
		{"foreign target", strings.ReplaceAll(body, "box-a", "box-b"), 0, false, 400},
		{"body injection", strings.TrimSuffix(body, "}") + `,"raw":"untrusted"}`, 0, false, 400},
		{"trailing JSON", body + ` {}`, 0, false, 400},
		{"oversize", strings.Repeat("x", 4097), 0, false, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/notify", strings.NewReader(tc.body))
			stamp := strconv.FormatInt(now.Add(tc.age).UnixMilli(), 10)
			mac := hmac.New(sha256.New, []byte(cfg.NotifySecret))
			mac.Write([]byte(stamp + "." + tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Mail-Time", stamp)
			r.Header.Set("X-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
			if tc.tamper {
				r.Header.Set("X-Mail-Signature", strings.Repeat("00", 32))
			}
			_, status := verifyNotification(r, cfg, now)
			if status != tc.want {
				t.Fatalf("status %d, want %d", status, tc.want)
			}
		})
	}
}
