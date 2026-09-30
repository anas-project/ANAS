package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func installedRelease() ReleaseIdentity {
	return ReleaseIdentity{Version: "0.1.1", Commit: strings.Repeat("a", 40)}
}
func installedPolicy() installationPolicy {
	return installationPolicy{Schema: installationSchema, Release: installedRelease()}
}

func TestInstallationPolicyRejectsAmbiguityAndVersionDrift(t *testing.T) {
	body, err := json.Marshal(installedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeInstallation(body, installedRelease()); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"duplicate":              strings.Replace(string(body), `"schema":`, `"schema":"anas.host-action-installation/v3","schema":`, 1),
		"escaped duplicate":      strings.Replace(string(body), `"schema":`, `"sch\u0065ma":"anas.host-action-installation/v3","schema":`, 1),
		"unknown path":           strings.Replace(string(body), `"schema":`, `"path":"private-marker","schema":`, 1),
		"case alias":             strings.Replace(string(body), `"release"`, `"Release"`, 1),
		"null":                   strings.Replace(string(body), `"schema":"anas.host-action-installation/v3"`, `"schema":null`, 1),
		"old service identity":   strings.Replace(string(body), `"schema":`, `"service_unit":"anasd.service","schema":`, 1),
		"old schema":             strings.Replace(string(body), `installation/v3`, `installation/v2`, 1),
		"trailing":               string(body) + `{}`,
		"oversize":               string(body) + strings.Repeat(" ", maxInstallationBytes),
		"missing release commit": strings.Replace(string(body), `,"commit":"`+strings.Repeat("a", 40)+`"`, ``, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeInstallation([]byte(raw), installedRelease()); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("invalid policy accepted or reflected", err)
			}
		})
	}
	for _, release := range []ReleaseIdentity{{Version: "dev", Commit: "unknown"}, {Version: "0.1.2", Commit: strings.Repeat("a", 40)}, {Version: "0.1.1", Commit: strings.Repeat("b", 40)}} {
		if _, err := decodeInstallation(body, release); err == nil {
			t.Fatal("release mismatch accepted")
		}
	}
}

func TestActivationEnvironmentIsExactAndNotAuthorization(t *testing.T) {
	valid := map[string]string{"LISTEN_PID": "123", "LISTEN_FDS": "1", "LISTEN_FDNAMES": "connection"}
	if checkActivationEnvironment(123, func(k string) string { return valid[k] }) != nil {
		t.Fatal("valid activation rejected")
	}
	for key, values := range map[string][]string{"LISTEN_PID": {"", "124", "0123", "+123"}, "LISTEN_FDS": {"", "0", "2", "01"}, "LISTEN_FDNAMES": {"", "listener", "connection:other"}} {
		for _, value := range values {
			if err := checkActivationEnvironment(123, func(k string) string {
				if k == key {
					return value
				}
				return valid[k]
			}); err == nil {
				t.Fatal("accepted malformed activation", key, value)
			}
		}
	}
}

// memoryLedger is an in-memory InvocationLedger for admission tests.
type memoryLedger struct {
	mu       sync.Mutex
	begun    map[string]bool
	finished map[string]actionabi.Event
	beginErr error
	onBegin  func()
}

func newMemoryLedger() *memoryLedger {
	return &memoryLedger{begun: map[string]bool{}, finished: map[string]actionabi.Event{}}
}

type memoryEntry struct {
	ledger *memoryLedger
	id     string
}

func (l *memoryLedger) Begin(_ context.Context, r actionabi.Request, _ ReleaseIdentity, _ PeerIdentity) (InvocationEntry, error) {
	if l.onBegin != nil {
		l.onBegin()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.beginErr != nil {
		return nil, l.beginErr
	}
	if l.begun[r.InvocationID] {
		return nil, ErrDenied
	}
	l.begun[r.InvocationID] = true
	return memoryEntry{l, r.InvocationID}, nil
}

func (e memoryEntry) Finish(terminal actionabi.Event) error {
	e.ledger.mu.Lock()
	defer e.ledger.mu.Unlock()
	if _, done := e.ledger.finished[e.id]; done {
		return ErrUnavailable
	}
	e.ledger.finished[e.id] = terminal
	return nil
}

func (l *memoryLedger) Status(_ context.Context, jobID, invocationID string) (InvocationStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	status := InvocationStatus{Schema: InvocationStatusSchema, State: InvocationAbsent}
	if terminal, ok := l.finished[invocationID]; ok {
		status.State, status.Terminal = InvocationFinished, &terminal
	} else if l.begun[invocationID] {
		status.State = InvocationRunning
	}
	return status, nil
}

func TestRecordedExecutionRefusesReplayAndGuardChanges(t *testing.T) {
	for _, test := range []string{"missing ledger", "replayed", "ledger unavailable", "changed after begin"} {
		t.Run(test, func(t *testing.T) {
			journal := &memoryAudit{}
			ledger := newMemoryLedger()
			var target InvocationLedger = ledger
			changed := false
			switch test {
			case "missing ledger":
				target = nil
			case "replayed":
				ledger.begun[testRequest().InvocationID] = true
			case "ledger unavailable":
				ledger.beginErr = errors.New("private-marker")
			case "changed after begin":
				ledger.onBegin = func() { changed = true }
			}
			event, err := executeRecorded(context.Background(), testCall(t), journal, target, installedRelease(), func() error {
				if changed {
					return ErrUnavailable
				}
				return nil
			}, func(actionabi.Event) error { return nil })
			if err == nil || event.Type != "" || strings.Contains(err.Error(), "private-marker") {
				t.Fatal(event, err)
			}
			for _, e := range journal.events {
				if e.Type == "host_action_started" {
					t.Fatal("executed without an invocation record")
				}
			}
			if test == "changed after begin" {
				terminal, ok := ledger.finished[testRequest().InvocationID]
				if !ok || terminal.Error == nil || terminal.Error.Code != "host_action_not_started" || terminal.Error.Outcome != actionabi.Failed {
					t.Fatal("aborted invocation left no not-started record", terminal)
				}
			}
		})
	}
}

func TestRecordedExecutionRecordsTheReturnedTerminal(t *testing.T) {
	journal := &memoryAudit{}
	ledger := newMemoryLedger()
	event, err := executeRecorded(context.Background(), testCall(t), journal, ledger, installedRelease(), func() error { return nil },
		func(actionabi.Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	recorded, ok := ledger.finished[testRequest().InvocationID]
	if !ok || recorded.Type != event.Type || recorded.JobID != event.JobID || recorded.InvocationID != event.InvocationID {
		t.Fatal("ledger terminal differs from the sent terminal", recorded, event)
	}
	status, err := ledger.Status(context.Background(), event.JobID, event.InvocationID)
	if err != nil || status.State != InvocationFinished {
		t.Fatal(status, err)
	}
	if _, err := executeRecorded(context.Background(), testCall(t), &memoryAudit{}, ledger, installedRelease(), func() error { return nil },
		func(actionabi.Event) error { return nil }); err == nil {
		t.Fatal("replayed invocation ran twice")
	}
}

func TestAdmissionAuditNeverClaimsUnparsedIdentifiers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	journal := &memoryAudit{}
	err := rejectAdmission(ctx, journal, testPeer(), "invalid_request", ErrRequest)
	if !errors.Is(err, ErrRequest) || len(journal.events) != 1 || journal.contexts[0] != nil {
		t.Fatal(err, journal.events)
	}
	e := journal.events[0]
	if e.Details["job_id"] != nil || e.Details["action"] != nil || e.Details["parameters"] != nil {
		t.Fatal("unauthenticated input was attributed as a real action")
	}
	if err := rejectAdmission(ctx, &memoryAudit{failAt: 1}, testPeer(), "peer_denied", ErrDenied); !errors.Is(err, ErrAudit) {
		t.Fatal(err)
	}
}

type cleanupFailureGuard struct{ closed bool }

func (*cleanupFailureGuard) check() error   { return nil }
func (g *cleanupFailureGuard) close() error { g.closed = true; return ErrUnavailable }

func TestActivationReturnsDescriptorCleanupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	guard := &cleanupFailureGuard{}
	// No socket is opened: the cancelled owner must short-circuit before
	// credential reads. A zero UnixConn safely returns an error from Close.
	a := &Activation{connection: &net.UnixConn{}, guard: guard}
	ledger := newMemoryLedger()
	ledger.onBegin = func() { t.Fatal("cancelled activation reached its invocation ledger") }
	if err := a.Serve(ctx, &memoryAudit{}, ledger); !errors.Is(err, ErrUnavailable) {
		t.Fatal("resource cleanup failure was lost behind the early return", err)
	}
	if !guard.closed || !a.closed {
		t.Fatal("activation resources were not retired")
	}
	if err := a.Close(); err != nil {
		t.Fatal("repeated close was not inert", err)
	}
}
