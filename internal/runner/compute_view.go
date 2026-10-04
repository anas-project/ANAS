package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/computenet"
	"github.com/anas-project/ANAS/internal/deployment"
)

// The console's lease network view (INCUS-R-128, R-129): everything comes
// from what Core froze and recorded, except the instance list, which is read
// live from the daemon with the lease's own certificate -- the same, project-
// scoped access the consumer already has, and read-only use of it. Nothing
// returned is a credential.

type workspaceComputeNetworkService struct {
	workspace string
}

var _ application.ComputeNetworkService = (*workspaceComputeNetworkService)(nil)

func NewWorkspaceComputeNetworkService(workspace string) application.ComputeNetworkService {
	if strings.TrimSpace(workspace) != "" {
		workspace = filepath.Clean(workspace)
	}
	return &workspaceComputeNetworkService{workspace: workspace}
}

// listLeaseInstances is replaced in tests.
var listLeaseInstances = readLeaseInstances

func (s *workspaceComputeNetworkService) ListLeaseNetworks(ctx context.Context) (application.ComputeLeaseNetworkResult, error) {
	result := application.ComputeLeaseNetworkResult{Leases: []application.ComputeLeaseNetwork{}}
	if s == nil || strings.TrimSpace(s.workspace) == "" {
		return result, moduleApplicationError(application.ErrorKindInvalidArgument, "workspace_required", "workspace is required", nil)
	}
	reader := deployment.NewReader(s.workspace)
	active, err := reader.Active(ctx)
	if err != nil {
		return result, moduleApplicationError(application.ErrorKindInternal, "state_unreadable", "active deployment state is unavailable", err)
	}
	if active.ActiveDeployment == "" {
		return result, nil
	}
	id := active.ActiveDeployment
	result.ActiveDeployment = &id
	manifest, _, err := reader.Manifest(ctx, id)
	if err != nil {
		return result, moduleApplicationError(application.ErrorKindInternal, "deployment_unreadable", "active deployment is unavailable", err)
	}
	base := filepath.Join(s.workspace, ".anas")
	for _, resource := range manifest.Resources {
		if resource.Contract != "compute" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		network := computenet.Default()
		if resource.ComputeNetwork != nil {
			network = *resource.ComputeNetwork.Clone()
		}
		lease := application.ComputeLeaseNetwork{
			Consumer: resource.Consumer, Resource: resource.ID, Provider: resource.Provider, Interface: resource.Interface,
			Sandbox: stringSpec(resource.Spec, "sandbox"), Status: "unknown",
			Egress: network.Egress, ModuleAccess: network.ModuleAccess, IntraLease: network.IntraLease, Ingress: network.Ingress,
			HTTPPorts: []int{}, HTTPPublications: []application.ComputeHTTPPublication{}, PortBindings: []application.ComputePortBinding{},
			Instances: []application.ComputeLeaseInstance{}, Directions: []application.ComputeNetworkDirection{},
		}
		for _, port := range network.HTTPPorts {
			lease.HTTPPorts = append(lease.HTTPPorts, int(port))
		}
		for _, d := range network.Directions() {
			lease.Directions = append(lease.Directions, application.ComputeNetworkDirection{Flow: d.Flow, Peer: d.Peer, Allowed: d.Allowed})
		}
		state, stateErr := readComputeResourceState(base, resource.Consumer, resource.ID)
		slots := map[string]computeSlotRecord{}
		if stateErr == nil {
			lease.Status = state.Status
			if state.Revocation != "" {
				lease.Status = state.Status + "/" + state.Revocation
			}
			if n := state.Actual.ComputeNetwork; n != nil {
				lease.Bridge, lease.IPv4Subnet, lease.IPv4Gateway = n.Bridge, n.IPv4Subnet, n.IPv4Gateway
				lease.IPv6Subnet, lease.IPv6Gateway = n.IPv6Subnet, n.IPv6Gateway
				for _, slot := range n.Slots {
					slots[slot.Name] = slot
				}
			}
		}
		for _, b := range network.Ports {
			binding := application.ComputePortBinding{Protocol: b.Protocol, HostPort: int(b.HostPort), Auto: b.Auto, Slot: b.Slot, GuestPort: int(b.GuestPort)}
			if slot, ok := network.SlotByName(b.Slot); ok {
				binding.Instance = slot.Instance
			}
			if record, ok := slots[b.Slot]; ok {
				binding.IPv4, binding.IPv6 = record.IPv4, record.IPv6
			}
			lease.PortBindings = append(lease.PortBindings, binding)
		}
		lease.HTTPPublications = activeHTTPPublications(s.workspace, resource.Consumer, resource.ID)
		instances, err := listLeaseInstances(ctx, base, id, resource)
		if err != nil {
			lease.InstancesError = "instance list unavailable"
		} else {
			for i := range instances {
				for _, slot := range network.Slots {
					if slot.Instance == instances[i].Name {
						instances[i].Slot = slot.Name
					}
				}
			}
			lease.Instances = instances
		}
		result.Leases = append(result.Leases, lease)
	}
	return result, nil
}

// activeHTTPPublications is replaced once the HTTP publication mediator runs
// (M11); until then no lease has an active HTTP publication to show.
var activeHTTPPublications = func(string, string, string) []application.ComputeHTTPPublication {
	return []application.ComputeHTTPPublication{}
}

func readComputeResourceState(base, consumer, id string) (resourceState, error) {
	var state resourceState
	if !resourceIdentifierPattern.MatchString(consumer) || !resourceIdentifierPattern.MatchString(id) {
		return state, fmt.Errorf("invalid resource identity")
	}
	err := readYAML(filepath.Join(base, "state", "resources", consumer+"."+id+".yml"), &state)
	return state, err
}

type leaseInstanceRecord struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	State  *struct {
		Network map[string]struct {
			Addresses []struct {
				Family  string `json:"family"`
				Address string `json:"address"`
				Scope   string `json:"scope"`
			} `json:"addresses"`
		} `json:"network"`
	} `json:"state"`
}

// readLeaseInstances lists the lease project's instances with the lease's own
// certificate against the pinned daemon. Errors never carry the endpoint or
// key material.
func readLeaseInstances(ctx context.Context, base, deploymentID string, resource deployment.Resource) ([]application.ComputeLeaseInstance, error) {
	env, err := parseEnvFile(filepath.Join(base, "deployments", deploymentID, "modules", resource.Provider, ".env"))
	if err != nil {
		return nil, errors.New("provider configuration unavailable")
	}
	endpoint := strings.TrimSpace(env["INCUS_ENDPOINT"])
	serverPEM, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env["INCUS_SERVER_CERT_B64"]))
	if err != nil || endpoint == "" {
		return nil, errors.New("provider connection unavailable")
	}
	store, err := loadSecretStore(base)
	if err != nil {
		return nil, errors.New("secret store unavailable")
	}
	credential := store.values[resource.CredentialSecretKey]
	certPEM, keyPEM, err := splitComputeCredential(credential)
	if err != nil {
		return nil, errors.New("lease credential unavailable")
	}
	client, err := pinnedLeaseClient(serverPEM, []byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, err
	}
	sandbox := stringSpec(resource.Spec, "sandbox")
	if !computeSandboxPattern.MatchString(sandbox) {
		return nil, errors.New("invalid lease project")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/")+"/1.0/instances?project="+url.QueryEscape(sandbox)+"&recursion=2", nil)
	if err != nil {
		return nil, errors.New("invalid provider endpoint")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("instance list request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20+1))
	if err != nil || len(body) > 4<<20 || response.StatusCode != http.StatusOK {
		return nil, errors.New("instance list request failed")
	}
	var envelope struct {
		Type     string                `json:"type"`
		Metadata []leaseInstanceRecord `json:"metadata"`
	}
	if json.NewDecoder(bytes.NewReader(body)).Decode(&envelope) != nil || envelope.Type != "sync" {
		return nil, errors.New("instance list response invalid")
	}
	out := make([]application.ComputeLeaseInstance, 0, len(envelope.Metadata))
	for _, record := range envelope.Metadata {
		instance := application.ComputeLeaseInstance{Name: record.Name, Status: record.Status, Addresses: []string{}}
		if record.State != nil {
			for name, nic := range record.State.Network {
				if name == "lo" {
					continue
				}
				for _, address := range nic.Addresses {
					if address.Scope == "global" && (address.Family == "inet" || address.Family == "inet6") {
						instance.Addresses = append(instance.Addresses, address.Address)
					}
				}
			}
		}
		slices.Sort(instance.Addresses)
		out = append(out, instance)
	}
	slices.SortFunc(out, func(a, b application.ComputeLeaseInstance) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func pinnedLeaseClient(serverPEM, certPEM, keyPEM []byte) (*http.Client, error) {
	block, _ := pem.Decode(serverPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("pinned server certificate unavailable")
	}
	pinned, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("pinned server certificate unavailable")
	}
	keypair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, errors.New("lease credential unavailable")
	}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{keypair}, MinVersion: tls.VersionTLS12,
			// The daemon's certificate is pinned byte for byte instead of
			// chain-verified; there is no fallback to unverified TLS.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 || !bytes.Equal(raw[0], pinned.Raw) {
					return errors.New("daemon certificate does not match the pin")
				}
				return nil
			},
		},
	}
	return &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
