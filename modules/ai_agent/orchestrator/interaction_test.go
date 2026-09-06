package main

import (
	"context"
	"strings"
	"testing"
)

func testInteraction(t *testing.T) (*InteractionReconciler, *fakeIssues) {
	t.Helper()
	issues, store := newFakeIssues(), NewMemoryStore()
	return &InteractionReconciler{
		Repo: testRepo(t), Issues: issues, Outbox: &Outbox{Store: store},
		Registry: testRegistry(t), ForgejoURL: "https://git.example",
		Author: "agent-codex", Log: func(string) {},
	}, issues
}

// AGENT-R-015: enabling a runtime creates its labels, and a second pass creates
// nothing.
func TestLabelsAreCreatedOnceAndKeptCurrent(t *testing.T) {
	reconciler, issues := testInteraction(t)
	ctx := context.Background()
	index, err := reconciler.EnsureLabels(ctx)
	if err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	wanted := LabelVocabulary(reconciler.Registry)
	if len(index) != len(wanted) {
		t.Fatalf("index holds %d labels, want %d", len(index), len(wanted))
	}
	created := issues.countCalls("CreateLabel")

	if _, err := reconciler.EnsureLabels(ctx); err != nil {
		t.Fatalf("second EnsureLabels: %v", err)
	}
	if issues.countCalls("CreateLabel") != created {
		t.Fatal("a second pass created labels again")
	}
	if issues.countCalls("UpdateLabel") != 0 {
		t.Fatal("an unchanged label was updated")
	}
}

// A label whose description drifted is corrected, but never deleted: deleting
// it would strip it from every issue that carries it.
func TestDriftedLabelsAreCorrectedNotDeleted(t *testing.T) {
	reconciler, issues := testInteraction(t)
	ctx := context.Background()
	if _, err := reconciler.EnsureLabels(ctx); err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	issues.mu.Lock()
	drifted := issues.labels[LabelPlan]
	drifted.Description = "someone edited this"
	issues.labels[LabelPlan] = drifted
	issues.mu.Unlock()

	if _, err := reconciler.EnsureLabels(ctx); err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if issues.countCalls("UpdateLabel:"+LabelPlan) != 1 {
		t.Fatal("the drifted label was not corrected")
	}
	// A repository label of its own is untouched.
	issues.mu.Lock()
	issues.labels["bug"] = RepoLabel{ID: 999, Name: "bug"}
	issues.mu.Unlock()
	if _, err := reconciler.EnsureLabels(ctx); err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if _, present := issues.labels["bug"]; !present {
		t.Fatal("a label the repository owns was removed")
	}
}

// AGENT-R-015: the templates are repository content, so a change goes through a
// pull request rather than straight to the default branch.
func TestTemplateChangesGoThroughAPullRequest(t *testing.T) {
	reconciler, issues := testInteraction(t)
	ctx := context.Background()
	fingerprint := RegistryFingerprint(reconciler.Registry)

	pull, err := reconciler.EnsureTemplates(ctx, fingerprint)
	if err != nil {
		t.Fatalf("EnsureTemplates: %v", err)
	}
	if pull == nil {
		t.Fatal("no pull request was opened for the initial templates")
	}
	if !issues.branches["ai/templates-"+fingerprint] {
		t.Fatal("the templates were written without a branch of their own")
	}

	// A second pass over an unchanged registry writes nothing.
	second, err := reconciler.EnsureTemplates(ctx, fingerprint)
	if err != nil {
		t.Fatalf("second EnsureTemplates: %v", err)
	}
	if second != nil {
		t.Fatal("an unchanged registry opened a second pull request")
	}
	if issues.countCalls("CreatePullRequest") != 1 {
		t.Fatalf("CreatePullRequest calls = %d, want 1", issues.countCalls("CreatePullRequest"))
	}
}

// The fingerprint follows the registry, so a changed runtime set produces a new
// branch rather than reusing the old one.
func TestRegistryFingerprintFollowsTheRegistry(t *testing.T) {
	full := RegistryFingerprint(testRegistry(t))
	single, err := NewRegistry([]string{"codex"}, map[string]string{"codex": strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if RegistryFingerprint(single) == full {
		t.Fatal("two different registries share a fingerprint")
	}
	if RegistryFingerprint(testRegistry(t)) != full {
		t.Fatal("the fingerprint is not stable for an unchanged registry")
	}
}

// AGENT-R-018 and AGENT-R-020: the resolved configuration is written back as
// labels and as an assignment, and the run parameters are not written at all.
func TestIssueSyncWritesLabelsAndAssignsButNotRunParameters(t *testing.T) {
	reconciler, issues := testInteraction(t)
	ctx := context.Background()
	index, err := reconciler.EnsureLabels(ctx)
	if err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	issue := *issues.seedIssue(7, "alice", "")
	config := IssueConfig{
		ChatAgents: []string{"codex"}, ExecAgent: "codex",
		Models:  map[string]string{"codex": "gpt-5.1-codex"},
		Efforts: map[string]string{"codex": "high"},
	}
	account := func(id string) string { return "agent-" + id }
	if err := reconciler.SyncIssueLabels(ctx, issue, config, index, account); err != nil {
		t.Fatalf("SyncIssueLabels: %v", err)
	}

	updated, err := issues.Issue(ctx, testRepo(t), 7)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	names := updated.LabelNames()
	if !contains(names, LabelChat+"codex") || !contains(names, LabelExec+"codex") {
		t.Fatalf("labels = %v, want the agent labels written back", names)
	}
	if !contains(names, LabelPullRequest) {
		t.Fatalf("labels = %v, want the completion mode recorded", names)
	}
	for _, name := range names {
		if strings.Contains(name, "gpt-5.1-codex") || strings.Contains(name, "high") {
			t.Fatalf("label %q carries a run parameter", name)
		}
	}
	// A form cannot assign, so the orchestrator does it by API.
	if len(updated.Assignees) != 1 || updated.Assignees[0].Login != "agent-codex" {
		t.Fatalf("assignees = %+v, want the discussing agent assigned", updated.Assignees)
	}
}

// An agent that was narrowed away loses its label, so the issue does not claim
// an agent that is not taking part.
func TestNarrowedAwayAgentLosesItsLabel(t *testing.T) {
	reconciler, issues := testInteraction(t)
	ctx := context.Background()
	index, _ := reconciler.EnsureLabels(ctx)
	issue := *issues.seedIssue(7, "alice", "")
	account := func(id string) string { return "agent-" + id }

	both := IssueConfig{ChatAgents: []string{"codex", "claude_code"}}
	if err := reconciler.SyncIssueLabels(ctx, issue, both, index, account); err != nil {
		t.Fatalf("SyncIssueLabels: %v", err)
	}
	issue, _ = issues.Issue(ctx, testRepo(t), 7)

	one := IssueConfig{ChatAgents: []string{"codex"}}
	if err := reconciler.SyncIssueLabels(ctx, issue, one, index, account); err != nil {
		t.Fatalf("SyncIssueLabels: %v", err)
	}
	updated, _ := issues.Issue(ctx, testRepo(t), 7)
	names := updated.LabelNames()
	if contains(names, LabelChat+"claude_code") {
		t.Fatalf("labels = %v; the narrowed-away agent still claims to take part", names)
	}
	if !contains(names, LabelChat+"codex") {
		t.Fatalf("labels = %v; the remaining agent lost its label", names)
	}
}

// A label the repository owns is never removed by the sync.
func TestSyncLeavesTheRepositorysOwnLabelsAlone(t *testing.T) {
	reconciler, issues := testInteraction(t)
	ctx := context.Background()
	index, _ := reconciler.EnsureLabels(ctx)
	issue := issues.seedIssue(7, "alice", "")
	issue.Labels = append(issue.Labels, RepoLabel{ID: 999, Name: "bug"})

	if err := reconciler.SyncIssueLabels(ctx, *issue, IssueConfig{ChatAgents: []string{"codex"}},
		index, func(id string) string { return "agent-" + id }); err != nil {
		t.Fatalf("SyncIssueLabels: %v", err)
	}
	updated, _ := issues.Issue(ctx, testRepo(t), 7)
	if !contains(updated.LabelNames(), "bug") {
		t.Fatalf("labels = %v; a label the repository owns was removed", updated.LabelNames())
	}
}
