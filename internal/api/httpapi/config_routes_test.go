package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

const testManagedConfigValidator = "cfgv-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type fakeConfigService struct {
	snapshot            application.ConfigSnapshot
	snapshotErr         error
	validation          application.ConfigValidationResult
	validationErr       error
	putResult           application.ConfigPutResult
	putErr              error
	putRequest          application.ConfigPutRequest
	getCalls            int
	validateCalls       int
	putCalls            int
	committed           bool
	observerIntent      application.ConfigCommitIntent
	afterObserverErr    error
	afterCommit         func()
	validationCandidate application.ConfigCandidate
}

func (service *fakeConfigService) GetConfig(context.Context) (application.ConfigSnapshot, error) {
	service.getCalls++
	return service.snapshot, service.snapshotErr
}

func (service *fakeConfigService) ValidateConfig(_ context.Context, candidate application.ConfigCandidate) (application.ConfigValidationResult, error) {
	service.validateCalls++
	service.validationCandidate = candidate
	return service.validation, service.validationErr
}

func (service *fakeConfigService) PutConfig(ctx context.Context, request application.ConfigPutRequest, observer application.ConfigCommitObserver) (application.ConfigPutResult, error) {
	service.putCalls++
	service.putRequest = request
	if service.putErr != nil {
		return application.ConfigPutResult{}, service.putErr
	}
	intent := service.observerIntent
	if intent.CandidateValidator == "" {
		intent = application.ConfigCommitIntent{
			CurrentValidator: testManagedConfigValidator, CandidateValidator: testManagedConfigValidator,
			Changes: []application.ConfigChange{{Path: "modules.demo.enabled", Change: "change", Effect: "runtime", Editable: true}},
		}
	}
	if intent.OperationID == "" {
		intent.OperationID = request.OperationID
	}
	if err := observer.BeforeConfigCommit(ctx, intent); err != nil {
		return application.ConfigPutResult{}, &application.Error{
			Kind: application.ErrorKindInternal, Code: "audit_unavailable", Message: "audit unavailable", Cause: err,
		}
	}
	if service.afterObserverErr != nil {
		return application.ConfigPutResult{}, service.afterObserverErr
	}
	service.committed = true
	if service.afterCommit != nil {
		service.afterCommit()
	}
	result := service.putResult
	if result.Validator == "" {
		result = application.ConfigPutResult{
			PreviousValidator: intent.CurrentValidator, Validator: intent.CandidateValidator,
			Config:  application.ConfigDocument{"modules": map[string]any{"demo": map[string]any{"enabled": true}}},
			Changes: intent.Changes,
		}
	}
	return result, nil
}

type recordingConfigAuditSink struct {
	events        []ConfigAuditEvent
	contextErrors []error
	failStage     string
}

func (sink *recordingConfigAuditSink) RecordConfigEvent(ctx context.Context, event ConfigAuditEvent) error {
	event.Changes = append([]ConfigAuditChange(nil), event.Changes...)
	sink.events = append(sink.events, event)
	sink.contextErrors = append(sink.contextErrors, ctx.Err())
	if event.Stage == sink.failStage {
		return errors.New("audit offline")
	}
	return nil
}

func TestValidateConfigPreservesJSONIntegersBeyondFloat64Precision(t *testing.T) {
	service := &fakeConfigService{validation: application.ConfigValidationResult{
		BaseValidator: testManagedConfigValidator, Config: application.ConfigDocument{},
	}}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, &recordingConfigAuditSink{}, 0)
	response := configRequest(handler, http.MethodPost, "/api/v1/workspaces/main/config/validate", `{"config":{"future_integer":9007199254740993},"sensitive":{}}`, false)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d, %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "candidate_digest") || strings.Contains(response.Body.String(), "candidate_validator") || !strings.Contains(response.Body.String(), `"base_validator":"`+testManagedConfigValidator+`"`) {
		t.Fatalf("validation response exposed or omitted validator fields: %s", response.Body.String())
	}
	value, ok := service.validationCandidate.Document["future_integer"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("decoded integer = %#v (%T)", service.validationCandidate.Document["future_integer"], service.validationCandidate.Document["future_integer"])
	}
}

func newConfigRouteHandler(t *testing.T, state ConsoleState, listener ListenerIdentity, service application.ConfigService, auditSink ConfigAuditSink, maxBytes int64) http.Handler {
	t.Helper()
	registry, _ := testRegistry(t, "main")
	handler, err := NewHandlerWithConfig(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: state,
		HostAllowed:  func(*http.Request) bool { return true },
		Listener:     listener,
		Authorize: func(_ *http.Request, request AuthorizationRequest) (Principal, error) {
			access := request.Policy.Access[state]
			return Principal{ID: "actor-1", Role: string(access.Authentication), Source: "test"}, nil
		},
	}, ConfigOptions{
		Factory: func(string) application.ConfigService { return service }, Audit: auditSink, MaxRequestBytes: maxBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func configRequest(handler http.Handler, method, path, body string, tlsEnabled bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if tlsEnabled {
		request.TLS = &tls.ConnectionState{}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestConfigRoutesDeclareClosedCapabilityPolicies(t *testing.T) {
	inventory, err := RouteInventory(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Permission{
		http.MethodGet + " /api/v1/workspaces/{ws}/config":           PermissionConfigRead,
		http.MethodPost + " /api/v1/workspaces/{ws}/config/validate": PermissionConfigValidate,
		http.MethodPut + " /api/v1/workspaces/{ws}/config":           PermissionConfigWrite,
	}
	for _, policy := range inventory {
		key := policy.Method + " " + policy.Pattern
		permission, exists := want[key]
		if !exists {
			continue
		}
		delete(want, key)
		if policy.Permission != permission || policy.Scope != ScopeWorkspace || !reflect.DeepEqual(policy.Listeners, []ListenerIdentity{ListenerDirect, ListenerTrustedProxy}) {
			t.Errorf("policy = %#v", policy)
		}
		if len(policy.Access) != 2 || policy.Access[StateBootstrap].Authentication != AuthenticationBootstrap ||
			policy.Access[StateFull].Authentication != AuthenticationOwner ||
			len(policy.Access[StateBootstrap].Transports) != 2 || len(policy.Access[StateFull].Transports) != 1 ||
			policy.Access[StateFull].Transports[0] != TransportTLS {
			t.Errorf("access = %#v", policy.Access)
		}
		if _, exists := policy.Access[StateM0]; exists {
			t.Error("M0 unexpectedly exposes config route")
		}
		if _, exists := policy.Access[StateEnrollment]; exists {
			t.Error("enrollment unexpectedly exposes config route")
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing config policies: %#v", want)
	}
}

func TestConfigRouteVisibilityMatchesCapabilityAndTransport(t *testing.T) {
	for _, test := range []struct {
		name     string
		state    ConsoleState
		listener ListenerIdentity
		tls      bool
		want     int
	}{
		{name: "bootstrap plaintext", state: StateBootstrap, listener: ListenerDirect, want: http.StatusOK},
		{name: "bootstrap TLS", state: StateBootstrap, listener: ListenerDirect, tls: true, want: http.StatusOK},
		{name: "full TLS", state: StateFull, listener: ListenerDirect, tls: true, want: http.StatusOK},
		{name: "full plaintext hidden", state: StateFull, listener: ListenerDirect, want: http.StatusNotFound},
		{name: "enrollment hidden", state: StateEnrollment, listener: ListenerDirect, tls: true, want: http.StatusNotFound},
		{name: "M0 hidden", state: StateM0, listener: ListenerDirect, want: http.StatusNotFound},
		{name: "trusted proxy full TLS", state: StateFull, listener: ListenerTrustedProxy, tls: true, want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeConfigService{snapshot: application.ConfigSnapshot{Config: application.ConfigDocument{}}}
			handler := newConfigRouteHandler(t, test.state, test.listener, service, &recordingConfigAuditSink{}, 0)
			response := configRequest(handler, http.MethodGet, "/api/v1/workspaces/main/config", "", test.tls)
			if response.Code != test.want {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGetConfigReturnsStrongManagedETagAndPrivateDTO(t *testing.T) {
	service := &fakeConfigService{snapshot: application.ConfigSnapshot{
		Managed: true, Validator: testManagedConfigValidator,
		Config:           application.ConfigDocument{"modules": map[string]any{"demo": map[string]any{"enabled": true}}},
		AvailableModules: []string{"demo", "fieldless"},
		Fields: []application.ConfigField{{
			Path: "modules.demo.password", DocumentPath: []string{"modules", "demo", "config", "password"},
			Sensitive: true, SensitiveState: "set", Editable: true,
		}},
	}}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, &recordingConfigAuditSink{}, 0)
	response := configRequest(handler, http.MethodGet, "/api/v1/workspaces/main/config", "", false)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"`+testManagedConfigValidator+`"` {
		t.Fatalf("response = %d ETag=%q body=%s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "secret-value") {
		t.Fatalf("unsafe response headers/body: %#v %s", response.Header(), response.Body.String())
	}
	var document configSnapshotResponse
	decodeResponse(t, response, &document)
	if document.APIVersion != APIVersion || document.WorkspaceID != "main" || !document.Managed || !reflect.DeepEqual(document.AvailableModules, []string{"demo", "fieldless"}) || len(document.Fields) != 1 || document.Fields[0].SensitiveState != "set" || !reflect.DeepEqual(document.Fields[0].DocumentPath, []string{"modules", "demo", "config", "password"}) {
		t.Fatalf("document = %#v", document)
	}
}

func TestSystemReportsConfigurationWriteCapability(t *testing.T) {
	service := &fakeConfigService{snapshot: application.ConfigSnapshot{Config: application.ConfigDocument{}}}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, &recordingConfigAuditSink{}, 0)
	response := configRequest(handler, http.MethodGet, "/api/v1/system", "", false)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d, %s", response.Code, response.Body.String())
	}
	var document struct {
		Capabilities struct {
			ReadOnly bool `json:"read_only"`
		} `json:"capabilities"`
	}
	decodeResponse(t, response, &document)
	if document.Capabilities.ReadOnly {
		t.Fatal("configured handler still reported a read-only API")
	}
}

func TestPutConfigWithoutConfigOptionsFailsClosed(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	for _, state := range []ConsoleState{StateBootstrap, StateFull} {
		t.Run(string(state), func(t *testing.T) {
			handler, err := NewHandlerWithSecurity(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
				InitialState: state,
				HostAllowed:  func(*http.Request) bool { return true },
				Listener:     ListenerDirect,
				Authorize: func(_ *http.Request, request AuthorizationRequest) (Principal, error) {
					return Principal{ID: "actor-1", Role: string(request.Policy.Access[state].Authentication), Source: "test"}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{}}`))
			if state == StateFull {
				request.TLS = &tls.ConnectionState{}
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"config_unavailable"`) {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestValidateConfigUsesStrictBoundedJSON(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		maxBytes    int64
		want        int
	}{
		{name: "valid", body: `{"config":{"modules":{}},"sensitive":{}}`, contentType: "application/json", want: http.StatusOK},
		{name: "valid omitted sensitive", body: `{"config":{}}`, contentType: "application/json", want: http.StatusOK},
		{name: "valid sensitive set", body: `{"config":{},"sensitive":{"demo.password":{"operation":"set","value":"secret"}}}`, contentType: "application/json", want: http.StatusOK},
		{name: "unknown envelope field", body: `{"config":{},"sensitive":{},"extra":true}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "case alias", body: `{"Config":{},"sensitive":{}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "duplicate envelope field", body: `{"config":{},"config":{"modules":{}},"sensitive":{}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "duplicate nested field", body: `{"config":{"modules":{},"modules":{"demo":{}}},"sensitive":{}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "unknown sensitive field", body: `{"config":{},"sensitive":{"modules.demo.password":{"operation":"unchanged","extra":true}}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "unchanged null value", body: `{"config":{},"sensitive":{"demo.password":{"operation":"unchanged","value":null}}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "unset null value", body: `{"config":{},"sensitive":{"demo.password":{"operation":"unset","value":null}}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "set null value", body: `{"config":{},"sensitive":{"demo.password":{"operation":"set","value":null}}}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "null sensitive", body: `{"config":{},"sensitive":null}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "trailing document", body: `{"config":{},"sensitive":{}} {}`, contentType: "application/json", want: http.StatusBadRequest},
		{name: "wrong media type", body: `{"config":{},"sensitive":{}}`, contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "too large", body: `{"config":{"value":"` + strings.Repeat("x", 256) + `"},"sensitive":{}}`, contentType: "application/json", maxBytes: 64, want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeConfigService{validation: application.ConfigValidationResult{
				BaseValidator: testManagedConfigValidator, Config: application.ConfigDocument{}, Changes: []application.ConfigChange{},
			}}
			handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, &recordingConfigAuditSink{}, test.maxBytes)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/main/config/validate", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
			if test.want == http.StatusOK && service.validateCalls != 1 || test.want != http.StatusOK && service.validateCalls != 0 {
				t.Fatalf("validate calls = %d", service.validateCalls)
			}
			if test.want != http.StatusOK && response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("content type = %q", response.Header().Get("Content-Type"))
			}
		})
	}
}

func TestPutConfigParsesOnlyExactSinglePreconditions(t *testing.T) {
	tests := []struct {
		name       string
		ifMatch    []string
		ifNone     []string
		want       int
		wantMode   application.ConfigPreconditionMode
		wantDigest string
	}{
		{name: "strong match", ifMatch: []string{`"` + testManagedConfigValidator + `"`}, want: http.StatusOK, wantMode: application.ConfigPreconditionMatch, wantDigest: testManagedConfigValidator},
		{name: "first managed", ifNone: []string{"*"}, want: http.StatusOK, wantMode: application.ConfigPreconditionMustCreate},
		{name: "missing", want: http.StatusPreconditionRequired},
		{name: "both families", ifMatch: []string{`"old"`}, ifNone: []string{"*"}, want: http.StatusBadRequest},
		{name: "multiple", ifMatch: []string{`"one"`, `"two"`}, want: http.StatusBadRequest},
		{name: "list", ifMatch: []string{`"one", "two"`}, want: http.StatusBadRequest},
		{name: "malformed", ifMatch: []string{"not-quoted"}, want: http.StatusBadRequest},
		{name: "weak", ifMatch: []string{`W/"old"`}, want: http.StatusPreconditionFailed},
		{name: "match wildcard", ifMatch: []string{"*"}, want: http.StatusPreconditionFailed},
		{name: "none strong tag", ifNone: []string{`"old"`}, want: http.StatusPreconditionFailed},
		{name: "none weak tag", ifNone: []string{`W/"old"`}, want: http.StatusPreconditionFailed},
		{name: "none malformed", ifNone: []string{"old"}, want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeConfigService{}
			auditSink := &recordingConfigAuditSink{}
			handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
			request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
			request.Header.Set("Content-Type", "application/json")
			for _, value := range test.ifMatch {
				request.Header.Add("If-Match", value)
			}
			for _, value := range test.ifNone {
				request.Header.Add("If-None-Match", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
			if test.want == http.StatusOK {
				if service.putCalls != 1 || service.putRequest.Precondition != test.wantMode || service.putRequest.ExpectedValidator != test.wantDigest {
					t.Fatalf("put request = %#v, calls=%d", service.putRequest, service.putCalls)
				}
			} else if service.putCalls != 0 {
				t.Fatalf("rejected precondition invoked PutConfig %d times", service.putCalls)
			}
			if len(auditSink.events) < 2 || auditSink.events[0].Stage != ConfigAuditAttempt {
				t.Fatalf("audit events = %#v", auditSink.events)
			}
		})
	}
}

func TestPutConfigAuditsAuthenticatedAttemptsBeforeWorkspaceResolution(t *testing.T) {
	service := &fakeConfigService{}
	auditSink := &recordingConfigAuditSink{}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/missing/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-None-Match", "*")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || service.putCalls != 0 {
		t.Fatalf("response=%d calls=%d body=%s", response.Code, service.putCalls, response.Body.String())
	}
	if len(auditSink.events) != 2 || auditSink.events[0].Stage != ConfigAuditAttempt ||
		auditSink.events[1].Stage != ConfigAuditFailure || auditSink.events[0].OperationID == "" ||
		auditSink.events[0].OperationID != auditSink.events[1].OperationID {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
}

func TestConfigAuditOperationIDUses128BitRandomShape(t *testing.T) {
	first, err := newConfigAuditOperationID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newConfigAuditOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len("cfg-")+32 || !strings.HasPrefix(first, "cfg-") || first == second {
		t.Fatalf("operation IDs = %q, %q", first, second)
	}
}

func TestPutConfigMapsWrongHeaderFamilyAndStaleDigest(t *testing.T) {
	for _, test := range []struct {
		name   string
		header string
		value  string
		kind   application.ErrorKind
		want   int
	}{
		{name: "managed with create header", header: "If-None-Match", value: "*", kind: application.ErrorKindPreconditionRequired, want: http.StatusPreconditionRequired},
		{name: "first with match header", header: "If-Match", value: `"` + testManagedConfigValidator + `"`, kind: application.ErrorKindPreconditionRequired, want: http.StatusPreconditionRequired},
		{name: "stale", header: "If-Match", value: `"stale"`, kind: application.ErrorKindFailedPrecondition, want: http.StatusPreconditionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeConfigService{putErr: &application.Error{Kind: test.kind, Code: "config_precondition_failed", Message: "precondition failed"}}
			handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, &recordingConfigAuditSink{}, 0)
			request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(test.header, test.value)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPutConfigAuditVetoesBeforeCommitAndNeverCarriesCandidateValues(t *testing.T) {
	service := &fakeConfigService{observerIntent: application.ConfigCommitIntent{
		CurrentValidator: testManagedConfigValidator, CandidateValidator: testManagedConfigValidator,
		Changes: []application.ConfigChange{{Path: "modules.demo.password", Change: "change", Effect: "runtime", Sensitive: true, Editable: true}},
	}}
	auditSink := &recordingConfigAuditSink{failStage: ConfigAuditAuthorized}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{"modules.demo.password":{"operation":"set","value":"super-secret-value"}}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || service.committed {
		t.Fatalf("response = %d committed=%t body=%s", response.Code, service.committed, response.Body.String())
	}
	if strings.Contains(fmt.Sprintf("%#v", auditSink.events), "super-secret-value") {
		t.Fatalf("audit leaked candidate value: %#v", auditSink.events)
	}
	if len(auditSink.events) != 3 || auditSink.events[0].Stage != ConfigAuditAttempt || auditSink.events[1].Stage != ConfigAuditAuthorized || auditSink.events[2].Stage != ConfigAuditFailure {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
}

func TestPutConfigRequiresAttemptAuditAndRecordsAuthorizedSuccess(t *testing.T) {
	service := &fakeConfigService{}
	auditSink := &recordingConfigAuditSink{}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.committed || response.Header().Get("ETag") != `"`+testManagedConfigValidator+`"` {
		t.Fatalf("response = %d committed=%t ETag=%q body=%s", response.Code, service.committed, response.Header().Get("ETag"), response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"digest"`) || !strings.Contains(response.Body.String(), `"validator":"`+testManagedConfigValidator+`"`) {
		t.Fatalf("PUT response validator schema = %s", response.Body.String())
	}
	if len(auditSink.events) != 3 || auditSink.events[0].Stage != ConfigAuditAttempt || auditSink.events[1].Stage != ConfigAuditAuthorized || auditSink.events[2].Stage != ConfigAuditSuccess {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
	if auditSink.events[1].CandidateValidator != testManagedConfigValidator || len(auditSink.events[1].Changes) != 1 {
		t.Fatalf("authorized audit = %#v", auditSink.events[1])
	}
	operationID := auditSink.events[0].OperationID
	if operationID == "" || service.putRequest.OperationID != operationID ||
		auditSink.events[1].OperationID != operationID || auditSink.events[2].OperationID != operationID {
		t.Fatalf("audit operation correlation = request %q events %#v", service.putRequest.OperationID, auditSink.events)
	}

	blockedService := &fakeConfigService{}
	blockedAudit := &recordingConfigAuditSink{failStage: ConfigAuditAttempt}
	blocked := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, blockedService, blockedAudit, 0)
	blockedRequest := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
	blockedRequest.Header.Set("Content-Type", "application/json")
	blockedRequest.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	blockedResponse := httptest.NewRecorder()
	blocked.ServeHTTP(blockedResponse, blockedRequest)
	if blockedResponse.Code != http.StatusServiceUnavailable || blockedService.putCalls != 0 {
		t.Fatalf("blocked response = %d calls=%d body=%s", blockedResponse.Code, blockedService.putCalls, blockedResponse.Body.String())
	}
}

func TestPutConfigTerminalAuditFailureDoesNotRewriteCommittedResponse(t *testing.T) {
	service := &fakeConfigService{}
	auditSink := &recordingConfigAuditSink{failStage: ConfigAuditSuccess}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !service.committed || response.Header().Get("ETag") != `"`+testManagedConfigValidator+`"` {
		t.Fatalf("response = %d committed=%t ETag=%q body=%s", response.Code, service.committed, response.Header().Get("ETag"), response.Body.String())
	}
	if len(auditSink.events) != 3 || auditSink.events[2].Stage != ConfigAuditSuccess {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
}

func TestPutConfigRecordsRecoveryRequiredAsIndeterminate(t *testing.T) {
	service := &fakeConfigService{afterObserverErr: &application.Error{
		Kind: application.ErrorKindInternal, Code: "config_recovery_required", Message: "recovery required",
	}}
	auditSink := &recordingConfigAuditSink{}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("response = %d body=%s", response.Code, response.Body.String())
	}
	if len(auditSink.events) != 3 || auditSink.events[0].Stage != ConfigAuditAttempt ||
		auditSink.events[1].Stage != ConfigAuditAuthorized || auditSink.events[2].Stage != ConfigAuditIndeterminate ||
		auditSink.events[2].FailureCode != "config_recovery_required" {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
}

func TestPutConfigTerminalAuditOutlivesRequestCancellation(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	service := &fakeConfigService{afterCommit: cancel}
	auditSink := &recordingConfigAuditSink{}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`)).WithContext(requestContext)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if len(auditSink.contextErrors) != 3 || auditSink.contextErrors[2] != nil {
		t.Fatalf("audit context errors = %#v", auditSink.contextErrors)
	}
	if auditSink.events[2].Stage != ConfigAuditSuccess {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
}

func TestPutConfigCancellationUsesSameResponseAndAuditFailureCode(t *testing.T) {
	service := &fakeConfigService{afterObserverErr: &application.Error{
		Kind: application.ErrorKindInvalidArgument, Code: "config_candidate_invalid",
		Message: "candidate validation canceled", Cause: context.Canceled,
	}}
	auditSink := &recordingConfigAuditSink{}
	handler := newConfigRouteHandler(t, StateBootstrap, ListenerDirect, service, auditSink, 0)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/main/config", strings.NewReader(`{"config":{},"sensitive":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"`+testManagedConfigValidator+`"`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestTimeout || len(auditSink.events) != 3 ||
		auditSink.events[2].Stage != ConfigAuditFailure || auditSink.events[2].FailureCode != "request_canceled" {
		t.Fatalf("response=%d body=%s audit=%#v", response.Code, response.Body.String(), auditSink.events)
	}
}
