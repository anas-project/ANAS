package hostconfirmation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
)

type memoryAudit struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (m *memoryAudit) AppendContext(_ context.Context, event audit.Event) (audit.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return audit.Event{}, m.err
	}
	event.Sequence = uint64(len(m.events) + 1)
	m.events = append(m.events, event)
	return event, nil
}

func (m *memoryAudit) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func testBinding(t *testing.T, plannedAt time.Time) actionabi.ConfirmationBinding {
	t.Helper()
	return actionabi.ConfirmationBinding{
		ABI: actionabi.Version, PlanJobID: "plan-1", PlanInvocationID: "plan-call-1", Action: "incus.uninstall",
		Actor: "local-owner", WorkspaceID: "main", ParametersDigest: strings.Repeat("a", 64),
		StateDigest: strings.Repeat("b", 64), SummaryDigest: strings.Repeat("c", 64),
		ReleaseDigest: strings.Repeat("d", 64), PlannedAt: plannedAt.UTC(),
	}
}

func TestIssueConsumeClaimRestartReplayAndConcurrency(t *testing.T) {
	ctx := context.Background()
	aud := &memoryAudit{}
	dir := confirmationDir(t)
	now := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	store, err := openForTest(ctx, dir, aud)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	binding := testBinding(t, now)
	issued, err := store.Issue(ctx, IssueRequest{Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token.Value() == "" || strings.Contains(fmt.Sprintf("%#v", issued), issued.Token.Value()) || strings.Contains(fmt.Sprintf("%v", issued.Token), issued.Token.Value()) {
		t.Fatal("raw token leaked through formatting")
	}
	consume := func() error {
		_, err := store.Consume(ctx, ConsumeRequest{Token: issued.Token, Binding: binding, ApplyJobID: "apply-1", InvocationID: "apply-call-1"})
		return err
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- consume()
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrConsumed) {
			t.Fatalf("concurrent consume error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("consume successes = %d, want 1", successes)
	}
	body, err := os.ReadFile(filepath.Join(dir, journalFilename))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), issued.Token.Value()) {
		t.Fatal("raw token persisted in confirmation ledger")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openForTest(ctx, dir, aud)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = func() time.Time { return now.Add(time.Minute) }
	digest, _ := binding.Digest()
	if _, err := reopened.Consume(ctx, ConsumeRequest{Token: issued.Token, Binding: binding, ApplyJobID: "apply-2", InvocationID: "apply-call-2"}); !errors.Is(err, ErrConsumed) {
		t.Fatalf("restart replay consume error = %v, want consumed", err)
	}
	claimed, err := reopened.Claim(ctx, ClaimRequest{BindingDigest: digest, ApplyJobID: "apply-1", InvocationID: "apply-call-1"})
	if err != nil {
		t.Fatalf("claim after restart: %v", err)
	}
	if claimed.Action != binding.Action || claimed.Actor != binding.Actor || claimed.WorkspaceID != binding.WorkspaceID ||
		claimed.ParametersDigest != binding.ParametersDigest || claimed.StateDigest != binding.StateDigest ||
		claimed.SummaryDigest != binding.SummaryDigest || claimed.ReleaseDigest != binding.ReleaseDigest ||
		!claimed.PlannedAt.Equal(binding.PlannedAt) || !claimed.ExpiresAt.Equal(binding.ExpiresAt()) {
		t.Fatalf("claim did not return trusted binding fields: %#v", claimed)
	}
	if _, err := reopened.Claim(ctx, ClaimRequest{BindingDigest: digest, ApplyJobID: "apply-1", InvocationID: "apply-call-1"}); !errors.Is(err, ErrConsumed) {
		t.Fatalf("second claim error = %v, want consumed", err)
	}
}

func TestExpiryBoundariesNoRenewalAndFutureTime(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 19, 2, 0, 0, 0, time.UTC)
	store, err := openForTest(ctx, confirmationDir(t), &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	binding := testBinding(t, now)
	issued, err := store.Issue(ctx, IssueRequest{Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Issue(ctx, IssueRequest{Binding: binding}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same plan reissue error = %v, want conflict", err)
	}
	store.now = func() time.Time { return binding.ExpiresAt() }
	if _, err := store.Issue(ctx, IssueRequest{Binding: binding}); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired same plan reissue error = %v, want expired", err)
	}
	store.now = func() time.Time { return binding.ExpiresAt().Add(-time.Nanosecond) }
	if _, err := store.Consume(ctx, ConsumeRequest{Token: issued.Token, Binding: binding, ApplyJobID: "apply", InvocationID: "call"}); err != nil {
		t.Fatalf("consume immediately before expiry: %v", err)
	}
	expiring, err := openForTest(ctx, confirmationDir(t), &memoryAudit{})
	if err != nil {
		t.Fatal(err)
	}
	expiring.now = func() time.Time { return now }
	lateBinding := testBinding(t, now)
	lateIssued, err := expiring.Issue(ctx, IssueRequest{Binding: lateBinding})
	if err != nil {
		t.Fatal(err)
	}
	expiring.now = func() time.Time { return lateBinding.ExpiresAt() }
	if _, err := expiring.Consume(ctx, ConsumeRequest{Token: lateIssued.Token, Binding: lateBinding, ApplyJobID: "apply", InvocationID: "call"}); !errors.Is(err, ErrExpired) {
		t.Fatalf("exact expiry error = %v, want expired", err)
	}
	future := testBinding(t, now.Add(time.Minute))
	expiring.now = func() time.Time { return now }
	if _, err := expiring.Issue(ctx, IssueRequest{Binding: future}); !errors.Is(err, ErrExpired) {
		t.Fatalf("future plan issue error = %v, want expired", err)
	}
}

func TestDriftMalformedFilesystemAuditAndPartialPersistenceFailClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	parent := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "linked-confirmations")
	if err := os.Symlink(parent, linkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := openForTest(ctx, linkDir, &memoryAudit{}); err == nil {
		t.Fatal("unexpectedly opened symlinked confirmation directory")
	}
	aud := &memoryAudit{}
	store, err := openForTest(ctx, confirmationDir(t), aud)
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	binding := testBinding(t, now)
	issued, err := store.Issue(ctx, IssueRequest{Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	drifted := binding
	drifted.ReleaseDigest = strings.Repeat("e", 64)
	if _, err := store.Consume(ctx, ConsumeRequest{Token: issued.Token, Binding: drifted, ApplyJobID: "apply", InvocationID: "call"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("release drift error = %v, want invalid", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(store.directory, journalFilename)
	if err := os.WriteFile(journal, []byte(`{"schema_version":1,"schema_version":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openForTest(ctx, store.directory, aud); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("malformed journal open error = %v, want unavailable", err)
	}

	badAudit := &memoryAudit{err: errors.New("audit disk full")}
	auditStore, err := openForTest(ctx, confirmationDir(t), badAudit)
	if err != nil {
		t.Fatal(err)
	}
	auditStore.now = func() time.Time { return now }
	if _, err := auditStore.Issue(ctx, IssueRequest{Binding: binding}); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("audit failure error = %v, want audit unavailable", err)
	}
	body, readErr := os.ReadFile(filepath.Join(auditStore.directory, journalFilename))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(body) != 0 {
		t.Fatal("audit failure changed confirmation ledger")
	}

	partial, err := openForTest(ctx, confirmationDir(t), aud)
	if err != nil {
		t.Fatal(err)
	}
	partial.now = func() time.Time { return now }
	partial.failAfterRecordWrite = true
	if _, err := partial.Issue(ctx, IssueRequest{Binding: binding}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("partial write error = %v, want unavailable", err)
	}
	partial.failAfterRecordWrite = false
	if _, err := partial.Issue(ctx, IssueRequest{Binding: binding}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("store resurrected after ambiguous persistence: %v", err)
	}
	if err := partial.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openForTest(ctx, partial.directory, aud)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = func() time.Time { return now }
	if _, err := reopened.Issue(ctx, IssueRequest{Binding: binding}); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous persisted receipt resurrected as reusable: %v", err)
	}
}

func confirmationDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "confirmations")
}
