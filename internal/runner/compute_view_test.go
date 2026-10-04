package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/computenet"
	"github.com/anas-project/ANAS/internal/deployment"
)

// INCUS-R-128: the view joins the frozen network, the Provider's recorded
// bridge and the live instances, and carries no credential.
func TestLeaseNetworkViewJoinsFrozenRecordedAndLiveState(t *testing.T) {
	workspace := t.TempDir()
	base := filepath.Join(workspace, ".anas")
	id := "20261003T000000Z-00000001"
	network := &computenet.Network{
		Egress: computenet.EgressInternet, ModuleAccess: true, Ingress: computenet.IngressPublished,
		Slots:       []computenet.Slot{{Name: "dev", Instance: "anas-fj-dev"}},
		HTTPPorts:   []uint16{7000},
		Ports:       []computenet.PortBinding{{Protocol: "tcp", HostPort: 30022, Auto: true, Slot: "dev", GuestPort: 22}},
		TraefikPort: 9000,
	}
	manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, Resources: []deploymentResource{
		{Consumer: "forgejo", ID: "runners", Contract: "compute", Provider: "incus", Interface: "incus_container",
			Spec: map[string]any{"sandbox": "anas-forgejo-runners"}, ComputeNetwork: network,
			CredentialSecretKey: "ANAS_RESOURCE_SECRET", LeaseSecretKey: "ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET"},
		{Consumer: "forgejo", ID: "db", Contract: "relational_database", Provider: "postgres", Interface: "postgres"},
	}}
	root := filepath.Join(base, "deployments", id)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: id}); err != nil {
		t.Fatal(err)
	}
	state := resourceState{APIVersion: resourceStateAPIVersion, Consumer: "forgejo", ResourceID: "runners", Contract: "compute", Status: "ready",
		Actual: resourceActual{ComputeNetwork: &computeNetworkState{Bridge: "lease120067207a", IPv4Subnet: "10.101.0.0/24", IPv4Gateway: "10.101.0.1",
			Slots: []computeSlotRecord{{Name: "dev", Instance: "anas-fj-dev", IPv4: "10.101.0.254"}}}}}
	if err := os.MkdirAll(filepath.Join(base, "state", "resources"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeYAMLAtomic(filepath.Join(base, "state", "resources", "forgejo.runners.yml"), state, 0600); err != nil {
		t.Fatal(err)
	}
	previous := listLeaseInstances
	t.Cleanup(func() { listLeaseInstances = previous })
	listLeaseInstances = func(_ context.Context, _, deploymentID string, resource deployment.Resource) ([]application.ComputeLeaseInstance, error) {
		if deploymentID != id || resource.Consumer != "forgejo" {
			t.Fatalf("listed %s/%s", deploymentID, resource.Consumer)
		}
		return []application.ComputeLeaseInstance{{Name: "anas-fj-dev", Status: "Running", Addresses: []string{"10.101.0.254"}}, {Name: "anas-fj-job1", Status: "Running", Addresses: []string{"10.101.0.17"}}}, nil
	}
	result, err := NewWorkspaceComputeNetworkService(workspace).ListLeaseNetworks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ActiveDeployment == nil || *result.ActiveDeployment != id || len(result.Leases) != 1 {
		t.Fatalf("result = %+v", result)
	}
	lease := result.Leases[0]
	if lease.Bridge != "lease120067207a" || lease.IPv4Gateway != "10.101.0.1" || lease.Egress != "internet" || !lease.ModuleAccess || lease.Ingress != "published" || lease.Status != "ready" {
		t.Fatalf("lease = %+v", lease)
	}
	if len(lease.PortBindings) != 1 || lease.PortBindings[0].IPv4 != "10.101.0.254" || lease.PortBindings[0].Instance != "anas-fj-dev" || lease.PortBindings[0].HostPort != 30022 {
		t.Fatalf("bindings = %+v", lease.PortBindings)
	}
	if lease.Instances[0].Slot != "dev" || lease.Instances[1].Slot != "" {
		t.Fatalf("instances = %+v", lease.Instances)
	}
	modules := false
	for _, d := range lease.Directions {
		if d.Flow == "egress" && d.Peer == computenet.PeerModules {
			modules = d.Allowed
		}
	}
	if !modules {
		t.Fatalf("module_access lease does not show the Modules direction: %+v", lease.Directions)
	}
	body, _ := json.Marshal(result)
	for _, secret := range []string{"ANAS_RESOURCE_SECRET", "LEASE_SECRET", "PRIVATE KEY", "lease_secret"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("view leaks %s: %s", secret, body)
		}
	}

	// A daemon that cannot be reached is a per-lease error, not a failed view.
	listLeaseInstances = func(context.Context, string, string, deployment.Resource) ([]application.ComputeLeaseInstance, error) {
		return nil, errors.New("connection refused to https://10.77.0.1:8443")
	}
	result, err = NewWorkspaceComputeNetworkService(workspace).ListLeaseNetworks(context.Background())
	if err != nil || result.Leases[0].InstancesError == "" || strings.Contains(result.Leases[0].InstancesError, "10.77") {
		t.Fatalf("unreachable daemon = %+v, %v", result.Leases[0], err)
	}
}

func TestLeaseNetworkViewWithoutDeploymentIsEmpty(t *testing.T) {
	result, err := NewWorkspaceComputeNetworkService(t.TempDir()).ListLeaseNetworks(context.Background())
	if err != nil || result.ActiveDeployment != nil || result.Leases == nil || len(result.Leases) != 0 {
		t.Fatalf("empty workspace = %+v, %v", result, err)
	}
}
