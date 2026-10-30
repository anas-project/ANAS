package store

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

func TestPostgreSQLCompanyDirectoryAtomicSnapshot(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS fixture_reject_company ON mail.company_directory; DROP FUNCTION IF EXISTS mail.fixture_reject_company(); DELETE FROM mail.company_directory WHERE source='company'`)
	})
	person := identity.CompanyContact{ID: "12345678-1234-4234-8234-123456789abc", Name: "Alice", Email: "alice@example.test", Groups: []string{}, DirectGroups: []string{}}
	snapshot := identity.CompanySnapshot{Schema: identity.CompanySchema, People: []identity.CompanyContact{person}, Groups: []identity.CompanyGroup{}}
	fingerprint, err := s.SaveCompanyDirectory(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	view, err := (Store{DB: db}).CompanyDirectory(ctx)
	if err != nil || view.Fingerprint != fingerprint || len(view.Data.People) != 1 {
		t.Fatal("snapshot lost across process", err)
	}
	if _, err = db.ExecContext(ctx, `CREATE FUNCTION mail.fixture_reject_company() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'controlled company write failure'; END;$$; CREATE TRIGGER fixture_reject_company BEFORE UPDATE ON mail.company_directory FOR EACH ROW EXECUTE FUNCTION mail.fixture_reject_company()`); err != nil {
		t.Fatal(err)
	}
	snapshot.People[0].Name = "Changed"
	if _, err = s.SaveCompanyDirectory(ctx, snapshot); err == nil {
		t.Fatal("failed write reported success")
	}
	if view, err = s.CompanyDirectory(ctx); err != nil || view.Fingerprint != fingerprint || view.Data.People[0].Name != "Alice" {
		t.Fatal("failed refresh cleared previous directory", err)
	}
	if _, err = db.ExecContext(ctx, `DROP TRIGGER fixture_reject_company ON mail.company_directory; DROP FUNCTION mail.fixture_reject_company(); UPDATE mail.company_directory SET snapshot=snapshot||'{"proxyAddresses":"private"}'::jsonb WHERE source='company'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompanyDirectory(ctx); !errors.Is(err, ErrCompanyDirectoryUnavailable) {
		t.Fatal("unknown private field silently accepted", err)
	}
	if _, err = s.SaveCompanyDirectory(ctx, identity.CompanySnapshot{Schema: identity.CompanySchema, People: []identity.CompanyContact{}, Groups: []identity.CompanyGroup{}}); err != nil {
		t.Fatal("authoritative empty snapshot rejected", err)
	}
}
