package main

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// templateDir is where Forgejo looks for issue form templates.
const templateDir = ".forgejo/issue_template"

// Field ids are the contract between the generated template and the answer
// parser. They are constants because a typo in either half would silently
// produce a form whose answers never parse.
const (
	FieldGoal        = "goal"
	FieldAcceptance  = "acceptance"
	FieldScope       = "scope"
	FieldForbidden   = "forbidden"
	FieldReferences  = "references"
	FieldChatAgents  = "chat-agents"
	FieldHostAgent   = "host-agent"
	FieldStopWhen    = "stop-when"
	FieldExecAgent   = "exec-agent"
	FieldBranch      = "branch"
	FieldCompletion  = "completion"
	FieldBudget      = "budget"
	FieldExecuteWhen = "execute-when"
	FieldRisks       = "risks"
	FieldLanguage    = "language"
	// FieldModel and FieldEffort are per-runtime and carry the runtime id, so
	// their ids are built rather than fixed.
	FieldModelPrefix  = "model-"
	FieldEffortPrefix = "effort-"
)

// TemplateFile is one generated file with the path it belongs at.
type TemplateFile struct {
	Path    string
	Content string
}

// formTemplate is the Forgejo issue form schema. Only the parts this module
// generates are modelled.
type formTemplate struct {
	Name   string      `yaml:"name"`
	About  string      `yaml:"about"`
	Title  string      `yaml:"title,omitempty"`
	Labels []string    `yaml:"labels,omitempty"`
	Body   []formBlock `yaml:"body"`
}

type formBlock struct {
	Type        string          `yaml:"type"`
	ID          string          `yaml:"id,omitempty"`
	Attributes  formAttributes  `yaml:"attributes"`
	Validations *formValidation `yaml:"validations,omitempty"`
}

type formAttributes struct {
	Label       string       `yaml:"label,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Placeholder string       `yaml:"placeholder,omitempty"`
	Value       string       `yaml:"value,omitempty"`
	Multiple    bool         `yaml:"multiple,omitempty"`
	Options     []any        `yaml:"options,omitempty"`
	Choices     []formChoice `yaml:"-"`
}

type formChoice struct {
	Label    string `yaml:"label"`
	Required bool   `yaml:"required,omitempty"`
}

type formValidation struct {
	Required bool `yaml:"required,omitempty"`
}

// issueConfig is `.forgejo/issue_template/config.yaml`.
type issueConfig struct {
	BlankIssuesEnabled bool          `yaml:"blank_issues_enabled"`
	ContactLinks       []contactLink `yaml:"contact_links,omitempty"`
}

type contactLink struct {
	Name  string `yaml:"name"`
	URL   string `yaml:"url"`
	About string `yaml:"about"`
}

// GenerateTemplates renders the whole template set from the registry. Nothing
// here names a runtime: the dropdowns, the per-runtime model and effort fields
// and the labels all come from registry entries, so adding a runtime changes
// the generated forms and no code (AGENT-R-015, AGENT-R-059).
func GenerateTemplates(registry *Registry, docsURL string) ([]TemplateFile, error) {
	if registry.Len() == 0 {
		return nil, fmt.Errorf("no agent runtime is enabled, so there is nothing to offer in a form")
	}
	files := make([]TemplateFile, 0, 6)
	for _, spec := range templateSpecs {
		template := spec.build(registry)
		encoded, err := encodeYAML(template)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", spec.file, err)
		}
		files = append(files, TemplateFile{Path: templateDir + "/" + spec.file, Content: encoded})
	}
	// Blank issues stay enabled: an ordinary issue between people must not be
	// forced through an agent form, and a blank issue does not wake an agent
	// until it is assigned or an agent is mentioned.
	config := issueConfig{BlankIssuesEnabled: true}
	if docsURL != "" {
		config.ContactLinks = []contactLink{{
			Name: "How the AI agents work", URL: docsURL,
			About: "Labels, comment commands, approval and what gets committed where",
		}}
	}
	encoded, err := encodeYAML(config)
	if err != nil {
		return nil, err
	}
	files = append(files, TemplateFile{Path: templateDir + "/config.yaml", Content: encoded})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// templateSpec describes one scenario. Forgejo issue forms have no conditional
// fields, so a scenario is its own template rather than one adaptive form --
// which is also why "round table" can require a chair while "task" has no such
// field at all.
type templateSpec struct {
	file    string
	name    string
	about   string
	title   string
	labels  []string
	fields  []string
	perTeam bool
}

var templateSpecs = []templateSpec{
	{
		file: "discuss.yaml", name: "Agent discussion",
		about:  "Work a requirement or a design out with an agent, and land the result as a document",
		title:  "[agent] ",
		labels: []string{LabelAuto},
		fields: []string{FieldGoal, FieldAcceptance, FieldScope, FieldForbidden, FieldReferences,
			FieldChatAgents, FieldExecAgent, FieldLanguage},
		perTeam: true,
	},
	{
		file: "roundtable.yaml", name: "Agent round table",
		about:  "Several agents discuss under one chair and converge on a document",
		title:  "[agent] ",
		labels: []string{LabelAuto},
		fields: []string{FieldGoal, FieldAcceptance, FieldScope, FieldForbidden, FieldReferences,
			FieldChatAgents, FieldHostAgent, FieldStopWhen, FieldExecAgent, FieldLanguage},
		perTeam: true,
	},
	{
		file: "task.yaml", name: "Agent task",
		about:  "A change that is already clear enough to execute",
		title:  "[agent] ",
		labels: []string{LabelAuto},
		fields: []string{FieldGoal, FieldAcceptance, FieldScope, FieldChatAgents, FieldExecAgent,
			FieldBranch, FieldCompletion, FieldBudget, FieldExecuteWhen, FieldRisks, FieldLanguage},
		perTeam: true,
	},
	{
		file: "bug.yaml", name: "Agent bug report",
		about:  "A defect for an agent to reproduce and fix",
		title:  "[agent] ",
		labels: []string{LabelAuto},
		fields: []string{FieldGoal, FieldAcceptance, FieldScope, FieldReferences, FieldChatAgents,
			FieldExecAgent, FieldBranch, FieldCompletion, FieldExecuteWhen, FieldRisks, FieldLanguage},
		perTeam: true,
	},
	{
		file: "research.yaml", name: "Agent research",
		about:   "Investigate and produce a research document; no code changes",
		title:   "[agent] ",
		labels:  []string{LabelAuto},
		fields:  []string{FieldGoal, FieldAcceptance, FieldReferences, FieldChatAgents, FieldLanguage},
		perTeam: true,
	},
}

func (s templateSpec) build(registry *Registry) formTemplate {
	template := formTemplate{Name: s.name, About: s.about, Title: s.title, Labels: s.labels}
	template.Body = append(template.Body, formBlock{
		Type: "markdown",
		Attributes: formAttributes{Value: "This form configures an agent. " +
			"Anything you leave empty falls back to the repository default, and any choice you are " +
			"not authorized for is narrowed with an explanation in the status comment."},
	})
	for _, field := range s.fields {
		template.Body = append(template.Body, buildField(field, registry)...)
	}
	if s.perTeam {
		// One model and one effort dropdown per enabled runtime. Effort uses
		// each runtime's own native values, and a runtime that does not grade
		// effort contributes no field at all rather than an invented one
		// (AGENT-R-061).
		for _, runtime := range registry.Entries() {
			template.Body = append(template.Body, formBlock{
				Type: "dropdown", ID: FieldModelPrefix + runtime.ID,
				Attributes: formAttributes{
					Label:       "Model for " + runtime.ID,
					Description: "Initial value only; change it later with /model.",
					Options:     toOptions(runtime.Models),
				},
			})
			if len(runtime.EffortLevels) == 0 {
				continue
			}
			template.Body = append(template.Body, formBlock{
				Type: "dropdown", ID: FieldEffortPrefix + runtime.ID,
				Attributes: formAttributes{
					Label:       "Thinking effort for " + runtime.ID,
					Description: "This runtime's own values; they do not mean the same as another runtime's.",
					Options:     toOptions(runtime.EffortLevels),
				},
			})
		}
	}
	return template
}

func buildField(field string, registry *Registry) []formBlock {
	switch field {
	case FieldGoal:
		return []formBlock{{Type: "textarea", ID: FieldGoal,
			Attributes:  formAttributes{Label: "Goal", Description: "What has to be done, and why."},
			Validations: &formValidation{Required: true}}}
	case FieldAcceptance:
		return []formBlock{{Type: "textarea", ID: FieldAcceptance,
			Attributes: formAttributes{Label: "Acceptance criteria",
				Description: "Leave empty and the agent drafts them into the document instead."}}}
	case FieldScope:
		return []formBlock{{Type: "input", ID: FieldScope,
			Attributes: formAttributes{Label: "Scope",
				Description: "Modules, subdirectories or packages the work is confined to."}}}
	case FieldForbidden:
		return []formBlock{{Type: "input", ID: FieldForbidden,
			Attributes: formAttributes{Label: "Paths that must not change",
				Description: "Added to the repository's own forbidden paths; it can only narrow them."}}}
	case FieldReferences:
		return []formBlock{{Type: "textarea", ID: FieldReferences,
			Attributes: formAttributes{Label: "References", Description: "Related issues, pull requests, documents or links."}}}
	case FieldChatAgents:
		return []formBlock{{Type: "dropdown", ID: FieldChatAgents,
			Attributes:  formAttributes{Label: "Discussing agents", Multiple: true, Options: toOptions(runtimeIDs(registry))},
			Validations: &formValidation{Required: true}}}
	case FieldHostAgent:
		hosts := registry.Hosts()
		ids := make([]string, 0, len(hosts))
		for _, host := range hosts {
			ids = append(ids, host.ID)
		}
		return []formBlock{{Type: "dropdown", ID: FieldHostAgent,
			Attributes: formAttributes{Label: "Chair",
				Description: "Must be one of the discussing agents. Only the chair may start a round.",
				Options:     toOptions(ids)},
			Validations: &formValidation{Required: true}}}
	case FieldStopWhen:
		return []formBlock{{Type: "textarea", ID: FieldStopWhen,
			Attributes: formAttributes{Label: "When the discussion may end",
				Description: "The chair's own test for convergence, not a switch for people. Hard ceilings on rounds, budget and wall clock apply regardless.",
				Value:       defaultStopCondition}}}
	case FieldExecAgent:
		return []formBlock{{Type: "dropdown", ID: FieldExecAgent,
			Attributes: formAttributes{Label: "Executing agent",
				Description: "Exactly one, or leave empty to discuss without executing.",
				Options:     toOptions(executorIDs(registry))}}}
	case FieldBranch:
		return []formBlock{{Type: "input", ID: FieldBranch,
			Attributes: formAttributes{Label: "Working branch", Description: "Empty means ai/<issue>-<version>."}}}
	case FieldCompletion:
		return []formBlock{{Type: "dropdown", ID: FieldCompletion,
			Attributes: formAttributes{Label: "How to finish",
				Options: toOptions([]string{"pull request", "commit directly"})}}}
	case FieldBudget:
		return []formBlock{{Type: "dropdown", ID: FieldBudget,
			Attributes: formAttributes{Label: "Budget tier", Options: toOptions(budgetTiers)}}}
	case FieldExecuteWhen:
		return []formBlock{{Type: "dropdown", ID: FieldExecuteWhen,
			Attributes: formAttributes{Label: "When to execute",
				Options: toOptions([]string{"after approval", "on approval", "overnight window", "wait for an event"})}}}
	case FieldRisks:
		return []formBlock{{Type: "checkboxes", ID: FieldRisks,
			Attributes: formAttributes{Label: "Risk declaration",
				Description: "Ticking any of these forces human approval and forbids committing directly.",
				Options:     toChoices(riskFactors)}}}
	case FieldLanguage:
		return []formBlock{{Type: "dropdown", ID: FieldLanguage,
			Attributes: formAttributes{Label: "Reply language",
				Description: "Empty means the repository default.",
				Options:     toOptions([]string{"en", "zh-CN"})}}}
	}
	return nil
}

// defaultStopCondition is the chair's default test. It is a prompt, and a user
// may replace or extend it; the hard ceilings are separate and cannot be
// written away (AGENT-R-022).
const defaultStopCondition = "The requirement's boundaries are clear; no disagreement is left open; " +
	"the acceptance criteria can be judged true or false; the risks are listed; " +
	"and nothing is left that needs a human decision."

var budgetTiers = []string{"small", "standard", "large"}

var riskFactors = []string{
	"Database or schema migration",
	"Credentials, keys or secrets",
	"Deleting data or files",
	"External systems or third-party services",
}

func runtimeIDs(registry *Registry) []string {
	ids := make([]string, 0, registry.Len())
	for _, runtime := range registry.Entries() {
		ids = append(ids, runtime.ID)
	}
	return ids
}

func executorIDs(registry *Registry) []string {
	var ids []string
	for _, runtime := range registry.Entries() {
		if runtime.Supports(ActionExecute) {
			ids = append(ids, runtime.ID)
		}
	}
	return ids
}

func toOptions(values []string) []any {
	options := make([]any, 0, len(values))
	for _, value := range values {
		options = append(options, value)
	}
	return options
}

func toChoices(values []string) []any {
	options := make([]any, 0, len(values))
	for _, value := range values {
		options = append(options, map[string]any{"label": value})
	}
	return options
}

func encodeYAML(value any) (string, error) {
	var out strings.Builder
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return "# Generated by the ANAS ai_agent module from its runtime registry.\n" +
		"# Edit the registry or the module configuration, not this file: it is\n" +
		"# regenerated whenever the enabled runtimes change.\n" + out.String(), nil
}
