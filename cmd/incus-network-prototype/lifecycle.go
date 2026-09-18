package main

import (
	"fmt"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
)

const publicationSchema = "anas.incus-http-lab-publication/v1"

type publicationRecord struct {
	AuthorizationEpoch string      `json:"authorization_epoch,omitempty"`
	Schema             string      `json:"schema"`
	Observation        observation `json:"observation"`
	// This file records a proposed publication, never proof it was applied.
	Evidence  *captureEvidence            `json:"evidence,omitempty"`
	Mediation *computeingress.Publication `json:"mediation,omitempty"`
}

type lifecycleStep struct {
	Operation string   `json:"operation"`
	Scope     string   `json:"scope"`
	Argv      []string `json:"argv,omitempty"`
	Artifact  string   `json:"artifact,omitempty"`
	Condition string   `json:"condition,omitempty"`
}

type lifecyclePlan struct {
	Schema           string          `json:"schema"`
	Action           string          `json:"action"`
	Reason           string          `json:"reason,omitempty"`
	GeneratedAt      time.Time       `json:"generated_at"`
	Preconditions    []string        `json:"preconditions"`
	Steps            []lifecycleStep `json:"steps"`
	Teardown         []lifecycleStep `json:"teardown"`
	OnPublishFailure []lifecycleStep `json:"on_publish_failure,omitempty"`
}

func cleanupSteps(o observation, a artifacts) []lifecycleStep {
	return []lifecycleStep{
		{Operation: "remove_http_route", Scope: "lab_traefik", Condition: "remove only owned INCUS_LAB route using the existing renderer; confirm it is absent before continuing"},
		{Operation: "revoke_backend", Scope: "lab_host", Argv: a.Revoke, Condition: "missing tuple is success only after reading back its absence"},
		{Operation: "clear_connections", Scope: "lab_host", Argv: a.Conntrack, Condition: "confirm no remaining original-source/guest-IP/port TCP entries, including when conntrack reports zero matches"},
		{Operation: "delete_guest_route", Scope: "traefik_network_namespace", Argv: a.DeleteRoute, Condition: "confirm the exact owned /32 route is absent"},
		{Operation: "confirm_retirement", Scope: "lab_host", Condition: fmt.Sprintf("keep guest IP %s reserved until route removal, tuple removal and connection cleanup are all confirmed; retain both deny filters; on failure keep IP held and stop", o.GuestIP)},
	}
}

func planLifecycle(previous *publicationRecord, current *observation, evidence *captureEvidence) (lifecyclePlan, *publicationRecord, *artifacts, error) {
	p := lifecyclePlan{Schema: "anas.incus-http-lab-plan/v1", GeneratedAt: time.Now().UTC(), Preconditions: []string{
		"operator-owned disposable lab only; no production route directory or default daemon",
		"one publication per lab session; previous input must identify the session actually applied, not merely another generated plan",
		"reobserve identity, allocation and veth immediately before mutation; snapshots are not live authorization",
		"reserve guest IP through activation and all cleanup; TTL alone does not make early IP reuse safe",
		"preserve Docker/Incus base chains; an early accept does not override a later drop",
	}}
	p.Teardown = []lifecycleStep{
		{Operation: "stop_lab_endpoints", Scope: "lab_session", Condition: "stop or disconnect every lab source and guest; verify the retired bridge cannot carry outside traffic before removing its deny filters"},
		{Operation: "delete_owned_filter", Scope: "lab_host", Argv: []string{"nft", "delete", "table", "inet", "anas_incus_lab"}, Condition: "only after endpoint shutdown; verify this session owns the table"},
		{Operation: "delete_owned_origin_filter", Scope: "lab_host", Argv: []string{"nft", "delete", "table", "bridge", "anas_incus_lab_l2"}, Condition: "only after endpoint shutdown; verify this session owns the table"},
	}
	if previous != nil {
		if previous.Schema != publicationSchema {
			return p, nil, nil, fmt.Errorf("unsupported previous publication schema")
		}
		if err := validateMediatedRecord(previous); err != nil {
			return p, nil, nil, err
		}
		old, err := generate(previous.Observation)
		if err != nil {
			return p, nil, nil, fmt.Errorf("previous publication is invalid; cannot safely derive cleanup")
		}
		p.Steps = append(p.Steps, cleanupSteps(previous.Observation, old)...)
	}
	if current == nil {
		if previous == nil {
			return p, nil, nil, fmt.Errorf("withdraw requires a previous publication")
		}
		p.Action = "withdraw"
		p.Reason = "operator_requested_withdrawal"
		return p, nil, nil, nil
	}
	a, err := generate(*current)
	if err != nil {
		if previous == nil {
			return p, nil, nil, err
		}
		// A stopped guest, moved IP or invalid scope must not produce new permits.
		// Only the previously validated identity is used for withdrawal commands.
		p.Action = "withdraw"
		p.Reason = "current_observation_not_publishable"
		return p, nil, nil, nil
	}
	p.Action = "publish"
	filterArtifact := "firewall.nft"
	slotCondition := "both anas_incus_lab tables and old INCUS_LAB route must be absent; do not replace unknown rules"
	if previous != nil {
		old := previous.Observation
		if old.Consumer != current.Consumer || old.Resource != current.Resource || old.Project != current.Project || old.Interface != current.Interface || old.DockerContainerID != current.DockerContainerID || old.DockerEndpointID != current.DockerEndpointID || old.GuestBridge != current.GuestBridge || old.GuestSubnet != current.GuestSubnet || old.IngressBridge != current.IngressBridge || old.IngressSubnet != current.IngressSubnet || old.TraefikVeth != current.TraefikVeth || old.TraefikIP != current.TraefikIP || old.IngressGateway != current.IngressGateway || old.TraefikInterface != current.TraefikInterface {
			p.Action = "withdraw"
			p.Reason = "lab_scope_or_topology_changed_requires_new_session"
			return p, nil, nil, nil
		}
		p.Action = "replace"
		filterArtifact = "firewall-replace.nft"
		slotCondition = "previous route and tuple are absent; both owned deny tables remain installed; replace them in one nft transaction"
	}
	p.Steps = append(p.Steps,
		lifecycleStep{Operation: "verify_frozen_auth", Scope: "lab_traefik", Condition: "for forward_auth, confirm the frozen middleware belongs to the configured provider and unauthorized requests fail closed; do not remove middleware to make the probe pass"},
		lifecycleStep{Operation: "reserve_http_host", Scope: "lab_registry", Condition: "reserve the Host and INCUS_LAB route ID; any existing foreign owner is a conflict"},
		lifecycleStep{Operation: "verify_lab_slot", Scope: "lab_host", Condition: slotCondition},
		lifecycleStep{Operation: "add_guest_route", Scope: "traefik_network_namespace", Argv: a.AddRoute, Condition: "no preexisting /32 except an identical route owned by this session; verify selected device and source address"},
		lifecycleStep{Operation: "load_backend_filter", Scope: "lab_host", Artifact: filterArtifact, Condition: "check and load only after current identity is reobserved and IP held; preserve all existing daemon rules"},
		lifecycleStep{Operation: "probe_http_backend", Scope: "traefik_network_namespace", Condition: fmt.Sprintf("verify the expected disposable fixture response from http://%s:%d before its 30-second permit expires", current.GuestIP, current.GuestPort)},
		lifecycleStep{Operation: "publish_http_route", Scope: "lab_traefik", Artifact: "traefik.env", Condition: "render with existing anas-entrypoint.sh into the isolated lab output; verify Host route; never let consumers write this directory"},
	)
	p.OnPublishFailure = cleanupSteps(*current, a)
	record := &publicationRecord{Schema: publicationSchema, Observation: *current, Evidence: evidence}
	return p, record, &a, nil
}
