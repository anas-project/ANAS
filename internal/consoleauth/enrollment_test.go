package consoleauth

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const enrollmentTestSPKI = "abababababababababababababababababababababababababababababababab"

func TestEnrollmentRecoveryRoutePatternsReturnsIndependentSlice(t *testing.T) {
	want := []string{
		"/api/v1/system",
		"/api/v1/system/ca",
		"/api/v1/jobs",
		"/api/v1/jobs/{id}",
		"/api/v1/jobs/{id}/events",
		EnrollmentHandoffRoute,
	}
	first := EnrollmentRecoveryRoutePatterns()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("enrollment recovery routes = %v, want %v", first, want)
	}
	first[0] = "/mutated"
	if second := EnrollmentRecoveryRoutePatterns(); !reflect.DeepEqual(second, want) {
		t.Fatalf("enrollment recovery routes share mutable storage: %v", second)
	}
}

func issueTestEnrollmentHandoff(t *testing.T, store *Store, transactionID string) EnrollmentHandoffCredential {
	t.Helper()
	_, handoff := issueTestEnrollmentHandoffWithBootstrap(t, store, transactionID)
	return handoff
}

func issueTestEnrollmentHandoffWithBootstrap(t *testing.T, store *Store, transactionID string) (BootstrapSessionCredential, EnrollmentHandoffCredential) {
	t.Helper()
	issued, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
		TransactionID: transactionID, State: StateBootstrap,
		AllowedRoutes: []string{EnrollmentHandoffRoute},
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteBootstrapToEnrollment(context.Background(), transactionID, []string{EnrollmentHandoffRoute}); err != nil {
		t.Fatal(err)
	}
	credential, err := store.IssueEnrollmentHandoff(context.Background(), IssueEnrollmentHandoffRequest{
		SessionToken: bootstrap.Token, CSRFToken: bootstrap.CSRFToken,
		SourceOrigin: "http://nas.example.test", TargetOrigin: "https://anas.example.test",
		SPKISHA256: enrollmentTestSPKI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bootstrap, credential
}

func exchangeTestEnrollmentHandoff(t *testing.T, store *Store, credential EnrollmentHandoffCredential) EnrollmentSessionCredential {
	t.Helper()
	session, err := store.ExchangeEnrollmentHandoff(context.Background(), ExchangeEnrollmentHandoffRequest{
		Token: credential.Token, SourceOrigin: credential.SourceOrigin,
		TargetOrigin: credential.TargetOrigin, SPKISHA256: credential.SPKISHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestEnrollmentHandoffAndSessionPersistOnlyDigests(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	store := openTestStore(t, directory, audit, clock)
	handoff := issueTestEnrollmentHandoff(t, store, "txn-enrollment")
	assertCredentialEntropy(t, handoff.Token)
	if handoff.SourceOrigin != "http://nas.example.test" || handoff.TargetOrigin != "https://anas.example.test" {
		t.Fatalf("canonical handoff origins = %#v", handoff)
	}
	if handoff.ExpiresAt.Sub(handoff.CreatedAt) != EnrollmentHandoffTTL {
		t.Fatalf("handoff TTL = %s", handoff.ExpiresAt.Sub(handoff.CreatedAt))
	}
	path := filepath.Join(directory, bootstrapFileName)
	assertPrivatePath(t, path, 0o600)
	assertFileOmits(t, path, handoff.Token)

	session := exchangeTestEnrollmentHandoff(t, store, handoff)
	assertCredentialEntropy(t, session.Token)
	assertCredentialEntropy(t, session.CSRFToken)
	if session.TransactionID != handoff.TransactionID || session.Origin != handoff.TargetOrigin || session.ExpiresAt.Sub(session.CreatedAt) != EnrollmentSessionTTL {
		t.Fatalf("enrollment session = %#v", session)
	}
	assertFileOmits(t, path, handoff.Token, session.Token, session.CSRFToken)

	principal, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken,
		Origin: session.Origin, TransactionID: session.TransactionID,
		Route: EnrollmentOwnerRoute, RequireCSRF: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.TransactionID != session.TransactionID || principal.ExpiresAt != session.ExpiresAt {
		t.Fatalf("principal = %#v", principal)
	}
	if _, err := store.ExchangeEnrollmentHandoff(context.Background(), ExchangeEnrollmentHandoffRequest{
		Token: handoff.Token, SourceOrigin: handoff.SourceOrigin,
		TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
	}); !errors.Is(err, ErrHandoffUnauthorized) {
		t.Fatalf("second handoff exchange error = %v", err)
	}
}

func TestEnrollmentBindingMismatchDoesNotConsumeHandoff(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	handoff := issueTestEnrollmentHandoff(t, store, "txn-binding")

	requests := []ExchangeEnrollmentHandoffRequest{
		{Token: handoff.Token, SourceOrigin: "http://other.example", TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256},
		{Token: handoff.Token, SourceOrigin: handoff.SourceOrigin, TargetOrigin: "https://other.example", SPKISHA256: handoff.SPKISHA256},
		{Token: handoff.Token, SourceOrigin: handoff.SourceOrigin, TargetOrigin: handoff.TargetOrigin, SPKISHA256: strings.Repeat("cd", 32)},
	}
	for _, request := range requests {
		if _, err := store.ExchangeEnrollmentHandoff(context.Background(), request); !errors.Is(err, ErrHandoffUnauthorized) {
			t.Fatalf("binding mismatch error = %v for %#v", err, request)
		}
	}
	if session := exchangeTestEnrollmentHandoff(t, store, handoff); session.TransactionID != "txn-binding" {
		t.Fatalf("session = %#v", session)
	}
}

func TestEnrollmentExchangeAuditFailureDoesNotConsumeHandoff(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStore(t, directory, audit, newTestClock())
	handoff := issueTestEnrollmentHandoff(t, store, "txn-audit")
	audit.FailNext()
	_, err := store.ExchangeEnrollmentHandoff(context.Background(), ExchangeEnrollmentHandoffRequest{
		Token: handoff.Token, SourceOrigin: handoff.SourceOrigin,
		TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("audit failure error = %v", err)
	}
	if session := exchangeTestEnrollmentHandoff(t, store, handoff); session.TransactionID != "txn-audit" {
		t.Fatalf("retry session = %#v", session)
	}
}

func TestEnrollmentHandoffHasSingleConsumerAcrossStores(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	issuer := openTestStore(t, directory, audit, clock)
	handoff := issueTestEnrollmentHandoff(t, issuer, "txn-concurrent-enrollment")

	const attempts = 16
	start := make(chan struct{})
	var successes atomic.Int32
	var unauthorized atomic.Int32
	var wait sync.WaitGroup
	for range attempts {
		store := openTestStore(t, directory, audit, clock)
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.ExchangeEnrollmentHandoff(context.Background(), ExchangeEnrollmentHandoffRequest{
				Token: handoff.Token, SourceOrigin: handoff.SourceOrigin,
				TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrHandoffUnauthorized):
				unauthorized.Add(1)
			default:
				t.Errorf("exchange error = %v", err)
			}
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || unauthorized.Load() != attempts-1 {
		t.Fatalf("successes = %d, unauthorized = %d", successes.Load(), unauthorized.Load())
	}
}

func TestEnrollmentSessionExpiresAndRevokes(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	clock := newTestClock()
	store := openTestStore(t, directory, &memoryAudit{}, clock)
	handoff := issueTestEnrollmentHandoff(t, store, "txn-expiry")
	session := exchangeTestEnrollmentHandoff(t, store, handoff)
	clock.Advance(EnrollmentSessionTTL)
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("expired session error = %v", err)
	}

	second := issueTestEnrollmentHandoff(t, store, "txn-expiry")
	secondSession := exchangeTestEnrollmentHandoff(t, store, second)
	if err := store.RevokeEnrollment(context.Background(), secondSession.TransactionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: secondSession.Token, CSRFToken: secondSession.CSRFToken, Origin: secondSession.Origin,
		TransactionID: secondSession.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("revoked session error = %v", err)
	}
}

func TestEnrollmentRejectsNonHTTPSTargetAndNonCanonicalSPKI(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
	issued, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
		TransactionID: "txn", State: StateBootstrap, AllowedRoutes: []string{EnrollmentHandoffRoute},
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := exchangeTestBootstrap(t, store, issued.Token)
	if err := store.PromoteBootstrapToEnrollment(context.Background(), "txn", []string{EnrollmentHandoffRoute}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []IssueEnrollmentHandoffRequest{
		{SessionToken: bootstrap.Token, CSRFToken: bootstrap.CSRFToken, SourceOrigin: "http://nas.example", TargetOrigin: "http://anas.example", SPKISHA256: enrollmentTestSPKI},
		{SessionToken: bootstrap.Token, CSRFToken: bootstrap.CSRFToken, SourceOrigin: "http://nas.example", TargetOrigin: "https://anas.example", SPKISHA256: strings.ToUpper(enrollmentTestSPKI)},
		{SessionToken: bootstrap.Token, CSRFToken: bootstrap.CSRFToken, SourceOrigin: "null", TargetOrigin: "https://anas.example", SPKISHA256: enrollmentTestSPKI},
	} {
		if _, err := store.IssueEnrollmentHandoff(context.Background(), request); err == nil {
			t.Fatalf("invalid handoff request succeeded: %#v", request)
		}
	}
}

func TestEnrollmentBootstrapSessionCanIssueOnlyOneHandoff(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
	bootstrapCredential, handoff := issueTestEnrollmentHandoffWithBootstrap(t, store, "txn-single-handoff")
	state, err := store.loadBootstrapState()
	if err != nil {
		t.Fatal(err)
	}
	var bootstrap bootstrapSessionRecord
	for _, candidate := range state.Sessions {
		bootstrap = candidate
	}
	_, err = store.IssueEnrollmentHandoff(context.Background(), IssueEnrollmentHandoffRequest{
		SessionToken: bootstrapCredential.Token, CSRFToken: bootstrapCredential.CSRFToken,
		SourceOrigin: handoff.SourceOrigin, TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
	})
	if !errors.Is(err, ErrHandoffUnauthorized) {
		t.Fatalf("second handoff issue error = %v", err)
	}
	if !bootstrap.HandoffIssued {
		t.Fatal("bootstrap session did not persist handoff consumption")
	}
}

func TestNonCanonicalExchangeBindingDoesNotConsumeHandoff(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
	handoff := issueTestEnrollmentHandoff(t, store, "txn-exact-origin")
	_, err := store.ExchangeEnrollmentHandoff(context.Background(), ExchangeEnrollmentHandoffRequest{
		Token: handoff.Token, SourceOrigin: "HTTP://NAS.EXAMPLE.TEST:80/",
		TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
	})
	if !errors.Is(err, ErrHandoffUnauthorized) {
		t.Fatalf("non-canonical exchange error = %v", err)
	}
	if session := exchangeTestEnrollmentHandoff(t, store, handoff); session.TransactionID != handoff.TransactionID {
		t.Fatalf("canonical retry session = %#v", session)
	}
}

func TestEnrollmentHandoffExpiresWithoutAutomaticRenewal(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	clock := newTestClock()
	store := openTestStore(t, directory, &memoryAudit{}, clock)
	handoff := issueTestEnrollmentHandoff(t, store, "txn-expired-handoff")
	clock.Advance(EnrollmentHandoffTTL + time.Nanosecond)
	_, err := store.ExchangeEnrollmentHandoff(context.Background(), ExchangeEnrollmentHandoffRequest{
		Token: handoff.Token, SourceOrigin: handoff.SourceOrigin,
		TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
	})
	if !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("expired handoff error = %v", err)
	}
}
