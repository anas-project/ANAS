package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
)

const hostInvokePath = "/api/v1/workspaces/main/host/actions/incus.status"

func hostRouteFixture(t *testing.T, state ConsoleState, listener ListenerIdentity, enabled bool, authorize func(*http.Request, AuthorizationRequest) (Principal, error), invoke func(context.Context, string, string, string) (consolejobs.CreateResult, error)) (http.Handler, *consolejobs.Store) {
	t.Helper()
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	if authorize == nil {
		authorize = func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: "local-owner", Role: "owner", Source: "local"}, nil
		}
	}
	opts := Options{Registry: registry, Jobs: &JobQueryOptions{Store: store}, Security: SecurityOptions{InitialState: state, Listener: listener, HostAllowed: func(*http.Request) bool { return true }, Authorize: authorize}}
	if enabled {
		opts.HostActions = &HostActionOptions{
			InvokePreflight: invoke,
			InvokePlan: func(context.Context, string, string, string, json.RawMessage, string) (consolejobs.CreateResult, error) {
				return consolejobs.CreateResult{}, hostaction.ErrRequest
			},
			IssueConfirmation: func(context.Context, string, string, string, string) (hostconfirmation.IssueResult, error) {
				return hostconfirmation.IssueResult{}, hostaction.ErrRequest
			},
			InvokeConfirmed: func(context.Context, string, string, string, string, json.RawMessage, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error) {
				return consolejobs.CreateResult{}, hostaction.ErrRequest
			},
		}
	}
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return h, store
}
func hostRouteRequest(h http.Handler, method, path, body, key string, tls bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	if tls {
		r.TLS = &tlsStateFixture
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var tlsStateFixture = tls.ConnectionState{}

func TestHostRouteUsesDurableQueueWithoutPrivateResponse(t *testing.T) {
	var store *consolejobs.Store
	var called int
	h, storeValue := hostRouteFixture(t, StateFull, ListenerDirect, true, nil, func(ctx context.Context, actor, workspace, key string) (consolejobs.CreateResult, error) {
		called++
		if actor != "local-owner" || workspace != "main" {
			t.Error("lost authenticated principal")
		}
		return store.CreateActionWithPolicyObserved(ctx, consolejobs.CreateSpec{WorkspaceID: workspace, Request: map[string]any{"private_marker": "must-not-be-in-response"}, Idempotency: consolejobs.IdempotencyInput{Principal: actor, Method: "POST", CanonicalPath: "/host", Key: key}}, "incus.status", "call-http", consolejobs.ActionCoalesce, consolejobs.JobCommitObserverFunc(func(context.Context, consolejobs.JobCommitIntent) error { return nil }))
	})
	store = storeValue
	first := hostRouteRequest(h, http.MethodPost, hostInvokePath, "{ \n }", "http-key", true)
	if first.Code != 202 || !strings.HasPrefix(first.Header().Get("Location"), "/api/v1/jobs/") {
		t.Fatal(first.Code, first.Body.String())
	}
	if strings.Contains(first.Body.String(), "must-not-be-in-response") || strings.Contains(first.Body.String(), "local-owner") {
		t.Fatal("private request/actor leaked")
	}
	second := hostRouteRequest(h, http.MethodPost, hostInvokePath, "{}", "http-key", true)
	jobs, err := store.List(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].Status != consolejobs.StatusQueued || called != 2 || second.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatal("HTTP executed or duplicated a job", err)
	}
}

func TestHostRouteRejectsPayloadAndUnregisteredWorkspace(t *testing.T) {
	called := 0
	h, _ := hostRouteFixture(t, StateFull, ListenerDirect, true, nil, func(context.Context, string, string, string) (consolejobs.CreateResult, error) {
		called++
		return consolejobs.CreateResult{}, nil
	})
	for _, body := range []string{"", "null", "[]", "{}{}", `{"uid":0}`, `{"command":"private-marker"}`, `{"parameters":{}}`, `{"Password":"private-marker"}`} {
		w := hostRouteRequest(h, http.MethodPost, hostInvokePath, body, "key", true)
		if w.Code != 400 || strings.Contains(w.Body.String(), "private-marker") {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if w := hostRouteRequest(h, http.MethodPost, hostInvokePath, strings.Repeat(" ", 4097), "key", true); w.Code != 413 {
		t.Fatal(w.Code)
	}
	if w := hostRouteRequest(h, http.MethodPost, hostInvokePath, "{}", "", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := hostRouteRequest(h, http.MethodPost, strings.Replace(hostInvokePath, "/main/", "/absent/", 1), "{}", "key", true); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := hostRouteRequest(h, http.MethodPost, hostInvokePath+"?path=private-marker", "{}", "key", true); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if called != 0 {
		t.Fatal("invalid request reached queue")
	}
}

func TestHostRouteIsHiddenWithoutCapabilityAndEquivalentOnBothListeners(t *testing.T) {
	for _, listener := range []ListenerIdentity{ListenerDirect, ListenerTrustedProxy} {
		for _, state := range []ConsoleState{StateM0, StateBootstrap, StateEnrollment, StateFull} {
			for _, enabled := range []bool{false, true} {
				calls := 0
				h, _ := hostRouteFixture(t, state, listener, enabled, nil, func(context.Context, string, string, string) (consolejobs.CreateResult, error) {
					calls++
					return consolejobs.CreateResult{}, hostaction.ErrUnavailable
				})
				w := hostRouteRequest(h, http.MethodPost, hostInvokePath, "{}", "key", true)
				want := 404
				if enabled && state == StateFull {
					want = 503
				}
				if w.Code != want || (want == 404 && calls != 0) {
					t.Fatal(state, listener, enabled, w.Code)
				}
				plain := hostRouteRequest(h, http.MethodPost, hostInvokePath, "{}", "key", false)
				if plain.Code != 404 {
					t.Fatal("plaintext route visible")
				}
			}
		}
	}
}

func TestHostRouteUsesActualLocalAuthenticationAndCSRF(t *testing.T) {
	ctx := context.Background()
	auth, err := consoleauth.Open(filepath.Join(t.TempDir(), "auth"), consoleauth.AuditSinkFunc(func(context.Context, consoleauth.AuditEvent) error { return nil }), consoleauth.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = auth.SetOwnerPassword(ctx, "http-owner-password-1234"); err != nil {
		t.Fatal(err)
	}
	login, err := auth.LoginLocal(ctx, consoleauth.LocalLoginRequest{Password: "http-owner-password-1234", Origin: "https://nas.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	h, _ := hostRouteFixture(t, StateFull, ListenerDirect, true, DirectSessionAuthorizer(auth), func(context.Context, string, string, string) (consolejobs.CreateResult, error) {
		called++
		return consolejobs.CreateResult{}, hostaction.ErrUnavailable
	})
	for _, tc := range []struct {
		cookie, csrf, origin string
		want                 int
	}{
		{"", "", "", 401}, {login.Token, "", "https://nas.example.test", 403}, {login.Token, login.CSRFToken, "https://evil.example.test", 403}, {login.Token, login.CSRFToken, "https://nas.example.test", 503},
	} {
		r := httptest.NewRequest(http.MethodPost, "https://nas.example.test"+hostInvokePath, strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "key")
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: consoleauth.LocalSessionCookieName, Value: tc.cookie})
		}
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-CSRF-Token", tc.csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want, w.Body.String())
		}
	}
	if called != 1 {
		t.Fatal("unauthenticated request reached queue")
	}
}

func TestHostRouteDenialAndFailuresNeverEchoInternals(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{hostaction.ErrDenied, 403}, {hostaction.ErrAudit, 503}, {consolejobs.ErrConflict, 409}, {errors.New("private-socket-path"), 500}} {
		h, _ := hostRouteFixture(t, StateFull, ListenerTrustedProxy, true, nil, func(context.Context, string, string, string) (consolejobs.CreateResult, error) {
			return consolejobs.CreateResult{}, tc.err
		})
		w := hostRouteRequest(h, http.MethodPost, hostInvokePath, "{}", "key", true)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private-socket-path") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
