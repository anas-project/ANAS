package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Splitter carves converged work out of a discussion and summarises a
// discussion that has run long (AGENT-R-067).
//
// Both operations exist for the same reason: a discussion grows sub-problems,
// and without them there are only two bad endings -- pile everything into one
// issue until the context is unusable, or copy and paste into a fresh issue and
// lose the tie to the documents and the branch.
type Splitter struct {
	Repo       Repo
	Issues     ForgejoIssues
	Outbox     *Outbox
	Documents  *DocumentWriter
	ForgejoURL string
	Now        func() time.Time
}

func (s *Splitter) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// SplitRequest is one piece of a discussion becoming its own issue.
type SplitRequest struct {
	Parent int
	RunID  string
	Title  string
	// Goal is what the child issue is for, in the requester's words. Everything
	// else is inherited.
	Goal string
	// Inherit is the parent's resolved configuration. The child starts from it
	// so that the agents, budget and completion rules do not silently change
	// when work moves to a new issue; a person can narrow it there afterwards
	// the same way they would on any issue.
	Inherit IssueConfig
	// Labels are the agent labels to carry over, already resolved to ids.
	Labels []int64
}

// SplitResult is what the split produced.
type SplitResult struct {
	Child int
	URL   string
	// Blocked records whether the dependency was registered. A dependency the
	// instance refuses (the feature is off) must not fail the split: the child
	// issue and the cross references are still the useful part.
	Blocked bool
	Note    string
}

// Split creates the child issue, records that the parent is blocked by it, and
// cross-references both ways.
func (s *Splitter) Split(ctx context.Context, request SplitRequest) (SplitResult, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return SplitResult{}, fmt.Errorf("a split needs a title")
	}
	goal := strings.TrimSpace(request.Goal)
	if goal == "" {
		goal = "Split out of #" + strconv.Itoa(request.Parent) + "."
	}

	child := request.Inherit
	child.Goal = goal
	// The child is a fresh piece of work: it inherits how the work is done, not
	// the parent's frozen artefacts or its schedule.
	child.ExecuteWhen = ""
	body := RenderIssueForm(child, request.Parent)

	var created Issue
	key := WriteKey(s.Repo, request.Parent, "split", title)
	written, err := s.Outbox.Do(ctx, key, request.RunID, "split", title,
		func(ctx context.Context) error {
			var err error
			created, err = s.Issues.CreateIssue(ctx, s.Repo, title, body, request.Labels)
			return err
		})
	if err != nil {
		return SplitResult{}, err
	}
	if !written {
		// An identical split already ran. Repeating a command must not open a
		// second issue (AGENT-R-044).
		return SplitResult{}, ErrAlreadySplit
	}

	result := SplitResult{Child: created.Number,
		URL: fmt.Sprintf("%s/%s/%s/issues/%d", strings.TrimRight(s.ForgejoURL, "/"),
			s.Repo.Owner, s.Repo.Name, created.Number)}

	// The parent is blocked by the child: the child has to land first. The hard
	// order lives in Forgejo's own dependency graph so it shows on both issues
	// without anyone reading the queue.
	if err := s.Issues.AddDependency(ctx, s.Repo, request.Parent, s.Repo, created.Number); err != nil {
		result.Note = "the dependency could not be recorded: " + err.Error()
	} else {
		result.Blocked = true
	}

	parentRef := "#" + strconv.Itoa(request.Parent)
	childRef := "#" + strconv.Itoa(created.Number)
	if _, err := s.comment(ctx, request.RunID, request.Parent, "split-parent-"+childRef,
		"Split "+childRef+" out of this issue"+blockedClause(result.Blocked)+
			"\n\n> "+goal+"\n\n"+result.Note); err != nil {
		return result, err
	}
	if _, err := s.comment(ctx, request.RunID, created.Number, "split-child-"+parentRef,
		"Split out of "+parentRef+", which is blocked until this lands."+
			"\n\nConfiguration was inherited from "+parentRef+"; narrow it here if this piece needs less."); err != nil {
		return result, err
	}
	return result, nil
}

// ErrAlreadySplit says an identical split already created its issue.
var ErrAlreadySplit = fmt.Errorf("this split already created its issue")

func blockedClause(blocked bool) string {
	if blocked {
		return ", which now blocks it"
	}
	return ""
}

func (s *Splitter) comment(ctx context.Context, runID string, issue int, key, body string) (bool, error) {
	writeKey := WriteKey(s.Repo, issue, "comment", key)
	return s.Outbox.Do(ctx, writeKey, runID, "comment", key, func(ctx context.Context) error {
		_, err := s.Issues.CreateComment(ctx, s.Repo, issue, body+"\n"+runMarker+runID+" -->")
		return err
	})
}

// SummaryRequest is a discussion being compressed into a document.
type SummaryRequest struct {
	Issue   int
	RunID   string
	Title   string
	Content string
	Version string
	Branch  string
	Stage   string
	Path    string
}

// Summarize commits the summary as a document and answers with a comment that
// links the commit. The summary goes into the repository rather than into a
// comment because that is what makes it survivable: versioned, diffable and
// reviewable, and still readable from the issue through a permalink
// (AGENT-R-024, AGENT-R-026).
func (s *Splitter) Summarize(ctx context.Context, request SummaryRequest) (Artifact, error) {
	if s.Documents == nil {
		return Artifact{}, fmt.Errorf("no document writer is configured")
	}
	stage := request.Stage
	if stage == "" {
		stage = string(StageRequirements)
	}
	version := request.Version
	if version == "" {
		version = "v1"
	}
	artifact, err := s.Documents.Commit(ctx, DocumentRequest{
		Issue: request.Issue, RunID: request.RunID, Path: request.Path,
		Content: request.Content, Stage: stage, Version: version,
		Branch:  request.Branch,
		Summary: request.Title,
		Message: fmt.Sprintf("docs: summarize the discussion on issue #%d", request.Issue),
	})
	if err != nil {
		return Artifact{}, err
	}
	body := "Summarised the discussion so far into [`" + artifact.Path + "`](" + artifact.URL + ")" +
		" at `" + shortSHA(artifact.CommitSHA) + "`."
	if _, err := s.comment(ctx, request.RunID, request.Issue, "summary-"+artifact.CommitSHA, body); err != nil {
		return artifact, err
	}
	return artifact, nil
}

// RenderIssueForm writes a configuration back out in the shape the generated
// templates produce, so a child issue is parsed by exactly the same code that
// parses a human submission -- inheritance is literal rather than a second
// representation that can drift (AGENT-R-015, AGENT-R-068).
func RenderIssueForm(config IssueConfig, parent int) string {
	var b strings.Builder
	field := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			value = "_No response_"
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", label, value)
	}
	goal := config.Goal
	if parent > 0 {
		goal = strings.TrimSpace(goal) + "\n\nSplit out of #" + strconv.Itoa(parent) + "."
	}
	field("Goal", goal)
	field("Acceptance criteria", config.Acceptance)
	field("Scope", config.Scope)
	field("Paths that must not change", strings.Join(config.ForbiddenPaths, ", "))
	field("References", config.References)
	field("Discussing agents", strings.Join(config.ChatAgents, ", "))
	field("Chair", config.HostAgent)
	field("When the discussion may end", config.StopCondition)
	field("Executing agent", config.ExecAgent)
	field("Working branch", config.Branch)
	field("How to finish", completionText(config.CommitDirectly))
	field("Budget tier", config.Budget)
	field("Reply language", config.Language)
	if len(config.Risks) > 0 {
		b.WriteString("### Risk declaration\n\n")
		for _, risk := range config.Risks {
			b.WriteString("- [x] " + risk + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
