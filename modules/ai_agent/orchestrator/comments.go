package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// statusMarker identifies the one status comment an issue may have. It is an
// HTML comment so it is invisible in the rendered issue but survives an edit
// that keeps the body, which is what lets the comment be found again after a
// restart without keeping its id anywhere but the database.
const statusMarker = "<!-- anas-agent-status -->"

// Reactions the orchestrator uses. Receiving an instruction with nothing to say
// yet is answered with one of these and never with a placeholder comment: a
// placeholder has to be deleted or rewritten later, and both dirty the timeline
// and the notifications (AGENT-R-019).
const (
	ReactionAcknowledged = "eyes"
	ReactionQueued       = "rocket"
	ReactionDone         = "+1"
)

// Phase is what the status comment shows at the top.
type Phase string

const (
	PhaseIdle      Phase = "idle"
	PhaseThinking  Phase = "thinking"
	PhasePlanning  Phase = "planning"
	PhaseQueued    Phase = "queued"
	PhaseExecuting Phase = "executing"
	PhaseReview    Phase = "waiting for review"
	PhaseCancelled Phase = "cancelled"
	PhaseFailed    Phase = "failed"
)

// Status is everything the single status comment shows: the current phase, the
// configuration actually in effect, what was narrowed and why, the artifacts,
// and the usage so far. It is the one place to answer "what is happening with
// this issue".
type Status struct {
	Phase      Phase
	Since      time.Time
	Config     IssueConfig
	Artifacts  []Artifact
	Basis      *ExecutionBasis
	TokensUsed int
	CostUSD    float64
	Notes      []string
	// Truncated records that a hard ceiling stopped the work rather than the
	// work finishing. AGENT-R-022 requires that to be visible in the output.
	Truncated string
}

// Artifact is one committed product, always addressed by the commit it landed
// in so the link stays valid when the file later moves (AGENT-R-026).
type Artifact struct {
	Path      string
	CommitSHA string
	URL       string
	Summary   string
}

// ExecutionBasis is the frozen `(document path, commit SHA)` an approval binds
// to. Execution reads that commit and nothing else; the conversation is
// background (AGENT-R-028).
type ExecutionBasis struct {
	Path       string
	CommitSHA  string
	FrozenAt   time.Time
	ApprovedBy string
}

// Matches reports whether a basis still describes the same frozen pair. Any
// difference invalidates the approval rather than being reconciled: an approval
// is consent to a specific document at a specific commit.
func (b ExecutionBasis) Matches(path, sha string) bool {
	return b.Path == path && b.CommitSHA == sha
}

// RenderStatus builds the status comment body. It is deterministic so an
// unchanged status produces an identical body, which is what lets the caller
// skip a pointless edit and keeps the throttle honest.
func RenderStatus(status Status, registry *Registry, forgejoURL string, repo Repo) string {
	var out strings.Builder
	out.WriteString(statusMarker + "\n")
	out.WriteString("**Agent status — " + string(status.Phase) + "**")
	if !status.Since.IsZero() && status.Phase != PhaseIdle {
		out.WriteString(" _(since " + status.Since.UTC().Format(time.RFC3339) + ")_")
	}
	out.WriteString("\n\n")

	if status.Truncated != "" {
		out.WriteString("> **Stopped by a hard limit**: " + status.Truncated +
			". The result below is what had been reached, not a natural conclusion.\n\n")
	}

	out.WriteString("| Setting | Value |\n| --- | --- |\n")
	row := func(name, value string) {
		if value != "" {
			out.WriteString("| " + name + " | " + value + " |\n")
		}
	}
	row("Discussing", strings.Join(status.Config.ChatAgents, ", "))
	row("Chair", status.Config.HostAgent)
	row("Executing", status.Config.ExecAgent)
	row("Model", joinPairs(status.Config.Models))
	row("Thinking effort", joinPairs(status.Config.Efforts))
	row("Branch", status.Config.Branch)
	row("Finish by", completionText(status.Config.CommitDirectly))
	row("Budget", status.Config.Budget)
	row("When", status.Config.ExecuteWhen)
	row("Language", status.Config.Language)
	if len(status.Config.Risks) > 0 {
		row("Declared risks", strings.Join(status.Config.Risks, "; "))
	}
	out.WriteString("\n")

	// Every narrowing is explained, one line each, naming what was asked for
	// and why it could not stand (AGENT-R-017).
	if len(status.Config.Downgrades) > 0 {
		out.WriteString("**Adjusted from what was requested**\n\n")
		for _, downgrade := range status.Config.Downgrades {
			out.WriteString("- " + downgrade.String() + "\n")
		}
		out.WriteString("\n")
	}

	if status.Basis != nil {
		out.WriteString("**Execution basis (frozen)**: `" + status.Basis.Path + "` at `" +
			shortSHA(status.Basis.CommitSHA) + "`")
		if status.Basis.ApprovedBy != "" {
			out.WriteString(", approved by @" + status.Basis.ApprovedBy)
		}
		out.WriteString("\n\n")
	}

	if len(status.Artifacts) > 0 {
		out.WriteString("**Artifacts**\n\n")
		for _, artifact := range status.Artifacts {
			link := artifact.URL
			if link == "" {
				link = PermalinkURL(forgejoURL, repo, artifact.CommitSHA, artifact.Path)
			}
			out.WriteString("- [" + artifact.Path + "](" + link + ") `" + shortSHA(artifact.CommitSHA) + "`")
			if artifact.Summary != "" {
				out.WriteString(" — " + artifact.Summary)
			}
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}

	if status.TokensUsed > 0 || status.CostUSD > 0 {
		out.WriteString(fmt.Sprintf("**Usage**: %d tokens, $%.4f\n\n", status.TokensUsed, status.CostUSD))
	}
	for _, note := range status.Notes {
		out.WriteString("> " + note + "\n")
	}
	out.WriteString("\n<sub>This comment is maintained in place. Replies and decisions are separate " +
		"comments. Commands:</sub>\n\n" + CommandHelp())
	return out.String()
}

func completionText(direct bool) string {
	if direct {
		return "commit directly"
	}
	return "pull request"
}

func joinPairs(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, ", ")
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// PermalinkURL addresses a file at a specific commit. A branch link would move
// under the reader; a commit link is what makes a reference in a comment stay
// true after the file is edited or the convention changes (AGENT-R-026).
func PermalinkURL(forgejoURL string, repo Repo, sha, path string) string {
	return strings.TrimRight(forgejoURL, "/") + "/" + repo.Owner + "/" + repo.Name +
		"/src/commit/" + sha + "/" + strings.TrimPrefix(path, "/")
}

// RawPermalinkURL addresses the raw bytes at a specific commit.
func RawPermalinkURL(forgejoURL string, repo Repo, sha, path string) string {
	return strings.TrimRight(forgejoURL, "/") + "/" + repo.Owner + "/" + repo.Name +
		"/raw/commit/" + sha + "/" + strings.TrimPrefix(path, "/")
}

// statusThrottle is the shortest interval between two status edits. Progress is
// worth showing, but an edit per tool call turns the comment into a flicker and
// costs an API call each time (AGENT-R-055).
const statusThrottle = 20 * time.Second

// Conversation owns everything the orchestrator writes into one issue. It holds
// the rule that an issue has exactly one status comment, updated in place, and
// that anything worth reading as a message is a new comment.
type Conversation struct {
	Repo       Repo
	Number     int
	Issues     ForgejoIssues
	Store      Store
	Outbox     *Outbox
	Registry   *Registry
	ForgejoURL string
	Now        func() time.Time

	mu           sync.Mutex
	statusID     int64
	statusBody   string
	lastStatusAt time.Time
}

func (c *Conversation) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
}

// AdoptStatusComment finds the existing status comment. It runs at start-up and
// after a restart: the comment is identified by its marker rather than by an id
// held in memory, so a rebuilt container reattaches to the same comment instead
// of posting a second one (AGENT-R-003, AGENT-R-019).
func (c *Conversation) AdoptStatusComment(ctx context.Context, agentAccounts []string) error {
	comments, err := c.Issues.Comments(ctx, c.Repo, c.Number)
	if err != nil {
		return err
	}
	mine := map[string]bool{}
	for _, account := range agentAccounts {
		mine[account] = true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, comment := range comments {
		if !strings.Contains(comment.Body, statusMarker) || !mine[comment.User.Login] {
			continue
		}
		// The earliest one wins. A second status comment can only exist through
		// a fault, and adopting the earliest keeps the comment in the position
		// readers already know -- near the top of the issue.
		if c.statusID == 0 || comment.ID < c.statusID {
			c.statusID, c.statusBody = comment.ID, comment.Body
		}
	}
	return nil
}

// UpdateStatus writes the status comment, creating it once and editing it
// afterwards. It returns whether anything was written.
func (c *Conversation) UpdateStatus(ctx context.Context, status Status) (bool, error) {
	body := RenderStatus(status, c.Registry, c.ForgejoURL, c.Repo)
	c.mu.Lock()
	unchanged := body == c.statusBody
	tooSoon := c.now().Sub(c.lastStatusAt) < statusThrottle
	statusID := c.statusID
	c.mu.Unlock()

	if unchanged {
		return false, nil
	}
	// The throttle never suppresses a terminal phase: "finished" or "failed"
	// arriving twenty seconds late is exactly the case a reader is waiting on.
	if tooSoon && statusID != 0 && !terminalPhase(status.Phase) {
		return false, nil
	}

	if statusID == 0 {
		comment, err := c.Issues.CreateComment(ctx, c.Repo, c.Number, body)
		if err != nil {
			return false, err
		}
		c.mu.Lock()
		c.statusID, c.statusBody, c.lastStatusAt = comment.ID, body, c.now()
		c.mu.Unlock()
		return true, nil
	}
	if _, err := c.Issues.EditComment(ctx, c.Repo, statusID, body); err != nil {
		return false, err
	}
	c.mu.Lock()
	c.statusBody, c.lastStatusAt = body, c.now()
	c.mu.Unlock()
	return true, nil
}

// Say posts a conversational reply: an opinion, a question, a conclusion.
// These are always new comments, so the timeline stays readable, notifications
// still fire and each one can be quoted (AGENT-R-019). The write is idempotent
// under the given key.
func (c *Conversation) Say(ctx context.Context, runID, key, body string) (bool, error) {
	marked := body
	if runID != "" {
		marked = body + "\n\n" + RunMarker(runID)
	}
	return c.Outbox.Do(ctx, WriteKey(c.Repo, c.Number, "comment", key), runID, "comment",
		c.Repo.String()+"#"+itoa(c.Number), func(ctx context.Context) error {
			_, err := c.Issues.CreateComment(ctx, c.Repo, c.Number, marked)
			return err
		})
}

// Acknowledge reacts to a comment. It is the whole response when there is
// nothing to say yet.
func (c *Conversation) Acknowledge(ctx context.Context, commentID int64, reaction string) error {
	return c.Issues.React(ctx, c.Repo, commentID, reaction)
}

// StatusCommentID exposes the adopted comment for the record layer.
func (c *Conversation) StatusCommentID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusID
}

func terminalPhase(phase Phase) bool {
	switch phase {
	case PhaseReview, PhaseFailed, PhaseCancelled:
		return true
	}
	return false
}

func itoa(value int) string { return fmt.Sprintf("%d", value) }
