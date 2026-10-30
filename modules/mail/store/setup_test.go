// TEST_CASES: MAILMOD-T-005 MAILMOD-T-017 MAILMOD-T-020 MAILMOD-T-038
package store

import (
	"errors"
	"sync"
	"testing"
)

func setupFixtureDraft() SetupDraft {
	return SetupDraft{Inbound: "worker-r2", Domain: "example.test", SourceURL: "https://mail-source.example.test", IngressSecretRef: "secrets.MAIL_INGRESS", LimitsConfirmed: true,
		Limits:   SetupLimits{AdditionalMailboxes: 2, Aliases: 10, MailboxBytes: 2 << 30, MessagesPerMinute: 30, CloudBytes: 2 << 30, DeliveredDays: 7},
		Outbound: SetupOutbound{Provider: "resend", Host: "smtp.resend.com", Port: 587, TLS: "starttls", Username: "resend", SecretRef: "secrets.MAIL_OUTBOUND_PASSWORDS"}}
}
func TestSetupDraftRejectsSecretURLsAndCanceledServices(t *testing.T) {
	if (SetupDraft{}).Validate() != nil || setupFixtureDraft().Validate() != nil {
		t.Fatal("valid or partial draft rejected")
	}
	for _, change := range []func(*SetupDraft){
		func(d *SetupDraft) { d.Inbound = "gmail-imap" }, func(d *SetupDraft) { d.Inbound = "nas-smtp" },
		func(d *SetupDraft) { d.SourceURL = "https://user:password@example.test" }, func(d *SetupDraft) { d.SourceURL = "https://example.test/?token=secret" },
		func(d *SetupDraft) { d.SourceURL = "http://example.test" }, func(d *SetupDraft) { d.SourceURL = "https://example.test/private" },
		func(d *SetupDraft) { d.IngressSecretRef = "eu_live_secret" }, func(d *SetupDraft) { d.Outbound.SecretRef = "secrets.OTHER_SERVICE" },
		func(d *SetupDraft) { d.Outbound.Provider = "smtp2go" }, func(d *SetupDraft) { d.Outbound.TLS = "none" },
		func(d *SetupDraft) { d.Limits.CloudBytes = -1 }, func(d *SetupDraft) { d.Limits.AdditionalMailboxes = -1 },
	} {
		d := setupFixtureDraft()
		change(&d)
		if d.Validate() == nil {
			t.Fatal("unsafe draft accepted")
		}
	}
}

func TestDirectSetupDraftHasNoCredentials(t *testing.T) {
	draft := setupFixtureDraft()
	draft.Outbound = SetupOutbound{Provider: "direct"}
	if err := draft.Validate(); err != nil {
		t.Fatal("direct draft rejected", err)
	}
	for _, bad := range []SetupOutbound{
		{Provider: "direct", SecretRef: "secrets.MAIL_OUTBOUND_PASSWORDS"},
		{Provider: "direct", Host: "smtp.example.test"},
		{Provider: "direct", Port: 25},
		{Provider: "direct", Username: "someone"},
	} {
		draft.Outbound = bad
		if draft.Validate() == nil {
			t.Fatal("direct draft accepted a relay field", bad)
		}
	}
}

func TestVPSDirectDraftIsAcceptedWithoutCloudflare(t *testing.T) {
	draft := setupFixtureDraft()
	draft.Inbound = "vps-direct"
	if err := draft.Validate(); err != nil {
		t.Fatal("VPS direct draft rejected", err)
	}
}
func TestDirectReceivingDraftRejectsCloudCredentials(t *testing.T) {
	draft := SetupDraft{Inbound: "direct", Domain: "example.test"}
	if err := draft.Validate(); err != nil {
		t.Fatal("direct receiving draft rejected", err)
	}
	for _, change := range []func(*SetupDraft){
		func(d *SetupDraft) { d.SourceURL = "https://source.example.test" },
		func(d *SetupDraft) { d.IngressSecretRef = "secrets.MAIL_INGRESS" },
		func(d *SetupDraft) { d.NotificationURL = "https://notify.example.test" },
		func(d *SetupDraft) { d.NotificationSecretRef = "secrets.MAIL_NOTIFY" },
	} {
		value := draft
		change(&value)
		if value.Validate() == nil {
			t.Fatal("cloud field accepted for direct receiving")
		}
	}
}
func TestPostgreSQLSetupDraftCASAndRevocation(t *testing.T) {
	s, ctx, grant, secret := credentialFixture(t)
	const fallbackGroup = "c747d482-f7a9-4586-8a12-a733488caf2f"
	group, err := s.AdmissionGroup(ctx, "administrator")
	if errors.Is(err, ErrAdmissionGroupUnknown) {
		err = s.PinAdmissionGroup(ctx, "administrator", fallbackGroup)
		group = fallbackGroup
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM mail.setup_draft`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.ExecContext(ctx, `DELETE FROM mail.setup_draft`) })
	state, err := s.SetupDraft(ctx, secret, grant.OwnerAnchor, group)
	if err != nil || state.Revision != 0 {
		t.Fatal("initial draft", err)
	}
	draft := setupFixtureDraft()
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.SaveSetupDraft(ctx, secret, grant.OwnerAnchor, group, 0, draft)
			out <- e
		}()
	}
	wg.Wait()
	close(out)
	wins, conflicts := 0, 0
	for e := range out {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrSetupConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("concurrent administrators overwrote a draft")
	}
	// A new Store instance reads the persisted draft without browser storage.
	state, err = (Store{DB: s.DB}).SetupDraft(ctx, secret, grant.OwnerAnchor, group)
	if err != nil || state.Revision != 1 || state.UpdatedAt == nil || state.Draft != draft {
		t.Fatal("draft not durable", err)
	}
	if _, err = s.SaveSetupDraft(ctx, secret, grant.OwnerAnchor, fallbackGroup[:35]+"0", 1, draft); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("wrong pinned group accepted", err)
	}
	if err = s.RevokeSession(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveSetupDraft(ctx, secret, grant.OwnerAnchor, group, 1, draft); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("revoked session changed draft", err)
	}
	if _, err = s.SetupDraft(ctx, secret, grant.OwnerAnchor, group); !errors.Is(err, ErrSessionDenied) {
		t.Fatal("revoked session read draft", err)
	}
	var revision int64
	if err = s.DB.QueryRowContext(ctx, `SELECT revision FROM mail.setup_draft`).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("denied write changed persisted plan", err)
	}
}
