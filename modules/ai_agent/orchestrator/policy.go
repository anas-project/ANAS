package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// RepoPermission is what Forgejo says a person holds on a repository. The
// values are upstream's own, verified against 15.0.7: a non-collaborator is
// reported as "none" rather than as an error, and an organization owner is
// "owner" rather than "admin".
type RepoPermission string

const (
	PermissionNone  RepoPermission = "none"
	PermissionRead  RepoPermission = "read"
	PermissionWrite RepoPermission = "write"
	PermissionAdmin RepoPermission = "admin"
	PermissionOwner RepoPermission = "owner"
)

// DeriveActions maps a repository permission onto the actions it implies. This
// is the second layer of the decision and the one nobody configures: it follows
// from the access the person already has, so an agent can never do on someone's
// behalf what that person could not do themselves.
func DeriveActions(permission RepoPermission) []Action {
	switch permission {
	case PermissionRead:
		return []Action{ActionReply}
	case PermissionWrite:
		return []Action{ActionReply, ActionPlan}
	case PermissionAdmin, PermissionOwner:
		return []Action{ActionReply, ActionPlan, ActionExecute}
	}
	return nil
}

// capabilityPrefix is how a directory group names an agent capability. The
// groups are projected into Forgejo teams by the identity provider, so what the
// orchestrator reads is a team list; the naming is the contract between them.
const capabilityPrefix = "CAP_ai_agent_"

// TerminalCapability is the group that grants terminal attachment to someone
// who is not an administrator (AGENT-R-057).
const TerminalCapability = capabilityPrefix + "terminal"

// Grant is the deployment-level ceiling one person holds: which runtimes they
// may use at all, and the strongest action they may ask for. It is derived from
// directory-group membership, never written by hand here.
type Grant struct {
	User string
	// Runtimes is the set of runtime ids the person may use. Empty means none,
	// which is a real and common answer, not a missing value.
	Runtimes []string
	// MaxAction is the ceiling. It caps whatever the repository permission
	// would otherwise allow.
	MaxAction Action
	Terminal  bool
	Source    string
	TakenAt   time.Time
}

// AllowsRuntime reports whether the ceiling covers a runtime.
func (g Grant) AllowsRuntime(id string) bool { return contains(g.Runtimes, id) }

// GrantFromTeams reads a person's ceiling out of their Forgejo team names. The
// projection is one-way and lossy on purpose: only the capability groups mean
// anything here, and a team that is not one is ignored rather than guessed at.
func GrantFromTeams(user string, teams []string, registry *Registry, now time.Time) Grant {
	grant := Grant{User: user, Source: "directory groups projected into Forgejo teams", TakenAt: now.UTC()}
	for _, team := range teams {
		name := strings.TrimSpace(team)
		if !strings.HasPrefix(name, capabilityPrefix) {
			continue
		}
		if name == TerminalCapability {
			grant.Terminal = true
			continue
		}
		id := strings.TrimPrefix(name, capabilityPrefix)
		// A group for a runtime this deployment does not run is not an error --
		// the directory serves more than one deployment -- but it grants
		// nothing here.
		if _, enabled := registry.Lookup(id); !enabled {
			continue
		}
		if !grant.AllowsRuntime(id) {
			grant.Runtimes = append(grant.Runtimes, id)
		}
	}
	sort.Strings(grant.Runtimes)
	// Holding any capability group is what makes a person a participant at all.
	// The ceiling itself is the strongest action the deployment is willing to
	// let a group member reach; the repository permission decides how much of
	// it they actually get.
	if len(grant.Runtimes) > 0 {
		grant.MaxAction = ActionExecute
	}
	return grant
}

// Override narrows what a subject may do, scoped as tightly as the entry says.
// An override can only take away: the final action set is an intersection, so
// an entry that names an action the person could not otherwise perform adds
// nothing (AGENT-R-031, AGENT-R-036).
type Override struct {
	ID      int64
	Repo    string
	Issue   int
	Agent   string
	User    string
	Actions []Action
	By      string
	At      time.Time
}

// Matches reports whether an override applies to a request. An empty field
// means "any", so a repository-wide entry is one with no issue, agent or user.
func (o Override) Matches(request Request) bool {
	if o.Repo != "" && o.Repo != request.Repo.String() {
		return false
	}
	if o.Issue != 0 && o.Issue != request.Issue {
		return false
	}
	if o.Agent != "" && o.Agent != request.Agent {
		return false
	}
	if o.User != "" && o.User != request.User {
		return false
	}
	return true
}

// Deny is an immediate veto. It beats every derivation and every override,
// takes effect the moment it is written, and exists so that stopping an agent
// or a person does not have to wait for a directory sync (AGENT-R-033).
type Deny struct {
	ID     int64
	User   string
	Agent  string
	Reason string
	By     string
	At     time.Time
}

// Matches reports whether a veto covers a request. An entry with no user is a
// veto on the agent for everyone; one with no agent is a veto on the person for
// every agent.
func (d Deny) Matches(request Request) bool {
	if d.User != "" && d.User != request.User {
		return false
	}
	if d.Agent != "" && d.Agent != request.Agent {
		return false
	}
	return d.User != "" || d.Agent != ""
}

// Request is one authorization question.
type Request struct {
	User   string
	Repo   Repo
	Issue  int
	Agent  string
	Action Action
	// Source says where the request came from -- a label, a comment command, a
	// form submission -- and is recorded so an audit can tell an approval typed
	// by a person from one implied by a template.
	Source string
}

// Decision is the answer, and it is recorded whether it was yes or no. An audit
// that kept only the approvals would be useless for the question audits
// actually ask, which is who was refused and why (AGENT-R-034).
type Decision struct {
	Allowed bool
	Request Request
	// Reason is written for the person who asked, not for a log reader.
	Reason string
	// Basis names every input that produced the answer, so a decision can be
	// explained later without re-deriving it from state that has since changed.
	Basis []string
	At    time.Time
}

func (d Decision) auditRecord() AuditRecord {
	verdict := "denied"
	if d.Allowed {
		verdict = "allowed"
	}
	return AuditRecord{
		At: d.At, Subject: d.Request.User, Source: d.Request.Source,
		Action: string(d.Request.Action), Decision: verdict,
		Reason: d.Reason + " [" + strings.Join(d.Basis, "; ") + "]",
		Repo:   d.Request.Repo.String(), Issue: d.Request.Issue,
	}
}

// PolicyStore is the durable half of the decision.
type PolicyStore interface {
	Grants(ctx context.Context) ([]Grant, error)
	SaveGrant(ctx context.Context, grant Grant) error
	Overrides(ctx context.Context, repo Repo) ([]Override, error)
	SaveOverride(ctx context.Context, override Override) error
	Denies(ctx context.Context) ([]Deny, error)
	SaveDeny(ctx context.Context, deny Deny) error
	RemoveDeny(ctx context.Context, user, agent string) error
	AppendAudit(ctx context.Context, record AuditRecord) error
}

// RepoSettings is the per-repository policy the decision consults.
type RepoSettings struct {
	Enabled bool
	// SyncFromRepoPermission is the whole-of-derivation switch. Turning it off
	// means the repository's own permissions grant nothing and only explicit
	// overrides do (AGENT-R-031).
	SyncFromRepoPermission bool
	AllowDirectCommit      bool
}

// PermissionReader answers what Forgejo says about a person.
type PermissionReader interface {
	RepoPermission(ctx context.Context, repo Repo, user string) (RepoPermission, error)
	UserTeams(ctx context.Context, user string) ([]string, error)
}

// PolicyEngine answers authorization questions and records every answer.
type PolicyEngine struct {
	Store       PolicyStore
	Permissions PermissionReader
	Registry    *Registry
	Settings    func(Repo) RepoSettings
	Now         func() time.Time
	// SnapshotTTL is how long a person's directory-derived ceiling is reused
	// before it is read again. It is a cache of a slow, remote answer, not a
	// source of truth: the deny table is consulted live on every decision so a
	// veto never waits for it to expire.
	SnapshotTTL time.Duration

	mu        sync.RWMutex
	snapshots map[string]Grant
}

func (p *PolicyEngine) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *PolicyEngine) ttl() time.Duration {
	if p.SnapshotTTL > 0 {
		return p.SnapshotTTL
	}
	return 5 * time.Minute
}

// Decide answers one request and writes the answer to the audit before
// returning it. The audit write is part of the decision, not a side effect of
// it: a decision that could not be recorded is refused, because an
// unrecordable approval is indistinguishable from one that never happened.
func (p *PolicyEngine) Decide(ctx context.Context, request Request) (Decision, error) {
	decision := p.evaluate(ctx, request)
	if err := p.Store.AppendAudit(ctx, decision.auditRecord()); err != nil {
		return Decision{}, fmt.Errorf("record the authorization decision: %w", err)
	}
	return decision, nil
}

func (p *PolicyEngine) evaluate(ctx context.Context, request Request) Decision {
	decision := Decision{Request: request, At: p.now()}
	deny := func(reason string, basis ...string) Decision {
		decision.Allowed, decision.Reason, decision.Basis = false, reason, basis
		return decision
	}

	settings := RepoSettings{}
	if p.Settings != nil {
		settings = p.Settings(request.Repo)
	}
	// A repository nobody enabled is not a refusal to explain to anyone -- the
	// events were already dropped at ingress (AGENT-R-030).
	if !settings.Enabled {
		return deny("agents are not enabled for this repository", "repository settings")
	}

	// The veto is consulted first and is read live, so revoking access takes
	// effect on the next request rather than at the next directory sync.
	denies, err := p.Store.Denies(ctx)
	if err != nil {
		return deny("the veto list could not be read, so the request is refused",
			"failing closed: "+err.Error())
	}
	for _, entry := range denies {
		if entry.Matches(request) {
			return deny("an explicit veto is in force: "+entry.Reason,
				"veto set by "+entry.By+" at "+entry.At.UTC().Format(time.RFC3339))
		}
	}

	if request.Agent != "" {
		if _, enabled := p.Registry.Lookup(request.Agent); !enabled {
			return deny("that agent runtime is not enabled in this deployment", "runtime registry")
		}
	}

	grant, err := p.grant(ctx, request.User)
	if err != nil {
		return deny("your capability groups could not be read, so the request is refused",
			"failing closed: "+err.Error())
	}
	if request.Agent != "" && !grant.AllowsRuntime(request.Agent) {
		return deny("you are not in the capability group for "+request.Agent,
			"needs the directory group "+capabilityPrefix+request.Agent)
	}
	if !actionWithin(request.Action, grant.MaxAction) {
		return deny("your capability groups do not reach "+string(request.Action),
			"deployment ceiling: "+string(grant.MaxAction))
	}

	derived := []Action{}
	basis := []string{"capability ceiling " + string(grant.MaxAction)}
	if settings.SyncFromRepoPermission {
		permission, err := p.Permissions.RepoPermission(ctx, request.Repo, request.User)
		if err != nil {
			return deny("your repository permission could not be read, so the request is refused",
				"failing closed: "+err.Error())
		}
		derived = DeriveActions(permission)
		basis = append(basis, "repository permission "+string(permission))
	} else {
		basis = append(basis, "repository permission derivation is switched off")
	}

	overrides, err := p.Store.Overrides(ctx, request.Repo)
	if err != nil {
		return deny("the policy overrides could not be read, so the request is refused",
			"failing closed: "+err.Error())
	}
	allowed := derived
	for _, override := range overrides {
		if !override.Matches(request) {
			continue
		}
		// An override intersects rather than replaces: it is a way to take
		// away, never a way to grant something the person could not otherwise
		// do (AGENT-R-036).
		allowed = intersectActions(allowed, override.Actions)
		basis = append(basis, fmt.Sprintf("override by %s limiting to %s",
			override.By, joinActions(override.Actions)))
	}

	if !containsAction(allowed, request.Action) {
		return deny("your permission on this repository does not allow "+string(request.Action),
			basis...)
	}
	decision.Allowed, decision.Reason, decision.Basis = true, "allowed", basis
	return decision
}

// grant returns a person's ceiling, reading it again when the snapshot has
// aged out.
func (p *PolicyEngine) grant(ctx context.Context, user string) (Grant, error) {
	p.mu.RLock()
	cached, ok := p.snapshots[user]
	p.mu.RUnlock()
	if ok && p.now().Sub(cached.TakenAt) < p.ttl() {
		return cached, nil
	}
	teams, err := p.Permissions.UserTeams(ctx, user)
	if err != nil {
		return Grant{}, err
	}
	grant := GrantFromTeams(user, teams, p.Registry, p.now())
	p.mu.Lock()
	if p.snapshots == nil {
		p.snapshots = map[string]Grant{}
	}
	p.snapshots[user] = grant
	p.mu.Unlock()
	if err := p.Store.SaveGrant(ctx, grant); err != nil {
		return Grant{}, err
	}
	return grant, nil
}

// Veto adds an immediate refusal and drops any cached ceiling that would
// contradict it. It backs the `agent-grant deny` command.
func (p *PolicyEngine) Veto(ctx context.Context, deny Deny) error {
	if deny.User == "" && deny.Agent == "" {
		return fmt.Errorf("a veto must name a person, an agent, or both")
	}
	if deny.At.IsZero() {
		deny.At = p.now()
	}
	if err := p.Store.SaveDeny(ctx, deny); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.snapshots, deny.User)
	p.mu.Unlock()
	return p.Store.AppendAudit(ctx, AuditRecord{
		At: deny.At, Subject: deny.User, Source: "veto", Action: "deny",
		Decision: "recorded", Reason: deny.Reason + " [set by " + deny.By + "]",
	})
}

// LiftVeto removes one.
func (p *PolicyEngine) LiftVeto(ctx context.Context, user, agent, by string) error {
	if err := p.Store.RemoveDeny(ctx, user, agent); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.snapshots, user)
	p.mu.Unlock()
	return p.Store.AppendAudit(ctx, AuditRecord{
		At: p.now(), Subject: user, Source: "veto", Action: "allow",
		Decision: "recorded", Reason: "veto lifted by " + by,
	})
}

// Recheck asks the same question again immediately before a job starts. It is a
// separate method so the call site reads as what it is: an approval can be
// minutes or days old, group membership can have been withdrawn since, and a
// veto can have been written in between (AGENT-R-035).
func (p *PolicyEngine) Recheck(ctx context.Context, request Request) (Decision, error) {
	// The cached ceiling is deliberately dropped: this is the moment the answer
	// has to be current rather than fast.
	p.mu.Lock()
	delete(p.snapshots, request.User)
	p.mu.Unlock()
	request.Source = request.Source + " (rechecked before the job started)"
	return p.Decide(ctx, request)
}

// RefreshSnapshots re-reads the ceiling of everyone currently cached. It runs
// on the ordinary sweep so a directory change reaches the decision without
// waiting for each person's own snapshot to expire.
func (p *PolicyEngine) RefreshSnapshots(ctx context.Context) error {
	p.mu.RLock()
	users := make([]string, 0, len(p.snapshots))
	for user := range p.snapshots {
		users = append(users, user)
	}
	p.mu.RUnlock()
	sort.Strings(users)
	for _, user := range users {
		p.mu.Lock()
		delete(p.snapshots, user)
		p.mu.Unlock()
		if _, err := p.grant(ctx, user); err != nil {
			return err
		}
	}
	return nil
}

// actionRank orders the actions by strength, so a ceiling can be compared to a
// request without enumerating pairs.
func actionRank(action Action) int {
	switch action {
	case ActionReply:
		return 1
	case ActionPlan:
		return 2
	case ActionExecute:
		return 3
	}
	return 0
}

func actionWithin(request, ceiling Action) bool {
	return actionRank(ceiling) >= actionRank(request) && actionRank(request) > 0
}

func containsAction(actions []Action, wanted Action) bool {
	for _, action := range actions {
		if action == wanted {
			return true
		}
	}
	return false
}

func intersectActions(left, right []Action) []Action {
	var out []Action
	for _, action := range left {
		if containsAction(right, action) {
			out = append(out, action)
		}
	}
	return out
}

func joinActions(actions []Action) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, string(action))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}
