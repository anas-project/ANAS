package main

import "strings"

// EngagementTrigger is the event that might pull an agent into an issue.
// Event is the Forgejo webhook event name; Comment carries the comment body for
// the events that have one, and is empty otherwise.
type EngagementTrigger struct {
	Event   string
	Comment string
}

// Engagement is the answer to "should an agent act on this issue at all", with
// the reason recorded either way. The reason is not diagnostics: a person who
// expected a reply and did not get one deserves to be told why, and a person
// whose ordinary issue was left alone deserves the same guarantee in reverse.
type Engagement struct {
	Engage bool
	Reason string
	// Runtime is the agent the trigger addressed, when the trigger named one.
	Runtime string
}

// ShouldEngage decides whether an inbound issue event deserves an agent
// (AGENT-R-068). An issue created from one of the generated templates is
// agent work by construction. Anything else -- a plain issue between two
// people, a bug report filed the ordinary way -- is left alone until somebody
// asks, either by assigning an agent account or by mentioning one.
//
// The template check reads the issue body rather than its labels. The front
// matter's labels are only a pre-selection on the new-issue page: the browser
// posts them back, so an issue submitted with the checkbox cleared, or through
// the API, carries no label at all while still being a form submission.
func ShouldEngage(issue Issue, trigger EngagementTrigger, runtimes *Registry) Engagement {
	if runtimes == nil {
		return Engagement{Reason: "no agent runtimes are registered"}
	}
	if LooksLikeAgentIssue(issue.Body) {
		return Engagement{Engage: true, Reason: "issue was created from an agent template"}
	}
	for _, assignee := range issue.Assignees {
		if runtime, ok := runtimes.ByAccount(assignee.Login); ok {
			return Engagement{Engage: true, Runtime: runtime.ID,
				Reason: "issue is assigned to " + assignee.Login}
		}
	}
	if account, ok := mentionedAccount(trigger.Comment, runtimes); ok {
		runtime, _ := runtimes.ByAccount(account)
		return Engagement{Engage: true, Runtime: runtime.ID,
			Reason: "a comment mentioned " + account}
	}
	return Engagement{Reason: "issue was not created from an agent template and no agent is assigned or mentioned"}
}

// mentionedAccount finds the first agent account named with an @ in a comment.
// Matching is on the account name so that an agent is only summoned by its own
// handle; naming the runtime ("codex") in prose does not count.
func mentionedAccount(comment string, runtimes *Registry) (string, bool) {
	if strings.TrimSpace(comment) == "" {
		return "", false
	}
	for _, account := range runtimes.Accounts() {
		if account == "" {
			continue
		}
		if containsMention(comment, account) {
			return account, true
		}
	}
	return "", false
}

func containsMention(comment, account string) bool {
	needle := "@" + account
	for index := 0; ; {
		found := strings.Index(comment[index:], needle)
		if found < 0 {
			return false
		}
		at := index + found
		after := at + len(needle)
		if after == len(comment) || !isMentionRune(comment[after]) {
			return true
		}
		index = after
	}
}

// isMentionRune reports whether a byte would continue a Forgejo user name, so
// that "@agent-codex-staging" does not summon "agent-codex".
func isMentionRune(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.':
		return true
	}
	return false
}

// AutoApprovalAllowed reports whether an issue may ever be approved by policy
// rather than by a person (AGENT-R-066).
//
// Today every approval is a human applying a label, so this returns true for
// most issues and changes nothing. It exists because that will not stay true:
// the moment a low-risk class is allowed to self-approve, an issue whose author
// declared a risk must not slip through with it. Writing the gate now, while
// there is no automatic path to forget it on, is cheaper than remembering to
// add it later.
func AutoApprovalAllowed(config IssueConfig) (bool, string) {
	if len(config.Risks) > 0 {
		return false, "a risk was declared (" + strings.Join(config.Risks, "; ") +
			"), which always requires a person to approve"
	}
	return true, ""
}
