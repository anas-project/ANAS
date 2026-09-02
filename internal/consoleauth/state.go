package consoleauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const stateAPIVersion = "anas.console-auth/v1"

var transactionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type bootstrapStateFile struct {
	APIVersion         string                             `json:"api_version"`
	Token              *bootstrapTokenRecord              `json:"token,omitempty"`
	Sessions           map[string]bootstrapSessionRecord  `json:"sessions"`
	Handoff            *enrollmentHandoffRecord           `json:"enrollment_handoff,omitempty"`
	EnrollmentSessions map[string]enrollmentSessionRecord `json:"enrollment_sessions"`
}

type bootstrapTokenRecord struct {
	Digest        string       `json:"digest"`
	IssuedAt      time.Time    `json:"issued_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	TransactionID string       `json:"transaction_id"`
	State         ConsoleState `json:"state"`
	AllowedRoutes []string     `json:"allowed_routes"`
}

type bootstrapSessionRecord struct {
	CSRFDigest    string       `json:"csrf_digest"`
	TransactionID string       `json:"transaction_id"`
	Origin        string       `json:"origin"`
	State         ConsoleState `json:"state"`
	AllowedRoutes []string     `json:"allowed_routes"`
	CreatedAt     time.Time    `json:"created_at"`
	ExpiresAt     time.Time    `json:"expires_at"`
	IdleExpiresAt time.Time    `json:"idle_expires_at"`
	HandoffIssued bool         `json:"handoff_issued"`
}

type enrollmentHandoffRecord struct {
	Digest        string    `json:"digest"`
	TransactionID string    `json:"transaction_id"`
	SourceOrigin  string    `json:"source_origin"`
	TargetOrigin  string    `json:"target_origin"`
	SPKISHA256    string    `json:"spki_sha256"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type enrollmentSessionRecord struct {
	CSRFDigest    string    `json:"csrf_digest"`
	TransactionID string    `json:"transaction_id"`
	Origin        string    `json:"origin"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type localStateFile struct {
	APIVersion       string                        `json:"api_version"`
	OwnerPasswordPHC string                        `json:"owner_password_phc,omitempty"`
	Sessions         map[string]localSessionRecord `json:"sessions"`
	StepUps          map[string]localStepUpRecord  `json:"step_ups"`
}

type localSessionRecord struct {
	CSRFDigest    string    `json:"csrf_digest"`
	Origin        string    `json:"origin"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	IdleExpiresAt time.Time `json:"idle_expires_at"`
}

type localStepUpRecord struct {
	SessionDigest string    `json:"session_digest"`
	Action        string    `json:"action"`
	WorkspaceID   string    `json:"workspace_id"`
	TargetID      string    `json:"target_id"`
	StateDigest   string    `json:"state_digest"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type proxyStateFile struct {
	APIVersion string                        `json:"api_version"`
	Sessions   map[string]proxySessionRecord `json:"sessions"`
	StepUps    map[string]proxyStepUpRecord  `json:"step_ups"`
}

type proxySessionRecord struct {
	CSRFDigest     string    `json:"csrf_digest"`
	Origin         string    `json:"origin"`
	Issuer         string    `json:"issuer"`
	Subject        string    `json:"subject"`
	SemanticRole   string    `json:"semantic_role"`
	DirectoryGroup string    `json:"directory_group"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	IdleExpiresAt  time.Time `json:"idle_expires_at"`
}

type proxyStepUpRecord struct {
	SessionDigest   string    `json:"session_digest"`
	AssertionDigest string    `json:"assertion_digest"`
	Action          string    `json:"action"`
	WorkspaceID     string    `json:"workspace_id"`
	TargetID        string    `json:"target_id"`
	StateDigest     string    `json:"state_digest"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func newBootstrapState() bootstrapStateFile {
	return bootstrapStateFile{
		APIVersion:         stateAPIVersion,
		Sessions:           map[string]bootstrapSessionRecord{},
		EnrollmentSessions: map[string]enrollmentSessionRecord{},
	}
}

func newLocalState() localStateFile {
	return localStateFile{APIVersion: stateAPIVersion, Sessions: map[string]localSessionRecord{}, StepUps: map[string]localStepUpRecord{}}
}

func newProxyState() proxyStateFile {
	return proxyStateFile{APIVersion: stateAPIVersion, Sessions: map[string]proxySessionRecord{}, StepUps: map[string]proxyStepUpRecord{}}
}

func (store *Store) loadBootstrapState() (bootstrapStateFile, error) {
	state, _, err := store.loadBootstrapStateWithExistence()
	return state, err
}

func (store *Store) loadBootstrapStateWithExistence() (bootstrapStateFile, bool, error) {
	var state bootstrapStateFile
	found, err := readJSONFile(filepath.Join(store.directory, bootstrapFileName), &state)
	if err != nil {
		return bootstrapStateFile{}, false, err
	}
	if !found {
		return newBootstrapState(), false, nil
	}
	if state.EnrollmentSessions == nil {
		state.EnrollmentSessions = map[string]enrollmentSessionRecord{}
	}
	if err := validateBootstrapState(state); err != nil {
		return bootstrapStateFile{}, true, fmt.Errorf("validate authentication state %s: %w", bootstrapFileName, err)
	}
	return state, true, nil
}

func (store *Store) writeBootstrapState(state bootstrapStateFile) error {
	if err := validateBootstrapState(state); err != nil {
		return fmt.Errorf("refuse invalid authentication state %s: %w", bootstrapFileName, err)
	}
	return writeJSONFile(filepath.Join(store.directory, bootstrapFileName), state)
}

func (store *Store) loadLocalState() (localStateFile, error) {
	state, _, err := store.loadLocalStateWithExistence()
	return state, err
}

func (store *Store) loadLocalStateWithExistence() (localStateFile, bool, error) {
	var state localStateFile
	found, err := readJSONFile(filepath.Join(store.directory, localFileName), &state)
	if err != nil {
		return localStateFile{}, false, err
	}
	if !found {
		return newLocalState(), false, nil
	}
	if state.StepUps == nil {
		state.StepUps = map[string]localStepUpRecord{}
	}
	if err := validateLocalState(state); err != nil {
		return localStateFile{}, true, fmt.Errorf("validate authentication state %s: %w", localFileName, err)
	}
	return state, true, nil
}

func (store *Store) writeLocalState(state localStateFile) error {
	if err := validateLocalState(state); err != nil {
		return fmt.Errorf("refuse invalid authentication state %s: %w", localFileName, err)
	}
	return writeJSONFile(filepath.Join(store.directory, localFileName), state)
}

func (store *Store) loadProxyState() (proxyStateFile, error) {
	var state proxyStateFile
	found, err := readJSONFile(filepath.Join(store.directory, proxyFileName), &state)
	if err != nil {
		return proxyStateFile{}, err
	}
	if !found {
		return newProxyState(), nil
	}
	if state.Sessions == nil {
		state.Sessions = map[string]proxySessionRecord{}
	}
	if state.StepUps == nil {
		state.StepUps = map[string]proxyStepUpRecord{}
	}
	if err := validateProxyState(state); err != nil {
		return proxyStateFile{}, fmt.Errorf("validate authentication state %s: %w", proxyFileName, err)
	}
	return state, nil
}

func (store *Store) writeProxyState(state proxyStateFile) error {
	if err := validateProxyState(state); err != nil {
		return fmt.Errorf("refuse invalid authentication state %s: %w", proxyFileName, err)
	}
	return writeJSONFile(filepath.Join(store.directory, proxyFileName), state)
}

func validateBootstrapState(state bootstrapStateFile) error {
	if state.APIVersion != stateAPIVersion {
		return fmt.Errorf("api_version must be %q", stateAPIVersion)
	}
	if state.Sessions == nil {
		return errors.New("sessions must be an object")
	}
	if state.EnrollmentSessions == nil {
		return errors.New("enrollment_sessions must be an object")
	}
	if len(state.Sessions) > 1 {
		return errors.New("bootstrap state contains more than one active session")
	}
	if state.Token != nil && len(state.Sessions) != 0 {
		return errors.New("bootstrap token and session cannot both be active")
	}
	if state.Token != nil {
		if err := validateDigest(state.Token.Digest); err != nil {
			return fmt.Errorf("token digest: %w", err)
		}
		if err := validateTransactionID(state.Token.TransactionID); err != nil {
			return err
		}
		if err := validateBootstrapStateValue(state.Token.State); err != nil {
			return err
		}
		if _, err := normalizeAllowedRoutes(state.Token.AllowedRoutes); err != nil {
			return err
		}
		if state.Token.IssuedAt.IsZero() || state.Token.ExpiresAt.Sub(state.Token.IssuedAt) < MinimumBootstrapTokenTTL || state.Token.ExpiresAt.Sub(state.Token.IssuedAt) > MaximumBootstrapTokenTTL {
			return errors.New("bootstrap token timestamps or TTL are invalid")
		}
	}
	for digest, session := range state.Sessions {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf("session digest: %w", err)
		}
		if err := validateDigest(session.CSRFDigest); err != nil {
			return fmt.Errorf("CSRF digest: %w", err)
		}
		if err := validateTransactionID(session.TransactionID); err != nil {
			return err
		}
		if err := validateBootstrapStateValue(session.State); err != nil {
			return err
		}
		if _, err := normalizeAllowedRoutes(session.AllowedRoutes); err != nil {
			return err
		}
		if normalized, err := NormalizeOrigin(session.Origin); err != nil || normalized != session.Origin {
			return errors.New("bootstrap session origin is invalid or not canonical")
		}
		if err := validateSessionTimes(session.CreatedAt, session.ExpiresAt, session.IdleExpiresAt, BootstrapSessionAbsoluteTTL); err != nil {
			return fmt.Errorf("bootstrap session timestamps: %w", err)
		}
	}
	if len(state.EnrollmentSessions) > 1 {
		return errors.New("bootstrap state contains more than one active enrollment session")
	}
	if state.Handoff != nil && len(state.EnrollmentSessions) != 0 {
		return errors.New("enrollment handoff and session cannot both be active")
	}
	if state.Handoff != nil {
		record := state.Handoff
		if err := validateDigest(record.Digest); err != nil {
			return fmt.Errorf("handoff digest: %w", err)
		}
		if err := validateTransactionID(record.TransactionID); err != nil {
			return err
		}
		if err := validateEnrollmentBinding(record.SourceOrigin, record.TargetOrigin, record.SPKISHA256); err != nil {
			return err
		}
		if record.CreatedAt.IsZero() || record.ExpiresAt.Sub(record.CreatedAt) != EnrollmentHandoffTTL {
			return errors.New("enrollment handoff timestamps or TTL are invalid")
		}
	}
	for digest, record := range state.EnrollmentSessions {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf("session digest: %w", err)
		}
		if err := validateDigest(record.CSRFDigest); err != nil {
			return fmt.Errorf("CSRF digest: %w", err)
		}
		if err := validateTransactionID(record.TransactionID); err != nil {
			return err
		}
		if normalized, err := NormalizeOrigin(record.Origin); err != nil || normalized != record.Origin || !strings.HasPrefix(record.Origin, "https://") {
			return errors.New("enrollment session origin must be canonical HTTPS")
		}
		if record.CreatedAt.IsZero() || record.ExpiresAt.Sub(record.CreatedAt) != EnrollmentSessionTTL {
			return errors.New("enrollment session timestamps or TTL are invalid")
		}
	}
	transaction := ""
	if state.Handoff != nil {
		transaction = state.Handoff.TransactionID
	}
	for _, record := range state.EnrollmentSessions {
		transaction = record.TransactionID
	}
	if transaction != "" {
		matchedSource := false
		for _, session := range state.Sessions {
			if session.TransactionID == transaction && session.State == StateEnrollment && session.HandoffIssued {
				matchedSource = true
			}
		}
		if !matchedSource {
			return errors.New("enrollment credential has no consumed enrollment bootstrap session")
		}
	}
	return nil
}

func validateLocalState(state localStateFile) error {
	if state.APIVersion != stateAPIVersion {
		return fmt.Errorf("api_version must be %q", stateAPIVersion)
	}
	if state.Sessions == nil {
		return errors.New("sessions must be an object")
	}
	if state.StepUps == nil {
		return errors.New("step_ups must be an object")
	}
	if len(state.Sessions) > 1024 {
		return errors.New("local session capacity exceeded")
	}
	if len(state.StepUps) > 1024 {
		return errors.New("local step-up capacity exceeded")
	}
	if state.OwnerPasswordPHC != "" {
		if _, _, err := parsePasswordPHC(state.OwnerPasswordPHC); err != nil {
			return err
		}
	} else if len(state.Sessions) != 0 || len(state.StepUps) != 0 {
		return errors.New("local credentials exist without an owner password")
	}
	for digest, session := range state.Sessions {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf("session digest: %w", err)
		}
		if err := validateDigest(session.CSRFDigest); err != nil {
			return fmt.Errorf("CSRF digest: %w", err)
		}
		if normalized, err := NormalizeOrigin(session.Origin); err != nil || normalized != session.Origin {
			return errors.New("local session origin is invalid or not canonical")
		}
		if err := validateSessionTimes(session.CreatedAt, session.ExpiresAt, session.IdleExpiresAt, LocalSessionAbsoluteTTL); err != nil {
			return fmt.Errorf("local session timestamps: %w", err)
		}
	}
	for digest, stepUp := range state.StepUps {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf("step-up digest: %w", err)
		}
		if err := validateDigest(stepUp.SessionDigest); err != nil {
			return fmt.Errorf("step-up session digest: %w", err)
		}
		if _, exists := state.Sessions[stepUp.SessionDigest]; !exists {
			return errors.New("step-up proof references a missing local session")
		}
		if err := validateLocalStepUpBinding(stepUp.Action, stepUp.WorkspaceID, stepUp.TargetID, stepUp.StateDigest); err != nil {
			return err
		}
		if stepUp.CreatedAt.IsZero() || stepUp.ExpiresAt.Sub(stepUp.CreatedAt) != LocalStepUpTTL {
			return errors.New("local step-up timestamps or TTL are invalid")
		}
	}
	return nil
}

func validateProxyState(state proxyStateFile) error {
	if state.APIVersion != stateAPIVersion || state.Sessions == nil || state.StepUps == nil {
		return errors.New("proxy authentication state is incomplete")
	}
	if len(state.Sessions) > 1024 || len(state.StepUps) > 1024 {
		return errors.New("proxy authentication state capacity exceeded")
	}
	for digest, session := range state.Sessions {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf("proxy session digest: %w", err)
		}
		if err := validateDigest(session.CSRFDigest); err != nil {
			return fmt.Errorf("proxy CSRF digest: %w", err)
		}
		if err := validateProxyIdentity(ProxyIdentity{Issuer: session.Issuer, Subject: session.Subject, SemanticRole: session.SemanticRole, DirectoryGroup: session.DirectoryGroup, AuthenticatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt, AssertionDigest: strings.Repeat("0", 64)}); err != nil {
			return err
		}
		if normalized, err := NormalizeOrigin(session.Origin); err != nil || normalized != session.Origin {
			return errors.New("proxy session origin is invalid or not canonical")
		}
		if session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) || session.ExpiresAt.Sub(session.CreatedAt) > ProxySessionAbsoluteTTL || session.IdleExpiresAt.Before(session.CreatedAt) || session.IdleExpiresAt.After(session.ExpiresAt) {
			return errors.New("proxy session timestamps are invalid")
		}
	}
	for digest, proof := range state.StepUps {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf("proxy step-up digest: %w", err)
		}
		if err := validateDigest(proof.SessionDigest); err != nil {
			return fmt.Errorf("proxy step-up session digest: %w", err)
		}
		if err := validateDigest(proof.AssertionDigest); err != nil {
			return fmt.Errorf("proxy recent-auth assertion digest: %w", err)
		}
		if _, exists := state.Sessions[proof.SessionDigest]; !exists {
			return errors.New("proxy step-up proof references a missing session")
		}
		if err := validateLocalStepUpBinding(proof.Action, proof.WorkspaceID, proof.TargetID, proof.StateDigest); err != nil {
			return err
		}
		if proof.CreatedAt.IsZero() || !proof.ExpiresAt.After(proof.CreatedAt) || proof.ExpiresAt.Sub(proof.CreatedAt) > LocalStepUpTTL {
			return errors.New("proxy step-up timestamps are invalid")
		}
	}
	return nil
}

func validateProxyIdentity(identity ProxyIdentity) error {
	issuer, err := url.Parse(identity.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.String() != identity.Issuer {
		return errors.New("proxy identity issuer must be a canonical HTTPS URL")
	}
	for name, value := range map[string]string{"subject": identity.Subject, "semantic role": identity.SemanticRole, "directory group": identity.DirectoryGroup} {
		if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("proxy identity %s is invalid", name)
		}
	}
	if identity.SemanticRole != "platform_admin" {
		return errors.New("proxy identity semantic role is not authorized")
	}
	if identity.AuthenticatedAt.IsZero() || identity.ExpiresAt.IsZero() || !identity.ExpiresAt.After(identity.AuthenticatedAt) {
		return errors.New("proxy identity timestamps are invalid")
	}
	if err := validateDigest(identity.AssertionDigest); err != nil {
		return fmt.Errorf("proxy identity assertion digest: %w", err)
	}
	return nil
}

func validateLocalStepUpBinding(action, workspaceID, targetID, stateDigest string) error {
	if len(action) == 0 || len(action) > 128 || len(workspaceID) == 0 || len(workspaceID) > 256 || len(targetID) > 256 {
		return errors.New("local step-up action or object binding is invalid")
	}
	if strings.TrimSpace(action) != action || strings.TrimSpace(workspaceID) != workspaceID || strings.TrimSpace(targetID) != targetID {
		return errors.New("local step-up binding contains surrounding whitespace")
	}
	if err := validateDigest(stateDigest); err != nil {
		return fmt.Errorf("local step-up state digest: %w", err)
	}
	return nil
}

func validateSessionTimes(createdAt, expiresAt, idleExpiresAt time.Time, absoluteTTL time.Duration) error {
	if createdAt.IsZero() || expiresAt.Sub(createdAt) != absoluteTTL {
		return errors.New("absolute lifetime is invalid")
	}
	if idleExpiresAt.Before(createdAt) || idleExpiresAt.After(expiresAt) {
		return errors.New("idle expiry is outside the absolute lifetime")
	}
	return nil
}

func validateBootstrapStateValue(state ConsoleState) error {
	if state != StateBootstrap && state != StateEnrollment {
		return errors.New("bootstrap credential state must be bootstrap or enrollment")
	}
	return nil
}

func validateTransactionID(value string) error {
	if !transactionIDPattern.MatchString(value) {
		return errors.New("transaction ID must contain 1-128 ASCII letters, digits, '.', '_' or '-'")
	}
	return nil
}

func normalizeAllowedRoutes(routes []string) ([]string, error) {
	if len(routes) == 0 || len(routes) > 128 {
		return nil, errors.New("allowed routes must contain between 1 and 128 entries")
	}
	normalized := make([]string, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for index, route := range routes {
		if len(route) > 512 || !strings.HasPrefix(route, "/") || strings.ContainsAny(route, "?#") || path.Clean(route) != route {
			return nil, fmt.Errorf("allowed route %d must be an absolute path without query or fragment", index)
		}
		parsed, err := url.ParseRequestURI(route)
		if err != nil || parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != route {
			return nil, fmt.Errorf("allowed route %d is invalid", index)
		}
		if _, exists := seen[route]; exists {
			return nil, fmt.Errorf("allowed routes contains duplicate %q", route)
		}
		seen[route] = struct{}{}
		normalized[index] = route
	}
	return normalized, nil
}

func routeAllowed(routes []string, route string) bool {
	for _, allowed := range routes {
		if subtle.ConstantTimeCompare([]byte(allowed), []byte(route)) == 1 {
			return true
		}
	}
	return false
}

func credentialDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func digestMatches(storedDigest, value string) bool {
	stored, err := hex.DecodeString(storedDigest)
	if err != nil || len(stored) != sha256.Size {
		return false
	}
	candidate := sha256.Sum256([]byte(value))
	return subtle.ConstantTimeCompare(stored, candidate[:]) == 1
}

func validateDigest(value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return errors.New("must be a lowercase SHA-256 digest")
	}
	return nil
}

func cloneRoutes(routes []string) []string {
	return append([]string{}, routes...)
}

func nextIdleExpiry(now, absolute time.Time, ttl time.Duration) time.Time {
	next := now.Add(ttl)
	if next.After(absolute) {
		return absolute
	}
	return next
}
