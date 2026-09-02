package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleauth"
	"gopkg.in/yaml.v3"
)

func TestOpenAPITracksImplementedSurface(t *testing.T) {
	document := readOpenAPIDocument(t)
	if got := document["openapi"]; got != "3.1.0" {
		t.Fatalf("openapi = %v", got)
	}
	assertInternalOpenAPIReferencesResolve(t, document)

	paths := objectAt(t, document, "paths")
	gotRoutes := make([]string, 0, len(paths))
	methods := map[string]bool{
		"get": true, "put": true, "post": true, "delete": true,
		"options": true, "head": true, "patch": true, "trace": true,
	}
	for path, raw := range paths {
		pathItem, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("paths.%s is %T, want object", path, raw)
		}
		for method := range pathItem {
			if methods[method] {
				gotRoutes = append(gotRoutes, strings.ToUpper(method)+" "+path)
			}
		}
	}
	inventory, err := RouteInventory(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantRoutes := make([]string, 0, len(inventory))
	for _, policy := range inventory {
		wantRoutes = append(wantRoutes, policy.Method+" "+policy.Pattern)
		operation := objectAt(t, objectAt(t, paths, policy.Pattern), strings.ToLower(policy.Method))
		assertOpenAPIRoutePolicy(t, operation, policy)
	}
	sort.Strings(gotRoutes)
	sort.Strings(wantRoutes)
	if !reflect.DeepEqual(gotRoutes, wantRoutes) {
		t.Fatalf("OpenAPI routes = %v, handler routes = %v", gotRoutes, wantRoutes)
	}
	wantResponses := map[string][]string{
		"GET /":                                                           {"200", "308", "400", "404", "405", "500"},
		"GET /assets/emergency.css":                                       {"200", "400", "404", "405", "500"},
		"GET /assets/emergency.js":                                        {"200", "400", "404", "405", "500"},
		"GET /assets/main.css":                                            {"200", "400", "404", "405", "500"},
		"GET /assets/main.js":                                             {"200", "400", "404", "405", "500"},
		"GET /emergency":                                                  {"200", "400", "404", "405", "500"},
		"GET /healthz":                                                    {"200", "400", "405", "500"},
		"GET /api/v1/audit-events":                                        {"200", "400", "401", "403", "404", "405", "408", "500", "503", "504"},
		"GET /api/v1/system":                                              {"200", "400", "404", "405", "408", "500", "503", "504"},
		"GET /api/v1/system/ca":                                           {"200", "400", "401", "403", "404", "405", "500", "503"},
		"GET /api/v1/auth/csrf":                                           {"200", "400", "404", "405", "500"},
		"GET /api/v1/auth/session":                                        {"200", "400", "401", "403", "404", "405", "500", "503"},
		"POST /api/v1/auth/bootstrap/exchange":                            {"200", "400", "401", "403", "404", "405", "413", "415", "429", "500", "503"},
		"POST /api/v1/auth/enrollment/handoffs":                           {"201", "400", "401", "403", "404", "405", "409", "500", "503"},
		"POST /api/v1/auth/enrollment/exchange":                           {"303", "400", "401", "403", "404", "405", "409", "413", "415", "500", "503"},
		"POST /api/v1/auth/enrollment/owner":                              {"201", "400", "401", "403", "404", "405", "413", "415", "500", "503"},
		"POST /api/v1/auth/login":                                         {"200", "400", "401", "403", "404", "405", "413", "415", "429", "500", "503"},
		"POST /api/v1/auth/logout":                                        {"204", "400", "401", "403", "404", "405", "500", "503"},
		"POST /api/v1/auth/step-up":                                       {"200", "400", "401", "403", "404", "405", "408", "413", "415", "429", "500", "503", "504"},
		"GET /api/v1/jobs":                                                {"200", "400", "401", "403", "404", "405", "408", "500", "503", "504"},
		"GET /api/v1/jobs/{id}":                                           {"200", "400", "401", "403", "404", "405", "408", "500", "503", "504"},
		"GET /api/v1/jobs/{id}/events":                                    {"200", "204", "400", "401", "403", "404", "405", "406", "408", "410", "429", "500", "503", "504"},
		"POST /api/v1/workspaces/{ws}/plans":                              {"200", "400", "401", "403", "404", "405", "408", "413", "415", "428", "429", "500", "503", "504"},
		"POST /api/v1/workspaces/{ws}/actions/apply":                      {"202", "400", "401", "403", "404", "405", "408", "409", "413", "415", "428", "429", "500", "503", "504"},
		"GET /api/v1/workspaces/{ws}/config":                              {"200", "400", "401", "403", "404", "405", "408", "412", "500", "503", "504"},
		"PUT /api/v1/workspaces/{ws}/config":                              {"200", "400", "401", "403", "404", "405", "408", "412", "413", "415", "428", "500", "503", "504"},
		"POST /api/v1/workspaces/{ws}/config/validate":                    {"200", "400", "401", "403", "404", "405", "408", "412", "413", "415", "500", "503", "504"},
		"GET /api/v1/workspaces/{ws}/status":                              {"200", "400", "401", "403", "404", "405", "408", "500", "504"},
		"GET /api/v1/workspaces/{ws}/deployments":                         {"200", "400", "401", "403", "404", "405", "408", "500", "504"},
		"GET /api/v1/workspaces/{ws}/deployments/{id}":                    {"200", "400", "401", "403", "404", "405", "408", "412", "500", "504"},
		"GET /api/v1/workspaces/{ws}/modules/{module}/commands":           {"200", "400", "401", "403", "404", "405", "408", "412", "500", "504"},
		"GET /api/v1/workspaces/{ws}/modules/{module}/commands/{command}": {"200", "400", "401", "403", "404", "405", "408", "412", "500", "504"},
	}
	if len(wantResponses) != len(inventory) {
		t.Fatalf("response contract covers %d routes, handler has %d", len(wantResponses), len(inventory))
	}
	for _, policy := range inventory {
		if _, ok := wantResponses[policy.Method+" "+policy.Pattern]; !ok {
			t.Errorf("response contract omits %s %s", policy.Method, policy.Pattern)
		}
	}
	for route, want := range wantResponses {
		method, path, ok := strings.Cut(route, " ")
		if !ok {
			t.Fatalf("invalid test route %q", route)
		}
		pathItem := objectAt(t, paths, path)
		operation := objectAt(t, pathItem, strings.ToLower(method))
		responses := objectAt(t, operation, "responses")
		got := make([]string, 0, len(responses))
		for status := range responses {
			got = append(got, status)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s responses = %v, want %v", route, got, want)
		}
	}
	putConfigResponses := objectAt(t, objectAt(t, objectAt(t, paths, "/api/v1/workspaces/{ws}/config"), "put"), "responses")
	if got := objectAt(t, putConfigResponses, "503")["$ref"]; got != "#/components/responses/ConfigPutServiceUnavailableProblem" {
		t.Fatalf("config PUT 503 response = %v", got)
	}

	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")
	assertRequiredPropertiesAreUnique(t, schemas)
	apiVersion := objectAt(t, schemas, "APIVersion")
	if got := apiVersion["const"]; got != APIVersion {
		t.Fatalf("OpenAPI API version = %v, handler = %q", got, APIVersion)
	}
	responses := objectAt(t, components, "responses")
	for _, name := range []string{
		"BadRequestProblem", "UnauthorizedProblem", "ForbiddenProblem", "NotFoundProblem",
		"ConflictProblem", "PreconditionProblem", "PreconditionRequiredProblem", "GetMethodNotAllowedProblem", "PostMethodNotAllowedProblem", "ConfigMethodNotAllowedProblem",
		"PayloadTooLargeProblem", "UnsupportedMediaTypeProblem", "UnsupportedFormMediaTypeProblem", "NotAcceptableProblem",
		"EventGapProblem", "TooManyRequestsProblem",
		"RequestCanceledProblem", "InternalProblem", "ServiceUnavailableProblem", "ConfigPutServiceUnavailableProblem", "DeadlineProblem",
	} {
		response := objectAt(t, responses, name)
		content := objectAt(t, response, "content")
		if _, ok := content["application/problem+json"]; !ok {
			t.Errorf("components.responses.%s does not use application/problem+json", name)
		}
	}
	if description, _ := objectAt(t, responses, "ConfigPutServiceUnavailableProblem")["description"].(string); !strings.Contains(description, "config_recovery_required") {
		t.Fatalf("config PUT 503 description does not document indeterminate recovery: %q", description)
	}
}

func assertOpenAPIRoutePolicy(t *testing.T, operation map[string]any, policy RoutePolicy) {
	t.Helper()
	if got := operation["x-anas-permission"]; got != string(policy.Permission) {
		t.Errorf("%s %s permission = %v, want %q", policy.Method, policy.Pattern, got, policy.Permission)
	}
	if got := operation["x-anas-scope"]; got != string(policy.Scope) {
		t.Errorf("%s %s scope = %v, want %q", policy.Method, policy.Pattern, got, policy.Scope)
	}
	gotListeners := stringSliceAt(t, operation, "x-anas-listeners")
	wantListeners := make([]string, 0, len(policy.Listeners))
	for _, listener := range policy.Listeners {
		wantListeners = append(wantListeners, string(listener))
	}
	sort.Strings(gotListeners)
	sort.Strings(wantListeners)
	if !reflect.DeepEqual(gotListeners, wantListeners) {
		t.Errorf("%s %s listeners = %v, want %v", policy.Method, policy.Pattern, gotListeners, wantListeners)
	}

	stateAccess := objectAt(t, operation, "x-anas-state-access")
	if len(stateAccess) != len(policy.Access) {
		t.Errorf("%s %s state count = %d, want %d", policy.Method, policy.Pattern, len(stateAccess), len(policy.Access))
	}
	for state, access := range policy.Access {
		declared := objectAt(t, stateAccess, string(state))
		if got := declared["authentication"]; got != string(access.Authentication) {
			t.Errorf("%s %s state %s authentication = %v, want %q", policy.Method, policy.Pattern, state, got, access.Authentication)
		}
		gotTransports := stringSliceAt(t, declared, "transports")
		wantTransports := make([]string, 0, len(access.Transports))
		for _, transport := range access.Transports {
			wantTransports = append(wantTransports, string(transport))
		}
		sort.Strings(gotTransports)
		sort.Strings(wantTransports)
		if !reflect.DeepEqual(gotTransports, wantTransports) {
			t.Errorf("%s %s state %s transports = %v, want %v", policy.Method, policy.Pattern, state, gotTransports, wantTransports)
		}
	}
}

func TestOpenAPIAuthenticationContractMatchesHandlers(t *testing.T) {
	document := readOpenAPIDocument(t)
	components := objectAt(t, document, "components")
	schemes := objectAt(t, components, "securitySchemes")
	wantSchemes := map[string]struct {
		location string
		name     string
		secure   string
		httpOnly bool
	}{
		"preAuthCSRFCookie":       {location: "cookie", name: preAuthCSRFCookieName, secure: "state-dependent", httpOnly: true},
		"bootstrapSessionCookie":  {location: "cookie", name: bootstrapSessionCookie, secure: "state-dependent", httpOnly: true},
		"enrollmentSessionCookie": {location: "cookie", name: consoleauth.EnrollmentSessionCookieName, secure: "always", httpOnly: true},
		"enrollmentCSRFCookie":    {location: "cookie", name: consoleauth.EnrollmentCSRFCookieName, secure: "always", httpOnly: false},
		"localSessionCookie":      {location: "cookie", name: localSessionCookie, secure: "always", httpOnly: true},
	}
	for schemeName, want := range wantSchemes {
		scheme := objectAt(t, schemes, schemeName)
		if scheme["type"] != "apiKey" || scheme["in"] != want.location || scheme["name"] != want.name {
			t.Errorf("security scheme %s = %#v", schemeName, scheme)
		}
		attributes := objectAt(t, scheme, "x-anas-cookie-attributes")
		if attributes["host_only"] != true || attributes["http_only"] != want.httpOnly || attributes["same_site"] != "Strict" || attributes["path"] != "/" || attributes["secure"] != want.secure {
			t.Errorf("security scheme %s cookie attributes = %#v", schemeName, attributes)
		}
	}
	csrfScheme := objectAt(t, schemes, "csrfHeader")
	if csrfScheme["type"] != "apiKey" || csrfScheme["in"] != "header" || csrfScheme["name"] != csrfHeaderName {
		t.Errorf("csrfHeader scheme = %#v", csrfScheme)
	}

	paths := objectAt(t, document, "paths")
	csrf := objectAt(t, objectAt(t, paths, "/api/v1/auth/csrf"), "get")
	if security, ok := csrf["security"].([]any); !ok || len(security) != 0 {
		t.Errorf("CSRF issue security = %#v, want anonymous", csrf["security"])
	}
	assertSetCookieResponse(t, csrf, "200")

	session := objectAt(t, objectAt(t, paths, "/api/v1/auth/session"), "get")
	assertSecurityRequirementAlternatives(t, session, []string{"bootstrapSessionCookie"}, []string{"localSessionCookie"})
	assertJSONResponseSchema(t, session, "200", "#/components/schemas/SessionRefreshResponse")

	exchange := objectAt(t, objectAt(t, paths, "/api/v1/auth/bootstrap/exchange"), "post")
	assertSecurityRequirement(t, exchange, "preAuthCSRFCookie", "csrfHeader")
	assertOriginParameter(t, exchange)
	assertJSONRequestSchema(t, exchange, "#/components/schemas/BootstrapExchangeRequest")
	assertJSONResponseSchema(t, exchange, "200", "#/components/schemas/BootstrapSessionResponse")
	assertSetCookieResponse(t, exchange, "200")

	handoff := objectAt(t, objectAt(t, paths, consoleauth.EnrollmentHandoffRoute), "post")
	assertSecurityRequirement(t, handoff, "bootstrapSessionCookie", "csrfHeader")
	assertOriginParameter(t, handoff)
	assertJSONResponseSchema(t, handoff, "201", "#/components/schemas/EnrollmentHandoffResponse")
	if _, exists := handoff["requestBody"]; exists {
		t.Error("enrollment handoff issuance unexpectedly declares a request body")
	}

	handoffExchange := objectAt(t, objectAt(t, paths, consoleauth.EnrollmentHandoffExchangeRoute), "post")
	if security, ok := handoffExchange["security"].([]any); !ok || len(security) != 0 {
		t.Errorf("enrollment handoff exchange security = %#v, want anonymous", handoffExchange["security"])
	}
	assertOriginParameter(t, handoffExchange)
	assertFormRequestSchema(t, handoffExchange, "#/components/schemas/EnrollmentHandoffExchangeRequest")
	exchangeRedirect := objectAt(t, objectAt(t, handoffExchange, "responses"), "303")
	if _, exists := exchangeRedirect["content"]; exists {
		t.Error("enrollment handoff exchange redirect unexpectedly declares a response body")
	}
	exchangeHeaders := objectAt(t, exchangeRedirect, "headers")
	if _, ok := exchangeHeaders["Location"].(map[string]any); !ok {
		t.Error("enrollment handoff exchange redirect omits Location")
	}
	assertSetCookieResponse(t, handoffExchange, "303")

	owner := objectAt(t, objectAt(t, paths, consoleauth.EnrollmentOwnerRoute), "post")
	assertSecurityRequirement(t, owner, "enrollmentSessionCookie", "enrollmentCSRFCookie", "csrfHeader")
	assertOriginParameter(t, owner)
	assertJSONRequestSchema(t, owner, "#/components/schemas/EnrollmentOwnerRequest")
	assertJSONResponseSchema(t, owner, "201", "#/components/schemas/EnrollmentOwnerResponse")
	assertSetCookieResponse(t, owner, "201")

	login := objectAt(t, objectAt(t, paths, "/api/v1/auth/login"), "post")
	assertSecurityRequirement(t, login, "preAuthCSRFCookie", "csrfHeader")
	assertOriginParameter(t, login)
	assertJSONRequestSchema(t, login, "#/components/schemas/LocalLoginRequest")
	assertJSONResponseSchema(t, login, "200", "#/components/schemas/LocalSessionResponse")
	assertSetCookieResponse(t, login, "200")

	logout := objectAt(t, objectAt(t, paths, "/api/v1/auth/logout"), "post")
	assertSecurityRequirement(t, logout, "localSessionCookie", "csrfHeader")
	assertOriginParameter(t, logout)
	assertSetCookieResponse(t, logout, "204")
	if _, exists := logout["requestBody"]; exists {
		t.Error("local logout unexpectedly declares a request body")
	}

	stepUp := objectAt(t, objectAt(t, paths, "/api/v1/auth/step-up"), "post")
	assertSecurityRequirement(t, stepUp, "localSessionCookie", "csrfHeader")
	assertOriginParameter(t, stepUp)
	assertJSONRequestSchema(t, stepUp, "#/components/schemas/LocalStepUpRequest")
	assertJSONResponseSchema(t, stepUp, "200", "#/components/schemas/LocalStepUpResponse")
	stepUpAccess := objectAt(t, stepUp, "x-anas-state-access")
	if len(stepUpAccess) != 1 || objectAt(t, stepUpAccess, "full")["authentication"] != "owner" {
		t.Fatalf("step-up state access = %#v", stepUpAccess)
	}

	schemas := objectAt(t, components, "schemas")
	bootstrapToken := objectAt(t, objectAt(t, objectAt(t, schemas, "BootstrapExchangeRequest"), "properties"), "token")
	handoffToken := objectAt(t, objectAt(t, objectAt(t, schemas, "EnrollmentHandoffExchangeRequest"), "properties"), "handoff")
	enrollmentPassword := objectAt(t, objectAt(t, objectAt(t, schemas, "EnrollmentOwnerRequest"), "properties"), "password")
	localPassword := objectAt(t, objectAt(t, objectAt(t, schemas, "LocalLoginRequest"), "properties"), "password")
	stepUpPassword := objectAt(t, objectAt(t, objectAt(t, schemas, "LocalStepUpRequest"), "properties"), "password")
	if bootstrapToken["writeOnly"] != true || handoffToken["writeOnly"] != true || enrollmentPassword["writeOnly"] != true || localPassword["writeOnly"] != true || stepUpPassword["writeOnly"] != true {
		t.Errorf("credential request fields must be writeOnly: token=%#v handoff=%#v enrollment_password=%#v local_password=%#v step_up_password=%#v", bootstrapToken, handoffToken, enrollmentPassword, localPassword, stepUpPassword)
	}
	assertPropertiesAbsent(t, schemas, "BootstrapSessionResponse", "token", "session_token", "password")
	if _, exists := schemas["EnrollmentSessionResponse"]; exists {
		t.Error("enrollment handoff exchange must not declare a JSON session response")
	}
	assertPropertiesAbsent(t, schemas, "EnrollmentOwnerResponse", "token", "session_token", "password", "handoff", "csrf_token")
	assertPropertiesAbsent(t, schemas, "LocalSessionResponse", "token", "session_token", "password", "transaction_id")
}

func TestOpenAPISystemCertificateAccessContract(t *testing.T) {
	document := readOpenAPIDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")

	system := objectAt(t, objectAt(t, paths, "/api/v1/system"), "get")
	assertJSONResponseSchema(t, system, "200", "#/components/schemas/SystemResponse")
	systemResponse := objectAt(t, schemas, "SystemResponse")
	assertRequiredNames(t, systemResponse, "api_version", "build", "capabilities", "workspace_ids", "certificate_issuer", "console_state", "listener", "direct_recovery_urls", "proxy_url")
	issuer := objectAt(t, objectAt(t, systemResponse, "properties"), "certificate_issuer")
	if got := stringSliceAt(t, issuer, "enum"); !reflect.DeepEqual(got, []string{"none", "temporary", "internal", "acme"}) {
		t.Fatalf("certificate issuer enum = %v", got)
	}
	consoleState := objectAt(t, objectAt(t, systemResponse, "properties"), "console_state")
	if consoleState["$ref"] != "#/components/schemas/ConsoleRuntimeState" {
		t.Fatalf("console state schema = %#v", consoleState)
	}
	if got := stringSliceAt(t, objectAt(t, schemas, "ConsoleRuntimeState"), "enum"); !reflect.DeepEqual(got, []string{"m0", "bootstrap", "enrollment", "full"}) {
		t.Fatalf("console state enum = %v", got)
	}

	ca := objectAt(t, objectAt(t, paths, "/api/v1/system/ca"), "get")
	caResponse := objectAt(t, objectAt(t, ca, "responses"), "200")
	caContent := objectAt(t, caResponse, "content")
	if _, ok := caContent["application/pem-certificate-chain"].(map[string]any); !ok {
		t.Fatalf("CA response content = %#v", caContent)
	}
	disposition := objectAt(t, objectAt(t, caResponse, "headers"), "Content-Disposition")
	if objectAt(t, disposition, "schema")["const"] != `attachment; filename="anas-internal-ca.crt"` {
		t.Fatalf("CA disposition = %#v", disposition)
	}

	redirect := objectAt(t, objectAt(t, paths, "/"), "get")
	redirectResponse := objectAt(t, objectAt(t, redirect, "responses"), "308")
	if _, exists := redirectResponse["content"]; exists {
		t.Fatal("plaintext HTTPS redirect unexpectedly declares a response body")
	}
	if _, ok := objectAt(t, redirectResponse, "headers")["Location"].(map[string]any); !ok {
		t.Fatal("plaintext HTTPS redirect omits Location")
	}
}

func TestOpenAPIJobContractMatchesHandlerProjectionAndSSE(t *testing.T) {
	document := readOpenAPIDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")

	list := objectAt(t, objectAt(t, paths, "/api/v1/jobs"), "get")
	assertJobSecurityAlternatives(t, list)
	assertJSONResponseSchema(t, list, "200", "#/components/schemas/JobListResponse")
	parameters, ok := list["parameters"].([]any)
	if !ok || len(parameters) != 2 {
		t.Fatalf("job list parameters = %#v", list["parameters"])
	}

	detail := objectAt(t, objectAt(t, paths, "/api/v1/jobs/{id}"), "get")
	assertJobSecurityAlternatives(t, detail)
	assertJSONResponseSchema(t, detail, "200", "#/components/schemas/JobDetailResponse")
	assertPropertiesAbsent(t, schemas, "JobDetail", "request", "created_by", "principal", "transaction_id")

	events := objectAt(t, objectAt(t, paths, "/api/v1/jobs/{id}/events"), "get")
	assertJobSecurityAlternatives(t, events)
	streamResponse := objectAt(t, objectAt(t, events, "responses"), "200")
	streamContent := objectAt(t, streamResponse, "content")
	if _, ok := streamContent["text/event-stream"].(map[string]any); !ok {
		t.Fatalf("job event response content = %#v", streamContent)
	}
	terminalResponse := objectAt(t, objectAt(t, events, "responses"), "204")
	if _, hasContent := terminalResponse["content"]; hasContent || !strings.Contains(terminalResponse["description"].(string), "EventSource") {
		t.Errorf("job event terminal response = %#v", terminalResponse)
	}
	gapResponse := objectAt(t, objectAt(t, events, "responses"), "410")
	if gapResponse["$ref"] != "#/components/responses/EventGapProblem" {
		t.Errorf("job event gap response = %#v", gapResponse)
	}
	lastEventID := objectAt(t, objectAt(t, components, "parameters"), "LastEventID")
	if lastEventID["in"] != "header" || lastEventID["name"] != "Last-Event-ID" {
		t.Errorf("LastEventID parameter = %#v", lastEventID)
	}
}

func TestOpenAPIDeploymentContractBindsOneTimeConfirmationAndBootstrapApply(t *testing.T) {
	document := readOpenAPIDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")

	plan := objectAt(t, objectAt(t, paths, "/api/v1/workspaces/{ws}/plans"), "post")
	assertConfigSecurityAlternatives(t, plan, true)
	assertOriginParameter(t, plan)
	assertJSONResponseSchema(t, plan, "200", "#/components/schemas/DeploymentPlanResponse")
	planRequest := objectAt(t, plan, "requestBody")
	if planRequest["required"] != false {
		t.Errorf("plan request body = %#v, want optional", planRequest)
	}
	planRequestContent := objectAt(t, planRequest, "content")
	planRequestMedia := objectAt(t, planRequestContent, "application/json")
	if got := objectAt(t, planRequestMedia, "schema")["$ref"]; got != "#/components/schemas/DeploymentPlanRequest" {
		t.Errorf("plan request schema = %v", got)
	}

	apply := objectAt(t, objectAt(t, paths, "/api/v1/workspaces/{ws}/actions/apply"), "post")
	assertConfigSecurityAlternatives(t, apply, true)
	assertOriginParameter(t, apply)
	assertOperationParameterRefs(t, apply, "#/components/parameters/Origin", "#/components/parameters/IdempotencyKey")
	assertJSONRequestSchema(t, apply, "#/components/schemas/DeploymentApplyRequest")
	assertJSONResponseSchema(t, apply, "202", "#/components/schemas/DeploymentApplyResponse")
	stateAccess := objectAt(t, apply, "x-anas-state-access")
	if len(stateAccess) != 2 || objectAt(t, stateAccess, "bootstrap")["authentication"] != "bootstrap" || objectAt(t, stateAccess, "full")["authentication"] != "owner" {
		t.Fatalf("apply state access = %#v", stateAccess)
	}

	applyProperties := objectAt(t, objectAt(t, schemas, "DeploymentApplyRequest"), "properties")
	if objectAt(t, applyProperties, "confirmation_token")["writeOnly"] != true {
		t.Error("apply confirmation token is not writeOnly")
	}
	if objectAt(t, applyProperties, "step_up_proof")["writeOnly"] != true {
		t.Error("apply step-up proof is not writeOnly")
	}
	planRequestProperties := objectAt(t, objectAt(t, schemas, "DeploymentPlanRequest"), "properties")
	if objectAt(t, planRequestProperties, "step_up_proof")["writeOnly"] != true {
		t.Error("plan step-up proof is not writeOnly")
	}
	assertRequiredNames(t, objectAt(t, schemas, "DeploymentApplyRequest"),
		"plan_job_id", "confirmation_token", "expected_config_validator", "expected_plan_digest", "allow_risky")
	jobErrorProperties := objectAt(t, objectAt(t, schemas, "JobError"), "properties")
	if got := objectAt(t, jobErrorProperties, "detail")["$ref"]; got != "#/components/schemas/JobErrorDetail" {
		t.Errorf("job error detail schema = %v", got)
	}
	assertRequiredNames(t, objectAt(t, schemas, "JobErrorDetail"), "blocked")
	for _, responseSchema := range []string{"DeploymentPlanResponse", "DeploymentApplyResponse"} {
		reachable := reachableOpenAPISchemas(t, schemas, responseSchema)
		if reachable["DeploymentApplyRequest"] {
			t.Errorf("%s response reaches apply request", responseSchema)
		}
		for _, forbidden := range []string{"ConfigCandidate", "DeploymentApplyRequest"} {
			if reachable[forbidden] {
				t.Errorf("%s response reaches request-only schema %s", responseSchema, forbidden)
			}
		}
	}
	planProperties := objectAt(t, objectAt(t, schemas, "DeploymentPlan"), "properties")
	for _, forbidden := range []string{"workspace", "config_path", "module_root", "path", "content_digest"} {
		if _, exists := planProperties[forbidden]; exists {
			t.Errorf("DeploymentPlan exposes %s", forbidden)
		}
	}
}

func TestOpenAPIAuditContractIsOwnerOnlyPaginatedAndValueSafe(t *testing.T) {
	document := readOpenAPIDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")

	list := objectAt(t, objectAt(t, paths, "/api/v1/audit-events"), "get")
	assertSecurityRequirement(t, list, "localSessionCookie")
	assertJSONResponseSchema(t, list, "200", "#/components/schemas/AuditEventListResponse")
	assertOperationParameterRefs(t, list, "#/components/parameters/Limit", "#/components/parameters/Cursor")
	stateAccess := objectAt(t, list, "x-anas-state-access")
	if len(stateAccess) != 1 {
		t.Fatalf("audit state access = %#v", stateAccess)
	}
	full := objectAt(t, stateAccess, "full")
	if full["authentication"] != "owner" || !reflect.DeepEqual(stringSliceAt(t, full, "transports"), []string{"tls"}) {
		t.Fatalf("audit full access = %#v", full)
	}
	auditEvent := objectAt(t, schemas, "AuditEvent")
	assertRequiredNames(t, auditEvent, "sequence", "timestamp", "type")
	properties := objectAt(t, auditEvent, "properties")
	for _, forbidden := range []string{"request", "password", "token", "secret", "credential", "session"} {
		if _, exists := properties[forbidden]; exists {
			t.Errorf("AuditEvent exposes %s", forbidden)
		}
	}
}

func TestOpenAPIConfigContractUsesStrongConditionsAndWriteOnlySensitiveOperations(t *testing.T) {
	document := readOpenAPIDocument(t)
	paths := objectAt(t, document, "paths")
	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")

	configPath := objectAt(t, paths, "/api/v1/workspaces/{ws}/config")
	get := objectAt(t, configPath, "get")
	assertConfigSecurityAlternatives(t, get, false)
	assertJSONResponseSchema(t, get, "200", "#/components/schemas/ConfigSnapshotResponse")
	assertConfigNoStoreResponse(t, get, true)

	validate := objectAt(t, objectAt(t, paths, "/api/v1/workspaces/{ws}/config/validate"), "post")
	assertConfigSecurityAlternatives(t, validate, true)
	assertOriginParameter(t, validate)
	assertJSONRequestSchema(t, validate, "#/components/schemas/ConfigCandidate")
	assertJSONResponseSchema(t, validate, "200", "#/components/schemas/ConfigValidationResponse")
	assertConfigNoStoreResponse(t, validate, false)

	put := objectAt(t, configPath, "put")
	assertConfigSecurityAlternatives(t, put, true)
	assertOriginParameter(t, put)
	assertOperationParameterRefs(t, put,
		"#/components/parameters/Origin",
		"#/components/parameters/ConfigIfMatch",
		"#/components/parameters/ConfigIfNoneMatch",
	)
	assertJSONRequestSchema(t, put, "#/components/schemas/ConfigCandidate")
	assertJSONResponseSchema(t, put, "200", "#/components/schemas/ConfigPutResponse")
	assertConfigNoStoreResponse(t, put, true)
	putResponses := objectAt(t, put, "responses")
	if got := objectAt(t, putResponses, "412")["$ref"]; got != "#/components/responses/PreconditionProblem" {
		t.Errorf("config PUT 412 = %v", got)
	}
	if got := objectAt(t, putResponses, "428")["$ref"]; got != "#/components/responses/PreconditionRequiredProblem" {
		t.Errorf("config PUT 428 = %v", got)
	}

	parameters := objectAt(t, components, "parameters")
	ifMatch := objectAt(t, parameters, "ConfigIfMatch")
	ifNoneMatch := objectAt(t, parameters, "ConfigIfNoneMatch")
	if ifMatch["name"] != "If-Match" || ifMatch["in"] != "header" || ifMatch["required"] != false {
		t.Errorf("ConfigIfMatch = %#v", ifMatch)
	}
	if ifNoneMatch["name"] != "If-None-Match" || ifNoneMatch["in"] != "header" || ifNoneMatch["required"] != false {
		t.Errorf("ConfigIfNoneMatch = %#v", ifNoneMatch)
	}
	if got := objectAt(t, ifMatch, "schema")["pattern"]; got != `^"cfgv-[0-9a-f]{64}"$` {
		t.Errorf("If-Match pattern = %v", got)
	}
	if got := objectAt(t, ifNoneMatch, "schema")["const"]; got != "*" {
		t.Errorf("If-None-Match const = %v", got)
	}
	headers := objectAt(t, components, "headers")
	managedETag := objectAt(t, headers, "ManagedConfigETag")
	if got := objectAt(t, managedETag, "schema")["pattern"]; got != `^"cfgv-[0-9a-f]{64}"$` {
		t.Errorf("managed config ETag pattern = %v", got)
	}
	configValidator := objectAt(t, schemas, "ConfigValidator")
	if got := configValidator["pattern"]; got != "^cfgv-[0-9a-f]{64}$" {
		t.Errorf("config validator pattern = %v", got)
	}

	candidate := objectAt(t, schemas, "ConfigCandidate")
	assertRequiredNames(t, candidate, "config")
	configField := objectAt(t, schemas, "ConfigField")
	if !slices.Contains(stringSliceAt(t, configField, "required"), "document_path") {
		t.Error("ConfigField.document_path is not required")
	}
	documentPath := objectAt(t, objectAt(t, configField, "properties"), "document_path")
	if got := documentPath["minItems"]; got != 1 {
		t.Errorf("ConfigField.document_path minItems = %v", got)
	}
	for _, responseName := range []string{"ConfigSnapshotResponse", "ConfigPutResponse"} {
		responseSchema := objectAt(t, schemas, responseName)
		if !slices.Contains(stringSliceAt(t, responseSchema, "required"), "available_modules") {
			t.Errorf("%s.available_modules is not required", responseName)
		}
		availableModules := objectAt(t, objectAt(t, responseSchema, "properties"), "available_modules")
		if availableModules["uniqueItems"] != true {
			t.Errorf("%s.available_modules is not unique: %#v", responseName, availableModules)
		}
	}
	sensitive := objectAt(t, objectAt(t, candidate, "properties"), "sensitive")
	additional := objectAt(t, sensitive, "additionalProperties")
	if additional["$ref"] != "#/components/schemas/ConfigSensitiveMutation" {
		t.Errorf("sensitive operation values = %#v", additional)
	}
	mutation := objectAt(t, schemas, "ConfigSensitiveMutation")
	branches, ok := mutation["oneOf"].([]any)
	if !ok || len(branches) != 3 {
		t.Fatalf("ConfigSensitiveMutation.oneOf = %#v", mutation["oneOf"])
	}
	wantOperations := map[string]bool{"unchanged": false, "set": false, "unset": false}
	for _, rawBranch := range branches {
		branch, ok := rawBranch.(map[string]any)
		if !ok {
			t.Fatalf("sensitive mutation branch = %#v", rawBranch)
		}
		ref, _ := branch["$ref"].(string)
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		variant := objectAt(t, schemas, name)
		properties := objectAt(t, variant, "properties")
		operation := objectAt(t, properties, "operation")["const"]
		op, ok := operation.(string)
		if !ok {
			t.Fatalf("%s operation const = %#v", name, operation)
		}
		if _, known := wantOperations[op]; !known {
			t.Errorf("unexpected sensitive operation %q", op)
		} else {
			wantOperations[op] = true
		}
		_, hasValue := properties["value"]
		if op == "set" {
			assertRequiredNames(t, variant, "operation", "value")
			if !hasValue || objectAt(t, properties, "value")["writeOnly"] != true {
				t.Errorf("set value is not request-only: %#v", properties["value"])
			}
		} else {
			assertRequiredNames(t, variant, "operation")
			if hasValue {
				t.Errorf("%s unexpectedly accepts value", op)
			}
		}
	}
	for operation, found := range wantOperations {
		if !found {
			t.Errorf("sensitive union omits %s", operation)
		}
	}

	publicConfig := objectAt(t, schemas, "ConfigPublicDocument")
	assertRequiredNames(t, objectAt(t, publicConfig, "not"), "secrets")
	for _, responseSchema := range []string{"ConfigSnapshotResponse", "ConfigValidationResponse", "ConfigPutResponse"} {
		reachable := reachableOpenAPISchemas(t, schemas, responseSchema)
		for _, requestOnly := range []string{"ConfigCandidate", "ConfigSensitiveMutation", "ConfigSensitiveSet", "ConfigSensitiveUnset", "ConfigSensitiveUnchanged"} {
			if reachable[requestOnly] {
				t.Errorf("%s response reaches request-only schema %s", responseSchema, requestOnly)
			}
		}
		for name := range reachable {
			properties, _ := objectAtOptional(schemas[name], "properties")
			if _, exposesValue := properties["value"]; exposesValue {
				t.Errorf("%s response reaches %s.value", responseSchema, name)
			}
		}
	}
}

func assertConfigSecurityAlternatives(t *testing.T, operation map[string]any, csrf bool) {
	t.Helper()
	raw, ok := operation["security"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("config security = %#v, want bootstrap/local alternatives", operation["security"])
	}
	wantCookies := map[string]bool{"bootstrapSessionCookie": false, "localSessionCookie": false}
	for _, item := range raw {
		requirement, ok := item.(map[string]any)
		wantLength := 1
		if csrf {
			wantLength = 2
		}
		if !ok || len(requirement) != wantLength {
			t.Fatalf("config security requirement = %#v", item)
		}
		cookie := ""
		for name, scopes := range requirement {
			if list, ok := scopes.([]any); !ok || len(list) != 0 {
				t.Errorf("config security scheme %s scopes = %#v", name, scopes)
			}
			if name == "csrfHeader" {
				if !csrf {
					t.Error("safe config read unexpectedly requires CSRF")
				}
				continue
			}
			cookie = name
		}
		if _, known := wantCookies[cookie]; !known {
			t.Errorf("unexpected config credential %q", cookie)
		} else {
			wantCookies[cookie] = true
		}
		if csrf {
			if _, exists := requirement["csrfHeader"]; !exists {
				t.Error("unsafe config operation omits CSRF header")
			}
		}
	}
	for cookie, found := range wantCookies {
		if !found {
			t.Errorf("config security omits %s", cookie)
		}
	}
}

func assertConfigNoStoreResponse(t *testing.T, operation map[string]any, etag bool) {
	t.Helper()
	response := objectAt(t, objectAt(t, operation, "responses"), "200")
	headers := objectAt(t, response, "headers")
	cache := objectAt(t, headers, "Cache-Control")
	if cache["$ref"] != "#/components/headers/CacheControlNoStore" {
		t.Errorf("config Cache-Control = %#v", cache)
	}
	entityTag, hasETag := headers["ETag"].(map[string]any)
	if etag {
		if !hasETag || entityTag["$ref"] != "#/components/headers/ManagedConfigETag" {
			t.Errorf("config ETag = %#v", headers["ETag"])
		}
	} else if hasETag {
		t.Errorf("validation unexpectedly declares ETag = %#v", entityTag)
	}
}

func assertOperationParameterRefs(t *testing.T, operation map[string]any, want ...string) {
	t.Helper()
	raw, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("operation parameters = %#v", operation["parameters"])
	}
	got := make([]string, 0, len(raw))
	for _, item := range raw {
		parameter, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("operation parameter = %#v", item)
		}
		ref, _ := parameter["$ref"].(string)
		got = append(got, ref)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("operation parameter refs = %v, want %v", got, want)
	}
}

func assertRequiredNames(t *testing.T, schema map[string]any, want ...string) {
	t.Helper()
	got := stringSliceAt(t, schema, "required")
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

func reachableOpenAPISchemas(t *testing.T, schemas map[string]any, root string) map[string]bool {
	t.Helper()
	reachable := map[string]bool{}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if rawRef, ok := typed["$ref"].(string); ok && strings.HasPrefix(rawRef, "#/components/schemas/") {
				name := strings.TrimPrefix(rawRef, "#/components/schemas/")
				if !reachable[name] {
					reachable[name] = true
					visit(schemas[name])
				}
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	reachable[root] = true
	visit(schemas[root])
	return reachable
}

func objectAtOptional(value any, key string) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	child, ok := object[key].(map[string]any)
	return child, ok
}

func assertJobSecurityAlternatives(t *testing.T, operation map[string]any) {
	t.Helper()
	raw, ok := operation["security"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("job security = %#v, want bootstrap/local alternatives", operation["security"])
	}
	want := map[string]bool{"bootstrapSessionCookie": false, "localSessionCookie": false}
	for _, item := range raw {
		requirement, ok := item.(map[string]any)
		if !ok || len(requirement) != 1 {
			t.Fatalf("job security requirement = %#v", item)
		}
		for name, scopes := range requirement {
			if _, known := want[name]; !known {
				t.Errorf("unexpected job security scheme %q", name)
				continue
			}
			if list, ok := scopes.([]any); !ok || len(list) != 0 {
				t.Errorf("job security scheme %s scopes = %#v", name, scopes)
			}
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("job security omits %s", name)
		}
	}
}

func assertSecurityRequirement(t *testing.T, operation map[string]any, names ...string) {
	t.Helper()
	raw, ok := operation["security"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("security = %#v, want one combined requirement", operation["security"])
	}
	requirement, ok := raw[0].(map[string]any)
	if !ok {
		t.Fatalf("security[0] is %T, want object", raw[0])
	}
	want := append([]string(nil), names...)
	got := make([]string, 0, len(requirement))
	for name, scopes := range requirement {
		got = append(got, name)
		if list, ok := scopes.([]any); !ok || len(list) != 0 {
			t.Errorf("security scheme %s scopes = %#v, want []", name, scopes)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("security schemes = %v, want %v", got, want)
	}
}

func assertSecurityRequirementAlternatives(t *testing.T, operation map[string]any, alternatives ...[]string) {
	t.Helper()
	raw, ok := operation["security"].([]any)
	if !ok || len(raw) != len(alternatives) {
		t.Fatalf("security = %#v, want %d alternatives", operation["security"], len(alternatives))
	}
	for index, names := range alternatives {
		requirement, ok := raw[index].(map[string]any)
		if !ok {
			t.Fatalf("security[%d] is %T, want object", index, raw[index])
		}
		want := append([]string(nil), names...)
		got := make([]string, 0, len(requirement))
		for name, scopes := range requirement {
			got = append(got, name)
			if list, ok := scopes.([]any); !ok || len(list) != 0 {
				t.Errorf("security scheme %s scopes = %#v, want []", name, scopes)
			}
		}
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("security alternative %d schemes = %v, want %v", index, got, want)
		}
	}
}

func assertOriginParameter(t *testing.T, operation map[string]any) {
	t.Helper()
	parameters, ok := operation["parameters"].([]any)
	if !ok {
		t.Fatalf("parameters = %T, want array", operation["parameters"])
	}
	for _, raw := range parameters {
		parameter, ok := raw.(map[string]any)
		if ok && parameter["$ref"] == "#/components/parameters/Origin" {
			return
		}
	}
	t.Error("operation omits required Origin parameter")
}

func assertJSONRequestSchema(t *testing.T, operation map[string]any, wantRef string) {
	t.Helper()
	requestBody := objectAt(t, operation, "requestBody")
	if requestBody["required"] != true {
		t.Error("authentication request body is not required")
	}
	content := objectAt(t, requestBody, "content")
	schema := objectAt(t, objectAt(t, content, "application/json"), "schema")
	if schema["$ref"] != wantRef {
		t.Errorf("request schema = %v, want %s", schema["$ref"], wantRef)
	}
}

func assertFormRequestSchema(t *testing.T, operation map[string]any, wantRef string) {
	t.Helper()
	requestBody := objectAt(t, operation, "requestBody")
	if requestBody["required"] != true {
		t.Error("form request body is not required")
	}
	content := objectAt(t, requestBody, "content")
	schema := objectAt(t, objectAt(t, content, "application/x-www-form-urlencoded"), "schema")
	if schema["$ref"] != wantRef {
		t.Errorf("form request schema = %v, want %s", schema["$ref"], wantRef)
	}
}

func assertJSONResponseSchema(t *testing.T, operation map[string]any, status, wantRef string) {
	t.Helper()
	response := objectAt(t, objectAt(t, operation, "responses"), status)
	content := objectAt(t, response, "content")
	schema := objectAt(t, objectAt(t, content, "application/json"), "schema")
	if schema["$ref"] != wantRef {
		t.Errorf("response %s schema = %v, want %s", status, schema["$ref"], wantRef)
	}
}

func assertSetCookieResponse(t *testing.T, operation map[string]any, status string) {
	t.Helper()
	response := objectAt(t, objectAt(t, operation, "responses"), status)
	headers := objectAt(t, response, "headers")
	if _, ok := headers["Set-Cookie"].(map[string]any); !ok {
		t.Errorf("response %s omits Set-Cookie header", status)
	}
}

func stringSliceAt(t *testing.T, object map[string]any, key string) []string {
	t.Helper()
	raw, ok := object[key].([]any)
	if !ok {
		t.Fatalf("%s is %T, want array", key, object[key])
	}
	result := make([]string, 0, len(raw))
	for _, value := range raw {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("%s contains %T, want string", key, value)
		}
		result = append(result, text)
	}
	return result
}

func assertInternalOpenAPIReferencesResolve(t *testing.T, document map[string]any) {
	t.Helper()
	var visit func(any, string)
	visit = func(value any, location string) {
		switch typed := value.(type) {
		case map[string]any:
			if rawRef, ok := typed["$ref"]; ok {
				ref, ok := rawRef.(string)
				if !ok || !strings.HasPrefix(ref, "#/") {
					t.Errorf("%s has unsupported reference %v", location, rawRef)
				} else if !openAPIReferenceExists(document, ref) {
					t.Errorf("%s references missing %s", location, ref)
				}
			}
			for key, child := range typed {
				visit(child, location+"/"+key)
			}
		case []any:
			for _, child := range typed {
				visit(child, location+"/[]")
			}
		}
	}
	visit(document, "#")
}

func openAPIReferenceExists(document map[string]any, ref string) bool {
	var current any = document
	for _, encoded := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(encoded, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[key]
		if !ok {
			return false
		}
	}
	return true
}

func assertRequiredPropertiesAreUnique(t *testing.T, schemas map[string]any) {
	t.Helper()
	for schemaName, rawSchema := range schemas {
		schema, ok := rawSchema.(map[string]any)
		if !ok {
			continue
		}
		required, ok := schema["required"].([]any)
		if !ok {
			continue
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s declares required fields without properties", schemaName)
			continue
		}
		seen := make(map[string]struct{}, len(required))
		for _, rawName := range required {
			name, ok := rawName.(string)
			if !ok {
				t.Errorf("%s has non-string required field %v", schemaName, rawName)
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				t.Errorf("%s repeats required field %q", schemaName, name)
			}
			seen[name] = struct{}{}
			if _, exists := properties[name]; !exists {
				t.Errorf("%s requires undeclared property %q", schemaName, name)
			}
		}
	}
}

func TestOpenAPIDeploymentProjectionOmitsSensitivePersistedFields(t *testing.T) {
	document := readOpenAPIDocument(t)
	schemas := objectAt(t, objectAt(t, document, "components"), "schemas")

	assertPropertiesAbsent(t, schemas, "Deployment", "config_fingerprint")
	assertPropertiesAbsent(t, schemas, "DeploymentModule",
		"render_digest", "compose_file", "hook", "changes", "contract_providers", "local_accounts")
	assertPropertiesAbsent(t, schemas, "DeploymentSetting", "fingerprint")
	assertPropertiesAbsent(t, schemas, "DeploymentResource", "spec", "password_secret", "credential_secret")
	assertPropertiesAbsent(t, schemas, "DeploymentSnapshotPolicy", "source", "root")
	assertPropertiesAbsent(t, schemas, "DeploymentDetailResponse", "workspace", "deployment_path")
	assertPropertiesAbsent(t, schemas, "ModuleCommand", "handler", "executor", "env", "secrets", "workspace")
}

func assertPropertiesAbsent(t *testing.T, schemas map[string]any, schemaName string, names ...string) {
	t.Helper()
	properties := objectAt(t, objectAt(t, schemas, schemaName), "properties")
	for _, name := range names {
		if _, ok := properties[name]; ok {
			t.Errorf("%s exposes persisted field %q", schemaName, name)
		}
	}
}

func readOpenAPIDocument(t *testing.T) map[string]any {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate OpenAPI test source")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "api", "openapi.yaml"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func objectAt(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", key, object[key])
	}
	return value
}
