package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func testConversation(t *testing.T) (*Conversation, *fakeIssues, *MemoryStore) {
	t.Helper()
	issues, store := newFakeIssues(), NewMemoryStore()
	issues.seedIssue(7, "alice", "### Goal\n\ndo it\n")
	return &Conversation{
		Repo: testRepo(t), Number: 7, Issues: issues, Store: store,
		Outbox: &Outbox{Store: store}, Registry: testRegistry(t),
		ForgejoURL: "https://git.example",
	}, issues, store
}

// AGENT-R-019: an issue has exactly one status comment, and it is edited in
// place forever rather than reposted.
func TestStatusCommentIsCreatedOnceAndEditedAfterwards(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	clock := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	conversation.Now = func() time.Time { return clock }

	status := Status{Phase: PhaseThinking, Since: clock, Config: IssueConfig{ChatAgents: []string{"codex"}}}
	if written, err := conversation.UpdateStatus(ctx, status); err != nil || !written {
		t.Fatalf("first UpdateStatus = %v, %v", written, err)
	}
	if issues.countCalls("CreateComment") != 1 {
		t.Fatalf("CreateComment calls = %d, want 1", issues.countCalls("CreateComment"))
	}

	clock = clock.Add(time.Minute)
	status.Phase = PhasePlanning
	if written, err := conversation.UpdateStatus(ctx, status); err != nil || !written {
		t.Fatalf("second UpdateStatus = %v, %v", written, err)
	}
	if issues.countCalls("CreateComment") != 1 {
		t.Fatal("the status was reposted instead of edited")
	}
	if issues.countCalls("EditComment") != 1 {
		t.Fatalf("EditComment calls = %d, want 1", issues.countCalls("EditComment"))
	}
	comments, _ := issues.Comments(ctx, testRepo(t), 7)
	if len(comments) != 1 {
		t.Fatalf("the issue holds %d comments; the status must be the only one", len(comments))
	}
}

// A restarted container must find the existing status comment rather than post
// a second one.
func TestStatusCommentIsAdoptedAfterARestart(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	conversation.Now = func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
	if _, err := conversation.UpdateStatus(ctx, Status{Phase: PhaseThinking}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	// A fresh Conversation, as a rebuilt container would have.
	restarted := &Conversation{
		Repo: testRepo(t), Number: 7, Issues: issues, Store: NewMemoryStore(),
		Outbox: &Outbox{Store: NewMemoryStore()}, Registry: testRegistry(t),
		ForgejoURL: "https://git.example",
		Now:        func() time.Time { return time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC) },
	}
	if err := restarted.AdoptStatusComment(ctx, []string{"agent-codex"}); err != nil {
		t.Fatalf("AdoptStatusComment: %v", err)
	}
	if restarted.StatusCommentID() == 0 {
		t.Fatal("the existing status comment was not adopted")
	}
	if _, err := restarted.UpdateStatus(ctx, Status{Phase: PhaseReview}); err != nil {
		t.Fatalf("UpdateStatus after restart: %v", err)
	}
	if issues.countCalls("CreateComment") != 1 {
		t.Fatal("the restarted orchestrator posted a second status comment")
	}
}

// A comment carrying the marker but written by someone else is not adopted:
// otherwise anyone could take over the status comment by pasting the marker.
func TestOnlyOurOwnStatusCommentIsAdopted(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	if _, err := issues.CreateComment(ctx, testRepo(t), 7, statusMarker+"\nnot ours"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	issues.mu.Lock()
	for _, comment := range issues.comments {
		comment.User.Login = "mallory"
	}
	issues.mu.Unlock()
	if err := conversation.AdoptStatusComment(ctx, []string{"agent-codex"}); err != nil {
		t.Fatalf("AdoptStatusComment: %v", err)
	}
	if conversation.StatusCommentID() != 0 {
		t.Fatal("a comment from another account was adopted as the status comment")
	}
}

// AGENT-R-055: progress updates are throttled, but a terminal phase is never
// suppressed -- that is the update a reader is waiting for.
func TestStatusUpdatesAreThrottledExceptWhenFinal(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	clock := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	conversation.Now = func() time.Time { return clock }

	if _, err := conversation.UpdateStatus(ctx, Status{Phase: PhaseThinking, TokensUsed: 1}); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	clock = clock.Add(time.Second)
	written, err := conversation.UpdateStatus(ctx, Status{Phase: PhaseThinking, TokensUsed: 2})
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if written {
		t.Fatal("an update one second later was written; progress must be throttled")
	}
	written, err = conversation.UpdateStatus(ctx, Status{Phase: PhaseFailed, TokensUsed: 3})
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if !written {
		t.Fatal("a terminal phase was suppressed by the throttle")
	}
	_ = issues
}

// An unchanged status writes nothing at all.
func TestUnchangedStatusWritesNothing(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	clock := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	conversation.Now = func() time.Time { return clock }
	status := Status{Phase: PhaseThinking, Since: clock}
	if _, err := conversation.UpdateStatus(ctx, status); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	clock = clock.Add(time.Hour)
	written, err := conversation.UpdateStatus(ctx, status)
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if written {
		t.Fatal("an identical status was rewritten")
	}
	if issues.countCalls("EditComment") != 0 {
		t.Fatal("an identical status caused an API call")
	}
}

// AGENT-R-019: a conversational reply is a new comment, and it is idempotent so
// a redelivery does not double it.
func TestRepliesAreNewCommentsAndIdempotent(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := conversation.Say(ctx, "run-1", "turn-1", "Here is what I found."); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if issues.countCalls("CreateComment") != 1 {
		t.Fatalf("CreateComment calls = %d; a repeated reply must be written once", issues.countCalls("CreateComment"))
	}
	comments, _ := issues.Comments(ctx, testRepo(t), 7)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Here is what I found.") {
		t.Fatalf("comments = %+v", comments)
	}
	// The reply carries the run marker, so the event it causes is recognised as
	// this orchestrator's own.
	if RunIDFromPayload([]byte(`{"comment":{"body":`+quote(comments[0].Body)+`}}`)) != "run-1" {
		t.Fatal("the reply carries no recoverable run marker")
	}
}

// A different turn is a different reply.
func TestDifferentTurnsProduceDifferentReplies(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	if _, err := conversation.Say(ctx, "run-1", "turn-1", "first"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if _, err := conversation.Say(ctx, "run-1", "turn-2", "second"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if issues.countCalls("CreateComment") != 2 {
		t.Fatalf("CreateComment calls = %d, want 2", issues.countCalls("CreateComment"))
	}
}

// AGENT-R-019: receiving an instruction with nothing to say yet is a reaction,
// never a placeholder comment.
func TestAcknowledgementIsAReactionNotAComment(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	ctx := context.Background()
	comment, err := issues.CreateComment(ctx, testRepo(t), 7, "/plan")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := issues.countCalls("CreateComment")
	if err := conversation.Acknowledge(ctx, comment.ID, ReactionAcknowledged); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if issues.countCalls("CreateComment") != before {
		t.Fatal("acknowledging posted a comment")
	}
	if len(issues.reactions[comment.ID]) != 1 {
		t.Fatal("no reaction was recorded")
	}
}

// AGENT-R-017 and AGENT-R-026: the rendered status explains every narrowing and
// links artifacts by commit, never by branch.
func TestRenderedStatusExplainsAndLinksByCommit(t *testing.T) {
	status := Status{
		Phase: PhasePlanning,
		Config: IssueConfig{
			ChatAgents: []string{"codex"},
			Downgrades: []Downgrade{{Field: "Budget tier", Requested: "large", Applied: "small",
				Reason: "this deployment's ceiling for you is small"}},
		},
		Artifacts: []Artifact{{Path: "dev-docs/plans/x.md", CommitSHA: strings.Repeat("a", 40)}},
		Basis:     &ExecutionBasis{Path: "dev-docs/plans/x.md", CommitSHA: strings.Repeat("a", 40), ApprovedBy: "alice"},
	}
	body := RenderStatus(status, testRegistry(t), "https://git.example", testRepo(t))
	if !strings.Contains(body, statusMarker) {
		t.Fatal("the status comment carries no marker, so it cannot be found again")
	}
	if !strings.Contains(body, "this deployment's ceiling") {
		t.Fatal("the narrowing was not explained in the status comment")
	}
	if !strings.Contains(body, "/src/commit/"+strings.Repeat("a", 40)+"/") {
		t.Fatalf("artifact link is not a commit permalink: %s", body)
	}
	if strings.Contains(body, "/src/branch/") {
		t.Fatal("an artifact was linked by branch, which moves under the reader")
	}
	if !strings.Contains(body, "Execution basis (frozen)") {
		t.Fatal("the frozen execution basis is not shown")
	}
}

// AGENT-R-022: work stopped by a hard ceiling must not read as work that
// concluded.
func TestTruncationIsVisibleInTheStatus(t *testing.T) {
	body := RenderStatus(Status{Phase: PhaseReview, Truncated: "the round limit (8) was reached"},
		testRegistry(t), "https://git.example", testRepo(t))
	if !strings.Contains(body, "Stopped by a hard limit") || !strings.Contains(body, "round limit") {
		t.Fatalf("the ceiling that stopped the work is not stated: %s", body)
	}
}

func TestStatusCreationFailureIsReported(t *testing.T) {
	conversation, issues, _ := testConversation(t)
	issues.failOn["CreateComment"] = errors.New("forgejo refused")
	if _, err := conversation.UpdateStatus(context.Background(), Status{Phase: PhaseThinking}); err == nil {
		t.Fatal("UpdateStatus reported success although the comment could not be created")
	}
}

func quote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	return `"` + escaped + `"`
}
