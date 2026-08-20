package runner

import (
	"reflect"
	"testing"
)

func TestTypedConfigParameterInventoryMatchesCLIProjection(t *testing.T) {
	reg, err := loadRegistry("../..")
	if err != nil {
		t.Fatal(err)
	}
	typed, err := configParameterInventory(reg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(typed), 146; got != want {
		t.Fatalf("typed inventory count = %d, want %d", got, want)
	}

	for _, entry := range typed {
		target := configTarget{Display: entry.Path, Module: entry.Module, Parameter: entry.Parameter}
		document := configTargetMetadataDocument(target, policyForTarget(target, reg), reg)
		checks := map[string]any{
			"path":           entry.Path,
			"module":         entry.Module,
			"parameter":      entry.Parameter,
			"type":           entry.Type,
			"default":        entry.Default,
			"has_default":    entry.HasDefault,
			"default_source": entry.DefaultSource,
			"env_key":        entry.EnvKey,
			"required":       entry.InputRequired,
			"input_required": entry.InputRequired,
			"must_resolve":   entry.MustResolve,
			"sensitive":      entry.Sensitive,
			"editable":       entry.Editable,
			"edit_command":   entry.EditCommand,
			"effect":         entry.Effect,
			"apply":          entry.Apply,
			"description":    entry.Description,
		}
		for field, want := range checks {
			if got := document[field]; !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s = %#v, want %#v", entry.Path, field, got, want)
			}
		}

		if got, exists := document["allowed_values"]; len(entry.AllowedValues) == 0 {
			if exists {
				t.Errorf("%s CLI allowed_values = %#v, want omitted", entry.Path, got)
			}
		} else if !reflect.DeepEqual(got, entry.AllowedValues) {
			t.Errorf("%s CLI allowed_values = %#v, want %#v", entry.Path, got, entry.AllowedValues)
		}

		wantConstraints := paramConstraintsDocument(ParamType{Constraints: entry.Constraints})
		if got, exists := document["constraints"]; len(wantConstraints) == 0 {
			if exists {
				t.Errorf("%s CLI constraints = %#v, want omitted", entry.Path, got)
			}
		} else if !reflect.DeepEqual(got, wantConstraints) {
			t.Errorf("%s CLI constraints = %#v, want %#v", entry.Path, got, wantConstraints)
		}
	}
}

func TestLoadConfigParameterInventoryUsesRepositoryRoot(t *testing.T) {
	entries, err := LoadConfigParameterInventory("../..")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(entries), 146; got != want {
		t.Fatalf("inventory count = %d, want %d", got, want)
	}
}
