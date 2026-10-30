package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

// ServiceAPIOptions is independent of console user sessions. The calling
// server enforces user authorization; anasd bounds its service grant.
type ServiceAPIOptions struct {
	Authenticate func(context.Context, string, string, string, string) (string, error)
	Factory      func(string) application.ApplicationCredentialService
	Audit        func(context.Context, ServiceCredentialAuditEvent) error
	MailInstall  func(context.Context, ServiceMailInstallCall) (ServiceMailInstallResult, error)
	MailOutbound func(context.Context, ServiceMailOutboundCall) (ServiceMailOutboundResult, error)
}

// The declared actor is untrusted audit context, never an authorization input.
// This event deliberately cannot carry a password, username, token or URL.
type ServiceCredentialAuditEvent struct {
	ClientID      string
	DeclaredActor string
	RequestID     string
	WorkspaceID   string
	Application   string
}

type serviceCredentialRequest struct {
	Actor     string `json:"actor"`
	RequestID string `json:"request_id,omitempty"`
}

func (h *handler) authorizeService(r *http.Request, request AuthorizationRequest) (Principal, error) {
	if h.serviceAPI == nil || h.serviceAPI.Authenticate == nil {
		return Principal{}, ErrUnauthenticated
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return Principal{}, ErrUnauthenticated
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n,") {
		return Principal{}, ErrUnauthenticated
	}
	id, err := h.serviceAPI.Authenticate(r.Context(), token, string(request.Policy.Permission), request.Params["ws"], request.Params["application"])
	switch {
	case errors.Is(err, serviceauth.ErrUnauthenticated):
		return Principal{}, ErrUnauthenticated
	case errors.Is(err, serviceauth.ErrForbidden):
		return Principal{}, ErrForbidden
	case err != nil:
		return Principal{}, err
	case id == "":
		return Principal{}, ErrUnauthenticated
	}
	return Principal{ID: id, Role: "service", Source: "service_token"}, nil
}

func (h *handler) readServiceApplicationCredential(w http.ResponseWriter, r *http.Request, params map[string]string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	var request serviceCredentialRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	if !validServiceAuditValue(request.Actor, 256) || request.RequestID != "" && !validServiceAuditValue(request.RequestID, 128) {
		writeProblem(w, http.StatusBadRequest, "invalid_actor", "service actor or request ID is invalid")
		return
	}
	path, ok := h.registry.Resolve(params["ws"])
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	service := h.serviceAPI.Factory(path)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "service_unavailable", "credential service is unavailable")
		return
	}
	credential, err := service.ReadApplicationCredential(r.Context(), params["application"])
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	defer func() { credential.Password = "" }()
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.Role != "service" || h.serviceAPI.Audit(r.Context(), ServiceCredentialAuditEvent{
		ClientID: principal.ID, DeclaredActor: request.Actor, RequestID: request.RequestID, WorkspaceID: params["ws"], Application: params["application"],
	}) != nil {
		writeProblem(w, http.StatusServiceUnavailable, "audit_unavailable", "credential access audit is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		APIVersion  string `json:"api_version"`
		WorkspaceID string `json:"workspace_id"`
		application.ApplicationCredential
	}{APIVersion: APIVersion, WorkspaceID: params["ws"], ApplicationCredential: credential})
}

func validServiceAuditValue(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
