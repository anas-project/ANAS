package consoleauth

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func enrollmentSessionForOwner(t *testing.T, store *Store, transactionID string) EnrollmentSessionCredential {
	t.Helper()
	handoff := issueTestEnrollmentHandoff(t, store, transactionID)
	return exchangeTestEnrollmentHandoff(t, store, handoff)
}

func TestCompleteInitialOwnerPublishesFullOnlyAfterRevokingAllCredentials(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	session := enrollmentSessionForOwner(t, store, "txn-owner")
	state := StateEnrollment
	err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Password: "correct horse battery staple",
	}, func(context.Context) error {
		if state != StateEnrollment {
			t.Fatalf("state before publish = %s", state)
		}
		state = StateFull
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if state != StateFull {
		t.Fatalf("state = %s", state)
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("enrollment session survived owner commit: %v", err)
	}
	if _, err := store.CurrentBootstrapTransaction(context.Background(), StateBootstrap); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("bootstrap credential survived owner commit: %v", err)
	}
	login, err := store.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "correct horse battery staple", Origin: "https://anas.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || login.CSRFToken == "" {
		t.Fatalf("local login = %#v", login)
	}
	if matches, err := filepath.Glob(filepath.Join(directory, ownerCommitFileName)); err != nil || len(matches) != 0 {
		t.Fatalf("owner commit journal remains after success: matches=%v err=%v", matches, err)
	}
}

func TestCompleteInitialOwnerConvergesAfterRequestCancellationAtDurablePrepare(t *testing.T) {
	transitionErr := errors.New("capability transition fixture failed")
	for _, test := range []struct {
		name    string
		publish bool
	}{
		{name: "commit published owner", publish: true},
		{name: "rollback unpublished owner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "auth")
			memory := &memoryAudit{}
			var rollbackAudited bool
			audit := AuditSinkFunc(func(ctx context.Context, event AuditEvent) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if event.Action == AuditOwnerEnroll && event.Reason == "transaction_rolled_back" {
					requireBoundedConvergenceContext(t, ctx)
					rollbackAudited = true
				}
				return memory.Record(ctx, event)
			})
			capabilityState := StateEnrollment
			var stateResolved bool
			store := openTestStoreWithState(t, directory, audit, newTestClock(), func(ctx context.Context) (ConsoleState, error) {
				requireBoundedConvergenceContext(t, ctx)
				stateResolved = true
				return capabilityState, nil
			})
			session := enrollmentSessionForOwner(t, store, "txn-canceled-request")

			requestCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			store.beforeOwnerCommitStep = func(step string) error {
				if step == "before_state_transition" {
					cancelRequest()
				}
				return nil
			}
			transitionCalled := false
			err := store.CompleteInitialOwner(requestCtx, CompleteInitialOwnerRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Password: "correct horse battery staple",
			}, func(ctx context.Context) error {
				transitionCalled = true
				requireBoundedConvergenceContext(t, ctx)
				if test.publish {
					capabilityState = StateFull
					return nil
				}
				return transitionErr
			})
			if !errors.Is(requestCtx.Err(), context.Canceled) {
				t.Fatalf("request context error = %v", requestCtx.Err())
			}
			if !transitionCalled {
				t.Fatal("state transition did not run after durable prepare")
			}
			if _, found, loadErr := store.loadOwnerCommitJournal(); loadErr != nil || found {
				t.Fatalf("owner WAL after convergence: found=%t err=%v", found, loadErr)
			}

			if test.publish {
				if err != nil {
					t.Fatalf("published owner commit error = %v", err)
				}
				if stateResolved || rollbackAudited {
					t.Fatalf("successful publish unexpectedly resolved state or rolled back: resolved=%t audited=%t", stateResolved, rollbackAudited)
				}
				if capabilityState != StateFull {
					t.Fatalf("capability state = %s", capabilityState)
				}
				if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
					Password: "correct horse battery staple", Origin: "https://anas.example.test",
				}); err != nil {
					t.Fatalf("committed owner cannot authenticate: %v", err)
				}
				return
			}

			if !errors.Is(err, transitionErr) {
				t.Fatalf("unpublished owner commit error = %v", err)
			}
			if !stateResolved || !rollbackAudited {
				t.Fatalf("failed publish did not resolve and roll back: resolved=%t audited=%t", stateResolved, rollbackAudited)
			}
			if capabilityState != StateEnrollment {
				t.Fatalf("capability state after rollback = %s", capabilityState)
			}
			if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
			}); err != nil {
				t.Fatalf("enrollment session was not restored: %v", err)
			}
			if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
				Password: "correct horse battery staple", Origin: "https://anas.example.test",
			}); !errors.Is(err, ErrOwnerNotConfigured) {
				t.Fatalf("unpublished owner survived rollback: %v", err)
			}
		})
	}
}

func TestCompleteInitialOwnerRejectsCanceledContextBeforePrepare(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	session := enrollmentSessionForOwner(t, store, "txn-pre-canceled")
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	transitionCalled := false
	err := store.CompleteInitialOwner(requestCtx, CompleteInitialOwnerRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Password: "correct horse battery staple",
	}, func(context.Context) error {
		transitionCalled = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled owner commit error = %v", err)
	}
	if transitionCalled {
		t.Fatal("state transition ran for a pre-canceled request")
	}
	if _, found, loadErr := store.loadOwnerCommitJournal(); loadErr != nil || found {
		t.Fatalf("owner WAL after pre-canceled request: found=%t err=%v", found, loadErr)
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); err != nil {
		t.Fatalf("pre-canceled request changed enrollment authentication: %v", err)
	}
}

func TestCompleteInitialOwnerResolvesAndRollsBackAfterTransitionBudgetExpires(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	memory := &memoryAudit{}
	rollbackUsedFreshContext := false
	audit := AuditSinkFunc(func(ctx context.Context, event AuditEvent) error {
		if event.Action == AuditOwnerEnroll && event.Reason == "transaction_rolled_back" {
			requireBoundedConvergenceContext(t, ctx)
			rollbackUsedFreshContext = true
		}
		return memory.Record(ctx, event)
	})
	resolutionUsedFreshContext := false
	store := openTestStoreWithState(t, directory, audit, newTestClock(), func(ctx context.Context) (ConsoleState, error) {
		requireBoundedConvergenceContext(t, ctx)
		resolutionUsedFreshContext = true
		return StateEnrollment, nil
	})
	store.transactionTimeout = 500 * time.Millisecond
	session := enrollmentSessionForOwner(t, store, "txn-transition-timeout")

	err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Password: "correct horse battery staple",
	}, func(ctx context.Context) error {
		requireBoundedConvergenceContext(t, ctx)
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("owner transition timeout error = %v", err)
	}
	if !resolutionUsedFreshContext || !rollbackUsedFreshContext {
		t.Fatalf("fresh convergence contexts: resolution=%t rollback=%t", resolutionUsedFreshContext, rollbackUsedFreshContext)
	}
	if _, found, loadErr := store.loadOwnerCommitJournal(); loadErr != nil || found {
		t.Fatalf("owner WAL after transition timeout: found=%t err=%v", found, loadErr)
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); err != nil {
		t.Fatalf("enrollment session was not restored after transition timeout: %v", err)
	}
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "correct horse battery staple", Origin: "https://anas.example.test",
	}); !errors.Is(err, ErrOwnerNotConfigured) {
		t.Fatalf("owner survived timed-out unpublished transition: %v", err)
	}
}

func TestCompleteInitialOwnerTransitionFailureRestoresAuthenticationSnapshot(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStoreWithState(t, directory, &memoryAudit{}, newTestClock(), func(context.Context) (ConsoleState, error) {
		return StateEnrollment, nil
	})
	session := enrollmentSessionForOwner(t, store, "txn-rollback")
	wantErr := errors.New("state transition fixture failed")
	err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Password: "correct horse battery staple",
	}, func(context.Context) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("complete error = %v", err)
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); err != nil {
		t.Fatalf("enrollment session was not restored: %v", err)
	}
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "correct horse battery staple", Origin: "https://anas.example.test",
	}); !errors.Is(err, ErrOwnerNotConfigured) {
		t.Fatalf("owner was partially committed: %v", err)
	}
	if _, found, err := store.loadOwnerCommitJournal(); err != nil || found {
		t.Fatalf("journal after rollback: found=%t err=%v", found, err)
	}
}

func TestCompleteInitialOwnerPublishedFullThenTransitionErrorKeepsOwner(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	capabilityState := StateEnrollment
	store := openTestStoreWithState(t, directory, &memoryAudit{}, newTestClock(), func(context.Context) (ConsoleState, error) {
		return capabilityState, nil
	})
	session := enrollmentSessionForOwner(t, store, "txn-published-full")
	postPublishErr := errors.New("directory sync failed after state rename")
	err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Password: "correct horse battery staple",
	}, func(context.Context) error {
		capabilityState = StateFull
		return postPublishErr
	})
	if err != nil {
		t.Fatalf("published owner enrollment returned error: %v", err)
	}
	if _, found, err := store.loadOwnerCommitJournal(); err != nil || found {
		t.Fatalf("journal after reconciled full publish: found=%t err=%v", found, err)
	}
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "correct horse battery staple", Origin: "https://anas.example.test",
	}); err != nil {
		t.Fatalf("published owner was rolled back: %v", err)
	}
}

func TestCompleteInitialOwnerUncertainTransitionErrorKeepsWAL(t *testing.T) {
	stateReadErr := errors.New("capability state unavailable")
	for _, test := range []struct {
		name     string
		provider func(context.Context) (ConsoleState, error)
	}{
		{name: "no provider"},
		{name: "read failure", provider: func(context.Context) (ConsoleState, error) {
			return "", stateReadErr
		}},
		{name: "unexpected bootstrap", provider: func(context.Context) (ConsoleState, error) {
			return StateBootstrap, nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "auth")
			store := openTestStoreWithState(t, directory, &memoryAudit{}, newTestClock(), test.provider)
			session := enrollmentSessionForOwner(t, store, "txn-owner-uncertain")
			transitionErr := errors.New("ambiguous transition failure")
			err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Password: "correct horse battery staple",
			}, func(context.Context) error { return transitionErr })
			if !errors.Is(err, transitionErr) || !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("complete error = %v", err)
			}
			if test.name == "read failure" && !errors.Is(err, stateReadErr) {
				t.Fatalf("complete error does not retain state read failure: %v", err)
			}
			if _, found, err := store.loadOwnerCommitJournal(); err != nil || !found {
				t.Fatalf("uncertain transaction WAL: found=%t err=%v", found, err)
			}
			if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
				Password: "correct horse battery staple", Origin: "https://anas.example.test",
			}); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("normal API did not fail closed: %v", err)
			}
			if err := store.RecoverInitialOwnerCommit(context.Background(), func(context.Context) (ConsoleState, error) {
				return StateFull, nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
				Password: "correct horse battery staple", Origin: "https://anas.example.test",
			}); err != nil {
				t.Fatalf("owner was not recovered forward: %v", err)
			}
		})
	}
}

func TestCompleteInitialOwnerRollbackAuditFailureKeepsWAL(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	store := openTestStoreWithState(t, directory, audit, newTestClock(), func(context.Context) (ConsoleState, error) {
		return StateEnrollment, nil
	})
	session := enrollmentSessionForOwner(t, store, "txn-owner-rollback-audit")
	transitionErr := errors.New("state transition failed")
	err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Password: "correct horse battery staple",
	}, func(context.Context) error {
		audit.FailNext()
		return transitionErr
	})
	if !errors.Is(err, transitionErr) || !errors.Is(err, ErrAuditUnavailable) || !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("complete error = %v", err)
	}
	if _, found, err := store.loadOwnerCommitJournal(); err != nil || !found {
		t.Fatalf("WAL after rollback audit failure: found=%t err=%v", found, err)
	}
	if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "correct horse battery staple", Origin: "https://anas.example.test",
	}); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("normal API did not fail closed: %v", err)
	}
	if err := store.RecoverInitialOwnerCommit(context.Background(), func(context.Context) (ConsoleState, error) {
		return StateEnrollment, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); err != nil {
		t.Fatalf("enrollment session was not restored: %v", err)
	}
}

func TestCompleteInitialOwnerFailureInjectionNeverPublishesPartialAuthentication(t *testing.T) {
	for _, failStep := range []string{"journal_prepared", "authentication_written", "before_state_transition"} {
		t.Run(failStep, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
			session := enrollmentSessionForOwner(t, store, "txn-injected")
			injected := errors.New("injected " + failStep)
			store.beforeOwnerCommitStep = func(step string) error {
				if step == failStep {
					return injected
				}
				return nil
			}
			transitionCalled := false
			err := store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Password: "correct horse battery staple",
			}, func(context.Context) error {
				transitionCalled = true
				return nil
			})
			if !errors.Is(err, injected) {
				t.Fatalf("complete error = %v", err)
			}
			if failStep != "before_state_transition" && transitionCalled {
				t.Fatal("state transition ran before injected failure")
			}
			store.beforeOwnerCommitStep = nil
			if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
			}); err != nil {
				t.Fatalf("enrollment state not restored: %v", err)
			}
		})
	}
}

// CONSOLE-R-082 and CONSOLE-R-091 require the first-owner commit and the
// enrollment -> full publish to remain a single serialized transition even
// when two daemon/store views race with the same still-valid enrollment
// credential. Exactly one caller may publish; the loser must observe the owner
// installed by the winner instead of invoking a second transition callback.
func TestCompleteInitialOwnerSerializesConcurrentCommits(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	audit := &memoryAudit{}
	clock := newTestClock()
	first := openTestStore(t, directory, audit, clock)
	second := openTestStore(t, directory, audit, clock)
	session := enrollmentSessionForOwner(t, first, "txn-concurrent-owner")
	stores := []*Store{first, second}
	start := make(chan struct{})
	results := make(chan error, len(stores))
	var callbacks atomic.Int32
	for _, store := range stores {
		go func(store *Store) {
			<-start
			results <- store.CompleteInitialOwner(context.Background(), CompleteInitialOwnerRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Password: "correct horse battery staple",
			}, func(context.Context) error {
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
		} else if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("concurrent owner commit error = %v", err)
		}
	}
	if successes != 1 || callbacks.Load() != 1 {
		t.Fatalf("successes=%d transition callbacks=%d", successes, callbacks.Load())
	}
	if _, err := first.LoginLocal(context.Background(), LocalLoginRequest{
		Password: "correct horse battery staple", Origin: session.Origin,
	}); err != nil {
		t.Fatalf("winning owner credential is unusable: %v", err)
	}
	if _, err := second.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); !errors.Is(err, ErrSessionUnauthorized) {
		t.Fatalf("losing store view retained enrollment credentials: %v", err)
	}
}

func TestRecoverInitialOwnerCommitChoosesSnapshotFromCapabilityState(t *testing.T) {
	for _, state := range []ConsoleState{StateBootstrap, StateEnrollment, StateFull} {
		t.Run(string(state), func(t *testing.T) {
			store := openTestStore(t, filepath.Join(t.TempDir(), "auth"), &memoryAudit{}, newTestClock())
			session := enrollmentSessionForOwner(t, store, "txn-recovery")
			previous, err := store.loadAuthenticationSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			phc, err := store.hashOwnerPassword("correct horse battery staple")
			if err != nil {
				t.Fatal(err)
			}
			next := previous
			next.Bootstrap, next.BootstrapExisted = newBootstrapState(), true
			next.Local, next.LocalExisted = newLocalState(), true
			next.Local.OwnerPasswordPHC = phc
			journal := ownerCommitJournal{
				APIVersion: ownerCommitAPIVersion, Phase: ownerCommitAuthReady,
				TransactionID: "txn-recovery", Previous: previous, Next: next,
			}
			if err := store.writeOwnerCommitJournal(journal); err != nil {
				t.Fatal(err)
			}
			// Simulate a crash after only part of the authentication snapshot
			// reached disk. Normal APIs must fail closed until recovery.
			if err := store.writeBootstrapState(next.Bootstrap); err != nil {
				t.Fatal(err)
			}
			if _, err := store.LoginLocal(context.Background(), LocalLoginRequest{
				Password: "correct horse battery staple", Origin: "https://anas.example.test",
			}); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatalf("API during recovery error = %v", err)
			}
			if err := store.RecoverInitialOwnerCommit(context.Background(), func(context.Context) (ConsoleState, error) {
				return state, nil
			}); err != nil {
				t.Fatal(err)
			}

			_, enrollmentErr := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
			})
			_, loginErr := store.LoginLocal(context.Background(), LocalLoginRequest{
				Password: "correct horse battery staple", Origin: "https://anas.example.test",
			})
			if state != StateFull {
				if enrollmentErr != nil || !errors.Is(loginErr, ErrOwnerNotConfigured) {
					t.Fatalf("rollback results: enrollment=%v login=%v", enrollmentErr, loginErr)
				}
			} else {
				if !errors.Is(enrollmentErr, ErrSessionUnauthorized) || loginErr != nil {
					t.Fatalf("roll-forward results: enrollment=%v login=%v", enrollmentErr, loginErr)
				}
			}
		})
	}
}

func TestRecoverInitialOwnerCommitRejectsMissingNextFileFlags(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	session := enrollmentSessionForOwner(t, store, "txn-owner-flag-tamper")
	previous, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	phc, err := store.hashOwnerPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	next := previous
	next.Bootstrap, next.BootstrapExisted = newBootstrapState(), true
	next.Local, next.LocalExisted = newLocalState(), false
	next.Local.OwnerPasswordPHC = phc
	journal := ownerCommitJournal{
		APIVersion: ownerCommitAPIVersion, Phase: ownerCommitAuthReady,
		TransactionID: session.TransactionID, Previous: previous, Next: next,
	}
	body, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitPrivateFileBytes(filepath.Join(directory, ownerCommitFileName), append(body, '\n')); err != nil {
		t.Fatal(err)
	}
	providerCalled := false
	err = store.RecoverInitialOwnerCommit(context.Background(), func(context.Context) (ConsoleState, error) {
		providerCalled = true
		return StateFull, nil
	})
	if err == nil {
		t.Fatal("owner WAL with missing next local file flag was accepted")
	}
	if providerCalled {
		t.Fatal("capability provider ran before WAL semantic validation")
	}
	if _, err := store.AuthenticateEnrollment(context.Background(), EnrollmentAuthenticationRequest{
		SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
		TransactionID: session.TransactionID, Route: EnrollmentOwnerRoute, RequireCSRF: true,
	}); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("store did not fail closed after owner WAL tampering: %v", err)
	}
}
