package main

import (
	"context"
	"fmt"
	"strings"
)

// InteractionReconciler keeps a repository's labels and issue form templates
// matching the runtime registry. It runs on every sweep, so enabling a runtime
// creates its labels and regenerates the forms without anyone editing a file
// (AGENT-R-015).
type InteractionReconciler struct {
	Repo       Repo
	Issues     ForgejoIssues
	Outbox     *Outbox
	Registry   *Registry
	ForgejoURL string
	DocsURL    string
	Author     string
	Log        func(string)
}

func (r *InteractionReconciler) log(format string, args ...any) {
	if r.Log == nil {
		return
	}
	r.Log(fmt.Sprintf(format, args...))
}

// LabelIndex maps label names to their repository ids, which is what the issue
// APIs need to write them.
type LabelIndex map[string]int64

// EnsureLabels creates the vocabulary and corrects drift in colour or
// description. It never deletes: a label removed here would be stripped off
// every issue that carries it, destroying history to enforce a cosmetic detail.
func (r *InteractionReconciler) EnsureLabels(ctx context.Context) (LabelIndex, error) {
	existing, err := r.Issues.RepoLabels(ctx, r.Repo)
	if err != nil {
		return nil, err
	}
	byName := map[string]RepoLabel{}
	for _, label := range existing {
		byName[label.Name] = label
	}
	index := LabelIndex{}
	for _, wanted := range LabelVocabulary(r.Registry) {
		current, present := byName[wanted.Name]
		if !present {
			created, err := r.Issues.CreateLabel(ctx, r.Repo, wanted)
			if err != nil {
				return nil, err
			}
			index[wanted.Name] = created.ID
			r.log("created label %s in %s", wanted.Name, r.Repo)
			continue
		}
		index[wanted.Name] = current.ID
		if strings.EqualFold(current.Color, strings.TrimPrefix(wanted.Color, "#")) &&
			current.Description == wanted.Description {
			continue
		}
		if _, err := r.Issues.UpdateLabel(ctx, r.Repo, current.ID, wanted); err != nil {
			return nil, err
		}
	}
	return index, nil
}

// EnsureTemplates regenerates the issue forms and, when they differ from what
// is committed, opens a pull request rather than pushing to the default branch.
// The templates are repository content: changing them is a change to the
// repository, and it goes through review like one (AGENT-R-015).
func (r *InteractionReconciler) EnsureTemplates(ctx context.Context, fingerprint string) (*PullRequest, error) {
	repository, err := r.Issues.Repository(ctx, r.Repo)
	if err != nil {
		return nil, err
	}
	files, err := GenerateTemplates(r.Registry, r.DocsURL)
	if err != nil {
		return nil, err
	}

	type pending struct {
		file TemplateFile
		sha  string
	}
	var changed []pending
	for _, file := range files {
		current, err := r.Issues.FileContents(ctx, r.Repo, file.Path, repository.DefaultBranch)
		switch {
		case err == nil:
			decoded, decodeErr := current.Decoded()
			if decodeErr != nil {
				return nil, decodeErr
			}
			if decoded == file.Content {
				continue
			}
			changed = append(changed, pending{file: file, sha: current.SHA})
		case isStatus(err, 404):
			changed = append(changed, pending{file: file})
		default:
			return nil, err
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}

	// The branch name carries a fingerprint of the registry state, so a second
	// reconciliation of the same state reuses the branch and the same outbox
	// key rather than opening a second pull request.
	branch := "ai/templates-" + fingerprint
	base := repository.DefaultBranch
	for index, item := range changed {
		newBranch := ""
		if index == 0 {
			newBranch = branch
		}
		target := branch
		if index == 0 {
			target = base
		}
		file, sha := item.file, item.sha
		key := WriteKey(r.Repo, 0, "template", file.Path+"@"+fingerprint)
		if _, err := r.Outbox.Do(ctx, key, "", "document", file.Path, func(ctx context.Context) error {
			_, err := r.Issues.PutFile(ctx, r.Repo, FileWrite{
				Path: file.Path, Content: file.Content, Branch: target, NewBranch: newBranch, SHA: sha,
				Message: "chore(agent): regenerate issue forms from the runtime registry\n\n" +
					"stage: template-sync\n",
				Author: r.Author, Email: r.Author + "@localhost.invalid",
			})
			return err
		}); err != nil {
			return nil, err
		}
	}

	var pull PullRequest
	key := WriteKey(r.Repo, 0, "template-pr", fingerprint)
	opened, err := r.Outbox.Do(ctx, key, "", "pull_request", branch, func(ctx context.Context) error {
		var err error
		pull, err = r.Issues.CreatePullRequest(ctx, r.Repo, branch, base,
			"Regenerate the agent issue forms",
			"The enabled agent runtimes changed, so the issue forms were regenerated from the "+
				"registry. They are generated files: edit the module configuration, not these.\n")
		return err
	})
	if err != nil {
		return nil, err
	}
	if !opened {
		return nil, nil
	}
	r.log("opened a pull request regenerating the issue forms of %s", r.Repo)
	return &pull, nil
}

// RegistryFingerprint identifies the registry state the templates were
// generated from, so an unchanged registry produces an unchanged branch name.
func RegistryFingerprint(registry *Registry) string {
	var parts []string
	for _, runtime := range registry.Entries() {
		parts = append(parts, runtime.ID+":"+runtime.DefaultModel+":"+
			strings.Join(runtime.Models, "|")+":"+strings.Join(runtime.EffortLevels, "|"))
	}
	return fingerprint(strings.Join(parts, ";"))[:12]
}

// SyncIssueLabels writes the configuration back onto the issue as labels, and
// assigns the discussing agents. Both are needed because a form can set labels
// but cannot assign, so assignment is always an API call after creation
// (AGENT-R-018).
//
// Model and effort are deliberately not written: they change repeatedly during
// a conversation and every change would emit an issue_label event
// (AGENT-R-020).
func (r *InteractionReconciler) SyncIssueLabels(ctx context.Context, issue Issue, config IssueConfig, index LabelIndex, agentAccount func(string) string) error {
	wanted := map[string]bool{}
	for _, agent := range config.ChatAgents {
		wanted[LabelChat+agent] = true
	}
	if config.HostAgent != "" {
		wanted[LabelHost+config.HostAgent] = true
	}
	if config.ExecAgent != "" {
		wanted[LabelExec+config.ExecAgent] = true
	}
	if config.CommitDirectly {
		wanted[LabelDirect] = true
	} else {
		wanted[LabelPullRequest] = true
	}

	var add []int64
	for name := range wanted {
		id, known := index[name]
		if !known {
			// A parameterised label the vocabulary does not pre-create, such as
			// ai:branch/<name>, is skipped rather than invented: creating
			// labels from issue content would let anyone fill the repository's
			// label list.
			continue
		}
		if !hasLabel(issue, name) {
			add = append(add, id)
		}
	}
	if len(add) > 0 {
		if err := r.Issues.AddLabels(ctx, r.Repo, issue.Number, add); err != nil {
			return err
		}
	}
	// Remove the agent labels that no longer apply, so the issue does not claim
	// an agent that was narrowed away.
	for _, current := range issue.Labels {
		if !strings.HasPrefix(current.Name, labelPrefix) || wanted[current.Name] {
			continue
		}
		if !isAgentFamily(current.Name) {
			continue
		}
		if err := r.Issues.RemoveLabel(ctx, r.Repo, issue.Number, current.ID); err != nil {
			return err
		}
	}

	assignees := make([]string, 0, len(config.ChatAgents))
	for _, agent := range config.ChatAgents {
		if account := agentAccount(agent); account != "" {
			assignees = append(assignees, account)
		}
	}
	if len(assignees) == 0 {
		return nil
	}
	return r.Issues.AssignIssue(ctx, r.Repo, issue.Number, assignees)
}

func isAgentFamily(name string) bool {
	for _, family := range []string{LabelChat, LabelHost, LabelExec} {
		if strings.HasPrefix(name, family) {
			return true
		}
	}
	return name == LabelPullRequest || name == LabelDirect
}

func hasLabel(issue Issue, name string) bool {
	for _, label := range issue.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}
