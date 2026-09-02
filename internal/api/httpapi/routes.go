package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/webui"
)

type routeHandler func(http.ResponseWriter, *http.Request, map[string]string)

type routeSpec struct {
	policy  RoutePolicy
	handler routeHandler
}

func (h *handler) routeSpecs() []routeSpec {
	allListeners := []ListenerIdentity{ListenerDirect, ListenerTrustedProxy}
	directOnly := []ListenerIdentity{ListenerDirect}
	publicAllStates := map[ConsoleState]RouteAccess{
		StateM0:         {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateBootstrap:  {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull:       {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
	}
	systemAccess := map[ConsoleState]RouteAccess{
		StateM0:         {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateBootstrap:  {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull:       {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
	}
	internalCAAccess := map[ConsoleState]RouteAccess{
		StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull:       {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
	}
	consoleRootAccess := map[ConsoleState]RouteAccess{
		StateM0:         {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateBootstrap:  {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull:       {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
	}
	consoleAssetAccess := map[ConsoleState]RouteAccess{
		StateM0:         {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateBootstrap:  {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
		StateFull:       {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
	}
	workspaceAccess := map[ConsoleState]RouteAccess{
		StateM0:   {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull: {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
	}
	jobAccess := map[ConsoleState]RouteAccess{
		StateBootstrap:  {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateEnrollment: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull:       {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
	}
	configAccess := map[ConsoleState]RouteAccess{
		StateBootstrap: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
		StateFull:      {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
	}
	auditAccess := map[ConsoleState]RouteAccess{
		StateFull: {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
	}
	planAccess := map[ConsoleState]RouteAccess{
		StateBootstrap: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
	}
	applyAccess := map[ConsoleState]RouteAccess{
		StateBootstrap: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
	}
	lifecycleAccess := map[ConsoleState]RouteAccess{
		StateFull: {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
	}
	if h.deploymentHTTP != nil && h.deploymentHTTP.stepUp != nil {
		planAccess[StateFull] = RouteAccess{Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}}
		applyAccess[StateFull] = RouteAccess{Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}}
	}
	routes := []routeSpec{
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/", Permission: PermissionConsoleUI, Scope: ScopeService, Listeners: allListeners, Access: consoleRootAccess},
			handler: h.consoleRoot,
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: consoleMainScriptPath, Permission: PermissionConsoleUI, Scope: ScopeService, Listeners: allListeners, Access: consoleAssetAccess},
			handler: webAssetHandler(webui.MainJavaScript),
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: consoleMainStylesPath, Permission: PermissionConsoleUI, Scope: ScopeService, Listeners: allListeners, Access: consoleAssetAccess},
			handler: webAssetHandler(webui.MainStyles),
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: consoleRecoveryPath, Permission: PermissionConsoleUI, Scope: ScopeService, Listeners: directOnly, Access: consoleAssetAccess},
			handler: webAssetHandler(webui.RecoveryIndex),
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: consoleRecoveryScriptPath, Permission: PermissionConsoleUI, Scope: ScopeService, Listeners: directOnly, Access: consoleAssetAccess},
			handler: webAssetHandler(webui.RecoveryScript),
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: consoleRecoveryStylesPath, Permission: PermissionConsoleUI, Scope: ScopeService, Listeners: directOnly, Access: consoleAssetAccess},
			handler: webAssetHandler(webui.RecoveryStyles),
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/healthz", Permission: PermissionPublic, Scope: ScopeService, Listeners: allListeners, Access: publicAllStates},
			handler: func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
				if _, ok := supportedQuery(w, r); !ok {
					return
				}
				writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
			},
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/system", Permission: PermissionPublic, Scope: ScopeService, Listeners: allListeners, Access: systemAccess},
			handler: func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
				if _, ok := supportedQuery(w, r); !ok {
					return
				}
				h.system(w, r)
			},
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/system/ca", Permission: PermissionSystemCA, Scope: ScopeService, Listeners: allListeners, Access: internalCAAccess},
			handler: h.downloadInternalCA,
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/status", Permission: PermissionWorkspaceRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: workspaceAccess},
			handler: func(w http.ResponseWriter, r *http.Request, params map[string]string) {
				service, ok := h.workspaceService(w, params["ws"])
				if ok {
					h.status(w, r, params["ws"], service)
				}
			},
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/deployments", Permission: PermissionWorkspaceRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: workspaceAccess},
			handler: func(w http.ResponseWriter, r *http.Request, params map[string]string) {
				service, ok := h.workspaceService(w, params["ws"])
				if ok {
					h.deployments(w, r, params["ws"], service)
				}
			},
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/deployments/{id}", Permission: PermissionWorkspaceRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: workspaceAccess},
			handler: func(w http.ResponseWriter, r *http.Request, params map[string]string) {
				service, ok := h.workspaceService(w, params["ws"])
				if ok {
					h.deployment(w, r, params["ws"], params["id"], service)
				}
			},
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/modules/{module}/commands", Permission: PermissionWorkspaceRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: workspaceAccess},
			handler: func(w http.ResponseWriter, r *http.Request, params map[string]string) {
				service, ok := h.workspaceService(w, params["ws"])
				if ok {
					h.moduleCommands(w, r, params["ws"], params["module"], service)
				}
			},
		},
		{
			policy: RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/modules/{module}/commands/{command}", Permission: PermissionWorkspaceRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: workspaceAccess},
			handler: func(w http.ResponseWriter, r *http.Request, params map[string]string) {
				service, ok := h.workspaceService(w, params["ws"])
				if ok {
					h.moduleCommand(w, r, params["ws"], params["module"], params["command"], service)
				}
			},
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/config", Permission: PermissionConfigRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: configAccess},
			handler: h.getConfig,
		},
		{
			policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/config/validate", Permission: PermissionConfigValidate, Scope: ScopeWorkspace, Listeners: allListeners, Access: configAccess},
			handler: h.validateConfig,
		},
		{
			policy:  RoutePolicy{Method: http.MethodPut, Pattern: "/api/v1/workspaces/{ws}/config", Permission: PermissionConfigWrite, Scope: ScopeWorkspace, Listeners: allListeners, Access: configAccess},
			handler: h.putConfig,
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/jobs", Permission: PermissionJobList, Scope: ScopeWorkspace, Listeners: allListeners, Access: jobAccess},
			handler: h.listJobs,
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/jobs/{id}", Permission: PermissionJobRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: jobAccess},
			handler: h.getJob,
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: jobEventsRoutePattern, Permission: PermissionJobEventsRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: jobAccess},
			handler: h.streamJobEvents,
		},
		{
			policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/audit-events", Permission: PermissionAuditList, Scope: ScopeService, Listeners: allListeners, Access: auditAccess},
			handler: h.listAuditEvents,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodGet, Pattern: "/api/v1/auth/csrf", Permission: PermissionAuthCSRF, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateBootstrap:  {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
					StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
					StateFull:       {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.issuePreAuthCSRF,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodGet, Pattern: "/api/v1/auth/session", Permission: PermissionAuthSession, Scope: ScopeService, Listeners: allListeners,
				Access: map[ConsoleState]RouteAccess{
					StateBootstrap:  {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
					StateEnrollment: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
					StateFull:       {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.refreshAuthSession,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodPost, Pattern: "/api/v1/auth/bootstrap/exchange", Permission: PermissionAuthExchange, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateBootstrap:  {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
					StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.exchangeBootstrapToken,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodPost, Pattern: consoleauth.EnrollmentHandoffRoute, Permission: PermissionEnrollmentHandoff, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateEnrollment: {Authentication: AuthenticationBootstrap, Transports: []RequestTransport{TransportPlaintext, TransportTLS}},
				},
			},
			handler: h.issueEnrollmentHandoff,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodPost, Pattern: consoleauth.EnrollmentHandoffExchangeRoute, Permission: PermissionEnrollmentExchange, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateEnrollment: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.exchangeEnrollmentHandoff,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodPost, Pattern: consoleauth.EnrollmentOwnerRoute, Permission: PermissionEnrollmentOwner, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateEnrollment: {Authentication: AuthenticationEnrollment, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.createEnrollmentOwner,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodPost, Pattern: "/api/v1/auth/login", Permission: PermissionAuthLogin, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateFull: {Authentication: AuthenticationNone, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.loginLocal,
		},
		{
			policy: RoutePolicy{
				Method: http.MethodPost, Pattern: "/api/v1/auth/logout", Permission: PermissionAuthLogout, Scope: ScopeService, Listeners: directOnly,
				Access: map[ConsoleState]RouteAccess{
					StateFull: {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
				},
			},
			handler: h.logoutLocal,
		},
	}
	if h.jobs != nil && h.jobs.cancel != nil {
		routes = append(routes, routeSpec{
			policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/jobs/{id}/cancel", Permission: PermissionJobCancel, Scope: ScopeWorkspace, Listeners: allListeners, Access: jobAccess},
			handler: h.cancelJob,
		})
	}
	if h.deploymentHTTP != nil {
		if h.deploymentHTTP.stepUp != nil {
			routes = append(routes, routeSpec{
				policy: RoutePolicy{
					Method: http.MethodPost, Pattern: "/api/v1/auth/step-up", Permission: PermissionAuthStepUp,
					Scope: ScopeService, Listeners: allListeners,
					Access: map[ConsoleState]RouteAccess{
						StateFull: {Authentication: AuthenticationOwner, Transports: []RequestTransport{TransportTLS}},
					},
				},
				handler: h.issueLocalStepUp,
			})
		}
		routes = append(routes,
			routeSpec{
				policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/plans", Permission: PermissionDeploymentPlan, Scope: ScopeWorkspace, Listeners: allListeners, Access: planAccess},
				handler: h.planDeployment,
			},
			routeSpec{
				policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/actions/apply", Permission: PermissionDeploymentApply, Scope: ScopeWorkspace, Listeners: allListeners, Access: applyAccess},
				handler: h.applyDeployment,
			},
		)
		if h.deploymentHTTP.serviceFactory != nil {
			routes = append(routes,
				routeSpec{
					policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/modules/actions/{action}", Permission: PermissionDeploymentLifecycle, Scope: ScopeWorkspace, Listeners: allListeners, Access: lifecycleAccess},
					handler: h.moduleLifecycle,
				},
				routeSpec{
					policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/actions/rollback", Permission: PermissionDeploymentRollback, Scope: ScopeWorkspace, Listeners: allListeners, Access: lifecycleAccess},
					handler: h.rollbackDeployment,
				},
			)
		}
		if h.deploymentHTTP.moduleFactory != nil {
			routes = append(routes,
				routeSpec{
					policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/workspaces/{ws}/modules", Permission: PermissionModuleRead, Scope: ScopeWorkspace, Listeners: allListeners, Access: lifecycleAccess},
					handler: h.listModules,
				},
				routeSpec{
					policy:  RoutePolicy{Method: http.MethodGet, Pattern: "/api/v1/catalog/modules", Permission: PermissionModuleCatalogRead, Scope: ScopeService, Listeners: allListeners, Access: lifecycleAccess},
					handler: h.catalogModules,
				},
				routeSpec{
					policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/actions/update-modules", Permission: PermissionModuleUpdate, Scope: ScopeWorkspace, Listeners: allListeners, Access: lifecycleAccess},
					handler: h.updateModules,
				},
				routeSpec{
					policy:  RoutePolicy{Method: http.MethodPost, Pattern: "/api/v1/workspaces/{ws}/modules/{module}/actions/{action}", Permission: PermissionModuleConfig, Scope: ScopeWorkspace, Listeners: allListeners, Access: lifecycleAccess},
					handler: h.configureModule,
				},
			)
		}
	}
	return routes
}

func validateRouteSpecs(routes []routeSpec) error {
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if err := validateRoutePolicy(route.policy); err != nil {
			return err
		}
		if route.handler == nil {
			return errors.New("route handler is required")
		}
		key := route.policy.Method + " " + route.policy.Pattern
		if _, exists := seen[key]; exists {
			return errors.New("route is registered more than once: " + key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (h *handler) dispatch(w http.ResponseWriter, r *http.Request) {
	type matchedRoute struct {
		route  routeSpec
		params map[string]string
	}
	matches := make([]matchedRoute, 0, 1)
	for _, route := range h.routes {
		if params, ok := matchRoutePattern(route.policy.Pattern, r.URL.Path); ok {
			matches = append(matches, matchedRoute{route: route, params: params})
		}
	}
	if len(matches) == 0 {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}

	state, err := h.security.State(r.Context())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "state_unavailable", "control-plane state is unavailable")
		return
	}
	r = withConsoleState(r, state)
	if h.security.Listener == ListenerTrustedProxy && state != StateFull {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	transport := requestTransport(r)
	if state == StateFull && transport == TransportPlaintext && plaintextCarriesCredentialOrBody(r) {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}
	eligible := matches[:0]
	for _, match := range matches {
		access, allowed := match.route.policy.Access[state]
		if allowed && allowsTransport(access, transport) && allowsListener(match.route.policy.Listeners, h.security.Listener) {
			eligible = append(eligible, match)
		}
	}
	if len(eligible) == 0 {
		writeProblem(w, http.StatusNotFound, "not_found", "resource was not found")
		return
	}

	for _, match := range eligible {
		if match.route.policy.Method != r.Method {
			continue
		}
		access := match.route.policy.Access[state]
		if access.Authentication != AuthenticationNone {
			principal, err := h.security.Authorize(r, AuthorizationRequest{Policy: match.route.policy, Params: copyParams(match.params)})
			switch {
			case errors.Is(err, ErrUnauthenticated):
				writeProblem(w, http.StatusUnauthorized, "unauthenticated", "authentication is required")
				return
			case errors.Is(err, ErrForbidden):
				writeProblem(w, http.StatusForbidden, "forbidden", "request is not permitted")
				return
			case err != nil:
				writeProblem(w, http.StatusInternalServerError, "authentication_unavailable", "authentication is unavailable")
				return
			}
			r = withPrincipal(r, principal)
		}
		match.route.handler(w, r, match.params)
		return
	}

	allowed := make([]string, 0, len(eligible))
	for _, match := range eligible {
		allowed = append(allowed, match.route.policy.Method)
	}
	sort.Strings(allowed)
	w.Header().Set("Allow", strings.Join(uniqueStrings(allowed), ", "))
	writeProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed")
}

func allowsListener(allowed []ListenerIdentity, listener ListenerIdentity) bool {
	for _, candidate := range allowed {
		if candidate == listener {
			return true
		}
	}
	return false
}

func (h *handler) workspaceService(w http.ResponseWriter, workspaceID string) (QueryService, bool) {
	workspacePath, ok := h.registry.Resolve(workspaceID)
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return nil, false
	}
	service := h.service(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusInternalServerError, "internal_error", "query service is unavailable")
		return nil, false
	}
	return service, true
}

func matchRoutePattern(pattern, path string) (map[string]string, bool) {
	if path == "" || path[0] != '/' || path != "/" && strings.HasSuffix(path, "/") || strings.Contains(path, "//") {
		return nil, false
	}
	patternParts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	pathParts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(patternParts) != len(pathParts) {
		return nil, false
	}
	params := make(map[string]string)
	for index, patternPart := range patternParts {
		pathPart := pathParts[index]
		if strings.HasPrefix(patternPart, "{") && strings.HasSuffix(patternPart, "}") {
			if pathPart == "" || strings.ContainsAny(pathPart, "{}") {
				return nil, false
			}
			params[patternPart[1:len(patternPart)-1]] = pathPart
			continue
		}
		if patternPart != pathPart {
			return nil, false
		}
	}
	return params, true
}

func copyParams(params map[string]string) map[string]string {
	copy := make(map[string]string, len(params))
	for key, value := range params {
		copy[key] = value
	}
	return copy
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

// RouteInventory returns the registered policy metadata without handler
// functions. Tests use it to ensure a new route cannot bypass the state,
// permission, object-scope, and transport gates.
func RouteInventory(registry *Registry, factory ServiceFactory) ([]RoutePolicy, error) {
	// Inventory describes the complete production contract. Runtime handlers
	// still omit deployment routes unless their audited dependencies validate.
	h := &handler{registry: registry, factory: factory, jobs: &jobHTTPState{
		cancel: func(context.Context, string) (consolejobs.Job, error) { return consolejobs.Job{}, nil },
	}, deploymentHTTP: &deploymentHTTPState{
		stepUp:         routeInventoryStepUp{},
		serviceFactory: func(string) application.DeploymentService { return nil },
		moduleFactory:  func(string, application.EventSink) application.ModuleManagementService { return nil },
	}}
	routes := h.routeSpecs()
	if err := validateRouteSpecs(routes); err != nil {
		return nil, err
	}
	result := make([]RoutePolicy, 0, len(routes))
	for _, route := range routes {
		result = append(result, route.policy)
	}
	return result, nil
}

func staticState(state ConsoleState) func(context.Context) (ConsoleState, error) {
	return func(context.Context) (ConsoleState, error) { return state, nil }
}
