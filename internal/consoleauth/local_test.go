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

func TestPasswordPHCAndVerification(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("PHC = %q", encoded)
	}
	verified, err := VerifyPassword(encoded, "correct horse battery staple")
	if err != nil || !verified {
		t.Fatalf("correct password = %t, %v", verified, err)
	}
	verified, err = VerifyPassword(encoded, "wrong password")
	if err != nil || verified {
		t.Fatalf("wrong password = %t, %v", verified, err)
	}
	for _, malformed := range []string{
		"plaintext",
		strings.Replace(encoded, "m=65536", "m=1024", 1),
		strings.Replace(encoded, "p=2", "p=2,x=1", 1),
		encoded + "junk",
	} {
		if _, err := VerifyPassword(malformed, "password"); err == nil {
			t.Fatalf("malformed PHC accepted: %q", malformed)
		}
	}
}

func TestLocalOwnerSessionLifecycleAndRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	if err := store.SetOwnerPassword(context.Background(), "owner-password"); err != nil {
		t.Fatal(err)
	}
	assertPrivatePath(t, filepath.Join(directory, localFileName), 0o600)
	assertFileOmits(t, filepath.Join(directory, localFileName), "owner-password")

	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "wrong", Origin: "https://anas.example"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong login error = %v", err)
	}
	credential, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "owner-password", Origin: "HTTPS://ANAS.EXAMPLE:443/"})
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialEntropy(t, credential.Token)
	assertCredentialEntropy(t, credential.CSRFToken)
	if credential.Origin != "https://anas.example" || credential.ExpiresAt.Sub(credential.CreatedAt) != LocalSessionAbsoluteTTL {
		t.Fatalf("credential = %#v", credential)
	}
	assertFileOmits(t, filepath.Join(directory, localFileName), credential.Token, credential.CSRFToken)

	for _, request := range []LocalAuthenticationRequest{
		{SessionToken: credential.Token, CSRFToken: "wrong", Origin: credential.Origin, RequireCSRF: true},
		{SessionToken: credential.Token, CSRFToken: credential.CSRFToken, Origin: "https://other.example", RequireCSRF: true},
	} {
		if _, err := store.AuthenticateLocal(context.Background(), request); err == nil {
			t.Fatalf("unauthorized local request succeeded: %#v", request)
		}
	}
	clock.Advance(20 * time.Minute)
	restarted := openTestStore(t, directory, audit, clock)
	principal, err := restarted.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{
		SessionToken: credential.Token, CSRFToken: credential.CSRFToken,
		Origin: credential.Origin, RequireCSRF: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.ExpiresAt != credential.ExpiresAt || principal.IdleExpiresAt.Sub(credential.CreatedAt) != 50*time.Minute {
		t.Fatalf("principal = %#v", principal)
	}
	if err := restarted.LogoutLocal(context.Background(), LocalLogoutRequest{
		SessionToken: credential.Token, CSRFToken: "wrong", Origin: credential.Origin,
	}); !errors.Is(err, ErrCSRFMismatch) {
		t.Fatalf("logout CSRF error = %v", err)
	}
	if _, err := restarted.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{
		SessionToken: credential.Token, Origin: credential.Origin,
	}); err != nil {
		t.Fatalf("failed CSRF logout revoked session: %v", err)
	}
	if err := restarted.LogoutLocal(context.Background(), LocalLogoutRequest{
		SessionToken: credential.Token, CSRFToken: credential.CSRFToken, Origin: credential.Origin,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{SessionToken: credential.Token, Origin: credential.Origin}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("logged out authentication error = %v", err)
	}

	events := audit.Events()
	var loginSuccess, loginFailure, logout bool
	for _, event := range events {
		if event.Action == AuditLocalLogin && event.Outcome == AuditSuccess {
			loginSuccess = true
		}
		if event.Action == AuditLocalLogin && event.Outcome == AuditFailure {
			loginFailure = true
		}
		if event.Action == AuditLocalLogout && event.Outcome == AuditSuccess {
			logout = true
		}
	}
	if !loginSuccess || !loginFailure || !logout {
		t.Fatalf("audit events = %#v", events)
	}
}

func TestPasswordChangeAndRevocationInvalidateLocalSessions(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	if err := store.SetOwnerPassword(context.Background(), "password-one"); err != nil {
		t.Fatal(err)
	}
	first, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "password-one", Origin: "https://anas.example"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "password-one", Origin: "https://anas.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeLocalSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first.Token, second.Token} {
		if _, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{SessionToken: token, Origin: "https://anas.example"}); !errors.Is(err, ErrSessionUnauthorized) {
			t.Fatalf("revoked session error = %v", err)
		}
	}
	third, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "password-one", Origin: "https://anas.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetOwnerPassword(context.Background(), "password-two"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{SessionToken: third.Token, Origin: "https://anas.example"}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("password-rotation session error = %v", err)
	}
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "password-one", Origin: "https://anas.example"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v", err)
	}
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "password-two", Origin: "https://anas.example"}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalObserveOnlyAuthenticationDoesNotExtendIdleExpiry(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	if err := store.SetOwnerPassword(context.Background(), "owner-password"); err != nil {
		t.Fatal(err)
	}
	credential, err := store.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "owner-password", Origin: "https://anas.example",
	})
	if err != nil {
		t.Fatal(err)
	}

	clock.Advance(20 * time.Minute)
	principal, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{
		SessionToken: credential.Token, Origin: credential.Origin, ObserveOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.IdleExpiresAt != credential.IdleExpiresAt {
		t.Fatalf("observe-only idle expiry = %s, want %s", principal.IdleExpiresAt, credential.IdleExpiresAt)
	}

	clock.Advance(10 * time.Minute)
	if _, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{
		SessionToken: credential.Token, Origin: credential.Origin, ObserveOnly: true,
	}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("observe-only idle expiry error = %v", err)
	}
}

func TestLocalAuditFailureDoesNotCommitSessionOrRevocation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	if err := store.SetOwnerPassword(context.Background(), "owner-password"); err != nil {
		t.Fatal(err)
	}
	audit.FailNext()
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "owner-password", Origin: "https://anas.example"}); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("login audit error = %v", err)
	}
	state, err := store.loadLocalState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Sessions) != 0 {
		t.Fatalf("failed login audit committed sessions: %#v", state.Sessions)
	}
	credential, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "owner-password", Origin: "https://anas.example"})
	if err != nil {
		t.Fatal(err)
	}
	audit.FailNext()
	if err := store.LogoutLocal(context.Background(), LocalLogoutRequest{
		SessionToken: credential.Token, CSRFToken: credential.CSRFToken, Origin: credential.Origin,
	}); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("logout audit error = %v", err)
	}
	if _, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{SessionToken: credential.Token, Origin: credential.Origin}); err != nil {
		t.Fatalf("failed logout audit revoked session: %v", err)
	}
	audit.FailNext()
	if err := store.RevokeLocalSessions(context.Background()); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("revocation audit error = %v", err)
	}
	if _, err := store.AuthenticateLocal(context.Background(), LocalAuthenticationRequest{SessionToken: credential.Token, Origin: credential.Origin}); err != nil {
		t.Fatalf("failed revocation audit revoked session: %v", err)
	}
}

func TestLocalLoginWithoutOwnerFailsClosedAndIsAudited(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStore(t, directory, audit, newTestClock())
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{Password: "guess", Origin: "https://anas.example"}); !errors.Is(err, ErrOwnerNotConfigured) {
		t.Fatalf("login without owner error = %v", err)
	}
	events := audit.Events()
	if len(events) != 1 || events[0].Action != AuditLocalLogin || events[0].Outcome != AuditFailure {
		t.Fatalf("audit events = %#v", events)
	}
	if _, err := os.Stat(filepath.Join(directory, localFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("login without owner wrote state: %v", err)
	}
}
