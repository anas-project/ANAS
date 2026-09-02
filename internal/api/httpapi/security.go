package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ConsoleState is the persisted control-plane capability state. StateM0 is a
// migration-only value used by the existing unauthenticated loopback server;
// production M1A configuration starts in StateBootstrap.
type ConsoleState string

const (
	StateM0         ConsoleState = "m0"
	StateBootstrap  ConsoleState = "bootstrap"
	StateEnrollment ConsoleState = "enrollment"
	StateFull       ConsoleState = "full"
)

type ListenerIdentity string

const (
	ListenerDirect       ListenerIdentity = "direct"
	ListenerTrustedProxy ListenerIdentity = "trusted_proxy"
)

type RequestTransport string

const (
	TransportPlaintext RequestTransport = "plaintext"
	TransportTLS       RequestTransport = "tls"
)

type Permission string

const (
	PermissionPublic             Permission = "public"
	PermissionConsoleUI          Permission = "console.ui.read"
	PermissionSystemCA           Permission = "system.ca.read"
	PermissionWorkspaceRead      Permission = "workspace.read"
	PermissionAuthCSRF           Permission = "auth.csrf"
	PermissionAuthSession        Permission = "auth.session.refresh"
	PermissionAuthExchange       Permission = "auth.bootstrap.exchange"
	PermissionAuthLogin          Permission = "auth.local.login"
	PermissionAuthLogout         Permission = "auth.local.logout"
	PermissionAuthStepUp         Permission = "auth.step_up"
	PermissionEnrollmentHandoff  Permission = "auth.enrollment.handoff"
	PermissionEnrollmentExchange Permission = "auth.enrollment.exchange"
	PermissionEnrollmentOwner    Permission = "auth.enrollment.owner"
	PermissionJobList            Permission = "job.list"
	PermissionJobRead            Permission = "job.read"
	PermissionJobEventsRead      Permission = "job.events.read"
	PermissionAuditList          Permission = "audit.list"
	PermissionConfigRead         Permission = "config.read"
	PermissionConfigValidate     Permission = "config.validate"
	PermissionConfigWrite        Permission = "config.write"
	PermissionDeploymentPlan     Permission = "deployment.plan"
	PermissionDeploymentApply    Permission = "deployment.apply"
)

type ObjectScope string

const (
	ScopeService   ObjectScope = "service"
	ScopeWorkspace ObjectScope = "workspace"
)

type Authentication string

const (
	AuthenticationNone       Authentication = "none"
	AuthenticationBootstrap  Authentication = "bootstrap"
	AuthenticationEnrollment Authentication = "enrollment"
	AuthenticationOwner      Authentication = "owner"
)

type RouteAccess struct {
	Authentication Authentication
	Transports     []RequestTransport
}

type RoutePolicy struct {
	Method     string
	Pattern    string
	Permission Permission
	Scope      ObjectScope
	Listeners  []ListenerIdentity
	Access     map[ConsoleState]RouteAccess
}

type AuthorizationRequest struct {
	Policy RoutePolicy
	Params map[string]string
	// ObserveOnly asks credential-backed authorizers to validate without
	// extending an idle session. It is reserved for periodic checks on an
	// already-established long-lived response such as an SSE stream.
	ObserveOnly bool
}

type Principal struct {
	ID                string
	Role              string
	Source            string
	TransactionID     string
	Issuer            string
	Subject           string
	SemanticRole      string
	DirectoryGroup    string
	AuthenticatedAt   time.Time
	IdentityExpiresAt time.Time
	AssertionDigest   string
	SessionDigest     string
}

var (
	ErrUnauthenticated = errors.New("request is unauthenticated")
	ErrForbidden       = errors.New("request is forbidden")
)

type SecurityOptions struct {
	State        func(context.Context) (ConsoleState, error)
	HostAllowed  func(*http.Request) bool
	Listener     ListenerIdentity
	Authorize    func(*http.Request, AuthorizationRequest) (Principal, error)
	InitialState ConsoleState
}

func legacySecurityOptions() SecurityOptions {
	return SecurityOptions{
		InitialState: StateM0,
		HostAllowed: func(r *http.Request) bool {
			return validLoopbackHost(r.Host)
		},
		Listener: ListenerDirect,
		Authorize: func(_ *http.Request, _ AuthorizationRequest) (Principal, error) {
			return Principal{ID: "m0-loopback", Role: "owner"}, nil
		},
	}
}

func normalizeSecurityOptions(options SecurityOptions) (SecurityOptions, error) {
	if options.State == nil {
		state := options.InitialState
		if state == "" {
			state = StateBootstrap
		}
		options.State = func(context.Context) (ConsoleState, error) { return state, nil }
	}
	if options.HostAllowed == nil {
		return SecurityOptions{}, fmt.Errorf("Host policy is required")
	}
	if options.Listener == "" {
		options.Listener = ListenerDirect
	}
	if options.Listener != ListenerDirect && options.Listener != ListenerTrustedProxy {
		return SecurityOptions{}, fmt.Errorf("unsupported listener identity %q", options.Listener)
	}
	if options.Authorize == nil {
		options.Authorize = func(_ *http.Request, request AuthorizationRequest) (Principal, error) {
			if access, ok := request.Policy.Access[StateM0]; ok && access.Authentication == AuthenticationNone {
				return Principal{ID: "m0-loopback", Role: "owner"}, nil
			}
			return Principal{}, ErrUnauthenticated
		}
	}
	return options, nil
}

func validateRoutePolicy(policy RoutePolicy) error {
	if policy.Method == "" || !strings.HasPrefix(policy.Pattern, "/") {
		return fmt.Errorf("route method and absolute pattern are required")
	}
	if policy.Permission == "" {
		return fmt.Errorf("route %s %s does not declare a permission", policy.Method, policy.Pattern)
	}
	if policy.Scope == "" {
		return fmt.Errorf("route %s %s does not declare an object scope", policy.Method, policy.Pattern)
	}
	if len(policy.Listeners) == 0 {
		return fmt.Errorf("route %s %s does not declare allowed listener identities", policy.Method, policy.Pattern)
	}
	for _, listener := range policy.Listeners {
		if listener != ListenerDirect && listener != ListenerTrustedProxy {
			return fmt.Errorf("route %s %s has invalid listener identity %q", policy.Method, policy.Pattern, listener)
		}
	}
	if len(policy.Access) == 0 {
		return fmt.Errorf("route %s %s does not declare allowed states", policy.Method, policy.Pattern)
	}
	for state, access := range policy.Access {
		if state != StateM0 && state != StateBootstrap && state != StateEnrollment && state != StateFull {
			return fmt.Errorf("route %s %s declares unknown state %q", policy.Method, policy.Pattern, state)
		}
		if access.Authentication != AuthenticationNone && access.Authentication != AuthenticationBootstrap &&
			access.Authentication != AuthenticationEnrollment && access.Authentication != AuthenticationOwner {
			return fmt.Errorf("route %s %s has no authentication policy for %s", policy.Method, policy.Pattern, state)
		}
		if len(access.Transports) == 0 {
			return fmt.Errorf("route %s %s has no transport policy for %s", policy.Method, policy.Pattern, state)
		}
		for _, transport := range access.Transports {
			if transport != TransportPlaintext && transport != TransportTLS {
				return fmt.Errorf("route %s %s has invalid transport %q", policy.Method, policy.Pattern, transport)
			}
		}
	}
	return nil
}

func requestTransport(r *http.Request) RequestTransport {
	if r.TLS != nil {
		return TransportTLS
	}
	return TransportPlaintext
}

func allowsTransport(access RouteAccess, transport RequestTransport) bool {
	for _, allowed := range access.Transports {
		if allowed == transport {
			return true
		}
	}
	return false
}

var directProxyHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Forwarded-User",
	"X-Forwarded-Email",
	"X-Forwarded-Groups",
	"X-Forwarded-Preferred-Username",
	"X-Forwarded-Subject",
	"X-Forwarded-Issuer",
	"X-Auth-Request-User",
	"X-Auth-Request-Email",
	"X-Auth-Request-Groups",
	"X-Remote-User",
	"Remote-User",
	"X-Anas-Identity-Issuer",
	"X-Anas-Identity-Subject",
	"X-Anas-Identity-Role",
	"X-Anas-Identity-Group",
	"X-Anas-Identity-Auth-Time",
	"X-Anas-Identity-Expires-At",
	"X-Anas-Identity-Assertion",
}

func stripDirectProxyHeaders(header http.Header) {
	for _, name := range directProxyHeaders {
		header.Del(name)
	}
}

type principalContextKey struct{}
type consoleStateContextKey struct{}

func withPrincipal(r *http.Request, principal Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal))
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

func withConsoleState(r *http.Request, state ConsoleState) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), consoleStateContextKey{}, state))
}

func ConsoleStateFromContext(ctx context.Context) (ConsoleState, bool) {
	state, ok := ctx.Value(consoleStateContextKey{}).(ConsoleState)
	return state, ok
}
