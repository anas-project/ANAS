package main

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RepoConventions is what a repository has told the orchestrator about its own
// engineering conventions. It is resolved in four steps, first hit wins:
// the versioned `.anas-agent.yml`, the conclusion of the onboarding issue, what
// can be inferred from the repository's existing shape, and finally a
// conservative built-in preset.
type RepoConventions struct {
	// Source records which of the four levels produced this, because "where did
	// this rule come from" is the first question when a path is refused.
	Source string `yaml:"-"`

	RequirementsDir string `yaml:"requirements_dir,omitempty"`
	PlansDir        string `yaml:"plans_dir,omitempty"`
	DesignDir       string `yaml:"design_dir,omitempty"`
	ResearchDir     string `yaml:"research_dir,omitempty"`
	// DocumentDirs is the whole allowlist a document may be written into. The
	// named directories above are conveniences; this is what is enforced.
	DocumentDirs   []string `yaml:"document_dirs,omitempty"`
	ForbiddenPaths []string `yaml:"forbidden_paths,omitempty"`

	Language      string   `yaml:"language,omitempty"`
	EnglishMirror bool     `yaml:"english_mirror,omitempty"`
	CommitStyle   string   `yaml:"commit_style,omitempty"`
	BranchPattern string   `yaml:"branch_pattern,omitempty"`
	RequirePR     bool     `yaml:"require_pull_request,omitempty"`
	TargetBranch  string   `yaml:"target_branch,omitempty"`
	TestCommand   string   `yaml:"test_command,omitempty"`
	LintCommand   string   `yaml:"lint_command,omitempty"`
	BuildCommand  string   `yaml:"build_command,omitempty"`
	ChatAgents    []string `yaml:"chat_agents,omitempty"`
	ExecAgent     string   `yaml:"exec_agent,omitempty"`
	Budget        string   `yaml:"budget,omitempty"`
	AllowDirect   bool     `yaml:"allow_direct_commit,omitempty"`
}

// ConfigPath is where a repository's own agent configuration lives. It is
// versioned with the repository on purpose: changing the conventions is a pull
// request like any other change.
const ConfigPath = ".anas-agent.yml"

// BuiltinConventions is the fourth level: what a repository gets before anyone
// has said anything. It is conservative -- documents only, pull requests
// always, no direct commit -- because the cost of guessing wrong is a commit
// nobody asked for.
func BuiltinConventions() RepoConventions {
	return RepoConventions{
		Source:        "built-in preset",
		DocumentDirs:  []string{"docs/"},
		RequirePR:     true,
		BranchPattern: "ai/<issue>-<version>",
		AllowDirect:   false,
	}
}

// ParseConventions reads `.anas-agent.yml`.
func ParseConventions(content string) (RepoConventions, error) {
	var conventions RepoConventions
	if err := yaml.Unmarshal([]byte(content), &conventions); err != nil {
		return RepoConventions{}, fmt.Errorf("parse %s: %w", ConfigPath, err)
	}
	conventions.Source = ConfigPath
	conventions.normalize()
	return conventions, nil
}

// normalize folds the named directories into the enforced allowlist, so a
// repository that set `requirements_dir` but forgot `document_dirs` still gets
// the directory it named -- and only that one.
func (c *RepoConventions) normalize() {
	for _, dir := range []string{c.RequirementsDir, c.PlansDir, c.DesignDir, c.ResearchDir} {
		if dir == "" {
			continue
		}
		normalized := normalizeDir(dir)
		if !contains(c.DocumentDirs, normalized) {
			c.DocumentDirs = append(c.DocumentDirs, normalized)
		}
	}
	for index, dir := range c.DocumentDirs {
		c.DocumentDirs[index] = normalizeDir(dir)
	}
}

func normalizeDir(dir string) string {
	dir = strings.TrimSpace(strings.TrimPrefix(path.Clean(strings.TrimSpace(dir)), "/"))
	if dir == "" || dir == "." {
		return ""
	}
	return dir + "/"
}

// InferConventions is the third level: read the repository's existing shape.
// It is a guess and says so, so a person can correct it in the onboarding issue
// rather than discovering it later in a commit.
func InferConventions(files []string) RepoConventions {
	conventions := BuiltinConventions()
	conventions.Source = "inferred from the repository's existing layout"
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || seen[dir] {
			return
		}
		seen[dir] = true
		conventions.DocumentDirs = append(conventions.DocumentDirs, dir)
	}
	conventions.DocumentDirs = nil
	for _, file := range files {
		switch {
		case strings.HasPrefix(file, "dev-docs/requirements/"):
			conventions.RequirementsDir = "dev-docs/requirements/"
			add("dev-docs/requirements/")
		case strings.HasPrefix(file, "dev-docs/plans/"):
			conventions.PlansDir = "dev-docs/plans/"
			add("dev-docs/plans/")
		case strings.HasPrefix(file, "docs/architecture/"):
			conventions.DesignDir = "docs/architecture/"
			add("docs/architecture/")
		case strings.HasPrefix(file, "docs/research/"):
			conventions.ResearchDir = "docs/research/"
			add("docs/research/")
		case strings.HasPrefix(file, "docs/"):
			add("docs/")
		}
		switch path.Base(file) {
		case "go.mod":
			conventions.TestCommand = valueOr(conventions.TestCommand, "go test ./...")
			conventions.BuildCommand = valueOr(conventions.BuildCommand, "go build ./...")
		case "Makefile":
			conventions.TestCommand = valueOr(conventions.TestCommand, "make test")
		case "package.json":
			conventions.TestCommand = valueOr(conventions.TestCommand, "npm test")
		}
	}
	if len(conventions.DocumentDirs) == 0 {
		conventions.DocumentDirs = []string{"docs/"}
	}
	return conventions
}

// Stage names the kind of product a document is. It selects which of the
// repository's own directories the document belongs in, so a research report
// lands where that repository keeps research and follows its conventions rather
// than in an agent-specific folder (AGENT-R-029).
type Stage string

const (
	StageRequirements Stage = "requirements"
	StagePlan         Stage = "plan"
	StageDesign       Stage = "design"
	StageResearch     Stage = "research"
)

// DirectoryFor returns the repository's own directory for a kind of document,
// and whether the repository has said where that kind goes. A repository that
// has not said is not guessed at: the caller asks, and an unanswered question
// becomes a question in the issue rather than a file in a plausible-looking
// place.
func (c RepoConventions) DirectoryFor(stage Stage) (string, bool) {
	var dir string
	switch stage {
	case StageRequirements:
		dir = c.RequirementsDir
	case StagePlan:
		dir = c.PlansDir
	case StageDesign:
		dir = c.DesignDir
	case StageResearch:
		dir = c.ResearchDir
	}
	if dir == "" {
		return "", false
	}
	return normalizeDir(dir), true
}

// ErrPathRefused is what a write outside the allowlist gets. It is a distinct
// error because refusing a path is a policy decision worth recording, not an
// I/O failure.
type ErrPathRefused struct {
	Path   string
	Reason string
}

func (e *ErrPathRefused) Error() string {
	return fmt.Sprintf("refusing to write %s: %s", e.Path, e.Reason)
}

// CheckDocumentPath decides whether a document may be written where the agent
// asked. This is the control plane's own decision and is made before any
// credential is anywhere near the repository, which is the point: the agent
// never holds git write access during discussion or planning, so an
// out-of-bounds write is impossible rather than merely detected
// (AGENT-R-024, AGENT-R-025).
func CheckDocumentPath(candidate string, conventions RepoConventions, extraForbidden []string) error {
	clean := strings.TrimPrefix(path.Clean(strings.TrimSpace(candidate)), "/")
	if clean == "" || clean == "." {
		return &ErrPathRefused{Path: candidate, Reason: "the path is empty"}
	}
	// A traversal is refused on the cleaned form, so `docs/../../etc/passwd`
	// cannot pass by looking like it starts inside an allowed directory.
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return &ErrPathRefused{Path: candidate, Reason: "the path escapes the repository"}
	}
	if strings.HasPrefix(clean, ".git/") {
		return &ErrPathRefused{Path: candidate, Reason: "the git directory is never writable"}
	}
	for _, forbidden := range append(append([]string(nil), conventions.ForbiddenPaths...), extraForbidden...) {
		forbidden = strings.TrimPrefix(strings.TrimSpace(forbidden), "/")
		if forbidden == "" {
			continue
		}
		if clean == forbidden || strings.HasPrefix(clean, strings.TrimSuffix(forbidden, "/")+"/") {
			return &ErrPathRefused{Path: candidate,
				Reason: "it is under the forbidden path " + forbidden}
		}
	}
	for _, allowed := range conventions.DocumentDirs {
		if allowed != "" && strings.HasPrefix(clean+"/", allowed) {
			return nil
		}
	}
	return &ErrPathRefused{Path: candidate,
		Reason: "documents may only be written under " + strings.Join(conventions.DocumentDirs, ", ") +
			" (from " + conventions.Source + ")"}
}

// DocumentWriter commits agent-produced documents. The control plane does this,
// not the agent: during discussion and planning the agent's workspace holds no
// git write credential at all, so "the agent wrote somewhere it should not" is
// not a case that can arise (AGENT-R-024).
type DocumentWriter struct {
	Repo        Repo
	Issues      ForgejoIssues
	Outbox      *Outbox
	ForgejoURL  string
	Conventions RepoConventions
	// Author is the agent account the commit is attributed to, so the history
	// says which agent produced the document.
	Author string
	Email  string
	Now    func() time.Time
}

// Commit writes one document and returns it as an artifact addressed by the
// commit it landed in.
func (w *DocumentWriter) Commit(ctx context.Context, request DocumentRequest) (Artifact, error) {
	// A document of a kind the repository has placed must go where the
	// repository puts that kind. Writing a research report into the plans
	// directory passes the allowlist but breaks the repository's own
	// conventions, which is what AGENT-R-029 is about.
	if dir, declared := w.Conventions.DirectoryFor(Stage(request.Stage)); declared {
		if !strings.HasPrefix(strings.TrimPrefix(path.Clean(request.Path), "/")+"/", dir) {
			return Artifact{}, &ErrPathRefused{Path: request.Path,
				Reason: string(request.Stage) + " documents belong under " + dir +
					" in this repository (from " + w.Conventions.Source + ")"}
		}
	}
	if err := CheckDocumentPath(request.Path, w.Conventions, request.ExtraForbidden); err != nil {
		return Artifact{}, err
	}
	branch := valueOr(request.Branch, w.Conventions.TargetBranch)
	// An existing file needs its blob SHA, or the contents API refuses the
	// write as a create-over-existing.
	sha := ""
	if existing, err := w.Issues.FileContents(ctx, w.Repo, request.Path, branch); err == nil {
		sha = existing.SHA
	} else if !isStatus(err, 404) {
		return Artifact{}, err
	}

	message := request.Message
	if message == "" {
		message = fmt.Sprintf("docs: %s for issue #%d", request.Stage, request.Issue)
	}
	// Every commit names the issue and the stage, so `git log` alone explains
	// why a file changed.
	message = strings.TrimSpace(message) + fmt.Sprintf("\n\nissue #%d\nstage: %s\n", request.Issue, request.Stage)

	var commit FileCommit
	key := WriteKey(w.Repo, request.Issue, "document", request.Path+"@"+request.Version)
	written, err := w.Outbox.Do(ctx, key, request.RunID, "document", request.Path,
		func(ctx context.Context) error {
			var err error
			commit, err = w.Issues.PutFile(ctx, w.Repo, FileWrite{
				Path: request.Path, Content: request.Content, Message: message,
				Branch: branch, NewBranch: request.NewBranch, SHA: sha,
				Author: w.Author, Email: w.Email,
			})
			return err
		})
	if err != nil {
		return Artifact{}, err
	}
	if !written {
		// The same document at the same version was already committed. That is
		// the idempotent outcome, not a failure -- but there is no new commit
		// to point at, so the caller is told so.
		return Artifact{}, ErrAlreadyWritten
	}
	return Artifact{
		Path: request.Path, CommitSHA: commit.Commit.SHA, Summary: request.Summary,
		URL: PermalinkURL(w.ForgejoURL, w.Repo, commit.Commit.SHA, request.Path),
	}, nil
}

// ErrAlreadyWritten says the document was committed by an earlier, identical
// attempt.
var ErrAlreadyWritten = fmt.Errorf("this document version was already committed")

// DocumentRequest is one document the agent produced.
type DocumentRequest struct {
	Issue          int
	RunID          string
	Path           string
	Content        string
	Message        string
	Summary        string
	Stage          string
	Version        string
	Branch         string
	NewBranch      string
	ExtraForbidden []string
}

// FreezeBasis records what an approval binds to. Nothing about the frozen pair
// is re-derived later: execution reads this commit, and if either half changes
// the approval is void (AGENT-R-028).
func FreezeBasis(artifact Artifact, approver string, now time.Time) ExecutionBasis {
	return ExecutionBasis{
		Path: artifact.Path, CommitSHA: artifact.CommitSHA,
		FrozenAt: now.UTC(), ApprovedBy: approver,
	}
}

// BasisStillValid re-reads the frozen document and reports whether the approval
// still stands. It is checked again immediately before a job starts, because an
// approval can be minutes or days old and the document can have moved under it.
func BasisStillValid(ctx context.Context, issues ForgejoIssues, repo Repo, basis ExecutionBasis) (bool, string, error) {
	current, err := issues.FileContents(ctx, repo, basis.Path, basis.CommitSHA)
	if err != nil {
		if isStatus(err, 404) {
			return false, "the approved document no longer exists at the frozen commit", nil
		}
		return false, "", err
	}
	if current.Path != strings.TrimPrefix(basis.Path, "/") && current.Path != basis.Path {
		return false, "the approved path now resolves to " + current.Path, nil
	}
	return true, "", nil
}
