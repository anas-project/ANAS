package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
	"github.com/anas-project/ANAS/internal/jobexecutor"
)

func newModuleCommandRouteHandler(t *testing.T, registry *Registry, store *consolejobs.Store, query *fakeQueryService, audit deploymentaudit.Sink) http.Handler {
	t.Helper()
	if audit == nil {
		audit = &recordingDeploymentAuditSink{}
	}
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return query }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local"}, nil
		},
	}, DeploymentOptions{
		PlanFactory: func(string) application.DeploymentPlanService { return &fakeDeploymentPlanService{} },
		ModuleCommandFactory: func(string, application.EventSink) application.ModuleCommandService {
			return nil
		},
		Store: store, Audit: audit, Notify: func(string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func safeModuleCommand(digest string) application.EffectiveModuleCommand {
	return application.EffectiveModuleCommand{
		Module: "demo", Release: "1.2.3-r4", DeploymentID: "dep-1", Available: true,
		Command: application.ModuleCommandDescriptor{
			ID: "reindex", Title: "Reindex", Mode: "sync", Risk: "safe",
			Lock: "workspace", Cancellable: "never", Digest: digest,
			Parameters: []application.ModuleCommandParameter{},
		},
	}
}

const invokePath = "/api/v1/workspaces/main/modules/demo/commands/reindex/actions/invoke"

func TestModuleCommandInvokeCreatesDurableJobBoundToTheFrozenDescriptor(t *testing.T) {
	registry, paths := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	digest := "sha256:" + strings.Repeat("c", 64)
	query := &fakeQueryService{command: safeModuleCommand(digest)}
	handler := newModuleCommandRouteHandler(t, registry, store, query, nil)

	body := `{"command_digest":"` + digest + `","parameters":{"full":true}}`
	response := fullDeploymentRequest(handler, http.MethodPost, invokePath, body, "invoke-key-1")
	if response.Code != http.StatusAccepted {
		t.Fatalf("invoke = %d, %s", response.Code, response.Body.String())
	}
	if location := response.Header().Get("Location"); !strings.HasPrefix(location, "/api/v1/jobs/") {
		t.Fatalf("Location = %q", location)
	}
	if strings.Contains(response.Body.String(), paths[0]) {
		t.Fatalf("invoke response leaks the workspace path: %s", response.Body.String())
	}

	jobs, err := store.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("stored jobs = %d, %v", len(jobs), err)
	}
	job := jobs[0]
	if job.Kind != jobexecutor.KindModuleCommandInvoke || !job.Mutating || job.WorkspaceID != "main" {
		t.Fatalf("job = %+v", job)
	}
	// The executor decodes this map again with DisallowUnknownFields, so the
	// stored shape is part of the contract, not an implementation detail.
	if job.Request["module"] != "demo" || job.Request["command"] != "reindex" || job.Request["command_digest"] != digest {
		t.Fatalf("stored request = %#v", job.Request)
	}
	if _, err := jobexecutorDecode(job.Request); err != nil {
		t.Fatalf("executor cannot decode the stored request: %v", err)
	}

	// A retry with the same key and body returns the original job instead of
	// running the command twice.
	repeat := fullDeploymentRequest(handler, http.MethodPost, invokePath, body, "invoke-key-1")
	if repeat.Code != http.StatusAccepted {
		t.Fatalf("idempotent retry = %d, %s", repeat.Code, repeat.Body.String())
	}
	after, err := store.List(context.Background())
	if err != nil || len(after) != 1 {
		t.Fatalf("idempotent retry created %d jobs, %v", len(after), err)
	}
}

func TestModuleCommandInvokeRejectsStaleAndUnusableDescriptors(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	digest := "sha256:" + strings.Repeat("d", 64)

	t.Run("missing digest", func(t *testing.T) {
		store := openHTTPJobStore(t, consolejobs.Options{})
		handler := newModuleCommandRouteHandler(t, registry, store, &fakeQueryService{command: safeModuleCommand(digest)}, nil)
		response := fullDeploymentRequest(handler, http.MethodPost, invokePath, `{}`, "k")
		assertProblem(t, response, http.StatusPreconditionRequired, "module_command_digest_required")
		assertNoJobs(t, store)
	})

	t.Run("stale digest", func(t *testing.T) {
		store := openHTTPJobStore(t, consolejobs.Options{})
		handler := newModuleCommandRouteHandler(t, registry, store, &fakeQueryService{command: safeModuleCommand(digest)}, nil)
		stale := `{"command_digest":"sha256:` + strings.Repeat("e", 64) + `"}`
		response := fullDeploymentRequest(handler, http.MethodPost, invokePath, stale, "k")
		assertProblem(t, response, http.StatusPreconditionFailed, "module_command_changed")
		assertNoJobs(t, store)
	})

	t.Run("unavailable command", func(t *testing.T) {
		store := openHTTPJobStore(t, consolejobs.Options{})
		command := safeModuleCommand(digest)
		command.Available = false
		command.UnavailableReason = "module is not running"
		handler := newModuleCommandRouteHandler(t, registry, store, &fakeQueryService{command: command}, nil)
		response := fullDeploymentRequest(handler, http.MethodPost, invokePath, `{"command_digest":"`+digest+`"}`, "k")
		assertProblem(t, response, http.StatusConflict, "module_command_unavailable")
		assertNoJobs(t, store)
	})

	t.Run("idempotency key is required", func(t *testing.T) {
		store := openHTTPJobStore(t, consolejobs.Options{})
		handler := newModuleCommandRouteHandler(t, registry, store, &fakeQueryService{command: safeModuleCommand(digest)}, nil)
		response := fullDeploymentRequest(handler, http.MethodPost, invokePath, `{"command_digest":"`+digest+`"}`, "")
		assertProblem(t, response, http.StatusBadRequest, "idempotency_key_required")
		assertNoJobs(t, store)
	})
}

// A destructive command must not be reachable with the ordinary owner session
// alone, and a safe command must not be able to spend a single-use proof it
// does not need.
func TestModuleCommandInvokeGatesDestructiveCommandsOnStepUp(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	digest := "sha256:" + strings.Repeat("f", 64)

	t.Run("destructive without a proof", func(t *testing.T) {
		store := openHTTPJobStore(t, consolejobs.Options{})
		command := safeModuleCommand(digest)
		command.Command.Risk = "destructive"
		handler := newModuleCommandRouteHandler(t, registry, store, &fakeQueryService{command: command}, nil)
		response := fullDeploymentRequest(handler, http.MethodPost, invokePath, `{"command_digest":"`+digest+`"}`, "k")
		assertProblem(t, response, http.StatusPreconditionRequired, "step_up_required")
		assertNoJobs(t, store)
	})

	t.Run("safe command refuses a proof", func(t *testing.T) {
		store := openHTTPJobStore(t, consolejobs.Options{})
		handler := newModuleCommandRouteHandler(t, registry, store, &fakeQueryService{command: safeModuleCommand(digest)}, nil)
		body := `{"command_digest":"` + digest + `","step_up_proof":"sup_` + strings.Repeat("A", 43) + `"}`
		response := fullDeploymentRequest(handler, http.MethodPost, invokePath, body, "k")
		assertProblem(t, response, http.StatusBadRequest, "step_up_request_invalid")
		assertNoJobs(t, store)
	})
}

// The route must not exist at all until an executor can run the job, otherwise
// an accepted invocation would queue work nothing ever drains.
func TestModuleCommandInvokeRouteAbsentWithoutAnExecutorBinding(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	handler, err := NewHandlerWithDeployment(registry, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: StateFull, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: consolejobs.PrincipalLocalOwner, Role: "owner", Source: "local"}, nil
		},
	}, DeploymentOptions{
		PlanFactory: func(string) application.DeploymentPlanService { return &fakeDeploymentPlanService{} },
		Store:       store, Audit: &recordingDeploymentAuditSink{}, Notify: func(string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := fullDeploymentRequest(handler, http.MethodPost, invokePath, `{"command_digest":"x"}`, "k")
	if response.Code != http.StatusNotFound {
		t.Fatalf("invoke without an executor binding = %d, want 404: %s", response.Code, response.Body.String())
	}
}

func assertProblem(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var document problem
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if document.Code != code {
		t.Fatalf("problem code = %q, want %q", document.Code, code)
	}
}

func assertNoJobs(t *testing.T, store *consolejobs.Store) {
	t.Helper()
	jobs, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("a rejected invocation created %d jobs", len(jobs))
	}
}

func jobexecutorDecode(request map[string]any) (application.InvokeModuleCommandRequest, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return application.InvokeModuleCommandRequest{}, err
	}
	var decoded application.InvokeModuleCommandRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	return decoded, decoder.Decode(&decoded)
}
