package main

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/anas-project/ANAS/internal/computeclient"
)

// A lease bridge carries one Provider-owned network ACL that only lets a
// guest send from the bridge's own subnets. Masquerade rewrites exactly those
// sources, so a guest forging an off-subnet source -- which IPv6 source
// filtering would stop, but that needs host br_netfilter -- has nowhere to go:
// the daemon drops it before routing. Ingress stays open so replies, DHCP and
// DNS keep working and the Incus defaults decide the rest.

const (
	aclEgressDefault  = "drop"
	aclIngressDefault = "allow"
	aclRuleNote       = "ANAS lease: only the bridge's own source addresses leave"
)

type aclRule struct {
	Action          string `json:"action"`
	Source          string `json:"source,omitempty"`
	Destination     string `json:"destination,omitempty"`
	Protocol        string `json:"protocol,omitempty"`
	SourcePort      string `json:"source_port,omitempty"`
	DestinationPort string `json:"destination_port,omitempty"`
	ICMPType        string `json:"icmp_type,omitempty"`
	ICMPCode        string `json:"icmp_code,omitempty"`
	Description     string `json:"description,omitempty"`
	State           string `json:"state"`
}

type networkACL struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Egress      []aclRule         `json:"egress"`
	Ingress     []aclRule         `json:"ingress"`
	Config      map[string]string `json:"config"`
}

// leaseSubnets returns the bridge's concrete networks. "auto" is replaced by
// the daemon at creation, so a missing concrete IPv4 network is an error; IPv6
// is absent when the lease has it off.
func leaseSubnets(n network, l lease) ([]string, error) {
	var subnets []string
	for _, family := range []string{"ipv4", "ipv6"} {
		value := strings.TrimSpace(n.Config[family+".address"])
		if value == "" || value == "none" {
			if family == "ipv4" || l.NetworkIPv6 {
				return nil, fmt.Errorf("lease network %s has no concrete %s subnet", n.Name, family)
			}
			continue
		}
		if family == "ipv6" && !l.NetworkIPv6 {
			return nil, fmt.Errorf("lease network %s still carries IPv6 while the lease has it off", n.Name)
		}
		_, prefix, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("lease network %s %s address is not a subnet", n.Name, family)
		}
		subnets = append(subnets, prefix.String())
	}
	return subnets, nil
}

func desiredLeaseACL(l lease, subnets []string) networkACL {
	return networkACL{
		Description: "ANAS compute lease source fence for " + l.Consumer,
		Egress:      []aclRule{{Action: "allow", Source: strings.Join(subnets, ","), Description: aclRuleNote, State: "enabled"}},
		Ingress:     []aclRule{},
		Config: map[string]string{
			"user.anas.consumer": l.Consumer,
			"user.anas.sandbox":  l.Sandbox,
			leaseCredentialKey:   l.Credential,
		},
	}
}

// ensureNetworkACL writes the ACL before the bridge references it (the daemon
// refuses an unknown ACL name) and returns its name.
func ensureNetworkACL(ctx context.Context, c *client, l lease, n network) (string, error) {
	subnets, err := leaseSubnets(n, l)
	if err != nil {
		return "", err
	}
	name := computeclient.NetworkName(l.Sandbox)
	desired := desiredLeaseACL(l, subnets)
	path := "/1.0/network-acls/" + name + "?project=default"
	var current networkACL
	err = c.do(ctx, "GET", path, nil, &current)
	switch {
	case err == nil:
		// A new object type has no pre-marker history to adopt: an ACL of
		// this name that is not this lease's is someone else's.
		if err := verifyACLOwner(current, l); err != nil {
			return "", err
		}
		if err := c.do(ctx, "PUT", path, desired, nil); err != nil {
			return "", err
		}
	case isNotFound(err):
		desired.Name = name
		if err := c.do(ctx, "POST", "/1.0/network-acls?project=default", desired, nil); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	var actual networkACL
	if err := c.do(ctx, "GET", path, nil, &actual); err != nil {
		return "", fmt.Errorf("read back lease network ACL: %w", err)
	}
	if err := verifyACL(actual, l, subnets); err != nil {
		return "", err
	}
	return name, nil
}

func verifyACLOwner(a networkACL, l lease) error {
	if a.Config["user.anas.consumer"] != l.Consumer || a.Config["user.anas.sandbox"] != l.Sandbox || a.Config[leaseCredentialKey] != l.Credential {
		return fmt.Errorf("network ACL %s is not owned by this lease; refusing to adopt or modify it", computeclient.NetworkName(l.Sandbox))
	}
	return nil
}

// verifyACL requires exactly one enabled egress allow rule naming exactly the
// bridge subnets, and no ingress rules. Anything else is drift.
func verifyACL(a networkACL, l lease, subnets []string) error {
	if err := verifyACLOwner(a, l); err != nil {
		return err
	}
	name := computeclient.NetworkName(l.Sandbox)
	if len(a.Egress) != 1 || len(a.Ingress) != 0 {
		return fmt.Errorf("network ACL %s carries unmanaged rules", name)
	}
	rule := a.Egress[0]
	if rule.Action != "allow" || rule.State != "enabled" || rule.Destination != "" || rule.Protocol != "" ||
		rule.SourcePort != "" || rule.DestinationPort != "" || rule.ICMPType != "" || rule.ICMPCode != "" {
		return fmt.Errorf("network ACL %s egress rule was changed", name)
	}
	got := strings.Split(rule.Source, ",")
	for i := range got {
		got[i] = strings.TrimSpace(got[i])
	}
	want := slices.Clone(subnets)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("network ACL %s does not allow exactly the lease subnets", name)
	}
	return nil
}

// bridgeACLConfig are the keys that attach the ACL to the bridge.
func bridgeACLConfig(acl string) map[string]string {
	return map[string]string{
		"security.acls":                        acl,
		"security.acls.default.egress.action":  aclEgressDefault,
		"security.acls.default.ingress.action": aclIngressDefault,
	}
}
