package consoleauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxySessionAndRecentAuthenticationStepUpAreIdentityBound(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	directory := filepath.Join(t.TempDir(), "auth")
	store, err := Open(directory, AuditSinkFunc(func(context.Context, AuditEvent) error { return nil }), StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	identity := ProxyIdentity{
		Issuer: "https://iam.example.test", Subject: "subject-123", SemanticRole: "platform_admin",
		DirectoryGroup: "NAS Admins", AuthenticatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
		AssertionDigest: strings.Repeat("a", 64),
	}
	session, err := store.RefreshProxySession(context.Background(), ProxySessionRefreshRequest{
		Origin: "https://anas.example.test:9000", Identity: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateProxy(context.Background(), ProxyAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		Identity: identity, RequireCSRF: true,
	}); err != nil {
		t.Fatal(err)
	}
	proof, err := store.IssueProxyStepUp(context.Background(), ProxyStepUpRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin, Identity: identity,
		Action: "deployment.apply", WorkspaceID: "main", StateDigest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if proof.ExpiresAt.After(identity.AuthenticatedAt.Add(ProxyRecentAuthenticationTTL)) {
		t.Fatalf("proof expiry %s exceeds recent-authentication window", proof.ExpiresAt)
	}
	if _, err := store.AuthenticateProxyStepUp(context.Background(), ProxyStepUpAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, Identity: identity, Token: proof.Token,
		Action: "deployment.apply", WorkspaceID: "main", StateDigest: strings.Repeat("b", 64),
	}); err != nil {
		t.Fatal(err)
	}
	changed := identity
	changed.Subject = "another-subject"
	if _, err := store.AuthenticateProxy(context.Background(), ProxyAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, Identity: changed,
	}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("changed subject error = %v", err)
	}
	changed = identity
	changed.AssertionDigest = strings.Repeat("c", 64)
	if _, err := store.AuthenticateProxyStepUp(context.Background(), ProxyStepUpAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, Identity: changed, Token: proof.Token,
		Action: "deployment.apply", WorkspaceID: "main", StateDigest: strings.Repeat("b", 64),
	}); !errors.Is(err, ErrStepUpUnauthorized) {
		t.Fatalf("changed assertion error = %v", err)
	}
	if _, err := store.AuthenticateProxyStepUp(context.Background(), ProxyStepUpAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, Identity: identity, Token: proof.Token,
		Action: "deployment.delete", WorkspaceID: "main", StateDigest: strings.Repeat("b", 64),
	}); !errors.Is(err, ErrStepUpUnauthorized) {
		t.Fatalf("changed action error = %v", err)
	}

	state, err := os.ReadFile(filepath.Join(directory, proxyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), session.Token) || strings.Contains(string(state), proof.Token) {
		t.Fatal("proxy state persisted a raw credential")
	}
}

func TestProxyStepUpRejectsStaleOIDCAuthentication(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store, err := Open(filepath.Join(t.TempDir(), "auth"), AuditSinkFunc(func(context.Context, AuditEvent) error { return nil }), StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	identity := ProxyIdentity{
		Issuer: "https://iam.example.test", Subject: "subject-123", SemanticRole: "platform_admin",
		DirectoryGroup: "NAS Admins", AuthenticatedAt: now.Add(-ProxyRecentAuthenticationTTL - time.Second),
		ExpiresAt: now.Add(time.Hour), AssertionDigest: strings.Repeat("a", 64),
	}
	session, err := store.RefreshProxySession(context.Background(), ProxySessionRefreshRequest{Origin: "https://anas.example.test:9000", Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.IssueProxyStepUp(context.Background(), ProxyStepUpRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin, Identity: identity,
		Action: "deployment.apply", WorkspaceID: "main", StateDigest: strings.Repeat("b", 64),
	})
	if !errors.Is(err, ErrStepUpUnauthorized) {
		t.Fatalf("stale OIDC authentication error = %v", err)
	}
}

func TestConsumeProxyStepUpIsAssertionBoundAndSingleUse(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	store, err := Open(filepath.Join(t.TempDir(), "auth"), AuditSinkFunc(func(context.Context, AuditEvent) error { return nil }), StoreOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	identity := ProxyIdentity{
		Issuer: "https://iam.example.test", Subject: "subject-123", SemanticRole: "platform_admin",
		DirectoryGroup: "NAS Admins", AuthenticatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), AssertionDigest: strings.Repeat("a", 64),
	}
	session, err := store.RefreshProxySession(context.Background(), ProxySessionRefreshRequest{Origin: "https://anas.example.test", Identity: identity})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := store.IssueProxyStepUp(context.Background(), ProxyStepUpRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin, Identity: identity,
		Action: "local_admin.reveal", WorkspaceID: "main", TargetID: "lad_" + strings.Repeat("a", 64), StateDigest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ProxyStepUpAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, Identity: identity, Token: proof.Token,
		Action: proof.Action, WorkspaceID: proof.WorkspaceID, TargetID: proof.TargetID, StateDigest: proof.StateDigest,
	}
	changed := request
	changed.Identity.AssertionDigest = strings.Repeat("c", 64)
	if _, err := store.ConsumeProxyStepUp(context.Background(), changed); !errors.Is(err, ErrStepUpUnauthorized) {
		t.Fatalf("changed assertion consume error = %v", err)
	}
	if _, err := store.ConsumeProxyStepUp(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeProxyStepUp(context.Background(), request); !errors.Is(err, ErrStepUpUnauthorized) {
		t.Fatalf("second consume error = %v", err)
	}
}
