package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

const (
	preAuthCSRFCookieName   = "anas_preauth_csrf"
	bootstrapSessionCookie  = consoleauth.BootstrapSessionCookieName
	localSessionCookie      = consoleauth.LocalSessionCookieName
	csrfHeaderName          = "X-CSRF-Token"
	maximumAuthRequestBytes = 4 << 10
	preAuthCSRFTTL          = 10 * time.Minute
)

type ConsoleAuthenticator interface {
	ExchangeBootstrapToken(context.Context, consoleauth.ExchangeBootstrapTokenRequest) (consoleauth.BootstrapSessionCredential, error)
	LoginLocal(context.Context, consoleauth.LocalLoginRequest) (consoleauth.LocalSessionCredential, error)
	AuthenticateLocal(context.Context, consoleauth.LocalAuthenticationRequest) (consoleauth.LocalPrincipal, error)
	RefreshLocalSession(context.Context, consoleauth.LocalSessionRefreshRequest) (consoleauth.LocalSessionCredential, error)
	LogoutLocal(context.Context, consoleauth.LocalLogoutRequest) error
}

type BootstrapSessionAuthenticator interface {
	CurrentBootstrapTransaction(context.Context, consoleauth.ConsoleState) (string, error)
	AuthenticateBootstrap(context.Context, consoleauth.BootstrapAuthenticationRequest) (consoleauth.BootstrapPrincipal, error)
	RefreshBootstrapSession(context.Context, consoleauth.BootstrapSessionRefreshRequest) (consoleauth.BootstrapSessionCredential, error)
}

type EnrollmentSessionAuthenticator interface {
	CurrentEnrollmentTransaction(context.Context) (string, error)
	AuthenticateEnrollment(context.Context, consoleauth.EnrollmentAuthenticationRequest) (consoleauth.EnrollmentPrincipal, error)
}

type DirectAuthenticator interface {
	ConsoleAuthenticator
	BootstrapSessionAuthenticator
	EnrollmentSessionAuthenticator
}

type authHTTPState struct {
	exchangePerClient *attemptLimiter
	exchangeGlobal    *attemptLimiter
	loginPerClient    *attemptLimiter
	loginGlobal       *attemptLimiter
	stepUpPerClient   *attemptLimiter
	stepUpGlobal      *attemptLimiter
	loginWork         chan struct{}
	random            io.Reader
}

func newAuthHTTPState() authHTTPState {
	return authHTTPState{
		exchangePerClient: newAttemptLimiter(10, 5*time.Minute),
		exchangeGlobal:    newAttemptLimiter(120, time.Minute),
		loginPerClient:    newAttemptLimiter(5, 5*time.Minute),
		loginGlobal:       newAttemptLimiter(30, time.Minute),
		stepUpPerClient:   newAttemptLimiter(5, 5*time.Minute),
		stepUpGlobal:      newAttemptLimiter(30, time.Minute),
		loginWork:         make(chan struct{}, 2),
		random:            rand.Reader,
	}
}

type csrfResponse struct {
	APIVersion string    `json:"api_version"`
	CSRFToken  string    `json:"csrf_token"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type authSessionResponse struct {
	APIVersion    string    `json:"api_version"`
	CSRFToken     string    `json:"csrf_token"`
	ExpiresAt     time.Time `json:"expires_at"`
	IdleExpiresAt time.Time `json:"idle_expires_at"`
	State         string    `json:"state"`
	TransactionID string    `json:"transaction_id,omitempty"`
}

type localStepUpRequest struct {
	Password     string `json:"password"`
	Action       string `json:"action"`
	WorkspaceID  string `json:"workspace_id"`
	DeploymentID string `json:"deployment_id,omitempty"`
}

type localStepUpResponse struct {
	APIVersion   string    `json:"api_version"`
	Proof        string    `json:"proof"`
	ExpiresAt    time.Time `json:"expires_at"`
	Action       string    `json:"action"`
	WorkspaceID  string    `json:"workspace_id"`
	DeploymentID string    `json:"deployment_id,omitempty"`
}

func (h *handler) issuePreAuthCSRF(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	state, ok := ConsoleStateFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "state_unavailable", "control-plane state is unavailable")
		return
	}
	token, err := randomURLCredential(h.authHTTP.random)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "random_unavailable", "security token generation is unavailable")
		return
	}
	expiresAt := time.Now().UTC().Add(preAuthCSRFTTL)
	http.SetCookie(w, preAuthCookie(token, state, expiresAt, false, r.TLS != nil))
	writeJSON(w, http.StatusOK, csrfResponse{APIVersion: APIVersion, CSRFToken: token, ExpiresAt: expiresAt})
}

func (h *handler) exchangeBootstrapToken(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.auth == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	origin, ok := requireWriteOriginAndPreAuthCSRF(w, r)
	if !ok {
		return
	}
	clientKey := directClientKey(r)
	if !allowAuthAttempt(w, h.authHTTP.exchangePerClient, clientKey) || !allowAuthAttempt(w, h.authHTTP.exchangeGlobal, "global") {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeAuthJSON(w, r, &body) {
		return
	}
	credential, err := h.auth.ExchangeBootstrapToken(r.Context(), consoleauth.ExchangeBootstrapTokenRequest{Token: body.Token, Origin: origin})
	body.Token = ""
	if err != nil {
		if errors.Is(err, consoleauth.ErrInvalidToken) || errors.Is(err, consoleauth.ErrCredentialExpired) {
			writeProblem(w, http.StatusUnauthorized, "invalid_bootstrap_token", "bootstrap token is invalid or expired")
			return
		}
		if errors.Is(err, consoleauth.ErrOriginMismatch) {
			writeProblem(w, http.StatusForbidden, "origin_mismatch", "request origin is not allowed")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	cookie, err := consoleauth.SessionCookie(bootstrapSessionCookie, credential.Token, credential.State, credential.ExpiresAt)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
		return
	}
	cookie.Secure = cookie.Secure || r.TLS != nil
	http.SetCookie(w, cookie)
	state, _ := ConsoleStateFromContext(r.Context())
	http.SetCookie(w, preAuthCookie("", state, time.Unix(1, 0), true, r.TLS != nil))
	writeJSON(w, http.StatusOK, authSessionResponse{
		APIVersion: APIVersion, CSRFToken: credential.CSRFToken,
		ExpiresAt: credential.ExpiresAt, IdleExpiresAt: credential.IdleExpiresAt,
		State: string(credential.State), TransactionID: credential.TransactionID,
	})
}

func (h *handler) refreshAuthSession(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if h.auth == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	state, stateOK := ConsoleStateFromContext(r.Context())
	principal, principalOK := PrincipalFromContext(r.Context())
	origin, err := canonicalRequestOrigin(r)
	if !stateOK || !principalOK || err != nil {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return
	}

	var response authSessionResponse
	switch state {
	case StateBootstrap, StateEnrollment:
		bootstrapAuth, ok := h.auth.(BootstrapSessionAuthenticator)
		if !ok || principal.Source != "bootstrap" || principal.TransactionID == "" {
			writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
			return
		}
		sessionToken, ok := uniqueCookieValue(r, bootstrapSessionCookie)
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
			return
		}
		authState := consoleauth.StateBootstrap
		if state == StateEnrollment {
			authState = consoleauth.StateEnrollment
		}
		credential, refreshErr := bootstrapAuth.RefreshBootstrapSession(r.Context(), consoleauth.BootstrapSessionRefreshRequest{
			SessionToken: sessionToken, Origin: origin, TransactionID: principal.TransactionID,
			State: authState, Route: r.URL.Path,
		})
		if refreshErr != nil {
			writeSessionRefreshError(w, refreshErr)
			return
		}
		response = authSessionResponse{
			APIVersion: APIVersion, CSRFToken: credential.CSRFToken,
			ExpiresAt: credential.ExpiresAt, IdleExpiresAt: credential.IdleExpiresAt,
			State: string(credential.State), TransactionID: credential.TransactionID,
		}
	case StateFull:
		if principal.Role != "owner" {
			writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
			return
		}
		switch principal.Source {
		case "local":
			sessionToken, ok := uniqueCookieValue(r, localSessionCookie)
			if !ok {
				writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
				return
			}
			credential, refreshErr := h.auth.RefreshLocalSession(r.Context(), consoleauth.LocalSessionRefreshRequest{
				SessionToken: sessionToken, Origin: origin,
			})
			if refreshErr != nil {
				writeSessionRefreshError(w, refreshErr)
				return
			}
			response = authSessionResponse{
				APIVersion: APIVersion, CSRFToken: credential.CSRFToken,
				ExpiresAt: credential.ExpiresAt, IdleExpiresAt: credential.IdleExpiresAt,
				State: string(StateFull),
			}
		case "oidc_proxy":
			proxyAuth, ok := h.auth.(ProxyAuthenticator)
			if !ok {
				writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
				return
			}
			sessionToken, _ := uniqueCookieValue(r, proxySessionCookie)
			credential, refreshErr := proxyAuth.RefreshProxySession(r.Context(), consoleauth.ProxySessionRefreshRequest{
				SessionToken: sessionToken, Origin: origin, Identity: proxyIdentityFromPrincipal(principal),
			})
			if refreshErr != nil {
				writeSessionRefreshError(w, refreshErr)
				return
			}
			cookie, cookieErr := consoleauth.SessionCookie(proxySessionCookie, credential.Token, consoleauth.StateFull, credential.ExpiresAt)
			if cookieErr != nil {
				writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
				return
			}
			http.SetCookie(w, cookie)
			response = authSessionResponse{
				APIVersion: APIVersion, CSRFToken: credential.CSRFToken,
				ExpiresAt: credential.ExpiresAt, IdleExpiresAt: credential.IdleExpiresAt,
				State: string(StateFull),
			}
		default:
			writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
			return
		}
	default:
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func writeSessionRefreshError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, consoleauth.ErrOriginMismatch), errors.Is(err, consoleauth.ErrRouteNotAllowed):
		writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
	case errors.Is(err, consoleauth.ErrSessionUnauthorized), errors.Is(err, consoleauth.ErrCredentialExpired),
		errors.Is(err, consoleauth.ErrTransactionMismatch), errors.Is(err, consoleauth.ErrStateMismatch):
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
	default:
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
	}
}

func (h *handler) loginLocal(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.auth == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	origin, ok := requireWriteOriginAndPreAuthCSRF(w, r)
	if !ok {
		return
	}
	clientKey := directClientKey(r)
	if !allowAuthAttempt(w, h.authHTTP.loginPerClient, clientKey) || !allowAuthAttempt(w, h.authHTTP.loginGlobal, "global") {
		return
	}
	select {
	case h.authHTTP.loginWork <- struct{}{}:
		defer func() { <-h.authHTTP.loginWork }()
	default:
		w.Header().Set("Retry-After", "1")
		writeProblem(w, http.StatusTooManyRequests, "login_rate_limited", "too many login attempts")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decodeAuthJSON(w, r, &body) {
		return
	}
	credential, err := h.auth.LoginLocal(r.Context(), consoleauth.LocalLoginRequest{Password: body.Password, Origin: origin})
	body.Password = ""
	if err != nil {
		if errors.Is(err, consoleauth.ErrInvalidCredentials) || errors.Is(err, consoleauth.ErrOwnerNotConfigured) {
			writeProblem(w, http.StatusUnauthorized, "invalid_credentials", "local credentials are invalid")
			return
		}
		if errors.Is(err, consoleauth.ErrOriginMismatch) {
			writeProblem(w, http.StatusForbidden, "origin_mismatch", "request origin is not allowed")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	cookie, err := consoleauth.SessionCookie(localSessionCookie, credential.Token, consoleauth.StateFull, credential.ExpiresAt)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
		return
	}
	http.SetCookie(w, cookie)
	state, _ := ConsoleStateFromContext(r.Context())
	http.SetCookie(w, preAuthCookie("", state, time.Unix(1, 0), true, r.TLS != nil))
	writeJSON(w, http.StatusOK, authSessionResponse{
		APIVersion: APIVersion, CSRFToken: credential.CSRFToken,
		ExpiresAt: credential.ExpiresAt, IdleExpiresAt: credential.IdleExpiresAt,
		State: string(StateFull),
	})
}

func (h *handler) logoutLocal(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.auth == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	sessionToken, ok := uniqueCookieValue(r, localSessionCookie)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return
	}
	origin, ok := requireSameRequestOrigin(w, r)
	if !ok {
		return
	}
	if err := h.auth.LogoutLocal(r.Context(), consoleauth.LocalLogoutRequest{
		SessionToken: sessionToken,
		CSRFToken:    r.Header.Get(csrfHeaderName),
		Origin:       origin,
	}); err != nil {
		if errors.Is(err, consoleauth.ErrCSRFMismatch) || errors.Is(err, consoleauth.ErrOriginMismatch) {
			writeProblem(w, http.StatusForbidden, "csrf_mismatch", "request CSRF or origin validation failed")
			return
		}
		if errors.Is(err, consoleauth.ErrSessionUnauthorized) || errors.Is(err, consoleauth.ErrCredentialExpired) {
			writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
			return
		}
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		return
	}
	expired, _ := consoleauth.ExpiredSessionCookie(localSessionCookie, consoleauth.StateFull)
	http.SetCookie(w, expired)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) issueLocalStepUp(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if h.deploymentHTTP == nil || h.deploymentHTTP.stepUp == nil {
		writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "step-up authentication is unavailable")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.Role != "owner" {
		writeProblem(w, http.StatusForbidden, "step_up_source_invalid", "this authentication source cannot issue a step-up proof")
		return
	}
	cookieName := localSessionCookie
	if principal.Source == "oidc_proxy" {
		cookieName = proxySessionCookie
	}
	sessionToken, ok := uniqueCookieValue(r, cookieName)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return
	}
	origin, ok := requireSameRequestOrigin(w, r)
	if !ok {
		return
	}
	clientKey := directClientKey(r)
	if principal.Source == "oidc_proxy" {
		clientKey = principal.ID
	}
	if !allowAuthAttempt(w, h.authHTTP.stepUpPerClient, clientKey) || !allowAuthAttempt(w, h.authHTTP.stepUpGlobal, "global") {
		return
	}
	select {
	case h.authHTTP.loginWork <- struct{}{}:
		defer func() { <-h.authHTTP.loginWork }()
	default:
		w.Header().Set("Retry-After", "1")
		writeProblem(w, http.StatusTooManyRequests, "authentication_rate_limited", "too many authentication attempts")
		return
	}
	var body localStepUpRequest
	if !decodeAuthJSON(w, r, &body) {
		return
	}
	if body.Action != deploymentaudit.ActionApply || body.WorkspaceID == "" || len(body.WorkspaceID) > 256 ||
		body.DeploymentID != "" && (utf8.RuneCountInString(body.DeploymentID) > 255 || deployment.ValidateID(body.DeploymentID) != nil) {
		body.Password = ""
		writeProblem(w, http.StatusBadRequest, "step_up_request_invalid", "step-up action or target is invalid")
		return
	}
	workspacePath, registered := h.registry.Resolve(body.WorkspaceID)
	if !registered {
		body.Password = ""
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	service := h.deploymentHTTP.planFactory(workspacePath)
	if service == nil {
		body.Password = ""
		writeProblem(w, http.StatusServiceUnavailable, "deployment_unavailable", "deployment planning is unavailable")
		return
	}
	plan, err := service.Plan(r.Context(), application.PlanRequest{})
	if err != nil {
		body.Password = ""
		writeApplicationError(w, err)
		return
	}
	if !validDeploymentPlanBinding(plan.ConfigValidator, plan.Digest) {
		body.Password = ""
		writeProblem(w, http.StatusInternalServerError, "plan_binding_invalid", "deployment plan is unavailable")
		return
	}
	stateDigest := deploymentStepUpStateDigest(body.WorkspaceID, body.DeploymentID, plan.ConfigValidator, plan.Digest)
	var credential consoleauth.LocalStepUpCredential
	switch principal.Source {
	case "local":
		if principal.ID != consolejobs.PrincipalLocalOwner || body.Password == "" {
			body.Password = ""
			writeProblem(w, http.StatusBadRequest, "step_up_request_invalid", "local step-up requires the current local password")
			return
		}
		credential, err = h.deploymentHTTP.stepUp.IssueLocalStepUp(r.Context(), consoleauth.LocalStepUpRequest{
			SessionToken: sessionToken, CSRFToken: r.Header.Get(csrfHeaderName), Origin: origin, Password: body.Password,
			Action: body.Action, WorkspaceID: body.WorkspaceID, TargetID: body.DeploymentID, StateDigest: stateDigest,
		})
	case "oidc_proxy":
		proxyStepUp, available := h.deploymentHTTP.stepUp.(ProxyDeploymentStepUpAuthenticator)
		if !available || body.Password != "" {
			body.Password = ""
			writeProblem(w, http.StatusForbidden, "step_up_source_invalid", "proxy step-up does not accept a password")
			return
		}
		credential, err = proxyStepUp.IssueProxyStepUp(r.Context(), consoleauth.ProxyStepUpRequest{
			SessionToken: sessionToken, CSRFToken: r.Header.Get(csrfHeaderName), Origin: origin,
			Identity: proxyIdentityFromPrincipal(principal), Action: body.Action, WorkspaceID: body.WorkspaceID,
			TargetID: body.DeploymentID, StateDigest: stateDigest,
		})
	default:
		writeProblem(w, http.StatusForbidden, "step_up_source_invalid", "this authentication source cannot issue a step-up proof")
		return
	}
	body.Password = ""
	if err != nil {
		switch {
		case errors.Is(err, consoleauth.ErrInvalidCredentials), errors.Is(err, consoleauth.ErrOwnerNotConfigured):
			writeProblem(w, http.StatusUnauthorized, "invalid_credentials", "local credentials are invalid")
		case errors.Is(err, consoleauth.ErrSessionUnauthorized), errors.Is(err, consoleauth.ErrCredentialExpired):
			writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		case errors.Is(err, consoleauth.ErrCSRFMismatch), errors.Is(err, consoleauth.ErrOriginMismatch):
			writeProblem(w, http.StatusForbidden, "csrf_mismatch", "request CSRF or origin validation failed")
		case errors.Is(err, consoleauth.ErrStepUpUnauthorized) && principal.Source == "oidc_proxy":
			writeProblem(w, http.StatusPreconditionRequired, "recent_auth_required", "a new identity-provider authentication is required")
		case errors.Is(err, consoleauth.ErrStepUpUnauthorized):
			writeProblem(w, http.StatusBadRequest, "step_up_request_invalid", "step-up action or target is invalid")
		default:
			writeProblem(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication is unavailable")
		}
		return
	}
	writeJSON(w, http.StatusOK, localStepUpResponse{
		APIVersion: APIVersion, Proof: credential.Token, ExpiresAt: credential.ExpiresAt,
		Action: credential.Action, WorkspaceID: credential.WorkspaceID, DeploymentID: credential.TargetID,
	})
}

// LocalOwnerAuthorizer authenticates host-only local-session cookies and does
// not inspect Authorization or proxy identity headers. Writes additionally
// require the exact request Origin and the session-bound CSRF header.
func LocalOwnerAuthorizer(auth ConsoleAuthenticator) func(*http.Request, AuthorizationRequest) (Principal, error) {
	return func(request *http.Request, authorization AuthorizationRequest) (Principal, error) {
		if auth == nil {
			return Principal{}, errors.New("console authenticator is unavailable")
		}
		sessionToken, ok := uniqueCookieValue(request, localSessionCookie)
		if !ok {
			return Principal{}, ErrUnauthenticated
		}
		origin, err := canonicalRequestOrigin(request)
		if err != nil {
			return Principal{}, ErrUnauthenticated
		}
		requireCSRF := request.Method != http.MethodGet && request.Method != http.MethodHead
		if requireCSRF {
			var valid bool
			origin, valid = exactOriginHeader(request, origin)
			if !valid {
				return Principal{}, ErrForbidden
			}
		}
		_, err = auth.AuthenticateLocal(request.Context(), consoleauth.LocalAuthenticationRequest{
			SessionToken: sessionToken,
			CSRFToken:    request.Header.Get(csrfHeaderName),
			Origin:       origin,
			RequireCSRF:  requireCSRF,
			ObserveOnly:  authorization.ObserveOnly,
		})
		switch {
		case errors.Is(err, consoleauth.ErrCSRFMismatch), errors.Is(err, consoleauth.ErrOriginMismatch):
			return Principal{}, ErrForbidden
		case errors.Is(err, consoleauth.ErrSessionUnauthorized), errors.Is(err, consoleauth.ErrCredentialExpired):
			return Principal{}, ErrUnauthenticated
		case err != nil:
			return Principal{}, err
		default:
			return Principal{ID: "local-owner", Role: "owner", Source: "local"}, nil
		}
	}
}

// DirectSessionAuthorizer dispatches authentication by the route's declared
// policy. It never infers bootstrap privileges from a path and always passes
// the stable route pattern plus current capability state and transaction to
// the credential store.
func DirectSessionAuthorizer(auth DirectAuthenticator) func(*http.Request, AuthorizationRequest) (Principal, error) {
	local := LocalOwnerAuthorizer(auth)
	return func(request *http.Request, authorization AuthorizationRequest) (Principal, error) {
		state, ok := ConsoleStateFromContext(request.Context())
		if !ok {
			return Principal{}, errors.New("console capability state is unavailable")
		}
		access, ok := authorization.Policy.Access[state]
		if !ok {
			return Principal{}, ErrForbidden
		}
		switch access.Authentication {
		case AuthenticationOwner:
			return local(request, authorization)
		case AuthenticationBootstrap:
			return authorizeBootstrapSession(request, authorization, state, auth)
		case AuthenticationEnrollment:
			return authorizeEnrollmentSession(request, authorization, state, auth)
		default:
			return Principal{}, ErrForbidden
		}
	}
}

func authorizeBootstrapSession(request *http.Request, authorization AuthorizationRequest, state ConsoleState, auth BootstrapSessionAuthenticator) (Principal, error) {
	if state != StateBootstrap && state != StateEnrollment {
		return Principal{}, ErrUnauthenticated
	}
	sessionToken, ok := uniqueCookieValue(request, bootstrapSessionCookie)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	origin, err := canonicalRequestOrigin(request)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	requireCSRF := request.Method != http.MethodGet && request.Method != http.MethodHead
	if requireCSRF {
		var valid bool
		origin, valid = exactOriginHeader(request, origin)
		if !valid {
			return Principal{}, ErrForbidden
		}
	}
	authState := consoleauth.StateBootstrap
	if state == StateEnrollment {
		authState = consoleauth.StateEnrollment
	}
	transactionID, err := auth.CurrentBootstrapTransaction(request.Context(), authState)
	if err != nil {
		return Principal{}, mapBootstrapAuthorizationError(err)
	}
	principal, err := auth.AuthenticateBootstrap(request.Context(), consoleauth.BootstrapAuthenticationRequest{
		SessionToken: sessionToken, CSRFToken: request.Header.Get(csrfHeaderName),
		Origin: origin, TransactionID: transactionID, State: authState,
		Route: authorization.Policy.Pattern, RequireCSRF: requireCSRF,
		ObserveOnly: authorization.ObserveOnly,
	})
	if err != nil {
		return Principal{}, mapBootstrapAuthorizationError(err)
	}
	return Principal{
		ID: "bootstrap:" + principal.TransactionID, Role: "bootstrap",
		Source: "bootstrap", TransactionID: principal.TransactionID,
	}, nil
}

func authorizeEnrollmentSession(request *http.Request, authorization AuthorizationRequest, state ConsoleState, auth EnrollmentSessionAuthenticator) (Principal, error) {
	if state != StateEnrollment {
		return Principal{}, ErrUnauthenticated
	}
	sessionToken, ok := uniqueCookieValue(request, consoleauth.EnrollmentSessionCookieName)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	origin, err := canonicalRequestOrigin(request)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	requireCSRF := request.Method != http.MethodGet && request.Method != http.MethodHead
	csrfToken := ""
	if requireCSRF {
		var valid bool
		origin, valid = exactOriginHeader(request, origin)
		if !valid {
			return Principal{}, ErrForbidden
		}
		csrfToken, valid = enrollmentDoubleSubmitCSRF(request)
		if !valid {
			return Principal{}, ErrForbidden
		}
	}
	transactionID, err := auth.CurrentEnrollmentTransaction(request.Context())
	if err != nil {
		return Principal{}, mapBootstrapAuthorizationError(err)
	}
	principal, err := auth.AuthenticateEnrollment(request.Context(), consoleauth.EnrollmentAuthenticationRequest{
		SessionToken: sessionToken, CSRFToken: csrfToken, Origin: origin,
		TransactionID: transactionID, Route: authorization.Policy.Pattern, RequireCSRF: requireCSRF,
	})
	if err != nil {
		return Principal{}, mapBootstrapAuthorizationError(err)
	}
	return Principal{
		ID: "enrollment:" + principal.TransactionID, Role: "enrollment",
		Source: "enrollment", TransactionID: principal.TransactionID,
	}, nil
}

func enrollmentDoubleSubmitCSRF(request *http.Request) (string, bool) {
	cookieValue, ok := uniqueCookieValue(request, consoleauth.EnrollmentCSRFCookieName)
	headerValues := request.Header.Values(csrfHeaderName)
	if !ok || cookieValue == "" || len(headerValues) != 1 || headerValues[0] == "" {
		return "", false
	}
	if subtle.ConstantTimeCompare([]byte(cookieValue), []byte(headerValues[0])) != 1 {
		return "", false
	}
	return headerValues[0], true
}

func mapBootstrapAuthorizationError(err error) error {
	switch {
	case errors.Is(err, consoleauth.ErrCSRFMismatch),
		errors.Is(err, consoleauth.ErrOriginMismatch),
		errors.Is(err, consoleauth.ErrRouteNotAllowed),
		errors.Is(err, consoleauth.ErrTransactionMismatch),
		errors.Is(err, consoleauth.ErrStateMismatch):
		return ErrForbidden
	case errors.Is(err, consoleauth.ErrSessionUnauthorized),
		errors.Is(err, consoleauth.ErrCredentialExpired):
		return ErrUnauthenticated
	default:
		return err
	}
}

func requireWriteOriginAndPreAuthCSRF(w http.ResponseWriter, r *http.Request) (string, bool) {
	origin, ok := requireSameRequestOrigin(w, r)
	if !ok {
		return "", false
	}
	cookieValue, ok := uniqueCookieValue(r, preAuthCSRFCookieName)
	headerValue := r.Header.Get(csrfHeaderName)
	if !ok || cookieValue == "" || headerValue == "" || subtle.ConstantTimeCompare([]byte(cookieValue), []byte(headerValue)) != 1 {
		writeProblem(w, http.StatusForbidden, "csrf_mismatch", "request CSRF validation failed")
		return "", false
	}
	return origin, true
}

func requireSameRequestOrigin(w http.ResponseWriter, r *http.Request) (string, bool) {
	expected, err := canonicalRequestOrigin(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_origin", "request origin is invalid")
		return "", false
	}
	origin, ok := exactOriginHeader(r, expected)
	if !ok {
		writeProblem(w, http.StatusForbidden, "origin_mismatch", "request origin is not allowed")
		return "", false
	}
	return origin, true
}

func canonicalRequestOrigin(r *http.Request) (string, error) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return consoleauth.NormalizeOrigin(scheme + "://" + r.Host)
}

func exactOriginHeader(r *http.Request, expected string) (string, bool) {
	values := r.Header.Values("Origin")
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false
	}
	origin, err := consoleauth.NormalizeOrigin(values[0])
	return origin, err == nil && values[0] == origin && origin == expected
}

func uniqueCookieValue(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			count++
			value = cookie.Value
		}
	}
	return value, count == 1 && value != ""
}

func preAuthCookie(value string, state ConsoleState, expiresAt time.Time, expired, transportSecure bool) *http.Cookie {
	cookie := &http.Cookie{
		Name:     preAuthCSRFCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   transportSecure || state == StateEnrollment || state == StateFull,
	}
	if expired {
		cookie.MaxAge = -1
	}
	return cookie
}

func randomURLCredential(source io.Reader) (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func allowAuthAttempt(w http.ResponseWriter, limiter *attemptLimiter, key string) bool {
	allowed, retry := limiter.allow(key)
	if allowed {
		return true
	}
	seconds := int(math.Ceil(retry.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
	writeProblem(w, http.StatusTooManyRequests, "authentication_rate_limited", "too many authentication attempts")
	return false
}

func decodeAuthJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximumAuthRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		} else {
			writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		}
		return false
	}
	defer clear(body)
	if validateUniqueJSONKeys(body) != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must be one valid JSON object")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}
