package store

import (
	"errors"
	"os"
	"sync"
	"testing"
)

func TestPostgreSQLDurableRevocationTasks(t *testing.T) {
	file := os.Getenv("MAIL_REVOCATION_TEST_CONFIG")
	if file == "" {
		t.Skip("dedicated revocation fixture required; no real-host pass")
	}
	t.Setenv("MAIL_STORE_TEST_CONFIG", file)
	t.Run("transaction rollback cannot leave orphan withdrawal", func(t *testing.T) {
		s, ctx, g, secret := credentialFixture(t)
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE mail.web_session SET revoked_at=clock_timestamp() WHERE id=$1`, g.SessionID); err != nil {
			t.Fatal(err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Session(ctx, secret); err != nil {
			t.Fatal("rollback revoked session", err)
		}
		if _, err = s.ClaimRevocation(ctx); !errors.Is(err, ErrTaskChanged) {
			t.Fatal("rolled back withdrawal persisted", err)
		}
	})
	t.Run("session withdrawal durable lease retry and stale completion", func(t *testing.T) {
		s, ctx, g, secret := credentialFixture(t)
		if err := s.RevokeSession(ctx, secret); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		out := make(chan RevocationTask, 8)
		failures := make(chan error, 8)
		for range 8 {
			wg.Go(func() { task, err := s.ClaimRevocation(ctx); out <- task; failures <- err })
		}
		wg.Wait()
		close(out)
		close(failures)
		winners := 0
		for err := range failures {
			if err == nil {
				winners++
			} else if !errors.Is(err, ErrTaskChanged) {
				t.Fatal(err)
			}
		}
		var task RevocationTask
		for value := range out {
			if value.ID != 0 {
				task = value
			}
		}
		if winners != 1 || task.SessionID != g.SessionID || task.MailboxID != "" || task.Attempts != 1 {
			t.Fatal("wrong task scope/concurrent claims", winners, task.ID)
		}
		if err := s.FinishRevocation(ctx, task, false, "dms-unavailable"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ClaimRevocation(ctx); !errors.Is(err, ErrTaskChanged) {
			t.Fatal("failure did not back off", err)
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE mail.revocation_task SET next_attempt=clock_timestamp() WHERE id=$1`, task.ID); err != nil {
			t.Fatal(err)
		}
		retry, err := (Store{DB: s.DB}).ClaimRevocation(ctx)
		if err != nil || retry.Lease == task.Lease || retry.Attempts != 2 {
			t.Fatal("restart retry did not acquire a fresh lease", err)
		}
		if err = s.FinishRevocation(ctx, task, true, ""); !errors.Is(err, ErrTaskChanged) {
			t.Fatal("old worker completed a newer lease", err)
		}
		if err = s.FinishRevocation(ctx, retry, true, ""); err != nil {
			t.Fatal(err)
		}
		if err = s.RevokeSession(ctx, secret); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ClaimRevocation(ctx); !errors.Is(err, ErrTaskChanged) {
			t.Fatal("idempotent completed logout requeued", err)
		}
	})
	t.Run("mailbox withdrawal keeps other box and rejects old generation", func(t *testing.T) {
		s, ctx, g, secret := credentialFixture(t)
		if _, err := s.IssueCredentials(ctx, g); err != nil {
			t.Fatal(err)
		}
		r := projectionResource(t, "additional")
		r.OwnerAnchor = g.OwnerAnchor
		p, err := s.StageMailbox(ctx, r, "")
		if err != nil {
			t.Fatal(err)
		}
		p, err = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		h, err := s.IssueHandoff(ctx, secret, r.ID, handoffState)
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.RedeemHandoff(ctx, h.Code, h.SessionID, r.ID, handoffState)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := s.IssueCredentials(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.WithholdMailbox(ctx, g.MailboxID); err != nil {
			t.Fatal(err)
		}
		task, err := s.ClaimRevocation(ctx)
		if err != nil || task.MailboxID != g.MailboxID || task.SessionID != g.SessionID {
			t.Fatal("box withdrawal lost scope", err)
		}
		current, err := s.MailboxProjection(ctx, g.MailboxID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StageMailbox(ctx, current.Resource, current.Revision); err != nil {
			t.Fatal(err)
		}
		if err = s.FinishRevocation(ctx, task, true, ""); !errors.Is(err, ErrTaskChanged) {
			t.Fatal("old task completed later box withdrawal", err)
		}
		newer, err := s.ClaimRevocation(ctx)
		if err != nil || newer.ID != task.ID || newer.Revision <= task.Revision {
			t.Fatal("newer task not recoverable", err)
		}
		if err = s.FinishRevocation(ctx, newer, true, ""); err != nil {
			t.Fatal(err)
		}
		if _, err = s.IntrospectCredential(ctx, credential.AccessToken, "imap"); err != nil {
			t.Fatal("other mailbox revoked", err)
		}
	})
	t.Run("expired session and crashed worker recover", func(t *testing.T) {
		s, ctx, g, _ := credentialFixture(t)
		if _, err := s.DB.ExecContext(ctx, `UPDATE mail.web_session SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, g.SessionID); err != nil {
			t.Fatal(err)
		}
		if err := s.ExpireSessions(ctx); err != nil {
			t.Fatal(err)
		}
		task, err := s.ClaimRevocation(ctx)
		if err != nil || task.SessionID != g.SessionID || task.MailboxID != "" {
			t.Fatal("expiry lost withdrawal", err)
		}
		if _, err = s.DB.ExecContext(ctx, `UPDATE mail.revocation_task SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
			t.Fatal(err)
		}
		retry, err := s.ClaimRevocation(ctx)
		if err != nil || retry.Lease == task.Lease {
			t.Fatal("crashed worker lease not recoverable", err)
		}
		if err = s.FinishRevocation(ctx, task, true, ""); !errors.Is(err, ErrTaskChanged) {
			t.Fatal("crashed worker cleared recovered task", err)
		}
		if err = s.FinishRevocation(ctx, retry, true, ""); err != nil {
			t.Fatal(err)
		}
	})
}
