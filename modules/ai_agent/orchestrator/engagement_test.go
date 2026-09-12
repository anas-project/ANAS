package main

import "testing"

func agentIssue(body string, assignees ...string) Issue {
	issue := Issue{Body: body}
	for _, login := range assignees {
		issue.Assignees = append(issue.Assignees, struct {
			Login string `json:"login"`
		}{Login: login})
	}
	return issue
}

func TestTemplateIssuesEngageWithoutBeingAsked(t *testing.T) {
	registry := testRegistry(t)
	decision := ShouldEngage(agentIssue(submittedBody), EngagementTrigger{Event: "issues"}, registry)
	if !decision.Engage {
		t.Fatalf("a form submission was ignored: %s", decision.Reason)
	}
}

// An ordinary issue between two people must not pull an agent in. This is the
// whole point of AGENT-R-068: the tracker stays usable for humans.
func TestOrdinaryIssuesAreLeftAlone(t *testing.T) {
	registry := testRegistry(t)
	issue := agentIssue("The deploy script fails on arm64. Steps:\n\n1. run it\n")
	decision := ShouldEngage(issue, EngagementTrigger{Event: "issues"}, registry)
	if decision.Engage {
		t.Fatal("an ordinary issue summoned an agent")
	}
	if decision.Reason == "" {
		t.Fatal("refusing to engage was not explained")
	}
}

func TestAssignmentEngagesAnOrdinaryIssue(t *testing.T) {
	registry := testRegistry(t)
	account := registry.Accounts()[0]
	issue := agentIssue("please have a look", account)
	decision := ShouldEngage(issue, EngagementTrigger{Event: "issue_assign"}, registry)
	if !decision.Engage {
		t.Fatalf("assigning an agent did not engage it: %s", decision.Reason)
	}
	if decision.Runtime == "" {
		t.Fatal("the engaged runtime was not identified")
	}
}

func TestMentionEngagesAnOrdinaryIssue(t *testing.T) {
	registry := testRegistry(t)
	account := registry.Accounts()[0]
	issue := agentIssue("plain issue")
	trigger := EngagementTrigger{Event: "issue_comment", Comment: "@" + account + " what do you think?"}
	decision := ShouldEngage(issue, trigger, registry)
	if !decision.Engage {
		t.Fatalf("a mention did not engage: %s", decision.Reason)
	}
}

// Naming the runtime in prose is not a summons, and a longer account name that
// merely starts with an agent's name is a different user.
func TestNearMissesDoNotEngage(t *testing.T) {
	registry := testRegistry(t)
	account := registry.Accounts()[0]
	issue := agentIssue("plain issue")
	for _, comment := range []string{
		"codex would probably do this better",
		"cc @" + account + "-staging please",
		"email agent@example.com",
	} {
		decision := ShouldEngage(issue, EngagementTrigger{Event: "issue_comment", Comment: comment}, registry)
		if decision.Engage {
			t.Errorf("%q engaged an agent", comment)
		}
	}
}

func TestDeclaredRiskBlocksAutomaticApproval(t *testing.T) {
	allowed, reason := AutoApprovalAllowed(IssueConfig{Risks: []string{"database migration"}})
	if allowed {
		t.Fatal("an issue that declared a risk was allowed to self-approve")
	}
	if reason == "" {
		t.Fatal("blocking automatic approval was not explained")
	}
	if allowed, _ := AutoApprovalAllowed(IssueConfig{}); !allowed {
		t.Fatal("an issue without declared risks was blocked")
	}
}
