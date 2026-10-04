package computenet

// Direction is one row of the lease network view: whether traffic between a
// lease instance and one kind of peer is allowed. It is derived from the frozen
// declaration alone, so the console can show it without asking the daemon.
type Direction struct {
	// Flow is egress (an instance opens the connection) or ingress (the peer
	// opens it). Replies to either are always allowed.
	Flow    string `json:"flow"`
	Peer    string `json:"peer"`
	Allowed bool   `json:"allowed"`
}

// Peers named in Directions. modules means ANAS Modules reached through
// Traefik; docker_published and docker_unpublished are container ports with
// and without a host port mapping.
const (
	PeerInternet          = "internet"
	PeerLAN               = "lan"
	PeerHost              = "host"
	PeerDockerPublished   = "docker_published"
	PeerDockerUnpublished = "docker_unpublished"
	PeerModules           = "modules"
	PeerOtherLeases       = "other_leases"
	PeerSameLease         = "same_lease"
	PeerLinkLocal         = "link_local"
	PeerGatewayServices   = "gateway_dns_dhcp"
	PeerTraefik           = "traefik"
	PeerPortClients       = "port_binding_clients"
	PeerDockerContainers  = "docker_containers"
)

// Directions lists what the declaration allows, in a fixed order.
func (n Network) Directions() []Direction {
	internet := n.Egress != EgressModulesOnly
	lan := n.Egress == EgressInternetLAN || n.Egress == EgressInternetLANHost
	host := n.Egress == EgressInternetLANHost
	modules := n.Egress == EgressModulesOnly || n.Egress == EgressInternetLANHost || n.ModuleAccess
	published := n.Ingress == IngressPublished
	return []Direction{
		{Flow: "egress", Peer: PeerGatewayServices, Allowed: true},
		{Flow: "egress", Peer: PeerInternet, Allowed: internet},
		{Flow: "egress", Peer: PeerLAN, Allowed: lan},
		{Flow: "egress", Peer: PeerHost, Allowed: host},
		{Flow: "egress", Peer: PeerDockerPublished, Allowed: host},
		{Flow: "egress", Peer: PeerDockerUnpublished, Allowed: false},
		{Flow: "egress", Peer: PeerModules, Allowed: modules},
		{Flow: "egress", Peer: PeerOtherLeases, Allowed: false},
		{Flow: "egress", Peer: PeerLinkLocal, Allowed: false},
		{Flow: "egress", Peer: PeerSameLease, Allowed: n.IntraLease},
		{Flow: "ingress", Peer: PeerTraefik, Allowed: published && len(n.HTTPPorts) > 0},
		{Flow: "ingress", Peer: PeerPortClients, Allowed: published && len(n.Ports) > 0},
		{Flow: "ingress", Peer: PeerLAN, Allowed: false},
		{Flow: "ingress", Peer: PeerHost, Allowed: false},
		{Flow: "ingress", Peer: PeerDockerContainers, Allowed: false},
		{Flow: "ingress", Peer: PeerOtherLeases, Allowed: false},
		{Flow: "ingress", Peer: PeerSameLease, Allowed: n.IntraLease},
	}
}
