// TEST_CASES: MAILMOD-T-047
package cloudrelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func originalFixture(t *testing.T, capacity int64) (*RouteLedger, *OriginalStore, string) {
	t.Helper()
	ledger, dir := ledgerFixture(t)
	if _, err := ledger.Replace(0, routeOp1, []ingress.Route{routeFixture("alice@example.test"), routeFixture("alias@example.test")}); err != nil {
		t.Fatal(err)
	}
	store, err := InitializeOriginalStore(ledger, capacity, 7)
	if err != nil {
		t.Fatal(err)
	}
	return ledger, store, dir
}
func TestOriginalStoreTransferAndReplay(t *testing.T) {
	ledger, store, dir := originalFixture(t, routeMax+1000)
	original := []byte("From: sender@example.net\r\nTo: forged@foreign.test\r\n\r\nbody\r\n")
	first, err := store.Store(routeOp2, "sender@example.net", []string{"alice@example.test", "alias@example.test"}, original)
	if err != nil || len(first.Recipients) != 2 {
		t.Fatalf("store: %+v %v", first, err)
	}
	alias := routeFixture("alias@example.test")
	alias.OwnerAnchor = routeOp2
	alias.MailboxID = routeOp3
	alias.ClaimVersion = routeOp1
	if _, err = ledger.Replace(1, routeOp2, []ingress.Route{routeFixture("alice@example.test"), alias}); err != nil {
		t.Fatal(err)
	}
	reopenedLedger, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOriginalStore(reopenedLedger, routeMax+1000, 7)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Store(routeOp2, "sender@example.net", []string{"alias@example.test", "alice@example.test"}, original)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("replay: %+v %v", again, err)
	}
	got, body, err := reopened.Read(routeOp2)
	if err != nil || !reflect.DeepEqual(first, got) || !bytes.Equal(original, body) {
		t.Fatalf("read: %+v %v", got, err)
	}
	next, err := reopened.Store(routeOp3, "", []string{"alias@example.test"}, []byte("new message"))
	if err != nil || next.Recipients[0].Route != alias || next.Recipients[0].Sequence <= first.Recipients[1].Sequence {
		t.Fatalf("new owner: %+v %v", next, err)
	}
	if _, err = reopened.Store(routeOp2, "sender@example.net", []string{"alias@example.test", "alice@example.test"}, []byte("different")); !errors.Is(err, ErrEnvelopeConflict) {
		t.Fatalf("changed replay: %v", err)
	}
	if _, err = OpenOriginalStore(reopenedLedger, routeMax+999, 7); !errors.Is(err, ErrOriginalState) {
		t.Fatalf("changed capacity: %v", err)
	}
}
func TestOriginalStoreInterruptedRecovery(t *testing.T) {
	ledger, store, dir := originalFixture(t, routeMax+100)
	rawDir := filepath.Join(dir, "originals")
	body := []byte("durable raw bytes")
	// Simulate interruption after the durable pending reservation and raw
	// publication but before the ready transition.
	digest := sha256.Sum256(body)
	pendingReservation, err := ledger.ReserveEnvelope(routeOp2, "", hex.EncodeToString(digest[:]), int64(len(body)), []string{"alice@example.test"})
	if err != nil || pendingReservation.State != "pending" {
		t.Fatalf("pending: %+v %v", pendingReservation, err)
	}
	if _, _, err = store.Read(routeOp2); !errors.Is(err, ErrOriginalPending) {
		t.Fatalf("pending became readable: %v", err)
	}
	orphan := filepath.Join(rawDir, "raw-"+routeOp2)
	if err := os.WriteFile(orphan, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Store(routeOp2, "", []string{"alice@example.test"}, []byte("different")); !errors.Is(err, ErrEnvelopeConflict) {
		t.Fatalf("orphan conflict: %v", err)
	}
	if state, err := ledger.Read(); err != nil || state.Sequence != 1 {
		t.Fatalf("orphan conflict changed route state: %+v %v", state, err)
	}
	old := routeFixture("alice@example.test")
	newRoute := old
	newRoute.OwnerAnchor, newRoute.MailboxID, newRoute.ClaimVersion = routeOp2, routeOp3, routeOp1
	if _, err = ledger.Replace(1, routeOp2, []ingress.Route{newRoute, routeFixture("alias@example.test")}); err != nil {
		t.Fatal(err)
	}
	reservation, err := store.Store(routeOp2, "", []string{"alice@example.test"}, body)
	if err != nil || reservation.State != "ready" || reservation.Recipients[0].Sequence != 1 || reservation.Recipients[0].Route != old {
		t.Fatalf("recover: %+v %v", reservation, err)
	}
	// An incomplete staging file was never committed and is safe to replace
	// only for its own uncommitted transaction.
	completed := []byte("completed")
	completedDigest := sha256.Sum256(completed)
	if _, err = ledger.ReserveEnvelope(routeOp3, "", hex.EncodeToString(completedDigest[:]), int64(len(completed)), []string{"alias@example.test"}); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(rawDir, ".pending-"+routeOp3)
	if err = os.WriteFile(pending, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Store(routeOp3, "", []string{"alias@example.test"}, completed); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(pending); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", err)
	}
	if err = os.WriteFile(orphan, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Read(routeOp2); !errors.Is(err, ErrOriginalState) {
		t.Fatalf("corrupt read: %v", err)
	}
	if _, err = store.Store(routeOp2, "", []string{"alice@example.test"}, body); !errors.Is(err, ErrOriginalState) {
		t.Fatalf("corrupt replay: %v", err)
	}
}

func TestOriginalStorePendingBeforeRawPreservesBindingAndQuota(t *testing.T) {
	ledger, store, dir := originalFixture(t, routeMax+10)
	body := []byte("123456")
	digest := sha256.Sum256(body)
	pending, err := ledger.ReserveEnvelope(routeOp2, "", hex.EncodeToString(digest[:]), int64(len(body)), []string{"alias@example.test"})
	if err != nil || pending.State != "pending" {
		t.Fatalf("reserve: %+v %v", pending, err)
	}
	if _, err = store.Store(routeOp3, "", []string{"alice@example.test"}, []byte("12345")); !errors.Is(err, ErrOriginalCapacity) {
		t.Fatalf("pending quota not reserved: %v", err)
	}
	old := routeFixture("alias@example.test")
	newRoute := old
	newRoute.OwnerAnchor, newRoute.MailboxID, newRoute.ClaimVersion = routeOp2, routeOp3, routeOp1
	if _, err = ledger.Replace(1, routeOp2, []ingress.Route{routeFixture("alice@example.test"), newRoute}); err != nil {
		t.Fatal(err)
	}
	reopenedLedger, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOriginalStore(reopenedLedger, routeMax+10, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = reopened.Read(routeOp2); !errors.Is(err, ErrOriginalPending) {
		t.Fatalf("pending readable: %v", err)
	}
	ready, err := reopened.Store(routeOp2, "", []string{"alias@example.test"}, body)
	if err != nil || ready.State != "ready" || ready.Recipients[0].Route != old || ready.Recipients[0].Sequence != pending.Recipients[0].Sequence {
		t.Fatalf("recovery changed ownership: %+v %v", ready, err)
	}
	_, got, err := reopened.Read(routeOp2)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("ready original: %v", err)
	}
}

func TestOriginalStoreConcurrentTransfer(t *testing.T) {
	for i := 0; i < 20; i++ {
		ledger, store, _ := originalFixture(t, routeMax+100)
		old := routeFixture("alias@example.test")
		newRoute := old
		newRoute.OwnerAnchor, newRoute.MailboxID, newRoute.ClaimVersion = routeOp2, routeOp3, routeOp1
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var reservation EnvelopeReservation
		var storeErr, changeErr error
		go func() {
			defer wg.Done()
			<-start
			for {
				reservation, storeErr = store.Store(routeOp3, "", []string{old.Address}, []byte("original"))
				if !errors.Is(storeErr, ErrRouteBusy) {
					return
				}
				runtime.Gosched()
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for {
				_, changeErr = ledger.Replace(1, routeOp2, []ingress.Route{routeFixture("alice@example.test"), newRoute})
				if !errors.Is(changeErr, ErrRouteBusy) {
					return
				}
				runtime.Gosched()
			}
		}()
		close(start)
		wg.Wait()
		if storeErr != nil || changeErr != nil {
			t.Fatalf("store/transfer: %v %v", storeErr, changeErr)
		}
		state, err := ledger.Read()
		if err != nil {
			t.Fatal(err)
		}
		captured := reservation.Recipients[0]
		if captured.Route == old {
			if *state.Bindings[1].ClosedSequence != captured.Sequence || state.Bindings[2].FirstSequence != captured.Sequence+1 {
				t.Fatal("old owner crosses boundary")
			}
		} else if captured.Route == newRoute {
			if *state.Bindings[1].ClosedSequence != captured.Sequence-1 || state.Bindings[2].FirstSequence != captured.Sequence {
				t.Fatal("new owner crosses boundary")
			}
		} else {
			t.Fatal("unknown owner")
		}
		read, original, err := store.Read(routeOp3)
		if err != nil || !reflect.DeepEqual(read, reservation) || string(original) != "original" {
			t.Fatalf("stored original: %v", err)
		}
	}
}
func TestOriginalStoreQuotaAndUnsafeFiles(t *testing.T) {
	t.Run("capacity", func(t *testing.T) {
		ledger, store, dir := originalFixture(t, routeMax+10)
		if err := os.WriteFile(filepath.Join(dir, "originals", "raw-"+routeOp2), []byte("orphan"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "originals", ".pending-"+routeOp1), []byte("xx"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Store(routeOp3, "", []string{"alice@example.test"}, []byte("123")); !errors.Is(err, ErrOriginalCapacity) {
			t.Fatalf("orphan or other staging not counted: %v", err)
		}
		if state, err := ledger.Read(); err != nil || state.Sequence != 0 {
			t.Fatalf("quota changed state: %+v %v", state, err)
		}
		if _, err := store.Store(routeOp2, "", []string{"alice@example.test"}, []byte("orphan")); !errors.Is(err, ErrOriginalState) {
			t.Fatalf("unbound orphan accepted: %v", err)
		}
	})
	for _, mode := range []string{"unknown file", "unsafe mode", "symlink", "unknown recipient", "invalid sender", "missing committed original"} {
		t.Run(mode, func(t *testing.T) {
			ledger, store, dir := originalFixture(t, routeMax+100)
			body := []byte("message")
			switch mode {
			case "unknown file":
				if err := os.WriteFile(filepath.Join(dir, "originals", "unexpected"), body, 0600); err != nil {
					t.Fatal(err)
				}
			case "unsafe mode":
				path := filepath.Join(dir, "originals", "raw-"+routeOp2)
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("config.json", filepath.Join(dir, "originals", "raw-"+routeOp2)); err != nil {
					t.Fatal(err)
				}
			case "missing committed original":
				digest := sha256.Sum256(body)
				if _, err := ledger.ReserveEnvelope(routeOp3, "", hex.EncodeToString(digest[:]), int64(len(body)), []string{"alice@example.test"}); err != nil {
					t.Fatal(err)
				}
				_, err := ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
					disk.Reservations[0].State = "ready"
					return disk, ledger.write(dir, disk)
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			address, from := "alice@example.test", ""
			if mode == "unknown recipient" {
				address = "no@example.test"
			}
			if mode == "invalid sender" {
				from = "bad\r\nheader"
			}
			id := routeOp3
			if mode == "unsafe mode" || mode == "symlink" {
				id = routeOp2
			}
			_, err := store.Store(id, from, []string{address}, body)
			if err == nil {
				t.Fatal("unexpected success")
			}
			if mode == "missing committed original" && !errors.Is(err, ErrOriginalState) {
				t.Fatalf("missing mismatch: %v", err)
			}
			if mode != "missing committed original" {
				state, e := ledger.Read()
				if e != nil || state.Sequence != 0 {
					t.Fatalf("rejection changed ledger: %+v %v", state, e)
				}
			}
		})
	}
}
