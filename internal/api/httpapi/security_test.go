package httpapi

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEveryRouteDeclaresSecurityMetadata(t *testing.T) {
	inventory, err := RouteInventory(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 17 {
		t.Fatalf("route inventory contains %d routes, want 17", len(inventory))
	}
	for _, policy := range inventory {
		if err := validateRoutePolicy(policy); err != nil {
			t.Errorf("%s %s: %v", policy.Method, policy.Pattern, err)
		}
	}
}

func TestSecurityHeadersDistinguishPlaintextAndTLS(t *testing.T) {
	handler := NewHandler(nil, nil)
	plainRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	plainResponse := httptest.NewRecorder()
	handler.ServeHTTP(plainResponse, plainRequest)
	if plainResponse.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("plaintext response included HSTS")
	}
	for _, name := range []string{"Cache-Control", "Content-Security-Policy", "Permissions-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options"} {
		if plainResponse.Header().Get(name) == "" {
			t.Errorf("plaintext response omitted %s", name)
		}
	}

	tlsRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	tlsRequest.TLS = &tls.ConnectionState{}
	tlsResponse := httptest.NewRecorder()
	handler.ServeHTTP(tlsResponse, tlsRequest)
	if tlsResponse.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("TLS response omitted HSTS")
	}
}

func TestRoutePolicyRejectsMissingSecurityMetadata(t *testing.T) {
	tests := []RoutePolicy{
		{Method: http.MethodGet, Pattern: "/missing-permission", Scope: ScopeService, Listeners: []ListenerIdentity{ListenerDirect}, Access: map[ConsoleState]RouteAccess{StateFull: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}}}},
		{Method: http.MethodGet, Pattern: "/missing-scope", Permission: PermissionPublic, Listeners: []ListenerIdentity{ListenerDirect}, Access: map[ConsoleState]RouteAccess{StateFull: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}}}},
		{Method: http.MethodGet, Pattern: "/missing-listener", Permission: PermissionPublic, Scope: ScopeService, Access: map[ConsoleState]RouteAccess{StateFull: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}}}},
		{Method: http.MethodGet, Pattern: "/missing-states", Permission: PermissionPublic, Scope: ScopeService, Listeners: []ListenerIdentity{ListenerDirect}},
	}
	for _, policy := range tests {
		if err := validateRoutePolicy(policy); err == nil {
			t.Fatalf("policy %#v was accepted", policy)
		}
	}
}

func TestBootstrapHidesFullRoutesBeforeMethodAndAuthentication(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	authorizeCalls := 0
	handler, err := NewHandlerWithSecurity(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateBootstrap,
		HostAllowed:  func(*http.Request) bool { return true },
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			authorizeCalls++
			return Principal{ID: "forged", Role: "owner"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/main/status", nil)
	request.Header.Set("X-Forwarded-User", "forged")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("response = %d, %s", response.Code, response.Body.String())
	}
	if authorizeCalls != 0 {
		t.Fatalf("hidden route invoked authorizer %d times", authorizeCalls)
	}
}

func TestFullPlaintextHidesTLSOnlyRoute(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	handler, err := NewHandlerWithSecurity(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull,
		HostAllowed:  func(*http.Request) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/main/status", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("response = %d, %s", response.Code, response.Body.String())
	}
}

func TestDirectListenerStripsForgedIdentityHeaders(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	var observed string
	handler, err := NewHandlerWithSecurity(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull,
		HostAllowed:  func(*http.Request) bool { return true },
		Listener:     ListenerDirect,
		Authorize: func(request *http.Request, _ AuthorizationRequest) (Principal, error) {
			observed = request.Header.Get("X-Forwarded-User")
			if observed != "" {
				return Principal{ID: observed, Role: "owner"}, nil
			}
			return Principal{}, ErrUnauthenticated
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/main/status", nil)
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("X-Forwarded-User", "forged-owner")
	request.Header.Set("Forwarded", "for=127.0.0.1;proto=https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d, %s", response.Code, response.Body.String())
	}
	if observed != "" {
		t.Fatalf("authorizer observed forged identity %q", observed)
	}
}

func TestAuthorizationErrorsHaveStableBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code int
	}{
		{name: "unauthenticated", err: ErrUnauthenticated, code: http.StatusUnauthorized},
		{name: "forbidden", err: ErrForbidden, code: http.StatusForbidden},
		{name: "unavailable", err: errors.New("store offline"), code: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry, _ := testRegistry(t, "main")
			handler, err := NewHandlerWithSecurity(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
				InitialState: StateFull,
				HostAllowed:  func(*http.Request) bool { return true },
				Authorize:    func(*http.Request, AuthorizationRequest) (Principal, error) { return Principal{}, test.err },
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/main/status", nil)
			request.TLS = &tls.ConnectionState{}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.code {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
		})
	}
}
