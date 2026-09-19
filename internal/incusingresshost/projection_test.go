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
	request := ProjectionRequest{Schema: ProjectionSchema, ScopeID: "scope_one", Epoch: strings.Repeat("a", 64), Deployment: "deployment-one", Lease: Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", GuestPort: 7000}
	response := validProjectionResponse(request)
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(_ context.Context, action string, body []byte) ([]byte, error) {
		if action != ProjectionActionID {
			t.Fatalf("unexpected action id: %s", action)
		}
		var decoded ProjectionRequest
		if err := json.Unmarshal(body, &decoded); err != nil || decoded != request {
			t.Fatal("projection request was not serialized as the bounded DTO")
		}
		out, _ := json.Marshal(response)
		return out, nil
	})}
	if _, err := client.ObserveHTTP(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	response.Authorized = append(response.Authorized, response.Authorized[0])
	client.Invoker = projectionInvokerFunc(func(context.Context, string, []byte) ([]byte, error) {
		out, _ := json.Marshal(response)
		return out, nil
	})
	if _, err := client.ObserveHTTP(context.Background(), request); err == nil {
		t.Fatal("duplicate installed authorization accepted")
	}
}

func TestProjectionClientRejectsClaimedUUIDIPWithoutInstalledPortGrant(t *testing.T) {
	request := ProjectionRequest{Schema: ProjectionSchema, ScopeID: "scope_one", Epoch: strings.Repeat("a", 64), Deployment: "deployment-one", Lease: Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", GuestPort: 7001}
	response := validProjectionResponse(request)
	response.Authorized[0].AllowedPorts = []uint16{7000}
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(context.Context, string, []byte) ([]byte, error) {
		out, _ := json.Marshal(response)
		return out, nil
	})}
	if _, err := client.ObserveHTTP(context.Background(), request); err == nil {
		t.Fatal("projection authorized a consumer-claimed port without installed grant")
	}
}

func validProjectionResponse(request ProjectionRequest) ProjectionResponse {
	return ProjectionResponse{
		Schema:     ProjectionSchema,
		ScopeID:    request.ScopeID,
		Epoch:      request.Epoch,
		Deployment: request.Deployment,
		ServerUUID: "22222222-2222-4222-8222-222222222222",
		Authorized: []AuthorizedHTTPLease{{
			Lease: request.Lease, ResourceID: "compute.runners", Project: "anas-runners",
			InstancePrefix: "anas-fj-", AllowedPorts: []uint16{7000, 7001}, Auth: "none",
		}},
		Identity: ProjectionHTTPIdentity{
			Lease: request.Lease, InstanceID: request.InstanceID,
			InstanceUUID: "11111111-1111-4111-8111-111111111111",
			Incarnation:  strings.Repeat("b", 64), State: "Running",
			GuestIP: "10.42.0.2", GuestMAC: "00:16:3e:01:02:03",
			HostVethName: "vethguest0", HostVethMAC: "02:00:00:00:00:10",
			HostVethPeerIfIndex: 77, GuestPort: request.GuestPort,
		},
	}
}
