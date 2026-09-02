package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
)

type fakeAuditQueryStore struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (store *fakeAuditQueryStore) List(context.Context) ([]audit.Event, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]audit.Event(nil), store.events...), store.err
}

func TestAuditListFiltersUnregisteredWorkspacesBeforeStablePagination(t *testing.T) {
	registry, _ := testRegistry(t, "main", "secondary")
	base := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	store := &fakeAuditQueryStore{events: []audit.Event{
		{Sequence: 1, Timestamp: base, Type: "local_owner.login", Actor: "owner", Outcome: "success"},
		{Sequence: 2, Timestamp: base.Add(time.Minute), Type: "workspace.config.put", Actor: "owner", WorkspaceID: "main", Outcome: "attempt"},
		{Sequence: 3, Timestamp: base.Add(2 * time.Minute), Type: "workspace.config.put", Actor: "owner", WorkspaceID: "main", Outcome: "authorized", Details: map[string]any{"operation_id": "cfg-visible"}},
		{Sequence: 4, Timestamp: base.Add(3 * time.Minute), Type: "workspace.config.put", Actor: "owner", WorkspaceID: "removed", Outcome: "success"},
		{Sequence: 5, Timestamp: base.Add(4 * time.Minute), Type: "console.state.transition", Actor: "owner", WorkspaceID: "secondary", Outcome: "success"},
	}}
	handler := newAuditTestHandler(t, registry, store, StateFull, Principal{ID: "local-owner", Role: "owner", Source: "local"}, ListenerDirect)

	first := serveTLSRequest(handler, "/api/v1/audit-events?limit=2")
	if first.Code != http.StatusOK {
		t.Fatalf("first page = %d, %s", first.Code, first.Body.String())
	}
	var firstPage auditEventListResponse
	decodeResponse(t, first, &firstPage)
	if len(firstPage.Items) != 2 || firstPage.Items[0].Sequence != 5 || firstPage.Items[1].Sequence != 3 || firstPage.NextCursor == nil {
		t.Fatalf("first page = %#v", firstPage)
	}
	if *firstPage.NextCursor == "3" || strings.Contains(*firstPage.NextCursor, "sequence") || strings.Contains(first.Body.String(), `"workspace_id":"removed"`) {
		t.Fatalf("audit page exposed cursor internals or removed workspace: %s", first.Body.String())
	}

	store.mu.Lock()
	store.events = append(store.events, audit.Event{
		Sequence: 6, Timestamp: base.Add(5 * time.Minute), Type: "local_owner.logout", Actor: "owner", Outcome: "success",
	})
	store.mu.Unlock()
	second := serveTLSRequest(handler, "/api/v1/audit-events?limit=2&cursor="+*firstPage.NextCursor)
	if second.Code != http.StatusOK {
		t.Fatalf("second page = %d, %s", second.Code, second.Body.String())
	}
	var secondPage auditEventListResponse
	decodeResponse(t, second, &secondPage)
	if len(secondPage.Items) != 2 || secondPage.Items[0].Sequence != 2 || secondPage.Items[1].Sequence != 1 || secondPage.NextCursor != nil {
		t.Fatalf("stable second page = %#v", secondPage)
	}
}

func TestAuditRouteRequiresFullOwnerAndIdentitySource(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := &fakeAuditQueryStore{events: []audit.Event{{
		Sequence: 1, Timestamp: time.Now().UTC(), Type: "test", Actor: "owner", WorkspaceID: "main",
	}}}
	for _, test := range []struct {
		name      string
		state     ConsoleState
		principal Principal
		want      int
	}{
		{name: "owner", state: StateFull, principal: Principal{ID: "owner", Role: "owner", Source: "local"}, want: http.StatusOK},
		{name: "viewer", state: StateFull, principal: Principal{ID: "viewer", Role: "viewer", Source: "oidc"}, want: http.StatusForbidden},
		{name: "missing source", state: StateFull, principal: Principal{ID: "owner", Role: "owner"}, want: http.StatusForbidden},
		{name: "bootstrap hidden", state: StateBootstrap, principal: Principal{ID: "bootstrap:txn", Role: "bootstrap", Source: "bootstrap", TransactionID: "txn"}, want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newAuditTestHandler(t, registry, store, test.state, test.principal, ListenerDirect)
			response := serveTLSRequest(handler, "/api/v1/audit-events")
			if response.Code != test.want {
				t.Fatalf("response = %d, %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAuditRouteSupportsTrustedProxyAndRejectsPlaintext(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := &fakeAuditQueryStore{}
	principal := Principal{ID: "directory-owner", Role: "owner", Source: "oidc"}
	proxy := newAuditTestHandler(t, registry, store, StateFull, principal, ListenerTrustedProxy)
	if response := serveTLSRequest(proxy, "/api/v1/audit-events"); response.Code != http.StatusOK {
		t.Fatalf("trusted proxy = %d, %s", response.Code, response.Body.String())
	}
	direct := newAuditTestHandler(t, registry, store, StateFull, Principal{ID: "owner", Role: "owner", Source: "local"}, ListenerDirect)
	if response := serveRequest(direct, http.MethodGet, "/api/v1/audit-events"); response.Code != http.StatusNotFound {
		t.Fatalf("plaintext route = %d, %s", response.Code, response.Body.String())
	}
}

func TestAuditListRejectsInvalidPaginationAndMapsStoreErrors(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	store := &fakeAuditQueryStore{}
	handler := newAuditTestHandler(t, registry, store, StateFull, Principal{ID: "owner", Role: "owner", Source: "local"}, ListenerDirect)
	for _, target := range []string{
		"/api/v1/audit-events?limit=0",
		"/api/v1/audit-events?limit=1&limit=2",
		"/api/v1/audit-events?cursor=not-base64",
		"/api/v1/audit-events?unknown=true",
	} {
		if response := serveTLSRequest(handler, target); response.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, %s", target, response.Code, response.Body.String())
		}
	}

	for _, test := range []struct {
		err  error
		want int
		code string
	}{
		{err: audit.ErrUnavailable, want: http.StatusServiceUnavailable, code: "audit_unavailable"},
		{err: context.Canceled, want: http.StatusRequestTimeout, code: "request_canceled"},
		{err: context.DeadlineExceeded, want: http.StatusGatewayTimeout, code: "deadline_exceeded"},
		{err: errors.New("unexpected"), want: http.StatusInternalServerError, code: "audit_unavailable"},
	} {
		store.err = test.err
		response := serveTLSRequest(handler, "/api/v1/audit-events")
		if response.Code != test.want || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
			t.Errorf("error %v = %d, %s", test.err, response.Code, response.Body.String())
		}
	}
}

func newAuditTestHandler(t *testing.T, registry *Registry, store AuditQueryStore, state ConsoleState, principal Principal, listener ListenerIdentity) http.Handler {
	t.Helper()
	handler, err := NewHandlerWithAuditQueries(registry, nil, SecurityOptions{
		InitialState: state,
		HostAllowed:  func(*http.Request) bool { return true },
		Listener:     listener,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return principal, nil
		},
	}, AuditQueryOptions{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
