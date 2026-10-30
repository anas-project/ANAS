package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func schemaChecksumForTest() string {
	sum := sha256.Sum256([]byte(schema))
	return hex.EncodeToString(sum[:])
}

func TestOpaqueIdentitiesAndPrimaryAddress(t *testing.T) {
	for _, prefix := range []string{"mbx_", "store_", "webmail_"} {
		a, e := token(prefix)
		if e != nil {
			t.Fatal(e)
		}
		b, e := token(prefix)
		if e != nil || a == b || !strings.HasPrefix(a, prefix) || len(a) != len(prefix)+32 {
			t.Fatal("invalid opaque identity")
		}
	}
	id, e := mailboxID()
	if e != nil || !anchorPattern.MatchString(id) {
		t.Fatal("invalid mailbox ID")
	}
	if v, e := primaryAddress("Alice@EXAMPLE.ORG"); e != nil || v != "alice@example.org" {
		t.Fatal("primary normalization")
	}
	for _, v := range []string{"Display <alice@example.org>", "alice@example.org@other.org", "alice..bad@example.org", " alice@example.org"} {
		if _, e := primaryAddress(v); e == nil {
			t.Fatalf("invalid primary accepted: %q", v)
		}
	}
}

func testDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	file := os.Getenv("MAIL_STORE_TEST_CONFIG")
	if file == "" {
		t.Skip("requires explicitly configured isolated PostgreSQL; not a real-host pass")
	}
	info, e := os.Stat(file)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private test configuration required")
	}
	b, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	var cfg struct {
		DSN string `json:"dsn"`
	}
	if json.Unmarshal(b, &cfg) != nil || cfg.DSN == "" {
		t.Fatal("invalid test configuration")
	}
	db, e := sql.Open("pgx", cfg.DSN)
	if e != nil {
		t.Fatal("database configuration rejected")
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	var databaseName string
	if db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&databaseName) != nil || (databaseName != "anas_mail_model_1025" && databaseName != "anas_mail_lifecycle_1025" && databaseName != "anas_mail_cold_1025") {
		t.Fatal("refusing non-fixture database")
	}
	return db, ctx
}

func TestPostgreSQLModel(t *testing.T) {
	db, ctx := testDatabase(t)
	var e error
	s := Store{DB: db}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.Migrate(ctx) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	t.Run("concurrent idempotent primary creation", func(t *testing.T) {
		const anchor = "11111111-2222-3333-4444-555555555555"
		results := make(chan Mailbox, 8)
		failures := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				m, e := s.EnsurePrimary(ctx, anchor, "Alice@example.org")
				results <- m
				failures <- e
			}()
		}
		wg.Wait()
		close(results)
		close(failures)
		for e := range failures {
			if e != nil {
				t.Fatal(e)
			}
		}
		var id string
		for m := range results {
			if id == "" {
				id = m.ID
			}
			if m.ID != id || m.State != "pending" || strings.Contains(m.ProtocolLogin, "11111111") || strings.Contains(m.StorageRef, "alice") || strings.Contains(m.RoundcubeKey, "alice") {
				t.Fatal("identity changed or leaked directory labels")
			}
		}
		var state string
		db.QueryRowContext(ctx, `SELECT directory_state FROM mail.person WHERE anchor=$1`, anchor).Scan(&state)
		if state != "unknown" {
			t.Fatal("resource creation granted directory authorization")
		}
		if _, e = s.EnsurePrimary(ctx, anchor, "renamed@example.org"); !errors.Is(e, ErrPrimaryAddressChanged) {
			t.Fatal("unimplemented primary rename was accepted")
		}
		if _, e = s.EnsurePrimary(ctx, "22222222-2222-3333-4444-555555555555", "alice@example.org"); !errors.Is(e, ErrAddressUnavailable) {
			t.Fatal("new anchor inherited occupied address")
		}
	})
	t.Run("different owners compete for one address", func(t *testing.T) {
		ids := []string{"33333333-2222-3333-4444-555555555555", "44444444-2222-3333-4444-555555555555"}
		out := make(chan error, 2)
		for _, anchor := range ids {
			wg.Add(1)
			go func(a string) { defer wg.Done(); _, e := s.EnsurePrimary(ctx, a, "claim@example.org"); out <- e }(anchor)
		}
		wg.Wait()
		close(out)
		ok, unavailable := 0, 0
		for e := range out {
			if e == nil {
				ok++
			} else if errors.Is(e, ErrAddressUnavailable) {
				unavailable++
			} else {
				t.Fatal(e)
			}
		}
		if ok != 1 || unavailable != 1 {
			t.Fatal("address has more than one owner")
		}
	})
	t.Run("reserved and retained addresses stay occupied", func(t *testing.T) {
		if _, e = db.ExecContext(ctx, `INSERT INTO mail.address(address,kind,state) VALUES('reserved@example.org','reserved','retained')`); e != nil {
			t.Fatal(e)
		}
		if _, e = s.EnsurePrimary(ctx, "55555555-2222-3333-4444-555555555555", "reserved@example.org"); !errors.Is(e, ErrAddressUnavailable) {
			t.Fatal("reserved address was reassigned")
		}
		if _, e = db.ExecContext(ctx, `UPDATE mail.address SET state='retained' WHERE address='alice@example.org'`); e != nil {
			t.Fatal(e)
		}
		if _, e = s.EnsurePrimary(ctx, "66666666-2222-3333-4444-555555555555", "Alice@example.org"); !errors.Is(e, ErrAddressUnavailable) {
			t.Fatal("retained address was reassigned")
		}
	})
	t.Run("schema drift prevents startup", func(t *testing.T) {
		if _, e = db.ExecContext(ctx, `UPDATE mail.schema_migration SET checksum='changed' WHERE version=1`); e != nil {
			t.Fatal(e)
		}
		if e = s.Migrate(ctx); !errors.Is(e, ErrSchemaMismatch) {
			t.Fatal("schema mismatch accepted")
		}
		sum := schemaChecksumForTest()
		if _, e = db.ExecContext(ctx, `UPDATE mail.schema_migration SET checksum=$1 WHERE version=1`, sum); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("unknown and incomplete migration history prevents startup", func(t *testing.T) {
		if _, e = db.ExecContext(ctx, `INSERT INTO mail.schema_migration(version,checksum) VALUES($1,'unknown')`, len(migrations)+1); e != nil {
			t.Fatal(e)
		}
		err := s.Migrate(ctx)
		if _, e = db.ExecContext(ctx, `DELETE FROM mail.schema_migration WHERE version=$1`, len(migrations)+1); e != nil {
			t.Fatal(e)
		}
		if !errors.Is(err, ErrSchemaMismatch) {
			t.Fatal("unknown later migration accepted")
		}
		if _, e = db.ExecContext(ctx, `DELETE FROM mail.schema_migration WHERE version=1`); e != nil {
			t.Fatal(e)
		}
		err = s.Migrate(ctx)
		if _, e = db.ExecContext(ctx, `INSERT INTO mail.schema_migration(version,checksum) VALUES(1,$1)`, schemaChecksumForTest()); e != nil {
			t.Fatal(e)
		}
		if !errors.Is(err, ErrSchemaMismatch) {
			t.Fatal("missing intermediate migration accepted")
		}
		if e = s.Migrate(ctx); e != nil {
			t.Fatal("restored fixture history rejected", e)
		}
	})
}
