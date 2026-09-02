package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
)

type fakeJobQueryStore struct {
	mu        sync.Mutex
	jobs      []consolejobs.Job
	events    map[string][]consolejobs.Event
	listErr   error
	getErr    error
	replayErr error
}

func (store *fakeJobQueryStore) List(context.Context) ([]consolejobs.Job, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]consolejobs.Job{}, store.jobs...), store.listErr
}

func (store *fakeJobQueryStore) Get(_ context.Context, id string) (consolejobs.Job, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.getErr != nil {
		return consolejobs.Job{}, store.getErr
	}
	for _, job := range store.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return consolejobs.Job{}, consolejobs.ErrNotFound
}

func (store *fakeJobQueryStore) Replay(_ context.Context, id string, options consolejobs.ReplayOptions) (consolejobs.EventPage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.replayErr != nil {
		return consolejobs.EventPage{}, store.replayErr
	}
	events := store.events[id]
	after := uint64(0)
	if options.AfterID != nil {
		after = *options.AfterID
	}
	page := consolejobs.EventPage{}
	if len(events) != 0 {
		page.LatestID = events[len(events)-1].ID
	}
	for _, event := range events {
		if event.ID > after {
			page.Events = append(page.Events, event)
		}
		if options.Limit > 0 && len(page.Events) == options.Limit {
			break
		}
	}
	return page, nil
}

func TestJobListFiltersByTransactionBeforeStablePagination(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	principalA := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-a")
	principalAEnrollment := mustTransactionPrincipal(t, consolejobs.PrincipalEnrollment, "txn-a")
	principalB := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-b")
	base := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	store := &fakeJobQueryStore{jobs: []consolejobs.Job{
		{ID: "old", Kind: "plan", WorkspaceID: "main", CreatedBy: principalA, CreatedAt: base, Status: consolejobs.StatusSucceeded, Revision: 1},
		{ID: "middle", Kind: "apply", WorkspaceID: "main", CreatedBy: principalAEnrollment, CreatedAt: base.Add(time.Minute), Status: consolejobs.StatusRunning, Revision: 2},
		{ID: "new", Kind: "apply", WorkspaceID: "main", CreatedBy: principalA, CreatedAt: base.Add(2 * time.Minute), Status: consolejobs.StatusQueued, Revision: 1},
		{ID: "other-transaction", WorkspaceID: "main", CreatedBy: principalB, CreatedAt: base.Add(3 * time.Minute), Status: consolejobs.StatusQueued},
		{ID: "prefix-collision", WorkspaceID: "main", CreatedBy: "bootstrap:txn-a:forged", CreatedAt: base.Add(4 * time.Minute), Status: consolejobs.StatusQueued},
		{ID: "unknown-workspace", WorkspaceID: "missing", CreatedBy: principalA, CreatedAt: base.Add(5 * time.Minute), Status: consolejobs.StatusQueued},
	}}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principalA, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-a",
	}, JobQueryOptions{})

	first := serveRequest(handler, http.MethodGet, "/api/v1/jobs?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("first page = %d, %s", first.Code, first.Body.String())
	}
	var firstPage jobListResponse
	decodeResponse(t, first, &firstPage)
	if len(firstPage.Items) != 2 || firstPage.Items[0].ID != "new" || firstPage.Items[1].ID != "middle" || firstPage.NextCursor == nil {
		t.Fatalf("first page = %#v", firstPage)
	}
	if strings.Contains(*firstPage.NextCursor, "middle") {
		t.Fatalf("cursor is not opaque: %q", *firstPage.NextCursor)
	}

	store.mu.Lock()
	store.jobs = append(store.jobs, consolejobs.Job{
		ID: "newer-after-page", WorkspaceID: "main", CreatedBy: principalA,
		CreatedAt: base.Add(10 * time.Minute), Status: consolejobs.StatusQueued,
	})
	store.mu.Unlock()
	second := serveRequest(handler, http.MethodGet, "/api/v1/jobs?limit=2&cursor="+*firstPage.NextCursor)
	if second.Code != http.StatusOK {
		t.Fatalf("second page = %d, %s", second.Code, second.Body.String())
	}
	var secondPage jobListResponse
	decodeResponse(t, second, &secondPage)
	if len(secondPage.Items) != 1 || secondPage.Items[0].ID != "old" || secondPage.NextCursor != nil {
		t.Fatalf("stable second page = %#v", secondPage)
	}
}

func TestJobObjectAuthorizationAndDetailRedaction(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	principalA := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-a")
	principalB := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-b")
	store := &fakeJobQueryStore{jobs: []consolejobs.Job{
		{
			ID: "job-a", Kind: "apply", WorkspaceID: "main", CreatedBy: principalA,
			CreatedAt: time.Now().UTC(), Status: consolejobs.StatusFailed, Request: map[string]any{"password": "raw-secret"},
			Warnings: []string{"safe warning"}, Result: map[string]any{"deployment_id": "dep-1"},
			Error: &consolejobs.JobError{Code: "apply_failed", Message: "safe failure"}, Revision: 4,
		},
		{ID: "job-b", WorkspaceID: "main", CreatedBy: principalB, CreatedAt: time.Now().UTC(), Status: consolejobs.StatusQueued},
		{ID: "job-unknown", WorkspaceID: "unregistered", CreatedBy: principalA, CreatedAt: time.Now().UTC(), Status: consolejobs.StatusQueued},
	}}
	bootstrap := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principalA, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-a",
	}, JobQueryOptions{})

	detail := serveRequest(bootstrap, http.MethodGet, "/api/v1/jobs/job-a")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail = %d, %s", detail.Code, detail.Body.String())
	}
	body := detail.Body.String()
	for _, forbidden := range []string{"raw-secret", `"request"`, `"created_by"`, principalA} {
		if strings.Contains(body, forbidden) {
			t.Errorf("detail leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "dep-1") || !strings.Contains(body, "apply_failed") {
		t.Fatalf("detail omitted safe result/error: %s", body)
	}
	for _, id := range []string{"job-b", "job-unknown"} {
		response := serveRequest(bootstrap, http.MethodGet, "/api/v1/jobs/"+id)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s = %d, %s", id, response.Code, response.Body.String())
		}
	}

	enrollment := newJobTestHandler(t, registry, store, StateEnrollment, Principal{
		ID: "enrollment:txn-a", Role: "enrollment", Source: "enrollment", TransactionID: "txn-a",
	}, JobQueryOptions{})
	if response := serveRequest(enrollment, http.MethodGet, "/api/v1/jobs/job-a"); response.Code != http.StatusOK {
		t.Fatalf("enrollment recovery = %d, %s", response.Code, response.Body.String())
	}

	owner := newJobTestHandler(t, registry, store, StateFull, Principal{ID: "local-owner", Role: "owner", Source: "local"}, JobQueryOptions{})
	if response := serveTLSRequest(owner, "/api/v1/jobs/job-b"); response.Code != http.StatusOK {
		t.Fatalf("owner history = %d, %s", response.Code, response.Body.String())
	}
	if response := serveTLSRequest(owner, "/api/v1/jobs/job-unknown"); response.Code != http.StatusNotFound {
		t.Fatalf("owner unknown workspace = %d, %s", response.Code, response.Body.String())
	}
	nonOwner := newJobTestHandler(t, registry, store, StateFull, Principal{ID: "viewer", Role: "viewer", Source: "oidc"}, JobQueryOptions{})
	if response := serveTLSRequest(nonOwner, "/api/v1/jobs"); response.Code != http.StatusForbidden {
		t.Fatalf("non-owner list = %d, %s", response.Code, response.Body.String())
	}
}

func TestJobEventsReplayReconnectAndIndependentWriteDeadlines(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{EventCapacity: 10})
	principal := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-replay")
	job := createHTTPJob(t, store, principal, "main")
	first, err := store.AppendEvent(context.Background(), job.ID, consolejobs.EventInput{Kind: "progress", Data: map[string]any{"percent": 10}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendEvent(context.Background(), job.ID, consolejobs.EventInput{Kind: "complete", Data: map[string]any{"percent": 100}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	progress := 100
	if _, err := store.Transition(context.Background(), job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{Progress: &progress}); err != nil {
		t.Fatal(err)
	}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principal, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-replay",
	}, JobQueryOptions{SSEWriteTimeout: 100 * time.Millisecond})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/events", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", strconvUint(first.ID))
	recorder := &deadlineResponseRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("replay = %d, %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if strings.Contains(body, `"id":`+strconvUint(first.ID)) || !strings.Contains(body, `"id":`+strconvUint(second.ID)) {
		t.Fatalf("Last-Event-ID replay = %s", body)
	}
	if !strings.Contains(body, "event: job") || !strings.Contains(body, "data: {") {
		t.Fatalf("SSE framing = %s", body)
	}
	if !recorder.sawBoundedAndClearedDeadline() {
		t.Fatalf("write deadlines = %#v", recorder.deadlines)
	}
}

func TestJobEventsReturnsMachineReadablePersistentGap(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{EventCapacity: 1})
	principal := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-gap")
	job := createHTTPJob(t, store, principal, "main")
	if _, err := store.AppendEvent(context.Background(), job.ID, consolejobs.EventInput{Kind: "first"}); err != nil {
		t.Fatal(err)
	}
	latest, err := store.AppendEvent(context.Background(), job.ID, consolejobs.EventInput{Kind: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{}); err != nil {
		t.Fatal(err)
	}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principal, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-gap",
	}, JobQueryOptions{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/events", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusGone || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("gap = %d %s, %s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var document eventGapProblem
	decodeResponse(t, response, &document)
	if document.Code != "event_gap" || document.PrunedThrough == 0 || document.LatestID != latest.ID || document.JobID != job.ID {
		t.Fatalf("gap problem = %#v", document)
	}
}

func TestJobEventsHeartbeatAndGlobalConnectionLimit(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	principal := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-live")
	store := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-live", Kind: "apply", WorkspaceID: "main", CreatedBy: principal,
		CreatedAt: time.Now().UTC(), Status: consolejobs.StatusRunning,
	}}, events: map[string][]consolejobs.Event{}}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principal, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-live",
	}, JobQueryOptions{
		MaxSSEConnections: 1, SSEHeartbeat: 10 * time.Millisecond,
		SSEPollInterval: 50 * time.Millisecond, SSEWriteTimeout: time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstRequest := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-live/events", nil).WithContext(ctx)
	firstRequest.Host = "127.0.0.1"
	firstRequest.Header.Set("Accept", "text/event-stream")
	firstResponse := newStreamingResponseWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(firstResponse, firstRequest)
	}()
	if connected := receiveStreamChunk(t, firstResponse.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}
	if heartbeat := receiveStreamChunk(t, firstResponse.chunks); heartbeat != ": heartbeat\n\n" {
		t.Fatalf("heartbeat = %q", heartbeat)
	}

	secondRequest := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-live/events", nil)
	secondRequest.Host = "127.0.0.1"
	secondRequest.Header.Set("Accept", "text/event-stream")
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusTooManyRequests || secondResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("connection limit = %d, Retry-After %q", secondResponse.Code, secondResponse.Header().Get("Retry-After"))
	}
	var problemDocument problem
	if err := json.NewDecoder(secondResponse.Body).Decode(&problemDocument); err != nil || problemDocument.Code != "sse_connection_limit" {
		t.Fatalf("connection limit problem = %#v, %v", problemDocument, err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream did not stop after request cancellation")
	}
}

func TestJobEventsDrainsFinalEventBeforeClosingTerminalStream(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{EventCapacity: 10})
	principal := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-terminal-drain")
	job := createHTTPJob(t, store, principal, "main")
	if _, err := store.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principal, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-terminal-drain",
	}, JobQueryOptions{
		SSEHeartbeat: time.Second, SSEPollInterval: 10 * time.Millisecond,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/events", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/event-stream")
	response, done := startStreamingRequest(handler, request)
	if connected := receiveStreamChunk(t, response.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}

	final, err := store.AppendEvent(context.Background(), job.ID, consolejobs.EventInput{
		Kind: "complete", Data: map[string]any{"percent": 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	progress := 100
	if _, err := store.Transition(context.Background(), job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{Progress: &progress}); err != nil {
		t.Fatal(err)
	}

	chunk := receiveStreamChunk(t, response.chunks)
	if !strings.Contains(chunk, "id: "+strconvUint(final.ID)+"\n") ||
		!strings.Contains(chunk, `"kind":"complete"`) {
		t.Fatalf("final event = %q", chunk)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream did not close after draining the terminal job")
	}

	reconnect := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/events", nil)
	reconnect.Host = "127.0.0.1"
	reconnect.Header.Set("Accept", "text/event-stream")
	reconnect.Header.Set("Last-Event-ID", strconvUint(final.ID))
	reconnectResponse := httptest.NewRecorder()
	handler.ServeHTTP(reconnectResponse, reconnect)
	if reconnectResponse.Code != http.StatusNoContent || reconnectResponse.Body.Len() != 0 {
		t.Fatalf("caught-up terminal reconnect = %d, %q", reconnectResponse.Code, reconnectResponse.Body.String())
	}
}

func TestJobEventsTerminalWithoutReplayableEventsReturnsNoContentAfterCursorValidation(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := openHTTPJobStore(t, consolejobs.Options{})
	principal := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-terminal-empty")
	job := createHTTPJob(t, store, principal, "main")
	if _, err := store.Start(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(context.Background(), job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{}); err != nil {
		t.Fatal(err)
	}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principal, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-terminal-empty",
	}, JobQueryOptions{})

	ahead := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/events", nil)
	ahead.Host = "127.0.0.1"
	ahead.Header.Set("Accept", "text/event-stream")
	ahead.Header.Set("Last-Event-ID", "1")
	aheadResponse := httptest.NewRecorder()
	handler.ServeHTTP(aheadResponse, ahead)
	if aheadResponse.Code != http.StatusBadRequest || !strings.Contains(aheadResponse.Body.String(), `"code":"invalid_last_event_id"`) {
		t.Fatalf("ahead terminal cursor = %d, %s", aheadResponse.Code, aheadResponse.Body.String())
	}

	empty := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+job.ID+"/events", nil)
	empty.Host = "127.0.0.1"
	empty.Header.Set("Accept", "text/event-stream")
	emptyResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyResponse, empty)
	if emptyResponse.Code != http.StatusNoContent || emptyResponse.Body.Len() != 0 ||
		emptyResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("empty terminal stream = %d, cache %q, body %q", emptyResponse.Code,
			emptyResponse.Header().Get("Cache-Control"), emptyResponse.Body.String())
	}
}

func TestJobEventsCloseSilentlyAfterBootstrapSessionRevocation(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	auth, err := consoleauth.Open(filepath.Join(t.TempDir(), "auth"), consoleauth.AuditSinkFunc(func(context.Context, consoleauth.AuditEvent) error {
		return nil
	}), consoleauth.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := auth.IssueBootstrapToken(context.Background(), consoleauth.IssueBootstrapTokenRequest{
		TransactionID: "txn-stream-revoke", State: consoleauth.StateBootstrap,
		AllowedRoutes: []string{jobEventsRoutePattern},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.ExchangeBootstrapToken(context.Background(), consoleauth.ExchangeBootstrapTokenRequest{
		Token: issued.Token, Origin: "http://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	createdBy := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-stream-revoke")
	jobs := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-revoke", WorkspaceID: "main", CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(), Status: consolejobs.StatusRunning,
	}}, events: map[string][]consolejobs.Event{}}
	var sawObserveOnly atomic.Bool
	direct := DirectSessionAuthorizer(auth)
	handler, err := NewHandlerWithJobQueries(registry, nil, SecurityOptions{
		State:       func(context.Context) (ConsoleState, error) { return StateBootstrap, nil },
		HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(request *http.Request, authorization AuthorizationRequest) (Principal, error) {
			if authorization.ObserveOnly {
				sawObserveOnly.Store(true)
			}
			return direct(request, authorization)
		},
	}, auth, JobQueryOptions{
		Store: jobs, SSEHeartbeat: 50 * time.Millisecond, SSEPollInterval: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://nas.example/api/v1/jobs/job-revoke/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	request.AddCookie(&http.Cookie{Name: consoleauth.BootstrapSessionCookieName, Value: session.Token})
	response, done := startStreamingRequest(handler, request)
	if connected := receiveStreamChunk(t, response.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}
	if err := auth.RevokeBootstrap(context.Background(), "txn-stream-revoke"); err != nil {
		t.Fatal(err)
	}
	assertStreamClosesSilently(t, response, done)
	if !sawObserveOnly.Load() {
		t.Fatal("periodic authorization did not use observe-only mode")
	}
}

func TestJobEventsCloseSilentlyAfterLocalSessionExpires(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	clock := newStreamAuthClock()
	auth, err := consoleauth.Open(filepath.Join(t.TempDir(), "auth"), consoleauth.AuditSinkFunc(func(context.Context, consoleauth.AuditEvent) error {
		return nil
	}), consoleauth.StoreOptions{Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetOwnerPassword(context.Background(), "owner-password"); err != nil {
		t.Fatal(err)
	}
	session, err := auth.LoginLocal(context.Background(), consoleauth.LocalLoginRequest{
		Password: "owner-password", Origin: "https://nas.example",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-expiry", WorkspaceID: "main", CreatedBy: "local-owner",
		CreatedAt: time.Now().UTC(), Status: consolejobs.StatusRunning,
	}}, events: map[string][]consolejobs.Event{}}
	direct := DirectSessionAuthorizer(auth)
	handler, err := NewHandlerWithJobQueries(registry, nil, SecurityOptions{
		State:       func(context.Context) (ConsoleState, error) { return StateFull, nil },
		HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: direct,
	}, auth, JobQueryOptions{
		Store: jobs, SSEHeartbeat: 500 * time.Millisecond, SSEPollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://nas.example/api/v1/jobs/job-expiry/events", nil)
	request.TLS = &tls.ConnectionState{}
	request.Header.Set("Accept", "text/event-stream")
	request.AddCookie(&http.Cookie{Name: consoleauth.LocalSessionCookieName, Value: session.Token})
	response, done := startStreamingRequest(handler, request)
	if connected := receiveStreamChunk(t, response.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}
	clock.Advance(consoleauth.LocalSessionIdleTTL)
	assertStreamClosesSilently(t, response, done)
}

func TestJobEventsCloseSilentlyWhenCapabilityStateChanges(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	createdBy := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-state-change")
	jobs := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-state", WorkspaceID: "main", CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(), Status: consolejobs.StatusRunning,
	}}, events: map[string][]consolejobs.Event{}}
	state := &streamConsoleState{value: StateBootstrap}
	principal := Principal{ID: createdBy, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-state-change"}
	handler, err := NewHandlerWithJobQueries(registry, nil, SecurityOptions{
		State: state.Current, HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) { return principal, nil },
	}, nil, JobQueryOptions{
		Store: jobs, SSEHeartbeat: 100 * time.Millisecond, SSEPollInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://nas.example/api/v1/jobs/job-state/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	response, done := startStreamingRequest(handler, request)
	if connected := receiveStreamChunk(t, response.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}
	state.Set(StateEnrollment)
	assertStreamClosesSilently(t, response, done)
}

func TestJobEventsRechecksObjectAuthorizationAtStreamBoundaries(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	createdBy := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-object")
	jobs := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-object", WorkspaceID: "main", CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(), Status: consolejobs.StatusRunning,
	}}, events: map[string][]consolejobs.Event{}}
	principal := Principal{ID: createdBy, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-object"}
	handler := newJobTestHandler(t, registry, jobs, StateBootstrap, principal, JobQueryOptions{
		SSEHeartbeat: 100 * time.Millisecond, SSEPollInterval: 100 * time.Millisecond,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-object/events", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/event-stream")
	response, done := startStreamingRequest(handler, request)
	if connected := receiveStreamChunk(t, response.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}
	jobs.mu.Lock()
	jobs.jobs[0].CreatedBy = mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-other")
	jobs.mu.Unlock()
	assertStreamClosesSilently(t, response, done)
}

func TestJobEventsReauthorizesBeforeEveryReplayBatch(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	createdBy := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-batches")
	jobs := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-batches", WorkspaceID: "main", CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(), Status: consolejobs.StatusRunning,
	}}, events: map[string][]consolejobs.Event{"job-batches": {
		{ID: 1, JobID: "job-batches", Timestamp: time.Now().UTC(), Kind: "first"},
		{ID: 2, JobID: "job-batches", Timestamp: time.Now().UTC(), Kind: "second"},
	}}}
	principal := Principal{ID: createdBy, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-batches"}
	var observations atomic.Int32
	handler, err := NewHandlerWithJobQueries(registry, nil, SecurityOptions{
		State:       func(context.Context) (ConsoleState, error) { return StateBootstrap, nil },
		HostAllowed: func(*http.Request) bool { return true }, Listener: ListenerDirect,
		Authorize: func(_ *http.Request, authorization AuthorizationRequest) (Principal, error) {
			if authorization.ObserveOnly && observations.Add(1) == 2 {
				return Principal{}, ErrUnauthenticated
			}
			return principal, nil
		},
	}, nil, JobQueryOptions{
		Store: jobs, SSEReplayBatchSize: 1,
		SSEHeartbeat: time.Second, SSEPollInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-batches/events", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "text/event-stream")
	response, done := startStreamingRequest(handler, request)
	if connected := receiveStreamChunk(t, response.chunks); connected != ": connected\n\n" {
		t.Fatalf("connected heartbeat = %q", connected)
	}
	first := receiveStreamChunk(t, response.chunks)
	if !strings.Contains(first, `"id":1`) {
		t.Fatalf("first replay batch = %q", first)
	}
	assertStreamClosesSilently(t, response, done)
	if observations.Load() != 2 {
		t.Fatalf("observe-only authorization calls = %d, want 2", observations.Load())
	}
}

func TestJobRoutesRejectMalformedInputsAndStoreFailures(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	principal := mustTransactionPrincipal(t, consolejobs.PrincipalBootstrap, "txn-errors")
	store := &fakeJobQueryStore{jobs: []consolejobs.Job{{
		ID: "job-errors", WorkspaceID: "main", CreatedBy: principal, CreatedAt: time.Now().UTC(), Status: consolejobs.StatusQueued,
	}}, events: map[string][]consolejobs.Event{}}
	handler := newJobTestHandler(t, registry, store, StateBootstrap, Principal{
		ID: principal, Role: "bootstrap", Source: "bootstrap", TransactionID: "txn-errors",
	}, JobQueryOptions{})
	for target, code := range map[string]string{
		"/api/v1/jobs?limit=0":              "invalid_limit",
		"/api/v1/jobs?cursor=not-base64!":   "invalid_cursor",
		"/api/v1/jobs?workspace_id=main":    "unknown_query_parameter",
		"/api/v1/jobs/job-errors?extra=yes": "unknown_query_parameter",
	} {
		response := serveRequest(handler, http.MethodGet, target)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%s = %d, %s", target, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-errors/events", nil)
	request.Host = "127.0.0.1"
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotAcceptable || !strings.Contains(response.Body.String(), `"code":"event_stream_required"`) {
		t.Fatalf("Accept rejection = %d, %s", response.Code, response.Body.String())
	}

	store.listErr = consolejobs.ErrUnavailable
	response = serveRequest(handler, http.MethodGet, "/api/v1/jobs")
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("store failure = %d, %s", response.Code, response.Body.String())
	}
}

type deadlineResponseRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

type streamingResponseWriter struct {
	header    http.Header
	chunks    chan string
	mu        sync.Mutex
	status    int
	deadlines []time.Time
}

type streamAuthClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStreamAuthClock() *streamAuthClock {
	return &streamAuthClock{now: time.Date(2026, 8, 29, 1, 2, 3, 0, time.UTC)}
}

func (clock *streamAuthClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *streamAuthClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

type streamConsoleState struct {
	mu    sync.Mutex
	value ConsoleState
}

func (state *streamConsoleState) Current(context.Context) (ConsoleState, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.value, nil
}

func (state *streamConsoleState) Set(value ConsoleState) {
	state.mu.Lock()
	state.value = value
	state.mu.Unlock()
}

func newStreamingResponseWriter() *streamingResponseWriter {
	return &streamingResponseWriter{header: make(http.Header), chunks: make(chan string, 16)}
}

func (writer *streamingResponseWriter) Header() http.Header { return writer.header }

func (writer *streamingResponseWriter) WriteHeader(status int) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *streamingResponseWriter) Write(body []byte) (int, error) {
	copy := string(append([]byte(nil), body...))
	writer.chunks <- copy
	return len(body), nil
}

func (*streamingResponseWriter) Flush() {}

func (writer *streamingResponseWriter) SetWriteDeadline(value time.Time) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.deadlines = append(writer.deadlines, value)
	return nil
}

func receiveStreamChunk(t *testing.T, chunks <-chan string) string {
	t.Helper()
	select {
	case chunk := <-chunks:
		return chunk
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE chunk")
		return ""
	}
}

func startStreamingRequest(handler http.Handler, request *http.Request) (*streamingResponseWriter, <-chan struct{}) {
	response := newStreamingResponseWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(response, request)
	}()
	return response, done
}

func assertStreamClosesSilently(t *testing.T, response *streamingResponseWriter, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream did not close after authorization changed")
	}
	for {
		select {
		case chunk := <-response.chunks:
			if !strings.HasPrefix(chunk, ": heartbeat\n\n") {
				t.Fatalf("authorization failure emitted stream data: %q", chunk)
			}
		default:
			return
		}
	}
}

func (recorder *deadlineResponseRecorder) SetWriteDeadline(value time.Time) error {
	recorder.deadlines = append(recorder.deadlines, value)
	return nil
}

func (recorder *deadlineResponseRecorder) sawBoundedAndClearedDeadline() bool {
	hasBounded := false
	hasCleared := false
	for _, deadline := range recorder.deadlines {
		if deadline.IsZero() {
			hasCleared = true
		} else {
			hasBounded = true
		}
	}
	return hasBounded && hasCleared
}

func newJobTestHandler(t *testing.T, registry *Registry, store JobQueryStore, state ConsoleState, principal Principal, overrides JobQueryOptions) http.Handler {
	t.Helper()
	overrides.Store = store
	handler, err := NewHandlerWithJobQueries(registry, nil, SecurityOptions{
		InitialState: state,
		HostAllowed:  func(*http.Request) bool { return true },
		Listener:     ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return principal, nil
		},
	}, nil, overrides)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func serveTLSRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "https://127.0.0.1"+target, nil)
	request.Host = "127.0.0.1"
	request.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func mustTransactionPrincipal(t *testing.T, kind consolejobs.PrincipalKind, transactionID string) string {
	t.Helper()
	principal, err := consolejobs.TransactionPrincipal(kind, transactionID)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func openHTTPJobStore(t *testing.T, options consolejobs.Options) *consolejobs.Store {
	t.Helper()
	store, err := consolejobs.Open(filepath.Join(t.TempDir(), "jobs"), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close job store: %v", err)
		}
	})
	return store
}

func createHTTPJob(t *testing.T, store *consolejobs.Store, principal, workspaceID string) consolejobs.Job {
	t.Helper()
	result, err := store.CreateOrGet(context.Background(), consolejobs.CreateSpec{
		Kind: "apply", WorkspaceID: workspaceID, Mutating: true,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: principal, Method: http.MethodPost, CanonicalPath: "/api/v1/workspaces/" + workspaceID + "/actions/apply",
			Key: "test-key-" + principal, RequestDigest: consolejobs.DigestRequest([]byte("{}")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Job
}

func strconvUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func TestDeploymentPlanJobDetailHidesPrivateConfirmationBinding(t *testing.T) {
	job := consolejobs.Job{
		Kind: deploymentaudit.ActionPlan,
		Result: map[string]any{
			"plan":         map[string]any{"digest": deploymentTestDigest},
			"confirmation": map[string]any{"proof_digest": strings.Repeat("a", 64)},
		},
	}
	detail := newJobDetailDTO(job)
	if _, exposed := detail.Result["confirmation"]; exposed {
		t.Fatalf("public plan job detail exposed confirmation binding: %#v", detail.Result)
	}
	if _, retained := detail.Result["plan"]; !retained {
		t.Fatalf("public plan result was removed: %#v", detail.Result)
	}
	if _, retained := job.Result["confirmation"]; !retained {
		t.Fatal("DTO projection mutated durable job result")
	}
}
