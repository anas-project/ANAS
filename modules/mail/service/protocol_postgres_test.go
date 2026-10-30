package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// Real TLS, HTTP and PG; AD facts are controlled test input. This does not
// substitute for the real Dovecot/Roundcube or IAM Provider acceptance.
func TestPostgreSQLProtocolHTTPCredentials(t *testing.T) {
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	owner := handoffPerson(t, db)
	r := identity.MailboxResource{ID: webmailTestBox, OwnerAnchor: owner, Kind: "primary", DefaultAddress: "protocol-" + owner + "@example.test", State: "active", Version: webmailTestBox, OperationID: webmailTestBox, OperationState: "complete"}
	r.Addresses = []identity.AddressResource{{Address: r.DefaultAddress, Kind: "primary", State: "active", Version: r.Version, OperationID: r.OperationID, OperationState: "complete"}}
	p, err := data.StageMailbox(t.Context(), r, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = data.ConfirmMailbox(t.Context(), r.ID, p.Revision, p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	_, session, err := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	dir := &protocolHTTPDirectory{resource: r}
	h, err := newLoginHandler("https://mail.example.test", fixtureIssuer, data, &httpIAM{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.configureWebmail(webmailConfig{URL: "https://webmail.example.test", ServiceToken: webmailTestKey}, data); err != nil {
		t.Fatal(err)
	}
	if err = h.configureProtocol(protocolConfig{InternalURL: "https://mail.example.test:8443", CertFile: "fixture.crt", KeyFile: "fixture.key", DovecotToken: protocolTestKey, SenderToken: strings.Repeat("e", 64), SendPerMinute: 30}, data, dir); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(h.internalHandler())
	defer server.Close()
	post := func(path, contentType, body, service string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequest("POST", server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "mail.example.test:8443"
		request.Header.Set("Content-Type", contentType)
		if service == "roundcube" {
			request.Header.Set("Authorization", "Bearer "+webmailTestKey)
		} else {
			key := protocolTestKey
			if service == "sender" {
				key = strings.Repeat("e", 64)
			}
			request.SetBasicAuth(service, key)
			request.Header.Set("X-Dovecot-Auth-Protocol", "imap")
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, raw
	}
	ticket, err := data.IssueHandoff(t.Context(), session, r.ID, webmailTestState)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"code": {ticket.Code}, "session_id": {ticket.SessionID}, "mailbox_id": {r.ID}, "browser_state": {webmailTestState}}
	status, raw := post("/internal/webmail/redeem", "application/x-www-form-urlencoded", form.Encode(), "roundcube")
	var c store.Credentials
	if status != 200 || json.Unmarshal(raw, &c) != nil || c.AccessToken == "" || c.MailboxID != r.ID {
		t.Fatal("credential redemption failed", status)
	}
	status, _ = post("/internal/webmail/redeem", "application/x-www-form-urlencoded", form.Encode(), "roundcube")
	if status != 401 {
		t.Fatal("handoff replay issued credentials", status)
	}
	check := func(want int, active bool) {
		t.Helper()
		status, raw = post("/internal/protocol/introspect", "application/x-www-form-urlencoded", url.Values{"token": {c.AccessToken}}.Encode(), "dovecot")
		var result struct{ Active bool }
		if json.Unmarshal(raw, &result) != nil || status != want || result.Active != active {
			t.Fatal("introspection", status, string(raw))
		}
	}
	check(200, true)
	submission := map[string]any{"login": c.SMTPUsername, "request_id": strings.Repeat("a", 32), "queue_id": "T" + strings.ReplaceAll(owner, "-", ""), "envelope": []string{r.DefaultAddress}, "from": []string{r.DefaultAddress}, "from_count": 1}
	for range 2 {
		body, _ := json.Marshal(submission)
		status, _ = post("/internal/protocol/sender", "application/json", string(body), "sender")
		if status != 204 {
			t.Fatal("valid/repeated submission denied", status)
		}
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM mail.smtp_submission WHERE mailbox_id=$1`, r.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("retry double counted", err, count)
	}
	submission["request_id"] = ""
	body, _ := json.Marshal(submission)
	status, _ = post("/internal/protocol/sender", "application/json", string(body), "sender")
	if status != 403 {
		t.Fatal("missing scan identity accepted", status)
	}
	submission["request_id"] = strings.Repeat("b", 32)
	body, _ = json.Marshal(submission)
	status, _ = post("/internal/protocol/sender", "application/json", string(body), "sender")
	if status != 204 {
		t.Fatal("new scan with reused queue was denied", status)
	}
	if err = db.QueryRow(`SELECT count(*) FROM mail.smtp_submission WHERE mailbox_id=$1`, r.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("new scan did not consume another allowance", err, count)
	}
	submission["from"] = []string{"foreign@example.test"}
	body, _ = json.Marshal(submission)
	status, _ = post("/internal/protocol/sender", "application/json", string(body), "sender")
	if status != 403 {
		t.Fatal("forged visible sender accepted", status)
	}
	dir.err = identity.ErrDirectoryUnavailable
	check(503, false)
	dir.err = nil
	check(200, true)
	dir.resource.Version = owner
	check(200, false)
	dir.resource = r
	if err = data.RevokeSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	check(200, false)
	status, _ = post("/internal/webmail/refresh", "application/x-www-form-urlencoded", url.Values{"refresh_token": {c.RefreshToken}}.Encode(), "roundcube")
	if status != 401 {
		t.Fatal("revoked session refreshed", status)
	}
}
