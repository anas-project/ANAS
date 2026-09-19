package computeingressruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeingress"
)

type fakeIncusHostActionClient struct {
	action string
	body   []byte
}

func (f *fakeIncusHostActionClient) InvokeIncusIngressHostAction(_ context.Context, action string, body []byte) error {
	f.action = action
	f.body = append([]byte(nil), body...)
	return nil
}

func TestIncusHostBackendSerializesBoundedTypedRequests(t *testing.T) {
	client := &fakeIncusHostActionClient{}
	backend := IncusHostBackend{ScopeID: "scope_one", Client: client}
	target := PublicationTarget{Epoch: strings.Repeat("a", 64), Incarnation: strings.Repeat("b", 64), NICMAC: "00:16:3e:01:02:03", Publication: computeingress.Publication{
		Reservation: strings.Repeat("c", 32) + ":1", Deployment: "deployment-one", Lease: computeingress.Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", InstanceUUID: "11111111-1111-4111-8111-111111111111", WorkloadID: "job:123", GuestPort: 7000, Host: "ci-job1.example.test", GuestIP: "10.42.0.2", Auth: "none",
	}}
	if err := backend.HoldAddress(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if client.action != IncusIngressActionHoldAddress {
		t.Fatalf("unexpected action: %s", client.action)
	}
	var request IncusIngressHostActionRequest
	if err := json.Unmarshal(client.body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Schema != IncusIngressActionSchema || request.ScopeID != "scope_one" || len(request.Targets) != 1 || request.Targets[0] != target {
		t.Fatal("host action request did not preserve the typed target boundary")
	}
}

func TestIncusHostBackendRejectsMissingClientAndInvalidTarget(t *testing.T) {
	backend := IncusHostBackend{ScopeID: "scope_one"}
	if err := backend.CheckHTTPArtifacts(context.Background(), nil); err == nil {
		t.Fatal("missing trusted host action client accepted")
	}
	client := &fakeIncusHostActionClient{}
	backend.Client = client
	bad := PublicationTarget{Epoch: strings.Repeat("a", 64)}
	if err := backend.HoldAddress(context.Background(), bad); err == nil {
		t.Fatal("invalid target serialized")
	}
}
