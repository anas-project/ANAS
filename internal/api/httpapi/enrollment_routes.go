package httpapi

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/consoleauth"
)

type EnrollmentWorkflow interface {
	IssueEnrollmentHandoff(context.Context, consoleauth.IssueEnrollmentHandoffRequest) (consoleauth.EnrollmentHandoffCredential, error)
	ExchangeEnrollmentHandoff(context.Context, consoleauth.ExchangeEnrollmentHandoffRequest) (consoleauth.EnrollmentSessionCredential, error)
	CompleteInitialOwner(context.Context, consoleauth.CompleteInitialOwnerRequest, func(context.Context) error) error
}

type EnrollmentTarget struct {
	Origin     string
	SPKISHA256 string
}

type EnrollmentOptions struct {
	Workflow                EnrollmentWorkflow
	CurrentTarget           func(context.Context) (EnrollmentTarget, error)
	CurrentConnectionTarget func(context.Context) (EnrollmentTarget, error)
	CompleteTransition      func(context.Context, string) error
}

func (options EnrollmentOptions) validate() error {
	if options.Workflow == nil || options.CurrentTarget == nil || options.CurrentConnectionTarget == nil || options.CompleteTransition == nil {
		return errors.New("enrollment workflow, target providers, and state transition are required")
	}
	return nil
}

type enrollmentHandoffResponse struct {
	APIVersion   string    `json:"api_version"`
	Handoff      string    `json:"handoff"`
	TargetOrigin string    `json:"target_origin"`
	FormAction   string    `json:"form_action"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type enrollmentOwnerResponse struct {
	APIVersion string `json:"api_version"`
	State      string `json:"state"`
}

func (h *handler) issueEnrollmentHandoff(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.enrollment == nil {
		writeProblem(w, http.StatusServiceUnavailable, "enrollment_unavailable", "enrollment is unavailable")
		return
	}
	if r.ContentLength > 0 || r.Body != nil && r.ContentLength != 0 {
		writeProblem(w, http.StatusBadRequest, "unexpected_body", "handoff issuance does not accept a request body")
		return
	}
	sourceOrigin, ok := requireSameRequestOrigin(w, r)
	if !ok {
		return
	}
	target, ok := h.currentEnrollmentTarget(w, r)
	if !ok {
		return
	}
	sessionToken, ok := uniqueCookieValue(r, bootstrapSessionCookie)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
		return
	}
	credential, err := h.enrollment.Workflow.IssueEnrollmentHandoff(r.Context(), consoleauth.IssueEnrollmentHandoffRequest{
		SessionToken: sessionToken, CSRFToken: r.Header.Get(csrfHeaderName),
		SourceOrigin: sourceOrigin, TargetOrigin: target.Origin, SPKISHA256: target.SPKISHA256,
	})
	if err != nil {
		writeEnrollmentError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, enrollmentHandoffResponse{
		APIVersion: APIVersion, Handoff: credential.Token, TargetOrigin: credential.TargetOrigin,
		FormAction: credential.TargetOrigin + consoleauth.EnrollmentHandoffExchangeRoute,
		ExpiresAt:  credential.ExpiresAt,
	})
}

func (h *handler) exchangeEnrollmentHandoff(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.enrollment == nil {
		writeProblem(w, http.StatusServiceUnavailable, "enrollment_unavailable", "enrollment is unavailable")
		return
	}
	if len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Authorization")) != 0 {
		writeProblem(w, http.StatusBadRequest, "handoff_credentials_forbidden", "handoff exchange does not accept cookies or authorization headers")
		return
	}
	sourceOrigin, ok := exactCanonicalOriginHeader(r)
	if !ok {
		writeProblem(w, http.StatusForbidden, "origin_mismatch", "handoff source Origin is invalid")
		return
	}
	target, ok := h.currentEnrollmentConnectionTarget(w, r)
	if !ok {
		return
	}
	requestOrigin, err := canonicalRequestOrigin(r)
	if err != nil || requestOrigin != target.Origin {
		writeProblem(w, http.StatusForbidden, "handoff_target_mismatch", "handoff target origin does not match this request")
		return
	}
	handoff, ok := decodeHandoffForm(w, r)
	if !ok {
		return
	}
	credential, err := h.enrollment.Workflow.ExchangeEnrollmentHandoff(r.Context(), consoleauth.ExchangeEnrollmentHandoffRequest{
		Token: handoff, SourceOrigin: sourceOrigin, TargetOrigin: target.Origin, SPKISHA256: target.SPKISHA256,
	})
	handoff = ""
	if err != nil {
		writeEnrollmentError(w, err)
		return
	}
	cookie, err := consoleauth.SessionCookie(consoleauth.EnrollmentSessionCookieName, credential.Token, consoleauth.StateEnrollment, credential.ExpiresAt)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
		return
	}
	csrfCookie, err := consoleauth.EnrollmentCSRFCookie(credential.CSRFToken, credential.ExpiresAt)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
		return
	}
	http.SetCookie(w, cookie)
	http.SetCookie(w, csrfCookie)
	w.Header().Set("Location", target.Origin+"/")
	w.WriteHeader(http.StatusSeeOther)
}

func (h *handler) createEnrollmentOwner(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if h.enrollment == nil {
		writeProblem(w, http.StatusServiceUnavailable, "enrollment_unavailable", "enrollment is unavailable")
		return
	}
	origin, ok := requireSameRequestOrigin(w, r)
	if !ok {
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.Source != "enrollment" || principal.TransactionID == "" {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "enrollment authentication is required")
		return
	}
	sessionToken, ok := uniqueCookieValue(r, consoleauth.EnrollmentSessionCookieName)
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "unauthenticated", "enrollment authentication is required")
		return
	}
	csrfToken, ok := enrollmentDoubleSubmitCSRF(r)
	if !ok {
		writeProblem(w, http.StatusForbidden, "csrf_mismatch", "request CSRF validation failed")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decodeAuthJSON(w, r, &body) {
		return
	}
	err := h.enrollment.Workflow.CompleteInitialOwner(r.Context(), consoleauth.CompleteInitialOwnerRequest{
		SessionToken: sessionToken, CSRFToken: csrfToken, Origin: origin,
		TransactionID: principal.TransactionID, Password: body.Password,
	}, func(ctx context.Context) error {
		return h.enrollment.CompleteTransition(ctx, principal.TransactionID)
	})
	body.Password = ""
	if err != nil {
		writeEnrollmentError(w, err)
		return
	}
	enrollmentExpired, _ := consoleauth.ExpiredSessionCookie(consoleauth.EnrollmentSessionCookieName, consoleauth.StateEnrollment)
	bootstrapExpired, _ := consoleauth.ExpiredSessionCookie(consoleauth.BootstrapSessionCookieName, consoleauth.StateEnrollment)
	http.SetCookie(w, enrollmentExpired)
	http.SetCookie(w, consoleauth.ExpiredEnrollmentCSRFCookie())
	http.SetCookie(w, bootstrapExpired)
	writeJSON(w, http.StatusCreated, enrollmentOwnerResponse{APIVersion: APIVersion, State: string(StateFull)})
}

func (h *handler) currentEnrollmentTarget(w http.ResponseWriter, r *http.Request) (EnrollmentTarget, bool) {
	target, err := h.enrollment.CurrentTarget(r.Context())
	return validateEnrollmentTarget(w, target, err)
}

func (h *handler) currentEnrollmentConnectionTarget(w http.ResponseWriter, r *http.Request) (EnrollmentTarget, bool) {
	target, err := h.enrollment.CurrentConnectionTarget(r.Context())
	return validateEnrollmentTarget(w, target, err)
}

func validateEnrollmentTarget(w http.ResponseWriter, target EnrollmentTarget, targetErr error) (EnrollmentTarget, bool) {
	if targetErr != nil {
		writeProblem(w, http.StatusConflict, "enrollment_certificate_not_ready", "a validated enrollment certificate is not ready")
		return EnrollmentTarget{}, false
	}
	origin, err := consoleauth.NormalizeOrigin(target.Origin)
	spki, spkiErr := hex.DecodeString(target.SPKISHA256)
	if err != nil || origin != target.Origin || !strings.HasPrefix(origin, "https://") ||
		spkiErr != nil || len(spki) != 32 || strings.ToLower(target.SPKISHA256) != target.SPKISHA256 {
		writeProblem(w, http.StatusServiceUnavailable, "enrollment_target_invalid", "the enrollment target is invalid")
		return EnrollmentTarget{}, false
	}
	return target, true
}

func exactCanonicalOriginHeader(r *http.Request) (string, bool) {
	values := r.Header.Values("Origin")
	if len(values) != 1 || values[0] == "null" || strings.Contains(values[0], ",") {
		return "", false
	}
	origin, err := consoleauth.NormalizeOrigin(values[0])
	return origin, err == nil && origin == values[0]
}

func decodeHandoffForm(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.URL.RawQuery != "" {
		writeProblem(w, http.StatusBadRequest, "invalid_handoff_form", "handoff must not appear in the URL")
		return "", false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		writeProblem(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "handoff exchange requires form encoding")
		return "", false
	}
	limited := io.LimitReader(r.Body, maximumAuthRequestBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maximumAuthRequestBytes {
		writeProblem(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large")
		return "", false
	}
	values, err := url.ParseQuery(string(body))
	for index := range body {
		body[index] = 0
	}
	if err != nil || len(values) != 1 || len(values["handoff"]) != 1 || values.Get("handoff") == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_handoff_form", "handoff form is invalid")
		return "", false
	}
	return values.Get("handoff"), true
}

func writeEnrollmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, consoleauth.ErrCSRFMismatch), errors.Is(err, consoleauth.ErrOriginMismatch),
		errors.Is(err, consoleauth.ErrRouteNotAllowed), errors.Is(err, consoleauth.ErrTransactionMismatch),
		errors.Is(err, consoleauth.ErrStateMismatch):
		writeProblem(w, http.StatusForbidden, "enrollment_forbidden", "enrollment authorization failed")
	case errors.Is(err, consoleauth.ErrSessionUnauthorized), errors.Is(err, consoleauth.ErrCredentialExpired),
		errors.Is(err, consoleauth.ErrHandoffUnauthorized):
		writeProblem(w, http.StatusUnauthorized, "enrollment_unauthorized", "enrollment credential is invalid or expired")
	case errors.Is(err, consoleauth.ErrRecoveryRequired), errors.Is(err, consoleauth.ErrAuditUnavailable):
		writeProblem(w, http.StatusServiceUnavailable, "enrollment_unavailable", "enrollment state is unavailable")
	default:
		writeProblem(w, http.StatusBadRequest, "owner_enrollment_failed", "owner enrollment could not be completed")
	}
}
