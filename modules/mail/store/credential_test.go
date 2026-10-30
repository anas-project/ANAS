package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProtocolSecretsAreOpaqueAndSeparated(t *testing.T) {
	for _, prefix := range []string{"acc_", "smt_", "ref_"} {
		a, digest, err := protocolSecret(prefix)
		b, _, second := protocolSecret(prefix)
		if err != nil || second != nil || a == b || len(digest) != 32 || strings.Contains(string(digest), a) {
			t.Fatal("invalid opaque secret")
		}
		if _, ok := protocolHash(a, prefix); !ok {
			t.Fatal("generated secret rejected")
		}
	}
	for _, secret := range []string{"", "ses_" + strings.Repeat("a", 64), "acc_" + strings.Repeat("g", 64), "acc_" + strings.Repeat("a", 63)} {
		if _, err := (Store{}).IntrospectCredential(t.Context(), secret, "imap"); !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("invalid secret reached database", err)
		}
	}
	if _, err := (Store{}).IssueCredentials(t.Context(), MailboxGrant{}); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("unverified grant accepted")
	}
}

func credentialFixture(t *testing.T) (Store, context.Context, MailboxGrant, string) {
	t.Helper()
	s, ctx, r := projectionStore(t)
	r.Addresses = append(r.Addresses, r.Addresses[0])
	r.Addresses[1].Address = "alias-" + r.DefaultAddress
	r.Addresses[1].Kind = "alias"
	p, err := s.StageMailbox(ctx, r, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmMailbox(ctx, r.ID, p.Revision, p.Fingerprint); err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.AdmitSession(ctx, VerifiedLogin{OwnerAnchor: r.OwnerAnchor, Issuer: "https://credentials.example.test", SID: r.ID, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.IssueHandoff(ctx, secret, r.ID, strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.RedeemHandoff(ctx, h.Code, h.SessionID, r.ID, strings.Repeat("A", 43))
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, g, secret
}

func TestPostgreSQLCredentialsBindProtocolMailboxAndSender(t *testing.T) {
	s, ctx, g, _ := credentialFixture(t)
	c, err := s.IssueCredentials(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if c.Username != g.ProtocolLogin || c.RoundcubeKey != g.RoundcubeKey || len(c.Addresses) != 2 || c.MailboxID != g.MailboxID || c.Expires.After(time.Now().Add(91*time.Second)) || c.RefreshExpires.After(time.Now().Add(16*time.Minute)) {
		t.Fatal("credential scope or lifetime changed")
	}
	for _, item := range []struct {
		secret, protocol string
		allowed          bool
	}{
		{c.AccessToken, "imap", true}, {c.SMTPToken, "smtp", true}, {c.AccessToken, "smtp", false}, {c.SMTPToken, "imap", false}, {c.RefreshToken, "imap", false}, {c.AccessToken, "pop3", false},
	} {
		i, err := s.IntrospectCredential(ctx, item.secret, item.protocol)
		if item.allowed && (err != nil || i.Grant.MailboxID != g.MailboxID || i.SMTPLogin != c.SMTPUsername) {
			t.Fatal("valid credential denied", err)
		}
		if !item.allowed && !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("cross-protocol secret accepted", err)
		}
	}
	for _, address := range c.Addresses {
		if _, err := s.SenderGrant(ctx, c.SMTPUsername, address, g.DefaultAddress); err != nil {
			t.Fatal("owned alias denied", err)
		}
	}
	for _, pair := range [][2]string{{"foreign@example.test", g.DefaultAddress}, {g.DefaultAddress, "foreign@example.test"}, {strings.ToUpper(g.DefaultAddress), g.DefaultAddress}, {"", g.DefaultAddress}} {
		if _, err := s.SenderGrant(ctx, c.SMTPUsername, pair[0], pair[1]); !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("unowned sender accepted", err)
		}
	}
	var plaintext bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.protocol_credential WHERE encode(access_hash,'hex')=$1 OR encode(smtp_hash,'hex')=$2 OR encode(refresh_hash,'hex')=$3)`, c.AccessToken, c.SMTPToken, c.RefreshToken).Scan(&plaintext); err != nil || plaintext {
		t.Fatal("plaintext credential persisted", err)
	}
	for _, mutate := range []func(*MailboxGrant){func(v *MailboxGrant) { v.AuthorizationEpoch++ }, func(v *MailboxGrant) { v.OwnerAnchor = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }, func(v *MailboxGrant) { v.Issuer = "https://foreign.example.test" }} {
		bad := g
		mutate(&bad)
		if _, err := s.IssueCredentials(ctx, bad); !errors.Is(err, ErrCredentialDenied) {
			t.Fatal("stale/foreign grant issued credential", err)
		}
	}
}

func TestPostgreSQLCredentialsRefreshHasOneWinnerAndInvalidatesOld(t *testing.T) {
	s, ctx, g, _ := credentialFixture(t)
	c, err := s.IssueCredentials(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan Credentials, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() { next, err := s.RefreshCredentials(ctx, c.RefreshToken); out <- next; failures <- err })
	}
	wg.Wait()
	close(out)
	close(failures)
	winners := 0
	for err := range failures {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrCredentialDenied) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("refresh winners", winners)
	}
	var next Credentials
	for value := range out {
		if value.AccessToken != "" {
			next = value
		}
	}
	if _, err = s.IntrospectCredential(ctx, c.AccessToken, "imap"); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("rotated IMAP token survived", err)
	}
	if _, err = s.SenderGrant(ctx, c.SMTPUsername, g.DefaultAddress, g.DefaultAddress); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("rotated SMTP authorization survived", err)
	}
	if _, err = s.IntrospectCredential(ctx, next.AccessToken, "imap"); err != nil {
		t.Fatal("winner unusable", err)
	}
	if _, err = s.RefreshCredentials(ctx, c.RefreshToken); !errors.Is(err, ErrCredentialDenied) {
		t.Fatal("refresh replay accepted", err)
	}
	if _, err = s.IntrospectCredential(ctx, next.AccessToken, "imap"); err != nil {
		t.Fatal("replay revoked winner", err)
	}
}

func TestPostgreSQLCredentialsRespectRevocationAndProjection(t *testing.T) {
	for _, reason := range []string{"session", "person", "mailbox", "projection", "expiry"} {
		t.Run(reason, func(t *testing.T) {
			s, ctx, g, secret := credentialFixture(t)
			c, err := s.IssueCredentials(ctx, g)
			if err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "session":
				err = s.RevokeSession(ctx, secret)
			case "person":
				err = s.DenyPerson(ctx, g.OwnerAnchor)
			case "mailbox":
				err = s.WithholdMailbox(ctx, g.MailboxID)
			case "projection":
				_, err = s.DB.ExecContext(ctx, `UPDATE mail.mailbox_projection SET applied_revision=NULL WHERE mailbox_id=$1`, g.MailboxID)
			case "expiry":
				_, err = s.DB.ExecContext(ctx, `UPDATE mail.protocol_credential SET created_at=clock_timestamp()-interval '1 hour',expires_at=clock_timestamp()-interval '1 second',refresh_expires_at=clock_timestamp()-interval '1 second' WHERE mailbox_id=$1`, g.MailboxID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.IntrospectCredential(ctx, c.AccessToken, "imap"); !errors.Is(err, ErrCredentialDenied) {
				t.Fatal("revoked credential survived", err)
			}
			if _, err = s.SenderGrant(ctx, c.SMTPUsername, g.DefaultAddress, g.DefaultAddress); !errors.Is(err, ErrCredentialDenied) {
				t.Fatal("revoked sender survived", err)
			}
			if _, err = s.RefreshCredentials(ctx, c.RefreshToken); !errors.Is(err, ErrCredentialDenied) {
				t.Fatal("revoked credential refreshed", err)
			}
		})
	}
}
