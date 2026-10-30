package dms

import (
	"context"
	"errors"
	"testing"
)

func TestUnconfirmedReadRetainsCASRevisionWithoutClaimingReady(t *testing.T) {
	a, in := fixture(t)
	a.probe = func(context.Context, map[string]entry) error { return ErrUnavailable }
	if _, err := a.Apply(t.Context(), in); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	pending, err := a.Read(t.Context(), in.Projection.Mailbox.ID)
	if !errors.Is(err, ErrUnavailable) || pending.Applied || pending.Revision != in.Projection.Revision || pending.Fingerprint != in.Projection.Fingerprint {
		t.Fatal("unconfirmed revision unavailable or reported ready", err)
	}
	next := nextRequest(t, in, "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	next.ExpectedRevision = pending.Revision
	a.probe = func(context.Context, map[string]entry) error { return nil }
	if applied, err := a.Apply(t.Context(), next); err != nil || !applied.Applied || applied.Revision != next.Projection.Revision {
		t.Fatal("newer configuration cannot recover older interrupted apply", err)
	}
	if _, err := a.Apply(t.Context(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("old interrupted request restored stale configuration", err)
	}
}
