// TEST_CASES: MAILMOD-T-080
package main

import (
	"context"
	"io"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// cloudflareVPSFixtureSource keeps the one-time destination verification mail
// on the disposable relay. It is only for the combined isolated acceptance
// fixture: the real consumer sees the synthetic test message through the real
// source, but never downloads or acknowledges the verification message. This
// filtered view must not be used to claim an unmodified production poll.
type cloudflareVPSFixtureSource struct {
	source       ingress.Source
	sourceID     string
	generation   string
	verification string
	expected     string
	route        ingress.Route
}

func (s cloudflareVPSFixtureSource) matches(m ingress.SourceMessage) bool {
	v := m.Metadata
	return s.sourceID != "" && s.generation != "" && s.verification != "" && s.route.Address != "" && s.route.MailboxID != "" && s.route.OwnerAnchor != "" && s.route.ClaimVersion != "" &&
		v.SourceID == s.sourceID && v.MessageID != "" && v.AcceptedAddress == s.route.Address &&
		v.MailboxID == s.route.MailboxID && v.OwnerAnchor == s.route.OwnerAnchor && v.ClaimVersion == s.route.ClaimVersion
}

func (s cloudflareVPSFixtureSource) allowed(m ingress.SourceMessage) bool {
	return s.matches(m) && m.Metadata.MessageID != s.verification && (s.expected == "" || m.Metadata.MessageID == s.expected)
}

func (s cloudflareVPSFixtureSource) List(ctx context.Context, cursor string) (ingress.SourcePage, error) {
	if s.source == nil || s.verification == "" || cursor != "" {
		return ingress.SourcePage{}, ingress.ErrIdentity
	}
	page, err := s.source.List(ctx, cursor)
	if err != nil {
		return ingress.SourcePage{}, err
	}
	if page.SourceID != s.sourceID || page.Generation != s.generation || page.Cursor != nil || len(page.Messages) < 1 || len(page.Messages) > 2 {
		return ingress.SourcePage{}, ingress.ErrIdentity
	}
	out := make([]ingress.SourceMessage, 0, 1)
	seenVerification := false
	for _, m := range page.Messages {
		if !s.matches(m) {
			return ingress.SourcePage{}, ingress.ErrIdentity
		}
		if m.Metadata.MessageID == s.verification {
			if seenVerification {
				return ingress.SourcePage{}, ingress.ErrIdentity
			}
			seenVerification = true
			continue
		}
		if !s.allowed(m) {
			return ingress.SourcePage{}, ingress.ErrIdentity
		}
		out = append(out, m)
	}
	if !seenVerification || len(out) > 1 {
		return ingress.SourcePage{}, ingress.ErrIdentity
	}
	page.Messages = out
	return page, nil
}

func (s cloudflareVPSFixtureSource) Download(ctx context.Context, m ingress.SourceMessage, dst io.Writer) (string, error) {
	if s.source == nil || !s.allowed(m) {
		return "", ingress.ErrIdentity
	}
	return s.source.Download(ctx, m, dst)
}

func (s cloudflareVPSFixtureSource) Acknowledge(ctx context.Context, m ingress.SourceMessage, receipt dms.IntakeReceipt) error {
	if s.source == nil || !s.allowed(m) {
		return ingress.ErrIdentity
	}
	return s.source.Acknowledge(ctx, m, receipt)
}

func (s cloudflareVPSFixtureSource) Message(ctx context.Context, id, target string) (ingress.SourceMessage, error) {
	if s.source == nil || s.verification == "" || id == "" || id == s.verification || s.expected != "" && id != s.expected || target != s.route.MailboxID {
		return ingress.SourceMessage{}, ingress.ErrIdentity
	}
	m, err := s.source.Message(ctx, id, target)
	if err != nil {
		return ingress.SourceMessage{}, err
	}
	if !s.allowed(m) || m.Metadata.MessageID != id {
		return ingress.SourceMessage{}, ingress.ErrIdentity
	}
	return m, nil
}
