package store

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginFlowRejectsMalformedBrowserBinding(t *testing.T) {
	for _, pair := range [][2]string{{"", ""}, {"arbitrary-state", "flow_" + string(bytes.Repeat([]byte("0"), 64))}, {string(bytes.Repeat([]byte("a"), 43)), "raw-password"}} {
		if _, err := (Store{}).ConsumeLogin(t.Context(), pair[0], pair[1]); !errors.Is(err, ErrLoginFlowDenied) {
			t.Fatal("malformed binding reached database")
		}
	}
}

func TestPostgreSQLLoginFlows(t *testing.T) {
	db, ctx := testDatabase(t)
	s := Store{DB: db}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f, browser, err := s.BeginLogin(ctx)
	if err != nil || !loginStatePattern.MatchString(f.State) || !loginBrowserPattern.MatchString(browser) || f.Expires.Before(time.Now()) {
		t.Fatal("login flow failed", err)
	}
	var stateHash, browserHash []byte
	h := sha256.Sum256([]byte(f.State))
	wantBrowser := sha256.Sum256([]byte(browser))
	if err := db.QueryRowContext(ctx, `SELECT state_hash,browser_hash FROM mail.login_flow WHERE state_hash=$1`, h[:]).Scan(&stateHash, &browserHash); err != nil || !bytes.Equal(stateHash, h[:]) || !bytes.Equal(browserHash, wantBrowser[:]) {
		t.Fatal("raw state/browser cookie persisted", err)
	}
	wrong := "flow_" + string(bytes.Repeat([]byte("0"), 64))
	if _, err := s.ConsumeLogin(ctx, f.State, wrong); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("wrong browser consumed flow")
	}
	// A new Store represents another instance/process, with no in-memory flow.
	consumed, err := (Store{DB: db}).ConsumeLogin(ctx, f.State, browser)
	if err != nil || consumed.State != f.State || consumed.Nonce != f.Nonce || consumed.Verifier != f.Verifier {
		t.Fatal("persistent flow binding changed", err)
	}
	if _, err := s.ConsumeLogin(ctx, f.State, browser); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("callback replay succeeded")
	}
	f, browser, err = s.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := (Store{DB: db}).ConsumeLogin(ctx, f.State, browser)
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrLoginFlowDenied) {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if wins.Load() != 1 {
		t.Fatal("concurrent callbacks did not have exactly one winner", wins.Load())
	}
	f, browser, err = s.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h = sha256.Sum256([]byte(f.State))
	if _, err := db.ExecContext(ctx, `UPDATE mail.login_flow SET created_at=clock_timestamp()-interval '10 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE state_hash=$1`, h[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeLogin(ctx, f.State, browser); !errors.Is(err, ErrLoginFlowDenied) {
		t.Fatal("expired flow accepted")
	}
	if _, _, err := s.BeginLogin(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mail.login_flow WHERE state_hash=$1`, h[:]).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired flow not reclaimed", err)
	}
}
