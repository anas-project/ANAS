// TEST_CASES: MAILMOD-T-014
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const addressRequest = `{"operation_id":"44444444-4444-4444-8444-444444444444","version":"11111111-1111-4111-8111-111111111111","action":"additional.create","address":"extra@example.test"}`

type addressStoreFake struct {
	*httpStore
	audits, finishes      int
	auditErr, errorFinish error
}

func (s *addressStoreFake) BeginAddressAudit(context.Context, string, string, directorymail.MailRequest) (int64, error) {
	s.audits++
	return 1, s.auditErr
}
func (s *addressStoreFake) FinishAddressAudit(context.Context, int64, string) error {
	s.finishes++
	return s.errorFinish
}

type addressFake struct {
	writes       int
	actor, owner string
	err          error
	before       func()
}

func (d *addressFake) View(context.Context, string, string) (directorymail.ManagementView, error) {
	return directorymail.ManagementView{Policy: directorymail.Policy{Protected: []string{"secret@example.test"}}}, d.err
}
func (d *addressFake) Apply(_ context.Context, a, o string, _ directorymail.MailRequest, check func() error) (directorymail.ManagementResult, error) {
	if d.before != nil {
		d.before()
	}
	if check() != nil {
		return directorymail.ManagementResult{}, directorymail.ErrDenied
	}
	d.writes++
	d.actor, d.owner = a, o
	return directorymail.ManagementResult{}, d.err
}
func addressFixture(t *testing.T) (*loginHandler, *addressStoreFake, *addressFake) {
	h, data, _, _ := loginFixture(t)
	data.session.AuthenticatedAt = time.Now()
	s := &addressStoreFake{httpStore: data}
	d := &addressFake{}
	h.store = s
	h.addresses = d
	h.directory = &setupHTTPDirectory{httpDirectory: &httpDirectory{}, admin: false}
	return h, s, d
}
func addressCall(h *loginHandler, body, owner, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://mail.example.test/api/addresses"+owner, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAddressManagementAuthorizationAuditAndRevocation(t *testing.T) {
	for _, scenario := range []string{"self", "foreign", "origin", "old-auth", "audit-down", "revoked", "quota", "audit-result-down"} {
		t.Run(scenario, func(t *testing.T) {
			h, s, d := addressFixture(t)
			owner, origin := "", "https://mail.example.test"
			want := 200
			switch scenario {
			case "foreign":
				owner = "?owner=22222222-2222-4222-8222-222222222222"
				want = 403
			case "origin":
				origin = "https://other.example"
				want = 403
			case "old-auth":
				s.session.AuthenticatedAt = time.Now().Add(-6 * time.Minute)
				want = 403
			case "audit-down":
				s.auditErr = errors.New("disk")
				want = 503
			case "revoked":
				d.before = func() { s.sessionErr = store.ErrSessionDenied }
			case "quota":
				d.err = directorymail.ErrQuota
			case "audit-result-down":
				s.errorFinish = errors.New("disk")
			}
			w := addressCall(h, addressRequest, owner, origin)
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if scenario == "self" && (d.writes != 1 || d.actor != fixtureAnchor || d.owner != fixtureAnchor || s.audits != 1 || s.finishes != 1) {
				t.Fatal("identity/audit lost")
			}
			if want != 200 || scenario == "revoked" {
				if d.writes != 0 {
					t.Fatal("denied operation wrote")
				}
			}
			if scenario == "quota" && !strings.Contains(w.Body.String(), `"directory":"quota"`) {
				t.Fatal("error became success")
			}
			if scenario == "audit-result-down" && !strings.Contains(w.Body.String(), `"audit":"unavailable"`) {
				t.Fatal("audit failure hidden")
			}
		})
	}
}
func TestAddressManagementStrictRequestAndOrigin(t *testing.T) {
	for _, body := range []string{addressRequest + `{}`, strings.TrimSuffix(addressRequest, "}") + `,"address":"other@example.test"}`, strings.Replace(addressRequest, `"address"`, `"Address"`, 1), strings.Replace(addressRequest, `"extra@example.test"`, `null`, 1), strings.TrimSuffix(addressRequest, "}") + `,"owner":"forged"}`, strings.TrimSuffix(addressRequest, "}") + `,"mailbox_id":null}`} {
		h, _, d := addressFixture(t)
		w := addressCall(h, body, "", "https://mail.example.test")
		if w.Code != 400 || d.writes != 0 {
			t.Fatal("ambiguous request accepted", w.Code)
		}
	}
}
func TestAddressReadDoesNotExposeProtectedPolicyToOrdinaryUser(t *testing.T) {
	h, _, _ := addressFixture(t)
	w := request(t, h, "GET", "/api/addresses", sessionCookie+"=session-secret", "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret@example.test") {
		t.Fatal("policy leaked", w.Code)
	}
}

func (d *addressFake) Person(_ context.Context, owner string) (identity.Person, error) {
	return identity.Person{Anchor: owner, Username: "alice", DisplayName: "Alice"}, d.err
}
func (d *addressFake) People(context.Context, string) ([]identity.Person, error) { return nil, d.err }

func (s *addressStoreFake) AdmissionGroup(context.Context, string) (string, error) {
	return "private-group", nil
}
func TestAddressAdministratorAuthorizationIsRechecked(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		h, _, d := addressFixture(t)
		directory := &setupHTTPDirectory{httpDirectory: &httpDirectory{}, admin: true}
		h.directory = directory
		if revoked {
			d.before = func() { directory.admin = false }
		}
		w := addressCall(h, addressRequest, "?owner=22222222-2222-4222-8222-222222222222", "https://mail.example.test")
		if w.Code != 200 || revoked && d.writes != 0 || !revoked && d.writes != 1 {
			t.Fatal("administrator authorization mismatch", w.Code, w.Body.String(), d.writes)
		}
	}
}
func TestAddressReauthenticationRejectsOldOrDifferentIdentity(t *testing.T) {
	for _, mode := range []string{"fresh", "old", "other"} {
		t.Run(mode, func(t *testing.T) {
			h, s, _ := addressFixture(t)
			iam := &httpIAM{auth: identity.Authentication{Anchor: fixtureAnchor, Issuer: fixtureIssuer, AuthenticatedAt: time.Now(), Expires: time.Now().Add(time.Hour)}}
			h.reauthIAM = iam
			if mode == "old" {
				iam.auth.AuthenticatedAt = time.Now().Add(-time.Hour)
			}
			if mode == "other" {
				iam.auth.Anchor = "22222222-2222-4222-8222-222222222222"
			}
			w := request(t, h, "GET", "/oidc/reauth-callback?state="+s.flow.State+"&code=code", flowCookie+"=browser-secret; "+sessionCookie+"=session-secret", "", "")
			if mode == "fresh" {
				if w.Code != 303 || w.Header().Get("Location") != "/addresses" {
					t.Fatal("fresh reauthentication failed", w.Code)
				}
			} else if w.Code != 401 || s.admitted != 0 {
				t.Fatal("invalid reauthentication admitted", w.Code)
			}
		})
	}
}

func (s *addressStoreFake) PinAdmissionGroup(context.Context, string, string) error { return nil }

func TestAddressLoginReturnIsBoundedToMailOwner(t *testing.T) {
	h, s, _ := addressFixture(t)
	target := "22222222-2222-4222-8222-222222222222"
	page := request(t, h, "GET", "/addresses?owner="+target, "", "", "")
	if page.Code != 303 || page.Header().Get("Location") != "/login" {
		t.Fatal("missing login navigation")
	}
	var returned bool
	for _, c := range page.Result().Cookies() {
		if c.Name == addressReturnCookie && c.Value == target && c.HttpOnly && c.Secure {
			returned = true
		}
	}
	if !returned {
		t.Fatal("lost return target")
	}
	w := request(t, h, "GET", "/oidc/callback?state="+s.flow.State+"&code=code", flowCookie+"=browser-secret; "+addressReturnCookie+"="+target, "", "")
	if w.Code != 303 || w.Header().Get("Location") != "/addresses?owner="+target {
		t.Fatal("login lost original target", w.Code, w.Header())
	}
	if w = request(t, h, "GET", "/addresses?owner=https://other.example", "", "", ""); w.Code != 400 {
		t.Fatal("external return accepted")
	}
}

func TestAddressPageExpiredSessionPreservesTargetButStoreFailureDoesNotRedirect(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		h, s, _ := addressFixture(t)
		s.sessionErr = store.ErrSessionDenied
		want := http.StatusSeeOther
		if unavailable {
			s.sessionErr = errors.New("database unavailable")
			want = http.StatusServiceUnavailable
		}
		target := "22222222-2222-4222-8222-222222222222"
		w := request(t, h, "GET", "/addresses?owner="+target, sessionCookie+"=expired", "", "")
		if w.Code != want {
			t.Fatal("unexpected session fallback", w.Code)
		}
		if unavailable {
			if w.Header().Get("Location") != "" {
				t.Fatal("store failure caused login loop")
			}
			continue
		}
		var cleared, returned bool
		for _, cookie := range w.Result().Cookies() {
			cleared = cleared || cookie.Name == sessionCookie && cookie.MaxAge < 0
			returned = returned || cookie.Name == addressReturnCookie && cookie.Value == target
		}
		if w.Header().Get("Location") != "/login" || !cleared || !returned {
			t.Fatal("expired session lost bounded login target")
		}
	}
}

func TestAddressPageAcceptsLanguageAndRejectsAmbiguousOwner(t *testing.T) {
	h, _, _ := addressFixture(t)
	for _, query := range []string{"?lang=zh-CN", "?owner=" + fixtureAnchor + "&lang=de"} {
		w := request(t, h, "GET", "/addresses"+query, sessionCookie+"=session-secret", "", "")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "addresses.js") {
			t.Fatal("language navigation failed", w.Code)
		}
	}
	w := request(t, h, "GET", "/addresses?owner="+fixtureAnchor+"&owner="+fixtureAnchor, sessionCookie+"=session-secret", "", "")
	if w.Code != http.StatusBadRequest {
		t.Fatal("ambiguous owner accepted", w.Code)
	}
}
