package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestBrowserWebmailStartIsAuthenticatedAndNotALoginGrant(t *testing.T) {
	h, s, _, _ := protocolFixture(t)
	w := request(t, h, "POST", "/api/webmail/start", sessionCookie+"=session-secret", url.Values{"mailbox_id": {webmailTestBox}}.Encode(), "https://mail.example.test")
	var result struct {
		PostTo string            `json:"post_to"`
		Fields map[string]string `json:"fields"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !strings.Contains(result.PostTo, "plugin.anas_begin") || result.Fields["mailbox_id"] != webmailTestBox {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(result.Fields) != 4 || !webmailStatePattern.MatchString(result.Fields["start_nonce"]) {
		t.Fatal("invalid start challenge")
	}
	key, _ := hex.DecodeString(webmailTestKey)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("anas-mail-webmail-start-v1\n" + webmailTestBox + "\n" + result.Fields["start_nonce"] + "\n" + result.Fields["issued_at"]))
	if hex.EncodeToString(mac.Sum(nil)) != result.Fields["signature"] || s.issued != 0 || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("start issued credential or wrong signature")
	}
	for _, origin := range []string{"", "https://foreign.example.test", "https://webmail.example.test"} {
		w = request(t, h, "POST", "/api/webmail/start", sessionCookie+"=session-secret", url.Values{"mailbox_id": {webmailTestBox}}.Encode(), origin)
		if w.Code != 403 {
			t.Fatal("cross-origin start allowed", w.Code)
		}
	}
	w = request(t, h, "POST", "/api/webmail/start", "", url.Values{"mailbox_id": {webmailTestBox}}.Encode(), "https://mail.example.test")
	if w.Code != 401 {
		t.Fatal("anonymous start allowed", w.Code)
	}
}

func TestBrowserContinueUsesOnlyBoundPostBody(t *testing.T) {
	h, _, _, _ := protocolFixture(t)
	challenge := signedWebmailChallenge(webmailTestBox, webmailTestState, time.Now())
	w := request(t, h, "POST", "/webmail/continue", sessionCookie+"=session-secret", challenge.Encode(), "https://webmail.example.test")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `method="post"`) || !strings.Contains(w.Body.String(), `name="code"`) || !strings.Contains(w.Body.String(), `name="browser_state"`) {
		t.Fatal("bound POST relay missing", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") || w.Header().Get("Referrer-Policy") != "origin" || !strings.Contains(w.Body.String(), `name="referrer" content="origin"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("relay browser policy missing")
	}
	if strings.Contains(w.Body.String(), "access_token") || strings.Contains(w.Body.String(), webmailTestKey) || strings.Contains(w.Header().Get("Location"), "handoff_") {
		t.Fatal("credential exposed or code put in URL")
	}
	for _, origin := range []string{"", "null", "https://mail.example.test", "https://foreign.example.test"} {
		w = request(t, h, "POST", "/webmail/continue", sessionCookie+"=session-secret", challenge.Encode(), origin)
		if w.Code != 403 {
			t.Fatal("untrusted challenge origin allowed", w.Code)
		}
	}
	challenge.Set("signature", strings.Repeat("0", 64))
	w = request(t, h, "POST", "/webmail/continue", sessionCookie+"=session-secret", challenge.Encode(), "https://webmail.example.test")
	if w.Code != 401 {
		t.Fatal("forged challenge allowed", w.Code)
	}
}
