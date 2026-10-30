package main

import (
	"context"
	"errors"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type revocationStore interface {
	ExpireSessions(context.Context) error
	ClaimRevocation(context.Context) (store.RevocationTask, error)
	FinishRevocation(context.Context, store.RevocationTask, bool, string) error
}
type sessionKicker interface {
	Kick(context.Context, string, string) error
}

// This worker has no directory dependency. A queued authoritative withdrawal
// continues while new directory reads fail or the configuration loop retries.
func revokeOne(ctx context.Context, data revocationStore, dms sessionKicker) (bool, error) {
	task, err := data.ClaimRevocation(ctx)
	if errors.Is(err, store.ErrTaskChanged) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = dms.Kick(ctx, task.SessionID, task.MailboxID); err != nil {
		if persist := data.FinishRevocation(ctx, task, false, "dms-unavailable"); persist != nil {
			return true, persist
		}
		return true, nil
	}
	return true, data.FinishRevocation(ctx, task, true, "")
}
func revocationLoop(ctx context.Context, data revocationStore, dms sessionKicker) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := data.ExpireSessions(work)
		if err == nil {
			for range 10 {
				found, err := revokeOne(work, data, dms)
				if err != nil || !found {
					break
				}
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
