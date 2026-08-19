package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateModuleConfigMetadataFileRequiresCompleteTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.yml")
	manifest := `name: demo
config:
  defaults:
    enabled: "true"
    label: demo
  types:
    enabled: bool
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateModuleConfigMetadataFile(path)
	if err == nil || !strings.Contains(err.Error(), "label") {
		t.Fatalf("audit error = %v, want missing label type", err)
	}
}

func TestValidateModuleConfigMetadataFileCoversEveryDeclarationSource(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
	}{
		{name: "input required only", config: "  input_required: [token]\n"},
		{name: "required only", config: "  required: [token]\n"},
		{name: "must resolve only", config: "  must_resolve: [token]\n"},
		{name: "default only", config: "  defaults: {token: value}\n"},
		{name: "change only", config: "  changes:\n    token: {effect: container_recreate}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "module.yml")
			manifest := "name: demo\nconfig:\n" + test.config
			if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidateModuleConfigMetadataFile(path)
			if err == nil || !strings.Contains(err.Error(), "token") {
				t.Fatalf("audit error = %v, want missing token type", err)
			}
		})
	}
}

func TestValidateModuleConfigMetadataFileAllowsLegacyBareEnvRequirementWithoutType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.yml")
	manifest := `name: demo
config:
  required: [EXTERNAL_TOKEN]
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModuleConfigMetadataFile(path); err != nil {
		t.Fatalf("runtime-only legacy environment requirement was treated as an untyped Module parameter: %v", err)
	}
}

func TestValidateModuleConfigMetadataFileAcceptsGenericCompleteSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.yml")
	manifest := `name: future-module
config:
  required: [mode]
  defaults:
    enabled: "true"
  changes:
    label: {effect: container_recreate}
  types:
    mode: {enum: [safe, fast]}
    enabled: bool
    label: string
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModuleConfigMetadataFile(path); err != nil {
		t.Fatalf("complete generic schema was rejected: %v", err)
	}
}

func TestValidateModuleConfigMetadataFileRejectsInvalidDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.yml")
	manifest := `name: demo
config:
  defaults: {workers: many}
  types: {workers: int}
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateModuleConfigMetadataFile(path)
	if err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Fatalf("audit error = %v, want invalid int default", err)
	}
}

func TestValidateModuleConfigMetadataFileRejectsStructuredStringDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.yml")
	manifest := `name: demo
config:
  defaults:
    labels: [one, two]
  types:
    labels: string
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateModuleConfigMetadataFile(path)
	if err == nil || !strings.Contains(err.Error(), "must be a scalar string") {
		t.Fatalf("audit error = %v, want structured-default rejection", err)
	}
}

func TestValidateModuleConfigMetadataFileRejectsContradictoryInputDefaults(t *testing.T) {
	for _, test := range []struct {
		name     string
		manifest string
		contains string
	}{
		{
			name: "literal and source",
			manifest: `name: demo
config:
  defaults: {token: fallback}
  types:
    token: {kind: string, default_source: inherited}
`,
			contains: "literal default with default_source",
		},
		{
			name: "input required and literal",
			manifest: `name: demo
config:
  input_required: [token]
  defaults: {token: fallback}
  types: {token: string}
`,
			contains: "also has a literal default",
		},
		{
			name: "input required and source",
			manifest: `name: demo
config:
  input_required: [token]
  types:
    token: {kind: string, default_source: generated}
`,
			contains: "also has default_source",
		},
		{
			name: "env-key input required and literal",
			manifest: `name: demo
config:
  input_required: [DEMO_TOKEN]
  defaults: {token: fallback}
  types: {token: string}
`,
			contains: "also has a literal default",
		},
		{
			name: "env-key input required and source",
			manifest: `name: demo
config:
  input_required: [DEMO_TOKEN]
  types:
    token: {kind: string, default_source: generated}
`,
			contains: "also has default_source",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "module.yml")
			if err := os.WriteFile(path, []byte(test.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidateModuleConfigMetadataFile(path)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("audit error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestValidateModuleConfigMetadataFileAllowsLegacyRequiredWithDefaults(t *testing.T) {
	for _, manifest := range []string{
		`name: demo
config:
  required: [token]
  defaults: {token: fallback}
  types: {token: string}
`,
		`name: demo
config:
  required: [token]
  types:
    token: {kind: string, default_source: runtime}
`,
	} {
		path := filepath.Join(t.TempDir(), "module.yml")
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateModuleConfigMetadataFile(path); err != nil {
			t.Fatalf("legacy required default was rejected: %v", err)
		}
	}
}

func TestValidateModuleConfigMetadataFileRejectsMisspelledInputRequired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.yml")
	manifest := `name: demo
config:
  input_requiredd: [token]
  types: {token: string}
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateModuleConfigMetadataFile(path)
	if err == nil || !strings.Contains(err.Error(), "input_requiredd") {
		t.Fatalf("audit error = %v, want strict unknown-field rejection", err)
	}
}

func TestValidateModuleConfigMetadataFileRejectsMalformedDefaultAndChangeKeys(t *testing.T) {
	for _, test := range []struct {
		name, config, field, want string
	}{
		{name: "empty default", config: "  defaults:\n    \" \": value\n", field: "config.defaults", want: "empty parameter name"},
		{name: "duplicate default", config: "  defaults:\n    Mode: safe\n    \" mode \": fast\n  types: {mode: string}\n", field: "config.defaults", want: "more than once after normalization"},
		{name: "empty change", config: "  changes:\n    \" \": {effect: container_recreate}\n", field: "config.changes", want: "empty parameter name"},
		{name: "duplicate change", config: "  changes:\n    Mode: {effect: container_recreate}\n    \" mode \": {effect: hot_reload}\n  types: {mode: string}\n", field: "config.changes", want: "more than once after normalization"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "module.yml")
			manifest := "name: demo\nconfig:\n" + test.config
			if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidateModuleConfigMetadataFile(path)
			if err == nil || !strings.Contains(err.Error(), test.field) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("audit error = %v, want %s %s", err, test.field, test.want)
			}
		})
	}
}

func TestValidateModuleConfigMetadataFileAllowsMustResolveWithDefaults(t *testing.T) {
	for _, manifest := range []string{
		`name: demo
config:
  must_resolve: [token]
  defaults: {token: fallback}
  types: {token: string}
`,
		`name: demo
config:
  must_resolve: [token]
  types:
    token: {kind: string, default_source: generated}
`,
	} {
		path := filepath.Join(t.TempDir(), "module.yml")
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateModuleConfigMetadataFile(path); err != nil {
			t.Fatalf("must_resolve default was rejected: %v", err)
		}
	}
}
