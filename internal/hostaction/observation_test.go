package hostaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

type observationBackendFunc func(context.Context, incusingresshost.ProjectionRequest) (incusingresshost.ProjectionResponse, error)

func (f observationBackendFunc) Observe(c context.Context, r incusingresshost.ProjectionRequest) (incusingresshost.ProjectionResponse, error) {
	return f(c, r)
}

func hostObservationRequest() incusingresshost.ProjectionRequest {
	return incusingresshost.ProjectionRequest{Schema: incusingresshost.ProjectionSchema, ObservationID: strings.Repeat("a", 64), ScopeID: "main", Epoch: strings.Repeat("b", 64),
		Deployment: "deployment-one", Lease: incusingresshost.Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
}
func hostObservationResponse(r incusingresshost.ProjectionRequest) incusingresshost.ProjectionResponse {
	return incusingresshost.ProjectionResponse{Schema: r.Schema, ObservationID: r.ObservationID, ScopeID: r.ScopeID, Epoch: r.Epoch, Deployment: r.Deployment,
		ServerUUID: "11111111-1111-4111-8111-111111111111", Authorized: []incusingresshost.AuthorizedHTTPLease{{Lease: r.Lease, ResourceID: "compute.runners", Project: "anas-runners", Interface: "incus_container", InstancePrefix: "anas-fj-", AllowedPorts: []uint16{7000}, Auth: "none"}},
		Identity: incusingresshost.ProjectionHTTPIdentity{Lease: r.Lease, InstanceID: r.InstanceID, WorkloadID: r.WorkloadID, InstanceUUID: "22222222-2222-4222-8222-222222222222", Incarnation: strings.Repeat("c", 64), State: "Running",
			GuestIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03", HostVethName: "vethguest0", HostVethMAC: "02:00:00:00:00:10", HostVethPeerIfIndex: 77, GuestPort: r.GuestPort}}
}

func TestObservationActionStrictRequestAndWorkspaceBoundary(t *testing.T) {
	r := hostObservationRequest()
	body, _ := json.Marshal(r)
	spec, ok := LookupAction(ActionObserveHTTP)
	if !ok || !spec.ReadOnly || !spec.RequiresRoot || spec.Mutating || spec.RequiresConfirm || spec.Policy != consolejobs.ActionReject || spec.Timeout != 30 {
		t.Fatal("observation registry contract", spec)
	}
	canonical, err := CanonicalParameters(ActionObserveHTTP, body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(canonical, &fields)
	reordered, _ := json.Marshal(fields)
	if _, err := CanonicalParameters(ActionObserveHTTP, reordered); err != nil {
		t.Fatal("stored map order rejected", err)
	}
	if !ObservationScopeMatchesWorkspace(ActionObserveHTTP, body, "main") || ObservationScopeMatchesWorkspace(ActionObserveHTTP, body, "other") {
		t.Fatal("workspace authority not bound")
	}
	for _, field := range []string{"path", "endpoint", "ip", "argv", "certificate", "uid"} {
		t.Run(field, func(t *testing.T) {
			bad := append([]byte(`{"`+field+`":"private-marker",`), body[1:]...)
			_, err := CanonicalParameters(ActionObserveHTTP, bad)
			if err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatal("unknown input accepted or leaked", err)
			}
		})
	}
	for _, bad := range []string{strings.Replace(string(body), `"workload_id":"job:123"`, `"workload_id":null`, 1), strings.Replace(string(body), `"scope_id":`, `"Scope_ID":`, 1), strings.Replace(string(body), `"schema":`, `"schema":"old","schema":`, 1)} {
		if _, err := CanonicalParameters(ActionObserveHTTP, []byte(bad)); err == nil {
			t.Fatal("ambiguous input accepted")
		}
	}
}

func TestObservationActionAuditAndProvisionalResultBoundary(t *testing.T) {
	old := newIngressObservationBackend
	defer func() { newIngressObservationBackend = old }()
	for _, scenario := range []string{"success", "start-audit", "finish-audit", "replayed-response", "backend-error"} {
		t.Run(scenario, func(t *testing.T) {
			journal := &memoryAudit{}
			if scenario == "start-audit" {
				journal.failAt = 1
			}
			if scenario == "finish-audit" {
				journal.failAt = 2
			}
			calls := 0
			newIngressObservationBackend = func() ingressObservationBackend {
				return observationBackendFunc(func(ctx context.Context, r incusingresshost.ProjectionRequest) (incusingresshost.ProjectionResponse, error) {
					calls++
					if len(journal.events) != 1 {
						t.Fatal("observation preceded audit")
					}
					if _, ok := ctx.Deadline(); !ok {
						t.Fatal("no deadline")
					}
					out := hostObservationResponse(r)
					if scenario == "replayed-response" {
						out.ObservationID = strings.Repeat("f", 64)
					}
					if scenario == "backend-error" {
						return out, errors.New("private-marker")
					}
					return out, nil
				})
			}
			request := testRequest()
			request.Action = ActionObserveHTTP
			request.Parameters, _ = json.Marshal(hostObservationRequest())
			call, err := prepare(request, testPeer())
			if err != nil {
				t.Fatal(err)
			}
			event, err := executeIncusProvision(context.Background(), call, journal, installedRelease())
			if scenario == "success" {
				if err != nil || event.Result == nil || *event.Result.Changed {
					t.Fatal(event, err)
				}
				if _, err := ProjectActionEvent(ActionObserveHTTP, event); err != nil {
					t.Fatal(err)
				}
			} else if event.Result != nil {
				t.Fatal("failed action exposed provisional identity")
			}
			if scenario == "start-audit" && calls != 0 {
				t.Fatal("audit failure did not prevent observation")
			}
			if scenario == "finish-audit" && (event.Error == nil || event.Error.Outcome != actionabi.Unknown) {
				t.Fatal("audit uncertainty lost")
			}
			wire, _ := json.Marshal(event)
			logged, _ := json.Marshal(journal.events)
			if strings.Contains(string(wire)+string(logged), "private-marker") {
				t.Fatal("private backend error leaked")
			}
			if scenario == "success" {
				event.Result.Value = append([]byte(`{"private_key":"private-marker",`), event.Result.Value[1:]...)
				if _, err := ProjectActionEvent(ActionObserveHTTP, event); err == nil {
					t.Fatal("unselected output accepted")
				}
			}
		})
	}
}
