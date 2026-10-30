package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestPostgreSQLNotificationDurableDeduplicationLeaseAndExpiry(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	source, _ := mailboxID()
	generation, _ := mailboxID()
	id, _ := mailboxID()
	box, _ := mailboxID()
	t.Cleanup(func() {
		if _, e := db.ExecContext(context.Background(), `DELETE FROM mail.ingress_notice WHERE source_id=$1`, source); e != nil {
			t.Error(e)
		}
	})
	n := IngressNotice{SourceID: source, Generation: generation, ID: id, MessageID: "source-1", MailboxID: box, BodySHA256: strings.Repeat("a", 64)}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.RecordIngressNotice(ctx, n) }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal("notice repeat", e)
		}
	}
	bad := n
	bad.MailboxID = generation
	if e := s.RecordIngressNotice(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("notice target drift", e)
	}
	bad = n
	bad.BodySHA256 = strings.Repeat("b", 64)
	if e := s.RecordIngressNotice(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("notice body drift", e)
	}
	task, e := (Store{DB: db}).ClaimIngressNotice(ctx, source)
	if e != nil || task.ID != n.ID {
		t.Fatal("durable hint lost", e)
	}
	if _, e = s.ClaimIngressNotice(ctx, source); !errors.Is(e, ErrIngressIdle) {
		t.Fatal("notice concurrently leased", e)
	}
	old := task
	if _, e = db.ExecContext(ctx, `UPDATE mail.ingress_notice SET lease_until=clock_timestamp()-interval '1 second' WHERE source_id=$1`, source); e != nil {
		t.Fatal(e)
	}
	task, e = s.ClaimIngressNotice(ctx, source)
	if e != nil || task.Lease == old.Lease {
		t.Fatal("notice lease did not recover", e)
	}
	if e = s.FinishIngressNotice(ctx, old, "accepted", ""); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("old notice worker completed", e)
	}
	if e = s.FinishIngressNotice(ctx, task, "pending", "source-unavailable"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, `UPDATE mail.ingress_notice SET next_attempt=clock_timestamp() WHERE source_id=$1`, source); e != nil {
		t.Fatal(e)
	}
	task, e = s.ClaimIngressNotice(ctx, source)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishIngressNotice(ctx, task, "accepted", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordIngressNotice(ctx, n); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimIngressNotice(ctx, source); !errors.Is(e, ErrIngressIdle) {
		t.Fatal("duplicate reopened accepted hint", e)
	}
	n.ID, _ = mailboxID()
	n.MessageID = "retention-expired"
	if e = s.RecordIngressNotice(ctx, n); e != nil {
		t.Fatal(e)
	}
	task, e = s.ClaimIngressNotice(ctx, source)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishIngressNotice(ctx, task, "expired", "source-copy-expired"); e != nil {
		t.Fatal(e)
	}
	var expired bool
	if e = db.QueryRowContext(ctx, `SELECT state='expired' AND last_error='source-copy-expired' AND completed_at IS NOT NULL FROM mail.ingress_notice WHERE source_id=$1 AND notification_id=$2`, source, n.ID).Scan(&expired); e != nil || !expired {
		t.Fatal("retention gap hidden", e)
	}
}
