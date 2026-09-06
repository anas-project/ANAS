package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testOnboarding(t *testing.T) (*Onboarding, *fakeIssues) {
	t.Helper()
	issues, store := newFakeIssues(), NewMemoryStore()
	return &Onboarding{
		Repo: testRepo(t), Issues: issues, Outbox: &Outbox{Store: store},
		ForgejoURL: "https://git.example", Registry: testRegistry(t), Defaults: fullDefaults(),
	}, issues
}

// The four-level resolution takes the first level that answers, and records
// which one that was so a later refusal can name its authority.
func TestConventionsResolveInPriorityOrder(t *testing.T) {
	onboarding, issues := testOnboarding(t)
	ctx := context.Background()

	// Level 4: nothing in the repository at all.
	conventions, err := onboarding.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if conventions.Source != "built-in preset" || conventions.AllowDirect {
		t.Fatalf("conventions = %+v; an unknown repository gets the conservative preset", conventions)
	}

	// Level 3: infer from the layout that is there.
	issues.seedFile("dev-docs/requirements/index.md", "# requirements\n")
	issues.seedFile("go.mod", "module x\n")
	conventions, err = onboarding.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if conventions.RequirementsDir != "dev-docs/requirements/" {
		t.Fatalf("conventions = %+v; the existing layout was not read", conventions)
	}
	if conventions.TestCommand != "go test ./..." {
		t.Fatalf("test command = %q; go.mod was present", conventions.TestCommand)
	}
	if !strings.Contains(conventions.Source, "inferred") {
		t.Fatalf("source = %q, want it marked as a guess", conventions.Source)
	}

	// Level 1: the versioned file wins over everything inferred.
	issues.seedFile(ConfigPath, "document_dirs:\n  - notes/\nallow_direct_commit: true\n")
	conventions, err = onboarding.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if conventions.Source != ConfigPath || !conventions.AllowDirect {
		t.Fatalf("conventions = %+v; the versioned file is authoritative", conventions)
	}
	if len(conventions.DocumentDirs) != 1 || conventions.DocumentDirs[0] != "notes/" {
		t.Fatalf("document dirs = %v, want only what the file says", conventions.DocumentDirs)
	}
}

// A named directory that the file forgot to repeat in document_dirs still
// becomes writable, because naming it is clearly the intent.
func TestNamedDirectoriesJoinTheAllowlist(t *testing.T) {
	conventions, err := ParseConventions("requirements_dir: dev-docs/requirements\nplans_dir: dev-docs/plans/\n")
	if err != nil {
		t.Fatalf("ParseConventions: %v", err)
	}
	if !contains(conventions.DocumentDirs, "dev-docs/requirements/") ||
		!contains(conventions.DocumentDirs, "dev-docs/plans/") {
		t.Fatalf("document dirs = %v", conventions.DocumentDirs)
	}
	if err := CheckDocumentPath("dev-docs/plans/x.md", conventions, nil); err != nil {
		t.Fatalf("a named directory was not writable: %v", err)
	}
	if err := CheckDocumentPath("internal/x.go", conventions, nil); err == nil {
		t.Fatal("a path outside the named directories was allowed")
	}
}

func TestMalformedConventionsAreReported(t *testing.T) {
	if _, err := ParseConventions("document_dirs: [unclosed\n"); err == nil {
		t.Fatal("a malformed configuration file was accepted")
	}
}

// AGENT-R-027: the questionnaire is opened once, pre-filled with the guesses.
func TestQuestionnaireIsOpenedOnceAndPreFilled(t *testing.T) {
	onboarding, issues := testOnboarding(t)
	ctx := context.Background()
	issues.seedFile("dev-docs/requirements/index.md", "# r\n")
	conventions, err := onboarding.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	issue, err := onboarding.OpenQuestionnaire(ctx, conventions)
	if err != nil {
		t.Fatalf("OpenQuestionnaire: %v", err)
	}
	if !strings.Contains(issue.Body, "dev-docs/requirements/") {
		t.Fatal("the questionnaire was not pre-filled with what was inferred")
	}
	if !strings.Contains(issue.Body, "guesses") {
		t.Fatal("the questionnaire does not say the answers are guesses")
	}
	if !strings.Contains(issue.Body, ConfigPath) {
		t.Fatal("the questionnaire does not say what will be committed")
	}
	if _, err := onboarding.OpenQuestionnaire(ctx, conventions); !errors.Is(err, ErrAlreadyWritten) {
		t.Fatalf("second OpenQuestionnaire = %v, want it recognised as already done", err)
	}
	if issues.countCalls("CreateIssue") != 1 {
		t.Fatalf("CreateIssue calls = %d, want 1", issues.countCalls("CreateIssue"))
	}
}

// AGENT-R-027: the configuration lands on a branch and through a pull request,
// without anything being cloned.
func TestOnboardingLandsAConfigurationThroughAPullRequest(t *testing.T) {
	onboarding, issues := testOnboarding(t)
	ctx := context.Background()
	conventions := testConventions()

	artifact, pull, err := onboarding.Land(ctx, 3, conventions, "agent-codex")
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if artifact.Path != ConfigPath || artifact.CommitSHA == "" {
		t.Fatalf("artifact = %+v", artifact)
	}
	if !strings.Contains(artifact.URL, "/src/commit/") {
		t.Fatalf("artifact URL = %q, want a commit permalink", artifact.URL)
	}
	if pull.Number == 0 {
		t.Fatal("no pull request was opened")
	}
	// The write went to a new branch, not to the default one.
	if !issues.branches["ai/3-onboarding"] {
		t.Fatal("the configuration was not committed on its own branch")
	}
	// What landed is parseable as the configuration it claims to be.
	parsed, err := ParseConventions(issues.files[ConfigPath])
	if err != nil {
		t.Fatalf("the committed file does not parse: %v", err)
	}
	if len(parsed.DocumentDirs) == 0 {
		t.Fatalf("the committed configuration is empty: %+v", parsed)
	}

	// Repeating the flow neither commits again nor opens a second pull request.
	if _, _, err := onboarding.Land(ctx, 3, conventions, "agent-codex"); !errors.Is(err, ErrAlreadyWritten) {
		t.Fatalf("second Land = %v", err)
	}
	if issues.countCalls("CreatePullRequest") != 1 {
		t.Fatalf("CreatePullRequest calls = %d, want 1", issues.countCalls("CreatePullRequest"))
	}
}

// The whole flow uses only the REST surface: no clone, no checkout, no git
// credential.
func TestOnboardingNeverNeedsACheckout(t *testing.T) {
	onboarding, issues := testOnboarding(t)
	ctx := context.Background()
	conventions, err := onboarding.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := onboarding.OpenQuestionnaire(ctx, conventions); err != nil {
		t.Fatalf("OpenQuestionnaire: %v", err)
	}
	if _, _, err := onboarding.Land(ctx, 1, conventions, "agent-codex"); err != nil {
		t.Fatalf("Land: %v", err)
	}
	for _, call := range issues.calls {
		for _, forbidden := range []string{"Clone", "Checkout", "Push"} {
			if strings.Contains(call, forbidden) {
				t.Fatalf("the onboarding flow called %s", call)
			}
		}
	}
}

func TestRenderedConventionsRoundTrip(t *testing.T) {
	original := testConventions()
	rendered, err := RenderConventions(original)
	if err != nil {
		t.Fatalf("RenderConventions: %v", err)
	}
	if !strings.Contains(rendered, "authoritative") {
		t.Fatal("the committed file does not say it is authoritative")
	}
	parsed, err := ParseConventions(rendered)
	if err != nil {
		t.Fatalf("ParseConventions: %v", err)
	}
	for _, dir := range original.DocumentDirs {
		if !contains(parsed.DocumentDirs, dir) {
			t.Fatalf("round trip lost %q: %+v", dir, parsed)
		}
	}
	for _, path := range original.ForbiddenPaths {
		if !contains(parsed.ForbiddenPaths, path) {
			t.Fatalf("round trip lost the forbidden path %q", path)
		}
	}
}
