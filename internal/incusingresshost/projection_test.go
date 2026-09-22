package incusingresshost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type projectionInvokerFunc func(context.Context, string, []byte) ([]byte, error)

func (f projectionInvokerFunc) InvokeHostAction(ctx context.Context, action string, body []byte) ([]byte, error) {
	return f(ctx, action, body)
}

func TestProjectionClientValidatesInstalledAuthorizationAndIdentity(t *testing.T) {
	request := ProjectionRequest{Schema: ProjectionSchema, ScopeID: "scope_one", Epoch: strings.Repeat("a", 64), Deployment: "deployment-one", Lease: Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
	var duplicate bool
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(_ context.Context, action string, body []byte) ([]byte, error) {
		if action != ProjectionActionID {
			t.Fatalf("unexpected action id: %s", action)
		}
		var decoded ProjectionRequest
		if err := json.Unmarshal(body, &decoded); err != nil || !hex64(decoded.ObservationID) {
			t.Fatal("projection request was not serialized as the bounded DTO")
		}
		comparison := decoded
		comparison.ObservationID = ""
		if comparison != request {
			t.Fatal("projection changed the requested scope")
		}
		response := validProjectionResponse(decoded)
		if duplicate {
			response.Authorized = append(response.Authorized, response.Authorized[0])
		}
		out, _ := json.Marshal(response)
		return out, nil
	})}
	if _, err := client.ObserveHTTP(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	duplicate = true
	if _, err := client.ObserveHTTP(context.Background(), request); err == nil {
		t.Fatal("duplicate installed authorization accepted")
	}
}

func TestProjectionClientRejectsClaimedUUIDIPWithoutInstalledPortGrant(t *testing.T) {
	request := ProjectionRequest{Schema: ProjectionSchema, ScopeID: "scope_one", Epoch: strings.Repeat("a", 64), Deployment: "deployment-one", Lease: Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7001}
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(_ context.Context, _ string, body []byte) ([]byte, error) {
		var decoded ProjectionRequest
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		response := validProjectionResponse(decoded)
		response.Authorized[0].AllowedPorts = []uint16{7000}
		out, _ := json.Marshal(response)
		return out, nil
	})}
	if _, err := client.ObserveHTTP(context.Background(), request); err == nil {
		t.Fatal("projection authorized a consumer-claimed port without installed grant")
	}
}

func validProjectionResponse(request ProjectionRequest) ProjectionResponse {
	return ProjectionResponse{
		Schema:        ProjectionSchema,
		ObservationID: request.ObservationID,
		ScopeID:       request.ScopeID,
		Epoch:         request.Epoch,
		Deployment:    request.Deployment,
		ServerUUID:    "22222222-2222-4222-8222-222222222222",
		Authorized: []AuthorizedHTTPLease{{
			Lease: request.Lease, ResourceID: "compute.runners", Project: "anas-runners", Interface: "incus_container",
			InstancePrefix: "anas-fj-", AllowedPorts: []uint16{7000, 7001}, Auth: "none",
		}},
		Identity: ProjectionHTTPIdentity{
			Lease: request.Lease, InstanceID: request.InstanceID, WorkloadID: request.WorkloadID,
			InstanceUUID: "11111111-1111-4111-8111-111111111111",
			Incarnation:  strings.Repeat("b", 64), State: "Running",
			GuestIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03",
			HostVethName: "vethguest0", HostVethMAC: "02:00:00:00:00:10",
			HostVethPeerIfIndex: 77, GuestPort: request.GuestPort,
		},
	}
}
