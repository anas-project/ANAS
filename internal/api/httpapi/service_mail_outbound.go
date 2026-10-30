package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

var (
	ErrMailOutboundConflict = errors.New("mail outbound configuration conflict")
	ErrMailOutboundUnknown  = errors.New("mail outbound configuration outcome unknown")
)

// ServiceMailOutboundRequest is private service-to-service input. Password is
// never returned in API responses or included in audit data or error messages.
type ServiceMailOutboundRequest struct {
	Actor       string `json:"actor"`
	OperationID string `json:"operation_id"`
	Provider    string `json:"provider"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         string `json:"tls"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

func (r ServiceMailOutboundRequest) String() string   { return "[private mail outbound request]" }
func (r ServiceMailOutboundRequest) GoString() string { return r.String() }

type ServiceMailOutboundCall struct {
	WorkspaceID, WorkspacePath, ClientID string
	Request                              ServiceMailOutboundRequest
	Reauthorize                          func(context.Context) error
}

type ServiceMailOutboundResult struct {
	State string `json:"state"`
}

var serviceMailOutboundOperation = regexp.MustCompile(`^cfg-[a-f0-9]{32}$`)

func mailOutboundFields(raw []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	defer func() {
		for _, value := range object {
			clear(value)
		}
	}()
	for key := range object {
		switch key {
		case "actor", "operation_id", "provider", "host", "port", "tls", "username", "password":
		default:
			return false
		}
	}
	return true
}

func (h *handler) invokeServiceMailOutbound(w http.ResponseWriter, r *http.Request, params map[string]string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if params["application"] != "mail" {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	if h.serviceAPI.MailOutbound == nil {
		writeProblem(w, http.StatusServiceUnavailable, "service_unavailable", "mail outbound configuration is unavailable")
		return
	}
	var input ServiceMailOutboundRequest
	if !decodeAuthJSONLimitChecked(w, r, &input, 8192, mailOutboundFields) {
		return
	}
	defer func() { input.Password = "" }()
	profile := smtpconfig.Profile{ID: "primary", Provider: input.Provider, Host: input.Host, Port: input.Port, TLS: input.TLS, Username: input.Username, Password: input.Password}
	config := smtpconfig.Config{Default: "primary", Profiles: []smtpconfig.Profile{profile}}
	if !validServiceAuditValue(input.Actor, 256) || !serviceMailOutboundOperation.MatchString(input.OperationID) || config.Validate() != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "mail outbound configuration request is invalid")
		return
	}
	path, ok := h.registry.Resolve(params["ws"])
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok || principal.Role != "service" {
		writeProblem(w, http.StatusUnauthorized, "unauthorized", "service authentication is required")
		return
	}
	_, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	reauthorize := func(ctx context.Context) error {
		id, err := h.serviceAPI.Authenticate(ctx, token, string(PermissionMailOutboundConfigure), params["ws"], "mail")
		if err != nil || id != principal.ID {
			return ErrForbidden
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := h.serviceAPI.MailOutbound(ctx, ServiceMailOutboundCall{WorkspaceID: params["ws"], WorkspacePath: path, ClientID: principal.ID, Request: input, Reauthorize: reauthorize})
	if err == nil && reauthorize(ctx) != nil {
		err = ErrMailOutboundUnknown
	}
	if err != nil {
		if errors.Is(err, ErrMailOutboundConflict) {
			writeProblem(w, http.StatusConflict, "mail_outbound_conflict", "mail outbound configuration conflicts with current state")
		} else {
			writeProblem(w, http.StatusServiceUnavailable, "mail_outbound_unknown", "mail outbound configuration could not be confirmed")
		}
		return
	}
	if result.State != "configured" {
		writeProblem(w, http.StatusServiceUnavailable, "mail_outbound_unknown", "mail outbound configuration could not be confirmed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
