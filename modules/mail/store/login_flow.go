package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

var (
	ErrLoginFlowDenied  = errors.New("mail login flow denied")
	loginStatePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	loginBrowserPattern = regexp.MustCompile(`^flow_[a-f0-9]{64}$`)
)

// BeginLogin persists the state/nonce/PKCE binding for a future HTTP caller.
// Its browser secret is returned once and must become a Secure, HttpOnly,
// SameSite=Lax cookie. This method neither redirects nor authenticates anyone.
func (s Store) BeginLogin(ctx context.Context) (identity.Flow, string, error) {
	return s.beginFlow(ctx, "login")
}
func (s Store) BeginLogout(ctx context.Context) (identity.Flow, string, error) {
	return s.beginFlow(ctx, "logout")
}
func (s Store) beginFlow(ctx context.Context, purpose string) (identity.Flow, string, error) {
	f, err := identity.NewFlow()
	if err != nil {
		return identity.Flow{}, "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return identity.Flow{}, "", err
	}
	browser := "flow_" + hex.EncodeToString(raw)
	stateHash := sha256.Sum256([]byte(f.State))
	browserHash := sha256.Sum256([]byte(browser))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return identity.Flow{}, "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM mail.login_flow WHERE expires_at<=clock_timestamp()`); err != nil {
		return identity.Flow{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.login_flow(state_hash,browser_hash,nonce,verifier,expires_at,purpose) VALUES($1,$2,$3,$4,$5,$6)`, stateHash[:], browserHash[:], f.Nonce, f.Verifier, f.Expires, purpose); err != nil {
		return identity.Flow{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return identity.Flow{}, "", err
	}
	return f, browser, nil
}

// ConsumeLogin removes a valid flow before code exchange. A wrong browser
// cannot consume it. Concurrent or repeated callbacks have at most one winner,
// including across process restarts; there is no in-memory authorization cache.
func (s Store) ConsumeLogin(ctx context.Context, state, browser string) (identity.Flow, error) {
	return s.consumeFlow(ctx, state, browser, "login")
}
func (s Store) ConsumeLogout(ctx context.Context, state, browser string) (identity.Flow, error) {
	return s.consumeFlow(ctx, state, browser, "logout")
}
func (s Store) consumeFlow(ctx context.Context, state, browser, purpose string) (identity.Flow, error) {
	if !loginStatePattern.MatchString(state) || !loginBrowserPattern.MatchString(browser) {
		return identity.Flow{}, ErrLoginFlowDenied
	}
	stateHash := sha256.Sum256([]byte(state))
	browserHash := sha256.Sum256([]byte(browser))
	f := identity.Flow{State: state}
	err := s.DB.QueryRowContext(ctx, `DELETE FROM mail.login_flow
		WHERE state_hash=$1 AND browser_hash=$2 AND purpose=$3 AND expires_at>clock_timestamp()
		RETURNING nonce,verifier,expires_at`, stateHash[:], browserHash[:], purpose).Scan(&f.Nonce, &f.Verifier, &f.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Flow{}, ErrLoginFlowDenied
	}
	if err != nil {
		return identity.Flow{}, err
	}
	return f, nil
}
