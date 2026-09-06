package main

import (
	"fmt"
	"sort"
	"strings"
)

// noResponse is what Forgejo renders for a field the submitter left empty.
// Treating it as a literal answer would set a scope of "_No response_".
const noResponse = "_No response_"

// Answers is the raw form response, keyed by the field's label as rendered into
// the issue body. Forgejo renders an answer as `### <label>` followed by the
// value; it does not write the field id, so the label is the only key
// available.
type Answers map[string]string

// ParseAnswers reads the answers out of an issue body. It is deliberately
// forgiving: a person can edit the body afterwards, fields can be reordered,
// and a template can gain or lose a field between the submission and the parse.
// None of that may cost the whole configuration, so an unreadable body yields
// no answers rather than an error, and the caller falls back to defaults with
// an explanation (AGENT-R-016).
func ParseAnswers(body string) Answers {
	answers := Answers{}
	var label string
	var value []string
	flush := func() {
		if label == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(value, "\n"))
		if text == noResponse {
			text = ""
		}
		// An unanswered field is recorded as empty rather than dropped: the set
		// of field labels is what identifies a rendered agent form, and dropping
		// the empty ones would make a mostly blank submission unrecognisable.
		// Get still returns "" for them, so no caller sees a placeholder.
		answers[normalizeLabel(label)] = text
		label, value = "", nil
	}
	inFence := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			if label != "" {
				value = append(value, line)
			}
			continue
		}
		// A heading inside a fence is content, not a field: an answer that
		// pastes a document would otherwise be split into phantom fields.
		if !inFence && strings.HasPrefix(line, "### ") {
			flush()
			label = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			continue
		}
		if label != "" {
			value = append(value, line)
		}
	}
	flush()
	return answers
}

// normalizeLabel makes lookup insensitive to case and surrounding punctuation,
// because a person editing the body by hand will not reproduce the label
// exactly.
func normalizeLabel(label string) string {
	return strings.ToLower(strings.TrimSpace(strings.Trim(label, " :：*_#")))
}

// Get reads one answer by the label the template used.
func (a Answers) Get(label string) string { return a[normalizeLabel(label)] }

// List reads a multi-select answer. Forgejo renders multiple choices as a
// comma-separated line; a person editing by hand tends to use a bullet list, so
// both are accepted.
func (a Answers) List(label string) []string {
	value := a.Get(label)
	if value == "" {
		return nil
	}
	var items []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*+ "))
		if line == "" {
			continue
		}
		for _, item := range strings.Split(line, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
	}
	return items
}

// Checked reports which checkbox entries were ticked.
func (a Answers) Checked(label string) []string {
	var checked []string
	for _, line := range strings.Split(a.Get(label), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- [x]") && !strings.HasPrefix(line, "- [X]") {
			continue
		}
		if item := strings.TrimSpace(line[5:]); item != "" {
			checked = append(checked, item)
		}
	}
	return checked
}

// agentFormFields are labels only an agent form produces. Two of them together
// is a strong enough signal, and requiring more than one keeps an ordinary
// issue that happens to have a "### Goal" heading out.
var agentFormFields = []string{"goal", "discussing agents", "acceptance criteria", "executing agent"}

// LooksLikeAgentIssue reports whether a body was produced by one of the
// generated forms.
//
// The body is the signal rather than the `ai:auto` label, because that label is
// not reliable: a template's front-matter labels are pre-selected in the web
// page and posted back by the browser, so an issue created any other way -- or
// by a person who unticked them -- carries none. Recognising the form from what
// it wrote is the part that cannot be switched off by accident.
func LooksLikeAgentIssue(body string) bool {
	answers := ParseAnswers(body)
	seen := 0
	for _, field := range agentFormFields {
		if _, present := answers[field]; present {
			seen++
		}
	}
	return seen >= 2
}

// IssueConfig is the configuration one issue runs under, after parsing,
// validation and narrowing.
type IssueConfig struct {
	Goal           string
	Acceptance     string
	Scope          string
	ForbiddenPaths []string
	References     string
	ChatAgents     []string
	HostAgent      string
	StopCondition  string
	ExecAgent      string
	Models         map[string]string
	Efforts        map[string]string
	Branch         string
	CommitDirectly bool
	Budget         string
	ExecuteWhen    string
	Risks          []string
	Language       string
	// Downgrades records every choice that was narrowed, in the order it was
	// decided. It is not diagnostics: AGENT-R-017 requires each narrowing to be
	// explained to the person who asked for it.
	Downgrades []Downgrade
}

// Downgrade is one narrowed choice.
type Downgrade struct {
	Field     string
	Requested string
	Applied   string
	Reason    string
}

func (d Downgrade) String() string {
	if d.Applied == "" {
		return fmt.Sprintf("%s: dropped %q -- %s", d.Field, d.Requested, d.Reason)
	}
	return fmt.Sprintf("%s: %q became %q -- %s", d.Field, d.Requested, d.Applied, d.Reason)
}

// Defaults are the deployment- and repository-level values an issue falls back
// to, and the ceiling it may not exceed.
type Defaults struct {
	ChatAgents  []string
	ExecAgent   string
	Budget      string
	Language    string
	ExecuteWhen string
	// AllowDirectCommit is the repository policy. An issue may turn direct
	// commit off but never on: configuration in an issue body can only narrow
	// authorization, never widen it (AGENT-R-036).
	AllowDirectCommit bool
	// Permitted is the action set the requester actually holds. A choice that
	// needs more than this is narrowed rather than refused, so a person with
	// reply rights still gets a working discussion.
	Permitted map[Action]bool
	// ForbiddenPaths comes from the repository configuration and is the floor:
	// an issue can add to it and cannot remove from it.
	ForbiddenPaths []string
}

// ResolveIssueConfig turns a form submission into the configuration the issue
// actually runs under. Everything it narrows is recorded with a reason, and an
// unreadable or absent answer is a fallback rather than a failure
// (AGENT-R-016, AGENT-R-017, AGENT-R-036).
func ResolveIssueConfig(answers Answers, registry *Registry, defaults Defaults) IssueConfig {
	config := IssueConfig{
		Goal:       answers.Get("Goal"),
		Acceptance: answers.Get("Acceptance criteria"),
		Scope:      answers.Get("Scope"),
		References: answers.Get("References"),
		Models:     map[string]string{},
		Efforts:    map[string]string{},
		Branch:     answers.Get("Working branch"),
		Risks:      answers.Checked("Risk declaration"),
	}

	// Forbidden paths union, never difference.
	config.ForbiddenPaths = append(config.ForbiddenPaths, defaults.ForbiddenPaths...)
	for _, path := range splitList(answers.Get("Paths that must not change")) {
		if !contains(config.ForbiddenPaths, path) {
			config.ForbiddenPaths = append(config.ForbiddenPaths, path)
		}
	}
	sort.Strings(config.ForbiddenPaths)

	config.ChatAgents = resolveAgents(answers.List("Discussing agents"), registry, ActionReply, &config)
	if len(config.ChatAgents) == 0 {
		config.ChatAgents = resolveAgents(defaults.ChatAgents, registry, ActionReply, &config)
	}
	if len(config.ChatAgents) == 0 {
		if entries := registry.Entries(); len(entries) > 0 {
			config.ChatAgents = []string{entries[0].ID}
			config.note("Discussing agents", "", entries[0].ID,
				"no readable choice and no repository default, so the first enabled runtime is used")
		}
	}

	config.HostAgent = resolveHost(answers.Get("Chair"), registry, &config)
	config.StopCondition = valueOr(answers.Get("When the discussion may end"), defaultStopCondition)

	config.ExecAgent = resolveExecutor(answers.Get("Executing agent"), registry, defaults, &config)
	config.Language = resolveLanguage(answers.Get("Reply language"), defaults, &config)
	config.Budget = resolveBudget(answers.Get("Budget tier"), defaults, &config)
	config.ExecuteWhen = valueOr(answers.Get("When to execute"), defaults.ExecuteWhen)

	// Model and effort are per-runtime, and only for runtimes actually taking
	// part: a model chosen for a runtime that was not selected is dropped
	// rather than silently kept.
	participating := append([]string(nil), config.ChatAgents...)
	if config.ExecAgent != "" && !contains(participating, config.ExecAgent) {
		participating = append(participating, config.ExecAgent)
	}
	for _, id := range participating {
		runtime, ok := registry.Lookup(id)
		if !ok {
			continue
		}
		if model := answers.Get("Model for " + id); model != "" {
			if err := runtime.ValidateModel(model); err != nil {
				config.note("Model for "+id, model, runtime.DefaultModel, err.Error())
				config.Models[id] = runtime.DefaultModel
			} else {
				config.Models[id] = model
			}
		}
		effort := answers.Get("Thinking effort for " + id)
		if effort == "" {
			continue
		}
		if err := runtime.ValidateEffort(effort); err != nil {
			if runtime.DefaultEffort != "" {
				config.note("Thinking effort for "+id, effort, runtime.DefaultEffort, err.Error())
				config.Efforts[id] = runtime.DefaultEffort
			} else {
				config.note("Thinking effort for "+id, effort, "", err.Error())
			}
			continue
		}
		config.Efforts[id] = effort
	}

	config.CommitDirectly = resolveCompletion(answers.Get("How to finish"), defaults, config.Risks, &config)
	return config
}

func (c *IssueConfig) note(field, requested, applied, reason string) {
	c.Downgrades = append(c.Downgrades, Downgrade{
		Field: field, Requested: requested, Applied: applied, Reason: reason,
	})
}

func resolveAgents(requested []string, registry *Registry, action Action, config *IssueConfig) []string {
	var resolved []string
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		runtime, ok := registry.Lookup(id)
		if !ok {
			config.note("Discussing agents", id, "", "that runtime is not enabled in this deployment")
			continue
		}
		if !runtime.Supports(action) {
			config.note("Discussing agents", id, "", fmt.Sprintf("%s cannot %s", id, action))
			continue
		}
		if !contains(resolved, id) {
			resolved = append(resolved, id)
		}
	}
	return resolved
}

func resolveHost(requested string, registry *Registry, config *IssueConfig) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	runtime, ok := registry.Lookup(requested)
	switch {
	case !ok:
		config.note("Chair", requested, "", "that runtime is not enabled in this deployment")
	case !runtime.Supports(ActionHost):
		config.note("Chair", requested, "", requested+" cannot chair a round table")
	case !contains(config.ChatAgents, requested):
		// A chair that is not at the table cannot start a round, so it is
		// added rather than dropped: the user clearly meant it to take part.
		config.ChatAgents = append(config.ChatAgents, requested)
		return requested
	default:
		return requested
	}
	// Fall back to the first discussing agent that can chair.
	for _, id := range config.ChatAgents {
		if runtime, ok := registry.Lookup(id); ok && runtime.Supports(ActionHost) {
			config.note("Chair", requested, id, "the chosen chair is unusable; the first capable discussing agent takes over")
			return id
		}
	}
	return ""
}

func resolveExecutor(requested string, registry *Registry, defaults Defaults, config *IssueConfig) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = defaults.ExecAgent
	}
	if requested == "" {
		return ""
	}
	if !defaults.Permitted[ActionExecute] {
		config.note("Executing agent", requested, "",
			"you do not hold execute authorization here, so the issue is discussion-only")
		return ""
	}
	runtime, ok := registry.Lookup(requested)
	if !ok {
		config.note("Executing agent", requested, "", "that runtime is not enabled in this deployment")
		return ""
	}
	if !runtime.Supports(ActionExecute) {
		config.note("Executing agent", requested, "", requested+" cannot execute")
		return ""
	}
	return requested
}

func resolveLanguage(requested string, defaults Defaults, config *IssueConfig) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return valueOr(defaults.Language, "en")
	}
	for _, supported := range []string{"en", "zh-CN"} {
		if strings.EqualFold(requested, supported) {
			return supported
		}
	}
	applied := valueOr(defaults.Language, "en")
	config.note("Reply language", requested, applied, "the orchestrator's own text exists only in en and zh-CN")
	return applied
}

func resolveBudget(requested string, defaults Defaults, config *IssueConfig) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return valueOr(defaults.Budget, "standard")
	}
	if !contains(budgetTiers, requested) {
		applied := valueOr(defaults.Budget, "standard")
		config.note("Budget tier", requested, applied,
			"allowed tiers are "+strings.Join(budgetTiers, ", "))
		return applied
	}
	// A tier above the deployment ceiling is narrowed to the ceiling.
	if defaults.Budget != "" && tierRank(requested) > tierRank(defaults.Budget) {
		config.note("Budget tier", requested, defaults.Budget,
			"this deployment's ceiling for you is "+defaults.Budget)
		return defaults.Budget
	}
	return requested
}

func tierRank(tier string) int {
	for index, candidate := range budgetTiers {
		if candidate == tier {
			return index
		}
	}
	return -1
}

func resolveCompletion(requested string, defaults Defaults, risks []string, config *IssueConfig) bool {
	requested = strings.TrimSpace(strings.ToLower(requested))
	if requested != "commit directly" {
		return false
	}
	if len(risks) > 0 {
		config.note("How to finish", "commit directly", "pull request",
			"a risk was declared ("+strings.Join(risks, "; ")+"), which always requires review")
		return false
	}
	if !defaults.AllowDirectCommit {
		config.note("How to finish", "commit directly", "pull request",
			"this repository's policy does not allow committing directly")
		return false
	}
	if !defaults.Permitted[ActionExecute] {
		config.note("How to finish", "commit directly", "pull request",
			"you do not hold execute authorization here")
		return false
	}
	return true
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
