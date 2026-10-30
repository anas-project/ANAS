package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSessionInputDoesNotAcceptLabelsOrUnsafeIssuer(t *testing.T) {
	now := time.Now()
	base := VerifiedLogin{OwnerAnchor: "11111111-2222-3333-4444-555555555555", Issuer: "https://iam.example.test/oidc", AuthenticatedAt: now, Expires: now.Add(time.Minute)}
	if !validLogin(base) {
		t.Fatal("valid verified identity rejected")
	}
	for _, issuer := range []string{"http://iam.example.test", "https://user:pass@iam.example.test", "https://iam.example.test/#secret", "https://iam.example.test/?q=secret", "https://iam.example.test/?", ""} {
		l := base
		l.Issuer = issuer
		if validLogin(l) {
			t.Fatal("unsafe issuer accepted")
		}
	}
	for _, anchor := range []string{"Alice", "alice@example.org", "11111111-2222-3333-4444-AAAAAAAAAAAA"} {
		l := base
		l.OwnerAnchor = anchor
		if validLogin(l) {
			t.Fatal("directory label accepted as identity")
		}
	}
	for _, token := range []string{"", "raw-password", "ses_123", "ses_" + string(bytes.Repeat([]byte("A"), 64))} {
		if _, ok := sessionHash(token); ok {
			t.Fatal("malformed session token accepted")
		}
		if _, err := (Store{}).Session(t.Context(), token); !errors.Is(err, ErrSessionDenied) {
			t.Fatal("malformed token reached database")
		}
	}
}

// This continuation is deliberately opt-in and refuses a fresh/already-upgraded
// DB. The requested historical baseline must survive all pending migrations.
func TestPostgreSQLUpgrade(t *testing.T) {
	if os.Getenv("MAIL_STORE_TEST_UPGRADE") != "1" {
		t.Skip("requires retained version-1 fixture; not an upgrade pass")
	}
	db, ctx := testDatabase(t)
	from := 1
	if raw := os.Getenv("MAIL_STORE_TEST_FROM_VERSION"); raw != "" {
		var err error
		from, err = strconv.Atoi(raw)
		if err != nil || from < 1 || from >= len(migrations) {
			t.Fatal("invalid historical upgrade baseline")
		}
	}
	var version int
	if db.QueryRowContext(ctx, `SELECT max(version) FROM mail.schema_migration`).Scan(&version) != nil || version != from {
		t.Fatal("upgrade test requires exactly the requested historical fixture")
	}
	snapshot := func() string {
		var v string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(json_agg(m ORDER BY m.id)::text,'[]') FROM (
			SELECT m.id,m.owner_anchor,m.kind,m.protocol_login,m.storage_ref,m.roundcube_key,m.state,m.created_at,a.address
			FROM mail.mailbox m LEFT JOIN mail.address a ON a.mailbox_id=m.id) m`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := snapshot()
	if before == "[]" {
		t.Fatal("no retained mailboxes to verify")
	}
	if err := (Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("upgrade changed mailbox identities or addresses")
	}
	var generationsValid bool
	if db.QueryRowContext(ctx, `SELECT COALESCE(bool_and(authorization_epoch=1),true) FROM mail.mailbox`).Scan(&generationsValid) != nil || !generationsValid {
		t.Fatal("existing mailbox authorization generations not initialized")
	}
	if db.QueryRowContext(ctx, `SELECT max(version) FROM mail.schema_migration`).Scan(&version) != nil || version != len(migrations) {
		t.Fatal("pending migrations not committed")
	}
	if err := (Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("repeat migration failed:", err)
	}
}

func TestPostgreSQLSessions(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := mailboxID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO mail.person(anchor) VALUES($1)`, owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	l := VerifiedLogin{OwnerAnchor: owner, Issuer: "https://iam.example.test", SID: "upstream-session-one", AuthenticatedAt: now, Expires: now.Add(30 * time.Minute)}
	if _, _, err := s.CreateSession(ctx, l); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("unknown directory admission created a session")
	}
	if _, err := db.ExecContext(ctx, `UPDATE mail.person SET directory_state='enabled' WHERE anchor=$1`, owner); err != nil {
		t.Fatal(err)
	}
	first, secret, err := s.CreateSession(ctx, l)
	if err != nil || !sessionPattern.MatchString(secret) || first.OwnerAnchor != owner {
		t.Fatal("session creation failed", err)
	}
	second, other, err := s.CreateSession(ctx, l)
	if err != nil || first.ID == second.ID || secret == other {
		t.Fatal("two logins share a local session", err)
	}
	var stored []byte
	if err := db.QueryRowContext(ctx, `SELECT token_hash FROM mail.web_session WHERE id=$1`, first.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(secret))
	if !bytes.Equal(stored, want[:]) || bytes.Contains(stored, []byte(secret)) {
		t.Fatal("raw cookie persisted instead of its hash")
	}
	loaded, err := s.Session(ctx, secret)
	if err != nil || loaded.ID != first.ID || loaded.Issuer != l.Issuer || loaded.SID != l.SID {
		t.Fatal("verified session lost its IAM binding", err)
	}
	// Revocation is durable and isolates simultaneous sessions of one person.
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.RevokeSession(ctx, secret) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := (Store{DB: db}).Session(ctx, secret); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("revoked session survived a new Store instance")
	}
	if _, err := s.Session(ctx, other); err != nil {
		t.Fatal("one session logout revoked another", err)
	}
	for _, state := range []string{"disabled", "deleted", "unknown"} {
		if _, err := db.ExecContext(ctx, `UPDATE mail.person SET directory_state=$2 WHERE anchor=$1`, owner, state); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Session(ctx, other); !errors.Is(err, ErrSessionDenied) {
			t.Fatal("unavailable person retained session access")
		}
		if _, _, err := s.CreateSession(ctx, l); !errors.Is(err, ErrSessionDenied) {
			t.Fatal("unavailable person obtained a new session")
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE mail.person SET directory_state='enabled' WHERE anchor=$1`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, secret); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("re-enabling a person resurrected a revoked session")
	}
	if _, err := db.ExecContext(ctx, `UPDATE mail.web_session SET expires_at=clock_timestamp()-interval '1 second',created_at=clock_timestamp()-interval '2 seconds',authenticated_at=clock_timestamp()-interval '3 seconds' WHERE id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, other); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("expired session accepted")
	}
	future := l
	future.AuthenticatedAt = now.Add(time.Hour)
	future.Expires = now.Add(2 * time.Hour)
	if _, _, err := s.CreateSession(ctx, future); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("future authentication accepted")
	}
	expired := l
	expired.AuthenticatedAt = now.Add(-time.Hour)
	expired.Expires = now.Add(-time.Minute)
	if _, _, err := s.CreateSession(ctx, expired); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("already expired login accepted")
	}
	if _, err := s.Session(ctx, "ses_"+string(bytes.Repeat([]byte("0"), 64))); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("unknown token accepted")
	}
	// Loss of PostgreSQL cannot fall back to an in-memory cached authorization.
	closed, err := sql.Open("pgx", "postgres://invalid@127.0.0.1:1/invalid?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if _, err := (Store{DB: closed}).Session(ctx, secret); err == nil {
		t.Fatal("database failure permitted access")
	}
}
