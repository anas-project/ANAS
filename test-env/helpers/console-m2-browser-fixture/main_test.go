package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleRequiresAndRecordsExactExpandedChain(t *testing.T) {
	fixture := &fixture{}
	preview := httptest.NewRecorder()
	fixture.handler().ServeHTTP(preview, httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/api/v1/workspaces/main/modules/actions/restart",
		strings.NewReader(`{"modules":["db"]}`),
	))
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), `"affected_modules":["db","app"]`) {
		t.Fatalf("preview = %d %s", preview.Code, preview.Body.String())
	}

	confirmation := `{"modules":["db"],"expected_deployment_id":"dep-active","expected_digest":"` + strings.Repeat("a", 64) + `","confirmed_modules":["db","app"]}`
	executeRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/workspaces/main/modules/actions/restart", strings.NewReader(confirmation))
	executeRequest.Header.Set("Idempotency-Key", "fixture-key")
	execute := httptest.NewRecorder()
	fixture.handler().ServeHTTP(execute, executeRequest)
	if execute.Code != http.StatusAccepted {
		t.Fatalf("execute = %d %s", execute.Code, execute.Body.String())
	}

	status := httptest.NewRecorder()
	fixture.handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/__fixture/status", nil))
	var document struct {
		Confirmed bool             `json:"confirmed"`
		Request   lifecycleRequest `json:"request"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if !document.Confirmed || strings.Join(document.Request.ConfirmedModules, ",") != "db,app" {
		t.Fatalf("status = %#v", document)
	}
}

func TestLifecycleRejectsClientInventedChain(t *testing.T) {
	fixture := &fixture{}
	confirmation := `{"modules":["db"],"expected_deployment_id":"dep-active","expected_digest":"` + strings.Repeat("a", 64) + `","confirmed_modules":["app","db"]}`
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/workspaces/main/modules/actions/restart", strings.NewReader(confirmation))
	request.Header.Set("Idempotency-Key", "fixture-key")
	response := httptest.NewRecorder()
	fixture.handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("execute = %d %s", response.Code, response.Body.String())
	}
}
