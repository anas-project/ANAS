package main

import (
	"strings"
	"testing"
)

// AGENT-R-020: labels carry authorization and process control. Model and
// thinking effort are adjusted repeatedly during a conversation, and putting
// them in labels would turn the issue timeline into a stream of parameter
// changes.
func TestNoLabelCarriesAModelOrAnEffortValue(t *testing.T) {
	registry := testRegistry(t)
	var forbidden []string
	for _, runtime := range registry.Entries() {
		forbidden = append(forbidden, runtime.Models...)
		forbidden = append(forbidden, runtime.EffortLevels...)
	}
	for _, label := range LabelVocabulary(registry) {
		for _, value := range forbidden {
			if strings.Contains(label.Name, value) {
				t.Errorf("label %q carries the run parameter %q", label.Name, value)
			}
		}
		if strings.Contains(label.Name, "model") || strings.Contains(label.Name, "effort") {
			t.Errorf("label %q names a run parameter", label.Name)
		}
	}
	// The commands that change them exist and deliberately write no label.
	for _, name := range []string{"model", "effort"} {
		command, ok := LookupCommand(name)
		if !ok {
			t.Fatalf("/%s is not a command", name)
		}
		if command.Label != "" {
			t.Errorf("/%s writes back the label %q; run parameters are state, not labels", name, command.Label)
		}
	}
}

// AGENT-R-015: the per-agent label families follow the registry.
func TestAgentLabelsFollowTheRegistry(t *testing.T) {
	registry := testRegistry(t)
	names := map[string]bool{}
	for _, label := range LabelVocabulary(registry) {
		names[label.Name] = true
	}
	for _, runtime := range registry.Entries() {
		if !names[LabelChat+runtime.ID] {
			t.Errorf("no discussion label for %s", runtime.ID)
		}
		if names[LabelHost+runtime.ID] != runtime.Supports(ActionHost) {
			t.Errorf("chair label for %s does not match its capability", runtime.ID)
		}
	}
	single, err := NewRegistry([]string{"pi"}, map[string]string{"pi": strings.Repeat("c", 64)})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	for _, label := range LabelVocabulary(single) {
		if strings.Contains(label.Name, "codex") {
			t.Fatalf("label %q exists for a runtime that is not enabled", label.Name)
		}
	}
}

// AGENT-R-023: a command and its label are the same intent, need the same
// authorization, and the command writes its label back.
func TestCommandsAndLabelsAgreeOnAuthorization(t *testing.T) {
	registry := testRegistry(t)
	for _, command := range Commands() {
		if command.Label == "" {
			continue
		}
		name := command.Label
		if strings.HasSuffix(name, "/") {
			name += "codex"
		}
		action, ok := ActionForLabel(registry, name)
		if !ok {
			t.Errorf("/%s writes back %q, which demands no authorization", command.Name, name)
			continue
		}
		if action != command.Action {
			t.Errorf("/%s needs %q but its label %q needs %q", command.Name, command.Action, name, action)
		}
	}
}

func TestCommandWriteBackCarriesTheValue(t *testing.T) {
	for _, testCase := range []struct{ line, want string }{
		{"/plan", LabelPlan},
		{"/approve", LabelApproved},
		{"/exec codex", LabelExec + "codex"},
		{"/execute now", LabelExecuteWhen + "now"},
		{"/execute at 2026-09-07T02:00Z", LabelExecuteWhen + "at=2026-09-07T02:00Z"},
		{"/model codex=gpt-5.1-codex", ""},
		{"/summarize", ""},
	} {
		invocations := ParseCommands(testCase.line)
		if len(invocations) != 1 {
			t.Fatalf("%q parsed into %d commands", testCase.line, len(invocations))
		}
		if got := invocations[0].LabelWriteBack(); got != testCase.want {
			t.Errorf("%q wrote back %q, want %q", testCase.line, got, testCase.want)
		}
	}
}

// A command is only a command at the start of a line, and never inside a code
// fence: acting on a quoted command is how a comment quoting someone else ends
// up approving an execution.
func TestCommandsAreOnlyRecognisedWhereTheyAreCommands(t *testing.T) {
	body := "I think we should not run /approve yet.\n\n" +
		"```\n/approve\n```\n\n" +
		"> /approve\n\n" +
		"/plan\n"
	invocations := ParseCommands(body)
	if len(invocations) != 1 || invocations[0].Command.Name != "plan" {
		t.Fatalf("parsed %+v; only the real command at the start of a line counts", invocations)
	}
}

func TestLocalizedAliasesResolveToTheSameCommand(t *testing.T) {
	direct, ok := LookupCommand("approve")
	if !ok {
		t.Fatal("/approve is missing")
	}
	alias, ok := LookupCommand("批准")
	if !ok {
		t.Fatal("the localized alias for approve is missing")
	}
	if alias.Name != direct.Name || alias.Action != direct.Action {
		t.Fatalf("alias resolved to %+v, want the same command and authorization as %+v", alias, direct)
	}
}

// A status label is the orchestrator's own output. A person adding one by hand
// is not a control signal.
func TestStatusLabelsAreNotControlSignals(t *testing.T) {
	registry := testRegistry(t)
	for _, name := range []string{LabelRunning, LabelFailed, LabelAtRisk, LabelJob, LabelQueue, LabelNeedsReview} {
		if !IsStatusLabel(registry, name) {
			t.Errorf("%q is not marked as a status label", name)
		}
		if _, demands := ActionForLabel(registry, name); demands {
			t.Errorf("%q demands an authorization, which implies a person may set it", name)
		}
	}
	for _, name := range []string{LabelPlan, LabelApproved, LabelCancel} {
		if IsStatusLabel(registry, name) {
			t.Errorf("%q is a control label but is marked as status", name)
		}
	}
}

func TestApprovalNeedsExecuteAndPlanNeedsPlan(t *testing.T) {
	registry := testRegistry(t)
	if action, _ := ActionForLabel(registry, LabelApproved); action != ActionExecute {
		t.Fatalf("%s needs %q, want execute", LabelApproved, action)
	}
	if action, _ := ActionForLabel(registry, LabelPlan); action != ActionPlan {
		t.Fatalf("%s needs %q, want plan", LabelPlan, action)
	}
	if action, _ := ActionForLabel(registry, LabelExec+"codex"); action != ActionExecute {
		t.Fatalf("%s needs %q, want execute", LabelExec+"codex", action)
	}
}

func TestLabelValueExtraction(t *testing.T) {
	if value, ok := LabelValue(LabelBranch+"ai/12-v1", LabelBranch); !ok || value != "ai/12-v1" {
		t.Fatalf("LabelValue = %q, %v", value, ok)
	}
	if _, ok := LabelValue(LabelPlan, LabelBranch); ok {
		t.Fatal("a fixed label was read as a parameterised one")
	}
}

func TestCommandHelpIsGeneratedFromTheTable(t *testing.T) {
	help := CommandHelp()
	for _, command := range Commands() {
		if !strings.Contains(help, "`/"+command.Name+"`") {
			t.Errorf("the help text omits /%s", command.Name)
		}
	}
}
