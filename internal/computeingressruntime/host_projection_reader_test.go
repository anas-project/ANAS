package computeingressruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

type projectedInvokerFunc func(context.Context, string, []byte) ([]byte, error)

func (f projectedInvokerFunc) InvokeHostAction(ctx context.Context, action string, body []byte) ([]byte, error) {
	return f(ctx, action, body)
}

func TestHostProjectionReaderUsesFreshNarrowActionWithoutCredentials(t *testing.T) {
	f := newIncusReaderFixture(t, computeclient.InterfaceContainer)
	snapshot := &deployment.HTTPAuthorizationSnapshot{Epoch: strings.Repeat("a", 64), Deployment: f.grant.Deployment, Authorizations: []*computeingress.Authorization{f.grant}}
	serverUUID := "11111111-1111-4111-8111-111111111111"
	calls := 0
	mode := ""
	client := incusingresshost.ProjectionClient{Invoker: projectedInvokerFunc(func(ctx context.Context, action string, body []byte) ([]byte, error) {
		calls++
		if action != incusingresshost.ProjectionActionID || strings.Contains(string(body), "endpoint") || strings.Contains(string(body), "guest_ip") {
			t.Fatal("wide host request")
		}
		var req incusingresshost.ProjectionRequest
		if json.Unmarshal(body, &req) != nil {
			t.Fatal("request decode")
		}
		observed, err := f.reader.ObserveHostHTTP(ctx, f.grant, f.request)
		if err != nil {
			return nil, err
		}
		v := observed.Facts
		out := incusingresshost.ProjectionResponse{Schema: req.Schema, ObservationID: req.ObservationID, ScopeID: req.ScopeID, Epoch: req.Epoch, Deployment: req.Deployment, ServerUUID: serverUUID,
			Authorized: []incusingresshost.AuthorizedHTTPLease{{Lease: req.Lease, ResourceID: "compute." + f.grant.Resource, Project: f.grant.Project, Interface: f.grant.Interface, InstancePrefix: f.grant.InstancePrefix, AllowedPorts: f.grant.Policy.AllowedPorts, Auth: f.grant.Policy.Auth}},
			Identity:   incusingresshost.ProjectionHTTPIdentity{Lease: req.Lease, InstanceID: req.InstanceID, WorkloadID: req.WorkloadID, InstanceUUID: v.InstanceUUID, Incarnation: v.Incarnation, State: v.State, GuestIP: v.GuestIP, GuestMAC: v.GuestMAC, HostVethName: "vethguest0", HostVethMAC: "02:00:00:00:00:10", HostVethPeerIfIndex: 77, GuestPort: req.GuestPort}}
		switch mode {
		case "server":
			out.ServerUUID = "33333333-3333-4333-8333-333333333333"
		case "auth":
			out.Authorized[0].Auth = "forward_auth"
		case "workload":
			out.Identity.WorkloadID = "other-job"
		case "project":
			out.Authorized[0].Project = "other-project"
		}
		return json.Marshal(out)
	})}
	r, err := NewHostProjectionReader("main", serverUUID, snapshot, client)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := r.ObserveHTTP(context.Background(), f.grant, f.request)
	if err != nil || facts.InstanceID != f.request.InstanceID {
		t.Fatal(facts, err)
	}
	for _, scenario := range []string{"server", "auth", "workload", "project"} {
		mode = scenario
		if _, err := r.ObserveHTTP(context.Background(), f.grant, f.request); err == nil {
			t.Fatal("invalid host projection accepted", scenario)
		}
	}
	before := calls
	altered := f.grant.Clone()
	altered.Policy.AllowedPorts = []uint16{7000, 9000}
	if _, err := r.ObserveHTTP(context.Background(), altered, f.request); err == nil || calls != before {
		t.Fatal("changed local authority reached privileged action")
	}
	mode = ""
	snapshot.Epoch = strings.Repeat("f", 64)
	if _, err := r.ObserveHTTP(context.Background(), f.grant, f.request); err != nil {
		t.Fatal("caller mutated installed snapshot", err)
	}
}
