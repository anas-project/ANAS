// TEST_CASES: MAILMOD-T-012
package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgreSQLIngressReservationUpgradeFromNonemptyV20(t *testing.T) {
	if os.Getenv("MAIL_INGRESS_UPGRADE") != "1" {
		t.Skip("requires independently seeded v20 source database; no upgrade pass")
	}
	db, ctx := testDatabase(t)
	var version int
	if err := db.QueryRowContext(ctx, `SELECT max(version) FROM mail.schema_migration`).Scan(&version); err != nil || version != 20 {
		t.Fatal("actual v20 fixture required", version, err)
	}
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	id, generation := "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	var capacity int64
	var revision, sequence int
	var unbound bool
	if err := db.QueryRowContext(ctx, `SELECT reserved_capacity_bytes,source_identity_fingerprint IS NULL,revision,sequence FROM mail.ingress_route_state WHERE source_id=$1`, id).Scan(&capacity, &unbound, &revision, &sequence); err != nil || capacity != 0 || !unbound || revision != 3 || sequence != 7 {
		t.Fatal("v21 erased source history or fabricated an existing reservation", err)
	}
	r := IngressReservation{SourceID: id, Generation: generation, IdentityFingerprint: strings.Repeat("a", 64), CapacityBytes: 1 << 30}
	if err := s.ReserveIngressSources(ctx, 2<<30, []IngressReservation{r}); err != nil {
		t.Fatal("explicit legacy binding failed", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT reserved_capacity_bytes,revision,sequence FROM mail.ingress_route_state WHERE source_id=$1`, id).Scan(&capacity, &revision, &sequence); err != nil || capacity != 1<<30 || revision != 3 || sequence != 7 {
		t.Fatal("binding changed known cloud history", err)
	}
	// Other tests use this same isolated lifecycle database after this phase.
	if _, err := db.ExecContext(ctx, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLIngressReservationsSurviveConfigOmissionAndConcurrentStartup(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	newReservation := func() IngressReservation {
		id, err := mailboxID()
		if err != nil {
			t.Fatal(err)
		}
		generation, err := mailboxID()
		if err != nil {
			t.Fatal(err)
		}
		return IngressReservation{SourceID: id, Generation: generation, IdentityFingerprint: strings.Repeat("a", 64), CapacityBytes: 1 << 30}
	}
	a, b, c := newReservation(), newReservation(), newReservation()
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, r := range []IngressReservation{a, b, c} {
			if _, err := db.ExecContext(clean, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, r.SourceID); err != nil {
				t.Error(err)
			}
			if _, err := db.ExecContext(clean, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`, r.SourceID); err != nil {
				t.Error(err)
			}
		}
	})
	if err := s.ReserveIngressSources(ctx, 2<<30, []IngressReservation{a}); err != nil {
		t.Fatal("initial reservation", err)
	}
	for _, mode := range []string{"omit", "empty", "shrink", "identity", "generation", "budget"} {
		t.Run(mode, func(t *testing.T) {
			after := a
			wanted, budget := []IngressReservation{a, b}, int64(2<<30)
			switch mode {
			case "omit":
				wanted = []IngressReservation{b}
			case "empty":
				wanted, budget = nil, 0
			case "shrink":
				after.CapacityBytes /= 2
				wanted = []IngressReservation{b, after}
			case "identity":
				after.IdentityFingerprint = strings.Repeat("b", 64)
				wanted = []IngressReservation{b, after}
			case "generation":
				after.Generation = b.Generation
				wanted = []IngressReservation{b, after}
			case "budget":
				budget = 1 << 30
			}
			if err := (Store{DB: db}).ReserveIngressSources(ctx, budget, wanted); !errors.Is(err, ErrIngressReservation) {
				t.Fatal("config silently forgot or replaced an existing reservation", err)
			}
			var rows int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_route_state WHERE source_id=$1`, b.SourceID).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("failed startup left a partially registered source", err)
			}
		})
	}
	// Two processes may not independently spend the same remaining budget or
	// remove the other process's newly registered source from their snapshot.
	var wait sync.WaitGroup
	results := make(chan struct {
		r   IngressReservation
		err error
	}, 2)
	for _, candidate := range []IngressReservation{b, c} {
		wait.Add(1)
		go func(r IngressReservation) {
			defer wait.Done()
			err := (Store{DB: db}).ReserveIngressSources(ctx, 2<<30, []IngressReservation{a, r})
			results <- struct {
				r   IngressReservation
				err error
			}{r, err}
		}(candidate)
	}
	wait.Wait()
	close(results)
	wins := 0
	var winner IngressReservation
	for result := range results {
		if result.err == nil {
			wins++
			winner = result.r
		} else if !errors.Is(result.err, ErrIngressReservation) {
			t.Fatal(result.err)
		}
	}
	if wins != 1 {
		t.Fatal("concurrent startup did not serialize source ownership", wins)
	}
	if err := (Store{DB: db}).ReserveIngressSources(ctx, 2<<30, []IngressReservation{winner, a}); err != nil {
		t.Fatal("exact reservation did not survive reconstruction", err)
	}
	var count, revision int
	var capacity int64
	if err := db.QueryRowContext(ctx, `SELECT count(*),sum(reserved_capacity_bytes),sum(revision) FROM mail.ingress_route_state WHERE source_id IN($1,$2,$3)`, a.SourceID, b.SourceID, c.SourceID).Scan(&count, &capacity, &revision); err != nil || count != 2 || capacity != 2<<30 || revision != 0 {
		t.Fatal("reservation altered routing or exceeded total capacity", count, capacity, revision, err)
	}
	legacy := b
	if winner.SourceID == b.SourceID {
		legacy = c
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO mail.ingress_poll(source_id,generation) VALUES($1,$2)`, legacy.SourceID, legacy.Generation); err != nil {
		t.Fatal(err)
	}
	if err := (Store{DB: db}).ReserveIngressSources(ctx, 2<<30, []IngressReservation{a, winner}); !errors.Is(err, ErrIngressReservation) {
		t.Fatal("a legacy poll without route-state disappeared from reservation checks", err)
	}
}
