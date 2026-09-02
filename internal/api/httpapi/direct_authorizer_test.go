package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

func TestDirectSessionAuthorizerBindsBootstrapStateTransactionAndRoutePattern(t *testing.T) {
	store := openDirectAuthorizerStore(t)
	const routePattern = "/api/v1/workspaces/{ws}/config/validate"
	issued, err := store.IssueBootstrapToken(context.Background(), consoleauth.IssueBootstrapTokenRequest{
		TransactionID: "txn-direct", State: consoleauth.StateBootstrap,
		AllowedRoutes: []string{routePattern},
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
	policy := RoutePolicy{
		Method: http.MethodPost, Pattern: routePattern,
		Permission: PermissionConfigValidate, Scope: ScopeWorkspace,
		Listeners: []ListenerIdentity{ListenerDirect},
		Access: map[ConsoleState]RouteAccess{
			StateBootstrap: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext}},
		},
	}
	request := httptest.NewRequest(http.MethodPost, "http://nas.example/api/v1/workspaces/main/config/validate", nil)
	request.Header.Set("Origin", "http://nas.example")
	request.Header.Set(csrfHeaderName, session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: consoleauth.BootstrapSessionCookieName, Value: session.Token})
	request = withConsoleState(request, StateBootstrap)

	principal, err := DirectSessionAuthorizer(store)(request, AuthorizationRequest{Policy: policy, Params: map[string]string{"ws": "main"}})
	if err != nil {
		t.Fatal(err)
	}
	if principal.TransactionID != "txn-direct" || principal.Source != "bootstrap" || principal.Role != "bootstrap" {
		t.Fatalf("principal = %#v", principal)
	}

	policy.Pattern = "/api/v1/workspaces/{ws}/actions/apply"
	if _, err := DirectSessionAuthorizer(store)(request, AuthorizationRequest{Policy: policy}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("undeclared route error = %v", err)
	}
	request.Header.Set("Origin", "http://other.example")
	if _, err := DirectSessionAuthorizer(store)(request, AuthorizationRequest{Policy: policy}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong origin error = %v", err)
	}
}

func TestDirectSessionAuthorizerAcceptsOnlyBoundEnrollmentSession(t *testing.T) {
	store := openDirectAuthorizerStore(t)
	issued, err := store.IssueBootstrapToken(context.Background(), consoleauth.IssueBootstrapTokenRequest{
		TransactionID: "txn-enrollment", State: consoleauth.StateBootstrap,
		AllowedRoutes: []string{consoleauth.EnrollmentHandoffRoute},
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := store.ExchangeBootstrapToken(context.Background(), consoleauth.ExchangeBootstrapTokenRequest{Token: issued.Token, Origin: "http://nas.example"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteBootstrapToEnrollment(context.Background(), "txn-enrollment", []string{consoleauth.EnrollmentHandoffRoute}); err != nil {
		t.Fatal(err)
	}
	handoff, err := store.IssueEnrollmentHandoff(context.Background(), consoleauth.IssueEnrollmentHandoffRequest{
		SessionToken: bootstrap.Token, CSRFToken: bootstrap.CSRFToken,
		SourceOrigin: "http://nas.example", TargetOrigin: "https://anas.example",
		SPKISHA256: "abababababababababababababababababababababababababababababababab",
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
	policy := RoutePolicy{
		Method: http.MethodPost, Pattern: consoleauth.EnrollmentOwnerRoute,
		Permission: PermissionAuthExchange, Scope: ScopeService,
		Listeners: []ListenerIdentity{ListenerDirect},
		Access: map[ConsoleState]RouteAccess{
			StateEnrollment: {Authentication: AuthenticationEnrollment, Transports: []RequestTransport{TransportTLS}},
		},
	}
	request := httptest.NewRequest(http.MethodPost, "https://anas.example"+consoleauth.EnrollmentOwnerRoute, nil)
	request.Header.Set("Origin", "https://anas.example")
	request.Header.Set(csrfHeaderName, session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: consoleauth.EnrollmentSessionCookieName, Value: session.Token})
	request.AddCookie(&http.Cookie{Name: consoleauth.EnrollmentCSRFCookieName, Value: session.CSRFToken})
	request = withConsoleState(request, StateEnrollment)
	principal, err := DirectSessionAuthorizer(store)(request, AuthorizationRequest{Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if principal.TransactionID != "txn-enrollment" || principal.Source != "enrollment" {
		t.Fatalf("principal = %#v", principal)
	}

	wrongState := withConsoleState(request.Clone(context.Background()), StateFull)
	if _, err := DirectSessionAuthorizer(store)(wrongState, AuthorizationRequest{Policy: policy}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong state error = %v", err)
	}
}

func openDirectAuthorizerStore(t *testing.T) *consoleauth.Store {
	t.Helper()
	store, err := consoleauth.Open(filepath.Join(t.TempDir(), "auth"), consoleauth.AuditSinkFunc(func(context.Context, consoleauth.AuditEvent) error {
		return nil
	}), consoleauth.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
