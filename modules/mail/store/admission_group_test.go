package store

import (
	"errors"
	"sync"
	"testing"
)

func TestPostgreSQLAdmissionGroupPins(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM mail.admission_group`); err != nil {
		t.Fatal(err)
	}
	a := "17171717-1717-4717-8717-171717171717"
	b := "18181818-1818-4818-8818-181818181818"
	if _, err := s.AdmissionGroup(ctx, "application"); !errors.Is(err, ErrAdmissionGroupUnknown) {
		t.Fatal("missing pin", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.PinAdmissionGroup(ctx, "application", a) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("concurrent pin", err)
		}
	}
	if err := s.PinAdmissionGroup(ctx, "application", b); !errors.Is(err, ErrAdmissionGroupConflict) {
		t.Fatal("role rebound", err)
	}
	if err := s.PinAdmissionGroup(ctx, "all", a); !errors.Is(err, ErrAdmissionGroupConflict) {
		t.Fatal("one group used for two roles", err)
	}
	if got, err := (Store{DB: db}).AdmissionGroup(ctx, "application"); err != nil || got != a {
		t.Fatal("restart lost immutable pin", err)
	}
	if err := s.PinAdmissionGroup(ctx, "all", b); err != nil {
		t.Fatal("second distinct role", err)
	}
	if _, err := s.AdmissionGroup(ctx, "username"); !errors.Is(err, ErrAdmissionGroupConflict) {
		t.Fatal("unknown role accepted", err)
	}
}
