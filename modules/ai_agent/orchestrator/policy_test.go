package main

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakePermissions answers the two questions the engine asks Forgejo.
type fakePermissions struct {
	permissions map[string]RepoPermission
	teams       map[string][]string
	reads       int
	failOn      map[string]error
}

func (f *fakePermissions) RepoPermission(_ context.Context, repo Repo, user string) (RepoPermission, error) {
	if err := f.failOn["RepoPermission"]; err != nil {
		return "", err
	}
	permission, ok := f.permissions[repo.String()+"|"+user]
	if !ok {
		return PermissionNone, nil
	}
	return permission, nil
}

func (f *fakePermissions) UserTeams(_ context.Context, user string) ([]string, error) {
	if err := f.failOn["UserTeams"]; err != nil {
		return nil, err
	}
	f.reads++
	return f.teams[user], nil
}

func testEngine(t *testing.T) (*PolicyEngine, *fakePermissions, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	permissions := &fakePermissions{
		permissions: map[string]RepoPermission{
			"anas-project/ANAS|alice": PermissionAdmin,
			"anas-project/ANAS|bob":   PermissionWrite,
			"anas-project/ANAS|carol": PermissionRead,
		},
		teams: map[string][]string{
			"alice": {"Owners", capabilityPrefix + "codex", capabilityPrefix + "claude_code"},
			"bob":   {capabilityPrefix + "codex"},
			"carol": {capabilityPrefix + "codex"},
			"dave":  {"Reviewers"},
		},
		failOn: map[string]error{},
	}
	engine := &PolicyEngine{
		Store: store, Permissions: permissions, Registry: testRegistry(t),
		Settings: func(Repo) RepoSettings {
			return RepoSettings{Enabled: true, SyncFromRepoPermission: true}
		},
		Now: func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) },
	}
	return engine, permissions, store
}

func ask(t *testing.T, engine *PolicyEngine, user string, action Action) Decision {
	t.Helper()
	decision, err := engine.Decide(context.Background(), Request{
		User: user, Repo: testRepo(t), Issue: 7, Agent: "codex", Action: action, Source: "label",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return decision
}

// AGENT-R-031: the action set follows from the repository permission the person
// already holds -- read replies, write plans, admin executes.
func TestActionsAreDerivedFromRepositoryPermission(t *testing.T) {
	engine, _, _ := testEngine(t)
	for _, testCase := range []struct {
		user    string
		allowed []Action
		refused []Action
	}{
		{"alice", []Action{ActionReply, ActionPlan, ActionExecute}, nil},
		{"bob", []Action{ActionReply, ActionPlan}, []Action{ActionExecute}},
		{"carol", []Action{ActionReply}, []Action{ActionPlan, ActionExecute}},
	} {
		for _, action := range testCase.allowed {
			if decision := ask(t, engine, testCase.user, action); !decision.Allowed {
				t.Errorf("%s was refused %s: %s", testCase.user, action, decision.Reason)
			}
		}
		for _, action := range testCase.refused {
			decision := ask(t, engine, testCase.user, action)
			if decision.Allowed {
				t.Errorf("%s was allowed %s", testCase.user, action)
			}
			if decision.Reason == "" {
				t.Errorf("%s was refused %s with no reason", testCase.user, action)
			}
		}
	}
}

func TestNonCollaboratorGetsNothing(t *testing.T) {
	engine, permissions, _ := testEngine(t)
	permissions.teams["stranger"] = []string{capabilityPrefix + "codex"}
	if decision := ask(t, engine, "stranger", ActionReply); decision.Allowed {
		t.Fatal("someone with no permission on the repository was allowed to reply")
	}
}

// AGENT-R-032: the capability group is a ceiling. Repository permission alone
// grants nothing without it, and the ceiling caps what the permission implies.
func TestCapabilityGroupsAreACeiling(t *testing.T) {
	engine, permissions, _ := testEngine(t)
	// dave is in no capability group at all, whatever his repository rights.
	permissions.permissions["anas-project/ANAS|dave"] = PermissionAdmin
	decision := ask(t, engine, "dave", ActionReply)
	if decision.Allowed {
		t.Fatal("a repository administrator with no capability group was allowed")
	}
	if !strings.Contains(decision.Reason, "capability group") {
		t.Fatalf("reason = %q, want it to name the missing group", decision.Reason)
	}
	// alice holds codex but not pi.
	byRuntime, err := engine.Decide(context.Background(), Request{
		User: "alice", Repo: testRepo(t), Issue: 7, Agent: "pi", Action: ActionReply, Source: "label",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if byRuntime.Allowed {
		t.Fatal("alice was allowed a runtime she holds no group for")
	}
	if !strings.Contains(byRuntime.Reason, "pi") {
		t.Fatalf("reason = %q, want it to name the runtime", byRuntime.Reason)
	}
}

// AGENT-R-031: turning the derivation off means repository permissions grant
// nothing and only explicit overrides do.
func TestDerivationCanBeSwitchedOffWholesale(t *testing.T) {
	engine, _, _ := testEngine(t)
	engine.Settings = func(Repo) RepoSettings {
		return RepoSettings{Enabled: true, SyncFromRepoPermission: false}
	}
	if decision := ask(t, engine, "alice", ActionReply); decision.Allowed {
		t.Fatal("an administrator was allowed although derivation is switched off")
	}
}

// AGENT-R-036: an override narrows and never widens.
func TestOverridesCanOnlyNarrow(t *testing.T) {
	engine, _, store := testEngine(t)
	ctx := context.Background()

	if err := store.SaveOverride(ctx, Override{
		Repo: testRepo(t).String(), User: "alice", Actions: []Action{ActionReply}, By: "admin",
	}); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}
	if decision := ask(t, engine, "alice", ActionExecute); decision.Allowed {
		t.Fatal("an override did not narrow an administrator down")
	}
	if decision := ask(t, engine, "alice", ActionReply); !decision.Allowed {
		t.Fatalf("the override removed more than it says: %s", decision.Reason)
	}

	// An override that names an action the person could not otherwise perform
	// adds nothing.
	if err := store.SaveOverride(ctx, Override{
		Repo: testRepo(t).String(), User: "carol",
		Actions: []Action{ActionReply, ActionPlan, ActionExecute}, By: "admin",
	}); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}
	if decision := ask(t, engine, "carol", ActionExecute); decision.Allowed {
		t.Fatal("an override granted an action the repository permission does not")
	}
}

// An override can be scoped to one issue, and then it must not affect another.
func TestOverrideScopeIsRespected(t *testing.T) {
	engine, _, store := testEngine(t)
	ctx := context.Background()
	if err := store.SaveOverride(ctx, Override{
		Repo: testRepo(t).String(), Issue: 7, User: "alice",
		Actions: []Action{ActionReply}, By: "admin",
	}); err != nil {
		t.Fatalf("SaveOverride: %v", err)
	}
	if decision := ask(t, engine, "alice", ActionExecute); decision.Allowed {
		t.Fatal("the issue-scoped override did not apply to its own issue")
	}
	elsewhere, err := engine.Decide(ctx, Request{
		User: "alice", Repo: testRepo(t), Issue: 8, Agent: "codex", Action: ActionExecute, Source: "label",
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !elsewhere.Allowed {
		t.Fatalf("the issue-scoped override leaked to another issue: %s", elsewhere.Reason)
	}
}

// AGENT-R-033: a veto beats everything and takes effect immediately, without
// waiting for a cached ceiling to expire.
func TestVetoTakesEffectImmediately(t *testing.T) {
	engine, _, _ := testEngine(t)
	ctx := context.Background()
	if decision := ask(t, engine, "alice", ActionExecute); !decision.Allowed {
		t.Fatalf("alice was refused before any veto: %s", decision.Reason)
	}
	if err := engine.Veto(ctx, Deny{User: "alice", Reason: "offboarding", By: "admin"}); err != nil {
		t.Fatalf("Veto: %v", err)
	}
	decision := ask(t, engine, "alice", ActionExecute)
	if decision.Allowed {
		t.Fatal("a veto did not take effect on the next request")
	}
	if !strings.Contains(decision.Reason, "offboarding") {
		t.Fatalf("reason = %q, want the veto's own reason", decision.Reason)
	}
	// Lifting it restores the previous answer.
	if err := engine.LiftVeto(ctx, "alice", "", "admin"); err != nil {
		t.Fatalf("LiftVeto: %v", err)
	}
	if decision := ask(t, engine, "alice", ActionExecute); !decision.Allowed {
		t.Fatalf("lifting the veto did not restore access: %s", decision.Reason)
	}
}

// A veto naming only an agent stops that agent for everyone.
func TestVetoOnAnAgentAppliesToEveryone(t *testing.T) {
	engine, _, _ := testEngine(t)
	if err := engine.Veto(context.Background(), Deny{
		Agent: "codex", Reason: "runtime incident", By: "admin",
	}); err != nil {
		t.Fatalf("Veto: %v", err)
	}
	for _, user := range []string{"alice", "bob", "carol"} {
		if decision := ask(t, engine, user, ActionReply); decision.Allowed {
			t.Errorf("%s was allowed to use a vetoed agent", user)
		}
	}
}

func TestVetoMustNameSomething(t *testing.T) {
	engine, _, _ := testEngine(t)
	if err := engine.Veto(context.Background(), Deny{Reason: "everything", By: "admin"}); err == nil {
		t.Fatal("a veto naming neither a person nor an agent was accepted")
	}
}

// AGENT-R-034: every decision is recorded, including the refusals, with who
// asked, where from, what for, the verdict and the reasoning.
func TestEveryDecisionIsAudited(t *testing.T) {
	engine, _, store := testEngine(t)
	ctx := context.Background()
	ask(t, engine, "alice", ActionExecute)
	ask(t, engine, "carol", ActionExecute)

	records, err := store.Audit(ctx, 10)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("audit holds %d records, want one per decision", len(records))
	}
	verdicts := map[string]AuditRecord{}
	for _, record := range records {
		verdicts[record.Subject] = record
		if record.At.IsZero() || record.Source == "" || record.Action == "" || record.Reason == "" {
			t.Errorf("incomplete audit record: %+v", record)
		}
		if record.Repo != testRepo(t).String() || record.Issue != 7 {
			t.Errorf("audit record does not say where: %+v", record)
		}
	}
	if verdicts["alice"].Decision != "allowed" {
		t.Errorf("alice's approval was recorded as %q", verdicts["alice"].Decision)
	}
	if verdicts["carol"].Decision != "denied" {
		t.Errorf("carol's refusal was recorded as %q", verdicts["carol"].Decision)
	}
}

// A decision that cannot be recorded is not a decision: an approval nobody can
// audit is indistinguishable from one that never happened.
func TestUnrecordableDecisionIsRefused(t *testing.T) {
	engine, _, _ := testEngine(t)
	engine.Store = failingAudit{engine.Store}
	if _, err := engine.Decide(context.Background(), Request{
		User: "alice", Repo: testRepo(t), Agent: "codex", Action: ActionReply, Source: "label",
	}); err == nil {
		t.Fatal("a decision was returned although it could not be recorded")
	}
}

type failingAudit struct{ PolicyStore }

func (failingAudit) AppendAudit(context.Context, AuditRecord) error {
	return errors.New("the audit store is unavailable")
}

// Every read the decision depends on fails closed: an unavailable input is
// never read as permission.
func TestEveryUnavailableInputFailsClosed(t *testing.T) {
	for _, call := range []string{"RepoPermission", "UserTeams"} {
		engine, permissions, _ := testEngine(t)
		permissions.failOn[call] = errors.New("forgejo is down")
		decision := ask(t, engine, "alice", ActionReply)
		if decision.Allowed {
			t.Errorf("a failing %s was read as permission", call)
		}
		if !strings.Contains(strings.Join(decision.Basis, " "), "failing closed") {
			t.Errorf("the refusal for %s does not say it failed closed: %v", call, decision.Basis)
		}
	}
}

// AGENT-R-030: a repository nobody enabled decides nothing.
func TestDisabledRepositoryAllowsNothing(t *testing.T) {
	engine, _, _ := testEngine(t)
	engine.Settings = func(Repo) RepoSettings { return RepoSettings{Enabled: false} }
	if decision := ask(t, engine, "alice", ActionReply); decision.Allowed {
		t.Fatal("a repository with agents disabled allowed a request")
	}
}

// AGENT-R-035: the question is asked again before the job starts, against
// current state rather than the cached ceiling.
func TestRecheckReadsCurrentStateNotTheSnapshot(t *testing.T) {
	engine, permissions, _ := testEngine(t)
	ctx := context.Background()
	request := Request{User: "alice", Repo: testRepo(t), Issue: 7, Agent: "codex",
		Action: ActionExecute, Source: "approval"}

	if decision, err := engine.Decide(ctx, request); err != nil || !decision.Allowed {
		t.Fatalf("the approval was refused: %v %v", decision.Reason, err)
	}
	readsAfterApproval := permissions.reads

	// The group is withdrawn between the approval and the job.
	permissions.teams["alice"] = []string{"Owners"}

	// An ordinary decision would still be inside the snapshot window.
	if decision, err := engine.Decide(ctx, request); err != nil || !decision.Allowed {
		t.Fatalf("the cached ceiling was expected to still allow this: %v %v", decision.Reason, err)
	}
	if permissions.reads != readsAfterApproval {
		t.Fatal("the ordinary decision re-read the ceiling, so this test proves nothing about the recheck")
	}

	decision, err := engine.Recheck(ctx, request)
	if err != nil {
		t.Fatalf("Recheck: %v", err)
	}
	if decision.Allowed {
		t.Fatal("the recheck honoured a withdrawn capability group")
	}
	if !strings.Contains(decision.Request.Source, "rechecked") {
		t.Fatalf("the audit does not distinguish the recheck: %q", decision.Request.Source)
	}
}

// The snapshot is a cache of a slow remote answer, and the sweep refreshes it
// so a directory change lands without waiting for each person to expire.
func TestSnapshotRefreshPicksUpDirectoryChanges(t *testing.T) {
	engine, permissions, _ := testEngine(t)
	ctx := context.Background()
	ask(t, engine, "bob", ActionReply)
	permissions.teams["bob"] = nil

	if decision := ask(t, engine, "bob", ActionReply); !decision.Allowed {
		t.Fatal("the snapshot was not used at all")
	}
	if err := engine.RefreshSnapshots(ctx); err != nil {
		t.Fatalf("RefreshSnapshots: %v", err)
	}
	if decision := ask(t, engine, "bob", ActionReply); decision.Allowed {
		t.Fatal("the refresh did not pick up the withdrawn group")
	}
}

func TestGrantFromTeamsReadsOnlyCapabilityGroups(t *testing.T) {
	registry := testRegistry(t)
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	grant := GrantFromTeams("alice", []string{
		"Owners", "Reviewers", capabilityPrefix + "codex", TerminalCapability,
		capabilityPrefix + "a_runtime_this_deployment_does_not_run",
	}, registry, now)

	if !grant.AllowsRuntime("codex") {
		t.Fatal("the codex group was not read")
	}
	if grant.AllowsRuntime("a_runtime_this_deployment_does_not_run") {
		t.Fatal("a group for a runtime this deployment does not run granted something")
	}
	if !grant.Terminal {
		t.Fatal("the terminal capability group was not read")
	}
	if grant.MaxAction != ActionExecute {
		t.Fatalf("ceiling = %q", grant.MaxAction)
	}

	none := GrantFromTeams("dave", []string{"Reviewers"}, registry, now)
	if len(none.Runtimes) != 0 || none.MaxAction != "" || none.Terminal {
		t.Fatalf("someone in no capability group holds %+v", none)
	}
}

// An upstream permission value nobody has seen before grants nothing rather
// than being guessed at.
func TestUnknownPermissionGrantsNothing(t *testing.T) {
	if actions := DeriveActions(RepoPermission("something-new")); len(actions) != 0 {
		t.Fatalf("an unrecognised permission derived %v", actions)
	}
	if actions := DeriveActions(PermissionNone); len(actions) != 0 {
		t.Fatalf("PermissionNone derived %v", actions)
	}
	if actions := DeriveActions(PermissionOwner); !containsAction(actions, ActionExecute) {
		t.Fatalf("an owner cannot execute: %v", actions)
	}
}

// TestPolicyAgainstLiveForgejo checks the two readings the decision depends on
// against a real instance: what Forgejo calls a permission level, and whether a
// person's capability groups can be read as another user. Both were probed
// before the engine was written, and this keeps them from drifting.
//
//	AI_AGENT_TEST_FORGEJO_URL=... AI_AGENT_TEST_FORGEJO_USER=... \
//	AI_AGENT_TEST_FORGEJO_PASSWORD=... AI_AGENT_TEST_FORGEJO_TOKEN=... \
//	AI_AGENT_TEST_FORGEJO_ORG=... AI_AGENT_TEST_FORGEJO_REPO_IN=... \
//	  go test ./modules/ai_agent/orchestrator -run TestPolicyAgainstLiveForgejo
func TestPolicyAgainstLiveForgejo(t *testing.T) {
	baseURL := os.Getenv("AI_AGENT_TEST_FORGEJO_URL")
	admin := os.Getenv("AI_AGENT_TEST_FORGEJO_USER")
	password := os.Getenv("AI_AGENT_TEST_FORGEJO_PASSWORD")
	token := os.Getenv("AI_AGENT_TEST_FORGEJO_TOKEN")
	org := os.Getenv("AI_AGENT_TEST_FORGEJO_ORG")
	repoName := os.Getenv("AI_AGENT_TEST_FORGEJO_REPO_IN")
	if baseURL == "" || admin == "" || password == "" || token == "" || org == "" || repoName == "" {
		t.Skip("set the AI_AGENT_TEST_FORGEJO_* variables to run the live policy tests")
	}
	ctx := context.Background()
	repo, err := ParseRepo(org + "/" + repoName)
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	redactor := NewRedactor(password, token)
	reader := NewPermissionReader(baseURL, admin, password, redactor)
	issues := NewForgejoIssues(baseURL, token, redactor).(issuesClient)
	adminAPI := NewForgejoAdmin(baseURL, admin, password, redactor)

	subject := "policy-" + strconv.FormatInt(time.Now().Unix(), 10)
	if _, err := adminAPI.EnsureUser(ctx, subject, subject+"@localhost.invalid"); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	// A person with no relationship to the repository reads as "none" rather
	// than as an error, which is what lets the engine treat absence as no
	// permission instead of as a failure.
	permission, err := reader.RepoPermission(ctx, repo, subject)
	if err != nil {
		t.Fatalf("RepoPermission: %v", err)
	}
	if permission != PermissionNone {
		t.Fatalf("a non-collaborator reads as %q, want %q", permission, PermissionNone)
	}
	if len(DeriveActions(permission)) != 0 {
		t.Fatal("a non-collaborator derived some action")
	}

	// Each level Forgejo reports maps onto the intended action set.
	for level, wanted := range map[RepoPermission][]Action{
		PermissionRead:  {ActionReply},
		PermissionWrite: {ActionReply, ActionPlan},
		PermissionAdmin: {ActionReply, ActionPlan, ActionExecute},
	} {
		if err := adminAPI.EnsureCollaborator(ctx, repo, subject, string(level)); err != nil {
			t.Fatalf("grant %s: %v", level, err)
		}
		got, err := reader.RepoPermission(ctx, repo, subject)
		if err != nil {
			t.Fatalf("RepoPermission: %v", err)
		}
		if got != level {
			t.Fatalf("granted %q, Forgejo reports %q -- the derivation table is keyed on the wrong values", level, got)
		}
		derived := DeriveActions(got)
		if len(derived) != len(wanted) {
			t.Fatalf("%q derived %v, want %v", got, derived, wanted)
		}
	}

	// The capability groups arrive as Forgejo teams, read by acting as the
	// person: one call rather than one per team.
	teamName := capabilityPrefix + "codex"
	var team struct {
		ID int64 `json:"id"`
	}
	err = issues.do(ctx, "POST", "/api/v1/orgs/"+org+"/teams",
		map[string]any{"name": teamName, "permission": "read", "units": []string{"repo.issues"}},
		&team, nil, 201, 422)
	if err != nil {
		t.Fatalf("create the capability team: %v", err)
	}
	if team.ID == 0 {
		// Already there from an earlier run; find it.
		var teams []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := issues.do(ctx, "GET", "/api/v1/orgs/"+org+"/teams", nil, &teams, nil, 200); err != nil {
			t.Fatalf("list teams: %v", err)
		}
		for _, candidate := range teams {
			if candidate.Name == teamName {
				team.ID = candidate.ID
			}
		}
	}
	if team.ID == 0 {
		t.Fatal("the capability team could not be found or created")
	}

	before, err := reader.UserTeams(ctx, subject)
	if err != nil {
		t.Fatalf("UserTeams: %v", err)
	}
	if grant := GrantFromTeams(subject, before, testRegistry(t), time.Now()); len(grant.Runtimes) != 0 {
		t.Fatalf("a person in no capability group holds %v", grant.Runtimes)
	}

	if err := issues.do(ctx, "PUT",
		"/api/v1/teams/"+strconv.FormatInt(team.ID, 10)+"/members/"+subject,
		nil, nil, nil, 204, 200); err != nil {
		t.Fatalf("add to the capability team: %v", err)
	}
	after, err := reader.UserTeams(ctx, subject)
	if err != nil {
		t.Fatalf("UserTeams: %v", err)
	}
	grant := GrantFromTeams(subject, after, testRegistry(t), time.Now())
	if !grant.AllowsRuntime("codex") {
		t.Fatalf("teams = %v; the capability group was not read back", after)
	}
	if grant.MaxAction != ActionExecute {
		t.Fatalf("ceiling = %q", grant.MaxAction)
	}
}
