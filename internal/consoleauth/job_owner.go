package consoleauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CheckJobOwner re-resolves a previously authenticated job's immutable actor.
// It is NOT a login API: knowing an actor id grants no HTTP/CLI access. Local
// owner jobs survive logout/password rotation while the local owner still
// exists. Proxy authority is bounded by the locally recorded identity/session
// expiry and the current installation's issuer/group; no IdP query is implied.
// This read never renews sessions or writes credentials, and is safe to call
// from a job precommit observer (it must not reenter the job store).
func (store *Store) CheckJobOwner(ctx context.Context, actor, issuer, group string) error {
	if store == nil || ctx == nil || ctx.Err() != nil || store.currentState == nil {
		return ErrSessionUnauthorized
	}
	state, err := store.currentState(ctx)
	if err != nil || state != StateFull {
		return ErrSessionUnauthorized
	}
	unlock, err := store.lock(ctx)
	if err != nil {
		return ErrSessionUnauthorized
	}
	defer unlock()
	if actor == "local-owner" {
		local, err := store.loadLocalState()
		if err != nil || local.OwnerPasswordPHC == "" {
			return ErrSessionUnauthorized
		}
		return ctx.Err()
	}
	if issuer == "" || group == "" || !strings.HasPrefix(actor, "oidc:") || validateDigest(strings.TrimPrefix(actor, "oidc:")) != nil {
		return ErrSessionUnauthorized
	}
	proxy, err := store.loadProxyState()
	if err != nil {
		return ErrSessionUnauthorized
	}
	now := store.currentTime()
	for _, record := range proxy.Sessions {
		if record.Issuer != issuer || record.DirectoryGroup != group || record.SemanticRole != "platform_admin" ||
			!now.Before(record.ExpiresAt) || !now.Before(record.IdleExpiresAt) {
			continue
		}
		digest := sha256.Sum256([]byte(record.Issuer + "\x00" + record.Subject))
		if actor == "oidc:"+hex.EncodeToString(digest[:]) {
			return ctx.Err()
		}
	}
	return ErrSessionUnauthorized
}
