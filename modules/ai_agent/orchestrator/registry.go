package main

import (
	"fmt"
	"sort"
	"strings"
)

// Action is one class of thing an agent may be asked to do. The three values
// are the whole vocabulary: the permission model (AGENT-R-031) derives a set of
// them from a Forgejo repository permission, and every command and label maps
// onto one of them.
type Action string

const (
	ActionReply   Action = "reply"
	ActionPlan    Action = "plan"
	ActionExecute Action = "execute"
	// ActionHost is not a permission; it records that a runtime is able to
	// chair a multi-agent round table. It lives here because the registry is
	// the only place allowed to describe what a runtime can do.
	ActionHost Action = "host"
)

// Runtime is a registry entry: everything the control plane is allowed to know
// about an agent runtime. Nothing outside this file may name a runtime, which
// is what makes "add a runtime" a registry edit plus an adapter rather than a
// change to the state machine (AGENT-R-059, AGENT-R-060).
type Runtime struct {
	ID      string
	Account string
	// Adapter names the process protocol implementation. It is looked up in the
	// adapter table, never switched on.
	Adapter string
	// Image is the pinned SHA-256 fingerprint of the runtime image. It is empty
	// in the built-in table and filled in from configuration, because which
	// image an operator approved is a deployment fact, not a product fact.
	Image        string
	CLIVersion   string
	AuthMode     string
	CredentialID string
	Models       []string
	DefaultModel string
	// EffortLevels holds this runtime's own native names for thinking effort.
	// There is deliberately no shared vocabulary: a shared one would invent
	// levels a runtime does not have and imply that "high" means the same thing
	// everywhere (AGENT-R-061).
	EffortLevels  []string
	DefaultEffort string
	// EffortTiers maps the abstract low/medium/high used for deployment-wide
	// defaults onto this runtime's native values. Display and audit always fall
	// back to the native value.
	EffortTiers  map[string]string
	Capabilities []Action
	// ResumableSession says whether the runtime can continue a session from its
	// own state directory. A runtime that cannot is not broken; it always takes
	// the documented rebuild path (AGENT-R-054).
	ResumableSession bool
	MaxConcurrent    int
	Status           string
}

// Supports reports whether this runtime can perform an action at all. It is a
// capability question, not an authorization question; authorization is decided
// separately and both have to say yes.
func (r Runtime) Supports(action Action) bool {
	for _, candidate := range r.Capabilities {
		if candidate == action {
			return true
		}
	}
	return false
}

// ValidateEffort accepts only this runtime's native effort values and, when it
// rejects one, says what the alternatives are. A runtime that does not grade
// effort at all accepts the empty value and nothing else.
func (r Runtime) ValidateEffort(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(r.EffortLevels) == 0 {
		return fmt.Errorf("runtime %q does not grade thinking effort", r.ID)
	}
	for _, level := range r.EffortLevels {
		if level == value {
			return nil
		}
	}
	return fmt.Errorf("runtime %q rejects thinking effort %q; allowed values are %s",
		r.ID, value, strings.Join(r.EffortLevels, ", "))
}

// ValidateModel works the same way for models.
func (r Runtime) ValidateModel(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, model := range r.Models {
		if model == value {
			return nil
		}
	}
	return fmt.Errorf("runtime %q rejects model %q; allowed values are %s",
		r.ID, value, strings.Join(r.Models, ", "))
}

// CapabilityGroup is the directory group that grants deployment-level use of
// this runtime. It is generated, never written by hand, so a new registry entry
// cannot be forgotten in the directory (AGENT-R-032, AGENT-R-059).
func (r Runtime) CapabilityGroup() string { return "CAP_ai_agent_" + r.ID }

// Registry is the set of runtimes a deployment has turned on.
type Registry struct {
	entries []Runtime
	byID    map[string]Runtime
}

// builtinRuntimes is the shipped catalogue. Adding a runtime here plus an
// adapter is the whole extension surface; no other file in this package names
// any of these ids.
var builtinRuntimes = []Runtime{
	{
		ID: "codex", Account: "agent-codex", Adapter: "cli_jsonl",
		CLIVersion: "0.48.0", AuthMode: "api_key", CredentialID: "ai_agent.codex_api_key",
		Models:       []string{"gpt-5.1-codex", "gpt-5.1-codex-mini"},
		DefaultModel: "gpt-5.1-codex",
		// Codex grades effort with its own four names; they are not the same
		// scale as any other runtime's and are never translated.
		EffortLevels:     []string{"low", "medium", "high", "xhigh"},
		DefaultEffort:    "medium",
		EffortTiers:      map[string]string{"low": "low", "medium": "medium", "high": "high"},
		Capabilities:     []Action{ActionReply, ActionPlan, ActionExecute, ActionHost},
		ResumableSession: true, MaxConcurrent: 2, Status: "enabled",
	},
	{
		ID: "claude_code", Account: "agent-claude-code", Adapter: "cli_jsonl",
		CLIVersion: "2.4.0", AuthMode: "api_key", CredentialID: "ai_agent.claude_code_api_key",
		Models:           []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"},
		DefaultModel:     "claude-sonnet-5",
		EffortLevels:     []string{"think", "megathink", "ultrathink"},
		DefaultEffort:    "think",
		EffortTiers:      map[string]string{"low": "think", "medium": "megathink", "high": "ultrathink"},
		Capabilities:     []Action{ActionReply, ActionPlan, ActionExecute, ActionHost},
		ResumableSession: true, MaxConcurrent: 2, Status: "enabled",
	},
	{
		ID: "pi", Account: "agent-pi", Adapter: "http_sdk",
		CLIVersion: "0.9.0", AuthMode: "api_key", CredentialID: "ai_agent.pi_api_key",
		Models:       []string{"pi-1"},
		DefaultModel: "pi-1",
		// Pi exposes no effort dimension. The issue form template omits the
		// field entirely rather than offering a level that does not exist.
		EffortLevels:     nil,
		Capabilities:     []Action{ActionReply, ActionPlan},
		ResumableSession: false, MaxConcurrent: 1, Status: "enabled",
	},
}

// NewRegistry selects the requested runtimes from the built-in catalogue and
// pins each one to an approved image fingerprint. A runtime without a pinned
// image is refused rather than started from a moving tag (AGENT-R-001).
func NewRegistry(requested []string, images map[string]string) (*Registry, error) {
	catalogue := map[string]Runtime{}
	for _, entry := range builtinRuntimes {
		catalogue[entry.ID] = entry
	}
	registry := &Registry{byID: map[string]Runtime{}}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		entry, ok := catalogue[id]
		if !ok {
			return nil, fmt.Errorf("unknown agent runtime %q; known runtimes are %s", id, strings.Join(builtinRuntimeIDs(), ", "))
		}
		if _, duplicate := registry.byID[id]; duplicate {
			return nil, fmt.Errorf("agent runtime %q is enabled more than once", id)
		}
		image := strings.TrimSpace(images[id])
		if image == "" {
			return nil, fmt.Errorf("agent runtime %q has no pinned image fingerprint", id)
		}
		entry.Image = image
		registry.byID[id] = entry
		registry.entries = append(registry.entries, entry)
	}
	sort.Slice(registry.entries, func(i, j int) bool { return registry.entries[i].ID < registry.entries[j].ID })
	return registry, nil
}

func builtinRuntimeIDs() []string {
	ids := make([]string, 0, len(builtinRuntimes))
	for _, entry := range builtinRuntimes {
		ids = append(ids, entry.ID)
	}
	sort.Strings(ids)
	return ids
}

// Entries returns the enabled runtimes in a stable order, so generated labels,
// templates and documentation do not churn between reconciliations.
func (r *Registry) Entries() []Runtime {
	out := make([]Runtime, len(r.entries))
	copy(out, r.entries)
	return out
}

func (r *Registry) Len() int { return len(r.entries) }

// Lookup finds an enabled runtime by id.
func (r *Registry) Lookup(id string) (Runtime, bool) {
	entry, ok := r.byID[strings.TrimSpace(id)]
	return entry, ok
}

// ByAccount finds the runtime a Forgejo account belongs to. Ingress uses it to
// recognise the module's own writes (AGENT-R-014).
func (r *Registry) ByAccount(account string) (Runtime, bool) {
	account = strings.TrimSpace(account)
	for _, entry := range r.entries {
		if entry.Account == account {
			return entry, true
		}
	}
	return Runtime{}, false
}

// Accounts lists every Forgejo account this deployment's agents act as.
func (r *Registry) Accounts() []string {
	accounts := make([]string, 0, len(r.entries))
	for _, entry := range r.entries {
		accounts = append(accounts, entry.Account)
	}
	return accounts
}

// CapabilityGroups lists the directory groups that grant use of the enabled
// runtimes.
func (r *Registry) CapabilityGroups() []string {
	groups := make([]string, 0, len(r.entries))
	for _, entry := range r.entries {
		groups = append(groups, entry.CapabilityGroup())
	}
	return groups
}

// Hosts lists the runtimes allowed to chair a round table.
func (r *Registry) Hosts() []Runtime {
	var hosts []Runtime
	for _, entry := range r.entries {
		if entry.Supports(ActionHost) {
			hosts = append(hosts, entry)
		}
	}
	return hosts
}
