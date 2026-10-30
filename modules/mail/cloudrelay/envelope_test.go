// TEST_CASES: MAILMOD-T-046
package cloudrelay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func TestEnvelopeReservationTransferAndReplay(t *testing.T) {
	ledger, dir := ledgerFixture(t)
	primary, alias := routeFixture("alice@example.test"), routeFixture("alias@example.test")
	if _, err := ledger.Replace(0, routeOp1, []ingress.Route{primary, alias}); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	original, err := ledger.ReserveEnvelope(routeOp2, "sender@example.net", digest, 50, []string{primary.Address, alias.Address})
	if err != nil || len(original.Recipients) != 2 || original.Recipients[0].Route != alias || original.Recipients[0].Sequence != 1 || original.Recipients[1].Sequence != 2 {
		t.Fatalf("reservation: %+v %v", original, err)
	}
	transferred := alias
	transferred.OwnerAnchor = routeOp2
	transferred.MailboxID = routeOp3
	transferred.ClaimVersion = routeOp1
	state, err := ledger.Replace(1, routeOp2, []ingress.Route{primary, transferred})
	if err != nil || state.Sequence != 2 || state.Bindings[1].ClosedSequence == nil || *state.Bindings[1].ClosedSequence != 2 || state.Bindings[2].FirstSequence != 3 {
		t.Fatalf("transfer: %+v %v", state, err)
	}
	reopened, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := reopened.ReserveEnvelope(routeOp2, "sender@example.net", digest, 50, []string{alias.Address, primary.Address})
	if err != nil || !reflect.DeepEqual(retry, original) {
		t.Fatalf("replay: %+v %v", retry, err)
	}
	next, err := reopened.ReserveEnvelope(routeOp3, "", digest, 50, []string{alias.Address})
	if err != nil || next.Recipients[0].Route != transferred || next.Recipients[0].Sequence != 3 {
		t.Fatalf("new owner: %+v %v", next, err)
	}
	for _, tc := range []struct {
		from, hash string
		size       int64
		addresses  []string
	}{
		{"different@example.net", digest, 50, []string{primary.Address, alias.Address}},
		{"sender@example.net", strings.Repeat("b", 64), 50, []string{primary.Address, alias.Address}},
		{"sender@example.net", digest, 51, []string{primary.Address, alias.Address}},
		{"sender@example.net", digest, 50, []string{primary.Address}},
	} {
		if _, err = reopened.ReserveEnvelope(routeOp2, tc.from, tc.hash, tc.size, tc.addresses); !errors.Is(err, ErrEnvelopeConflict) {
			t.Fatalf("changed replay: %v", err)
		}
	}
	if _, err = reopened.ReserveEnvelope(routeOp1, "", digest, 50, []string{primary.Address, "missing@example.test"}); !errors.Is(err, ErrEnvelopeRecipient) {
		t.Fatalf("unknown recipient: %v", err)
	}
	if _, err = reopened.ReserveEnvelope(routeOp1, "invalid\r\nheader", digest, 50, []string{primary.Address}); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("unsafe sender: %v", err)
	}
	if _, err = reopened.ReserveEnvelope(routeOp1, "", digest, 50, []string{primary.Address, primary.Address}); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("duplicate recipient: %v", err)
	}
	final, err := reopened.Read()
	if err != nil || final.Sequence != 3 {
		t.Fatalf("rejections changed sequence: %+v %v", final, err)
	}
}

func TestEnvelopeReservationConcurrentTransfer(t *testing.T) {
	for n := 0; n < 30; n++ {
		ledger, _ := ledgerFixture(t)
		old := routeFixture("alias@example.test")
		if _, err := ledger.Replace(0, routeOp1, []ingress.Route{old}); err != nil {
			t.Fatal(err)
		}
		next := old
		next.OwnerAnchor = routeOp2
		next.MailboxID = routeOp3
		next.ClaimVersion = routeOp1
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		var reservation EnvelopeReservation
		var firstErr, secondErr error
		go func() {
			defer wg.Done()
			<-start
			for {
				reservation, firstErr = ledger.ReserveEnvelope(routeOp3, "", strings.Repeat("a", 64), 10, []string{old.Address})
				if !errors.Is(firstErr, ErrRouteBusy) {
					return
				}
				runtime.Gosched()
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for {
				_, secondErr = ledger.Replace(1, routeOp2, []ingress.Route{next})
				if !errors.Is(secondErr, ErrRouteBusy) {
					return
				}
				runtime.Gosched()
			}
		}()
		close(start)
		wg.Wait()
		if firstErr != nil || secondErr != nil {
			t.Fatalf("reserve/replace: %v %v", firstErr, secondErr)
		}
		state, err := ledger.Read()
		if err != nil {
			t.Fatal(err)
		}
		if len(reservation.Recipients) != 1 || len(state.Bindings) != 2 {
			t.Fatal("unexpected binding")
		}
		captured := reservation.Recipients[0]
		if captured.Route == old {
			if *state.Bindings[0].ClosedSequence != captured.Sequence || state.Bindings[1].FirstSequence != captured.Sequence+1 {
				t.Fatal("old capture crosses transfer")
			}
		} else if captured.Route == next {
			if captured.Sequence != state.Bindings[1].FirstSequence || *state.Bindings[0].ClosedSequence != captured.Sequence-1 {
				t.Fatal("new capture crosses transfer")
			}
		} else {
			t.Fatal("unexpected owner")
		}
	}
}
func TestEnvelopeReservationCorruptStateFailsClosed(t *testing.T) {
	ledger, dir := ledgerFixture(t)
	if _, err := ledger.Replace(0, routeOp1, []ingress.Route{routeFixture("alice@example.test")}); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.ReserveEnvelope(routeOp2, "", strings.Repeat("a", 64), 10, []string{"alice@example.test"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "routes.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk routeDisk
	if err = json.Unmarshal(raw, &disk); err != nil {
		t.Fatal(err)
	}
	disk.Reservations[0].Recipients[0].Sequence = 9223372036854775807
	raw, err = json.Marshal(disk)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Read(); !errors.Is(err, ErrRouteState) {
		t.Fatalf("invalid reservation: %v", err)
	}
}

func TestEnvelopeReservationCapacityDoesNotBreakReplay(t *testing.T) {
	ledger, _ := ledgerFixture(t)
	address := "alice@example.test"
	if _, err := ledger.Replace(0, routeOp1, []ingress.Route{routeFixture(address)}); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("99999999-9999-4999-8999-%012d", i)
		if _, err := ledger.ReserveEnvelope(id, "", digest, 10, []string{address}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ledger.ReserveEnvelope(routeOp3, "", digest, 10, []string{address}); !errors.Is(err, ErrEnvelopeCapacity) {
		t.Fatalf("new reservation while full: %v", err)
	}
	old, err := ledger.ReserveEnvelope("99999999-9999-4999-8999-000000000000", "", digest, 10, []string{address})
	if err != nil || old.Recipients[0].Sequence != 1 {
		t.Fatalf("replay while full: %+v %v", old, err)
	}
	state, err := ledger.Read()
	if err != nil || state.Sequence != 64 {
		t.Fatalf("capacity changed state: %+v %v", state, err)
	}
}
