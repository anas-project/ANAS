package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

type fakeApplicationCredentialService struct{ calls int }

func (s *fakeApplicationCredentialService) ReadApplicationCredential(_ context.Context, app string) (application.ApplicationCredential, error) {
	s.calls++
	return application.ApplicationCredential{Application: app, Username: "admin", Password: "backend-secret", URL: "https://backend.test"}, nil
}

func TestServiceCredentialRouteUsesIndependentGrantAndAuditsBeforeDisclosure(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	service := &fakeApplicationCredentialService{}
	var auditEvent ServiceCredentialAuditEvent
	auditFails := false
	options := Options{Registry: registry, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
		t.Fatal("owner authorizer used for service API")
		return Principal{}, nil
	}}, ServiceAPI: &ServiceAPIOptions{
		Authenticate: func(_ context.Context, token, permission, workspace, resource string) (string, error) {
			if token != "portal-token" {
				return "", serviceauth.ErrUnauthenticated
			}
			if permission != string(PermissionApplicationCredentialRead) || workspace != "main" || resource != "traefik-admin" {
				return "", serviceauth.ErrForbidden
			}
			return "user-portal", nil
		},
		Factory: func(string) application.ApplicationCredentialService { return service },
		Audit: func(_ context.Context, event ServiceCredentialAuditEvent) error {
			auditEvent = event
			if auditFails {
				return errors.New("audit unavailable")
			}
			return nil
		},
	}}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.TLS = &tls.ConnectionState{}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	path := "/api/v1/service/workspaces/main/applications/traefik-admin/credentials"
	res := invoke(path, "portal-token", `{"actor":"directory-anchor","request_id":"request-1"}`)
	if res.Code != 200 || !strings.Contains(res.Body.String(), "backend-secret") || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response %d %s", res.Code, res.Body.String())
	}
	if auditEvent.ClientID != "user-portal" || auditEvent.DeclaredActor != "directory-anchor" || auditEvent.WorkspaceID != "main" || auditEvent.Application != "traefik-admin" {
		t.Fatalf("audit %#v", auditEvent)
	}
	auditFails = true
	res = invoke(path, "portal-token", `{"actor":"directory-anchor"}`)
	if res.Code != 503 || strings.Contains(res.Body.String(), "backend-secret") {
		t.Fatalf("audit failure exposed credential: %d %s", res.Code, res.Body.String())
	}
	for _, test := range []struct {
		name, path, token, body string
		code                    int
	}{
		{"wrong token", path, "other", `{"actor":"admin"}`, 401},
		{"wrong workspace", strings.Replace(path, "/main/", "/other/", 1), "portal-token", `{"actor":"admin"}`, 403},
		{"wrong resource", strings.Replace(path, "traefik-admin", "another-admin", 1), "portal-token", `{"actor":"admin"}`, 403},
		{"missing actor", path, "portal-token", `{}`, 400},
		{"control actor", path, "portal-token", `{"actor":"admin\nforged"}`, 400},
		{"duplicate actor", path, "portal-token", `{"actor":"admin","actor":"other"}`, 400},
		{"unknown field", path, "portal-token", `{"actor":"admin","password":"not-forwarded"}`, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := service.calls
			res := invoke(test.path, test.token, test.body)
			if res.Code != test.code || service.calls != before {
				t.Fatalf("%d %s, calls %d -> %d", res.Code, res.Body.String(), before, service.calls)
			}
		})
	}
}

func TestServiceCredentialRouteHiddenOutsideFullDirectTLS(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    ConsoleState
		listener ListenerIdentity
		tls      bool
	}{
		{"plaintext", StateFull, ListenerDirect, false}, {"bootstrap", StateBootstrap, ListenerDirect, true}, {"enrollment", StateEnrollment, ListenerDirect, true}, {"proxy", StateFull, ListenerTrustedProxy, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, err := New(Options{Security: SecurityOptions{InitialState: test.state, Listener: test.listener, HostAllowed: func(*http.Request) bool { return true }}, ServiceAPI: &ServiceAPIOptions{
				Authenticate: func(context.Context, string, string, string, string) (string, error) {
					t.Fatal("hidden route authenticated")
					return "", nil
				},
				Factory: func(string) application.ApplicationCredentialService { t.Fatal("hidden route read data"); return nil }, Audit: func(context.Context, ServiceCredentialAuditEvent) error { return nil },
			}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/service/workspaces/main/applications/traefik-admin/credentials", strings.NewReader(`{"actor":"admin"}`))
			if test.tls {
				req.TLS = &tls.ConnectionState{}
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer portal-token")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != 404 {
				t.Fatalf("got %d %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestServiceTokenDoesNotAuthorizeOwnerRoutes(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	handler, err := New(Options{Registry: registry, Factory: func(string) QueryService { return &fakeQueryService{} }, Security: SecurityOptions{InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Authorize: LocalOwnerAuthorizer(newFakeConsoleAuthenticator())}, ServiceAPI: &ServiceAPIOptions{
		Authenticate: func(context.Context, string, string, string, string) (string, error) {
			t.Fatal("service authorizer reached owner route")
			return "portal", nil
		}, Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, ServiceCredentialAuditEvent) error { return nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/main/status", nil)
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Authorization", "Bearer portal-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("service token reached owner route: %d", res.Code)
	}
}
