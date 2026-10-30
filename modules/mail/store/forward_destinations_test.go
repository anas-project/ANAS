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

func TestPostgreSQLForwardDestinationUpgradeFromV22(t *testing.T) {
	if os.Getenv("MAIL_FORWARD_UPGRADE") != "1" {
		t.Skip("requires fresh isolated v22 database; not an upgrade pass")
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
	if _, err = tx.ExecContext(ctx, `CREATE SCHEMA mail; CREATE TABLE mail.schema_migration(version integer PRIMARY KEY,checksum text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 26 {
		t.Fatal("update the explicit upgrade fixture for a new schema")
	}
	for i, migration := range migrations[:22] {
		if _, err = tx.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail.schema_migration(version,checksum) VALUES($1,$2)`, i+1, migrationChecksum(migration)); err != nil {
			t.Fatal(err)
		}
	}
	source, generation := "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	fingerprint := strings.Repeat("a", 64)
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.ingress_route_state(source_id,generation,revision,fingerprint,sequence,reserved_capacity_bytes,source_identity_fingerprint) VALUES($1,$2,3,$3,7,1024,$4)`, source, generation, fingerprint, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = (Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("v22 upgrade", err)
	}
	var gotGeneration, gotFingerprint, identity string
	var revision, sequence, capacity, version int
	if err = db.QueryRowContext(ctx, `SELECT generation,revision,fingerprint,sequence,reserved_capacity_bytes,source_identity_fingerprint FROM mail.ingress_route_state WHERE source_id=$1`, source).Scan(&gotGeneration, &revision, &gotFingerprint, &sequence, &capacity, &identity); err != nil || gotGeneration != generation || revision != 3 || sequence != 7 || capacity != 1024 || gotFingerprint != fingerprint || identity != strings.Repeat("b", 64) {
		t.Fatal("upgrade changed source history", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT max(version) FROM mail.schema_migration`).Scan(&version); err != nil || version != 26 {
		t.Fatal("new schema absent", err)
	}
	if err = (Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("repeat upgrade", err)
	}
}

func TestPostgreSQLForwardDestinationIntentAndWithdrawal(t *testing.T) {
	s, ctx, resource, projection, source, generation := sourceRouteFixture(t)
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, q := range []string{`DELETE FROM mail.forward_destination WHERE source_id=$1`, `DELETE FROM mail.forward_source WHERE source_id=$1`} {
			if _, err := s.DB.ExecContext(clean, q, source); err != nil {
				t.Error(err)
			}
		}
	})
	route := routeFor(resource)
	account, zone, domain := strings.Repeat("1", 32), strings.Repeat("2", 32), "in.example.test"
	if _, err := s.PrepareForwardDestination(ctx, source, generation, account, zone, domain, route); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("unconfirmed source permitted destination", err)
	}
	routes := []IngressRoute{route}
	op, err := s.StageSourceRoutes(ctx, source, generation, routes, map[string]string{resource.ID: projection.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.IssueSourceRoutes(ctx, op); err != nil {
		t.Fatal(err)
	}
	b := bindingFor(op, route, 1)
	if err = s.ObserveSourceRoutes(ctx, routeOutcome(op, 3, []IngressRouteBinding{b})); err != nil {
		t.Fatal(err)
	}
	if err = s.EnsureForwardSource(ctx, source, generation, account, zone, domain); err != nil {
		t.Fatal("forward source not bound", err)
	}
	if err = s.EnsureForwardSource(ctx, source, generation, account, zone, "other.example.test"); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("forward source domain drift accepted", err)
	}
	v, err := s.PrepareForwardDestination(ctx, source, generation, account, zone, domain, route)
	if err != nil || v.State != "prepared" || v.DestinationID != "" || v.BindingID != b.Key() {
		t.Fatal("intent not prepared", err)
	}
	for _, candidate := range []struct{ account, zone, domain string }{{strings.Repeat("3", 32), zone, domain}, {account, strings.Repeat("3", 32), domain}, {account, zone, "other.example.test"}} {
		if _, err = s.PrepareForwardDestination(ctx, source, generation, candidate.account, candidate.zone, candidate.domain, route); !errors.Is(err, ErrIngressChanged) {
			t.Fatal("source identity drift accepted", err)
		}
	}
	forged := v
	forged.Target = "foreign@in.example.test"
	if err = s.IssueForwardDestination(ctx, forged); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("forged target issued", err)
	}
	providerID := strings.Repeat("4", 32)
	if err = s.ConfirmForwardDestination(ctx, v, providerID); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("unissued response accepted", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- (Store{DB: s.DB}).IssueForwardDestination(ctx, v) }()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrIngressChanged) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("concurrent issue did not select exactly one caller", winners)
	}
	restarted, err := (Store{DB: s.DB}).PrepareForwardDestination(ctx, source, generation, account, zone, domain, route)
	if err != nil || restarted.State != "issued" || restarted.DestinationID != "" {
		t.Fatal("unknown request lost after restart", err)
	}
	if err = s.IssueForwardDestination(ctx, v); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("issued request was reissued", err)
	}
	if err = s.CloseIngressBinding(ctx, b.IngressBinding, 3); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmForwardDestination(ctx, v, providerID); err != nil {
		t.Fatal("late response not recorded", err)
	}
	if err = s.ConfirmForwardDestination(ctx, v, providerID); err != nil {
		t.Fatal("identical response not idempotent", err)
	}
	if err = s.ConfirmForwardDestination(ctx, v, strings.Repeat("5", 32)); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("different provider ID replaced history", err)
	}
	if _, err = s.PrepareForwardDestination(ctx, source, generation, account, zone, domain, route); !errors.Is(err, ErrIngressChanged) {
		t.Fatal("closed claim reopened", err)
	}
	var state, recorded string
	if err = s.DB.QueryRowContext(ctx, `SELECT state,destination_id FROM mail.forward_destination WHERE binding_id=$1`, v.BindingID).Scan(&state, &recorded); err != nil || state != "created" || recorded != providerID {
		t.Fatal("late response history missing", err)
	}
}
