package consoleauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)}
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

type memoryAudit struct {
	mu       sync.Mutex
	events   []AuditEvent
	failNext bool
}

func (audit *memoryAudit) Record(_ context.Context, event AuditEvent) error {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if audit.failNext {
		audit.failNext = false
		return errors.New("audit fixture failure")
	}
	audit.events = append(audit.events, event)
	return nil
}

func (audit *memoryAudit) FailNext() {
	audit.mu.Lock()
	audit.failNext = true
	audit.mu.Unlock()
}

func (audit *memoryAudit) Events() []AuditEvent {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	return append([]AuditEvent{}, audit.events...)
}

func openTestStore(t *testing.T, directory string, audit AuditSink, clock *testClock) *Store {
	return openTestStoreWithState(t, directory, audit, clock, nil)
}

func openTestStoreWithState(t *testing.T, directory string, audit AuditSink, clock *testClock, currentState func(context.Context) (ConsoleState, error)) *Store {
	t.Helper()
	store, err := Open(directory, audit, StoreOptions{Now: clock.Now, CurrentState: currentState})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func issueTestBootstrap(t *testing.T, store *Store, transactionID string) IssuedBootstrapToken {
	t.Helper()
	issued, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
		TransactionID: transactionID,
		State:         StateBootstrap,
		AllowedRoutes: []string{"/api/v1/bootstrap/status", "/api/v1/bootstrap/apply"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func exchangeTestBootstrap(t *testing.T, store *Store, token string) BootstrapSessionCredential {
	t.Helper()
	credential, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{
		Token: token, Origin: "http://NAS.Example.Test:80/",
	})
	if err != nil {
		t.Fatal(err)
	}
	return credential
}
