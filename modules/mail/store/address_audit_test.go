// TEST_CASES: MAILMOD-T-014
package store

import (
	"os"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
)

func TestPostgreSQLAddressAuditUpgradeFromV25(t *testing.T) {
	if os.Getenv("MAIL_ADDRESS_AUDIT_UPGRADE") != "1" {
		t.Skip("requires a fresh isolated v25 database")
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
		t.Fatal("review upgrade fixture for new schema")
	}
	for i, migration := range migrations[:25] {
		if _, err = tx.ExecContext(ctx, migration); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO mail.schema_migration VALUES($1,$2)`, i+1, migrationChecksum(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s := Store{DB: db}
	owner := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	before, err := s.EnsurePrimary(ctx, owner, "upgrade@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("v25 upgrade", err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("repeat upgrade", err)
	}
	after, err := s.EnsurePrimary(ctx, owner, "upgrade@example.test")
	if err != nil || before != after {
		t.Fatal("v25 mailbox identity changed", err)
	}
	var version, attempts int
	if err = db.QueryRowContext(ctx, `SELECT max(version) FROM mail.schema_migration`).Scan(&version); err != nil || version != 26 {
		t.Fatal("v26 absent", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM mail.address_audit`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("migration fabricated audit history", err)
	}
	r := directorymail.MailRequest{ID: "44444444-4444-4444-8444-444444444444", Version: "11111111-1111-4111-8111-111111111111", Action: "additional.create", Address: "upgrade-extra@example.test"}
	id, err := s.BeginAddressAudit(ctx, owner, owner, r)
	if err != nil {
		t.Fatal("upgraded audit insert", err)
	}
	if err = s.FinishAddressAudit(ctx, id, "saved"); err != nil {
		t.Fatal("upgraded audit completion", err)
	}
}

func TestPostgreSQLAddressAuditRetainsUnknownAndCompletesOnce(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r := directorymail.MailRequest{ID: "44444444-4444-4444-8444-444444444444", Version: "11111111-1111-4111-8111-111111111111", Action: "additional.create", Address: "test@example.test"}
	id, err := s.BeginAddressAudit(ctx, r.ID, r.Version, r)
	if err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DELETE FROM mail.address_audit WHERE id=$1`, id)
	var outcome string
	if err = db.QueryRowContext(ctx, `SELECT outcome FROM mail.address_audit WHERE id=$1 AND finished_at IS NULL`, id).Scan(&outcome); err != nil || outcome != "unknown" {
		t.Fatal("interruption cannot be represented", err)
	}
	if err = s.FinishAddressAudit(ctx, id, "saved"); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishAddressAudit(ctx, id, "unknown"); err == nil {
		t.Fatal("completed attempt overwritten")
	}
}
