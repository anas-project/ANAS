package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

const proxySessionCookie = consoleauth.ProxySessionCookieName

const (
	proxyIssuerHeader    = "X-Anas-Identity-Issuer"
	proxySubjectHeader   = "X-Anas-Identity-Subject"
	proxyRoleHeader      = "X-Anas-Identity-Role"
	proxyGroupHeader     = "X-Anas-Identity-Group"
	proxyAuthTimeHeader  = "X-Anas-Identity-Auth-Time"
	proxyExpiresHeader   = "X-Anas-Identity-Expires-At"
	proxyAssertionHeader = "X-Anas-Identity-Assertion"
)

type ProxyAuthenticator interface {
	RefreshProxySession(context.Context, consoleauth.ProxySessionRefreshRequest) (consoleauth.ProxySessionCredential, error)
	AuthenticateProxy(context.Context, consoleauth.ProxyAuthenticationRequest) (consoleauth.ProxyPrincipal, error)
}

type TrustedProxyOptions struct {
	Authenticator          ProxyAuthenticator
	ExpectedIssuer         string
	ExpectedDirectoryGroup string
	Now                    func() time.Time
}

// TrustedProxyAuthorizer accepts the fixed identity contract only on the
// separately composed trusted-proxy handler. It rejects duplicate and
// comma-joined values, binds stable issuer+subject to a local proxy session,
// and maps only platform_admin to owner.
func TrustedProxyAuthorizer(options TrustedProxyOptions) (func(*http.Request, AuthorizationRequest) (Principal, error), error) {
	if options.Authenticator == nil {
		return nil, errors.New("trusted proxy authenticator is required")
	}
	if options.ExpectedIssuer == "" || options.ExpectedDirectoryGroup == "" {
		return nil, errors.New("trusted proxy issuer and directory group are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return func(request *http.Request, authorization AuthorizationRequest) (Principal, error) {
		identity, err := parseTrustedProxyIdentity(request.Header, options.ExpectedIssuer, options.ExpectedDirectoryGroup, options.Now().UTC())
		if err != nil {
			return Principal{}, ErrUnauthenticated
		}
		principal := proxyPrincipal(identity)
		if authorization.Policy.Permission == PermissionAuthSession {
			return principal, nil
		}
		sessionToken, ok := uniqueCookieValue(request, proxySessionCookie)
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
		resolved, err := options.Authenticator.AuthenticateProxy(request.Context(), consoleauth.ProxyAuthenticationRequest{
			SessionToken: sessionToken, CSRFToken: request.Header.Get(csrfHeaderName), Origin: origin,
			Identity: identity, RequireCSRF: requireCSRF, ObserveOnly: authorization.ObserveOnly,
		})
		switch {
		case errors.Is(err, consoleauth.ErrCSRFMismatch), errors.Is(err, consoleauth.ErrOriginMismatch):
			return Principal{}, ErrForbidden
		case errors.Is(err, consoleauth.ErrSessionUnauthorized), errors.Is(err, consoleauth.ErrCredentialExpired):
			return Principal{}, ErrUnauthenticated
		case err != nil:
			return Principal{}, err
		}
		principal.SessionDigest = resolved.SessionDigest
		return principal, nil
	}, nil
}

func parseTrustedProxyIdentity(header http.Header, expectedIssuer, expectedGroup string, now time.Time) (consoleauth.ProxyIdentity, error) {
	issuer, err := singleProxyHeader(header, proxyIssuerHeader, 1024)
	if err != nil || issuer != expectedIssuer {
		return consoleauth.ProxyIdentity{}, errors.New("proxy identity issuer is missing or unexpected")
	}
	subject, err := singleProxyHeader(header, proxySubjectHeader, 512)
	if err != nil {
		return consoleauth.ProxyIdentity{}, err
	}
	role, err := singleProxyHeader(header, proxyRoleHeader, 64)
	if err != nil || role != "platform_admin" {
		return consoleauth.ProxyIdentity{}, errors.New("proxy semantic role is unauthorized")
	}
	group, err := singleProxyHeader(header, proxyGroupHeader, 512)
	if err != nil || group != expectedGroup {
		return consoleauth.ProxyIdentity{}, errors.New("proxy directory group is unauthorized")
	}
	authTime, err := proxyUnixTime(header, proxyAuthTimeHeader)
	if err != nil {
		return consoleauth.ProxyIdentity{}, err
	}
	expiresAt, err := proxyUnixTime(header, proxyExpiresHeader)
	if err != nil || !expiresAt.After(now) || !expiresAt.After(authTime) {
		return consoleauth.ProxyIdentity{}, errors.New("proxy identity is expired or has invalid timestamps")
	}
	assertion, err := singleProxyHeader(header, proxyAssertionHeader, 16384)
	if err != nil {
		return consoleauth.ProxyIdentity{}, err
	}
	digest := sha256.Sum256([]byte(assertion))
	return consoleauth.ProxyIdentity{
		Issuer: issuer, Subject: subject, SemanticRole: role, DirectoryGroup: group,
		AuthenticatedAt: authTime, ExpiresAt: expiresAt, AssertionDigest: hex.EncodeToString(digest[:]),
	}, nil
}

func singleProxyHeader(header http.Header, name string, maximum int) (string, error) {
	values := header.Values(name)
	if len(values) != 1 {
		return "", fmt.Errorf("%s must occur exactly once", name)
	}
	value := values[0]
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || strings.ContainsAny(value, ",\r\n\x00") {
		return "", fmt.Errorf("%s is ambiguous or invalid", name)
	}
	return value, nil
}

func proxyUnixTime(header http.Header, name string) (time.Time, error) {
	value, err := singleProxyHeader(header, name, 20)
	if err != nil {
		return time.Time{}, err
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != value || seconds <= 0 {
		return time.Time{}, fmt.Errorf("%s must be canonical Unix seconds", name)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func proxyPrincipal(identity consoleauth.ProxyIdentity) Principal {
	digest := sha256.Sum256([]byte(identity.Issuer + "\x00" + identity.Subject))
	return Principal{
		ID: "oidc:" + hex.EncodeToString(digest[:]), Role: "owner", Source: "oidc_proxy",
		Issuer: identity.Issuer, Subject: identity.Subject, SemanticRole: identity.SemanticRole,
		DirectoryGroup: identity.DirectoryGroup, AuthenticatedAt: identity.AuthenticatedAt,
		IdentityExpiresAt: identity.ExpiresAt, AssertionDigest: identity.AssertionDigest,
	}
}

func proxyIdentityFromPrincipal(principal Principal) consoleauth.ProxyIdentity {
	return consoleauth.ProxyIdentity{
		Issuer: principal.Issuer, Subject: principal.Subject, SemanticRole: principal.SemanticRole,
		DirectoryGroup: principal.DirectoryGroup, AuthenticatedAt: principal.AuthenticatedAt,
		ExpiresAt: principal.IdentityExpiresAt, AssertionDigest: principal.AssertionDigest,
	}
}
