package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const webmailTestBox = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
const webmailTestState = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
const webmailTestKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type httpHandoffs struct {
	issued, redeemed, checked     int
	issueErr, redeemErr, checkErr error
	ticket                        store.Handoff
	grant                         store.MailboxGrant
	cookie, box, browser          string
}

func (d *httpHandoffs) IssueHandoff(_ context.Context, cookie, box, browser string) (store.Handoff, error) {
	d.issued++
	d.cookie, d.box, d.browser = cookie, box, browser
	return d.ticket, d.issueErr
}
func (d *httpHandoffs) RedeemHandoff(context.Context, string, string, string, string) (store.MailboxGrant, error) {
	d.redeemed++
	return d.grant, d.redeemErr
}
func (d *httpHandoffs) CheckMailboxGrant(_ context.Context, g store.MailboxGrant) (store.MailboxGrant, error) {
	d.checked++
	return g, d.checkErr
}

func signedWebmailChallenge(box, browser string, now time.Time) url.Values {
	issued := strconv.FormatInt(now.Unix(), 10)
	key, _ := hex.DecodeString(webmailTestKey)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("anas-mail-webmail-handoff-v1\n" + box + "\n" + browser + "\n" + issued))
	return url.Values{"mailbox_id": {box}, "browser_state": {browser}, "issued_at": {issued}, "signature": {hex.EncodeToString(mac.Sum(nil))}}
}

func webmailFixture(t *testing.T) (*loginHandler, *httpStore, *httpDirectory, *httpHandoffs) {
	t.Helper()
	h, s, _, d := loginFixture(t)
	handoffs := &httpHandoffs{
		ticket: store.Handoff{Code: "handoff_" + strings.Repeat("a", 64), SessionID: fixtureAnchor, MailboxID: webmailTestBox, Expires: time.Now().Add(30 * time.Second)},
		grant:  store.MailboxGrant{SessionID: fixtureAnchor, MailboxID: webmailTestBox, OwnerAnchor: fixtureAnchor, Issuer: fixtureIssuer, ProtocolLogin: "mbx_opaque", RoundcubeKey: "webmail_opaque", DefaultAddress: "alice@example.test", AuthorizationEpoch: 1},
	}
	if err := h.configureWebmail(webmailConfig{URL: "https://webmail.example.test", ServiceToken: webmailTestKey}, handoffs); err != nil {
		t.Fatal(err)
	}
	return h, s, d, handoffs
}

func redemptionForm() url.Values {
	return url.Values{"code": {"handoff_" + strings.Repeat("a", 64)}, "session_id": {fixtureAnchor}, "mailbox_id": {webmailTestBox}, "browser_state": {webmailTestState}}
}

func redeemRequest(h http.Handler, form url.Values, mutate func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://mail.example.test/internal/webmail/redeem", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Authorization", "Bearer "+webmailTestKey)
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWebmailHTTPBoundExchange(t *testing.T) {
	h, _, dir, data := webmailFixture(t)
	form := signedWebmailChallenge(webmailTestBox, webmailTestState, time.Now())
	w := request(t, h, "POST", "/api/webmail/handoff", sessionCookie+"=session-secret", form.Encode(), "https://mail.example.test")
	if w.Code != 200 || data.issued != 1 || data.cookie != "session-secret" || data.box != webmailTestBox || data.browser != webmailTestState || dir.reads != 1 {
		t.Fatal("issue binding", w.Code, w.Body.String())
	}
	var issued struct{ Code, PostTo string }
	var values map[string]any
	if json.Unmarshal(w.Body.Bytes(), &values) != nil || values["code"] != data.ticket.Code || values["post_to"] != "https://webmail.example.test/?_task=login&_action=plugin.anas_login" {
		t.Fatal("fixed POST destination", issued)
	}
	if w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "session-secret") || strings.Contains(w.Body.String(), webmailTestKey) {
		t.Fatal("handoff in redirect or credential disclosure")
	}
	w = redeemRequest(h, redemptionForm(), nil)
	if w.Code != 200 || data.redeemed != 1 || data.checked != 1 || dir.reads != 2 || !strings.Contains(w.Body.String(), "mbx_opaque") {
		t.Fatal("redeem", w.Code, w.Body.String())
	}
	for _, secret := range []string{"owner_anchor", "Issuer", "password", "credential", webmailTestKey, data.ticket.Code} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("unexpected redemption disclosure", secret)
		}
	}
}

func TestWebmailIssueRejectsForgedChallengesAndRequests(t *testing.T) {
	for _, kind := range []string{"no origin", "foreign origin", "query", "unknown field", "duplicate field", "bad encoding", "oversize", "bad signature", "changed mailbox", "changed browser", "expired", "future", "no cookie", "duplicate cookie", "session denied", "session database", "wrong issuer", "directory denied", "directory outage", "pending mailbox", "issue database"} {
		t.Run(kind, func(t *testing.T) {
			h, session, dir, data := webmailFixture(t)
			form := signedWebmailChallenge(webmailTestBox, webmailTestState, time.Now())
			path, origin, cookie := "/api/webmail/handoff", "https://mail.example.test", sessionCookie+"=session-secret"
			want, issueCalls := 401, 0
			switch kind {
			case "no origin":
				origin, want = "", 403
			case "foreign origin":
				origin, want = "https://evil.test", 403
			case "query":
				path, want = path+"?code=not-allowed", 400
			case "unknown field":
				form.Set("owner", fixtureAnchor)
				want = 400
			case "duplicate field":
				form.Add("browser_state", webmailTestState)
				want = 400
			case "oversize":
				form.Set("signature", strings.Repeat("x", 3000))
				want = 400
			case "bad signature":
				form.Set("signature", strings.Repeat("0", 64))
			case "changed mailbox":
				form.Set("mailbox_id", fixtureAnchor)
			case "changed browser":
				form.Set("browser_state", strings.Repeat("B", 43))
			case "expired":
				form = signedWebmailChallenge(webmailTestBox, webmailTestState, time.Now().Add(-61*time.Second))
			case "future":
				form = signedWebmailChallenge(webmailTestBox, webmailTestState, time.Now().Add(10*time.Second))
			case "no cookie":
				cookie = ""
			case "duplicate cookie":
				cookie += "; " + cookie
			case "session denied":
				session.sessionErr = store.ErrSessionDenied
			case "session database":
				session.sessionErr = errors.New("private-password")
				want = 503
			case "wrong issuer":
				session.session.Issuer = "https://other.test"
			case "directory denied":
				dir.err = identity.ErrAdmission
			case "directory outage":
				dir.err = identity.ErrDirectoryUnavailable
				want = 503
			case "pending mailbox":
				data.issueErr = store.ErrHandoffDenied
				issueCalls = 1
			case "issue database":
				data.issueErr = errors.New("private-password")
				want, issueCalls = 503, 1
			}
			body := form.Encode()
			if kind == "bad encoding" {
				body += "&invalid=%zz"
				want = 400
			}
			w := request(t, h, "POST", path, cookie, body, origin)
			if w.Code != want || data.issued != issueCalls || strings.Contains(w.Body.String(), "private-password") || strings.Contains(w.Body.String(), data.ticket.Code) {
				t.Fatal(kind, w.Code, w.Body.String(), data.issued)
			}
			if kind == "directory denied" && session.denied != 1 {
				t.Fatal("AD denial not persisted")
			}
			if kind == "directory outage" && session.denied != 0 {
				t.Fatal("outage recorded as denial")
			}
		})
	}
}

func TestWebmailRedemptionRequiresServiceAndExactBody(t *testing.T) {
	for _, kind := range []string{"missing auth", "bad auth", "duplicate auth", "origin", "cookie", "wrong host", "query", "json", "duplicate type", "unknown field", "duplicate field", "missing field", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			h, _, dir, data := webmailFixture(t)
			form := redemptionForm()
			want := 401
			switch kind {
			case "unknown field":
				form.Set("return_to", "https://evil.test")
				want = 400
			case "duplicate field":
				form.Add("code", "other")
				want = 400
			case "missing field":
				form.Del("browser_state")
				want = 400
			case "oversize":
				form.Set("code", strings.Repeat("x", 3000))
				want = 400
			case "wrong host", "query", "json", "duplicate type":
				want = 400
			}
			w := redeemRequest(h, form, func(r *http.Request) {
				switch kind {
				case "missing auth":
					r.Header.Del("Authorization")
				case "bad auth":
					r.Header.Set("Authorization", "Bearer wrong")
				case "duplicate auth":
					r.Header.Add("Authorization", "Bearer "+webmailTestKey)
				case "origin":
					r.Header.Set("Origin", "https://mail.example.test")
				case "cookie":
					r.Header.Set("Cookie", sessionCookie+"=session-secret")
				case "wrong host":
					r.Host = "evil.test"
					r.Header.Set("X-Forwarded-Host", "mail.example.test")
				case "query":
					r.URL.RawQuery = "code=query-code"
				case "json":
					r.Header.Set("Content-Type", "application/json")
				case "duplicate type":
					r.Header.Add("Content-Type", "application/x-www-form-urlencoded")
				}
			})
			if w.Code != want || data.redeemed != 0 || dir.reads != 0 {
				t.Fatal(kind, w.Code, "unauthenticated request touched state")
			}
		})
	}
}

func TestWebmailRedemptionRechecksCurrentAdmission(t *testing.T) {
	for _, kind := range []string{"code denied", "database", "wrong issuer", "directory denied", "directory outage", "late logout", "late withholding", "check database"} {
		t.Run(kind, func(t *testing.T) {
			h, session, dir, data := webmailFixture(t)
			want := 401
			switch kind {
			case "code denied":
				data.redeemErr = store.ErrHandoffDenied
			case "database":
				data.redeemErr = errors.New("private-password")
				want = 503
			case "wrong issuer":
				data.grant.Issuer = "https://other.test"
			case "directory denied":
				dir.err = identity.ErrAdmission
			case "directory outage":
				dir.err = identity.ErrDirectoryUnavailable
				want = 503
			case "late logout", "late withholding":
				dir.afterRead = func() { data.checkErr = store.ErrHandoffDenied }
			case "check database":
				data.checkErr = errors.New("private-password")
				want = 503
			}
			w := redeemRequest(h, redemptionForm(), nil)
			if w.Code != want || data.redeemed != 1 || strings.Contains(w.Body.String(), "private-password") || strings.Contains(w.Body.String(), "mbx_opaque") {
				t.Fatal(kind, w.Code, w.Body.String())
			}
			if kind == "directory denied" && session.denied != 1 {
				t.Fatal("authoritative denial not saved")
			}
			if kind == "directory outage" && session.denied != 0 {
				t.Fatal("directory outage revoked person")
			}
		})
	}
}

func TestWebmailBindingIsExplicitAndFixed(t *testing.T) {
	h, _, _, _ := loginFixture(t)
	for _, path := range []string{"/api/webmail/handoff", "/internal/webmail/redeem"} {
		if w := request(t, h, "POST", path, "", "", ""); w.Code != 404 {
			t.Fatal("unconfigured endpoint published")
		}
	}
	for _, cfg := range []webmailConfig{
		{URL: "http://webmail.example.test", ServiceToken: webmailTestKey},
		{URL: "https://mail.example.test", ServiceToken: webmailTestKey},
		{URL: "https://user:private-password@webmail.example.test", ServiceToken: webmailTestKey},
		{URL: "https://webmail.example.test/path", ServiceToken: webmailTestKey},
		{URL: "https://webmail.example.test?", ServiceToken: webmailTestKey},
		{URL: "https://webmail.example.test", ServiceToken: "private-password"},
	} {
		if err := h.configureWebmail(cfg, &httpHandoffs{}); err == nil || strings.Contains(err.Error(), "private-password") {
			t.Fatal("unsafe binding accepted or leaked", err)
		}
	}
}
