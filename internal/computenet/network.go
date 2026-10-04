// Package computenet validates a compute lease's network declaration: its
// egress tier, the two lease switches, its ingress tier, its slots and its port
// bindings. Core freezes the result into the deployment; the Provider turns it
// into the lease bridge's ACL; hostd reads the frozen port bindings back. None
// of those three trusts the others' copy, so they all parse through here.
package computenet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	EgressInternet        = "internet"
	EgressInternetLAN     = "internet_lan"
	EgressInternetLANHost = "internet_lan_host"
	EgressModulesOnly     = "modules_only"

	IngressNone      = "none"
	IngressPublished = "published"

	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"

	// MaxSlots and MaxPortBindings bound what one lease can ask the host for.
	MaxSlots        = 32
	MaxPortBindings = 64
)

var (
	slotName       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	instanceSuffix = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// Slot pins one instance name to a fixed address the Provider reserves.
type Slot struct {
	Name     string `json:"name" yaml:"name"`
	Instance string `json:"instance" yaml:"instance"`
}

// PortBinding forwards one host port, unchanged, to a slot. HostPort is zero
// while the declaration says auto; Core resolves it before freezing.
type PortBinding struct {
	Protocol  string `json:"protocol" yaml:"protocol"`
	HostPort  uint16 `json:"host_port" yaml:"host_port"`
	Auto      bool   `json:"auto,omitempty" yaml:"auto,omitempty"`
	Slot      string `json:"slot" yaml:"slot"`
	GuestPort uint16 `json:"guest_port" yaml:"guest_port"`
}

// Network is the normalized declaration. HTTPPorts mirrors
// publish.http.allowed_ports, the only part of the HTTP publication the
// lease ACL needs; everything else about it stays in computeingress.
type Network struct {
	Egress       string        `json:"egress" yaml:"egress"`
	ModuleAccess bool          `json:"module_access" yaml:"module_access"`
	IntraLease   bool          `json:"intra_lease" yaml:"intra_lease"`
	Ingress      string        `json:"ingress" yaml:"ingress"`
	Slots        []Slot        `json:"slots,omitempty" yaml:"slots,omitempty"`
	HTTPPorts    []uint16      `json:"http_ports,omitempty" yaml:"http_ports,omitempty"`
	Ports        []PortBinding `json:"ports,omitempty" yaml:"ports,omitempty"`
	// TraefikPort is the port Traefik's HTTPS entrypoint listens on inside its
	// container. Core freezes it when the lease reaches Modules through
	// Traefik; it is not part of the declaration.
	TraefikPort uint16 `json:"traefik_port,omitempty" yaml:"traefik_port,omitempty"`
}

// Default is what an omitted network declaration means.
func Default() Network {
	return Network{Egress: EgressInternet, Ingress: IngressNone}
}

// ParseSpec reads network and publish from a compute resource spec. The
// instance prefix and the instance quota come from the same spec; they bound
// which names a slot may pin and how many slots a lease may hold.
func ParseSpec(spec map[string]any) (Network, error) {
	n := Default()
	prefix, _ := spec["instance_prefix"].(string)
	maxInstances := 0
	if quota, ok := spec["quota"].(map[string]any); ok {
		maxInstances, _ = intValue(quota["max_instances"])
	}
	if raw, present := spec["network"]; present {
		object, ok := raw.(map[string]any)
		if !ok {
			return Network{}, fmt.Errorf("network must be an object")
		}
		for key := range object {
			switch key {
			case "egress", "module_access", "intra_lease", "ingress", "slots":
			default:
				return Network{}, fmt.Errorf("network.%s is not a supported field", key)
			}
		}
		if value, present := object["egress"]; present {
			if n.Egress, ok = value.(string); !ok {
				return Network{}, fmt.Errorf("network.egress must be a string")
			}
		}
		if value, present := object["ingress"]; present {
			if n.Ingress, ok = value.(string); !ok {
				return Network{}, fmt.Errorf("network.ingress must be a string")
			}
		}
		for key, target := range map[string]*bool{"module_access": &n.ModuleAccess, "intra_lease": &n.IntraLease} {
			if value, present := object[key]; present {
				if *target, ok = value.(bool); !ok {
					return Network{}, fmt.Errorf("network.%s must be true or false", key)
				}
			}
		}
		if raw, present := object["slots"]; present {
			slots, ok := raw.(map[string]any)
			if !ok {
				return Network{}, fmt.Errorf("network.slots must map slot names to {instance: <name>}")
			}
			for name, rawSlot := range slots {
				slot, ok := rawSlot.(map[string]any)
				instance, named := slot["instance"].(string)
				if !ok || !named || len(slot) != 1 {
					return Network{}, fmt.Errorf("network.slots.%s must be exactly {instance: <name>}", name)
				}
				n.Slots = append(n.Slots, Slot{Name: name, Instance: instance})
			}
			slices.SortFunc(n.Slots, func(a, b Slot) int { return strings.Compare(a.Name, b.Name) })
		}
	}
	publishHTTP := false
	if raw, present := spec["publish"]; present {
		object, ok := raw.(map[string]any)
		if !ok {
			return Network{}, fmt.Errorf("publish must be an object")
		}
		for key := range object {
			switch key {
			case "http":
				publishHTTP = true
				http, ok := object["http"].(map[string]any)
				if !ok {
					return Network{}, fmt.Errorf("publish.http must be an object")
				}
				ports, ok := http["allowed_ports"].([]any)
				if !ok {
					return Network{}, fmt.Errorf("publish.http.allowed_ports must list guest ports")
				}
				for _, raw := range ports {
					port, ok := portValue(raw)
					if !ok {
						return Network{}, fmt.Errorf("publish.http.allowed_ports must contain integers between 1 and 65535")
					}
					n.HTTPPorts = append(n.HTTPPorts, port)
				}
				slices.Sort(n.HTTPPorts)
			case "ports":
				items, ok := object["ports"].([]any)
				if !ok {
					return Network{}, fmt.Errorf("publish.ports must be a list")
				}
				for i, raw := range items {
					binding, err := parseBinding(raw)
					if err != nil {
						return Network{}, fmt.Errorf("publish.ports[%d]: %w", i, err)
					}
					n.Ports = append(n.Ports, binding)
				}
			default:
				return Network{}, fmt.Errorf("publish.%s is not a supported field", key)
			}
		}
	}
	if err := n.validate(prefix, maxInstances); err != nil {
		return Network{}, err
	}
	if n.Ingress == IngressNone && (publishHTTP || len(n.Ports) > 0) {
		// INCUS-R-132: a lease that admits nothing cannot declare what it admits.
		return Network{}, fmt.Errorf("network.ingress none cannot declare publish; set network.ingress: published")
	}
	n.sortPorts()
	return n, nil
}

func parseBinding(raw any) (PortBinding, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return PortBinding{}, fmt.Errorf("must be {protocol, host_port, slot, guest_port}")
	}
	for key := range object {
		switch key {
		case "protocol", "host_port", "slot", "guest_port":
		default:
			return PortBinding{}, fmt.Errorf("%s is not a supported field", key)
		}
	}
	b := PortBinding{}
	b.Protocol, _ = object["protocol"].(string)
	b.Slot, _ = object["slot"].(string)
	if guest, ok := portValue(object["guest_port"]); ok {
		b.GuestPort = guest
	} else {
		return PortBinding{}, fmt.Errorf("guest_port must be an integer between 1 and 65535")
	}
	switch value := object["host_port"].(type) {
	case string:
		if value != "auto" {
			return PortBinding{}, fmt.Errorf("host_port must be an integer between 1 and 65535 or auto")
		}
		b.Auto = true
	default:
		port, ok := portValue(value)
		if !ok {
			return PortBinding{}, fmt.Errorf("host_port must be an integer between 1 and 65535 or auto")
		}
		b.HostPort = port
	}
	return b, nil
}

func (n Network) validate(prefix string, maxInstances int) error {
	switch n.Egress {
	case EgressInternet, EgressInternetLAN, EgressInternetLANHost, EgressModulesOnly:
	default:
		return fmt.Errorf("network.egress must be internet, internet_lan, internet_lan_host or modules_only")
	}
	if n.Ingress != IngressNone && n.Ingress != IngressPublished {
		return fmt.Errorf("network.ingress must be none or published")
	}
	if len(n.Slots) > MaxSlots {
		return fmt.Errorf("network.slots holds at most %d slots", MaxSlots)
	}
	if maxInstances > 0 && len(n.Slots) > maxInstances {
		return fmt.Errorf("network.slots cannot pin more instances than quota.max_instances")
	}
	instances := map[string]bool{}
	slots := map[string]bool{}
	for _, slot := range n.Slots {
		if !slotName.MatchString(slot.Name) {
			return fmt.Errorf("network.slots names must be lowercase labels of at most 31 characters")
		}
		// A slot pins a name the shared client will create, so the name has to
		// be one this lease may create at all.
		if prefix == "" || !strings.HasPrefix(slot.Instance, prefix) || !instanceSuffix.MatchString(strings.TrimPrefix(slot.Instance, prefix)) {
			return fmt.Errorf("network.slots.%s instance must be inside the lease instance_prefix", slot.Name)
		}
		if instances[slot.Instance] {
			return fmt.Errorf("network.slots pins instance %s twice", slot.Instance)
		}
		instances[slot.Instance], slots[slot.Name] = true, true
	}
	seenHTTP := map[uint16]bool{}
	for _, port := range n.HTTPPorts {
		if port == 0 || seenHTTP[port] {
			return fmt.Errorf("publish.http.allowed_ports must contain distinct ports between 1 and 65535")
		}
		seenHTTP[port] = true
	}
	if len(n.Ports) > MaxPortBindings {
		return fmt.Errorf("publish.ports holds at most %d bindings", MaxPortBindings)
	}
	hostPorts := map[string]bool{}
	for _, b := range n.Ports {
		if b.Protocol != ProtocolTCP && b.Protocol != ProtocolUDP {
			return fmt.Errorf("publish.ports protocol must be tcp or udp")
		}
		if !slots[b.Slot] {
			return fmt.Errorf("publish.ports slot %q is not declared in network.slots", b.Slot)
		}
		// A declared auto port has no number yet; a frozen one keeps the auto
		// marker next to the number Core chose, so later applies reuse it.
		if b.GuestPort == 0 || b.HostPort == 0 && !b.Auto {
			return fmt.Errorf("publish.ports needs a guest_port and a host_port or auto")
		}
		if b.HostPort != 0 {
			key := b.Protocol + "/" + strconv.Itoa(int(b.HostPort))
			if hostPorts[key] {
				return fmt.Errorf("publish.ports binds %s twice", key)
			}
			hostPorts[key] = true
		}
	}
	return nil
}

func (n *Network) sortPorts() {
	slices.SortStableFunc(n.Ports, func(a, b PortBinding) int {
		if c := strings.Compare(a.Slot, b.Slot); c != 0 {
			return c
		}
		if c := strings.Compare(a.Protocol, b.Protocol); c != 0 {
			return c
		}
		return int(a.GuestPort) - int(b.GuestPort)
	})
}

// Validate checks a frozen network: every port binding has its host port.
func (n Network) Validate(prefix string, maxInstances int) error {
	if err := n.validate(prefix, maxInstances); err != nil {
		return err
	}
	if n.Ingress == IngressNone && (len(n.HTTPPorts) > 0 || len(n.Ports) > 0) {
		return fmt.Errorf("network.ingress none cannot carry publications")
	}
	for _, b := range n.Ports {
		if b.HostPort == 0 {
			return fmt.Errorf("frozen port binding has no resolved host port")
		}
	}
	if n.NeedsTraefik() != (n.TraefikPort != 0) {
		return fmt.Errorf("frozen lease network must name the Traefik port exactly when it reaches Traefik")
	}
	return nil
}

// NeedsTraefik reports whether the lease ACL names the Traefik address set:
// as an egress destination or as the only HTTP publication source.
func (n Network) NeedsTraefik() bool {
	return n.Egress == EgressModulesOnly || n.ModuleAccess && (n.Egress == EgressInternet || n.Egress == EgressInternetLAN) ||
		len(n.HTTPPorts) > 0
}

// SlotFor returns the slot that pins an instance name.
func (n Network) SlotFor(instance string) (Slot, bool) {
	for _, slot := range n.Slots {
		if slot.Instance == instance {
			return slot, true
		}
	}
	return Slot{}, false
}

// SlotByName returns a declared slot.
func (n Network) SlotByName(name string) (Slot, bool) {
	for _, slot := range n.Slots {
		if slot.Name == name {
			return slot, true
		}
	}
	return Slot{}, false
}

// Clone copies the slices so a frozen value cannot be changed through a caller.
func (n *Network) Clone() *Network {
	if n == nil {
		return nil
	}
	out := *n
	out.Slots = slices.Clone(n.Slots)
	out.HTTPPorts = slices.Clone(n.HTTPPorts)
	out.Ports = slices.Clone(n.Ports)
	return &out
}

// Encode is the single-line JSON form the Provider receives.
func (n Network) Encode() (string, error) {
	body, err := json.Marshal(n)
	return string(body), err
}

// Decode reads Encode's output, refusing unknown or duplicate fields.
func Decode(body string) (Network, error) {
	var n Network
	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&n); err != nil {
		return Network{}, fmt.Errorf("lease network declaration is not valid JSON")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return Network{}, fmt.Errorf("lease network declaration has trailing input")
	}
	return n, nil
}

func portValue(value any) (uint16, bool) {
	n, ok := intValue(value)
	if !ok || n < 1 || n > 65535 {
		return 0, false
	}
	return uint16(n), true
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case uint16:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}
