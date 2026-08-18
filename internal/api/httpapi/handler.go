// Package httpapi exposes the read-only M0 application queries over HTTP.
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
}

// ServiceFactory binds an application query service to one canonical
// workspace path. It is also called with an empty path for /system, whose
// Version query is workspace independent.
type ServiceFactory func(workspacePath string) QueryService

type handler struct {
	registry *Registry
	factory  ServiceFactory
}

func NewHandler(registry *Registry, factory ServiceFactory) http.Handler {
	if registry == nil {
		registry = &Registry{paths: map[string]string{}, ids: []string{}}
	}
	return &handler{registry: registry, factory: factory}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if !validLoopbackHost(r.Host) {
		writeProblem(w, http.StatusBadRequest, "invalid_host", "request Host must be a numeric loopback IP")
		return
	}

	switch r.URL.Path {
	case "/healthz":
		if !requireGET(w, r) {
			return
		}
		if _, ok := supportedQuery(w, r); !ok {
			return
		}
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
		return
	case "/api/v1/system":
		if !requireGET(w, r) {
			return
		}
		if _, ok := supportedQuery(w, r); !ok {
			return
		}
		h.system(w, r)
		return
	}

	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(segments) < 5 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "workspaces" {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	if !requireGET(w, r) {
		return
	}
	workspaceID := segments[3]
	workspacePath, ok := h.registry.Resolve(workspaceID)
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	service := h.service(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusInternalServerError, "internal_error", "query service is unavailable")
		return
	}

	switch {
	case len(segments) == 5 && segments[4] == "status":
		h.status(w, r, workspaceID, service)
	case len(segments) == 5 && segments[4] == "deployments":
		h.deployments(w, r, workspaceID, service)
	case len(segments) == 6 && segments[4] == "deployments" && segments[5] != "":
		h.deployment(w, r, workspaceID, segments[5], service)
	default:
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
	}
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
	writeJSON(w, http.StatusOK, systemResponse{
		APIVersion:   APIVersion,
		Build:        systemBuild{Version: result.Version, Commit: result.Commit, Date: result.Date},
		Capabilities: systemCapabilities{ReadOnly: true},
		WorkspaceIDs: h.registry.IDs(),
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
