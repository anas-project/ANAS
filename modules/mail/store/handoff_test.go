package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const handoffState = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestHandoffRejectsUntrustedInputBeforeDatabase(t *testing.T) {
	s := Store{}
	for _, input := range []struct{ cookie, box, browser string }{
		{"not-a-session", "11111111-2222-3333-4444-555555555555", handoffState},
		{"ses_" + strings.Repeat("a", 64), "alice@example.test", handoffState},
		{"ses_" + strings.Repeat("a", 64), "11111111-2222-3333-4444-555555555555", "caller-return-url"},
	} {
		if _, err := s.IssueHandoff(t.Context(), input.cookie, input.box, input.browser); !errors.Is(err, ErrHandoffDenied) {
			t.Fatal(err)
		}
	}
	if _, err := s.RedeemHandoff(t.Context(), "raw-password", "not-a-session", "not-a-mailbox", handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal(err)
	}
	if err := s.WithholdMailbox(t.Context(), "alice@example.test"); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal(err)
	}
	if _, err := s.CheckMailboxGrant(t.Context(), MailboxGrant{}); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal(err)
	}
}

func TestPostgreSQLHandoffGrantFreshness(t *testing.T) {
	db, ctx := testDatabase(t)
	f := newHandoffFixture(t, db, ctx)
	f.activate(t, ctx, f.box.ID)
	h, err := f.data.IssueHandoff(ctx, f.secret, f.box.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.data.RedeemHandoff(ctx, h.Code, h.SessionID, h.MailboxID, handoffState)
	if err != nil || g.Issuer != "https://iam.example.test" {
		t.Fatal("issuer binding", err)
	}
	if fresh, err := (Store{DB: db}).CheckMailboxGrant(ctx, g); err != nil || fresh != g {
		t.Fatal("fresh grant", err)
	}
	for _, kind := range []string{"owner", "issuer", "epoch", "session", "box"} {
		bad := g
		switch kind {
		case "owner":
			bad.OwnerAnchor = "11111111-2222-3333-4444-555555555555"
		case "issuer":
			bad.Issuer = "https://other.test"
		case "epoch":
			bad.AuthorizationEpoch++
		case "session":
			bad.SessionID = "11111111-2222-3333-4444-555555555555"
		case "box":
			bad.MailboxID = "11111111-2222-3333-4444-555555555555"
		}
		if _, err := f.data.CheckMailboxGrant(ctx, bad); !errors.Is(err, ErrHandoffDenied) {
			t.Fatal(kind, err)
		}
	}
	if err := f.data.WithholdMailbox(ctx, f.box.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.data.CheckMailboxGrant(ctx, g); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("late withdrawal accepted", err)
	}
	f.activate(t, ctx, f.box.ID)
	if _, err := f.data.CheckMailboxGrant(ctx, g); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("withdrawn grant revived", err)
	}
}

type handoffFixture struct {
	data    Store
	db      *sql.DB
	box     Mailbox
	session Session
	secret  string
}

func newHandoffFixture(t *testing.T, db *sql.DB, ctx context.Context) *handoffFixture {
	t.Helper()
	owner, err := mailboxID()
	if err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db}
	box, err := s.EnsurePrimary(ctx, owner, "m-"+owner+"@example.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := db.BeginTx(c, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer tx.Rollback()
		for _, query := range []string{`DELETE FROM mail.revocation_task WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`,
			`DELETE FROM mail.webmail_handoff WHERE session_id IN (SELECT id FROM mail.web_session WHERE owner_anchor=$1)`,
			`DELETE FROM mail.address WHERE mailbox_id IN (SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`,
			`DELETE FROM mail.mailbox WHERE owner_anchor=$1`,
			`DELETE FROM mail.web_session WHERE owner_anchor=$1`,
			`DELETE FROM mail.person WHERE anchor=$1`,
		} {
			if _, err = tx.ExecContext(c, query, owner); err != nil {
				t.Error(err)
				return
			}
		}
		if err = tx.Commit(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now()
	session, secret, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: owner, Issuer: "https://iam.example.test", SID: "handoff-" + owner, AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return &handoffFixture{data: s, db: db, box: box, session: session, secret: secret}
}

// Activation is test-only: no runtime API currently approves an AD mailbox
// mapping or confirms DMS/Roundcube configuration. Pending stays closed there.
func (f *handoffFixture) activate(t *testing.T, ctx context.Context, box string) {
	t.Helper()
	if _, err := f.db.ExecContext(ctx, `UPDATE mail.mailbox SET state='active' WHERE id=$1`, box); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE mail.address SET state='active' WHERE mailbox_id=$1`, box); err != nil {
		t.Fatal(err)
	}
}

func (f *handoffFixture) additional(t *testing.T, ctx context.Context) Mailbox {
	t.Helper()
	m := Mailbox{OwnerAnchor: f.session.OwnerAnchor, State: "pending"}
	var err error
	if m.ID, err = mailboxID(); err != nil {
		t.Fatal(err)
	}
	m.DefaultAddress = "additional-" + m.ID + "@example.test"
	if m.ProtocolLogin, err = token("mbx_"); err != nil {
		t.Fatal(err)
	}
	if m.StorageRef, err = token("store_"); err != nil {
		t.Fatal(err)
	}
	if m.RoundcubeKey, err = token("webmail_"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(ctx, `INSERT INTO mail.mailbox(id,owner_anchor,kind,protocol_login,storage_ref,roundcube_key) VALUES($1,$2,'additional',$3,$4,$5)`, m.ID, m.OwnerAnchor, m.ProtocolLogin, m.StorageRef, m.RoundcubeKey); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(ctx, `INSERT INTO mail.address(address,mailbox_id,kind,is_default) VALUES($1,$2,'independent',true)`, m.DefaultAddress, m.ID); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPostgreSQLHandoffBindings(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := newHandoffFixture(t, db, ctx)
	other := newHandoffFixture(t, db, ctx)
	if _, err := s.IssueHandoff(ctx, f.secret, f.box.ID, handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("pending mailbox granted handoff", err)
	}
	f.activate(t, ctx, f.box.ID)
	other.activate(t, ctx, other.box.ID)
	additional := f.additional(t, ctx)
	f.activate(t, ctx, additional.ID)
	if _, err := s.IssueHandoff(ctx, f.secret, other.box.ID, handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("another person's box granted", err)
	}
	ticket, err := s.IssueHandoff(ctx, f.secret, f.box.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	if !handoffCodePattern.MatchString(ticket.Code) || ticket.SessionID != f.session.ID || ticket.MailboxID != f.box.ID || time.Until(ticket.Expires) > 31*time.Second {
		t.Fatal("invalid handoff binding/expiry")
	}
	var codeHash, browserHash []byte
	if err := db.QueryRowContext(ctx, `SELECT code_hash,browser_hash FROM mail.webmail_handoff WHERE session_id=$1`, f.session.ID).Scan(&codeHash, &browserHash); err != nil {
		t.Fatal(err)
	}
	wantCode, wantBrowser := sha256.Sum256([]byte(ticket.Code)), sha256.Sum256([]byte(handoffState))
	if !bytes.Equal(codeHash, wantCode[:]) || !bytes.Equal(browserHash, wantBrowser[:]) {
		t.Fatal("raw handoff/browser secret persisted")
	}
	wrongID, err := mailboxID()
	if err != nil {
		t.Fatal(err)
	}
	secondSession, _, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: f.session.OwnerAnchor, Issuer: f.session.Issuer, SID: f.session.SID + "-second", AuthenticatedAt: f.session.AuthenticatedAt, Expires: f.session.Expires})
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []struct{ session, box, browser string }{
		{wrongID, ticket.MailboxID, handoffState},
		{secondSession.ID, ticket.MailboxID, handoffState},
		{other.session.ID, ticket.MailboxID, handoffState},
		{ticket.SessionID, other.box.ID, handoffState},
		{ticket.SessionID, additional.ID, handoffState},
		{ticket.SessionID, ticket.MailboxID, strings.Repeat("B", 43)},
	} {
		if _, err := s.RedeemHandoff(ctx, ticket.Code, wrong.session, wrong.box, wrong.browser); !errors.Is(err, ErrHandoffDenied) {
			t.Fatal("wrong binding accepted", err)
		}
	}
	// A newly constructed Store can redeem it once, with no memory state.
	grant, err := (Store{DB: db}).RedeemHandoff(ctx, ticket.Code, ticket.SessionID, ticket.MailboxID, handoffState)
	if err != nil || grant.OwnerAnchor != f.session.OwnerAnchor || grant.ProtocolLogin != f.box.ProtocolLogin || grant.RoundcubeKey != f.box.RoundcubeKey || grant.DefaultAddress != f.box.DefaultAddress || grant.AuthorizationEpoch != 1 {
		t.Fatal("bound grant mismatch", err)
	}
	if _, err := s.RedeemHandoff(ctx, ticket.Code, ticket.SessionID, ticket.MailboxID, handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("handoff replay", err)
	}
}

func TestPostgreSQLHandoffExpiryAndCleanup(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := newHandoffFixture(t, db, ctx)
	f.activate(t, ctx, f.box.ID)
	now := time.Now()
	short, secret, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: f.session.OwnerAnchor, Issuer: f.session.Issuer, SID: f.session.SID + "-short", AuthenticatedAt: now.Add(-time.Minute), Expires: now.Add(20 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.IssueHandoff(ctx, secret, f.box.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	if first.Expires.After(short.Expires) {
		t.Fatal("handoff outlived its person session")
	}
	hash := sha256.Sum256([]byte(first.Code))
	if _, err = db.ExecContext(ctx, `UPDATE mail.webmail_handoff SET created_at=clock_timestamp()-interval '2 seconds',expires_at=clock_timestamp()-interval '1 second' WHERE code_hash=$1`, hash[:]); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.IssueHandoff(ctx, secret, f.box.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	var old int
	if db.QueryRowContext(ctx, `SELECT count(*) FROM mail.webmail_handoff WHERE code_hash=$1`, hash[:]).Scan(&old) != nil || old != 0 {
		t.Fatal("expired ticket not reclaimed")
	}
	if _, err = s.RedeemHandoff(ctx, fresh.Code, fresh.SessionID, fresh.MailboxID, handoffState); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLConcurrentHandoffRedemption(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := newHandoffFixture(t, db, ctx)
	f.activate(t, ctx, f.box.ID)
	ticket, err := s.IssueHandoff(ctx, f.secret, f.box.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.RedeemHandoff(ctx, ticket.Code, ticket.SessionID, ticket.MailboxID, handoffState)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrHandoffDenied) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("concurrent winners", winners)
	}
}

func TestPostgreSQLHandoffRevocation(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"session revoked", "session expired", "person denied and readmitted", "mailbox withheld and reenabled", "address not ready", "expired ticket"} {
		t.Run(kind, func(t *testing.T) {
			f := newHandoffFixture(t, db, ctx)
			f.activate(t, ctx, f.box.ID)
			ticket, err := s.IssueHandoff(ctx, f.secret, f.box.ID, handoffState)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "session revoked":
				err = s.RevokeSession(ctx, f.secret)
			case "session expired":
				_, err = db.ExecContext(ctx, `UPDATE mail.web_session SET authenticated_at=clock_timestamp()-interval '3 seconds',created_at=clock_timestamp()-interval '2 seconds',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, f.session.ID)
			case "person denied and readmitted":
				err = s.DenyPerson(ctx, f.session.OwnerAnchor)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				_, _, err = s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: f.session.OwnerAnchor, Issuer: f.session.Issuer, SID: f.session.SID + "-new", AuthenticatedAt: now, Expires: now.Add(time.Hour)})
			case "mailbox withheld and reenabled":
				err = s.WithholdMailbox(ctx, f.box.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.WithholdMailbox(ctx, f.box.ID); err != nil {
					t.Fatal(err)
				}
				var epoch int64
				if db.QueryRowContext(ctx, `SELECT authorization_epoch FROM mail.mailbox WHERE id=$1`, f.box.ID).Scan(&epoch) != nil || epoch != 2 {
					t.Fatal("withholding is not idempotent")
				}
				f.activate(t, ctx, f.box.ID)
			case "address not ready":
				_, err = db.ExecContext(ctx, `UPDATE mail.address SET state='pending' WHERE mailbox_id=$1`, f.box.ID)
			case "expired ticket":
				_, err = db.ExecContext(ctx, `UPDATE mail.webmail_handoff SET created_at=clock_timestamp()-interval '2 seconds',expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1`, f.session.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.RedeemHandoff(ctx, ticket.Code, ticket.SessionID, ticket.MailboxID, handoffState); !errors.Is(err, ErrHandoffDenied) {
				t.Fatal("revoked handoff accepted", err)
			}
			var left int
			if db.QueryRowContext(ctx, `SELECT count(*) FROM mail.webmail_handoff WHERE session_id=$1`, f.session.ID).Scan(&left) != nil || left != 0 {
				t.Fatal("correctly bound denied code not consumed")
			}
			if kind == "mailbox withheld and reenabled" {
				fresh, err := s.IssueHandoff(ctx, f.secret, f.box.ID, handoffState)
				if err != nil {
					t.Fatal("confirmed new generation denied", err)
				}
				if _, err = s.RedeemHandoff(ctx, fresh.Code, fresh.SessionID, fresh.MailboxID, handoffState); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPostgreSQLMailboxHandoffIsolation(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := newHandoffFixture(t, db, ctx)
	f.activate(t, ctx, f.box.ID)
	additional := f.additional(t, ctx)
	f.activate(t, ctx, additional.ID)
	first, err := s.IssueHandoff(ctx, f.secret, f.box.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.IssueHandoff(ctx, f.secret, additional.ID, handoffState)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.WithholdMailbox(ctx, f.box.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RedeemHandoff(ctx, first.Code, first.SessionID, first.MailboxID, handoffState); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("withheld mailbox accepted old ticket", err)
	}
	grant, err := s.RedeemHandoff(ctx, second.Code, second.SessionID, second.MailboxID, handoffState)
	if err != nil || grant.ProtocolLogin != additional.ProtocolLogin || grant.RoundcubeKey != additional.RoundcubeKey {
		t.Fatal("unaffected mailbox lost its binding", err)
	}
	var addressStillActive bool
	if db.QueryRowContext(ctx, `SELECT state='active' FROM mail.address WHERE address=$1`, f.box.DefaultAddress).Scan(&addressStillActive) != nil || !addressStillActive {
		t.Fatal("withholding changed receive address state")
	}
	// No claim of real DMS reception is made here; this checks local projection.
}

func TestHandoffDatabaseFailureDoesNotGrant(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://invalid@127.0.0.1:1/invalid?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s := Store{DB: db}
	id := "11111111-2222-3333-4444-555555555555"
	if _, err := s.IssueHandoff(t.Context(), "ses_"+strings.Repeat("a", 64), id, handoffState); err == nil {
		t.Fatal("database failure granted code")
	}
	if _, err := s.RedeemHandoff(t.Context(), "handoff_"+strings.Repeat("a", 64), id, id, handoffState); err == nil {
		t.Fatal("database failure granted redemption")
	}
	if _, err := s.CheckMailboxGrant(t.Context(), MailboxGrant{SessionID: id, MailboxID: id, OwnerAnchor: id, Issuer: "https://iam.example.test", AuthorizationEpoch: 1}); err == nil {
		t.Fatal("database failure accepted identifiers")
	}
}
