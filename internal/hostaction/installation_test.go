package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func installedRelease() ReleaseIdentity {
	return ReleaseIdentity{Version: "0.1.1", Commit: strings.Repeat("a", 40)}
}
func installedPolicy() installationPolicy {
	return installationPolicy{Schema: installationSchema, Release: installedRelease(), ServiceMode: serviceModeSystemdRoot, ServiceUnit: "anasd.service", SocketGID: 0}
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
		"duplicate":               strings.Replace(string(body), `"service_mode":"systemd-root-service"`, `"service_mode":"systemd-root-service","service_mode":"systemd-root-service"`, 1),
		"escaped duplicate":       strings.Replace(string(body), `"service_unit":"anasd.service"`, `"service_unit":"anasd.service","service_\u0075nit":"anasd.service"`, 1),
		"unknown path":            strings.Replace(string(body), `"schema":`, `"path":"private-marker","schema":`, 1),
		"case alias":              strings.Replace(string(body), `"service_mode"`, `"Service_Mode"`, 1),
		"null":                    strings.Replace(string(body), `"socket_gid":0`, `"socket_gid":null`, 1),
		"implicit old root uid":   strings.Replace(string(body), `"service_mode":"systemd-root-service"`, `"service_uid":0`, 1),
		"invalid socket sentinel": strings.Replace(string(body), `"socket_gid":0`, `"socket_gid":4294967295`, 1),
		"wrong mode":              strings.Replace(string(body), `"systemd-root-service"`, `"root"`, 1),
		"invalid unit":            strings.Replace(string(body), `"anasd.service"`, `"../anasd.service"`, 1),
		"trailing":                string(body) + `{}`,
		"oversize":                string(body) + strings.Repeat(" ", maxInstallationBytes),
		"missing field":           strings.Replace(string(body), `,"socket_gid":0`, ``, 1),
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

type bindingFixture func(context.Context, actionabi.Request, ReleaseIdentity, PeerIdentity, func(context.Context) error) error

func (f bindingFixture) WithHostInvocation(ctx context.Context, r actionabi.Request, release ReleaseIdentity, peer PeerIdentity, run func(context.Context) error) error {
	return f(ctx, r, release, peer, run)
}

func TestBoundExecutionRejectsUnregisteredJobsAndGuardChanges(t *testing.T) {
	for _, test := range []string{"missing binding", "denied", "not invoked", "changed before callback"} {
		t.Run(test, func(t *testing.T) {
			journal := &memoryAudit{}
			changed := false
			var binding JobBinding = bindingFixture(func(ctx context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
				if test == "denied" {
					return errors.New("private-marker")
				}
				if test == "not invoked" {
					return nil
				}
				changed = true
				return run(ctx)
			})
			if test == "missing binding" {
				binding = nil
			}
			event, err := executeBound(context.Background(), testCall(t), journal, binding, installedRelease(), func() error {
				if changed {
					return ErrUnavailable
				}
				return nil
			})
			if err == nil || event.Type != "" || strings.Contains(err.Error(), "private-marker") {
				t.Fatal(event, err)
			}
			for _, e := range journal.events {
				if e.Type == "host_action_started" {
					t.Fatal("executed without job binding")
				}
			}
		})
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

func TestJobBindingCannotExecuteAfterReturning(t *testing.T) {
	var delayed func(context.Context) error
	journal := &memoryAudit{}
	call := testCall(t)
	binding := bindingFixture(func(_ context.Context, _ actionabi.Request, _ ReleaseIdentity, _ PeerIdentity, run func(context.Context) error) error {
		delayed = run
		return nil
	})
	if _, err := executeBound(context.Background(), call, journal, binding, installedRelease(), func() error { return nil }); err == nil {
		t.Fatal("absent synchronous authorization accepted")
	}
	if delayed == nil {
		t.Fatal("missing delayed fixture")
	}
	if err := delayed(context.Background()); !errors.Is(err, ErrDenied) {
		t.Fatal("late callback executed", err)
	}
	if call.used.Load() || len(journal.events) != 1 || journal.events[0].Type != "host_action_rejected" {
		t.Fatal("handler outlived authorization")
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
	binding := bindingFixture(func(context.Context, actionabi.Request, ReleaseIdentity, PeerIdentity, func(context.Context) error) error {
		t.Fatal("cancelled activation reached its job binding")
		return nil
	})
	if err := a.Serve(ctx, &memoryAudit{}, binding); !errors.Is(err, ErrUnavailable) {
		t.Fatal("resource cleanup failure was lost behind the early return", err)
	}
	if !guard.closed || !a.closed {
		t.Fatal("activation resources were not retired")
	}
	if err := a.Close(); err != nil {
		t.Fatal("repeated close was not inert", err)
	}
}
