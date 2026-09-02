package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

const deploymentTestValidator = "cfgv-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const deploymentTestDigest = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

type fakeDeploymentPlanService struct {
	result application.PlanResult
	err    error
	calls  int
}

func (service *fakeDeploymentPlanService) Plan(context.Context, application.PlanRequest) (application.PlanResult, error) {
	service.calls++
	return service.result, service.err
}

type recordingDeploymentAuditSink struct {
	events    []deploymentaudit.Event
	failStage deploymentaudit.Stage
}

type fakeDeploymentStepUp struct {
	credential        consoleauth.LocalStepUpCredential
	issueCalls        int
	authenticateCalls int
	proxyIssueCalls   int
	proxyAuthCalls    int
	proxyIdentity     consoleauth.ProxyIdentity
}

func (stepUp *fakeDeploymentStepUp) IssueProxyStepUp(_ context.Context, request consoleauth.ProxyStepUpRequest) (consoleauth.ProxyStepUpCredential, error) {
	stepUp.proxyIssueCalls++
	stepUp.proxyIdentity = request.Identity
	requestLocal := consoleauth.LocalStepUpRequest{
		SessionToken: request.SessionToken, CSRFToken: request.CSRFToken, Origin: request.Origin,
		Action: request.Action, WorkspaceID: request.WorkspaceID, TargetID: request.TargetID, StateDigest: request.StateDigest,
	}
	credential, err := stepUp.IssueLocalStepUp(context.Background(), requestLocal)
	stepUp.issueCalls--
	return credential, err
}

func (stepUp *fakeDeploymentStepUp) AuthenticateProxyStepUp(_ context.Context, request consoleauth.ProxyStepUpAuthenticationRequest) (consoleauth.ProxyStepUpBinding, error) {
	stepUp.proxyAuthCalls++
	if request.Identity != stepUp.proxyIdentity {
		return consoleauth.ProxyStepUpBinding{}, consoleauth.ErrStepUpUnauthorized
	}
	credential := stepUp.credential
	if request.Token != credential.Token || request.Action != credential.Action || request.WorkspaceID != credential.WorkspaceID ||
		request.TargetID != credential.TargetID || request.StateDigest != credential.StateDigest ||
		consolejobs.DigestRequest([]byte(request.SessionToken)) != credential.SessionDigest {
		return consoleauth.ProxyStepUpBinding{}, consoleauth.ErrStepUpUnauthorized
	}
	return consoleauth.ProxyStepUpBinding{
		Digest: credential.Digest, SessionDigest: credential.SessionDigest, Action: credential.Action,
		WorkspaceID: credential.WorkspaceID, TargetID: credential.TargetID, StateDigest: credential.StateDigest,
		CreatedAt: credential.CreatedAt, ExpiresAt: credential.ExpiresAt,
	}, nil
}

func (stepUp *fakeDeploymentStepUp) IssueLocalStepUp(_ context.Context, request consoleauth.LocalStepUpRequest) (consoleauth.LocalStepUpCredential, error) {
	stepUp.issueCalls++
	token := "sup_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	now := time.Now().UTC()
	stepUp.credential = consoleauth.LocalStepUpCredential{
		Token: token, Digest: consolejobs.DigestRequest([]byte(token)), SessionDigest: consolejobs.DigestRequest([]byte(request.SessionToken)),
		Action: request.Action, WorkspaceID: request.WorkspaceID, TargetID: request.TargetID, StateDigest: request.StateDigest,
		CreatedAt: now, ExpiresAt: now.Add(consoleauth.LocalStepUpTTL),
	}
	return stepUp.credential, nil
}

func (stepUp *fakeDeploymentStepUp) AuthenticateLocalStepUp(_ context.Context, request consoleauth.LocalStepUpAuthenticationRequest) (consoleauth.LocalStepUpBinding, error) {
	stepUp.authenticateCalls++
	credential := stepUp.credential
	if request.Token != credential.Token || request.Action != credential.Action || request.WorkspaceID != credential.WorkspaceID ||
		request.TargetID != credential.TargetID || request.StateDigest != credential.StateDigest ||
		consolejobs.DigestRequest([]byte(request.SessionToken)) != credential.SessionDigest {
		return consoleauth.LocalStepUpBinding{}, consoleauth.ErrStepUpUnauthorized
	}
	return consoleauth.LocalStepUpBinding{
		Digest: credential.Digest, SessionDigest: credential.SessionDigest, Action: credential.Action,
		WorkspaceID: credential.WorkspaceID, TargetID: credential.TargetID, StateDigest: credential.StateDigest,
		CreatedAt: credential.CreatedAt, ExpiresAt: credential.ExpiresAt,
	}, nil
}

func (stepUp *fakeDeploymentStepUp) ConsumeLocalStepUp(ctx context.Context, request consoleauth.LocalStepUpAuthenticationRequest) (consoleauth.LocalStepUpBinding, error) {
	binding, err := stepUp.AuthenticateLocalStepUp(ctx, request)
	if err == nil {
		stepUp.credential.Token = ""
	}
	return binding, err
}

func (stepUp *fakeDeploymentStepUp) ConsumeProxyStepUp(ctx context.Context, request consoleauth.ProxyStepUpAuthenticationRequest) (consoleauth.ProxyStepUpBinding, error) {
	binding, err := stepUp.AuthenticateProxyStepUp(ctx, request)
	if err == nil {
		stepUp.credential.Token = ""
	}
	return binding, err
}

func (sink *recordingDeploymentAuditSink) RecordDeploymentEvent(_ context.Context, event deploymentaudit.Event) error {
	sink.events = append(sink.events, event)
	if event.Stage == sink.failStage {
		return errors.New("audit offline")
	}
	return nil
}

func TestBootstrapPlanAndApplyPersistAuditedOneTimeConfirmation(t *testing.T) {
	registry, workspacePaths := testRegistry(t, "main")
	jobDirectory := filepath.Join(t.TempDir(), "jobs")
	store, err := consolejobs.Open(jobDirectory, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	auditSink := &recordingDeploymentAuditSink{}
	notifyCalls := 0
	service := &fakeDeploymentPlanService{result: application.PlanResult{
		Workspace: workspacePaths[0], ConfigValidator: deploymentTestValidator, Digest: deploymentTestDigest,
		Modules: []string{"demo"}, ModulePlans: map[string]map[string]string{"demo": {"effect": "restart"}},
	}}
	handler := newDeploymentRouteHandler(t, registry, store, StateBootstrap, Principal{
		ID: "bootstrap:transaction-a", Role: "bootstrap", Source: "bootstrap", TransactionID: "transaction-a",
	}, service, auditSink, func(string) { notifyCalls++ })

	planResponse := deploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/plans", `{}`, "")
	if planResponse.Code != http.StatusOK {
		t.Fatalf("plan response = %d, %s", planResponse.Code, planResponse.Body.String())
	}
	if strings.Contains(planResponse.Body.String(), workspacePaths[0]) {
		t.Fatalf("plan response exposed workspace path: %s", planResponse.Body.String())
	}
	var planned deploymentPlanResponse
	decodeResponse(t, planResponse, &planned)
	if planned.Job.Status != consolejobs.StatusSucceeded || planned.Plan.ConfigValidator != deploymentTestValidator ||
		planned.Plan.Digest != deploymentTestDigest || len(planned.Confirmation.Token) != len("cnf_")+64 ||
		planned.Confirmation.PlanJobID != planned.Job.ID {
		t.Fatalf("plan response = %#v", planned)
	}

	applyBody, err := json.Marshal(map[string]any{
		"plan_job_id": planned.Job.ID, "confirmation_token": planned.Confirmation.Token,
		"expected_config_validator": deploymentTestValidator, "expected_plan_digest": deploymentTestDigest,
		"allow_risky": false, "snapshot": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	applyResponse := deploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", string(applyBody), "apply-key")
	if applyResponse.Code != http.StatusAccepted {
		t.Fatalf("apply response = %d, %s", applyResponse.Code, applyResponse.Body.String())
	}
	var applied deploymentApplyResponse
	decodeResponse(t, applyResponse, &applied)
	if applied.Existing || applied.Job.Status != consolejobs.StatusQueued || applied.Job.Kind != deploymentaudit.ActionApply || notifyCalls != 1 {
		t.Fatalf("apply response = %#v, notify calls = %d", applied, notifyCalls)
	}
	if location := applyResponse.Header().Get("Location"); location != "/api/v1/jobs/"+applied.Job.ID {
		t.Fatalf("apply Location = %q", location)
	}

	retry := deploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", string(applyBody), "apply-key")
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry response = %d, %s", retry.Code, retry.Body.String())
	}
	var retried deploymentApplyResponse
	decodeResponse(t, retry, &retried)
	if !retried.Existing || retried.Job.ID != applied.Job.ID || notifyCalls != 1 {
		t.Fatalf("retry = %#v, notify calls = %d", retried, notifyCalls)
	}
	secondKey := deploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", string(applyBody), "different-key")
	if secondKey.Code != http.StatusConflict || !strings.Contains(secondKey.Body.String(), `"code":"confirmation_consumed"`) {
		t.Fatalf("second consumption = %d, %s", secondKey.Code, secondKey.Body.String())
	}

	wantStages := []deploymentaudit.Stage{
		deploymentaudit.StageJobCreateAuthorized, deploymentaudit.StageJobStartAuthorized,
		deploymentaudit.StageConfirmationIssueAuthorized, deploymentaudit.StageJobSucceededAuthorized,
		deploymentaudit.StageConfirmationConsumeAndCreateAuthorized,
	}
	if len(auditSink.events) != len(wantStages) {
		t.Fatalf("audit events = %#v", auditSink.events)
	}
	for index, stage := range wantStages {
		if auditSink.events[index].Stage != stage || auditSink.events[index].Actor != "bootstrap:transaction-a" || auditSink.events[index].WorkspaceID != "main" {
			t.Fatalf("audit event %d = %#v", index, auditSink.events[index])
		}
	}
	journal, err := os.ReadFile(filepath.Join(jobDirectory, consolejobs.JournalFilename))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(journal, []byte(planned.Confirmation.Token)) {
		t.Fatal("job journal contains raw confirmation proof")
	}
	// The public response is the only place the raw proof may appear. Inspect
	// the canonical journal path via the store fixture directory below.
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs = %#v", jobs)
	}
	for _, job := range jobs {
		body, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), planned.Confirmation.Token) {
			t.Fatal("durable job state contains raw confirmation proof")
		}
	}
}

func TestDeploymentCreateAuditFailureLeavesNoJob(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	auditSink := &recordingDeploymentAuditSink{failStage: deploymentaudit.StageJobCreateAuthorized}
	handler := newDeploymentRouteHandler(t, registry, store, StateBootstrap, Principal{
		ID: "bootstrap:transaction-a", Role: "bootstrap", Source: "bootstrap", TransactionID: "transaction-a",
	}, &fakeDeploymentPlanService{}, auditSink, func(string) {})

	response := deploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/plans", `{}`, "")
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("plan response = %d, %s", response.Code, response.Body.String())
	}
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("audit failure created jobs: %#v", jobs)
	}
}

func TestDeploymentRoutesStayClosedWithoutDependenciesAndFullApplyStepUp(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	base, err := NewHandlerWithSecurity(registry, nil, SecurityOptions{
		InitialState: StateBootstrap, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := deploymentRequest(base, http.MethodPost, "/api/v1/workspaces/main/plans", `{}`, ""); response.Code != http.StatusNotFound {
		t.Fatalf("unconfigured plan route = %d, %s", response.Code, response.Body.String())
	}

	store := openHTTPJobStore(t, consolejobs.Options{})
	full := newDeploymentRouteHandler(t, registry, store, StateFull, Principal{ID: "local-owner", Role: "owner", Source: "local"},
		&fakeDeploymentPlanService{}, &recordingDeploymentAuditSink{}, func(string) {})
	request := httptest.NewRequest(http.MethodPost, "https://nas.example/api/v1/workspaces/main/actions/apply", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	full.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("full apply without step-up route = %d, %s", response.Code, response.Body.String())
	}
}

func TestFullLocalStepUpBindsPlanAndIsConsumedWithApply(t *testing.T) {
	registry, workspacePaths := testRegistry(t, "main")
	jobDirectory := filepath.Join(t.TempDir(), "jobs")
	store, err := consolejobs.Open(jobDirectory, consolejobs.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	stepUp := &fakeDeploymentStepUp{}
	service := &fakeDeploymentPlanService{result: application.PlanResult{
		Workspace: workspacePaths[0], ConfigValidator: deploymentTestValidator, Digest: deploymentTestDigest,
		Modules: []string{"demo"},
	}}
	handler := newDeploymentRouteHandlerWithStepUp(t, registry, store, StateFull, Principal{
		ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local",
	}, service, &recordingDeploymentAuditSink{}, func(string) {}, stepUp)

	stepUpResponse := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/auth/step-up",
		`{"password":"owner-password","action":"deployment.apply","workspace_id":"main"}`, "")
	if stepUpResponse.Code != http.StatusOK {
		t.Fatalf("step-up response = %d, %s", stepUpResponse.Code, stepUpResponse.Body.String())
	}
	var issued localStepUpResponse
	decodeResponse(t, stepUpResponse, &issued)
	if issued.Proof == "" || stepUp.issueCalls != 1 {
		t.Fatalf("step-up response = %#v, calls = %d", issued, stepUp.issueCalls)
	}

	planResponse := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/plans",
		`{"step_up_proof":"`+issued.Proof+`"}`, "")
	if planResponse.Code != http.StatusOK {
		t.Fatalf("plan response = %d, %s", planResponse.Code, planResponse.Body.String())
	}
	var planned deploymentPlanResponse
	decodeResponse(t, planResponse, &planned)
	if stepUp.authenticateCalls != 1 || planned.Confirmation.PlanJobID != planned.Job.ID {
		t.Fatalf("plan = %#v, step-up authenticate calls = %d", planned, stepUp.authenticateCalls)
	}
	applyBody, err := json.Marshal(map[string]any{
		"plan_job_id": planned.Job.ID, "confirmation_token": planned.Confirmation.Token, "step_up_proof": issued.Proof,
		"expected_config_validator": planned.Plan.ConfigValidator, "expected_plan_digest": planned.Plan.Digest, "allow_risky": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	applyResponse := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", string(applyBody), "full-apply")
	if applyResponse.Code != http.StatusAccepted {
		t.Fatalf("apply response = %d, %s", applyResponse.Code, applyResponse.Body.String())
	}
	second := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", string(applyBody), "full-apply-second")
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), `"code":"step_up_consumed"`) {
		t.Fatalf("second apply = %d, %s", second.Code, second.Body.String())
	}
	journal, err := os.ReadFile(filepath.Join(jobDirectory, consolejobs.JournalFilename))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(journal, []byte(issued.Proof)) {
		t.Fatal("job journal contains raw step-up proof")
	}
}

func TestFullProxyStepUpUsesRecentOIDCIdentityAndSameDeploymentCapabilities(t *testing.T) {
	registry, workspacePaths := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	stepUp := &fakeDeploymentStepUp{}
	service := &fakeDeploymentPlanService{result: application.PlanResult{
		Workspace: workspacePaths[0], ConfigValidator: deploymentTestValidator, Digest: deploymentTestDigest,
		Modules: []string{"demo"},
	}}
	principal := Principal{
		ID: "oidc:subject", Role: "owner", Source: "oidc_proxy", Issuer: "https://iam.example.test", Subject: "subject-123",
		SemanticRole: "platform_admin", DirectoryGroup: "NAS Admins", AuthenticatedAt: time.Now().UTC().Add(-time.Minute),
		IdentityExpiresAt: time.Now().UTC().Add(time.Hour), AssertionDigest: strings.Repeat("a", 64),
	}
	auditSink := &recordingDeploymentAuditSink{}
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerTrustedProxy,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) { return principal, nil },
	}, DeploymentOptions{PlanFactory: func(string) application.DeploymentPlanService { return service }, Store: store, Audit: auditSink, StepUp: stepUp, Notify: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}

	stepUpResponse := fullProxyDeploymentRequest(handler, http.MethodPost, "/api/v1/auth/step-up",
		`{"action":"deployment.apply","workspace_id":"main"}`, "")
	if stepUpResponse.Code != http.StatusOK {
		t.Fatalf("proxy step-up = %d, %s", stepUpResponse.Code, stepUpResponse.Body.String())
	}
	var issued localStepUpResponse
	decodeResponse(t, stepUpResponse, &issued)
	if stepUp.proxyIssueCalls != 1 || stepUp.proxyIdentity.Subject != "subject-123" {
		t.Fatalf("proxy step-up calls=%d identity=%#v", stepUp.proxyIssueCalls, stepUp.proxyIdentity)
	}
	planResponse := fullProxyDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/plans", `{"step_up_proof":"`+issued.Proof+`"}`, "")
	if planResponse.Code != http.StatusOK {
		t.Fatalf("proxy plan = %d, %s", planResponse.Code, planResponse.Body.String())
	}
	var planned deploymentPlanResponse
	decodeResponse(t, planResponse, &planned)
	applyBody, _ := json.Marshal(map[string]any{
		"plan_job_id": planned.Job.ID, "confirmation_token": planned.Confirmation.Token, "step_up_proof": issued.Proof,
		"expected_config_validator": planned.Plan.ConfigValidator, "expected_plan_digest": planned.Plan.Digest, "allow_risky": false,
	})
	apply := fullProxyDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", string(applyBody), "proxy-apply")
	if apply.Code != http.StatusAccepted || stepUp.proxyAuthCalls != 1 {
		t.Fatalf("proxy apply = %d auth calls=%d body=%s", apply.Code, stepUp.proxyAuthCalls, apply.Body.String())
	}
	for _, event := range auditSink.events {
		if event.IdentitySource == "oidc_proxy" && (event.IdentityIssuer != principal.Issuer || event.IdentitySubject != principal.Subject || event.SemanticRole != "platform_admin" || event.DirectoryGroup != "NAS Admins") {
			t.Fatalf("proxy audit identity = %#v", event)
		}
	}
}

func TestFullDeploymentRequiresStepUpBeforePlanOrApply(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	stepUp := &fakeDeploymentStepUp{}
	handler := newDeploymentRouteHandlerWithStepUp(t, registry, store, StateFull, Principal{
		ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local",
	}, &fakeDeploymentPlanService{}, &recordingDeploymentAuditSink{}, func(string) {}, stepUp)
	plan := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/plans", `{}`, "")
	if plan.Code != http.StatusPreconditionRequired || !strings.Contains(plan.Body.String(), `"code":"step_up_required"`) {
		t.Fatalf("plan without step-up = %d, %s", plan.Code, plan.Body.String())
	}
	apply := fullDeploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", `{}`, "apply")
	if apply.Code != http.StatusPreconditionRequired || !strings.Contains(apply.Body.String(), `"code":"step_up_required"`) {
		t.Fatalf("apply without step-up = %d, %s", apply.Code, apply.Body.String())
	}
}

func TestDeploymentApplyRejectsMalformedInputBeforeJobCreation(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	handler := newDeploymentRouteHandler(t, registry, store, StateBootstrap, Principal{
		ID: "bootstrap:transaction-a", Role: "bootstrap", Source: "bootstrap", TransactionID: "transaction-a",
	}, &fakeDeploymentPlanService{}, &recordingDeploymentAuditSink{}, func(string) {})
	validToken := "cnf_" + strings.Repeat("0", 64)
	valid := `{"plan_job_id":"job-a","confirmation_token":"` + validToken +
		`","expected_config_validator":"` + deploymentTestValidator + `","expected_plan_digest":"` + deploymentTestDigest + `","allow_risky":false}`
	tests := []struct {
		name, body, key, code string
		status                int
	}{
		{name: "missing confirmation", body: `{"allow_risky":false}`, key: "key", status: http.StatusPreconditionRequired, code: "confirmation_required"},
		{name: "malformed token", body: strings.Replace(valid, validToken, "cnf_bad", 1), key: "key", status: http.StatusBadRequest, code: "confirmation_invalid"},
		{name: "malformed validator", body: strings.Replace(valid, deploymentTestValidator, "cfgv-bad", 1), key: "key", status: http.StatusBadRequest, code: "confirmation_invalid"},
		{name: "missing risk decision", body: strings.Replace(valid, `,"allow_risky":false`, "", 1), key: "key", status: http.StatusBadRequest, code: "apply_request_invalid"},
		{name: "path deployment id", body: strings.Replace(valid, `,"allow_risky":false`, `,"deployment_id":"../outside","allow_risky":false`, 1), key: "key", status: http.StatusBadRequest, code: "invalid_deployment_id"},
		{name: "long deployment id", body: strings.Replace(valid, `,"allow_risky":false`, `,"deployment_id":"`+strings.Repeat("x", 256)+`","allow_risky":false`, 1), key: "key", status: http.StatusBadRequest, code: "invalid_deployment_id"},
		{name: "conflicting snapshot policy", body: strings.Replace(valid, `,"allow_risky":false`, `,"allow_risky":false,"snapshot":true,"no_snapshot":true`, 1), key: "key", status: http.StatusBadRequest, code: "invalid_snapshot_policy"},
		{name: "duplicate field", body: strings.Replace(valid, `,"allow_risky":false`, `,"allow_risky":false,"allow_risky":true`, 1), key: "key", status: http.StatusBadRequest, code: "invalid_json"},
		{name: "unknown field", body: strings.Replace(valid, `,"allow_risky":false`, `,"allow_risky":false,"command":"shell"`, 1), key: "key", status: http.StatusBadRequest, code: "invalid_json"},
		{name: "missing idempotency", body: valid, status: http.StatusBadRequest, code: "idempotency_key_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := deploymentRequest(handler, http.MethodPost, "/api/v1/workspaces/main/actions/apply", test.body, test.key)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
		})
	}
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("invalid apply requests created jobs: %#v", jobs)
	}
}

func newDeploymentRouteHandler(
	t *testing.T,
	registry *Registry,
	store *consolejobs.Store,
	state ConsoleState,
	principal Principal,
	service application.DeploymentPlanService,
	auditSink deploymentaudit.Sink,
	notify func(string),
) http.Handler {
	return newDeploymentRouteHandlerWithStepUp(t, registry, store, state, principal, service, auditSink, notify, nil)
}

func newDeploymentRouteHandlerWithStepUp(
	t *testing.T,
	registry *Registry,
	store *consolejobs.Store,
	state ConsoleState,
	principal Principal,
	service application.DeploymentPlanService,
	auditSink deploymentaudit.Sink,
	notify func(string),
	stepUp DeploymentStepUpAuthenticator,
) http.Handler {
	t.Helper()
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: state, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) { return principal, nil },
	}, DeploymentOptions{
		PlanFactory: func(string) application.DeploymentPlanService { return service },
		Store:       store, Audit: auditSink, StepUp: stepUp, Notify: notify,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func fullDeploymentRequest(handler http.Handler, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://nas.example"+path, strings.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://nas.example")
	request.Header.Set(csrfHeaderName, "csrf-token")
	request.AddCookie(&http.Cookie{Name: localSessionCookie, Value: "local-session-token"})
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func fullProxyDeploymentRequest(handler http.Handler, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://anas.example.test:9000"+path, strings.NewReader(body))
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://anas.example.test:9000")
	request.Header.Set(csrfHeaderName, "csrf-token")
	request.AddCookie(&http.Cookie{Name: proxySessionCookie, Value: "proxy-session-token"})
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func deploymentRequest(handler http.Handler, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
