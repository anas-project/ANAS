package main

import (
	"strings"
	"testing"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry([]string{"codex", "claude_code", "pi"}, map[string]string{
		"codex": strings.Repeat("a", 64), "claude_code": strings.Repeat("b", 64), "pi": strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func fullDefaults() Defaults {
	return Defaults{
		ChatAgents: []string{"codex"}, ExecAgent: "codex", Budget: "large", Language: "en",
		ExecuteWhen: "after approval", AllowDirectCommit: true,
		Permitted:      map[Action]bool{ActionReply: true, ActionPlan: true, ActionExecute: true},
		ForbiddenPaths: []string{"vendor/"},
	}
}

const submittedBody = `### Goal

Make the thing work.

### Acceptance criteria

It works.

### Scope

internal/thing

### Discussing agents

codex, claude_code

### Executing agent

codex

### Model for codex

gpt-5.1-codex

### Thinking effort for codex

high

### Reply language

zh-CN
`

// AGENT-R-016: the answers Forgejo renders into the body are read back.
func TestParseAnswersReadsRenderedFields(t *testing.T) {
	answers := ParseAnswers(submittedBody)
	if got := answers.Get("Goal"); got != "Make the thing work." {
		t.Fatalf("Goal = %q", got)
	}
	if got := answers.List("Discussing agents"); len(got) != 2 || got[0] != "codex" {
		t.Fatalf("Discussing agents = %v", got)
	}
}

// AGENT-R-016: the parser has to survive a body a person edited by hand --
// reordered fields, changed spacing, an absent field, a stray "_No response_".
func TestParseAnswersToleratesHandEditing(t *testing.T) {
	edited := "### Reply language\n\nen\n\n###  goal :\n\nStill the goal\n\n" +
		"### Scope\n\n_No response_\n\n### Not a field we know\n\nwhatever\n"
	answers := ParseAnswers(edited)
	if got := answers.Get("Goal"); got != "Still the goal" {
		t.Fatalf("Goal = %q; the parser must tolerate case and punctuation drift", got)
	}
	if got := answers.Get("Scope"); got != "" {
		t.Fatalf("Scope = %q; Forgejo's placeholder for an empty field is not an answer", got)
	}
	if got := answers.Get("Acceptance criteria"); got != "" {
		t.Fatalf("a missing field returned %q instead of nothing", got)
	}
}

// A pasted document full of headings must not be split into phantom fields.
func TestParseAnswersIgnoresHeadingsInsideCodeFences(t *testing.T) {
	body := "### Goal\n\nSee below\n\n```markdown\n### Scope\n\nnot a field\n```\n\n### Scope\n\nreal scope\n"
	answers := ParseAnswers(body)
	if got := answers.Get("Scope"); got != "real scope" {
		t.Fatalf("Scope = %q; a heading inside a fence is content", got)
	}
	if !strings.Contains(answers.Get("Goal"), "```") {
		t.Fatalf("Goal lost its fenced block: %q", answers.Get("Goal"))
	}
}

// A body that is not a form at all yields nothing, and the caller falls back to
// defaults rather than failing.
func TestParseAnswersReturnsNothingForAFreeformBody(t *testing.T) {
	if answers := ParseAnswers("just a normal issue someone wrote"); len(answers) != 0 {
		t.Fatalf("answers = %v, want none", answers)
	}
}

func TestResolveIssueConfigAcceptsAValidSubmission(t *testing.T) {
	config := ResolveIssueConfig(ParseAnswers(submittedBody), testRegistry(t), fullDefaults())
	if len(config.ChatAgents) != 2 || config.ExecAgent != "codex" {
		t.Fatalf("config = %+v", config)
	}
	if config.Models["codex"] != "gpt-5.1-codex" || config.Efforts["codex"] != "high" {
		t.Fatalf("model/effort = %v / %v", config.Models, config.Efforts)
	}
	if config.Language != "zh-CN" {
		t.Fatalf("language = %q", config.Language)
	}
	if len(config.Downgrades) != 0 {
		t.Fatalf("downgrades = %v, want none for a valid submission", config.Downgrades)
	}
}

// AGENT-R-017: every narrowed choice is explained, naming what was asked for
// and why it could not stand.
func TestEveryNarrowingIsExplained(t *testing.T) {
	body := `### Goal

do it

### Discussing agents

codex, not_a_runtime, pi

### Executing agent

pi

### Model for codex

some-other-model

### Thinking effort for codex

ultrathink

### Budget tier

enormous

### Reply language

kl-GL
`
	config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), fullDefaults())
	explained := map[string]Downgrade{}
	for _, downgrade := range config.Downgrades {
		explained[downgrade.Field+"/"+downgrade.Requested] = downgrade
		if downgrade.Reason == "" {
			t.Fatalf("downgrade %+v has no reason", downgrade)
		}
	}
	for _, want := range []string{
		"Discussing agents/not_a_runtime",
		"Executing agent/pi",
		"Model for codex/some-other-model",
		"Thinking effort for codex/ultrathink",
		"Budget tier/enormous",
		"Reply language/kl-GL",
	} {
		if _, ok := explained[want]; !ok {
			t.Errorf("no explanation for %s; recorded: %v", want, config.Downgrades)
		}
	}
	// pi cannot execute, so the issue becomes discussion-only rather than
	// silently executing with something else.
	if config.ExecAgent != "" {
		t.Fatalf("exec agent = %q; a runtime that cannot execute must not be substituted", config.ExecAgent)
	}
	// An effort value that belongs to a different runtime is not accepted here.
	if config.Efforts["codex"] == "ultrathink" {
		t.Fatal("codex accepted claude_code's effort value")
	}
}

// AGENT-R-036: configuration in an issue may only narrow. Direct commit is
// refused when the requester lacks execute, when policy forbids it, and when a
// risk is declared -- each with its own reason.
func TestIssueConfigCanOnlyNarrow(t *testing.T) {
	body := "### Goal\n\ndo it\n\n### Discussing agents\n\ncodex\n\n### How to finish\n\ncommit directly\n"

	permitted := fullDefaults()
	if config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), permitted); !config.CommitDirectly {
		t.Fatal("a permitted direct commit was refused")
	}

	noPolicy := fullDefaults()
	noPolicy.AllowDirectCommit = false
	if config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), noPolicy); config.CommitDirectly {
		t.Fatal("an issue turned direct commit on although repository policy forbids it")
	}

	noRights := fullDefaults()
	noRights.Permitted = map[Action]bool{ActionReply: true}
	config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), noRights)
	if config.CommitDirectly || config.ExecAgent != "" {
		t.Fatalf("config = %+v; a requester without execute must get neither", config)
	}

	risky := "### Goal\n\ndo it\n\n### Discussing agents\n\ncodex\n\n### How to finish\n\ncommit directly\n" +
		"\n### Risk declaration\n\n- [x] Credentials, keys or secrets\n- [ ] Deleting data or files\n"
	riskyConfig := ResolveIssueConfig(ParseAnswers(risky), testRegistry(t), fullDefaults())
	if riskyConfig.CommitDirectly {
		t.Fatal("a declared risk did not force review")
	}
	if len(riskyConfig.Risks) != 1 {
		t.Fatalf("risks = %v, want only the ticked one", riskyConfig.Risks)
	}
}

// The forbidden-path list is a union: an issue adds to the repository's, never
// removes from it.
func TestForbiddenPathsOnlyGrow(t *testing.T) {
	body := "### Goal\n\ndo it\n\n### Discussing agents\n\ncodex\n\n### Paths that must not change\n\ninternal/secret\n"
	config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), fullDefaults())
	if !contains(config.ForbiddenPaths, "vendor/") {
		t.Fatalf("forbidden = %v; the repository's own entry was dropped", config.ForbiddenPaths)
	}
	if !contains(config.ForbiddenPaths, "internal/secret") {
		t.Fatalf("forbidden = %v; the issue's addition was lost", config.ForbiddenPaths)
	}
}

// A budget above the ceiling is narrowed to the ceiling, not refused.
func TestBudgetIsCappedAtTheCeiling(t *testing.T) {
	body := "### Goal\n\ndo it\n\n### Discussing agents\n\ncodex\n\n### Budget tier\n\nlarge\n"
	capped := fullDefaults()
	capped.Budget = "small"
	config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), capped)
	if config.Budget != "small" {
		t.Fatalf("budget = %q, want the ceiling", config.Budget)
	}
	if len(config.Downgrades) == 0 {
		t.Fatal("capping the budget was not explained")
	}
}

// An unreadable body still produces a usable configuration from the defaults,
// rather than leaving the issue unconfigured.
func TestUnparseableBodyFallsBackToDefaults(t *testing.T) {
	config := ResolveIssueConfig(ParseAnswers("nothing structured here"), testRegistry(t), fullDefaults())
	if len(config.ChatAgents) == 0 {
		t.Fatal("no discussing agent was chosen")
	}
	if config.Language == "" || config.Budget == "" {
		t.Fatalf("config = %+v, want the defaults applied", config)
	}
}

// A chair that was not listed among the discussing agents is added rather than
// dropped: it cannot chair a table it is not at.
func TestChairIsAddedToTheTable(t *testing.T) {
	body := "### Goal\n\ndo it\n\n### Discussing agents\n\ncodex\n\n### Chair\n\nclaude_code\n"
	config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), fullDefaults())
	if config.HostAgent != "claude_code" {
		t.Fatalf("chair = %q", config.HostAgent)
	}
	if !contains(config.ChatAgents, "claude_code") {
		t.Fatalf("chat agents = %v, want the chair among them", config.ChatAgents)
	}
}

// A runtime that cannot chair is replaced by one that can, with an explanation.
func TestUnusableChairFallsBackToACapableOne(t *testing.T) {
	body := "### Goal\n\ndo it\n\n### Discussing agents\n\ncodex, pi\n\n### Chair\n\npi\n"
	config := ResolveIssueConfig(ParseAnswers(body), testRegistry(t), fullDefaults())
	if config.HostAgent != "codex" {
		t.Fatalf("chair = %q, want the capable discussing agent", config.HostAgent)
	}
	if len(config.Downgrades) == 0 {
		t.Fatal("replacing the chair was not explained")
	}
}

// A template's front-matter labels are pre-selected in the web page and posted
// back by the browser, so an issue can be a perfectly good agent submission and
// carry no label at all. Recognition therefore reads the body.
func TestAgentIssuesAreRecognisedFromTheirBody(t *testing.T) {
	if !LooksLikeAgentIssue(submittedBody) {
		t.Fatal("a rendered agent form was not recognised")
	}
	// Even a submission where every optional field was left empty is still
	// recognisably an agent form.
	mostlyEmpty := "### Goal\n\ndo it\n\n### Acceptance criteria\n\n_No response_\n\n" +
		"### Discussing agents\n\n_No response_\n"
	if !LooksLikeAgentIssue(mostlyEmpty) {
		t.Fatal("a mostly empty agent form was not recognised")
	}
	for _, ordinary := range []string{
		"Just a normal issue someone opened.",
		"### Goal\n\nships are nice\n",
		"## Steps to reproduce\n\n1. do a thing\n",
		"",
	} {
		if LooksLikeAgentIssue(ordinary) {
			t.Errorf("an ordinary issue was taken for an agent form: %q", ordinary)
		}
	}
}

// Recording an unanswered field must not turn Forgejo's placeholder into an
// answer anywhere a caller can see it.
func TestPlaceholderNeverSurfacesAsAValue(t *testing.T) {
	answers := ParseAnswers("### Scope\n\n_No response_\n\n### Goal\n\nreal\n")
	if got := answers.Get("Scope"); got != "" {
		t.Fatalf("Scope = %q, want empty", got)
	}
	if got := answers.List("Scope"); len(got) != 0 {
		t.Fatalf("Scope list = %v, want none", got)
	}
	if got := answers.Get("Goal"); got != "real" {
		t.Fatalf("Goal = %q", got)
	}
}
