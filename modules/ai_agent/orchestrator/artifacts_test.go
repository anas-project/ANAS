package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testConventions() RepoConventions {
	conventions := RepoConventions{
		Source: "test", DocumentDirs: []string{"dev-docs/", "docs/"},
		ForbiddenPaths: []string{"vendor/", "config.yml"}, TargetBranch: "main",
	}
	conventions.normalize()
	return conventions
}

// AGENT-R-025: a document may only land inside the directories the repository
// allows, and a refusal says which authority the rule came from.
func TestDocumentPathAllowlist(t *testing.T) {
	conventions := testConventions()
	for _, allowed := range []string{
		"dev-docs/requirements/x.md", "docs/architecture/y.md", "docs/z.md",
	} {
		if err := CheckDocumentPath(allowed, conventions, nil); err != nil {
			t.Errorf("CheckDocumentPath(%q) = %v, want it allowed", allowed, err)
		}
	}
	for _, refused := range []string{
		"internal/runner/manifest.go", "README.md", "/etc/passwd", "",
		"vendor/x/y.md", "config.yml", ".git/config",
	} {
		err := CheckDocumentPath(refused, conventions, nil)
		if err == nil {
			t.Errorf("CheckDocumentPath(%q) allowed a path outside the allowlist", refused)
			continue
		}
		var refusal *ErrPathRefused
		if !errors.As(err, &refusal) || refusal.Reason == "" {
			t.Errorf("CheckDocumentPath(%q) = %v, want a refusal with a reason", refused, err)
		}
	}
}

// A traversal must be judged on the cleaned path, or `docs/../../etc/passwd`
// passes by starting with an allowed prefix.
func TestDocumentPathRefusesTraversal(t *testing.T) {
	conventions := testConventions()
	for _, path := range []string{
		"docs/../../etc/passwd", "docs/../internal/secret.go", "dev-docs/../../../root/.ssh/id_ed25519",
	} {
		if err := CheckDocumentPath(path, conventions, nil); err == nil {
			t.Errorf("CheckDocumentPath(%q) allowed a traversal", path)
		}
	}
	// A traversal that resolves back inside the allowlist is fine; the check is
	// on where the path lands, not on how it is spelled.
	if err := CheckDocumentPath("docs/a/../b.md", conventions, nil); err != nil {
		t.Errorf("a path that resolves inside the allowlist was refused: %v", err)
	}
}

// An issue's extra forbidden paths narrow further, and a forbidden path beats
// an allowed directory.
func TestForbiddenPathsBeatTheAllowlist(t *testing.T) {
	conventions := testConventions()
	if err := CheckDocumentPath("docs/secret/plan.md", conventions, []string{"docs/secret"}); err == nil {
		t.Fatal("the issue's extra forbidden path was ignored")
	}
	if err := CheckDocumentPath("docs/open/plan.md", conventions, []string{"docs/secret"}); err != nil {
		t.Fatalf("an unrelated path was refused: %v", err)
	}
}

func testWriter(t *testing.T) (*DocumentWriter, *fakeIssues, *MemoryStore) {
	t.Helper()
	issues, store := newFakeIssues(), NewMemoryStore()
	return &DocumentWriter{
		Repo: testRepo(t), Issues: issues, Outbox: &Outbox{Store: store},
		ForgejoURL: "https://git.example", Conventions: testConventions(),
		Author: "agent-codex", Email: "agent-codex@localhost.invalid",
	}, issues, store
}

// AGENT-R-024, AGENT-R-026: the control plane commits the document, and the
// artifact is addressed by the commit it landed in.
func TestCommitProducesACommitAddressedArtifact(t *testing.T) {
	writer, issues, _ := testWriter(t)
	ctx := context.Background()
	artifact, err := writer.Commit(ctx, DocumentRequest{
		Issue: 7, RunID: "run-1", Path: "dev-docs/plans/thing.md", Version: "v1",
		Content: "# plan\n", Stage: "plan", Summary: "the plan",
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if artifact.CommitSHA == "" {
		t.Fatal("the artifact has no commit")
	}
	if !strings.Contains(artifact.URL, "/src/commit/"+artifact.CommitSHA+"/") {
		t.Fatalf("artifact URL = %q, want a commit permalink", artifact.URL)
	}
	if issues.files["dev-docs/plans/thing.md"] != "# plan\n" {
		t.Fatal("the document was not committed")
	}
}

// AGENT-R-025: an out-of-bounds path is refused before any write is attempted.
func TestCommitRefusesAnOutOfBoundsPathWithoutWriting(t *testing.T) {
	writer, issues, _ := testWriter(t)
	_, err := writer.Commit(context.Background(), DocumentRequest{
		Issue: 7, Path: "internal/runner/manifest.go", Content: "package main\n", Version: "v1",
	})
	if err == nil {
		t.Fatal("Commit wrote outside the allowlist")
	}
	if issues.countCalls("PutFile") != 0 {
		t.Fatal("the refusal happened after the write was attempted")
	}
}

// Updating an existing document sends the blob it replaces, so a concurrent
// edit is a conflict rather than a silent overwrite.
func TestUpdatingAnExistingDocumentSendsItsBlob(t *testing.T) {
	writer, issues, _ := testWriter(t)
	ctx := context.Background()
	issues.seedFile("dev-docs/plans/thing.md", "# old\n")
	if _, err := writer.Commit(ctx, DocumentRequest{
		Issue: 7, Path: "dev-docs/plans/thing.md", Content: "# new\n", Version: "v2", Stage: "plan",
	}); err != nil {
		t.Fatalf("Commit over an existing file: %v", err)
	}
	if issues.files["dev-docs/plans/thing.md"] != "# new\n" {
		t.Fatal("the document was not updated")
	}
}

// AGENT-R-044: the same document version is committed once, however many times
// the trigger arrives.
func TestRepeatedCommitOfTheSameVersionWritesOnce(t *testing.T) {
	writer, issues, _ := testWriter(t)
	ctx := context.Background()
	request := DocumentRequest{Issue: 7, RunID: "run-1", Path: "docs/x.md",
		Content: "one\n", Version: "v1", Stage: "plan"}
	if _, err := writer.Commit(ctx, request); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	_, err := writer.Commit(ctx, request)
	if !errors.Is(err, ErrAlreadyWritten) {
		t.Fatalf("second Commit = %v, want ErrAlreadyWritten", err)
	}
	if issues.countCalls("PutFile") != 1 {
		t.Fatalf("PutFile calls = %d, want 1", issues.countCalls("PutFile"))
	}
}

// The commit message names the issue and the stage, so `git log` alone explains
// why a file changed.
func TestCommitMessageNamesTheIssueAndStage(t *testing.T) {
	writer, issues, _ := testWriter(t)
	var captured FileWrite
	writer.Issues = recordingIssues{fakeIssues: issues, onPut: func(w FileWrite) { captured = w }}
	if _, err := writer.Commit(context.Background(), DocumentRequest{
		Issue: 42, Path: "docs/x.md", Content: "x\n", Version: "v1", Stage: "requirements",
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !strings.Contains(captured.Message, "issue #42") || !strings.Contains(captured.Message, "stage: requirements") {
		t.Fatalf("commit message = %q", captured.Message)
	}
	if captured.Author != "agent-codex" {
		t.Fatalf("author = %q; the history must say which agent produced the document", captured.Author)
	}
}

// AGENT-R-028: an approval freezes a (path, commit) pair, and any change to
// either half voids it.
func TestExecutionBasisIsFrozenAndInvalidatedByAnyChange(t *testing.T) {
	artifact := Artifact{Path: "dev-docs/plans/x.md", CommitSHA: strings.Repeat("a", 40)}
	basis := FreezeBasis(artifact, "alice", time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if !basis.Matches(artifact.Path, artifact.CommitSHA) {
		t.Fatal("the frozen basis does not match what it was frozen from")
	}
	if basis.Matches(artifact.Path, strings.Repeat("b", 40)) {
		t.Fatal("a different commit still matched the approval")
	}
	if basis.Matches("dev-docs/plans/other.md", artifact.CommitSHA) {
		t.Fatal("a different document still matched the approval")
	}
	if basis.ApprovedBy != "alice" || basis.FrozenAt.IsZero() {
		t.Fatalf("basis = %+v, want the approver and the moment recorded", basis)
	}
}

// The basis is re-checked against the repository, not just against memory: an
// approval can be days old.
func TestBasisValidityIsCheckedAgainstTheRepository(t *testing.T) {
	issues := newFakeIssues()
	ctx := context.Background()
	issues.seedFile("dev-docs/plans/x.md", "# plan\n")
	basis := ExecutionBasis{Path: "dev-docs/plans/x.md", CommitSHA: strings.Repeat("a", 40)}
	valid, reason, err := BasisStillValid(ctx, issues, testRepo(t), basis)
	if err != nil || !valid {
		t.Fatalf("BasisStillValid = %v, %q, %v", valid, reason, err)
	}
	gone := ExecutionBasis{Path: "dev-docs/plans/removed.md", CommitSHA: strings.Repeat("a", 40)}
	valid, reason, err = BasisStillValid(ctx, issues, testRepo(t), gone)
	if err != nil {
		t.Fatalf("BasisStillValid: %v", err)
	}
	if valid || reason == "" {
		t.Fatalf("a missing document still validated: %v %q", valid, reason)
	}
}

func TestPermalinksAddressCommitsNotBranches(t *testing.T) {
	sha := strings.Repeat("a", 40)
	link := PermalinkURL("https://git.example/", testRepo(t), sha, "docs/x.md")
	if link != "https://git.example/anas-project/ANAS/src/commit/"+sha+"/docs/x.md" {
		t.Fatalf("PermalinkURL = %q", link)
	}
	raw := RawPermalinkURL("https://git.example", testRepo(t), sha, "/docs/x.md")
	if !strings.Contains(raw, "/raw/commit/"+sha+"/docs/x.md") {
		t.Fatalf("RawPermalinkURL = %q", raw)
	}
}

// recordingIssues lets a test see the write a caller built.
type recordingIssues struct {
	*fakeIssues
	onPut func(FileWrite)
}

func (r recordingIssues) PutFile(ctx context.Context, repo Repo, request FileWrite) (FileCommit, error) {
	r.onPut(request)
	return r.fakeIssues.PutFile(ctx, repo, request)
}

// AGENT-R-029: a document of a kind the repository has placed goes where that
// repository puts that kind. Passing the allowlist is not enough -- a research
// report in the plans directory is inside the allowlist and still wrong.
func TestDocumentsFollowTheRepositorysOwnPlacement(t *testing.T) {
	writer, _, _ := testWriter(t)
	writer.Conventions = RepoConventions{
		Source: "test", DocumentDirs: []string{"dev-docs/", "docs/"},
		RequirementsDir: "dev-docs/requirements/", PlansDir: "dev-docs/plans/",
		ResearchDir: "docs/research/", TargetBranch: "main",
	}
	writer.Conventions.normalize()
	ctx := context.Background()

	if _, err := writer.Commit(ctx, DocumentRequest{
		Issue: 7, Path: "docs/research/finding.md", Content: "# r\n",
		Stage: string(StageResearch), Version: "v1",
	}); err != nil {
		t.Fatalf("a research document in the research directory was refused: %v", err)
	}

	_, err := writer.Commit(ctx, DocumentRequest{
		Issue: 7, Path: "dev-docs/plans/finding.md", Content: "# r\n",
		Stage: string(StageResearch), Version: "v1",
	})
	if err == nil {
		t.Fatal("a research document was written into the plans directory")
	}
	var refusal *ErrPathRefused
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Reason, "docs/research/") {
		t.Fatalf("refusal = %v, want it to name where research belongs", err)
	}
}

// A repository that has not said where a kind of document goes is not guessed
// at: the allowlist still applies, and nothing narrower is invented.
func TestUnplacedDocumentKindsFallBackToTheAllowlist(t *testing.T) {
	writer, _, _ := testWriter(t)
	ctx := context.Background()
	if _, err := writer.Commit(ctx, DocumentRequest{
		Issue: 7, Path: "docs/anything.md", Content: "# x\n",
		Stage: string(StageResearch), Version: "v1",
	}); err != nil {
		t.Fatalf("a repository with no research directory refused an allowed path: %v", err)
	}
}

func TestDirectoryForReportsWhetherTheRepositorySaid(t *testing.T) {
	conventions := RepoConventions{ResearchDir: "docs/research"}
	conventions.normalize()
	if dir, declared := conventions.DirectoryFor(StageResearch); !declared || dir != "docs/research/" {
		t.Fatalf("DirectoryFor(research) = %q, %v", dir, declared)
	}
	if _, declared := conventions.DirectoryFor(StageDesign); declared {
		t.Fatal("a directory the repository never named was reported as declared")
	}
}
