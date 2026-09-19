package incusingresshost

const installedScopeSchema = "anas.incus-http-installed-scope/v1"

// InstalledScope is the root-owned on-disk projection used by
// NewLocalInstalledBackend. It intentionally contains no command paths, raw nft
// text, namespace paths, Incus sockets or credentials.
type InstalledScope struct {
	Schema             string            `json:"schema"`
	ScopeID            string            `json:"scope_id"`
	ScopeName          string            `json:"scope_name"`
	ReceiptDir         string            `json:"receipt_dir"`
	RouteNetNS         string            `json:"route_netns"`
	RouteTable         uint32            `json:"route_table"`
	RouteProtocol      uint8             `json:"route_protocol"`
	PermitTable        string            `json:"permit_table"`
	OriginTable        string            `json:"origin_table"`
	GuestBridge        string            `json:"guest_bridge"`
	IngressBridge      string            `json:"ingress_bridge"`
	TraefikVeth        string            `json:"traefik_veth"`
	TraefikInterface   string            `json:"traefik_interface"`
	TraefikSourceIP    string            `json:"traefik_source_ip"`
	IngressGateway     string            `json:"ingress_gateway"`
	GuestSubnet        string            `json:"guest_subnet"`
	Namespace          RouteNamespacePin `json:"namespace"`
	AddressRouting     *AddressRouting   `json:"address_routing,omitempty"`
	PermitTTLSeconds   uint16            `json:"permit_ttl_seconds"`
	CommandTimeoutMS   uint16            `json:"command_timeout_ms"`
	PublicationEnabled bool              `json:"publication_enabled"`
	ProductionGate     ProductionGate    `json:"production_gate"`
}

type ProductionGate struct {
	AllocationContract string `json:"allocation_contract"`
	NamespaceExecutor  string `json:"namespace_executor"`
	NFTBaseline        string `json:"nft_baseline"`
	ProbeIdentity      string `json:"probe_identity"`
}
