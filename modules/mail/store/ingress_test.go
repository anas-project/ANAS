// TEST_CASES: MAILMOD-T-008
package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIngressObservationRejectsUntrustedNamesBeforeDatabase(t *testing.T) {
	o := IngressObservation{SourceID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", MessageID: "cloud-1", MailboxID: "33333333-3333-4333-8333-333333333333", OwnerAnchor: "44444444-4444-4444-8444-444444444444", AcceptedAddress: "alice@example.test", ClaimVersion: "55555555-5555-4555-8555-555555555555", Sequence: 1, Bytes: 100, ETag: "object-1", ReceivedAt: time.Now().UTC()}
	if !o.valid() {
		t.Fatal("fixture")
	}
	for _, mode := range []string{"path", "owner", "case", "large", "sequence", "sender", "digest"} {
		t.Run(mode, func(t *testing.T) {
			bad := o
			switch mode {
			case "path":
				bad.MessageID = "../mail"
			case "owner":
				bad.OwnerAnchor = "alice"
			case "case":
				bad.AcceptedAddress = "Alice@example.test"
			case "large":
				bad.Bytes = 25<<20 + 1
			case "sequence":
				bad.Sequence = 0
			case "sender":
				bad.EnvelopeFrom = "Name <sender@example.net>"
			case "digest":
				bad.ExpectedSHA256 = "wrong"
			}
			if _, e := (Store{}).StageIngress(t.Context(), bad); !errors.Is(e, ErrIngressChanged) {
				t.Fatal("untrusted observation accepted", e)
			}
		})
	}
}

func TestPostgreSQLIngressBindingLedgerLeaseAndRecovery(t *testing.T) {
	s, ctx, r := projectionStore(t)
	p, e := s.StageMailbox(ctx, r, "")
	if e != nil {
		t.Fatal(e)
	}
	source, _ := mailboxID()
	generation, _ := mailboxID()
	b := IngressBinding{SourceID: source, Generation: generation, Address: r.DefaultAddress, ClaimVersion: r.Addresses[0].Version, MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, FirstSequence: 10}
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, q := range []string{`DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`} {
			if _, e := s.DB.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
	})
	if e = s.ConfirmIngressBinding(ctx, b); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("unapplied projection opened source", e)
	}
	if _, e = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmIngressBinding(ctx, b); e != nil {
		t.Fatal("confirmed source", e)
	}
	if e = s.ConfirmIngressBinding(ctx, b); e != nil {
		t.Fatal("source repeat", e)
	}
	// Claim versions may be shared by several addresses in one AD operation.
	other := b
	other.Address = "other@example.test"
	if other.Key() == b.Key() {
		t.Fatal("claim version collapsed different addresses")
	}
	o := IngressObservation{SourceID: source, Generation: generation, MessageID: "cloud-1", MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, AcceptedAddress: r.DefaultAddress, ClaimVersion: b.ClaimVersion, EnvelopeFrom: "sender@example.net", Sequence: 10, Bytes: 100, ETag: "object-1", ReceivedAt: time.Now().UTC()}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.StageIngress(ctx, o); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal("concurrent repeat", e)
		}
	}
	var count int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1`, source).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate rows", e)
	}
	bad := o
	bad.EnvelopeFrom = "forged@example.net"
	if _, e = s.StageIngress(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("metadata drift", e)
	}
	bad = o
	bad.OwnerAnchor = generation
	if _, e = s.StageIngress(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("foreign owner", e)
	}
	if e = s.CloseIngressBinding(ctx, b, 12); e != nil {
		t.Fatal(e)
	}
	if e = s.CloseIngressBinding(ctx, b, 13); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("closure widened", e)
	}
	if e = s.ConfirmIngressBinding(ctx, b); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("closed permission reopened", e)
	}
	bad = o
	bad.MessageID = "past-fence"
	bad.Sequence = 13
	if _, e = s.StageIngress(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("post-removal message accepted", e)
	}
	// Mail accepted before closure remains the original box's work, including
	// for a disabled person. This grants no login or sender permission.
	if e = s.DenyPerson(ctx, r.OwnerAnchor); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StageIngress(ctx, o); e != nil {
		t.Fatal("pre-removal work lost", e)
	}
	wins := make(chan IngressTask, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); task, e := s.ClaimIngress(ctx, source); wins <- task; failures <- e }()
	}
	wg.Wait()
	close(wins)
	close(failures)
	claimed := 0
	for e := range failures {
		if e == nil {
			claimed++
		} else if !errors.Is(e, ErrIngressIdle) {
			t.Fatal(e)
		}
	}
	if claimed != 1 {
		t.Fatal("task has multiple workers", claimed)
	}
	var task IngressTask
	for v := range wins {
		if v.ID != "" {
			task = v
		}
	}
	if _, e = s.AdvanceIngress(ctx, task, "complete", strings.Repeat("a", 64), "queued"); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("queue or download completed", e)
	}
	digest := strings.Repeat("a", 64)
	task, e = s.AdvanceIngress(ctx, task, "native", digest, "")
	if e != nil {
		t.Fatal("download durability", e)
	}
	task, e = s.AdvanceIngress(ctx, task, "native", digest, "unknown")
	if e != nil {
		t.Fatal(e)
	}
	// A second process reading the same PG sees uncertainty and the same key.
	again, e := (Store{DB: s.DB}).IngressTask(ctx, o.Key())
	if e != nil || again.NativeState != "unknown" || again.SHA256 != digest {
		t.Fatal("restart lost uncertainty", e)
	}
	if _, e = s.AdvanceIngress(ctx, task, "ack", digest, "queued"); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("queue acknowledged", e)
	}
	old := task
	if _, e = s.DB.ExecContext(ctx, `UPDATE mail.ingress_message SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); e != nil {
		t.Fatal(e)
	}
	task, e = s.ClaimIngress(ctx, source)
	if e != nil || task.Lease == old.Lease {
		t.Fatal("expired lease recovery", e)
	}
	if _, e = s.AdvanceIngress(ctx, old, "ack", digest, "stored"); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("old worker advanced", e)
	}
	task, e = s.AdvanceIngress(ctx, task, "ack", digest, "stored")
	if e != nil {
		t.Fatal(e)
	}
	task, e = s.AdvanceIngress(ctx, task, "cleanup", digest, "stored")
	if e != nil {
		t.Fatal(e)
	}
	var ack bool
	if e = s.DB.QueryRowContext(ctx, `SELECT acknowledged_at IS NOT NULL AND completed_at IS NULL FROM mail.ingress_message WHERE id=$1`, task.ID).Scan(&ack); e != nil || !ack {
		t.Fatal("cleanup intent lost cloud acknowledgement", e)
	}
	task, e = s.AdvanceIngress(ctx, task, "complete", digest, "stored")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseIngress(ctx, task, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimIngress(ctx, source); !errors.Is(e, ErrIngressIdle) {
		t.Fatal("completed task leased", e)
	}
	if row, e := s.StageIngress(ctx, o); e != nil || row.Phase != "complete" {
		t.Fatal("retained cloud copy reopened delivery", e)
	}
	bad = o
	bad.ExpectedSHA256 = strings.Repeat("b", 64)
	if _, e = s.StageIngress(ctx, bad); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("new cloud acknowledgement changed hash", e)
	}
	o.ExpectedSHA256 = digest
	if _, e = s.StageIngress(ctx, o); e != nil {
		t.Fatal("matching final source hash rejected", e)
	}
}

func TestPostgreSQLIngressCursorHasExclusiveLeaseAndMonotonicRevision(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	source, _ := mailboxID()
	generation, _ := mailboxID()
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM mail.ingress_poll WHERE source_id=$1`, source)
	})
	p, e := s.ClaimIngressPoll(ctx, source, generation)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimIngressPoll(ctx, source, generation); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("parallel source traversal", e)
	}
	p, e = s.AdvanceIngressPoll(ctx, p, "next-page", 7)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdvanceIngressPoll(ctx, p, "old", 6); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("route revision regressed", e)
	}
	if e = s.ReleaseIngressPoll(ctx, p, "source-unavailable"); e != nil {
		t.Fatal(e)
	}
	p, e = (Store{DB: db}).ClaimIngressPoll(ctx, source, generation)
	if e != nil || p.Cursor != "next-page" || p.RouteRevision != 7 {
		t.Fatal("cursor not durable", e)
	}
	p, e = s.AdvanceIngressPoll(ctx, p, "", 7)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReleaseIngressPoll(ctx, p, ""); e != nil {
		t.Fatal(e)
	}
	foreign, _ := mailboxID()
	if _, e = s.ClaimIngressPoll(ctx, source, foreign); !errors.Is(e, ErrIngressChanged) {
		t.Fatal("source reset reused cursor", e)
	}
}
