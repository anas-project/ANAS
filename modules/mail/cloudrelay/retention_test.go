// TEST_CASES: MAILMOD-T-050
package cloudrelay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

func ackReservation(t *testing.T, s *OriginalStore, r EnvelopeReservation, index int) ingress.SourceMessage {
	t.Helper()
	m := s.entry(r, r.Recipients[index]).message
	var input sourceAck
	input.SourceID = routeSource
	input.Generation = routeGeneration
	input.Receipt.ID = m.Metadata.Key()
	input.Receipt.MailboxID = m.Metadata.MailboxID
	input.Receipt.SHA256 = m.Metadata.SHA256
	input.Receipt.State = "stored"
	if _, err := s.sourceAck(m.Metadata.MessageID, m.Metadata.MailboxID, input); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestRetentionWaitsForAllTargetsAndRetainsReceipt(t *testing.T) {
	ledger, s, dir := originalFixture(t, routeMax+10000)
	reservation, err := s.Store(routeOp2, "", []string{"alice@example.test", "alias@example.test"}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	first := ackReservation(t, s, reservation, 0)
	if removed, err := s.Maintain(time.Now().Add(8 * 24 * time.Hour)); err != nil || removed {
		t.Fatalf("partially delivered removed: %v %v", removed, err)
	}
	ackReservation(t, s, reservation, 1)
	if removed, err := s.Maintain(time.Now().Add(6 * 24 * time.Hour)); err != nil || removed {
		t.Fatalf("early retention: %v %v", removed, err)
	}
	if removed, err := s.Maintain(time.Now().Add(8 * 24 * time.Hour)); err != nil || !removed {
		t.Fatalf("retention: %v %v", removed, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "originals", "raw-"+routeOp2)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw remains: %v", err)
	}
	if _, err = s.Store(routeOp2, "", []string{"alice@example.test", "alias@example.test"}, []byte("original")); !errors.Is(err, ErrEnvelopeExpired) {
		t.Fatalf("retired transaction reused: %v", err)
	}
	if _, err = ledger.ReserveEnvelope(routeOp2, "", first.Metadata.SHA256, first.Bytes, []string{"alice@example.test", "alias@example.test"}); !errors.Is(err, ErrEnvelopeExpired) {
		t.Fatalf("retired reservation reused: %v", err)
	}
	client, _, _ := sourceHTTPFixture(t, s)
	receipt := dms.IntakeReceipt{ID: first.Metadata.Key(), MailboxID: first.Metadata.MailboxID, SHA256: first.Metadata.SHA256, State: "stored"}
	if err = client.Acknowledge(context.Background(), first, receipt); err != nil {
		t.Fatalf("lost acknowledgement replay: %v", err)
	}
	if _, err = client.Message(context.Background(), first.Metadata.MessageID, first.Metadata.MailboxID); !errors.Is(err, ingress.ErrGone) {
		t.Fatalf("retired content visible: %v", err)
	}
	page, err := client.List(context.Background(), "")
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("retired listed: %+v %v", page, err)
	}
	if err := os.Remove(filepath.Join(dir, "originals", "retired-"+routeOp2)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store(routeOp2, "", []string{"alice@example.test", "alias@example.test"}, []byte("original")); !errors.Is(err, ErrEnvelopeExpired) {
		t.Fatalf("missing tombstone resurrected transaction: %v", err)
	}
	if err := client.Acknowledge(context.Background(), first, receipt); !errors.Is(err, ingress.ErrUnavailable) {
		t.Fatalf("missing proof accepted: %v", err)
	}
}
func TestRetentionPressureOldestDeliveredOnly(t *testing.T) {
	_, s, dir := originalFixture(t, routeMax+6000)
	first, err := s.Store(routeOp1, "", []string{"alice@example.test"}, []byte(strings.Repeat("a", 2200)))
	if err != nil {
		t.Fatal(err)
	}
	ackReservation(t, s, first, 0)
	second, err := s.Store(routeOp2, "", []string{"alice@example.test"}, []byte(strings.Repeat("b", 1600)))
	if err != nil {
		t.Fatal(err)
	}
	ackReservation(t, s, second, 0)
	third, err := s.Store(routeOp3, "", []string{"alias@example.test"}, []byte(strings.Repeat("c", 1000)))
	if err != nil {
		t.Fatal(err)
	}
	newID := "99999999-9999-4999-8999-999999999999"
	if _, err = s.Store(newID, "", []string{"alias@example.test"}, []byte(strings.Repeat("d", 1800))); err != nil {
		t.Fatalf("pressure cleanup: %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "originals", "raw-"+first.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest not removed: %v", err)
	}
	for _, id := range []string{second.ID, third.ID, newID} {
		if _, err = os.Stat(filepath.Join(dir, "originals", "raw-"+id)); err != nil {
			t.Fatalf("other original lost: %v", err)
		}
	}
}
func TestRetentionProtectsUnconfirmedAndCountsEveryScan(t *testing.T) {
	_, s, dir := originalFixture(t, routeMax+1000)
	if _, err := s.Store(routeOp2, "", []string{"alice@example.test"}, []byte(strings.Repeat("a", 800))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store(routeOp3, "", []string{"alias@example.test"}, []byte(strings.Repeat("b", 300))); !errors.Is(err, ErrOriginalCapacity) {
		t.Fatalf("unconfirmed capacity: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "originals", "raw-"+routeOp2)); err != nil {
		t.Fatalf("unconfirmed deleted: %v", err)
	}
	err := s.sourceLocked(func(_ int, raw int, disk routeDisk) (routeDisk, error) {
		a, e := originalUsage(raw, disk)
		if e != nil {
			return routeDisk{}, e
		}
		b, e := originalUsage(raw, disk)
		if e != nil {
			return routeDisk{}, e
		}
		if a != b || a < 800 {
			t.Fatalf("repeated scan: %d %d", a, b)
		}
		return disk, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestRetentionRecoversAfterUnlink(t *testing.T) {
	ledger, s, dir := originalFixture(t, routeMax+10000)
	r, err := s.Store(routeOp2, "", []string{"alice@example.test"}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	m := ackReservation(t, s, r, 0)
	// Simulate death after the synced tombstone and raw unlink, before the
	// root removes the purging record and records the retired identity.
	err = s.sourceLocked(func(root, raw int, disk routeDisk) (routeDisk, error) {
		disk.Reservations[0].State = "purging"
		if e := ledger.write(root, disk); e != nil {
			return routeDisk{}, e
		}
		if e := s.writeRetired(raw, disk, disk.Reservations[0]); e != nil {
			return routeDisk{}, e
		}
		if e := unix.Unlinkat(raw, "raw-"+r.ID, 0); e != nil {
			return routeDisk{}, e
		}
		if e := unix.Fsync(raw); e != nil {
			return routeDisk{}, e
		}
		return disk, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reopenedLedger, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOriginalStore(reopenedLedger, routeMax+10000, 7)
	if err != nil {
		t.Fatal(err)
	}
	client, _, _ := sourceHTTPFixture(t, reopened)
	receipt := dms.IntakeReceipt{ID: m.Metadata.Key(), MailboxID: m.Metadata.MailboxID, SHA256: m.Metadata.SHA256, State: "stored"}
	if err = client.Acknowledge(context.Background(), m, receipt); err != nil {
		t.Fatalf("pending cleanup receipt: %v", err)
	}
	if removed, err := reopened.Maintain(time.Now()); err != nil || !removed {
		t.Fatalf("recover purge: %v %v", removed, err)
	}
	if err = client.Acknowledge(context.Background(), m, receipt); err != nil {
		t.Fatalf("retired receipt: %v", err)
	}
	if _, err = reopened.Store(routeOp2, "", []string{"alice@example.test"}, []byte("original")); !errors.Is(err, ErrEnvelopeExpired) {
		t.Fatalf("retired reaccepted: %v", err)
	}
}

func TestRetentionCorruptProofDoesNotDeleteOriginal(t *testing.T) {
	_, s, dir := originalFixture(t, routeMax+10000)
	r, err := s.Store(routeOp2, "", []string{"alice@example.test"}, []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	ackReservation(t, s, r, 0)
	if err = os.WriteFile(filepath.Join(dir, "originals", "retired-"+r.ID), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.Maintain(time.Now().Add(8 * 24 * time.Hour)); removed || !errors.Is(err, ErrOriginalState) {
		t.Fatalf("corrupt proof: %v %v", removed, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "originals", "raw-"+r.ID))
	if err != nil || string(data) != "original" {
		t.Fatalf("original lost: %v", err)
	}
}

func TestRetentionSchedulerStops(t *testing.T) {
	_, s, _ := originalFixture(t, routeMax+10000)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.RunRetention(ctx, nil) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("retention scheduler did not stop")
	}
}
