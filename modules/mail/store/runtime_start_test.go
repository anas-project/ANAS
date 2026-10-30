package store

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

// This two-process fixture is driven by server-mail-store-runtime-e2e.py. The
// server restarts the real isolated PostgreSQL process between these phases.
func TestPostgreSQLActualProcessRestart(t *testing.T) {
	phase := os.Getenv("MAIL_RUNTIME_RESTART_PHASE")
	if phase == "" {
		t.Skip("requires the explicit two-process PostgreSQL restart fixture")
	}
	const file = "/tmp/anas-mail-runtime-restart-grants.json"
	type saved struct {
		Started     time.Time
		Grant       MailboxGrant
		Cookie      string
		Credentials Credentials
		Handoff     Handoff
	}
	if phase == "before" {
		db, ctx := testDatabase(t)
		s := Store{DB: db}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		// Do not use credentialFixture here: its t.Cleanup deliberately
		// deletes its rows when the first test process exits.
		resource := projectionResource(t, "primary")
		projection, err := s.StageMailbox(ctx, resource, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ConfirmMailbox(ctx, resource.ID, projection.Revision, projection.Fingerprint); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		_, cookie, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: resource.OwnerAnchor, Issuer: "https://restart.example.test", SID: resource.ID, AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		grant, err := s.MailboxForSession(ctx, cookie, resource.ID)
		if err != nil {
			t.Fatal(err)
		}
		value := saved{Grant: grant, Cookie: cookie}
		if value.Credentials, err = s.IssueCredentials(ctx, grant); err != nil {
			t.Fatal(err)
		}
		if value.Handoff, err = s.IssueHandoff(ctx, cookie, grant.MailboxID, handoffState); err != nil {
			t.Fatal(err)
		}
		if err = s.DB.QueryRowContext(ctx, `SELECT pg_postmaster_start_time()`).Scan(&value.Started); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal("create private restart state", err)
		}
		err = json.NewEncoder(f).Encode(value)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal("persist private restart state")
		}
		return
	}
	if phase != "after" {
		t.Fatal("unknown PostgreSQL restart phase")
	}
	defer os.Remove(file)
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("private restart state required")
	}
	data, err := os.ReadFile(file)
	var value saved
	if err != nil || json.Unmarshal(data, &value) != nil {
		t.Fatal("restart state unreadable")
	}
	db, ctx := testDatabase(t)
	cleanupProjectionOwner(t, db, value.Grant.OwnerAnchor)
	s := Store{DB: db}
	var current time.Time
	if err = db.QueryRowContext(ctx, `SELECT pg_postmaster_start_time()`).Scan(&current); err != nil || current.Equal(value.Started) {
		t.Fatal("fixture did not restart the real PostgreSQL process", err)
	}
	if !time.Now().Before(value.Handoff.Expires) || !time.Now().Before(value.Credentials.Expires) {
		t.Fatal("restart exceeded grant lifetime; denial would not prove fencing")
	}
	if _, err = s.Session(ctx, value.Cookie); err != nil {
		t.Fatal("pre-fence baseline session was already denied", err)
	}
	if _, err = s.IntrospectCredential(ctx, value.Credentials.AccessToken, "imap"); err != nil {
		t.Fatal("pre-fence baseline credential was already denied", err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(ctx, value.Cookie); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("old Cookie accepted after real PostgreSQL restart", err)
	}
	if _, err = s.RedeemHandoff(ctx, value.Handoff.Code, value.Handoff.SessionID, value.Handoff.MailboxID, handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("old handoff accepted after real PostgreSQL restart", err)
	}
	for protocol, secret := range map[string]string{"imap": value.Credentials.AccessToken, "smtp": value.Credentials.SMTPToken} {
		if _, err = s.IntrospectCredential(ctx, secret, protocol); !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("old access credential accepted after real PostgreSQL restart", protocol, err)
		}
	}
	if _, err = s.RefreshCredentials(ctx, value.Credentials.RefreshToken); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("old refresh accepted after real PostgreSQL restart", err)
	}
	now := time.Now()
	_, fresh, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: value.Grant.OwnerAnchor, Issuer: value.Grant.Issuer, SID: "fresh-real-pg-restart", AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(time.Hour)})
	if err != nil {
		t.Fatal("fresh admission after real PostgreSQL restart", err)
	}
	if _, err = s.MailboxForSession(ctx, fresh, value.Grant.MailboxID); err != nil {
		t.Fatal("mailbox unavailable after real PostgreSQL restart", err)
	}
}

func TestPostgreSQLRuntimeStartFencesRestoredGrants(t *testing.T) {
	s, ctx, grant, cookie := credentialFixture(t)
	credentials, err := s.IssueCredentials(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := s.IssueHandoff(ctx, cookie, grant.MailboxID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	flow, browser, err := s.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Reopening Go against the same live PostgreSQL must preserve its session.
	if err = (Store{DB: s.DB}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(ctx, cookie); err != nil {
		t.Fatal("Go-only restart revoked a current session", err)
	}
	// This is a transaction-level recovery-state test. A separate real-host
	// test must replace/restart PostgreSQL and run the formal restore workflow.
	if _, err = s.DB.ExecContext(ctx, `UPDATE mail.runtime_start SET postgres_started_at=postgres_started_at-interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- (Store{DB: s.DB}).Migrate(ctx) }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Session(ctx, cookie); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("restored Cookie accepted", err)
	}
	if _, err = s.RedeemHandoff(ctx, handoff.Code, handoff.SessionID, handoff.MailboxID, handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("restored handoff accepted", err)
	}
	for protocol, secret := range map[string]string{"imap": credentials.AccessToken, "smtp": credentials.SMTPToken} {
		if _, err = s.IntrospectCredential(ctx, secret, protocol); !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("restored access credential accepted", protocol, err)
		}
	}
	if _, err = s.RefreshCredentials(ctx, credentials.RefreshToken); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("restored refresh credential accepted", err)
	}
	if _, err = s.ConsumeLogin(ctx, flow.State, browser); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("pre-recovery login callback accepted", err)
	}
	var tasks, revision int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*),max(revision) FROM mail.revocation_task WHERE session_id=$1 AND mailbox_id IS NULL`, grant.SessionID).Scan(&tasks, &revision); err != nil || tasks != 1 || revision != 1 {
		t.Fatal("recovery did not enqueue exactly one durable withdrawal", err, tasks, revision)
	}
	now := time.Now()
	_, fresh, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: grant.OwnerAnchor, Issuer: grant.Issuer, SID: "fresh-after-recovery", AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(time.Hour)})
	if err != nil {
		t.Fatal("fresh verified admission failed", err)
	}
	if _, err = s.MailboxForSession(ctx, fresh, grant.MailboxID); err != nil {
		t.Fatal("recovery changed mailbox ownership or storage", err)
	}
	if _, err = s.Session(ctx, cookie); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("fresh admission revived the old Cookie", err)
	}
}
