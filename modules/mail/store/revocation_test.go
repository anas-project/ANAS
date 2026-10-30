package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

func TestPostgreSQLAdmissionAndLogout(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := mailboxID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	l := VerifiedLogin{OwnerAnchor: owner, Issuer: "https://iam.example.test", SID: "sid-" + owner, AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(time.Hour)}
	_, old, err := s.AdmitSession(ctx, l)
	if err != nil {
		t.Fatal("fresh admission", err)
	}
	var boxes int
	if db.QueryRowContext(ctx, `SELECT count(*) FROM mail.mailbox WHERE owner_anchor=$1`, owner).Scan(&boxes) != nil || boxes != 0 {
		t.Fatal("login provisioned a mailbox")
	}
	if err := s.DenyPerson(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, old); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("denied person retained session")
	}
	_, current, err := s.AdmitSession(ctx, l)
	if err != nil {
		t.Fatal("fresh readmission", err)
	}
	if _, err := s.Session(ctx, old); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("readmission resurrected old session")
	}
	otherLogin := l
	otherLogin.SID += "-independent"
	_, other, err := s.AdmitSession(ctx, otherLogin)
	if err != nil {
		t.Fatal(err)
	}
	logout := identity.Logout{Issuer: l.Issuer, ID: "logout-" + owner, Anchor: owner, SID: l.SID, IssuedAt: now}
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ApplyLogout(ctx, logout) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent logout", err)
		}
	}
	if _, err := (Store{DB: db}).Session(ctx, current); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("logout not durable")
	}
	if _, err := s.Session(ctx, other); err != nil {
		t.Fatal("unrelated IAM session revoked", err)
	}
	if _, _, err := s.AdmitSession(ctx, l); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("late callback resurrected logged-out sid", err)
	}
	conflict := logout
	conflict.SID = otherLogin.SID
	if err := s.ApplyLogout(ctx, conflict); err == nil {
		t.Fatal("jti target changed")
	}
	if _, err := s.Session(ctx, other); err != nil {
		t.Fatal("conflicting replay changed authorization")
	}
	// A notification before its first callback also blocks session creation.
	before := logout
	before.ID += "-before"
	before.SID += "-before"
	if err := s.ApplyLogout(ctx, before); err != nil {
		t.Fatal(err)
	}
	late := l
	late.SID = before.SID
	if _, _, err := s.AdmitSession(ctx, late); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("pre-callback logout lost")
	}
	// With no sid, only authentications at/before the logout are revoked.
	all := logout
	all.ID += "-subject"
	all.SID = ""
	if err := s.ApplyLogout(ctx, all); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, other); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("subject logout missed another session")
	}
	fresh := l
	fresh.SID += "-fresh"
	fresh.AuthenticatedAt = now.Add(time.Second)
	if _, _, err := s.AdmitSession(ctx, fresh); err != nil {
		t.Fatal("fresh IAM authentication rejected", err)
	}
	// Logout needs PostgreSQL only: no directory object is passed to this path.
}

func TestPostgreSQLLoginLogoutRace(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		owner, err := mailboxID()
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		l := VerifiedLogin{OwnerAnchor: owner, Issuer: "https://iam.example.test", SID: "race-" + owner, AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(time.Hour)}
		logout := identity.Logout{Issuer: l.Issuer, ID: l.SID, SID: l.SID, IssuedAt: now}
		start := make(chan struct{})
		done := make(chan error, 1)
		go func() { <-start; done <- s.ApplyLogout(ctx, logout) }()
		close(start)
		_, secret, createErr := s.AdmitSession(ctx, l)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if createErr != nil && !errors.Is(createErr, ErrSessionDenied) {
			t.Fatal(createErr)
		}
		if secret != "" {
			if _, err := s.Session(ctx, secret); !errors.Is(err, ErrSessionDenied) {
				t.Fatal("race left a live session")
			}
		}
	}
}
