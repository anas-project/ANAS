package computenet

import (
	"maps"
	"strings"
	"testing"
)

func baseSpec() map[string]any {
	return map[string]any{
		"sandbox":         "anas-lab",
		"instance_prefix": "anas-lab-",
		"quota":           map[string]any{"max_instances": 4, "cpu": 1, "memory_mib": 512, "disk_gib": 4},
	}
}

func TestOmittedNetworkIsInternetWithoutIngress(t *testing.T) {
	n, err := ParseSpec(baseSpec())
	if err != nil {
		t.Fatal(err)
	}
	if n.Egress != EgressInternet || n.Ingress != IngressNone || n.ModuleAccess || n.IntraLease || len(n.Slots)+len(n.Ports)+len(n.HTTPPorts) != 0 {
		t.Fatalf("default network = %+v", n)
	}
	if n.NeedsTraefik() {
		t.Fatal("the default lease must not depend on the Traefik address set")
	}
}

func TestParsesEveryDeclaredField(t *testing.T) {
	spec := baseSpec()
	spec["network"] = map[string]any{
		"egress": "internet_lan", "module_access": true, "intra_lease": true, "ingress": "published",
		"slots": map[string]any{"dev": map[string]any{"instance": "anas-lab-dev"}, "db": map[string]any{"instance": "anas-lab-db"}},
	}
	spec["publish"] = map[string]any{
		"http": map[string]any{"allowed_ports": []any{8080, 7000}, "domain": map[string]any{"mode": "fixed", "prefix": "lab"}},
		"ports": []any{
			map[string]any{"protocol": "udp", "host_port": 30053, "slot": "dev", "guest_port": 53},
			map[string]any{"protocol": "tcp", "host_port": "auto", "slot": "dev", "guest_port": 22},
			map[string]any{"protocol": "tcp", "host_port": 30432, "slot": "db", "guest_port": 5432},
		},
	}
	n, err := ParseSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if n.Slots[0].Name != "db" || n.Slots[1].Name != "dev" {
		t.Fatalf("slots are not sorted: %+v", n.Slots)
	}
	if n.HTTPPorts[0] != 7000 || n.HTTPPorts[1] != 8080 {
		t.Fatalf("HTTP ports are not sorted: %v", n.HTTPPorts)
	}
	if len(n.Ports) != 3 || n.Ports[0].Slot != "db" || !n.Ports[1].Auto || n.Ports[1].HostPort != 0 || n.Ports[2].Protocol != "udp" {
		t.Fatalf("ports = %+v", n.Ports)
	}
	if !n.NeedsTraefik() {
		t.Fatal("HTTP publication and module_access both need the Traefik address set")
	}
	if err := n.Validate("anas-lab-", 4); err == nil {
		t.Fatal("an unresolved auto port must not pass frozen validation")
	}
	n.Ports[1].HostPort = 30001
	if err := n.Validate("anas-lab-", 4); err == nil {
		t.Fatal("a lease that reaches Traefik froze without the Traefik port")
	}
	n.TraefikPort = 9000
	if err := n.Validate("anas-lab-", 4); err != nil {
		t.Fatalf("resolved network: %v", err)
	}
	encoded, err := n.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(encoded)
	if err != nil || decoded.Ports[1].HostPort != 30001 || !decoded.Ports[1].Auto {
		t.Fatalf("round trip = %+v, %v", decoded, err)
	}
}

// INCUS-R-132: none admits nothing, so it cannot declare a publication.
func TestIngressNoneRejectsPublications(t *testing.T) {
	for name, publish := range map[string]map[string]any{
		"http":  {"http": map[string]any{"allowed_ports": []any{80}, "domain": map[string]any{"mode": "fixed", "prefix": "a"}}},
		"ports": {"ports": []any{map[string]any{"protocol": "tcp", "host_port": 30000, "slot": "dev", "guest_port": 22}}},
	} {
		spec := baseSpec()
		spec["network"] = map[string]any{"slots": map[string]any{"dev": map[string]any{"instance": "anas-lab-dev"}}}
		spec["publish"] = publish
		if _, err := ParseSpec(spec); err == nil || !strings.Contains(err.Error(), "ingress none") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestRejectsInvalidDeclarations(t *testing.T) {
	slots := func(extra map[string]any) map[string]any {
		out := map[string]any{"dev": map[string]any{"instance": "anas-lab-dev"}}
		maps.Copy(out, extra)
		return out
	}
	cases := map[string]func(map[string]any){
		"unknown tier":    func(s map[string]any) { s["network"] = map[string]any{"egress": "everything"} },
		"unknown ingress": func(s map[string]any) { s["network"] = map[string]any{"ingress": "open"} },
		"unknown field":   func(s map[string]any) { s["network"] = map[string]any{"lan": true} },
		"string switch":   func(s map[string]any) { s["network"] = map[string]any{"module_access": "yes"} },
		"slot outside prefix": func(s map[string]any) {
			s["network"] = map[string]any{"slots": map[string]any{"dev": map[string]any{"instance": "other-dev"}}}
		},
		"slot extra field": func(s map[string]any) {
			s["network"] = map[string]any{"slots": map[string]any{"dev": map[string]any{"instance": "anas-lab-dev", "ip": "10.0.0.2"}}}
		},
		"duplicate instance": func(s map[string]any) {
			s["network"] = map[string]any{"slots": slots(map[string]any{"two": map[string]any{"instance": "anas-lab-dev"}})}
		},
		"bad slot name": func(s map[string]any) {
			s["network"] = map[string]any{"slots": map[string]any{"Dev": map[string]any{"instance": "anas-lab-dev"}}}
		},
		"more slots than quota": func(s map[string]any) {
			many := map[string]any{}
			for _, name := range []string{"a", "b", "c", "d", "e"} {
				many[name] = map[string]any{"instance": "anas-lab-" + name}
			}
			s["network"] = map[string]any{"slots": many}
		},
		"unknown slot": func(s map[string]any) {
			s["network"] = map[string]any{"ingress": "published", "slots": slots(nil)}
			s["publish"] = map[string]any{"ports": []any{map[string]any{"protocol": "tcp", "host_port": 30000, "slot": "web", "guest_port": 80}}}
		},
		"bad protocol": func(s map[string]any) {
			s["network"] = map[string]any{"ingress": "published", "slots": slots(nil)}
			s["publish"] = map[string]any{"ports": []any{map[string]any{"protocol": "sctp", "host_port": 30000, "slot": "dev", "guest_port": 80}}}
		},
		"duplicate host port": func(s map[string]any) {
			s["network"] = map[string]any{"ingress": "published", "slots": slots(nil)}
			s["publish"] = map[string]any{"ports": []any{
				map[string]any{"protocol": "tcp", "host_port": 30000, "slot": "dev", "guest_port": 80},
				map[string]any{"protocol": "tcp", "host_port": 30000, "slot": "dev", "guest_port": 81},
			}}
		},
		"bad host port": func(s map[string]any) {
			s["network"] = map[string]any{"ingress": "published", "slots": slots(nil)}
			s["publish"] = map[string]any{"ports": []any{map[string]any{"protocol": "tcp", "host_port": "random", "slot": "dev", "guest_port": 80}}}
		},
		"zero guest port": func(s map[string]any) {
			s["network"] = map[string]any{"ingress": "published", "slots": slots(nil)}
			s["publish"] = map[string]any{"ports": []any{map[string]any{"protocol": "tcp", "host_port": 30000, "slot": "dev", "guest_port": 0}}}
		},
		"unknown publish": func(s map[string]any) {
			s["network"] = map[string]any{"ingress": "published"}
			s["publish"] = map[string]any{"tcp": []any{}}
		},
	}
	for name, mutate := range cases {
		spec := baseSpec()
		mutate(spec)
		if _, err := ParseSpec(spec); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestDirectionsFollowTheTierTable(t *testing.T) {
	allowed := func(n Network, flow, peer string) bool {
		for _, d := range n.Directions() {
			if d.Flow == flow && d.Peer == peer {
				return d.Allowed
			}
		}
		t.Fatalf("no %s/%s row", flow, peer)
		return false
	}
	internet := Default()
	if !allowed(internet, "egress", PeerInternet) || allowed(internet, "egress", PeerLAN) || allowed(internet, "egress", PeerModules) {
		t.Fatal("internet tier")
	}
	internet.ModuleAccess = true
	if !allowed(internet, "egress", PeerModules) || allowed(internet, "egress", PeerHost) {
		t.Fatal("module_access")
	}
	host := Network{Egress: EgressInternetLANHost, Ingress: IngressNone}
	if !allowed(host, "egress", PeerHost) || !allowed(host, "egress", PeerDockerPublished) || allowed(host, "egress", PeerDockerUnpublished) || allowed(host, "egress", PeerOtherLeases) {
		t.Fatal("internet_lan_host tier")
	}
	only := Network{Egress: EgressModulesOnly, Ingress: IngressNone}
	if allowed(only, "egress", PeerInternet) || !allowed(only, "egress", PeerModules) || !allowed(only, "egress", PeerGatewayServices) {
		t.Fatal("modules_only tier")
	}
	published := Network{Egress: EgressInternet, Ingress: IngressPublished, HTTPPorts: []uint16{80}}
	if !allowed(published, "ingress", PeerTraefik) || allowed(published, "ingress", PeerPortClients) || allowed(published, "ingress", PeerOtherLeases) {
		t.Fatal("published tier")
	}
}
