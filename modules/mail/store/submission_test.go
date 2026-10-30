package store

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// This test owns a fresh isolated database; it cannot run alongside fixtures
// that already installed the current schema.
func TestPostgreSQLSubmissionUpgradeFromV24(t *testing.T) {
	if os.Getenv("MAIL_SUBMISSION_UPGRADE") != "1" {
		t.Skip("requires a fresh isolated v24 database")
	}
	db, ctx := testDatabase(t)
	var existing bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='mail')`).Scan(&existing); err != nil || existing {
		t.Fatal("fresh isolated database required", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA mail; CREATE TABLE mail.schema_migration(version integer PRIMARY KEY, checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 26 {
		t.Fatal("update the explicit upgrade fixture for a new schema")
	}
	for i, m := range migrations[:24] {
		if _, err = tx.ExecContext(ctx, m); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail.schema_migration(version,checksum) VALUES($1,$2)`, i+1, migrationChecksum(m)); err != nil {
			t.Fatal(err)
		}
	}
	const old = `
INSERT INTO mail.person(anchor) VALUES('11111111-1111-4111-8111-111111111111');
INSERT INTO mail.mailbox(id,owner_anchor,kind,protocol_login,storage_ref,roundcube_key) VALUES('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','primary','mbx_'||repeat('a',32),'store_'||repeat('a',32),'webmail_'||repeat('a',32));
INSERT INTO mail.web_session(id,token_hash,owner_anchor,iam_issuer,iam_sid,authenticated_at,expires_at) VALUES('33333333-3333-4333-8333-333333333333',decode(repeat('a1',32),'hex'),'11111111-1111-4111-8111-111111111111','https://iam.example.test','test',clock_timestamp()-interval '1 minute',clock_timestamp()+interval '1 hour');
INSERT INTO mail.protocol_credential(id,family_id,session_id,mailbox_id,authorization_epoch,access_hash,smtp_hash,refresh_hash,smtp_login,expires_at,refresh_expires_at) VALUES('44444444-4444-4444-8444-444444444444','55555555-5555-4555-8555-555555555555','33333333-3333-4333-8333-333333333333','22222222-2222-4222-8222-222222222222',1,decode(repeat('a2',32),'hex'),decode(repeat('a3',32),'hex'),decode(repeat('a4',32),'hex'),'sub_'||repeat('a',32),clock_timestamp()+interval '1 minute',clock_timestamp()+interval '1 hour');
INSERT INTO mail.smtp_submission(queue_id,credential_id,mailbox_id,session_id,envelope,visible_from) VALUES('ABC123','44444444-4444-4444-8444-444444444444','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333','old@example.test','old@example.test');`
	if _, err = tx.ExecContext(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = (Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("v24 upgrade", err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM mail.smtp_submission WHERE queue_id='ABC123' AND request_id IS NULL AND accepted_at>clock_timestamp()-interval '60 seconds'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("legacy rate record lost or assigned a fabricated ID", count, err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO mail.smtp_submission(request_id,queue_id,credential_id,mailbox_id,session_id,envelope,visible_from) SELECT repeat('a',32),queue_id,credential_id,mailbox_id,session_id,envelope,visible_from FROM mail.smtp_submission WHERE request_id IS NULL`); err != nil {
		t.Fatal("new scan could not reuse historical queue", err)
	}
	if err = (Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("repeat migration", err)
	}
}

func TestPostgreSQLSubmissionRateAndRetryAreAtomic(t *testing.T) {
	s, ctx, g, _ := credentialFixture(t)
	c, err := s.IssueCredentials(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.SenderGrant(ctx, c.SMTPUsername, g.DefaultAddress, g.DefaultAddress)
	if err != nil {
		t.Fatal(err)
	}
	for _, unavailable := range []string{"NOQUEUE", "undef", "unknown"} {
		if err = s.AuthorizeSubmission(ctx, i, strings.Repeat("a", 32), unavailable, g.DefaultAddress, g.DefaultAddress, 4); !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("missing queue identity accepted", err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for n := range 12 {
		wg.Go(func() {
			results <- s.AuthorizeSubmission(ctx, i, fmt.Sprintf("%032x", n+1), fmt.Sprintf("Q%s%02d", g.MailboxID[:8], n), g.DefaultAddress, g.DefaultAddress, 4)
		})
	}
	wg.Wait()
	close(results)
	allowed, limited := 0, 0
	for err := range results {
		if err == nil {
			allowed++
		} else if errors.Is(err, ErrSendLimit) {
			limited++
		} else {
			t.Fatal(err)
		}
	}
	if allowed != 4 || limited != 8 {
		t.Fatal("concurrent rate bypass", allowed, limited)
	}
	var queue, requestID string
	if err = s.DB.QueryRowContext(ctx, `SELECT queue_id,request_id FROM mail.smtp_submission WHERE mailbox_id=$1 LIMIT 1`, g.MailboxID).Scan(&queue, &requestID); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = s.AuthorizeSubmission(ctx, i, requestID, queue, g.DefaultAddress, g.DefaultAddress, 4); err != nil {
			t.Fatal("identical retry consumed quota", err)
		}
	}
	if err = s.AuthorizeSubmission(ctx, i, requestID, queue, c.Addresses[0], c.Addresses[1], 4); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("queue replay changed sender", err)
	}
	if err = s.AuthorizeSubmission(ctx, i, strings.Repeat("d", 32), queue, g.DefaultAddress, g.DefaultAddress, 4); !errors.Is(err, ErrSendLimit) {
		t.Fatal("reused queue ID bypassed rate limit", err)
	}
	if err = s.AuthorizeSubmission(ctx, i, requestID, "OTHER123", g.DefaultAddress, g.DefaultAddress, 4); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("same request changed queue", err)
	}
	if err = s.AuthorizeSubmission(ctx, i, "", queue, g.DefaultAddress, g.DefaultAddress, 4); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("missing request ID accepted", err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE mail.smtp_submission SET accepted_at=clock_timestamp()-interval '61 seconds' WHERE mailbox_id=$1`, g.MailboxID); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeSubmission(ctx, i, strings.Repeat("b", 32), "N"+g.MailboxID[:8], g.DefaultAddress, g.DefaultAddress, 4); err != nil {
		t.Fatal("rolling window did not recover", err)
	}
	next, err := s.RefreshCredentials(ctx, c.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	i, err = s.SenderGrant(ctx, next.SMTPUsername, g.DefaultAddress, g.DefaultAddress)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeSubmission(ctx, i, strings.Repeat("c", 32), queue, g.DefaultAddress, g.DefaultAddress, 4); err != nil {
		t.Fatal("later SMTP credential could not reuse queue ID", err)
	}
	var rows int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.smtp_submission WHERE mailbox_id=$1 AND queue_id=$2`, g.MailboxID, queue).Scan(&rows); err != nil || rows != 2 {
		t.Fatal("later submission confused with earlier retry", rows, err)
	}
	if err = s.WithholdMailbox(ctx, g.MailboxID); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeSubmission(ctx, i, requestID, queue, g.DefaultAddress, g.DefaultAddress, 4); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("withdrawn queued sender authorized", err)
	}
}
