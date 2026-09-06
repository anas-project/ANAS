package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Onboarding creates the questionnaire issue a repository gets when agents are
// first enabled on it, and turns the answers into a committed
// `.anas-agent.yml` and a pull request. The whole flow is REST: nothing here
// clones the repository, which is what makes it usable on a repository the
// orchestrator has no checkout of and no write credential for
// (AGENT-R-027).
type Onboarding struct {
	Repo       Repo
	Issues     ForgejoIssues
	Outbox     *Outbox
	ForgejoURL string
	Defaults   Defaults
	Registry   *Registry
	Now        func() time.Time
}

func (o *Onboarding) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

// probePaths are the files worth reading to guess a repository's conventions.
// The list is short on purpose: this runs on every newly enabled repository,
// and a broad crawl would cost dozens of API calls to learn very little.
var probePaths = []string{
	ConfigPath, "AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md",
	"go.mod", "package.json", "Makefile",
	"dev-docs/requirements/index.md", "dev-docs/plans/index.md",
	"docs/architecture/index.md", "docs/research/index.md", "docs/index.md",
}

// Resolve walks the four levels and returns the first that answers. The level
// is recorded on the result, so a later refusal can say which authority it came
// from rather than just asserting a rule.
func (o *Onboarding) Resolve(ctx context.Context) (RepoConventions, error) {
	repository, err := o.Issues.Repository(ctx, o.Repo)
	if err != nil {
		return RepoConventions{}, err
	}
	branch := repository.DefaultBranch

	// Level 1: the versioned configuration file is authoritative.
	if content, err := o.Issues.FileContents(ctx, o.Repo, ConfigPath, branch); err == nil {
		decoded, decodeErr := content.Decoded()
		if decodeErr != nil {
			return RepoConventions{}, decodeErr
		}
		return ParseConventions(decoded)
	} else if !isStatus(err, 404) {
		return RepoConventions{}, err
	}

	// Level 3: infer from what the repository already looks like. Level 2, the
	// onboarding issue's conclusion, only exists once someone has answered it,
	// and answering it produces the level 1 file -- so there is nothing
	// separate to read here.
	var present []string
	for _, candidate := range probePaths {
		if candidate == ConfigPath {
			continue
		}
		if _, err := o.Issues.FileContents(ctx, o.Repo, candidate, branch); err == nil {
			present = append(present, candidate)
		}
	}
	if len(present) > 0 {
		conventions := InferConventions(present)
		conventions.TargetBranch = branch
		return conventions, nil
	}

	// Level 4.
	conventions := BuiltinConventions()
	conventions.TargetBranch = branch
	return conventions, nil
}

// OpenQuestionnaire creates the onboarding issue, pre-filled with what could be
// inferred so a person confirms rather than composes. It is idempotent under
// the outbox key, so enabling a repository twice does not open two issues.
func (o *Onboarding) OpenQuestionnaire(ctx context.Context, inferred RepoConventions) (Issue, error) {
	body := renderQuestionnaire(o.Repo, inferred, o.Registry, o.Defaults)
	var issue Issue
	key := WriteKey(o.Repo, 0, "onboarding", "questionnaire")
	written, err := o.Outbox.Do(ctx, key, "", "issue", o.Repo.String(), func(ctx context.Context) error {
		var err error
		issue, err = o.Issues.CreateIssue(ctx, o.Repo,
			"[agent] Set up how the agents work in this repository", body, nil)
		return err
	})
	if err != nil {
		return Issue{}, err
	}
	if !written {
		return Issue{}, ErrAlreadyWritten
	}
	return issue, nil
}

func renderQuestionnaire(repo Repo, inferred RepoConventions, registry *Registry, defaults Defaults) string {
	var out strings.Builder
	out.WriteString("Agents have been enabled for `" + repo.String() + "`. " +
		"Before they write anything, they need to know how this repository is organised.\n\n" +
		"The answers below are **guesses** (" + inferred.Source + "). " +
		"Correct anything that is wrong, then reply `/approve`: the orchestrator will commit " +
		"`" + ConfigPath + "` and open a pull request. Nothing is cloned and nothing is written " +
		"until that pull request is merged.\n\n")

	out.WriteString("### Where documents go\n\n")
	out.WriteString("| Question | Proposed answer |\n| --- | --- |\n")
	row := func(question, answer string) {
		if answer == "" {
			answer = "_(none found — please fill in)_"
		}
		out.WriteString("| " + question + " | `" + answer + "` |\n")
	}
	row("Requirements", inferred.RequirementsDir)
	row("Implementation plans", inferred.PlansDir)
	row("Design documents", inferred.DesignDir)
	row("Research", inferred.ResearchDir)
	row("Everything a document may be written into", strings.Join(inferred.DocumentDirs, ", "))
	row("Paths that must never change", strings.Join(inferred.ForbiddenPaths, ", "))

	out.WriteString("\n### How changes are made\n\n")
	out.WriteString("| Question | Proposed answer |\n| --- | --- |\n")
	row("Target branch", inferred.TargetBranch)
	row("Working branch pattern", inferred.BranchPattern)
	row("Pull request required", fmt.Sprintf("%t", inferred.RequirePR))
	row("Committing directly allowed", fmt.Sprintf("%t", inferred.AllowDirect))
	row("Commit message style", inferred.CommitStyle)

	out.WriteString("\n### How the code is checked\n\n")
	out.WriteString("| Question | Proposed answer |\n| --- | --- |\n")
	row("Tests", inferred.TestCommand)
	row("Lint", inferred.LintCommand)
	row("Build", inferred.BuildCommand)

	out.WriteString("\n### Which agents, by default\n\n")
	out.WriteString("| Question | Proposed answer |\n| --- | --- |\n")
	row("Discussing agents", strings.Join(defaults.ChatAgents, ", "))
	row("Executing agent", defaults.ExecAgent)
	row("Budget tier", defaults.Budget)
	row("Available runtimes", strings.Join(runtimeIDs(registry), ", "))

	out.WriteString("\n<details><summary>What will be committed</summary>\n\n```yaml\n")
	rendered, err := RenderConventions(inferred)
	if err != nil {
		rendered = "# could not render: " + err.Error() + "\n"
	}
	out.WriteString(rendered)
	out.WriteString("```\n\n</details>\n")
	return out.String()
}

// RenderConventions serialises the configuration file.
func RenderConventions(conventions RepoConventions) (string, error) {
	conventions.normalize()
	sort.Strings(conventions.DocumentDirs)
	body, err := encodeYAML(conventions)
	if err != nil {
		return "", err
	}
	return strings.Replace(body,
		"# Generated by the ANAS ai_agent module from its runtime registry.\n"+
			"# Edit the registry or the module configuration, not this file: it is\n"+
			"# regenerated whenever the enabled runtimes change.\n",
		"# How the ANAS AI agents work in this repository.\n"+
			"# This file is authoritative: it beats anything inferred and anything said\n"+
			"# in an issue. Change it through a pull request like any other change.\n", 1), nil
}

// Land commits the configuration and opens the pull request. It runs after a
// person has confirmed the questionnaire, and it is the whole of AGENT-R-027:
// a repository with no agent configuration ends up with one, reviewed, without
// anything being cloned.
func (o *Onboarding) Land(ctx context.Context, issue int, conventions RepoConventions, author string) (Artifact, PullRequest, error) {
	repository, err := o.Issues.Repository(ctx, o.Repo)
	if err != nil {
		return Artifact{}, PullRequest{}, err
	}
	rendered, err := RenderConventions(conventions)
	if err != nil {
		return Artifact{}, PullRequest{}, err
	}
	branch := fmt.Sprintf("ai/%d-onboarding", issue)

	var commit FileCommit
	key := WriteKey(o.Repo, issue, "onboarding", "config")
	written, err := o.Outbox.Do(ctx, key, "", "document", ConfigPath, func(ctx context.Context) error {
		var err error
		commit, err = o.Issues.PutFile(ctx, o.Repo, FileWrite{
			Path: ConfigPath, Content: rendered, Branch: repository.DefaultBranch,
			NewBranch: branch, Author: author, Email: author + "@localhost.invalid",
			Message: fmt.Sprintf("chore: describe how the AI agents work here\n\nissue #%d\nstage: onboarding\n", issue),
		})
		return err
	})
	if err != nil {
		return Artifact{}, PullRequest{}, err
	}
	if !written {
		return Artifact{}, PullRequest{}, ErrAlreadyWritten
	}

	var pull PullRequest
	pullKey := WriteKey(o.Repo, issue, "onboarding", "pull-request")
	if _, err := o.Outbox.Do(ctx, pullKey, "", "pull_request", branch, func(ctx context.Context) error {
		var err error
		pull, err = o.Issues.CreatePullRequest(ctx, o.Repo, branch, repository.DefaultBranch,
			"Describe how the AI agents work in this repository",
			fmt.Sprintf("Closes #%d.\n\nThis file is what the agents read before writing anything. "+
				"Merging it makes these conventions authoritative; changing them later is another "+
				"pull request against the same file.\n", issue))
		return err
	}); err != nil {
		return Artifact{}, PullRequest{}, err
	}

	artifact := Artifact{
		Path: ConfigPath, CommitSHA: commit.Commit.SHA, Summary: "repository agent conventions",
		URL: PermalinkURL(o.ForgejoURL, o.Repo, commit.Commit.SHA, ConfigPath),
	}
	return artifact, pull, nil
}
