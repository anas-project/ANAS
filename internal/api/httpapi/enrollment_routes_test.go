package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

const enrollmentRouteTestSPKI = "abababababababababababababababababababababababababababababababab"

func TestEnrollmentHTTPFlowBindsServerTargetAndCreatesOwner(t *testing.T) {
	store := openDirectAuthorizerStore(t)
	bootstrap := prepareHTTPEnrollmentBootstrap(t, store, "txn-http-enrollment")
	state := StateEnrollment
	target := EnrollmentTarget{Origin: "https://anas.example", SPKISHA256: enrollmentRouteTestSPKI}
	handler := newEnrollmentTestHandler(t, store, &state, target)

	issue := httptest.NewRequest(http.MethodPost, "http://nas.example"+consoleauth.EnrollmentHandoffRoute, nil)
	issue.Header.Set("Origin", "http://nas.example")
	issue.Header.Set(csrfHeaderName, bootstrap.CSRFToken)
	issue.AddCookie(&http.Cookie{Name: consoleauth.BootstrapSessionCookieName, Value: bootstrap.Token})
	issueRecorder := httptest.NewRecorder()
	handler.ServeHTTP(issueRecorder, issue)
	if issueRecorder.Code != http.StatusCreated {
		t.Fatalf("issue status = %d body=%s", issueRecorder.Code, issueRecorder.Body.String())
	}
	var handoff enrollmentHandoffResponse
	if err := json.Unmarshal(issueRecorder.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	if handoff.Handoff == "" || handoff.TargetOrigin != target.Origin || handoff.FormAction != target.Origin+consoleauth.EnrollmentHandoffExchangeRoute {
		t.Fatalf("handoff response = %#v", handoff)
	}
	assertHTTPFileOmits(t, filepath.Join(store.Directory(), "bootstrap.json"), handoff.Handoff)

	wrongTarget := httptest.NewRequest(http.MethodPost, "https://other.example"+consoleauth.EnrollmentHandoffExchangeRoute,
		strings.NewReader(url.Values{"handoff": []string{handoff.Handoff}}.Encode()))
	wrongTarget.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wrongTarget.Header.Set("Origin", "http://nas.example")
	wrongTargetRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongTargetRecorder, wrongTarget)
	if wrongTargetRecorder.Code != http.StatusForbidden {
		t.Fatalf("wrong-target status = %d body=%s", wrongTargetRecorder.Code, wrongTargetRecorder.Body.String())
	}

	exchange := httptest.NewRequest(http.MethodPost, handoff.FormAction,
		strings.NewReader(url.Values{"handoff": []string{handoff.Handoff}}.Encode()))
	exchange.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	exchange.Header.Set("Origin", "http://nas.example")
	exchangeRecorder := httptest.NewRecorder()
	handler.ServeHTTP(exchangeRecorder, exchange)
	if exchangeRecorder.Code != http.StatusSeeOther {
		t.Fatalf("exchange status = %d body=%s", exchangeRecorder.Code, exchangeRecorder.Body.String())
	}
	if location := exchangeRecorder.Header().Get("Location"); location != target.Origin+"/" {
		t.Fatalf("exchange Location = %q", location)
	}
	if allowOrigin := exchangeRecorder.Header().Get("Access-Control-Allow-Origin"); allowOrigin != "" {
		t.Fatalf("exchange enabled CORS for %q", allowOrigin)
	}
	if exchangeRecorder.Body.Len() != 0 || strings.Contains(exchangeRecorder.Body.String(), "csrf") {
		t.Fatalf("exchange response body leaked enrollment data: %q", exchangeRecorder.Body.String())
	}
	var enrollmentCookie, csrfCookie *http.Cookie
	for _, cookie := range exchangeRecorder.Result().Cookies() {
		switch cookie.Name {
		case consoleauth.EnrollmentSessionCookieName:
			enrollmentCookie = cookie
		case consoleauth.EnrollmentCSRFCookieName:
			csrfCookie = cookie
		}
	}
	if enrollmentCookie == nil || !enrollmentCookie.Secure || !enrollmentCookie.HttpOnly ||
		enrollmentCookie.SameSite != http.SameSiteStrictMode || enrollmentCookie.Path != "/" || enrollmentCookie.Domain != "" {
		t.Fatalf("enrollment session cookie = %#v", enrollmentCookie)
	}
	if csrfCookie == nil || csrfCookie.Value == "" || !csrfCookie.Secure || csrfCookie.HttpOnly ||
		csrfCookie.SameSite != http.SameSiteStrictMode || csrfCookie.Path != "/" || csrfCookie.Domain != "" {
		t.Fatalf("enrollment CSRF cookie = %#v", csrfCookie)
	}
	assertHTTPFileOmits(t, filepath.Join(store.Directory(), "bootstrap.json"), handoff.Handoff, enrollmentCookie.Value, csrfCookie.Value)

	ownerBody := []byte(`{"password":"correct horse battery staple"}`)
	owner := httptest.NewRequest(http.MethodPost, target.Origin+consoleauth.EnrollmentOwnerRoute, bytes.NewReader(ownerBody))
	owner.Header.Set("Content-Type", "application/json")
	owner.Header.Set("Origin", target.Origin)
	owner.Header.Set(csrfHeaderName, csrfCookie.Value)
	owner.AddCookie(enrollmentCookie)
	owner.AddCookie(csrfCookie)
	ownerRecorder := httptest.NewRecorder()
	handler.ServeHTTP(ownerRecorder, owner)
	if ownerRecorder.Code != http.StatusCreated {
		t.Fatalf("owner status = %d body=%s", ownerRecorder.Code, ownerRecorder.Body.String())
	}
	if state != StateFull {
		t.Fatalf("state after owner = %s", state)
	}
	for _, name := range []string{
		consoleauth.EnrollmentSessionCookieName,
		consoleauth.EnrollmentCSRFCookieName,
		consoleauth.BootstrapSessionCookieName,
	} {
		if expired := findResponseCookie(t, ownerRecorder.Result(), name); expired.MaxAge != -1 || expired.Value != "" {
			t.Fatalf("expired %s cookie = %#v", name, expired)
		}
	}
	if _, err := store.LoginLocal(context.Background(), consoleauth.LocalLoginRequest{
		Password: "correct horse battery staple", Origin: target.Origin,
	}); err != nil {
		t.Fatalf("local owner login: %v", err)
	}

	repeat := httptest.NewRequest(http.MethodPost, target.Origin+consoleauth.EnrollmentOwnerRoute, bytes.NewReader(ownerBody))
	repeatRecorder := httptest.NewRecorder()
	handler.ServeHTTP(repeatRecorder, repeat)
	if repeatRecorder.Code != http.StatusNotFound {
		t.Fatalf("full-state enrollment route = %d body=%s", repeatRecorder.Code, repeatRecorder.Body.String())
	}
}

func TestEnrollmentExchangeRejectsAmbientCredentialsAndMalformedFormWithoutConsumption(t *testing.T) {
	for _, mutate := range []func(*http.Request){
		func(request *http.Request) { request.Header.Set("Cookie", "unrelated=value") },
		func(request *http.Request) { request.Header.Set("Authorization", "Bearer ambient") },
		func(request *http.Request) { request.Header.Set("Origin", "HTTP://NAS.EXAMPLE:80/") },
		func(request *http.Request) { request.URL.RawQuery = "handoff=forbidden" },
	} {
		store := openDirectAuthorizerStore(t)
		bootstrap := prepareHTTPEnrollmentBootstrap(t, store, "txn-negative")
		state := StateEnrollment
		handler := newEnrollmentTestHandler(t, store, &state, EnrollmentTarget{Origin: "https://anas.example", SPKISHA256: enrollmentRouteTestSPKI})
		handoff := issueHTTPHandoff(t, handler, bootstrap)
		request := httptest.NewRequest(http.MethodPost, handoff.FormAction,
			strings.NewReader(url.Values{"handoff": []string{handoff.Handoff}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "http://nas.example")
		mutate(request)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code < 400 {
			t.Fatalf("invalid exchange succeeded: %d body=%s", recorder.Code, recorder.Body.String())
		}
		// Every rejection happens before credential consumption; the exact
		// canonical retry must still succeed.
		retry := httptest.NewRequest(http.MethodPost, handoff.FormAction,
			strings.NewReader(url.Values{"handoff": []string{handoff.Handoff}}.Encode()))
		retry.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		retry.Header.Set("Origin", "http://nas.example")
		retryRecorder := httptest.NewRecorder()
		handler.ServeHTTP(retryRecorder, retry)
		if retryRecorder.Code != http.StatusSeeOther {
			t.Fatalf("retry status = %d body=%s", retryRecorder.Code, retryRecorder.Body.String())
		}
	}
}

func TestEnrollmentTargetRejectsNonCanonicalSPKIDigest(t *testing.T) {
	for _, digest := range []string{
		strings.ToUpper(enrollmentRouteTestSPKI),
		strings.Repeat("z", 64),
		"ab",
	} {
		store := openDirectAuthorizerStore(t)
		bootstrap := prepareHTTPEnrollmentBootstrap(t, store, "txn-invalid-target")
		state := StateEnrollment
		handler := newEnrollmentTestHandler(t, store, &state, EnrollmentTarget{Origin: "https://anas.example", SPKISHA256: digest})
		request := httptest.NewRequest(http.MethodPost, "http://nas.example"+consoleauth.EnrollmentHandoffRoute, nil)
		request.Header.Set("Origin", "http://nas.example")
		request.Header.Set(csrfHeaderName, bootstrap.CSRFToken)
		request.AddCookie(&http.Cookie{Name: consoleauth.BootstrapSessionCookieName, Value: bootstrap.Token})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("digest %q status = %d body=%s", digest, recorder.Code, recorder.Body.String())
		}
	}
}

func TestEnrollmentExchangeUsesCertificateBoundToCurrentConnection(t *testing.T) {
	store := openDirectAuthorizerStore(t)
	bootstrap := prepareHTTPEnrollmentBootstrap(t, store, "txn-connection-spki")
	state := StateEnrollment
	issuedTarget := EnrollmentTarget{Origin: "https://anas.example", SPKISHA256: enrollmentRouteTestSPKI}
	connectionTarget := issuedTarget
	handler := newEnrollmentTestHandlerWithTargets(t, store, &state,
		func(context.Context) (EnrollmentTarget, error) { return issuedTarget, nil },
		func(context.Context) (EnrollmentTarget, error) { return connectionTarget, nil },
	)
	handoff := issueHTTPHandoff(t, handler, bootstrap)
	connectionTarget.SPKISHA256 = strings.Repeat("cd", 32)

	exchange := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, handoff.FormAction,
			strings.NewReader(url.Values{"handoff": []string{handoff.Handoff}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "http://nas.example")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if recorder := exchange(); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched connection SPKI status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	connectionTarget = issuedTarget
	if recorder := exchange(); recorder.Code != http.StatusSeeOther {
		t.Fatalf("matching connection SPKI retry status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEnrollmentOwnerRequiresCookieHeaderMatchAndServerBoundCSRF(t *testing.T) {
	store := openDirectAuthorizerStore(t)
	bootstrap := prepareHTTPEnrollmentBootstrap(t, store, "txn-owner-csrf")
	target := EnrollmentTarget{Origin: "https://anas.example", SPKISHA256: enrollmentRouteTestSPKI}
	handoff, err := store.IssueEnrollmentHandoff(context.Background(), consoleauth.IssueEnrollmentHandoffRequest{
		SessionToken: bootstrap.Token, CSRFToken: bootstrap.CSRFToken,
		SourceOrigin: bootstrap.Origin, TargetOrigin: target.Origin, SPKISHA256: target.SPKISHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.ExchangeEnrollmentHandoff(context.Background(), consoleauth.ExchangeEnrollmentHandoffRequest{
		Token: handoff.Token, SourceOrigin: handoff.SourceOrigin,
		TargetOrigin: handoff.TargetOrigin, SPKISHA256: handoff.SPKISHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := StateEnrollment
	handler := newEnrollmentTestHandler(t, store, &state, target)

	tests := []struct {
		name         string
		cookieValues []string
		headerValues []string
	}{
		{name: "missing cookie", headerValues: []string{session.CSRFToken}},
		{name: "missing header", cookieValues: []string{session.CSRFToken}},
		{name: "cookie header mismatch", cookieValues: []string{session.CSRFToken}, headerValues: []string{"different"}},
		{name: "duplicate cookie", cookieValues: []string{session.CSRFToken, session.CSRFToken}, headerValues: []string{session.CSRFToken}},
		{name: "duplicate header", cookieValues: []string{session.CSRFToken}, headerValues: []string{session.CSRFToken, session.CSRFToken}},
		{name: "matching attacker value fails server digest", cookieValues: []string{"attacker"}, headerValues: []string{"attacker"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, target.Origin+consoleauth.EnrollmentOwnerRoute,
				strings.NewReader(`{"password":"must-not-be-created"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", target.Origin)
			for _, value := range test.headerValues {
				request.Header.Add(csrfHeaderName, value)
			}
			request.AddCookie(&http.Cookie{Name: consoleauth.EnrollmentSessionCookieName, Value: session.Token})
			for _, value := range test.cookieValues {
				request.AddCookie(&http.Cookie{Name: consoleauth.EnrollmentCSRFCookieName, Value: value})
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("owner status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			if state != StateEnrollment {
				t.Fatalf("state after rejected owner = %s", state)
			}
			if _, err := store.AuthenticateEnrollment(context.Background(), consoleauth.EnrollmentAuthenticationRequest{
				SessionToken: session.Token, CSRFToken: session.CSRFToken, Origin: session.Origin,
				TransactionID: session.TransactionID, Route: consoleauth.EnrollmentOwnerRoute, RequireCSRF: true,
			}); err != nil {
				t.Fatalf("rejected request consumed enrollment session: %v", err)
			}
		})
	}
}

func prepareHTTPEnrollmentBootstrap(t *testing.T, store *consoleauth.Store, transactionID string) consoleauth.BootstrapSessionCredential {
	t.Helper()
	issued, err := store.IssueBootstrapToken(context.Background(), consoleauth.IssueBootstrapTokenRequest{
		TransactionID: transactionID, State: consoleauth.StateBootstrap,
		AllowedRoutes: []string{consoleauth.EnrollmentHandoffRoute},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.ExchangeBootstrapToken(context.Background(), consoleauth.ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteBootstrapToEnrollment(context.Background(), transactionID, []string{consoleauth.EnrollmentHandoffRoute}); err != nil {
		t.Fatal(err)
	}
	return session
}

func newEnrollmentTestHandler(t *testing.T, store *consoleauth.Store, state *ConsoleState, target EnrollmentTarget) http.Handler {
	t.Helper()
	return newEnrollmentTestHandlerWithTargets(t, store, state,
		func(context.Context) (EnrollmentTarget, error) { return target, nil },
		func(context.Context) (EnrollmentTarget, error) { return target, nil },
	)
}

func newEnrollmentTestHandlerWithTargets(
	t *testing.T,
	store *consoleauth.Store,
	state *ConsoleState,
	currentTarget func(context.Context) (EnrollmentTarget, error),
	connectionTarget func(context.Context) (EnrollmentTarget, error),
) http.Handler {
	t.Helper()
	security := SecurityOptions{
		State:       func(context.Context) (ConsoleState, error) { return *state, nil },
		HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: DirectSessionAuthorizer(store),
	}
	handler, err := NewHandlerWithEnrollment(nil, nil, security, store, EnrollmentOptions{
		Workflow:                store,
		CurrentTarget:           currentTarget,
		CurrentConnectionTarget: connectionTarget,
		CompleteTransition: func(_ context.Context, transactionID string) error {
			if transactionID == "" || *state != StateEnrollment {
				return errors.New("invalid transition")
			}
			*state = StateFull
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func issueHTTPHandoff(t *testing.T, handler http.Handler, bootstrap consoleauth.BootstrapSessionCredential) enrollmentHandoffResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://nas.example"+consoleauth.EnrollmentHandoffRoute, nil)
	request.Header.Set("Origin", "http://nas.example")
	request.Header.Set(csrfHeaderName, bootstrap.CSRFToken)
	request.AddCookie(&http.Cookie{Name: consoleauth.BootstrapSessionCookieName, Value: bootstrap.Token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("issue status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response enrollmentHandoffResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func assertHTTPFileOmits(t *testing.T, path string, values ...string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if value != "" && bytes.Contains(body, []byte(value)) {
			t.Fatalf("%s contains credential value", path)
		}
	}
}
