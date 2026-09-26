package incusprovision

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestForwardingObservationCannotTreatEarlierAcceptAsReachability(t *testing.T) {
	body := []byte(`{"nftables":[{"metainfo":{"json_schema_version":1}},
	{"chain":{"family":"inet","table":"incus","name":"fwd.test","type":"filter","hook":"forward","prio":-100,"policy":"accept"}},
	{"chain":{"family":"ip","table":"filter","name":"FORWARD","type":"filter","hook":"forward","prio":0,"policy":"drop"}},
	{"chain":{"family":"ip","table":"filter","name":"DOCKER-USER"}}]}`)
	got, err := parseForwardingObservation([]byte("1\n"), body)
	if err != nil || got.IPv4Routing != "enabled" || got.IPv4Filter != "drop_observed" || got.IPv4BaseChains != 2 || !got.DockerUserChain {
		t.Fatal("later Docker base-chain drop was hidden by an earlier accept", got, err)
	}
	obs := newFakeRuntime(t).obs
	obs.Forwarding = got
	plan, err := buildPlan(Request{}, obs, State{})
	if err != nil || plan.ComputeReady || !slices.Contains(plan.Warnings, "ipv4_forward_filter_drop_observed") ||
		!slices.Contains(plan.Warnings, "guest_egress_unverified") {
		t.Fatal("forwarding diagnostic was not exposed without granting compute readiness", plan, err)
	}
}

func TestForwardingObservationIsNotAnAuthorizationOrInstallationBlocker(t *testing.T) {
	for _, policy := range []string{"accept", "drop"} {
		body := []byte(`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"FORWARD","type":"filter","hook":"forward","prio":0,"policy":"` + policy + `"}}]}`)
		got, err := parseForwardingObservation([]byte("0\n"), body)
		if err != nil || got.IPv4Routing != "disabled" {
			t.Fatal(got, err)
		}
		obs := newFakeRuntime(t).obs
		obs.Forwarding = got
		plan, err := buildPlan(Request{}, obs, State{})
		if err != nil || plan.ComputeReady || plan.Disposition != "pending" || len(plan.Steps) == 0 || len(plan.Blockers) != 0 ||
			!slices.Contains(plan.Warnings, "ipv4_forwarding_disabled") || !slices.Contains(plan.Warnings, "guest_egress_unverified") {
			t.Fatal("control-plane maintenance was blocked or data-plane readiness was fabricated", err)
		}
	}
}

func TestForwardingMalformedDataNeverBecomesNoDropObserved(t *testing.T) {
	for _, body := range []string{
		``, `{}`, `{"nftables":null}`, `{"nftables":[],"nftables":[]}`, `{"Nftables":[]}`,
		`{"nftables":[null]}`, `{"nftables":[{"chain":null}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"FORWARD","type":"filter","hook":"forward","policy":"accept","policy":"drop"}}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"FORWARD","type":"filter","hook":"forward","Policy":"drop"}}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"FORWARD","type":"filter","hook":"forward"}}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"FORWARD","type":"filter","hook":"forward","policy":"private-input"}}]}`,
		`{"nftables":[{"metainfo":{"json_schema_version":2}}]}`,
		`{"nftables":[{"metainfo":{"JSON_SCHEMA_VERSION":1}}]}`,
		`{"nftables":[{"metainfo":{"json_schema_version":2,"JSON_SCHEMA_VERSION":1}}]}`,
		`{"nftables":[{"metainfo":{"json_schema_version":1,"future_schema":true}}]}`,
		`{"nftables":[{"metainfo":{"json_schema_version":1}},{"metainfo":{"json_schema_version":1}}]}`,
		`{"nftables":[{"chain":{"family":"future","table":"filter","name":"FORWARD","type":"filter","hook":"forward","policy":"drop"}}]}`,
		`{"nftables":[{"chain":{"family":"ip","table":"filter","name":"FORWARD","hook":null}}]}`,
		`{"nftables":[]} {}`, strings.Repeat("x", (1<<20)+1),
	} {
		got, err := parseForwardingObservation([]byte("1"), []byte(body))
		if !errors.Is(err, ErrIncomplete) || got.IPv4Filter != "unobserved" || strings.Contains(err.Error(), "private-input") {
			t.Errorf("invalid observation was accepted or exposed: %.80s", body)
		}
	}
}

func TestForwardingDigestIgnoresCountersButBindsPolicyAndRouting(t *testing.T) {
	makeObservation := func(policy, routing string, counter int) Observation {
		t.Helper()
		body := map[string]any{"nftables": []any{
			map[string]any{"chain": map[string]any{"family": "ip", "table": "filter", "name": "FORWARD", "type": "filter", "hook": "forward", "policy": policy}},
			map[string]any{"rule": map[string]any{"family": "ip", "table": "filter", "chain": "FORWARD", "expr": []any{map[string]any{"counter": map[string]int{"packets": counter}}}}},
		}}
		encoded, _ := json.Marshal(body)
		forwarding, err := parseForwardingObservation([]byte(routing), encoded)
		if err != nil {
			t.Fatal(err)
		}
		obs := newFakeRuntime(t).obs
		obs.Forwarding = forwarding
		return obs
	}
	original := observationDigest(makeObservation("drop", "1", 1))
	if original != observationDigest(makeObservation("drop", "1", 123456)) ||
		original == observationDigest(makeObservation("accept", "1", 1)) ||
		original == observationDigest(makeObservation("drop", "0", 1)) {
		t.Fatal("traffic counters invalidate approval, or real forwarding facts are not bound")
	}
}

func TestForwardingUnknownOrEmptyPolicyNeverImpliesReachability(t *testing.T) {
	got, err := parseForwardingObservation([]byte("not-a-sysctl"), []byte(`{"nftables":[]}`))
	if err != nil || got.IPv4Routing != "unobserved" || got.IPv4Filter != "no_drop_observed" || got.IPv4BaseChains != 0 {
		t.Fatal(got, err)
	}
	obs := newFakeRuntime(t).obs
	obs.Forwarding = got
	plan, err := buildPlan(Request{}, obs, State{})
	if err != nil || plan.ComputeReady || !slices.Contains(plan.Warnings, "guest_egress_unverified") {
		t.Fatal("empty ruleset became positive admission", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newLocalRuntime().observeForwarding(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled observation should not run a command or read host policy", err)
	}
}
