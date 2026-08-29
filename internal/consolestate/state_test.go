package consolestate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenInitializesBootstrapAndRestartPreservesState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	audit := &recordingAudit{}
	store := openTestStore(t, directory, audit)
	if got, err := store.Current(context.Background()); err != nil || got != StateBootstrap {
		t.Fatalf("initial state = %q, %v", got, err)
	}
	assertPrivatePath(t, directory, 0o700)
	assertPrivatePath(t, filepath.Join(directory, StateFileName), 0o600)
	assertPrivatePath(t, filepath.Join(directory, LockFileName), 0o600)

	body, err := os.ReadFile(filepath.Join(directory, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(body, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 || persisted["api_version"] != APIVersion || persisted["state"] != string(StateBootstrap) {
		t.Fatalf("persisted state = %#v", persisted)
	}
	if len(audit.Events()) != 0 {
		t.Fatal("initial bootstrap state was incorrectly recorded as a transition")
	}

	restarted := openTestStore(t, directory, audit)
	if got, err := restarted.Current(context.Background()); err != nil || got != StateBootstrap {
		t.Fatalf("restarted state = %q, %v", got, err)
	}
}

func TestTransitionAllowsOnlyForwardSequenceWithFixedAudit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	audit := &recordingAudit{}
	store := openTestStore(t, directory, audit)

	assertInvalidTransition(t, store, StateFull, "certificate-monitor")
	assertInvalidTransition(t, store, StateBootstrap, "certificate-monitor")
	if got, err := store.Transition(context.Background(), StateEnrollment, "certificate-monitor"); err != nil || got != StateEnrollment {
		t.Fatalf("bootstrap -> enrollment = %q, %v", got, err)
	}
	assertInvalidTransition(t, store, StateEnrollment, "certificate-monitor")
	assertInvalidTransition(t, store, StateBootstrap, "certificate-monitor")
	if got, err := store.Transition(context.Background(), StateFull, "local-owner"); err != nil || got != StateFull {
		t.Fatalf("enrollment -> full = %q, %v", got, err)
	}
	for _, target := range []State{StateBootstrap, StateEnrollment, StateFull} {
		assertInvalidTransition(t, store, target, "local-owner")
	}
	if _, err := store.Transition(context.Background(), State("future"), "local-owner"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unknown target error = %v", err)
	}
	if _, err := store.Transition(context.Background(), StateFull, "credential with spaces"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid actor error = %v", err)
	}

	events := audit.Events()
	want := []TransitionEvent{
		{From: StateBootstrap, To: StateEnrollment, Actor: "certificate-monitor", Reason: ReasonBootstrapCompleted},
		{From: StateEnrollment, To: StateFull, Actor: "local-owner", Reason: ReasonOwnerEnrolled},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	restarted := openTestStore(t, directory, audit)
	if got, err := restarted.Current(context.Background()); err != nil || got != StateFull {
		t.Fatalf("restarted state = %q, %v", got, err)
	}

	eventType := reflect.TypeOf(TransitionEvent{})
	if eventType.NumField() != 4 {
		t.Fatalf("TransitionEvent has %d fields, want closed four-field shape", eventType.NumField())
	}
	for index := 0; index < eventType.NumField(); index++ {
		name := strings.ToLower(eventType.Field(index).Name)
		for _, credentialMarker := range []string{"password", "token", "session", "csrf", "credential", "secret"} {
			if strings.Contains(name, credentialMarker) {
				t.Fatalf("TransitionEvent exposes credential-shaped field %q", name)
			}
		}
	}
}

func TestAuditFailureDoesNotCommitTransition(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	audit := &recordingAudit{}
	store := openTestStore(t, directory, audit)
	want := errors.New("durable audit unavailable")
	audit.FailNext(want)
	got, err := store.Transition(context.Background(), StateEnrollment, "certificate-monitor")
	if got != StateBootstrap || !errors.Is(err, ErrAuditUnavailable) || !errors.Is(err, want) {
		t.Fatalf("failed transition = %q, %v", got, err)
	}
	if current, err := store.Current(context.Background()); err != nil || current != StateBootstrap {
		t.Fatalf("state after failed audit = %q, %v", current, err)
	}
	if current, err := openTestStore(t, directory, audit).Current(context.Background()); err != nil || current != StateBootstrap {
		t.Fatalf("restarted state after failed audit = %q, %v", current, err)
	}
}

func TestAuditSinkIsMandatory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	if _, err := Open(context.Background(), directory, nil); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("nil audit error = %v", err)
	}
	store, err := Open(context.Background(), directory, AuditSinkFunc(nil))
	if err != nil {
		// A typed sink is accepted at construction but must still fail closed on
		// the first transition, which is checked below.
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), StateEnrollment, "certificate-monitor"); !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("typed-nil audit error = %v", err)
	}
}

func TestConcurrentTransitionHasExactlyOneCommit(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	audit := &recordingAudit{}
	openTestStore(t, directory, audit)
	const attempts = 24
	stores := make([]*Store, attempts)
	for index := range stores {
		stores[index] = openTestStore(t, directory, audit)
	}
	start := make(chan struct{})
	var successes atomic.Int32
	var rejected atomic.Int32
	var wait sync.WaitGroup
	for _, store := range stores {
		wait.Add(1)
		go func(store *Store) {
			defer wait.Done()
			<-start
			_, err := store.Transition(context.Background(), StateEnrollment, "certificate-monitor")
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrInvalidTransition):
				rejected.Add(1)
			default:
				t.Errorf("transition error = %v", err)
			}
		}(store)
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || rejected.Load() != attempts-1 {
		t.Fatalf("successes = %d, rejected = %d", successes.Load(), rejected.Load())
	}
	if events := audit.Events(); len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if current, err := stores[0].Current(context.Background()); err != nil || current != StateEnrollment {
		t.Fatalf("concurrent result = %q, %v", current, err)
	}
}

func TestLockWaitHonorsContextCancellation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	audit := newBlockingAudit()
	first := openTestStore(t, directory, audit)
	second := openTestStore(t, directory, audit)
	done := make(chan error, 1)
	go func() {
		_, err := first.Transition(context.Background(), StateEnrollment, "certificate-monitor")
		done <- err
	}()
	select {
	case <-audit.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first transition did not reach the audit sink")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := second.Current(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Current error = %v", err)
	}
	close(audit.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first transition did not finish")
	}
}

func TestInterruptedCommitAndOrphanTemporaryKeepLastState(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "console")
	audit := &recordingAudit{}
	store := openTestStore(t, directory, audit)
	want := errors.New("simulated crash before rename")
	store.beforeRename = func(_, _ string) error { return want }
	if got, err := store.Transition(context.Background(), StateEnrollment, "certificate-monitor"); got != StateBootstrap || !errors.Is(err, ErrStateUnavailable) || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("interrupted transition = %q, %v", got, err)
	}
	store.beforeRename = nil
	orphanPath := filepath.Join(directory, "."+StateFileName+"-orphan.tmp")
	if err := os.WriteFile(orphanPath, []byte(`{"api_version":"`+APIVersion+`","state":"full"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := openTestStore(t, directory, audit)
	if current, err := restarted.Current(context.Background()); err != nil || current != StateBootstrap {
		t.Fatalf("state after interrupted commit = %q, %v", current, err)
	}
}

func TestCorruptMissingAndUnknownStateFailClosed(t *testing.T) {
	tests := []struct {
		name string
		body string
		omit bool
	}{
		{name: "missing", omit: true},
		{name: "corrupt", body: `{not-json`},
		{name: "unknown field", body: `{"api_version":"` + APIVersion + `","state":"bootstrap","future":true}`},
		{name: "unknown version", body: `{"api_version":"anas.console-state/v2","state":"bootstrap"}`},
		{name: "unknown state", body: `{"api_version":"` + APIVersion + `","state":"future"}`},
		{name: "multiple values", body: `{"api_version":"` + APIVersion + `","state":"bootstrap"} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "console")
			audit := &recordingAudit{}
			store := openTestStore(t, directory, audit)
			path := filepath.Join(directory, StateFileName)
			var err error
			if test.omit {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte(test.body), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Current(context.Background()); !errors.Is(err, ErrStateUnavailable) {
				t.Fatalf("Current error = %v", err)
			}
			if _, err := Open(context.Background(), directory, audit); !errors.Is(err, ErrStateUnavailable) {
				t.Fatalf("restart error = %v", err)
			}
		})
	}
}

func assertInvalidTransition(t *testing.T, store *Store, target State, actor string) {
	t.Helper()
	if _, err := store.Transition(context.Background(), target, actor); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("transition to %s error = %v", target, err)
	}
}

func openTestStore(t *testing.T, directory string, audit AuditSink) *Store {
	t.Helper()
	store, err := Open(context.Background(), directory, audit)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func assertPrivatePath(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, want %04o non-symlink", path, info.Mode(), mode)
	}
}

type recordingAudit struct {
	mu       sync.Mutex
	events   []TransitionEvent
	failNext error
}

func (audit *recordingAudit) RecordTransition(_ context.Context, event TransitionEvent) error {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if audit.failNext != nil {
		err := audit.failNext
		audit.failNext = nil
		return err
	}
	audit.events = append(audit.events, event)
	return nil
}

func (audit *recordingAudit) Events() []TransitionEvent {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	return append([]TransitionEvent{}, audit.events...)
}

func (audit *recordingAudit) FailNext(err error) {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	audit.failNext = err
}

type blockingAudit struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAudit() *blockingAudit {
	return &blockingAudit{entered: make(chan struct{}), release: make(chan struct{})}
}

func (audit *blockingAudit) RecordTransition(ctx context.Context, _ TransitionEvent) error {
	audit.once.Do(func() { close(audit.entered) })
	select {
	case <-audit.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
