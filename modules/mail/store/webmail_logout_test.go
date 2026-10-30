package store

import (
	"errors"
	"testing"
	"time"
)

func TestPostgreSQLLogoutPurposeAndExactSession(t *testing.T) {
	s, ctx, g, cookie := credentialFixture(t)
	login, browser, err := s.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeLogout(ctx, login.State, browser); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("login flow used as logout", err)
	}
	if _, err = s.ConsumeLogin(ctx, login.State, browser); err != nil {
		t.Fatal("wrong purpose consumed login", err)
	}
	logout, browser, err := s.BeginLogout(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConsumeLogin(ctx, logout.State, browser); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("logout flow used as login", err)
	}
	if _, err = s.ConsumeLogout(ctx, logout.State, browser); err != nil {
		t.Fatal("logout flow lost", err)
	}
	if _, err = s.ConsumeLogout(ctx, logout.State, browser); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("logout flow replay", err)
	}
	credential, err := s.IssueCredentials(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.RefreshCredentials(ctx, credential.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	_, independent, err := s.CreateSession(ctx, VerifiedLogin{OwnerAnchor: g.OwnerAnchor, Issuer: g.Issuer, SID: "independent-logout-" + g.SessionID, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		id, err := s.RevokeCredentialSession(ctx, credential.RefreshToken)
		if err != nil || id != g.SessionID {
			t.Fatal("terminal logout via old generation", err)
		}
	}
	if _, err = s.RefreshGrant(ctx, next.RefreshToken); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("current generation survived same-session logout", err)
	}
	if _, err = s.Session(ctx, independent); err != nil {
		t.Fatal("other personnel login revoked", err)
	}
	if id, err := s.LogoutSessionID(ctx, cookie, g.Issuer); err != nil || id != g.SessionID {
		t.Fatal("revoked cookie could not match logout", err)
	}
	if _, err = s.LogoutSessionID(ctx, cookie, "https://foreign.example.test"); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("old cookie matched other issuer", err)
	}
	var tasks int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.revocation_task WHERE session_id=$1 AND mailbox_id IS NULL`, g.SessionID).Scan(&tasks); err != nil || tasks != 1 {
		t.Fatal("logout task missing or duplicated", tasks, err)
	}
}
