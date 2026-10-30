package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPostgreSQLDirectoryCursorCASAndRestart(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), `DELETE FROM mail.directory_cursor WHERE consumer='samba-mail-v1'`)
	})
	if seq, err := s.DirectoryCursor(ctx); err != nil || seq != 0 {
		t.Fatal("new cursor", seq, err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for seq := int64(1); seq <= 8; seq++ {
		wg.Go(func() {
			err := s.SaveDirectoryCursor(ctx, 0, seq)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrDirectoryCursorChanged) {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if wins.Load() != 1 {
		t.Fatal("concurrent cursor overwrite", wins.Load())
	}
	seq, err := (Store{DB: db}).DirectoryCursor(ctx)
	if err != nil || seq < 1 {
		t.Fatal("cursor lost across instance", err)
	}
	if err = s.SaveDirectoryCursor(ctx, 0, 100); !errors.Is(err, ErrDirectoryCursorChanged) {
		t.Fatal("stale consumer overwrote progress", err)
	}
	if err = s.SaveDirectoryCursor(ctx, seq, 0); err != nil {
		t.Fatal("explicit reset after reconciliation rejected", err)
	}
}
