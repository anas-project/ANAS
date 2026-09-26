package incusprovision

import (
	"slices"
	"testing"
)

func TestPlanDigestIgnoresNetworkEnumerationOrder(t *testing.T) {
	obs := newFakeRuntime(t).obs
	obs.ExternalCIDRs = []string{"172.17.0.0/16", "10.203.0.0/24", "127.0.0.0/8"}
	obs.DockerCIDRs = []string{"172.17.0.0/16", "10.203.0.0/24"}
	obs.IncusCIDRs = []string{"10.80.0.0/24", "fd00:80::/64"}
	before := stableDigest(obs)
	first, err := buildPlan(Request{}, obs, State{})
	if err != nil {
		t.Fatal(err)
	}
	if stableDigest(obs) != before {
		t.Fatal("plan canonicalization mutated the caller's observation slices")
	}
	permuted := obs
	permuted.ExternalCIDRs = slices.Clone(obs.ExternalCIDRs)
	permuted.DockerCIDRs = slices.Clone(obs.DockerCIDRs)
	permuted.IncusCIDRs = slices.Clone(obs.IncusCIDRs)
	slices.Reverse(permuted.ExternalCIDRs)
	slices.Reverse(permuted.DockerCIDRs)
	slices.Reverse(permuted.IncusCIDRs)
	second, err := buildPlan(Request{}, permuted, State{})
	if err != nil || first.Digest != second.Digest || first.ObservationDigest != second.ObservationDigest {
		t.Fatal("the same observed networks invalidate the confirmation merely by enumeration order", err)
	}
}

func TestNetworkCanonicalizationDoesNotHideTopologyChanges(t *testing.T) {
	obs := newFakeRuntime(t).obs
	obs.ExternalCIDRs = []string{"10.203.0.0/24", "172.17.0.0/16"}
	obs.DockerCIDRs = []string{"10.203.0.0/24", "172.17.0.0/16"}
	obs.IncusCIDRs = []string{"10.80.0.0/24"}
	obs.ControlNetworkID = "original-immutable-identity"
	obs.ControlInterfaceName = "br-anas-ctrl"
	obs.ControlInterfaceIndex = 5
	obs.ControlGateway = "10.203.0.1"
	obs.ControlSubnet = "10.203.0.0/24"
	first, err := buildPlan(Request{}, obs, State{})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Observation){
		"external addition":  func(o *Observation) { o.ExternalCIDRs = append(slices.Clone(o.ExternalCIDRs), "10.203.0.0/25") },
		"docker removal":     func(o *Observation) { o.DockerCIDRs = slices.Clone(o.DockerCIDRs[:1]) },
		"incus replacement":  func(o *Observation) { o.IncusCIDRs = []string{"10.90.0.0/24"} },
		"duplicate addition": func(o *Observation) { o.DockerCIDRs = append(slices.Clone(o.DockerCIDRs), "10.203.0.0/24") },
		"network identity":   func(o *Observation) { o.ControlNetworkID = "replacement-network" },
		"interface name":     func(o *Observation) { o.ControlInterfaceName = "br-other" },
		"interface index":    func(o *Observation) { o.ControlInterfaceIndex++ },
		"gateway":            func(o *Observation) { o.ControlGateway = "10.203.0.2" },
		"subnet":             func(o *Observation) { o.ControlSubnet = "10.204.0.0/24" },
		"service":            func(o *Observation) { o.IncusDaemonActive = !o.IncusDaemonActive },
		"running guest":      func(o *Observation) { o.RunningManagedGuests++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := obs
			mutate(&changed)
			second, err := buildPlan(Request{}, changed, State{})
			if err != nil || first.Digest == second.Digest || first.ObservationDigest == second.ObservationDigest {
				t.Fatal("a real resource/readiness change did not invalidate the confirmation", err)
			}
		})
	}
}
