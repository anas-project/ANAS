package store

import (
	"errors"
	"testing"
)

func TestPostgreSQLMailboxPreflightDerivesOwnerFromSession(t *testing.T) {
	s, ctx, g, secret := credentialFixture(t)
	current, err := s.MailboxForSession(ctx, secret, g.MailboxID)
	if err != nil || current != g {
		t.Fatal("authorized preflight", err)
	}
	_, _, other, otherSecret := credentialFixture(t)
	for _, pair := range [][2]string{{secret, other.MailboxID}, {otherSecret, g.MailboxID}, {"invalid", g.MailboxID}} {
		if _, err = s.MailboxForSession(ctx, pair[0], pair[1]); !errors.Is(err, ErrHandoffDenied) {
			t.Fatal("foreign mailbox preflight allowed", err)
		}
	}
	if err = s.WithholdMailbox(ctx, g.MailboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MailboxForSession(ctx, secret, g.MailboxID); !errors.Is(err, ErrHandoffDenied) {
		t.Fatal("pending mailbox preflight allowed", err)
	}
}
