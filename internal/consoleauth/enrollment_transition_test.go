package consoleauth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

var testEnrollmentRecoveryRoutes = []string{
	"/api/v1/jobs/current",
	EnrollmentHandoffRoute,
}

func TestAdvanceToEnrollmentNarrowsActiveBootstrapTokenBeforeStatePublish(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStore(t, directory, audit, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-token")
	capabilityState := StateBootstrap

	err := store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		state, err := store.loadBootstrapState()
		if err != nil {
			t.Fatal(err)
		}
		if state.Token == nil || state.Token.State != StateEnrollment || !reflect.DeepEqual(state.Token.AllowedRoutes, testEnrollmentRecoveryRoutes) {
			t.Fatalf("authentication candidate was not published before callback: %#v", state.Token)
		}
		journal, found, err := store.loadEnrollmentCommitJournal()
		if err != nil || !found || journal.Phase != enrollmentCommitAuthReady {
			t.Fatalf("journal at callback: found=%t phase=%q err=%v", found, journal.Phase, err)
		}
		events := audit.Events()
		last := events[len(events)-1]
		if last.Action != AuditBootstrapPromote || last.Outcome != AuditSuccess {
			t.Fatalf("promotion was not audited before callback: %#v", last)
		}
		if capabilityState != StateBootstrap {
			t.Fatalf("capability state before callback = %s", capabilityState)
		}
		capabilityState = StateEnrollment
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilityState != StateEnrollment {
		t.Fatalf("capability state = %s", capabilityState)
	}
	if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || found {
		t.Fatalf("journal after success: found=%t err=%v", found, err)
	}

	session, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateEnrollment || !reflect.DeepEqual(session.AllowedRoutes, testEnrollmentRecoveryRoutes) {
		t.Fatalf("exchanged enrollment credential = %#v", session)
	}
}

func TestAdvanceToEnrollmentConvergesAfterRequestCancellationAtDurablePrepare(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-canceled-advance")
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	store.beforeEnrollmentCommitStep = func(step string) error {
		if step == "before_state_transition" {
			cancelRequest()
		}
		return nil
	}
	capabilityState := StateBootstrap
	transitionCalled := false
	err := store.AdvanceToEnrollment(requestCtx, issued.TransactionID, testEnrollmentRecoveryRoutes, func(ctx context.Context) error {
		transitionCalled = true
		requireBoundedConvergenceContext(t, ctx)
		capabilityState = StateEnrollment
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(requestCtx.Err(), context.Canceled) {
		t.Fatalf("request context error = %v", requestCtx.Err())
	}
	if !transitionCalled || capabilityState != StateEnrollment {
		t.Fatalf("transition called=%t capability state=%s", transitionCalled, capabilityState)
	}
	if _, found, loadErr := store.loadEnrollmentCommitJournal(); loadErr != nil || found {
		t.Fatalf("enrollment WAL after convergence: found=%t err=%v", found, loadErr)
	}
	session, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatalf("promoted bootstrap credential is unusable: %v", err)
	}
	if session.State != StateEnrollment || !reflect.DeepEqual(session.AllowedRoutes, testEnrollmentRecoveryRoutes) {
		t.Fatalf("promoted session = %#v", session)
	}
}

func TestAdvanceToEnrollmentResolvesAndRollsBackAfterTransitionBudgetExpires(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	memory := &memoryAudit{}
	rollbackUsedFreshContext := false
	audit := AuditSinkFunc(func(ctx context.Context, event AuditEvent) error {
		if event.Action == AuditBootstrapPromote && event.Reason == "transaction_rolled_back" {
			requireBoundedConvergenceContext(t, ctx)
			rollbackUsedFreshContext = true
		}
		return memory.Record(ctx, event)
	})
	resolutionUsedFreshContext := false
	store := openTestStoreWithState(t, directory, audit, newTestClock(), func(ctx context.Context) (ConsoleState, error) {
		requireBoundedConvergenceContext(t, ctx)
		resolutionUsedFreshContext = true
		return StateBootstrap, nil
	})
	store.transactionTimeout = 500 * time.Millisecond
	issued := issueTestBootstrap(t, store, "txn-advance-timeout")

	err := store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(ctx context.Context) error {
		requireBoundedConvergenceContext(t, ctx)
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("enrollment transition timeout error = %v", err)
	}
	if !resolutionUsedFreshContext || !rollbackUsedFreshContext {
		t.Fatalf("fresh convergence contexts: resolution=%t rollback=%t", resolutionUsedFreshContext, rollbackUsedFreshContext)
	}
	if _, found, loadErr := store.loadEnrollmentCommitJournal(); loadErr != nil || found {
		t.Fatalf("enrollment WAL after transition timeout: found=%t err=%v", found, loadErr)
	}
	session, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatalf("rolled-back bootstrap credential is unusable: %v", err)
	}
	if session.State != StateBootstrap {
		t.Fatalf("rolled-back bootstrap session state = %s", session.State)
	}
}

func TestAdvanceToEnrollmentNarrowsActiveBootstrapSession(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-session")
	session := exchangeTestBootstrap(t, store, issued.Token)

	if err := store.AdvanceToEnrollment(context.Background(), session.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	principal, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, State: StateEnrollment,
		Route: EnrollmentHandoffRoute, RequireCSRF: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.State != StateEnrollment || !reflect.DeepEqual(principal.AllowedRoutes, testEnrollmentRecoveryRoutes) {
		t.Fatalf("principal = %#v", principal)
	}
	if _, err := store.AuthenticateBootstrap(context.Background(), BootstrapAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, State: StateEnrollment,
		Route: "/api/v1/bootstrap/apply", RequireCSRF: true,
	}); !errors.Is(err, ErrRouteNotAllowed) {
		t.Fatalf("old bootstrap route error = %v", err)
	}
}

func TestAdvanceToEnrollmentAllowsNoActiveCredential(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	called := false
	if err := store.AdvanceToEnrollment(context.Background(), "txn-empty", testEnrollmentRecoveryRoutes, func(context.Context) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("state transition was not called")
	}
	if _, err := os.Lstat(filepath.Join(directory, bootstrapFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty authentication state unexpectedly created: %v", err)
	}
}

func TestAdvanceToEnrollmentRejectsWrongTransactionOrCredentialState(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *Store)
		want    error
	}{
		{
			name:    "different transaction",
			prepare: func(t *testing.T, store *Store) { issueTestBootstrap(t, store, "txn-other") },
			want:    ErrTransactionMismatch,
		},
		{
			name: "already enrollment",
			prepare: func(t *testing.T, store *Store) {
				issueTestBootstrap(t, store, "txn-target")
				if err := store.PromoteBootstrapToEnrollment(context.Background(), "txn-target", testEnrollmentRecoveryRoutes); err != nil {
					t.Fatal(err)
				}
			},
			want: ErrStateMismatch,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
			test.prepare(t, store)
			previous, err := store.loadAuthenticationSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = store.AdvanceToEnrollment(context.Background(), "txn-target", testEnrollmentRecoveryRoutes, func(context.Context) error {
				called = true
				return nil
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("advance error = %v, want %v", err, test.want)
			}
			if called {
				t.Fatal("state transition ran for invalid authentication state")
			}
			after, err := store.loadAuthenticationSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, previous) {
				t.Fatal("authentication state changed after rejected advance")
			}
		})
	}
}

func TestAdvanceToEnrollmentRejectsExistingLocalOwnerState(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
	if err := store.SetOwnerPassword(context.Background(), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = store.AdvanceToEnrollment(context.Background(), "txn-owner-exists", testEnrollmentRecoveryRoutes, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrStateMismatch) {
		t.Fatalf("advance error = %v", err)
	}
	if called {
		t.Fatal("state transition ran with an existing local owner")
	}
	after, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, previous) {
		t.Fatal("authentication state changed after local-owner rejection")
	}
}

func TestAdvanceToEnrollmentTransitionFailureRollsBack(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStoreWithState(t, directory, audit, newTestClock(), func(context.Context) (ConsoleState, error) {
		return StateBootstrap, nil
	})
	issued := issueTestBootstrap(t, store, "txn-rollback")
	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("capability transition fixture failed")
	err = store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("advance error = %v", err)
	}
	after, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, previous) {
		t.Fatal("authentication snapshot was not rolled back")
	}
	if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || found {
		t.Fatalf("journal after rollback: found=%t err=%v", found, err)
	}
	events := audit.Events()
	last := events[len(events)-1]
	if last.Action != AuditBootstrapPromote || last.Outcome != AuditFailure || last.Reason != "transaction_rolled_back" {
		t.Fatalf("rollback audit event = %#v", last)
	}
}

func TestAdvanceToEnrollmentPublishedThenTransitionErrorKeepsCandidate(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	capabilityState := StateBootstrap
	store := openTestStoreWithState(t, directory, &memoryAudit{}, newTestClock(), func(context.Context) (ConsoleState, error) {
		return capabilityState, nil
	})
	issued := issueTestBootstrap(t, store, "txn-published-enrollment")
	postPublishErr := errors.New("directory sync failed after state rename")
	err := store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		capabilityState = StateEnrollment
		return postPublishErr
	})
	if err != nil {
		t.Fatalf("published enrollment advance returned error: %v", err)
	}
	if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || found {
		t.Fatalf("journal after reconciled enrollment publish: found=%t err=%v", found, err)
	}
	session, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateEnrollment || !reflect.DeepEqual(session.AllowedRoutes, testEnrollmentRecoveryRoutes) {
		t.Fatalf("published authentication candidate = %#v", session)
	}
}

func TestAdvanceToEnrollmentUncertainTransitionErrorKeepsWAL(t *testing.T) {
	stateReadErr := errors.New("capability state unavailable")
	for _, test := range []struct {
		name     string
		provider func(context.Context) (ConsoleState, error)
	}{
		{name: "no provider"},
		{name: "read failure", provider: func(context.Context) (ConsoleState, error) {
			return "", stateReadErr
		}},
		{name: "invalid state", provider: func(context.Context) (ConsoleState, error) {
			return ConsoleState("invalid"), nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "auth")
			store := openTestStoreWithState(t, directory, &memoryAudit{}, newTestClock(), test.provider)
			issued := issueTestBootstrap(t, store, "txn-advance-uncertain")
			transitionErr := errors.New("ambiguous transition failure")
			err := store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
				return transitionErr
			})
			if !errors.Is(err, transitionErr) || !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("advance error = %v", err)
			}
			if test.name == "read failure" && !errors.Is(err, stateReadErr) {
				t.Fatalf("advance error does not retain state read failure: %v", err)
			}
			if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || !found {
				t.Fatalf("uncertain transaction WAL: found=%t err=%v", found, err)
			}
			if _, err := store.CurrentBootstrapTransaction(context.Background(), StateEnrollment); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("normal API did not fail closed: %v", err)
			}
			if err := store.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
				return StateBootstrap, nil
			}); err != nil {
				t.Fatal(err)
			}
			transactionID, err := store.CurrentBootstrapTransaction(context.Background(), StateBootstrap)
			if err != nil || transactionID != issued.TransactionID {
				t.Fatalf("bootstrap credential was not recovered: transaction=%q err=%v", transactionID, err)
			}
		})
	}
}

func TestAdvanceToEnrollmentAuditFailureDoesNotWriteOrPublish(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStore(t, directory, audit, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-audit")
	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	audit.FailNext()
	called := false
	err = store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("advance error = %v", err)
	}
	if called {
		t.Fatal("state transition ran after audit failure")
	}
	after, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, previous) {
		t.Fatal("authentication state changed after audit failure")
	}
	if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || found {
		t.Fatalf("journal after audit failure: found=%t err=%v", found, err)
	}
}

func TestAdvanceToEnrollmentRollbackAuditFailureKeepsWAL(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStoreWithState(t, directory, audit, newTestClock(), func(context.Context) (ConsoleState, error) {
		return StateBootstrap, nil
	})
	issued := issueTestBootstrap(t, store, "txn-rollback-audit")
	transitionErr := errors.New("state transition failed")
	err := store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		audit.FailNext()
		return transitionErr
	})
	if !errors.Is(err, transitionErr) || !errors.Is(err, ErrAuditUnavailable) || !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("advance error = %v", err)
	}
	if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || !found {
		t.Fatalf("WAL after rollback audit failure: found=%t err=%v", found, err)
	}
	if _, err := store.CurrentBootstrapTransaction(context.Background(), StateBootstrap); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("normal API did not fail closed: %v", err)
	}
	if err := store.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
		return StateBootstrap, nil
	}); err != nil {
		t.Fatal(err)
	}
	transactionID, err := store.CurrentBootstrapTransaction(context.Background(), StateBootstrap)
	if err != nil || transactionID != issued.TransactionID {
		t.Fatalf("credential after audited recovery: transaction=%q err=%v", transactionID, err)
	}
}

func TestAdvanceToEnrollmentFailureInjectionRollsBack(t *testing.T) {
	for _, failStep := range []string{"journal_prepared", "authentication_written", "before_state_transition"} {
		t.Run(failStep, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
			issued := issueTestBootstrap(t, store, "txn-injected")
			previous, err := store.loadAuthenticationSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + failStep)
			store.beforeEnrollmentCommitStep = func(step string) error {
				if step == failStep {
					return injected
				}
				return nil
			}
			called := false
			err = store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
				called = true
				return nil
			})
			if !errors.Is(err, injected) {
				t.Fatalf("advance error = %v", err)
			}
			if failStep != "before_state_transition" && called {
				t.Fatal("state transition ran before injected failure")
			}
			after, err := store.loadAuthenticationSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(after, previous) {
				t.Fatal("authentication snapshot was not rolled back")
			}
		})
	}
}

func TestRecoverAdvanceToEnrollmentChoosesSnapshotFromCapabilityState(t *testing.T) {
	for _, capabilityState := range []ConsoleState{StateBootstrap, StateEnrollment, StateFull} {
		t.Run(string(capabilityState), func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "auth")
			audit := &memoryAudit{}
			clock := newTestClock()
			store := openTestStore(t, directory, audit, clock)
			issued := issueTestBootstrap(t, store, "txn-recovery")
			previous, next := prepareEnrollmentRecovery(t, store, issued.TransactionID)
			restarted := openTestStore(t, directory, audit, clock)

			if _, err := restarted.CurrentBootstrapTransaction(context.Background(), StateEnrollment); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("API while WAL is pending error = %v", err)
			}
			if err := restarted.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
				return capabilityState, nil
			}); err != nil {
				t.Fatal(err)
			}
			after, err := restarted.loadAuthenticationSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			want := next
			if capabilityState == StateBootstrap {
				want = previous
			}
			if !reflect.DeepEqual(after, want) {
				t.Fatalf("recovered snapshot for %s does not match selected side", capabilityState)
			}
			if _, found, err := restarted.loadEnrollmentCommitJournal(); err != nil || found {
				t.Fatalf("journal after recovery: found=%t err=%v", found, err)
			}
		})
	}
}

func TestRecoverAdvanceToEnrollmentAuditFailureLeavesWALAndCandidate(t *testing.T) {
	audit := &memoryAudit{}
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), audit, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-recovery-audit")
	_, next := prepareEnrollmentRecovery(t, store, issued.TransactionID)
	audit.FailNext()

	err := store.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
		return StateEnrollment, nil
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("recovery error = %v", err)
	}
	after, loadErr := store.loadAuthenticationSnapshot()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(after, next) {
		t.Fatal("authentication state changed despite recovery audit failure")
	}
	if _, found, loadErr := store.loadEnrollmentCommitJournal(); loadErr != nil || !found {
		t.Fatalf("journal after failed recovery audit: found=%t err=%v", found, loadErr)
	}
	if _, err := store.CurrentBootstrapTransaction(context.Background(), StateEnrollment); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("store did not remain fail closed: %v", err)
	}
}

func TestAdvanceToEnrollmentCleanupFailureReturnsSuccessAndRecoversForward(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-cleanup")
	store.beforeEnrollmentCommitStep = func(step string) error {
		if step == "before_journal_cleanup" {
			return errors.New("cleanup fixture failed")
		}
		return nil
	}
	capabilityState := StateBootstrap
	if err := store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
		capabilityState = StateEnrollment
		return nil
	}); err != nil {
		t.Fatalf("published advance returned cleanup error: %v", err)
	}
	if _, found, err := store.loadEnrollmentCommitJournal(); err != nil || !found {
		t.Fatalf("deferred cleanup WAL: found=%t err=%v", found, err)
	}
	store.beforeEnrollmentCommitStep = nil
	if err := store.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
		return capabilityState, nil
	}); err != nil {
		t.Fatal(err)
	}
	session, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.State != StateEnrollment {
		t.Fatalf("recovered credential state = %s", session.State)
	}
}

func TestRecoverAdvanceToEnrollmentRejectsCorruptOrUnknownJournal(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "corrupt", body: "{not-json\n"},
		{name: "unknown field", body: `{"api_version":"anas.console-auth-bootstrap-enrollment-commit/v1","unknown":true}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "auth")
			store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
			if err := os.WriteFile(filepath.Join(directory, enrollmentCommitFileName), []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			called := false
			err := store.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
				called = true
				return StateBootstrap, nil
			})
			if err == nil {
				t.Fatal("corrupt WAL was accepted")
			}
			if called {
				t.Fatal("capability provider called before WAL validation")
			}
			if _, err := store.CurrentBootstrapTransaction(context.Background(), StateBootstrap); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("store did not fail closed with invalid WAL: %v", err)
			}
		})
	}
}

func TestRecoverAdvanceToEnrollmentRejectsTamperedExistenceFlags(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	issued := issueTestBootstrap(t, store, "txn-existence-tamper")
	prepareEnrollmentRecovery(t, store, issued.TransactionID)
	journal, found, err := store.loadEnrollmentCommitJournal()
	if err != nil || !found {
		t.Fatalf("load valid journal: found=%t err=%v", found, err)
	}
	journal.Previous.BootstrapExisted = false
	journal.Next.BootstrapExisted = false
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitPrivateFileBytes(filepath.Join(directory, enrollmentCommitFileName), append(body, '\n')); err != nil {
		t.Fatal(err)
	}
	providerCalled := false
	err = store.RecoverAdvanceToEnrollment(context.Background(), func(context.Context) (ConsoleState, error) {
		providerCalled = true
		return StateEnrollment, nil
	})
	if err == nil {
		t.Fatal("tampered existence flags were accepted")
	}
	if providerCalled {
		t.Fatal("capability provider ran before WAL semantic validation")
	}
	if _, err := store.CurrentBootstrapTransaction(context.Background(), StateEnrollment); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("store did not fail closed after WAL tampering: %v", err)
	}
}

func TestAdvanceToEnrollmentSerializesConcurrentCommits(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	first := openTestStore(t, directory, audit, newTestClock())
	second := openTestStore(t, directory, audit, newTestClock())
	issued := issueTestBootstrap(t, first, "txn-concurrent")
	stores := []*Store{first, second}
	start := make(chan struct{})
	results := make(chan error, len(stores))
	var callbacks atomic.Int32
	for _, store := range stores {
		go func(store *Store) {
			<-start
			results <- store.AdvanceToEnrollment(context.Background(), issued.TransactionID, testEnrollmentRecoveryRoutes, func(context.Context) error {
				callbacks.Add(1)
				return nil
			})
		}(store)
	}
	close(start)
	var successes int
	for range stores {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrStateMismatch) {
			t.Fatalf("concurrent advance error = %v", err)
		}
	}
	if successes != 1 || callbacks.Load() != 1 {
		t.Fatalf("successes=%d callbacks=%d", successes, callbacks.Load())
	}
}

func prepareEnrollmentRecovery(t *testing.T, store *Store, transactionID string) (authenticationSnapshot, authenticationSnapshot) {
	t.Helper()
	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	next, err := enrollmentAuthenticationCandidate(previous, transactionID, testEnrollmentRecoveryRoutes)
	if err != nil {
		t.Fatal(err)
	}
	journal := enrollmentCommitJournal{
		APIVersion: enrollmentCommitAPIVersion, Phase: enrollmentCommitAuthReady,
		TransactionID: transactionID, RecoveryRoutes: cloneRoutes(testEnrollmentRecoveryRoutes),
		Previous: previous, Next: next,
	}
	if err := store.writeEnrollmentCommitJournal(journal); err != nil {
		t.Fatal(err)
	}
	if err := store.writeAuthenticationSnapshot(next); err != nil {
		t.Fatal(err)
	}
	return previous, next
}
