package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var errFake = errors.New("dependencies are disabled on this instance")

func testSplitter(t *testing.T) (*Splitter, *fakeIssues) {
	t.Helper()
	writer, issues, store := testWriter(t)
	return &Splitter{
		Repo: testRepo(t), Issues: issues, Outbox: &Outbox{Store: store},
		Documents: writer, ForgejoURL: "https://git.example",
	}, issues
}

func parentConfig() IssueConfig {
	return IssueConfig{
		Goal: "make the thing work", Acceptance: "tests pass",
		ChatAgents: []string{"codex", "claude_code"}, HostAgent: "codex", ExecAgent: "codex",
		Budget: "small", Language: "zh", Branch: "ai/12-v1",
		ForbiddenPaths: []string{"vendor/"}, ExecuteWhen: "now",
	}
}

// AGENT-R-067: the child inherits the parent's configuration, the parent is
// blocked by it, and both sides are cross-referenced.
func TestSplitCreatesADependentChildIssue(t *testing.T) {
	splitter, issues := testSplitter(t)
	ctx := context.Background()
	result, err := splitter.Split(ctx, SplitRequest{
		Parent: 12, RunID: "run-1", Title: "Extract the parser",
		Goal: "pull the parser out first", Inherit: parentConfig(),
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if result.Child == 0 {
		t.Fatal("no child issue was created")
	}
	if !result.Blocked {
		t.Fatalf("the parent was not blocked by the child: %s", result.Note)
	}
	if blockers := issues.dependencies[12]; len(blockers) != 1 || blockers[0] != result.Child {
		t.Fatalf("dependency not recorded on the parent: %v", blockers)
	}

	child, err := issues.Issue(ctx, testRepo(t), result.Child)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// Inheritance is literal: the child body is a form the ordinary parser
	// reads, so nothing about the configuration needs a second representation.
	if !LooksLikeAgentIssue(child.Body) {
		t.Fatal("the child issue is not recognisable as an agent issue")
	}
	inherited := ResolveIssueConfig(ParseAnswers(child.Body), testRegistry(t), fullDefaults())
	if inherited.ExecAgent != "codex" || len(inherited.ChatAgents) != 2 {
		t.Fatalf("configuration was not inherited: %+v", inherited)
	}
	if !strings.Contains(child.Body, "#12") {
		t.Fatal("the child does not point back at the parent")
	}

	var parentSaw, childSaw bool
	for _, id := range issues.byIssue[12] {
		if strings.Contains(issues.comments[id].Body, "Split #") {
			parentSaw = true
		}
	}
	for _, id := range issues.byIssue[result.Child] {
		if strings.Contains(issues.comments[id].Body, "Split out of #12") {
			childSaw = true
		}
	}
	if !parentSaw || !childSaw {
		t.Fatalf("cross references missing: parent=%v child=%v", parentSaw, childSaw)
	}
}

// Repeating the command must not open a second issue (AGENT-R-044).
func TestSplitIsIdempotent(t *testing.T) {
	splitter, issues := testSplitter(t)
	ctx := context.Background()
	request := SplitRequest{Parent: 12, RunID: "run-1", Title: "Extract the parser",
		Goal: "pull the parser out first", Inherit: parentConfig()}
	if _, err := splitter.Split(ctx, request); err != nil {
		t.Fatalf("first split: %v", err)
	}
	before := len(issues.issues)
	if _, err := splitter.Split(ctx, request); err != ErrAlreadySplit {
		t.Fatalf("second split: want ErrAlreadySplit, got %v", err)
	}
	if len(issues.issues) != before {
		t.Fatal("a repeated split created a second issue")
	}
}

// A dependency the instance refuses must not lose the split: the child issue
// and the cross references are still the useful part.
func TestSplitSurvivesARefusedDependency(t *testing.T) {
	splitter, issues := testSplitter(t)
	issues.failOn["AddDependency"] = errFake
	result, err := splitter.Split(context.Background(), SplitRequest{
		Parent: 12, RunID: "run-1", Title: "Extract the parser", Inherit: parentConfig()})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if result.Child == 0 {
		t.Fatal("the child issue was lost with the dependency")
	}
	if result.Blocked || result.Note == "" {
		t.Fatal("the refused dependency was not reported")
	}
}

// AGENT-R-067 + AGENT-R-024/026: the summary is a committed document, and the
// comment points at the commit rather than repeating the content.
func TestSummarizeCommitsADocumentAndLinksIt(t *testing.T) {
	splitter, issues := testSplitter(t)
	artifact, err := splitter.Summarize(context.Background(), SummaryRequest{
		Issue: 12, RunID: "run-1", Title: "discussion so far",
		Path: "dev-docs/requirements/thing.md", Content: "# thing\n",
	})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if artifact.CommitSHA == "" || artifact.URL == "" {
		t.Fatalf("the summary was not addressed by a commit: %+v", artifact)
	}
	var linked bool
	for _, id := range issues.byIssue[12] {
		if strings.Contains(issues.comments[id].Body, artifact.URL) {
			linked = true
		}
	}
	if !linked {
		t.Fatal("the summary comment does not link the commit")
	}
}
