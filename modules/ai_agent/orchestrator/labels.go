package main

import (
	"fmt"
	"sort"
	"strings"
)

// labelPrefix namespaces every label this module owns, so a repository's own
// labels are never mistaken for control signals and vice versa.
const labelPrefix = "ai:"

// LabelKind says what a label is for. The split matters because only the
// control labels are a person's to set: the status labels are the module's own
// output, and treating them as input would let anyone fake a running job.
type LabelKind string

const (
	// LabelControl is set by people and read by the orchestrator.
	LabelControl LabelKind = "control"
	// LabelStatus is written by the orchestrator and is read-only to people.
	LabelStatus LabelKind = "status"
)

// Label is one entry of the vocabulary.
type Label struct {
	Name        string
	Kind        LabelKind
	Color       string
	Description string
	// Action is the authorization this label demands. An empty action means the
	// label carries no authority of its own.
	Action Action
}

// Parameterised label families. A label in one of these families carries a
// value after the slash -- `ai:chat/codex`, `ai:target/main` -- and the value is
// what the orchestrator acts on.
const (
	LabelChat        = labelPrefix + "chat/"
	LabelHost        = labelPrefix + "host/"
	LabelExec        = labelPrefix + "exec/"
	LabelBranch      = labelPrefix + "branch/"
	LabelTarget      = labelPrefix + "target/"
	LabelExecuteWhen = labelPrefix + "exec-when/"
)

// Fixed labels.
const (
	LabelAuto        = labelPrefix + "auto"
	LabelManual      = labelPrefix + "manual"
	LabelSilent      = labelPrefix + "silent"
	LabelPlan        = labelPrefix + "plan"
	LabelApproved    = labelPrefix + "approved"
	LabelCancel      = labelPrefix + "cancel"
	LabelPullRequest = labelPrefix + "pr"
	LabelDirect      = labelPrefix + "direct"
	LabelAtRisk      = labelPrefix + "at-risk"
	LabelRunning     = labelPrefix + "running"
	LabelNeedsReview = labelPrefix + "needs-review"
	LabelFailed      = labelPrefix + "failed"
	LabelJob         = labelPrefix + "job"
	LabelQueue       = labelPrefix + "queue"
)

// controlColor and statusColor keep the two kinds visually distinct in the
// issue list, which is the only place a person sees them side by side.
const (
	controlColor = "#5319e7"
	statusColor  = "#0e8a16"
)

// LabelVocabulary builds the labels a deployment needs. The per-agent families
// come from the registry, so enabling a runtime creates its labels and no file
// anywhere names a runtime (AGENT-R-015, AGENT-R-059).
//
// Model and thinking effort are deliberately absent. They are adjusted
// repeatedly during a conversation, and every adjustment would emit an
// issue_label event and a timeline entry, turning the issue's history into a
// stream of parameter changes. Labels carry authorization and process control;
// run parameters are state, kept in the database and shown in the status
// comment (AGENT-R-020).
func LabelVocabulary(registry *Registry) []Label {
	labels := []Label{
		{LabelAuto, LabelControl, controlColor, "Agent takes part in this issue without being asked each time", ""},
		{LabelManual, LabelControl, controlColor, "Agent acts only when assigned or mentioned", ""},
		{LabelSilent, LabelControl, controlColor, "Agent withdraws from this issue", ""},
		{LabelPlan, LabelControl, controlColor, "Freeze the issue snapshot and produce a plan document", ActionPlan},
		{LabelApproved, LabelControl, controlColor, "Approve execution against the frozen document", ActionExecute},
		{LabelCancel, LabelControl, controlColor, "Interrupt the running job and destroy its instance", ActionReply},
		{LabelPullRequest, LabelControl, controlColor, "Finish by opening a pull request", ActionPlan},
		{LabelDirect, LabelControl, controlColor, "Commit straight to the target branch, if repository policy allows", ActionExecute},
		{LabelAtRisk, LabelStatus, statusColor, "The estimate does not fit before the due date", ""},
		{LabelRunning, LabelStatus, statusColor, "A job is running for this issue", ""},
		{LabelNeedsReview, LabelStatus, statusColor, "Work is finished and waiting for a person", ""},
		{LabelFailed, LabelStatus, statusColor, "The last job failed", ""},
		{LabelJob, LabelStatus, statusColor, "This is an execution issue split from a parent", ""},
		{LabelQueue, LabelStatus, statusColor, "This is the pinned queue overview", ""},
	}
	for _, runtime := range registry.Entries() {
		labels = append(labels,
			Label{LabelChat + runtime.ID, LabelControl, controlColor,
				"Discuss with " + runtime.ID, ActionReply},
			Label{LabelExec + runtime.ID, LabelControl, controlColor,
				"Execute with " + runtime.ID, ActionExecute},
		)
		if runtime.Supports(ActionHost) {
			labels = append(labels, Label{LabelHost + runtime.ID, LabelControl, controlColor,
				"Let " + runtime.ID + " chair the round table", ActionReply})
		}
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].Name < labels[j].Name })
	return labels
}

// Command is a comment command. Commands and labels are two spellings of the
// same intent: both go through one authorization decision, and a command writes
// its label back afterwards so the issue always shows the current state
// (AGENT-R-023).
type Command struct {
	Name string
	// Aliases carry the localized spellings. They are data rather than a
	// parser branch, so adding a language does not touch the dispatcher.
	Aliases []string
	// Action is the authorization the command demands.
	Action Action
	// Label is written back after the command runs. Empty means the command
	// changes state that labels deliberately do not carry.
	Label string
	// TakesValue says whether the rest of the line is an argument.
	TakesValue bool
	Summary    string
}

// commands is the whole command surface. It is a table rather than a switch so
// that the authorization decision, the label write-back and the help text
// cannot drift apart from the dispatch.
var commands = []Command{
	{Name: "plan", Aliases: []string{"出方案", "计划"}, Action: ActionPlan, Label: LabelPlan,
		Summary: "Freeze the snapshot and produce a plan document"},
	{Name: "approve", Aliases: []string{"批准"}, Action: ActionExecute, Label: LabelApproved,
		Summary: "Approve execution against the frozen document"},
	{Name: "stop", Aliases: []string{"停止", "中断"}, Action: ActionReply, Label: LabelCancel,
		Summary: "Interrupt the running job"},
	{Name: "chat", Aliases: []string{"讨论"}, Action: ActionReply, Label: LabelChat, TakesValue: true,
		Summary: "Set the discussing agents"},
	{Name: "exec", Aliases: []string{"执行者"}, Action: ActionExecute, Label: LabelExec, TakesValue: true,
		Summary: "Set the executing agent"},
	// Model and effort change no label on purpose; see LabelVocabulary.
	{Name: "model", Aliases: []string{"模型"}, Action: ActionReply, TakesValue: true,
		Summary: "Set the model for an agent"},
	{Name: "effort", Aliases: []string{"思考强度"}, Action: ActionReply, TakesValue: true,
		Summary: "Set the thinking effort, in the runtime's own values"},
	{Name: "branch", Aliases: []string{"分支"}, Action: ActionPlan, Label: LabelBranch, TakesValue: true,
		Summary: "Set the working branch"},
	{Name: "budget", Aliases: []string{"预算"}, Action: ActionExecute, TakesValue: true,
		Summary: "Set the budget tier"},
	{Name: "due", Aliases: []string{"截止"}, Action: ActionPlan, TakesValue: true,
		Summary: "Set the issue due date"},
	{Name: "execute", Aliases: []string{"执行时机"}, Action: ActionExecute, Label: LabelExecuteWhen, TakesValue: true,
		Summary: "Set when execution starts: now, at <time>, on <event>, hold"},
	{Name: "summarize", Aliases: []string{"总结"}, Action: ActionReply,
		Summary: "Summarize the discussion so far"},
	{Name: "split", Aliases: []string{"拆分"}, Action: ActionPlan,
		Summary: "Split the work into dependent issues"},
}

// LookupCommand resolves a command name or one of its aliases.
func LookupCommand(name string) (Command, bool) {
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return Command{}, false
}

// Commands lists the surface, for help text and generated documentation.
func Commands() []Command {
	out := make([]Command, len(commands))
	copy(out, commands)
	return out
}

// Invocation is one parsed command from a comment.
type Invocation struct {
	Command Command
	Value   string
	// Line is the original text, kept so a refusal can quote what was asked.
	Line string
}

// ParseCommands finds the commands in a comment body. A command is only
// recognised at the start of a line: an unanchored search would fire on a
// command quoted inside a sentence or a code block, and "the agent did what a
// quotation said" is exactly the failure this avoids.
func ParseCommands(body string) []Invocation {
	var found []Invocation
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "/") {
			continue
		}
		name, value, _ := strings.Cut(line[1:], " ")
		command, ok := LookupCommand(name)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if !command.TakesValue {
			value = ""
		}
		found = append(found, Invocation{Command: command, Value: value, Line: line})
	}
	return found
}

// LabelWriteBack returns the label a completed command should leave behind, so
// the issue shows the state a command put it in (AGENT-R-023). A command whose
// label is a family gets the value appended.
func (i Invocation) LabelWriteBack() string {
	if i.Command.Label == "" {
		return ""
	}
	if !strings.HasSuffix(i.Command.Label, "/") {
		return i.Command.Label
	}
	value := strings.TrimSpace(i.Value)
	if value == "" {
		return ""
	}
	// `/execute at 2026-09-07T02:00Z` becomes `ai:exec-when/at=2026-09-07T02:00Z`.
	if verb, argument, found := strings.Cut(value, " "); found {
		return i.Command.Label + verb + "=" + strings.TrimSpace(argument)
	}
	return i.Command.Label + value
}

// LabelValue extracts the value from a parameterised label.
func LabelValue(label, family string) (string, bool) {
	if !strings.HasPrefix(label, family) {
		return "", false
	}
	return strings.TrimPrefix(label, family), true
}

// IsStatusLabel reports whether a label is the orchestrator's own output. A
// person adding one by hand is not a control signal and must not be read as
// one.
func IsStatusLabel(registry *Registry, name string) bool {
	for _, label := range LabelVocabulary(registry) {
		if label.Name == name {
			return label.Kind == LabelStatus
		}
	}
	return false
}

// ActionForLabel reports the authorization a label demands.
func ActionForLabel(registry *Registry, name string) (Action, bool) {
	for _, label := range LabelVocabulary(registry) {
		if label.Name != name {
			continue
		}
		if label.Action == "" {
			return "", false
		}
		return label.Action, true
	}
	for family, action := range map[string]Action{
		LabelChat: ActionReply, LabelHost: ActionReply, LabelExec: ActionExecute,
		LabelBranch: ActionPlan, LabelTarget: ActionPlan, LabelExecuteWhen: ActionExecute,
	} {
		if strings.HasPrefix(name, family) {
			return action, true
		}
	}
	return "", false
}

// CommandHelp renders the command surface for a comment. It is generated from
// the same table the dispatcher uses, so documented and accepted commands
// cannot diverge.
func CommandHelp() string {
	var out strings.Builder
	out.WriteString("| Command | Needs | What it does |\n| --- | --- | --- |\n")
	for _, command := range commands {
		names := "`/" + command.Name + "`"
		for _, alias := range command.Aliases {
			names += " `/" + alias + "`"
		}
		out.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", names, command.Action, command.Summary))
	}
	return out.String()
}
