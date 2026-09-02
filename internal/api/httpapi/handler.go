// Package httpapi exposes typed application queries, audited deployment and
// configuration operations, and the console authentication/route-policy
// boundary over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/application"
)

const (
	defaultPageLimit = 50
	maximumPageLimit = 100
	maximumCursorLen = 512
)

// QueryService is the narrow application boundary used by the HTTP adapter.
// Keeping it local lets handler tests use a fake without widening the
// application package with transport-specific concepts.
type QueryService interface {
	Version(context.Context) (application.VersionResult, error)
	Status(context.Context) (application.StatusResult, error)
	ListDeployments(context.Context, application.ListDeploymentsRequest) (application.ListDeploymentsResult, error)
	InspectDeployment(context.Context, application.InspectDeploymentRequest) (application.InspectDeploymentResult, error)
	ListModuleCommands(context.Context, application.ListModuleCommandsRequest) (application.ListModuleCommandsResult, error)
	GetModuleCommand(context.Context, application.GetModuleCommandRequest) (application.EffectiveModuleCommand, error)
}

// ServiceFactory binds an application query service to one canonical
// workspace path. It is also called with an empty path for /system, whose
// Version query is workspace independent.
type ServiceFactory func(workspacePath string) QueryService

type handler struct {
	registry       *Registry
	factory        ServiceFactory
	config         *configHTTPState
	security       SecurityOptions
	auth           ConsoleAuthenticator
	enrollment     *EnrollmentOptions
	jobs           *jobHTTPState
	deploymentHTTP *deploymentHTTPState
	audit          *auditHTTPState
	systemHTTP     *systemHTTPState
	authHTTP       authHTTPState
	routes         []routeSpec
}

func NewHandler(registry *Registry, factory ServiceFactory) http.Handler {
	handler, err := NewHandlerWithSecurity(registry, factory, legacySecurityOptions())
	if err != nil {
		panic(err)
	}
	return handler
}

func NewHandlerWithSecurity(registry *Registry, factory ServiceFactory, security SecurityOptions) (http.Handler, error) {
	return newHandler(registry, factory, security, nil, nil, nil, nil, nil, nil, nil)
}

func NewHandlerWithAuthentication(registry *Registry, factory ServiceFactory, security SecurityOptions, auth ConsoleAuthenticator) (http.Handler, error) {
	if auth == nil {
		return nil, errors.New("console authenticator is required")
	}
	return newHandler(registry, factory, security, auth, nil, nil, nil, nil, nil, nil)
}

// NewHandlerWithConfig adds the synchronous desired-configuration surface to
// a handler whose authentication policy is supplied by SecurityOptions.
func NewHandlerWithConfig(registry *Registry, factory ServiceFactory, security SecurityOptions, config ConfigOptions) (http.Handler, error) {
	configState, err := newConfigHTTPState(config)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, nil, nil, nil, configState, nil, nil, nil)
}

// NewHandlerWithDeployment exposes only the audited deployment routes in
// addition to the base query surface. It is primarily useful to compose a
// listener whose authentication is already supplied by SecurityOptions.
func NewHandlerWithDeployment(registry *Registry, factory ServiceFactory, security SecurityOptions, deployment DeploymentOptions) (http.Handler, error) {
	deploymentState, err := newDeploymentHTTPState(deployment)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, nil, nil, nil, nil, deploymentState, nil, nil)
}

func NewHandlerWithEnrollment(registry *Registry, factory ServiceFactory, security SecurityOptions, auth DirectAuthenticator, enrollment EnrollmentOptions) (http.Handler, error) {
	if auth == nil {
		return nil, errors.New("console authenticator is required")
	}
	if err := enrollment.validate(); err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, auth, &enrollment, nil, nil, nil, nil, nil)
}

// NewHandlerWithJobQueries adds the durable read-only job history and event
// stream surface. The SecurityOptions authorizer remains the single
// authentication boundary, so this constructor can be used with bootstrap,
// enrollment-recovery, or owner authentication.
func NewHandlerWithJobQueries(registry *Registry, factory ServiceFactory, security SecurityOptions, auth ConsoleAuthenticator, jobs JobQueryOptions) (http.Handler, error) {
	jobState, err := newJobHTTPState(jobs)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, auth, nil, jobState, nil, nil, nil, nil)
}

// NewHandlerWithAuditQueries adds the durable full-state audit history surface.
// Route policy and object filtering still enforce owner authentication and
// registered-workspace visibility.
func NewHandlerWithAuditQueries(registry *Registry, factory ServiceFactory, security SecurityOptions, auditQuery AuditQueryOptions) (http.Handler, error) {
	auditState, err := newAuditHTTPState(auditQuery)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, nil, nil, nil, nil, nil, auditState, nil)
}

// NewHandlerWithEnrollmentAndJobQueries composes the enrollment workflow and
// durable read-only job surface without weakening either dependency's startup
// validation.
func NewHandlerWithEnrollmentAndJobQueries(registry *Registry, factory ServiceFactory, security SecurityOptions, auth DirectAuthenticator, enrollment EnrollmentOptions, jobs JobQueryOptions) (http.Handler, error) {
	if auth == nil {
		return nil, errors.New("console authenticator is required")
	}
	if err := enrollment.validate(); err != nil {
		return nil, err
	}
	jobState, err := newJobHTTPState(jobs)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, auth, &enrollment, jobState, nil, nil, nil, nil)
}

// NewHandlerWithEnrollmentJobQueriesAndConfig composes the complete direct
// console surface used by anasd. Configuration validation and writes stay
// synchronous, while ConfigOptions supplies the independent application and
// durable-audit boundaries required before a write can be published.
func NewHandlerWithEnrollmentJobQueriesAndConfig(
	registry *Registry,
	factory ServiceFactory,
	security SecurityOptions,
	auth DirectAuthenticator,
	enrollment EnrollmentOptions,
	jobs JobQueryOptions,
	config ConfigOptions,
	auditQuery AuditQueryOptions,
) (http.Handler, error) {
	if auth == nil {
		return nil, errors.New("console authenticator is required")
	}
	if err := enrollment.validate(); err != nil {
		return nil, err
	}
	jobState, err := newJobHTTPState(jobs)
	if err != nil {
		return nil, err
	}
	configState, err := newConfigHTTPState(config)
	if err != nil {
		return nil, err
	}
	auditState, err := newAuditHTTPState(auditQuery)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, auth, &enrollment, jobState, configState, nil, auditState, nil)
}

// NewHandlerWithEnrollmentJobsConfigAndDeployment composes the production
// direct-console surface, including audited plan/first-apply routes.
func NewHandlerWithEnrollmentJobsConfigAndDeployment(
	registry *Registry,
	factory ServiceFactory,
	security SecurityOptions,
	auth DirectAuthenticator,
	enrollment EnrollmentOptions,
	jobs JobQueryOptions,
	config ConfigOptions,
	deployment DeploymentOptions,
	auditQuery AuditQueryOptions,
	systemOptions SystemOptions,
) (http.Handler, error) {
	if auth == nil {
		return nil, errors.New("console authenticator is required")
	}
	if err := enrollment.validate(); err != nil {
		return nil, err
	}
	jobState, err := newJobHTTPState(jobs)
	if err != nil {
		return nil, err
	}
	configState, err := newConfigHTTPState(config)
	if err != nil {
		return nil, err
	}
	deploymentState, err := newDeploymentHTTPState(deployment)
	if err != nil {
		return nil, err
	}
	auditState, err := newAuditHTTPState(auditQuery)
	if err != nil {
		return nil, err
	}
	systemState, err := newSystemHTTPState(systemOptions)
	if err != nil {
		return nil, err
	}
	return newHandler(registry, factory, security, auth, &enrollment, jobState, configState, deploymentState, auditState, systemState)
}

func newHandler(registry *Registry, factory ServiceFactory, security SecurityOptions, auth ConsoleAuthenticator, enrollment *EnrollmentOptions, jobs *jobHTTPState, config *configHTTPState, deployment *deploymentHTTPState, auditState *auditHTTPState, systemState *systemHTTPState) (http.Handler, error) {
	if registry == nil {
		registry = &Registry{paths: map[string]string{}, ids: []string{}}
	}
	security, err := normalizeSecurityOptions(security)
	if err != nil {
		return nil, err
	}
	h := &handler{
		registry:       registry,
		factory:        factory,
		config:         config,
		security:       security,
		auth:           auth,
		enrollment:     enrollment,
		jobs:           jobs,
		deploymentHTTP: deployment,
		audit:          auditState,
		systemHTTP:     systemState,
		authHTTP:       newAuthHTTPState(),
	}
	h.routes = h.routeSpecs()
	if err := validateRouteSpecs(h.routes); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header(), r.TLS != nil)
	if h.security.Listener == ListenerDirect {
		stripDirectProxyHeaders(r.Header)
	}
	if !h.security.HostAllowed(r) {
		writeProblem(w, http.StatusBadRequest, "invalid_host", "request Host is not allowed")
		return
	}
	h.dispatch(w, r)
}

func setSecurityHeaders(header http.Header, isTLS bool) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	header.Set("Referrer-Policy", "strict-origin")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	if isTLS {
		header.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

func (h *handler) moduleCommands(w http.ResponseWriter, r *http.Request, workspaceID, moduleName string, service QueryService) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	result, err := service.ListModuleCommands(r.Context(), application.ListModuleCommandsRequest{Module: moduleName})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newModuleCommandListResponse(workspaceID, result))
}

func (h *handler) moduleCommand(w http.ResponseWriter, r *http.Request, workspaceID, moduleName, commandID string, service QueryService) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	result, err := service.GetModuleCommand(r.Context(), application.GetModuleCommandRequest{Module: moduleName, Command: commandID})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newModuleCommandDetailResponse(workspaceID, result))
}

func validLoopbackHost(value string) bool {
	if value == "" {
		return false
	}
	host := value
	port := ""
	switch {
	case strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]"):
		host = value[1 : len(value)-1]
	case strings.HasPrefix(value, "["):
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil || port == "" || !validNumericPort(port) {
			return false
		}
	default:
		if ip, ok := parseHostIP(value); ok {
			return ip.IsLoopback()
		}
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil || port == "" || !validNumericPort(port) {
			return false
		}
	}
	ip, ok := parseHostIP(host)
	return ok && ip.IsLoopback()
}

func parseHostIP(value string) (net.IP, bool) {
	ipValue := value
	if before, zone, found := strings.Cut(value, "%"); found {
		if zone == "" || strings.Contains(zone, "%") || !validIPv6Zone(zone) {
			return nil, false
		}
		ipValue = before
	}
	ip := net.ParseIP(ipValue)
	if ip == nil {
		return nil, false
	}
	if ipValue != value && ip.To4() != nil {
		return nil, false
	}
	return ip, true
}

func validIPv6Zone(zone string) bool {
	for _, char := range zone {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validNumericPort(port string) bool {
	for _, char := range port {
		if char < '0' || char > '9' {
			return false
		}
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 0 && value <= 65535
}

func (h *handler) system(w http.ResponseWriter, r *http.Request) {
	state, ok := ConsoleStateFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "state_unavailable", "control-plane state is unavailable")
		return
	}
	service := h.service("")
	if service == nil {
		writeProblem(w, http.StatusInternalServerError, "internal_error", "query service is unavailable")
		return
	}
	result, err := service.Version(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	material, err := h.systemHTTP.certificate(r.Context())
	if err != nil {
		writeProblem(w, http.StatusServiceUnavailable, "certificate_unavailable", "certificate information is unavailable")
		return
	}
	var proxyURL *string
	directRecoveryURLs := []string{}
	if h.systemHTTP != nil {
		directRecoveryURLs = append([]string{}, h.systemHTTP.directRecoveryURLs...)
	}
	if h.systemHTTP != nil && h.systemHTTP.proxyURL != "" {
		value := h.systemHTTP.proxyURL
		proxyURL = &value
	}
	writeJSON(w, http.StatusOK, systemResponse{
		APIVersion:         APIVersion,
		Build:              systemBuild{Version: result.Version, Commit: result.Commit, Date: result.Date},
		Capabilities:       systemCapabilities{ReadOnly: h.config == nil},
		WorkspaceIDs:       h.registry.IDs(),
		CertificateIssuer:  material.Issuer,
		ConsoleState:       state,
		Listener:           h.security.Listener,
		DirectRecoveryURLs: directRecoveryURLs,
		ProxyURL:           proxyURL,
	})
}

func (h *handler) status(w http.ResponseWriter, r *http.Request, workspaceID string, service QueryService) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	result, err := service.Status(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newStatusResponse(workspaceID, result))
}

func (h *handler) deployments(w http.ResponseWriter, r *http.Request, workspaceID string, service QueryService) {
	request, ok := paginationRequest(w, r)
	if !ok {
		return
	}
	result, err := service.ListDeployments(r.Context(), request)
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newDeploymentListResponse(workspaceID, result))
}

func (h *handler) deployment(w http.ResponseWriter, r *http.Request, workspaceID, deploymentID string, service QueryService) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	if utf8.RuneCountInString(deploymentID) > 255 {
		writeProblem(w, http.StatusBadRequest, "invalid_deployment_id", "deployment ID is invalid")
		return
	}
	result, err := service.InspectDeployment(r.Context(), application.InspectDeploymentRequest{DeploymentID: deploymentID})
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newDeploymentDetailResponse(workspaceID, result))
}

func (h *handler) service(workspacePath string) QueryService {
	if h == nil || h.factory == nil {
		return nil
	}
	return h.factory(workspacePath)
}

func paginationRequest(w http.ResponseWriter, r *http.Request) (application.ListDeploymentsRequest, bool) {
	query, ok := supportedQuery(w, r, "limit", "cursor")
	if !ok {
		return application.ListDeploymentsRequest{}, false
	}
	request := application.ListDeploymentsRequest{Limit: defaultPageLimit}
	if values, present := query["limit"]; present {
		if len(values) != 1 || values[0] == "" {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be a single integer between 1 and 100")
			return request, false
		}
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > maximumPageLimit {
			writeProblem(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer between 1 and 100")
			return request, false
		}
		request.Limit = limit
	}
	if values, present := query["cursor"]; present {
		if len(values) != 1 || values[0] == "" || len(values[0]) > maximumCursorLen {
			writeProblem(w, http.StatusBadRequest, "invalid_cursor", "cursor must be a single non-empty value no longer than 512 bytes")
			return request, false
		}
		request.Cursor = values[0]
	}
	return request, true
}

func supportedQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_query", "query string is invalid")
		return nil, false
	}
	allowedNames := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedNames[name] = struct{}{}
	}
	for name := range query {
		if _, ok := allowedNames[name]; !ok {
			writeProblem(w, http.StatusBadRequest, "unknown_query_parameter", "query parameter is not supported")
			return nil, false
		}
	}
	return query, true
}

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", http.MethodGet)
	writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
	return false
}

type problem struct {
	APIVersion string `json:"api_version"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
	Code       string `json:"code"`
}

func writeApplicationError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		writeProblem(w, http.StatusGatewayTimeout, "deadline_exceeded", "request deadline was exceeded")
		return
	}
	if errors.Is(err, context.Canceled) {
		writeProblem(w, http.StatusRequestTimeout, "request_canceled", "request was canceled")
		return
	}
	applicationError, ok := application.ErrorOf(err)
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	status := http.StatusInternalServerError
	switch applicationError.Kind {
	case application.ErrorKindInvalidArgument:
		status = http.StatusBadRequest
	case application.ErrorKindNotFound:
		status = http.StatusNotFound
	case application.ErrorKindFailedPrecondition:
		status = http.StatusPreconditionFailed
	case application.ErrorKindPreconditionRequired:
		status = http.StatusPreconditionRequired
	}
	if applicationError.Kind == application.ErrorKindInternal &&
		(applicationError.Code == "audit_unavailable" || applicationError.Code == "config_recovery_required" ||
			applicationError.Code == "config_unavailable" || applicationError.Code == "secrets_unavailable" ||
			applicationError.Code == "runtime_lock_unavailable") {
		status = http.StatusServiceUnavailable
	}
	code := applicationError.Code
	if code == "" {
		code = "internal_error"
	}
	writeProblem(w, status, code, publicErrorDetail(applicationError, status))
}

func publicErrorDetail(err *application.Error, status int) string {
	switch err.Code {
	case "invalid_deployment_id":
		return "deployment ID is invalid"
	case "invalid_cursor":
		return "cursor is invalid"
	case "invalid_limit":
		return "limit is invalid"
	case "invalid_module":
		return "module name is invalid"
	case "invalid_module_command":
		return "module command ID is invalid"
	case "module_not_active":
		return "module is not present in the active deployment"
	case "module_command_not_found":
		return "module command was not found"
	case "deployment_missing":
		if status == http.StatusNotFound {
			return "deployment was not found"
		}
		return "deployment metadata is invalid"
	case "state_unreadable":
		return "workspace state could not be read"
	default:
		if status >= 500 {
			return "internal server error"
		}
		return "request could not be completed"
	}
}

func writeProblem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSONWithType(w, status, problem{
		APIVersion: APIVersion, Type: "about:blank", Title: http.StatusText(status),
		Status: status, Detail: detail, Code: code,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	writeJSONWithType(w, status, value)
}

func writeJSONWithType(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		w.Header().Set("Content-Type", "application/problem+json")
		status = http.StatusInternalServerError
		body = []byte(`{"api_version":"anas.dev/api/v1","type":"about:blank","title":"Internal Server Error","status":500,"detail":"response encoding failed","code":"response_encoding_failed"}`)
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}
