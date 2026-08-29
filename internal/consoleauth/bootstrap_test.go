package consoleauth

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBootstrapCredentialLifecycleStoresOnlyDigests(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	issued := issueTestBootstrap(t, store, "txn-one")
	assertCredentialEntropy(t, issued.Token)
	if issued.ExpiresAt.Sub(issued.IssuedAt) != DefaultBootstrapTokenTTL {
		t.Fatalf("token TTL = %s", issued.ExpiresAt.Sub(issued.IssuedAt))
	}
	assertPrivatePath(t, directory, 0o700)
	assertPrivatePath(t, filepath.Join(directory, lockFileName), 0o600)
	assertPrivatePath(t, filepath.Join(directory, bootstrapFileName), 0o600)
	assertFileOmits(t, filepath.Join(directory, bootstrapFileName), issued.Token)

	session := exchangeTestBootstrap(t, store, issued.Token)
	assertCredentialEntropy(t, session.Token)
	assertCredentialEntropy(t, session.CSRFToken)
	if session.Origin != "http://nas.example.test" || session.ExpiresAt.Sub(session.CreatedAt) != BootstrapSessionAbsoluteTTL || session.IdleExpiresAt.Sub(session.CreatedAt) != BootstrapSessionIdleTTL {
		t.Fatalf("bootstrap session = %#v", session)
	}
	assertFileOmits(t, filepath.Join(directory, bootstrapFileName), issued.Token, session.Token, session.CSRFToken)

	if _, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: issued.Token, Origin: session.Origin}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second exchange error = %v", err)
	}
	principal, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken,
		Origin: session.Origin, TransactionID: "txn-one", State: StateBootstrap,
		Route: "/api/v1/bootstrap/apply", RequireCSRF: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.TransactionID != "txn-one" || principal.ExpiresAt != session.ExpiresAt {
		t.Fatalf("principal = %#v", principal)
	}
	for _, request := range []BootstrapAuthenticationRequest{
		{SessionToken: session.Token, CSRFToken: "wrong", Origin: session.Origin, TransactionID: "txn-one", State: StateBootstrap, Route: "/api/v1/bootstrap/apply", RequireCSRF: true},
		{SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: "http://other.example", TransactionID: "txn-one", State: StateBootstrap, Route: "/api/v1/bootstrap/apply", RequireCSRF: true},
		{SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin, TransactionID: "txn-other", State: StateBootstrap, Route: "/api/v1/bootstrap/apply", RequireCSRF: true},
		{SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin, TransactionID: "txn-one", State: StateEnrollment, Route: "/api/v1/bootstrap/apply", RequireCSRF: true},
		{SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin, TransactionID: "txn-one", State: StateBootstrap, Route: "/api/v1/not-allowed", RequireCSRF: true},
	} {
		if _, err := store.AuthenticateBootstrap(context.Background(), request); err == nil {
			t.Fatalf("unauthorized request succeeded: %#v", request)
		}
	}

	events := audit.Events()
	if len(events) < 3 || events[0].Action != AuditBootstrapIssue || events[0].Outcome != AuditSuccess || events[1].Action != AuditBootstrapExchange || events[1].Outcome != AuditSuccess || events[2].Outcome != AuditFailure {
		t.Fatalf("audit events = %#v", events)
	}
	var authenticationFailures int
	for _, event := range events {
		if event.Action == AuditBootstrapAuth && event.Outcome == AuditFailure {
			authenticationFailures++
		}
	}
	if authenticationFailures != 5 {
		t.Fatalf("bootstrap authentication failures = %d, events = %#v", authenticationFailures, events)
	}
}

func TestBootstrapTTLBoundsAndReissueRevocation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	for _, ttl := range []time.Duration{14 * time.Minute, 31 * time.Minute} {
		_, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
			TTL: ttl, TransactionID: "txn-invalid", State: StateBootstrap, AllowedRoutes: []string{"/status"},
		})
		if err == nil {
			t.Fatalf("TTL %s was accepted", ttl)
		}
	}
	first := issueTestBootstrap(t, store, "txn-first")
	second := issueTestBootstrap(t, store, "txn-second")
	if _, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: first.Token, Origin: "http://nas.example"}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replaced token error = %v", err)
	}
	secondSession := exchangeTestBootstrap(t, store, second.Token)
	third := issueTestBootstrap(t, store, "txn-third")
	if _, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: secondSession.Token, Origin: secondSession.Origin, TransactionID: "txn-second",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("reissued session error = %v", err)
	}
	if _, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: third.Token, Origin: "http://nas.example"}); err != nil {
		t.Fatal(err)
	}
	events := audit.Events()
	var replacedToken, revokedSession bool
	for _, event := range events {
		if event.Action == AuditBootstrapIssue && event.Outcome == AuditSuccess && event.ReplacedToken {
			replacedToken = true
		}
		if event.Action == AuditBootstrapIssue && event.Outcome == AuditSuccess && event.RevokedSessions == 1 {
			revokedSession = true
		}
	}
	if !replacedToken || !revokedSession {
		t.Fatalf("reissue audit did not describe replacement: %#v", events)
	}
}

func TestBootstrapExchangeIsSingleConsumerAcrossStores(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	issuer := openTestStore(t, directory, audit, clock)
	issued := issueTestBootstrap(t, issuer, "txn-concurrent")

	const attempts = 24
	stores := make([]*Store, attempts)
	for index := range stores {
		stores[index] = openTestStore(t, directory, audit, clock)
	}
	start := make(chan struct{})
	var successes atomic.Int32
	var invalid atomic.Int32
	var wait sync.WaitGroup
	for _, store := range stores {
		wait.Add(1)
		go func(store *Store) {
			defer wait.Done()
			<-start
			_, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: issued.Token, Origin: "http://nas.example"})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrInvalidToken):
				invalid.Add(1)
			default:
				t.Errorf("exchange error = %v", err)
			}
		}(store)
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || invalid.Load() != attempts-1 {
		t.Fatalf("successes = %d, invalid = %d", successes.Load(), invalid.Load())
	}
}

func TestBootstrapSessionSurvivesRestartWithoutAbsoluteRenewal(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	session := exchangeTestBootstrap(t, store, issueTestBootstrap(t, store, "txn-restart").Token)

	clock.Advance(20 * time.Minute)
	restarted := openTestStore(t, directory, audit, clock)
	principal, err := restarted.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-restart",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.ExpiresAt != session.ExpiresAt || principal.IdleExpiresAt.Sub(session.CreatedAt) != 50*time.Minute {
		t.Fatalf("renewed principal = %#v", principal)
	}
	clock.Advance(25 * time.Minute)
	principal, err = openTestStore(t, directory, audit, clock).AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-restart",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.ExpiresAt != session.ExpiresAt || principal.IdleExpiresAt.Sub(session.CreatedAt) != 75*time.Minute {
		t.Fatalf("second renewal = %#v", principal)
	}
	clock.Advance(76 * time.Minute)
	_, err = restarted.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-restart",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	})
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("absolute expiry error = %v", err)
	}
}

func TestBootstrapObserveOnlyAuthenticationDoesNotExtendIdleExpiry(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	session := exchangeTestBootstrap(t, store, issueTestBootstrap(t, store, "txn-observe").Token)

	clock.Advance(20 * time.Minute)
	principal, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-observe",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status", ObserveOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.IdleExpiresAt != session.IdleExpiresAt {
		t.Fatalf("observe-only idle expiry = %s, want %s", principal.IdleExpiresAt, session.IdleExpiresAt)
	}

	clock.Advance(10 * time.Minute)
	if _, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-observe",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status", ObserveOnly: true,
	}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("observe-only idle expiry error = %v", err)
	}
}

func TestBootstrapTokenIdleExpiryAndExplicitRevocation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	issued, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
		TTL: MinimumBootstrapTokenTTL, TransactionID: "txn-expiry", State: StateBootstrap,
		AllowedRoutes: []string{"/status"},
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(MinimumBootstrapTokenTTL)
	if _, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: issued.Token, Origin: "http://nas.example"}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("token expiry error = %v", err)
	}
	session := exchangeTestBootstrap(t, store, issueTestBootstrap(t, store, "txn-idle").Token)
	clock.Advance(BootstrapSessionIdleTTL)
	if _, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-idle",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("idle expiry error = %v", err)
	}
	active := exchangeTestBootstrap(t, store, issueTestBootstrap(t, store, "txn-revoke").Token)
	if err := store.RevokeBootstrap(context.Background(), "txn-revoke"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: active.Token, Origin: active.Origin, TransactionID: "txn-revoke",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("revoked bootstrap session error = %v", err)
	}
}

func TestBootstrapAuditFailureDoesNotCommitCredentialChanges(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	audit.FailNext()
	_, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
		TransactionID: "txn-audit", State: StateBootstrap, AllowedRoutes: []string{"/status"},
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("issue audit error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, bootstrapFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("bootstrap state exists after failed issue audit: %v", statErr)
	}
	issued := issueTestBootstrap(t, store, "txn-audit")
	audit.FailNext()
	if _, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: issued.Token, Origin: "http://nas.example"}); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("exchange audit error = %v", err)
	}
	// The token remains valid because the failed audit prevented its consumption.
	session := exchangeTestBootstrap(t, store, issued.Token)
	audit.FailNext()
	if err := store.RevokeBootstrap(context.Background(), "txn-audit"); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("revoke audit error = %v", err)
	}
	if _, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, Origin: session.Origin, TransactionID: "txn-audit",
		State: StateBootstrap, Route: "/api/v1/bootstrap/status",
	}); err != nil {
		t.Fatalf("session was revoked despite failed audit: %v", err)
	}
}

func TestBootstrapAuthenticationFailureRequiresAudit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStore(t, directory, audit, newTestClock())
	session := exchangeTestBootstrap(t, store, issueTestBootstrap(t, store, "txn-auth-failure").Token)
	audit.FailNext()
	_, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: "wrong", Origin: session.Origin,
		TransactionID: "txn-auth-failure", State: StateBootstrap,
		Route: "/api/v1/bootstrap/apply", RequireCSRF: true,
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("authentication audit error = %v", err)
	}
}

func assertCredentialEntropy(t *testing.T, value string) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < credentialRandomBytes {
		t.Fatalf("credential decodes to %d bytes, error %v", len(raw), err)
	}
}

func assertPrivatePath(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v", path, info.Mode())
	}
}

func assertFileOmits(t *testing.T, path string, values ...string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value != "" && strings.Contains(string(body), value) {
			t.Fatalf("%s contains plaintext credential", path)
		}
	}
}
