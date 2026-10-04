package application

import "context"

// ComputeNetworkService answers the console's read-only lease network view
// (INCUS-R-128): for every compute lease of the active deployment, its bridge,
// tiers, publications, instances and the directions its tiers allow.
type ComputeNetworkService interface {
	ListLeaseNetworks(context.Context) (ComputeLeaseNetworkResult, error)
}

type ComputeNetworkServiceFactory func(workspacePath string) ComputeNetworkService

type ComputeLeaseNetworkResult struct {
	ActiveDeployment *string               `json:"active_deployment"`
	Leases           []ComputeLeaseNetwork `json:"leases"`
}

// ComputeLeaseNetwork carries no credential, key, certificate or naming key.
type ComputeLeaseNetwork struct {
	Consumer         string                    `json:"consumer"`
	Resource         string                    `json:"resource"`
	Provider         string                    `json:"provider"`
	Interface        string                    `json:"interface"`
	Sandbox          string                    `json:"sandbox"`
	Status           string                    `json:"status"`
	Bridge           string                    `json:"bridge,omitempty"`
	IPv4Subnet       string                    `json:"ipv4_subnet,omitempty"`
	IPv4Gateway      string                    `json:"ipv4_gateway,omitempty"`
	IPv6Subnet       string                    `json:"ipv6_subnet,omitempty"`
	IPv6Gateway      string                    `json:"ipv6_gateway,omitempty"`
	Egress           string                    `json:"egress"`
	ModuleAccess     bool                      `json:"module_access"`
	IntraLease       bool                      `json:"intra_lease"`
	Ingress          string                    `json:"ingress"`
	HTTPPorts        []int                     `json:"http_ports"`
	HTTPPublications []ComputeHTTPPublication  `json:"http_publications"`
	PortBindings     []ComputePortBinding      `json:"port_bindings"`
	Instances        []ComputeLeaseInstance    `json:"instances"`
	InstancesError   string                    `json:"instances_error,omitempty"`
	Directions       []ComputeNetworkDirection `json:"directions"`
}

type ComputeHTTPPublication struct {
	Host     string `json:"host"`
	Instance string `json:"instance"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
}

type ComputePortBinding struct {
	Protocol  string `json:"protocol"`
	HostPort  int    `json:"host_port"`
	Auto      bool   `json:"auto"`
	Slot      string `json:"slot"`
	Instance  string `json:"instance"`
	IPv4      string `json:"ipv4,omitempty"`
	IPv6      string `json:"ipv6,omitempty"`
	GuestPort int    `json:"guest_port"`
}

type ComputeLeaseInstance struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Addresses []string `json:"addresses"`
	Slot      string   `json:"slot,omitempty"`
}

type ComputeNetworkDirection struct {
	Flow    string `json:"flow"`
	Peer    string `json:"peer"`
	Allowed bool   `json:"allowed"`
}
