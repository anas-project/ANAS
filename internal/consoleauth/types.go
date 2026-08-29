// Package consoleauth implements the local bootstrap and break-glass
// authentication state used by anasd. Credential values are returned only at
// creation or exchange boundaries; persistent records contain digests.
package consoleauth

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	DefaultBootstrapTokenTTL = 20 * time.Minute
	MinimumBootstrapTokenTTL = 15 * time.Minute
	MaximumBootstrapTokenTTL = 30 * time.Minute

	BootstrapSessionAbsoluteTTL = 2 * time.Hour
	BootstrapSessionIdleTTL     = 30 * time.Minute
	EnrollmentHandoffTTL        = 5 * time.Minute
	EnrollmentSessionTTL        = 15 * time.Minute
	LocalSessionAbsoluteTTL     = 12 * time.Hour
	LocalSessionIdleTTL         = 30 * time.Minute

	credentialRandomBytes = 32
)

var (
	ErrAuditUnavailable    = errors.New("authentication audit is unavailable")
	ErrInvalidToken        = errors.New("credential is invalid")
	ErrCredentialExpired   = errors.New("credential has expired")
	ErrSessionUnauthorized = errors.New("session is unauthorized")
	ErrOriginMismatch      = errors.New("request origin is not authorized")
	ErrCSRFMismatch        = errors.New("CSRF token is invalid")
	ErrRouteNotAllowed     = errors.New("route is not allowed for this session")
	ErrStateMismatch       = errors.New("session state is not authorized")
	ErrTransactionMismatch = errors.New("bootstrap transaction is not authorized")
	ErrOwnerNotConfigured  = errors.New("local owner is not configured")
	ErrInvalidCredentials  = errors.New("local credentials are invalid")
	ErrHandoffUnauthorized = errors.New("enrollment handoff is unauthorized")
	ErrRecoveryRequired    = errors.New("authentication transaction recovery is required")
)

type ConsoleState string

const (
	StateBootstrap  ConsoleState = "bootstrap"
	StateEnrollment ConsoleState = "enrollment"
	StateFull       ConsoleState = "full"
)

type AuditAction string

const (
	AuditBootstrapIssue    AuditAction = "bootstrap_token.issue"
	AuditBootstrapExchange AuditAction = "bootstrap_token.exchange"
	AuditBootstrapAuth     AuditAction = "bootstrap_session.authenticate"
	AuditBootstrapPromote  AuditAction = "bootstrap_credential.promote"
	AuditBootstrapRecover  AuditAction = "bootstrap_credential.promotion_recover"
	AuditBootstrapRevoke   AuditAction = "bootstrap_credential.revoke"
	AuditHandoffIssue      AuditAction = "enrollment_handoff.issue"
	AuditHandoffExchange   AuditAction = "enrollment_handoff.exchange"
	AuditEnrollmentAuth    AuditAction = "enrollment_session.authenticate"
	AuditEnrollmentRevoke  AuditAction = "enrollment_credential.revoke"
	AuditOwnerEnroll       AuditAction = "local_owner.enroll"
	AuditOwnerRecover      AuditAction = "local_owner.enrollment_recover"
	AuditOwnerPasswordSet  AuditAction = "local_owner.password_set"
	AuditLocalLogin        AuditAction = "local_owner.login"
	AuditLocalLogout       AuditAction = "local_session.logout"
	AuditLocalRevoke       AuditAction = "local_session.revoke"
)

type AuditOutcome string

const (
	AuditSuccess AuditOutcome = "success"
	AuditFailure AuditOutcome = "failure"
)

// AuditEvent deliberately has no generic payload map. Its closed fields make
// it difficult for callers to accidentally include a token, password, session,
// CSRF value, or password hash.
type AuditEvent struct {
	Action          AuditAction
	Outcome         AuditOutcome
	OccurredAt      time.Time
	Reason          string
	TransactionID   string
	Origin          string
	TargetOrigin    string
	State           ConsoleState
	ReplacedToken   bool
	RevokedSessions int
}

type AuditSink interface {
	Record(context.Context, AuditEvent) error
}

type AuditSinkFunc func(context.Context, AuditEvent) error

func (function AuditSinkFunc) Record(ctx context.Context, event AuditEvent) error {
	return function(ctx, event)
}

// StoreOptions contains injectable process-local dependencies. A zero Random
// or Now uses crypto/rand.Reader or time.Now. Production composition should
// provide CurrentState so an ambiguous state-transition error can be resolved
// without waiting for startup recovery.
type StoreOptions struct {
	Random       io.Reader
	Now          func() time.Time
	CurrentState func(context.Context) (ConsoleState, error)
}

type IssueBootstrapTokenRequest struct {
	TTL           time.Duration
	TransactionID string
	State         ConsoleState
	AllowedRoutes []string
}

type IssuedBootstrapToken struct {
	Token         string
	IssuedAt      time.Time
	ExpiresAt     time.Time
	TransactionID string
	State         ConsoleState
}

type ExchangeBootstrapTokenRequest struct {
	Token  string
	Origin string
}

type BootstrapSessionCredential struct {
	Token         string
	CSRFToken     string
	TransactionID string
	Origin        string
	State         ConsoleState
	AllowedRoutes []string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

type BootstrapAuthenticationRequest struct {
	SessionToken  string
	CSRFToken     string
	Origin        string
	TransactionID string
	State         ConsoleState
	Route         string
	RequireCSRF   bool
	// ObserveOnly validates the current session without extending its idle
	// expiry. Long-lived transports use it for periodic authorization checks;
	// ordinary request authentication leaves it false and records activity.
	ObserveOnly bool
}

type BootstrapPrincipal struct {
	TransactionID string
	Origin        string
	State         ConsoleState
	AllowedRoutes []string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

type IssueEnrollmentHandoffRequest struct {
	SessionToken string
	CSRFToken    string
	SourceOrigin string
	TargetOrigin string
	SPKISHA256   string
}

type EnrollmentHandoffCredential struct {
	Token         string
	TransactionID string
	SourceOrigin  string
	TargetOrigin  string
	SPKISHA256    string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type ExchangeEnrollmentHandoffRequest struct {
	Token        string
	SourceOrigin string
	TargetOrigin string
	SPKISHA256   string
}

type EnrollmentSessionCredential struct {
	Token         string
	CSRFToken     string
	TransactionID string
	Origin        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type EnrollmentAuthenticationRequest struct {
	SessionToken  string
	CSRFToken     string
	Origin        string
	TransactionID string
	Route         string
	RequireCSRF   bool
}

type EnrollmentPrincipal struct {
	TransactionID string
	Origin        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type CompleteInitialOwnerRequest struct {
	SessionToken  string
	CSRFToken     string
	Origin        string
	TransactionID string
	Password      string
}

type LocalLoginRequest struct {
	Password string
	Origin   string
}

type LocalSessionCredential struct {
	Token         string
	CSRFToken     string
	Origin        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}

type LocalAuthenticationRequest struct {
	SessionToken string
	CSRFToken    string
	Origin       string
	RequireCSRF  bool
	// ObserveOnly validates the current session without extending its idle
	// expiry. Long-lived transports use it for periodic authorization checks;
	// ordinary request authentication leaves it false and records activity.
	ObserveOnly bool
}

type LocalLogoutRequest struct {
	SessionToken string
	CSRFToken    string
	Origin       string
}

type LocalPrincipal struct {
	Origin        string
	CreatedAt     time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}
