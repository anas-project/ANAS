package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
)

func TestHostTypedRoutesRejectAmbiguousJSONBeforeAdmission(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	called := 0
	h, err := New(Options{Registry: registry, Jobs: &JobQueryOptions{Store: openHTTPJobStore(t, consolejobs.Options{})},
		Security: SecurityOptions{InitialState: StateFull, Listener: ListenerDirect, HostAllowed: func(*http.Request) bool { return true },
			Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
				return Principal{ID: "owner", Role: "owner", Source: "local"}, nil
			}},
		HostActions: &HostActionOptions{
			InvokePreflight: func(context.Context, string, string, string) (consolejobs.CreateResult, error) {
				called++
				return consolejobs.CreateResult{}, hostaction.ErrUnavailable
			},
			InvokePlan: func(context.Context, string, string, string, json.RawMessage, string) (consolejobs.CreateResult, error) {
				called++
				return consolejobs.CreateResult{}, hostaction.ErrUnavailable
			},
			IssueConfirmation: func(context.Context, string, string, string, string) (hostconfirmation.IssueResult, error) {
				called++
				return hostconfirmation.IssueResult{}, hostaction.ErrUnavailable
			},
			InvokeConfirmed: func(context.Context, string, string, string, string, json.RawMessage, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error) {
				called++
				return consolejobs.CreateResult{}, hostaction.ErrUnavailable
			},
		}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/workspaces/main/host/actions/"
	for _, tc := range []struct {
		suffix, body string
		status       int
	}{
		{"incus/install/plan", `{"request":{},"request":{"skip":true}}`, 400},
		{"incus/install/plan", `{"Request":{}}`, 400},
		{"incus/install/plan", `{"request":{"Skip":true}}`, 400},
		{"incus/install/plan", `{"request":{"skip":null}}`, 400},
		{"incus/install/plan", `{"request":null}`, 400},
		{"incus/install/plan", `{"request":{"storage_size_gib":15}}`, 400},
		{"incus/install/plan", `{"request":{"storage_size_gib":4097}}`, 400},
		{"incus/install/plan", `{"request":{"interface":"shell"}}`, 400},
		{"incus/uninstall/plan", `{"request":{"skip":true}}`, 400},
		{"incus/arbitrary/plan", `{"request":{}}`, 404},
		{"incus/arbitrary/apply", `{}`, 404},
		{"incus/observer/plan", `{"request":{"Operation":"refresh"}}`, 400},
		{"incus/observer/plan", `{"request":{"operation":null}}`, 400},
		{"incus/observer/plan", `{"request":{"operation":"refresh","operation":"disable"}}`, 400},
		{"incus/observer/plan", `{"request":{"operation":"refresh","workspace_id":"other"}}`, 400},
		{"incus/observer/plan", `{"request":{"operation":"refresh","snapshot":{}}}`, 400},
		{"incus/observer/plan", `{"request":{"operation":"install"}}`, 400},
		{"confirm", `{"plan_job_id":"a","plan_job_id":"b","action":"incus.install"}`, 400},
		{"confirm", `{"plan_job_id":"a","Action":"incus.install"}`, 400},
		{"confirm", `{"plan_job_id":"a","action":null}`, 400},
		{"incus/install/apply", `{"plan_job_id":"a","confirmation_token":"private-marker","parameters":null}`, 400},
		{"incus/install/apply", `{"plan_job_id":"a","confirmation_token":"private-marker","confirmation_token":"b","parameters":{}}`, 400},
		{"incus/install/apply", `{"plan_job_id":"a","confirmation_token":"private-marker","parameters":{"path":"/tmp"}}`, 400},
	} {
		w := hostRouteRequest(h, http.MethodPost, base+tc.suffix, tc.body, "key", true)
		if w.Code != tc.status || called != 0 || strings.Contains(w.Body.String(), "private-marker") {
			t.Fatalf("invalid host request reached admission: route=%s code=%d calls=%d body=%s", tc.suffix, w.Code, called, w.Body.String())
		}
		var problem map[string]any
		if json.Unmarshal(w.Body.Bytes(), &problem) != nil {
			t.Fatal("handler wrote more than one problem document")
		}
	}
	w := hostRouteRequest(h, http.MethodPost, base+"incus/install/plan", `{"request":{"interface":"incus_container","remove_packages":false}}`, "key", true)
	if w.Code != 503 || called != 1 {
		t.Fatalf("valid typed request not admitted: %d %s", w.Code, w.Body.String())
	}
	for i, operation := range []string{"refresh", "disable"} {
		w := hostRouteRequest(h, http.MethodPost, base+"incus/observer/plan", `{"request":{"operation":"`+operation+`"}}`, "key", true)
		if w.Code != 503 || called != i+2 {
			t.Fatalf("observer plan not admitted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestHostJSONMediaTypeFailureIsOneResponse(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json"))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	var request hostActionConfirmHTTPRequest
	if decodeStrictHostActionJSON(w, r, &request) || w.Code != 415 {
		t.Fatal("media type rejection was lost")
	}
	var problem map[string]any
	if json.Unmarshal(w.Body.Bytes(), &problem) != nil {
		t.Fatal("multiple problem documents")
	}
}

func TestOpenAPIHostActionRequestsAndResponsesMatchTypedHandlers(t *testing.T) {
	doc := readOpenAPIDocument(t)
	paths := objectAt(t, doc, "paths")
	schemas := objectAt(t, objectAt(t, doc, "components"), "schemas")
	request := objectAt(t, schemas, "IncusHostRequest")
	if request["additionalProperties"] != false {
		t.Fatal("host request schema allows arbitrary properties")
	}
	for _, phase := range []string{"plan", "apply"} {
		operation := objectAt(t, objectAt(t, paths, "/api/v1/workspaces/{ws}/host/actions/incus/{phase}/"+phase), "post")
		responses := objectAt(t, operation, "responses")
		if objectAt(t, responses, "202")["$ref"] != "#/components/responses/AcceptedHostActionJob" {
			t.Fatal("host admission is not the actual JobDetail response")
		}
		body := objectAt(t, objectAt(t, objectAt(t, objectAt(t, operation, "requestBody"), "content"), "application/json"), "schema")
		properties := objectAt(t, body, "properties")
		key, want := "request", "IncusHostRequest"
		if phase == "apply" {
			key, want = "parameters", "IncusHostApplyParameters"
		}
		variants, ok := objectAt(t, properties, key)["oneOf"].([]any)
		observer := "IncusObserverOperation"
		if phase == "apply" {
			observer = "IncusObserverApplyParameters"
		}
		if !ok || len(variants) != 2 {
			t.Fatal("host API is missing its distinct observer schema")
		}
		for i, name := range []string{want, observer} {
			v, ok := variants[i].(map[string]any)
			if !ok || v["$ref"] != "#/components/schemas/"+name {
				t.Fatal("host API data schema drifted")
			}
		}
	}
}
