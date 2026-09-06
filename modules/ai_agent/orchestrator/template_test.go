package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// AGENT-R-015: the whole template set comes from the registry.
func TestTemplatesAreGeneratedFromTheRegistry(t *testing.T) {
	files, err := GenerateTemplates(testRegistry(t), "https://docs.example/agents")
	if err != nil {
		t.Fatalf("GenerateTemplates: %v", err)
	}
	byPath := map[string]string{}
	for _, file := range files {
		byPath[file.Path] = file.Content
		if !strings.HasPrefix(file.Path, templateDir+"/") {
			t.Fatalf("%s is not under the Forgejo template directory", file.Path)
		}
	}
	for _, want := range []string{"discuss.yaml", "roundtable.yaml", "task.yaml", "bug.yaml", "research.yaml", "config.yaml"} {
		if _, ok := byPath[templateDir+"/"+want]; !ok {
			t.Errorf("missing generated template %s", want)
		}
	}

	// Every enabled runtime appears in the discussing dropdown, and nothing
	// else does.
	var discuss formTemplate
	if err := yaml.Unmarshal([]byte(byPath[templateDir+"/discuss.yaml"]), &discuss); err != nil {
		t.Fatalf("the generated template is not valid YAML: %v", err)
	}
	options := dropdownOptions(t, discuss, FieldChatAgents)
	if len(options) != 3 {
		t.Fatalf("discussing agents = %v, want one per enabled runtime", options)
	}

	// A runtime that cannot execute is absent from the executing dropdown: an
	// option that always downgrades is a trap, not a choice.
	executors := dropdownOptions(t, discuss, FieldExecAgent)
	for _, option := range executors {
		runtime, _ := testRegistry(t).Lookup(option)
		if !runtime.Supports(ActionExecute) {
			t.Fatalf("%q is offered as an executor but cannot execute", option)
		}
	}
}

// AGENT-R-061: effort options are each runtime's own values, and a runtime that
// does not grade effort contributes no field rather than an invented one.
func TestEffortFieldsUseNativeValuesAndAreOmittedWhenAbsent(t *testing.T) {
	files, err := GenerateTemplates(testRegistry(t), "")
	if err != nil {
		t.Fatalf("GenerateTemplates: %v", err)
	}
	var discuss formTemplate
	for _, file := range files {
		if strings.HasSuffix(file.Path, "discuss.yaml") {
			if err := yaml.Unmarshal([]byte(file.Content), &discuss); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
		}
	}
	registry := testRegistry(t)
	codex, _ := registry.Lookup("codex")
	options := dropdownOptions(t, discuss, FieldEffortPrefix+"codex")
	if len(options) != len(codex.EffortLevels) {
		t.Fatalf("codex effort options = %v, want its own %v", options, codex.EffortLevels)
	}
	claude, _ := registry.Lookup("claude_code")
	claudeOptions := dropdownOptions(t, discuss, FieldEffortPrefix+"claude_code")
	for _, option := range claudeOptions {
		if contains(codex.EffortLevels, option) && !contains(claude.EffortLevels, option) {
			t.Fatalf("claude_code was offered %q, which belongs to another runtime", option)
		}
	}
	// pi grades no effort, so there is no field at all.
	if fields := dropdownOptions(t, discuss, FieldEffortPrefix+"pi"); fields != nil {
		t.Fatalf("pi was given an effort field with %v", fields)
	}
}

// The templates must not hard-code a runtime name: a deployment with one
// runtime enabled gets forms that offer only it.
func TestTemplatesFollowTheEnabledSet(t *testing.T) {
	registry, err := NewRegistry([]string{"pi"}, map[string]string{"pi": strings.Repeat("c", 64)})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	files, err := GenerateTemplates(registry, "")
	if err != nil {
		t.Fatalf("GenerateTemplates: %v", err)
	}
	for _, file := range files {
		for _, absent := range []string{"codex", "claude_code"} {
			if strings.Contains(file.Content, absent) {
				t.Fatalf("%s mentions %q although it is not enabled", file.Path, absent)
			}
		}
	}
}

func TestGenerateTemplatesRefusesAnEmptyRegistry(t *testing.T) {
	registry, err := NewRegistry(nil, nil)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := GenerateTemplates(registry, ""); err == nil {
		t.Fatal("templates were generated with no runtime enabled")
	}
}

// Blank issues stay enabled so ordinary issues between people are not forced
// through an agent form.
func TestBlankIssuesRemainEnabled(t *testing.T) {
	files, err := GenerateTemplates(testRegistry(t), "")
	if err != nil {
		t.Fatalf("GenerateTemplates: %v", err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Path, "config.yaml") {
			continue
		}
		var config issueConfig
		if err := yaml.Unmarshal([]byte(file.Content), &config); err != nil {
			t.Fatalf("unmarshal config: %v", err)
		}
		if !config.BlankIssuesEnabled {
			t.Fatal("blank issues were disabled; a normal issue must not need an agent form")
		}
		return
	}
	t.Fatal("no config.yaml was generated")
}

// The generated field labels are what the parser looks for. If the two drift
// the form silently stops configuring anything, so they are checked together.
func TestGeneratedLabelsAreWhatTheParserReads(t *testing.T) {
	files, err := GenerateTemplates(testRegistry(t), "")
	if err != nil {
		t.Fatalf("GenerateTemplates: %v", err)
	}
	var task formTemplate
	for _, file := range files {
		if strings.HasSuffix(file.Path, "task.yaml") {
			if err := yaml.Unmarshal([]byte(file.Content), &task); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
		}
	}
	// Build a body the way Forgejo renders one, from the generated labels, and
	// check the resolver understands it.
	var body strings.Builder
	values := map[string]string{
		"Goal": "do the thing", "Discussing agents": "codex", "Executing agent": "codex",
		"Budget tier": "small", "How to finish": "pull request", "Reply language": "en",
	}
	for _, block := range task.Body {
		if block.Attributes.Label == "" {
			continue
		}
		value, ok := values[block.Attributes.Label]
		if !ok {
			continue
		}
		body.WriteString("### " + block.Attributes.Label + "\n\n" + value + "\n\n")
	}
	config := ResolveIssueConfig(ParseAnswers(body.String()), testRegistry(t), fullDefaults())
	if config.Goal != "do the thing" || len(config.ChatAgents) != 1 || config.Budget != "small" {
		t.Fatalf("config = %+v; the generated labels and the parser have drifted apart", config)
	}
}

func dropdownOptions(t *testing.T, template formTemplate, id string) []string {
	t.Helper()
	for _, block := range template.Body {
		if block.ID != id {
			continue
		}
		options := make([]string, 0, len(block.Attributes.Options))
		for _, option := range block.Attributes.Options {
			if text, ok := option.(string); ok {
				options = append(options, text)
			}
		}
		return options
	}
	return nil
}
