package main

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestM2AgainstLiveForgejo drives the interaction contract against a real
// Forgejo. The M1 probe found five upstream facts that no stub would have
// caught, so the same treatment is given to the surface M2 adds: generated
// forms actually being recognised as forms, labels, comment editing,
// reactions, the contents API and pull requests.
//
//	AI_AGENT_TEST_FORGEJO_URL=... AI_AGENT_TEST_FORGEJO_USER=... \
//	AI_AGENT_TEST_FORGEJO_PASSWORD=... AI_AGENT_TEST_FORGEJO_TOKEN=... \
//	AI_AGENT_TEST_FORGEJO_ORG=... AI_AGENT_TEST_FORGEJO_REPO_IN=... \
//	  go test ./modules/ai_agent/orchestrator -run TestM2AgainstLiveForgejo
func TestM2AgainstLiveForgejo(t *testing.T) {
	baseURL := os.Getenv("AI_AGENT_TEST_FORGEJO_URL")
	token := os.Getenv("AI_AGENT_TEST_FORGEJO_TOKEN")
	org := os.Getenv("AI_AGENT_TEST_FORGEJO_ORG")
	repoName := os.Getenv("AI_AGENT_TEST_FORGEJO_REPO_IN")
	if baseURL == "" || token == "" || org == "" || repoName == "" {
		t.Skip("set AI_AGENT_TEST_FORGEJO_URL/TOKEN/ORG/REPO_IN to run the live M2 tests")
	}
	ctx := context.Background()
	repo, err := ParseRepo(org + "/" + repoName)
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	redactor := NewRedactor(token)
	issues := NewForgejoIssues(baseURL, token, redactor)
	store := NewMemoryStore()
	registry := testRegistry(t)
	suffix := strconv.FormatInt(time.Now().Unix(), 10)

	reconciler := &InteractionReconciler{
		Repo: repo, Issues: issues, Outbox: &Outbox{Store: store}, Registry: registry,
		ForgejoURL: baseURL, Author: os.Getenv("AI_AGENT_TEST_FORGEJO_USER"), Log: func(string) {},
	}

	// Labels: the whole vocabulary is created, and a second pass creates
	// nothing. Forgejo validates colours, so an unacceptable one fails here.
	index, err := reconciler.EnsureLabels(ctx)
	if err != nil {
		t.Fatalf("EnsureLabels: %v", err)
	}
	if len(index) != len(LabelVocabulary(registry)) {
		t.Fatalf("created %d labels, want %d", len(index), len(LabelVocabulary(registry)))
	}
	second, err := reconciler.EnsureLabels(ctx)
	if err != nil {
		t.Fatalf("second EnsureLabels: %v", err)
	}
	for name, id := range index {
		if second[name] != id {
			t.Fatalf("label %q changed id between passes: %d then %d", name, id, second[name])
		}
	}

	// Templates: generated and proposed through a pull request. A repository
	// that already carries the current forms needs no pull request at all, and
	// that is the correct outcome rather than a missing one -- so the assertion
	// is on the end state, not on the mechanism.
	fingerprint := RegistryFingerprint(registry) + "-" + suffix
	pull, err := reconciler.EnsureTemplates(ctx, fingerprint)
	if err != nil {
		t.Fatalf("EnsureTemplates: %v", err)
	}
	if pull != nil {
		t.Logf("templates proposed in pull request #%d", pull.Number)
		if !contains(branchNames(ctx, t, issues, repo), "ai/templates-"+fingerprint) {
			t.Log("note: the template branch was not listed back")
		}
	} else {
		t.Log("the repository already carries the current forms")
	}
	// A second pass over an unchanged registry must propose nothing further.
	again, err := reconciler.EnsureTemplates(ctx, fingerprint)
	if err != nil {
		t.Fatalf("second EnsureTemplates: %v", err)
	}
	if again != nil {
		t.Fatal("an unchanged registry proposed the templates a second time")
	}

	// The decisive question a stub cannot answer: does Forgejo accept the
	// generated files as issue forms? A template it rejects is silently inert,
	// and the whole of AGENT-R-015 rests on this.
	if templates := recognisedTemplates(ctx, t, baseURL, token, repo); len(templates) > 0 {
		expected, err := GenerateTemplates(registry, "")
		if err != nil {
			t.Fatalf("GenerateTemplates: %v", err)
		}
		wanted := 0
		for _, file := range expected {
			if !strings.HasSuffix(file.Path, "config.yaml") {
				wanted++
			}
		}
		if len(templates) != wanted {
			t.Fatalf("Forgejo recognised %d of %d generated forms; the rest were rejected",
				len(templates), wanted)
		}
		for name, fields := range templates {
			if fields == 0 {
				t.Errorf("Forgejo recognised %s but parsed no fields from it", name)
			}
		}
	} else {
		t.Log("no forms on the default branch yet; merge the templates pull request to check recognition")
	}

	// An issue exercising the comment contract.
	issue, err := issues.CreateIssue(ctx, repo, "[agent] live M2 check "+suffix,
		"### Goal\n\nCheck the interaction contract.\n\n### Discussing agents\n\ncodex\n", nil)
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	t.Cleanup(func() {
		// Leave the issue: deleting it would remove the evidence of the run.
		t.Logf("live M2 issue: %s/%s/%s/issues/%d", baseURL, repo.Owner, repo.Name, issue.Number)
	})

	// AGENT-R-018: a form cannot assign, so assignment is an API call.
	if err := issues.AddLabels(ctx, repo, issue.Number, []int64{index[LabelChat+"codex"]}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	reread, err := issues.Issue(ctx, repo, issue.Number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !contains(reread.LabelNames(), LabelChat+"codex") {
		t.Fatalf("labels = %v, want the one just added", reread.LabelNames())
	}
	if err := issues.RemoveLabel(ctx, repo, issue.Number, index[LabelChat+"codex"]); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}

	// AGENT-R-019: one status comment, edited in place; a reply is a new
	// comment; an acknowledgement is a reaction.
	conversation := &Conversation{
		Repo: repo, Number: issue.Number, Issues: issues, Store: store,
		Outbox: &Outbox{Store: store}, Registry: registry, ForgejoURL: baseURL,
		Now: func() time.Time { return time.Now().UTC() },
	}
	status := Status{Phase: PhaseThinking, Since: time.Now().UTC(),
		Config: IssueConfig{ChatAgents: []string{"codex"}, Language: "en"}}
	if written, err := conversation.UpdateStatus(ctx, status); err != nil || !written {
		t.Fatalf("UpdateStatus = %v, %v", written, err)
	}
	statusID := conversation.StatusCommentID()
	if statusID == 0 {
		t.Fatal("the status comment was not recorded")
	}
	status.Phase = PhaseReview
	if written, err := conversation.UpdateStatus(ctx, status); err != nil || !written {
		t.Fatalf("second UpdateStatus = %v, %v", written, err)
	}
	if conversation.StatusCommentID() != statusID {
		t.Fatal("the status comment was replaced instead of edited")
	}
	if _, err := conversation.Say(ctx, "run-"+suffix, "turn-1", "A reply is its own comment."); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if err := conversation.Acknowledge(ctx, statusID, ReactionAcknowledged); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	comments, err := issues.Comments(ctx, repo, issue.Number)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	statusCount := 0
	for _, comment := range comments {
		if strings.Contains(comment.Body, statusMarker) {
			statusCount++
		}
	}
	if statusCount != 1 {
		t.Fatalf("the issue holds %d status comments, want exactly 1", statusCount)
	}

	// A restarted orchestrator adopts the existing status comment from the
	// marker alone.
	restarted := &Conversation{
		Repo: repo, Number: issue.Number, Issues: issues, Store: NewMemoryStore(),
		Outbox: &Outbox{Store: NewMemoryStore()}, Registry: registry, ForgejoURL: baseURL,
	}
	author := os.Getenv("AI_AGENT_TEST_FORGEJO_USER")
	if err := restarted.AdoptStatusComment(ctx, []string{author}); err != nil {
		t.Fatalf("AdoptStatusComment: %v", err)
	}
	if restarted.StatusCommentID() != statusID {
		t.Fatalf("adopted comment %d, want %d", restarted.StatusCommentID(), statusID)
	}

	// AGENT-R-024, R-026: the control plane commits a document and the artifact
	// is addressed by the commit it landed in.
	writer := &DocumentWriter{
		Repo: repo, Issues: issues, Outbox: &Outbox{Store: store}, ForgejoURL: baseURL,
		Conventions: RepoConventions{Source: "test", DocumentDirs: []string{"docs/"}, TargetBranch: "main"},
		Author:      author, Email: author + "@localhost.invalid",
	}
	artifact, err := writer.Commit(ctx, DocumentRequest{
		Issue: issue.Number, RunID: "run-" + suffix, Path: "docs/agent-live-" + suffix + ".md",
		Content: "# live check\n", Stage: "plan", Version: "v1", Summary: "a document",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if artifact.CommitSHA == "" || !strings.Contains(artifact.URL, "/src/commit/") {
		t.Fatalf("artifact = %+v", artifact)
	}
	// The permalink resolves: a link that 404s in a comment is worse than none.
	if _, err := issues.FileContents(ctx, repo, artifact.Path, artifact.CommitSHA); err != nil {
		t.Fatalf("the artifact is not readable at its own commit: %v", err)
	}

	// Updating the same document a second time needs the blob it replaces.
	updated, err := writer.Commit(ctx, DocumentRequest{
		Issue: issue.Number, RunID: "run-" + suffix, Path: artifact.Path,
		Content: "# live check, revised\n", Stage: "plan", Version: "v2",
	})
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if updated.CommitSHA == artifact.CommitSHA {
		t.Fatal("the revision produced the same commit")
	}

	// AGENT-R-028: the frozen basis is checked against the repository.
	basis := FreezeBasis(artifact, "tester", time.Now().UTC())
	valid, reason, err := BasisStillValid(ctx, issues, repo, basis)
	if err != nil {
		t.Fatalf("BasisStillValid: %v", err)
	}
	if !valid {
		t.Fatalf("the frozen basis stopped being valid: %s", reason)
	}

	// AGENT-R-027: onboarding lands a configuration through a pull request
	// without cloning anything.
	onboarding := &Onboarding{
		Repo: repo, Issues: issues, Outbox: &Outbox{Store: store}, ForgejoURL: baseURL,
		Registry: registry, Defaults: fullDefaults(),
	}
	conventions, err := onboarding.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Logf("resolved conventions from: %s", conventions.Source)
	onboardingIssue, err := onboarding.OpenQuestionnaire(ctx, conventions)
	if err != nil {
		t.Fatalf("OpenQuestionnaire: %v", err)
	}
	configArtifact, configPull, err := onboarding.Land(ctx, onboardingIssue.Number, conventions, author)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if configPull.Number == 0 || configArtifact.CommitSHA == "" {
		t.Fatalf("onboarding produced %+v / %+v", configArtifact, configPull)
	}
	committed, err := issues.FileContents(ctx, repo, ConfigPath, configArtifact.CommitSHA)
	if err != nil {
		t.Fatalf("the committed configuration is not readable: %v", err)
	}
	decoded, err := committed.Decoded()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := ParseConventions(decoded); err != nil {
		t.Fatalf("what was committed does not parse as a configuration: %v", err)
	}
}

// recognisedTemplates asks Forgejo which issue forms it actually parsed, and
// how many fields it found in each.
func recognisedTemplates(ctx context.Context, t *testing.T, baseURL, token string, repo Repo) map[string]int {
	t.Helper()
	client := NewForgejoIssues(baseURL, token, NewRedactor(token)).(issuesClient)
	var templates []struct {
		FileName string `json:"file_name"`
		Body     []struct {
			Type string `json:"type"`
		} `json:"body"`
	}
	err := client.do(ctx, "GET", client.repoPath(repo, "/issue_templates"), nil, &templates, nil, 200)
	if err != nil {
		t.Fatalf("read issue templates: %v", err)
	}
	out := map[string]int{}
	for _, template := range templates {
		out[template.FileName] = len(template.Body)
	}
	return out
}

// branchNames lists the repository's branches, for the template branch check.
func branchNames(ctx context.Context, t *testing.T, issues ForgejoIssues, repo Repo) []string {
	t.Helper()
	client, ok := issues.(issuesClient)
	if !ok {
		return nil
	}
	var branches []struct {
		Name string `json:"name"`
	}
	if err := client.do(ctx, "GET", client.repoPath(repo, "/branches"), nil, &branches, nil, 200); err != nil {
		return nil
	}
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		names = append(names, branch.Name)
	}
	return names
}
