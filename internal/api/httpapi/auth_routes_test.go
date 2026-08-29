package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

func TestBootstrapCSRFAndExchangeSetHostOnlyStrictCookies(t *testing.T) {
	auth := newFakeConsoleAuthenticator()
	handler := newAuthenticationHandler(t, StateBootstrap, ListenerDirect, auth)
	origin := "http://192.0.2.20:8080"
	csrfToken, csrfCookie := requestPreAuthCSRF(t, handler, origin)
	if csrfCookie.Secure || !csrfCookie.HttpOnly || csrfCookie.SameSite != http.SameSiteStrictMode || csrfCookie.Path != "/" || csrfCookie.Domain != "" {
		t.Fatalf("bootstrap CSRF cookie = %#v", csrfCookie)
	}

	request := httptest.NewRequest(http.MethodPost, origin+"/api/v1/auth/bootstrap/exchange", strings.NewReader(`{"token":"one-use-token"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set(csrfHeaderName, csrfToken)
	request.AddCookie(csrfCookie)
	request.RemoteAddr = "192.0.2.30:54321"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("exchange = %d, %s", response.Code, response.Body.String())
	}
	if auth.exchangeCalls != 1 || auth.lastExchange.Token != "one-use-token" || auth.lastExchange.Origin != origin {
		t.Fatalf("exchange request = %#v, calls %d", auth.lastExchange, auth.exchangeCalls)
	}
	session := findResponseCookie(t, response.Result(), bootstrapSessionCookie)
	if session.Value != auth.bootstrapCredential.Token || session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Path != "/" || session.Domain != "" {
		t.Fatalf("bootstrap session cookie = %#v", session)
	}
	if strings.Contains(response.Body.String(), auth.bootstrapCredential.Token) || strings.Contains(response.Body.String(), "one-use-token") {
		t.Fatalf("exchange response leaked token: %s", response.Body.String())
	}
}

func TestBootstrapOverTLSMarksPreAuthAndSessionCookiesSecure(t *testing.T) {
	auth := newFakeConsoleAuthenticator()
	handler := newAuthenticationHandler(t, StateBootstrap, ListenerDirect, auth)
	origin := "https://192.0.2.20:8080"
	csrfToken, csrfCookie := requestPreAuthCSRF(t, handler, origin)
	if !csrfCookie.Secure {
		t.Fatal("TLS bootstrap CSRF cookie was not Secure")
	}

	request := httptest.NewRequest(http.MethodPost, origin+"/api/v1/auth/bootstrap/exchange", strings.NewReader(`{"token":"one-use-token"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set(csrfHeaderName, csrfToken)
	request.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("exchange = %d, %s", response.Code, response.Body.String())
	}
	if session := findResponseCookie(t, response.Result(), bootstrapSessionCookie); !session.Secure {
		t.Fatal("TLS bootstrap session cookie was not Secure")
	}
}

func TestBootstrapExchangeRejectsOriginCSRFForgeryAndOversizedBody(t *testing.T) {
	for _, test := range []struct {
		name        string
		origin      string
		headerToken string
		body        string
		contentType string
		want        int
	}{
		{name: "foreign origin", origin: "http://other.example:8080", headerToken: "match", body: `{"token":"value"}`, contentType: "application/json", want: http.StatusForbidden},
		{name: "noncanonical origin", origin: "HTTP://192.0.2.20:8080/", headerToken: "match", body: `{"token":"value"}`, contentType: "application/json", want: http.StatusForbidden},
		{name: "missing CSRF", origin: "http://192.0.2.20:8080", body: `{"token":"value"}`, contentType: "application/json", want: http.StatusForbidden},
		{name: "wrong media", origin: "http://192.0.2.20:8080", headerToken: "match", body: `{"token":"value"}`, contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "oversized", origin: "http://192.0.2.20:8080", headerToken: "match", body: `{"token":"` + strings.Repeat("x", maximumAuthRequestBytes) + `"}`, contentType: "application/json", want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := newFakeConsoleAuthenticator()
			handler := newAuthenticationHandler(t, StateBootstrap, ListenerDirect, auth)
			request := httptest.NewRequest(http.MethodPost, "http://192.0.2.20:8080/api/v1/auth/bootstrap/exchange", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Origin", test.origin)
			request.Header.Set(csrfHeaderName, test.headerToken)
			request.AddCookie(&http.Cookie{Name: preAuthCSRFCookieName, Value: "match"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
			if auth.exchangeCalls != 0 {
				t.Fatalf("rejected request exchanged token %d times", auth.exchangeCalls)
			}
		})
	}
}

func TestBootstrapAuthenticationRoutesDoNotEnableCORS(t *testing.T) {
	handler := newAuthenticationHandler(t, StateBootstrap, ListenerDirect, newFakeConsoleAuthenticator())
	request := httptest.NewRequest(http.MethodOptions, "http://192.0.2.20:8080/api/v1/auth/bootstrap/exchange", nil)
	request.Header.Set("Origin", "http://other.example:8080")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("preflight = %d, %s", response.Code, response.Body.String())
	}
	for _, name := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if value := response.Header().Get(name); value != "" {
			t.Fatalf("preflight unexpectedly set %s: %q", name, value)
		}
	}
}

func TestFullLocalLoginRequiresTLSAndSetsSecureCookie(t *testing.T) {
	auth := newFakeConsoleAuthenticator()
	handler := newAuthenticationHandler(t, StateFull, ListenerDirect, auth)
	origin := "https://anas.example:8443"
	csrfToken, csrfCookie := requestPreAuthCSRF(t, handler, origin)
	if !csrfCookie.Secure {
		t.Fatal("full CSRF cookie was not Secure")
	}
	request := httptest.NewRequest(http.MethodPost, origin+"/api/v1/auth/login", strings.NewReader(`{"password":"owner-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	request.Header.Set(csrfHeaderName, csrfToken)
	request.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login = %d, %s", response.Code, response.Body.String())
	}
	if auth.loginCalls != 1 || auth.lastLogin.Password != "owner-password" || auth.lastLogin.Origin != origin {
		t.Fatalf("login request = %#v, calls %d", auth.lastLogin, auth.loginCalls)
	}
	session := findResponseCookie(t, response.Result(), localSessionCookie)
	if !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Domain != "" || session.Path != "/" {
		t.Fatalf("local session cookie = %#v", session)
	}
	if strings.Contains(response.Body.String(), "owner-password") || strings.Contains(response.Body.String(), auth.localCredential.Token) {
		t.Fatalf("login response leaked credential: %s", response.Body.String())
	}

	plain := httptest.NewRequest(http.MethodPost, "http://anas.example:8443/api/v1/auth/login", strings.NewReader(`{"password":"owner-password"}`))
	plain.Header.Set("Content-Type", "application/json")
	plainResponse := httptest.NewRecorder()
	handler.ServeHTTP(plainResponse, plain)
	if plainResponse.Code != http.StatusNotFound || auth.loginCalls != 1 {
		t.Fatalf("plaintext login = %d, calls %d", plainResponse.Code, auth.loginCalls)
	}
}

func TestLocalLoginRoutesAreHiddenOnTrustedProxyListener(t *testing.T) {
	auth := newFakeConsoleAuthenticator()
	handler := newAuthenticationHandler(t, StateFull, ListenerTrustedProxy, auth)
	request := httptest.NewRequest(http.MethodPost, "https://anas.example/api/v1/auth/login", strings.NewReader(`{"password":"value"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || auth.loginCalls != 0 {
		t.Fatalf("proxy local login = %d, calls %d", response.Code, auth.loginCalls)
	}
}

func TestLocalOwnerAuthorizerUsesOnlyUniqueSessionCookie(t *testing.T) {
	for _, test := range []struct {
		name       string
		cookies    []*http.Cookie
		wantStatus int
		wantCalls  int
	}{
		{name: "valid", cookies: []*http.Cookie{{Name: localSessionCookie, Value: "session-value"}}, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "duplicate", cookies: []*http.Cookie{{Name: localSessionCookie, Value: "one"}, {Name: localSessionCookie, Value: "two"}}, wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth := newFakeConsoleAuthenticator()
			registry, _ := testRegistry(t, "main")
			handler, err := NewHandlerWithAuthentication(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
				InitialState: StateFull,
				HostAllowed:  func(*http.Request) bool { return true },
				Listener:     ListenerDirect,
				Authorize:    LocalOwnerAuthorizer(auth),
			}, auth)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "https://anas.example/api/v1/workspaces/main/status", nil)
			for _, cookie := range test.cookies {
				request.AddCookie(cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || auth.authenticateCalls != test.wantCalls {
				t.Fatalf("response = %d, auth calls %d", response.Code, auth.authenticateCalls)
			}
		})
	}
}

func TestLocalLogoutRequiresSessionBoundOriginAndCSRF(t *testing.T) {
	auth := newFakeConsoleAuthenticator()
	handler := newAuthenticationHandler(t, StateFull, ListenerDirect, auth)
	origin := "https://anas.example"
	request := httptest.NewRequest(http.MethodPost, origin+"/api/v1/auth/logout", nil)
	request.Header.Set("Origin", origin)
	request.Header.Set(csrfHeaderName, "csrf-value")
	request.AddCookie(&http.Cookie{Name: localSessionCookie, Value: "session-value"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || auth.authenticateCalls != 1 || auth.logoutCalls != 1 {
		t.Fatalf("logout = %d, authenticate %d, logout %d: %s", response.Code, auth.authenticateCalls, auth.logoutCalls, response.Body.String())
	}
	if auth.lastLogout.SessionToken != "session-value" || auth.lastLogout.CSRFToken != "csrf-value" || auth.lastLogout.Origin != origin {
		t.Fatalf("logout request = %#v", auth.lastLogout)
	}
	expired := findResponseCookie(t, response.Result(), localSessionCookie)
	if expired.MaxAge != -1 || !expired.Secure {
		t.Fatalf("expired session cookie = %#v", expired)
	}
}

func TestAuthenticationAttemptsAreRateLimited(t *testing.T) {
	auth := newFakeConsoleAuthenticator()
	httpHandler := newAuthenticationHandler(t, StateBootstrap, ListenerDirect, auth)
	h := httpHandler.(*handler)
	h.authHTTP.exchangePerClient = newAttemptLimiter(1, time.Hour)
	origin := "http://192.0.2.20:8080"
	csrfToken, csrfCookie := requestPreAuthCSRF(t, h, origin)
	for attempt, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodPost, origin+"/api/v1/auth/bootstrap/exchange", strings.NewReader(`{"token":"value"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", origin)
		request.Header.Set(csrfHeaderName, csrfToken)
		request.AddCookie(csrfCookie)
		request.RemoteAddr = "192.0.2.30:54321"
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("attempt %d = %d, want %d", attempt+1, response.Code, want)
		}
		if want == http.StatusTooManyRequests && response.Header().Get("Retry-After") == "" {
			t.Fatal("rate-limited response omitted Retry-After")
		}
	}
	if auth.exchangeCalls != 1 {
		t.Fatalf("exchange calls = %d", auth.exchangeCalls)
	}
}

func newAuthenticationHandler(t *testing.T, state ConsoleState, listener ListenerIdentity, auth *fakeConsoleAuthenticator) http.Handler {
	t.Helper()
	handler, err := NewHandlerWithAuthentication(nil, nil, SecurityOptions{
		InitialState: state,
		HostAllowed:  func(*http.Request) bool { return true },
		Listener:     listener,
		Authorize:    LocalOwnerAuthorizer(auth),
	}, auth)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func requestPreAuthCSRF(t *testing.T, handler http.Handler, origin string) (string, *http.Cookie) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, origin+"/api/v1/auth/csrf", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("csrf = %d, %s", response.Code, response.Body.String())
	}
	var body csrfResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CSRFToken == "" {
		t.Fatal("empty CSRF token")
	}
	return body.CSRFToken, findResponseCookie(t, response.Result(), preAuthCSRFCookieName)
}

func findResponseCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response omitted cookie %s", name)
	return nil
}

type fakeConsoleAuthenticator struct {
	bootstrapCredential consoleauth.BootstrapSessionCredential
	localCredential     consoleauth.LocalSessionCredential
	exchangeErr         error
	loginErr            error
	authenticateErr     error
	logoutErr           error
	exchangeCalls       int
	loginCalls          int
	authenticateCalls   int
	logoutCalls         int
	lastExchange        consoleauth.ExchangeBootstrapTokenRequest
	lastLogin           consoleauth.LocalLoginRequest
	lastAuthenticate    consoleauth.LocalAuthenticationRequest
	lastLogout          consoleauth.LocalLogoutRequest
}

func newFakeConsoleAuthenticator() *fakeConsoleAuthenticator {
	now := time.Now().UTC()
	return &fakeConsoleAuthenticator{
		bootstrapCredential: consoleauth.BootstrapSessionCredential{
			Token: "server-bootstrap-session", CSRFToken: "bootstrap-csrf", TransactionID: "bootstrap", State: consoleauth.StateBootstrap,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour), IdleExpiresAt: now.Add(30 * time.Minute),
		},
		localCredential: consoleauth.LocalSessionCredential{
			Token: "server-local-session", CSRFToken: "local-csrf", Origin: "https://anas.example:8443",
			CreatedAt: now, ExpiresAt: now.Add(time.Hour), IdleExpiresAt: now.Add(30 * time.Minute),
		},
	}
}

func (auth *fakeConsoleAuthenticator) ExchangeBootstrapToken(_ context.Context, request consoleauth.ExchangeBootstrapTokenRequest) (consoleauth.BootstrapSessionCredential, error) {
	auth.exchangeCalls++
	auth.lastExchange = request
	return auth.bootstrapCredential, auth.exchangeErr
}

func (auth *fakeConsoleAuthenticator) LoginLocal(_ context.Context, request consoleauth.LocalLoginRequest) (consoleauth.LocalSessionCredential, error) {
	auth.loginCalls++
	auth.lastLogin = request
	return auth.localCredential, auth.loginErr
}

func (auth *fakeConsoleAuthenticator) AuthenticateLocal(_ context.Context, request consoleauth.LocalAuthenticationRequest) (consoleauth.LocalPrincipal, error) {
	auth.authenticateCalls++
	auth.lastAuthenticate = request
	return consoleauth.LocalPrincipal{}, auth.authenticateErr
}

func (auth *fakeConsoleAuthenticator) LogoutLocal(_ context.Context, request consoleauth.LocalLogoutRequest) error {
	auth.logoutCalls++
	auth.lastLogout = request
	return auth.logoutErr
}

var _ ConsoleAuthenticator = (*fakeConsoleAuthenticator)(nil)
