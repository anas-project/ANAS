package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/runner"
	"gopkg.in/yaml.v3"
)

func TestValidateRejectsStaleModuleVersion(t *testing.T) {
	m := manifest{Name: "demo", Version: "2.0", Revision: 1, Title: "Demo"}
	inv := validInventory()
	inv.ModuleVersion = "1.0"
	if err := validate(m, inv); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("validate returned %v, want stale version error", err)
	}
}

func TestValidateRejectsStaleModuleRevision(t *testing.T) {
	m := manifest{Name: "demo", Version: "1.0", Revision: 2, Title: "Demo"}
	inv := validInventory()
	if err := validate(m, inv); err == nil || !strings.Contains(err.Error(), "module_revision") {
		t.Fatalf("validate returned %v, want stale revision error", err)
	}
}

func TestValidateRejectsNonCanonicalLanguage(t *testing.T) {
	m := manifest{Name: "demo", Version: "1.0", Revision: 1, Title: "Demo"}
	inv := validInventory()
	inv.Language.Supported = []string{"zh_cn"}
	if err := validate(m, inv); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("validate returned %v, want canonical spelling error", err)
	}
}

func TestReplaceGeneratedBlockPreservesManualContent(t *testing.T) {
	base := "# Demo\n\nmanual\n\n" + blockStart + "\nold\n" + blockEnd + "\n\nafter\n"
	got, err := replaceRequiredGeneratedBlock(base, blockStart, blockEnd, blockStart+"\nnew\n"+blockEnd+"\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"manual", "new", "after"} {
		if !strings.Contains(got, want) {
			t.Fatalf("result lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Fatalf("old generated block remains:\n%s", got)
	}
}

func TestRunGeneratesAndChecksBothLanguageReferences(t *testing.T) {
	root := t.TempDir()
	moduleDir := filepath.Join(root, "modules", "demo")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestYAML := `api_version: anas.module/v1
kind: Module
name: demo
version: "1.0"
revision: 1
title: Demo
description: Demo module.
abi:
  supports: [anas.module-hook/v1]
status: release
category: test
runtime:
  type: compose
  compose_file: docker-compose.yml
upgrade:
  data_breaking: []
`
	inventoryYAML := `api_version: anas.module-localization/v1
module: demo
module_version: "1.0"
module_revision: 1
reviewed_at: 2026-08-13
timezone: {status: container, mechanism: TZ}
language:
  status: supported
  scope: Demo UI
  selection: browser
  global_default: not_consumed
  global_locale: not_consumed
  fallback: English
  supported: [en]
  evidence: [{version: "1.0", url: "https://example.com", path: locales}]
`
	documents := map[string]string{
		"module.yml":                             manifestYAML,
		"localization.yml":                       inventoryYAML,
		"docker-compose.yml":                     "services:\n  demo:\n    image: example/demo:1.0-r1\n",
		"README.md":                              "# Demo\n\n## 快速信息\n\n" + factsBlockStart + "\nold\n" + factsBlockEnd + "\n\n" + blockStart + "\nold localization\n" + blockEnd + "\n",
		"README.en.md":                           "# Demo\n\n## Quick facts\n\n" + factsBlockStart + "\nold\n" + factsBlockEnd + "\n",
		filepath.Join("docs", "technical.md"):    "# Demo\n\n" + identityBlockStart + "\nold\n" + identityBlockEnd + "\n\n## Compose 拓扑\n\n" + topologyBlockStart + "\nold\n" + topologyBlockEnd + "\n",
		filepath.Join("docs", "technical.en.md"): "# Demo\n\n" + identityBlockStart + "\nold\n" + identityBlockEnd + "\n\n## Compose topology\n\n" + topologyBlockStart + "\nold\n" + topologyBlockEnd + "\n",
	}
	for name, body := range documents {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(moduleDir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moduleDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "docs", "reference", "module-localization.md"),
		filepath.Join(root, "docs", "en", "reference", "module-localization.md"),
		filepath.Join(moduleDir, "README.md"),
		filepath.Join(moduleDir, "README.en.md"),
		filepath.Join(moduleDir, "docs", "technical.md"),
		filepath.Join(moduleDir, "docs", "technical.en.md"),
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("paired generated reference missing %s: %v", path, err)
		}
		if strings.Contains(path, "modules") && (!strings.Contains(string(body), "1.0-r1") || strings.Contains(string(body), "\nold\n")) {
			t.Fatalf("Module document was not synchronized: %s\n%s", path, body)
		}
	}
	if err := run(root, true); err != nil {
		t.Fatalf("paired generated references failed check: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "docs", "en", "reference", "module-localization.md")); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil {
		t.Fatal("check accepted a missing English generated reference")
	}
}

func TestLoadModulesRejectsParameterWithoutDeclaredType(t *testing.T) {
	root := t.TempDir()
	moduleDir := filepath.Join(root, "modules", "demo")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestYAML := `name: demo
version: "1.0"
revision: 1
title: Demo
config:
  defaults:
    enabled: "true"
`
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifestYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadModules(root)
	if err == nil || !strings.Contains(err.Error(), "enabled") || !strings.Contains(err.Error(), "declared types") {
		t.Fatalf("loadModules error = %v, want missing type metadata", err)
	}
}

func TestRequiredGeneratedBlockRejectsMissingDuplicateAndReversedMarkers(t *testing.T) {
	validBlock := factsBlockStart + "\nold\n" + factsBlockEnd + "\n"
	for name, body := range map[string]string{
		"missing":   "manual only\n",
		"duplicate": validBlock + validBlock,
		"reversed":  factsBlockEnd + "\nold\n" + factsBlockStart + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := replaceRequiredGeneratedBlock(body, factsBlockStart, factsBlockEnd, validBlock); err == nil {
				t.Fatalf("accepted %s markers", name)
			}
		})
	}
}

func TestRenderComposeTopologyIsDeterministic(t *testing.T) {
	var compose composeDocument
	body := `services:
  zed:
    image: example/zed:1
    networks: [back, front]
    volumes: [one:/one]
  alpha:
    build:
      context: ./alpha
`
	if err := yaml.Unmarshal([]byte(body), &compose); err != nil {
		t.Fatal(err)
	}
	rendered, err := renderComposeTopology(moduleDoc{Compose: compose})
	if err != nil {
		t.Fatal(err)
	}
	alpha := strings.Index(rendered, "| `alpha` | `build: ./alpha` | `` | 0 |")
	zed := strings.Index(rendered, "| `zed` | `example/zed:1` | `back, front` | 1 |")
	if alpha < 0 || zed < 0 || alpha > zed {
		t.Fatalf("unexpected topology:\n%s", rendered)
	}
}

func TestValidateManagedComposeImagesChecksEveryServiceScalar(t *testing.T) {
	var compose composeDocument
	body := `services:
  first:
    image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-demo:1.0.0-r3
  second:
    image: ghcr.io/anas-project/anas-demo:1.0.0-r3
`
	if err := yaml.Unmarshal([]byte(body), &compose); err != nil {
		t.Fatal(err)
	}
	if err := validateManagedComposeImages(compose, []string{"anas-demo"}, "1.0.0", 3); err != nil {
		t.Fatal(err)
	}
	compose.Services["second"] = composeService{Image: "ghcr.io/anas-project/anas-demo:1.0.0-r2"}
	if err := validateManagedComposeImages(compose, []string{"anas-demo"}, "1.0.0", 3); err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatalf("stale service image error = %v", err)
	}
}

func TestSyncParameterTableProjectsSchemaAndPreservesManualPurpose(t *testing.T) {
	minimum, maximum := 1, 65535
	module := moduleDoc{Parameters: []runner.ConfigParameterInventoryEntry{
		{
			Path: "demo.port", Type: "int", Default: "3478", HasDefault: true,
			DefaultSource: "static", EnvKey: "DEMO_PORT", Editable: true,
			Effect:      "container_recreate",
			Constraints: configschema.Constraints{Minimum: &minimum, Maximum: &maximum},
		},
		{
			Path: "demo.token", Type: "string", Default: "host-current-value", HasDefault: false,
			DefaultSource: "generated", EnvKey: "DEMO_TOKEN", MustResolve: true,
			Sensitive: true, Editable: false, EditCommand: "rotate-token", Effect: "credential_rotate",
		},
	}}
	base := `# Demo

manual before

## All configuration parameters

manual table introduction

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| ` + "`demo.port`" + ` | int | ` + "`3478`" + ` | ` + "`DEMO_PORT`" + ` | no | no | yes | ` + "`container_recreate`" + ` | Keep the port purpose. |
| ` + "`demo.token`" + ` | string | — | ` + "`DEMO_TOKEN`" + ` | no | yes | no: ` + "`rotate-token`" + ` | ` + "`credential_rotate`" + ` | Keep the token purpose. |

manual after
`
	got, err := syncParameterTable(base, module, true, "## All configuration parameters")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"manual before", "manual table introduction", "manual after",
		"Keep the port purpose.", "Keep the token purpose.",
		"| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve |",
		"`1..65535`", "`static`", "`generated`", "no | yes | yes | no: `rotate-token`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("synchronized table missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "host-current-value") {
		t.Fatalf("non-literal current host/runtime value leaked into documentation:\n%s", got)
	}
	gotAgain, err := syncParameterTable(got, module, true, "## All configuration parameters")
	if err != nil {
		t.Fatal(err)
	}
	if gotAgain != got {
		t.Fatalf("parameter table synchronization is not idempotent:\n%s", gotAgain)
	}
}

func TestSplitMarkdownRowPreservesEscapedPipe(t *testing.T) {
	cells := splitMarkdownRow("| `demo.pattern` | Match left \\| right |")
	if len(cells) != 2 || cells[1] != `Match left \| right` {
		t.Fatalf("split cells = %#v", cells)
	}
}

func TestBundledParameterTablesMatchGeneratedInventory(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	modules, err := loadModules(root)
	if err != nil {
		t.Fatal(err)
	}
	tables, rows, expectedTables, expectedRows := 0, 0, 0, 0
	for _, module := range modules {
		if len(module.Parameters) == 0 {
			continue
		}
		expectedTables += 4
		expectedRows += len(module.Parameters) * 4
		for _, document := range []struct {
			path    string
			english bool
			heading string
		}{
			{filepath.Join(module.Dir, "README.md"), false, "## 所有可用配置参数"},
			{filepath.Join(module.Dir, "README.en.md"), true, "## All configuration parameters"},
			{filepath.Join(module.Dir, "docs", "technical.md"), false, "## 配置契约"},
			{filepath.Join(module.Dir, "docs", "technical.en.md"), true, "## Configuration contract"},
		} {
			body, err := os.ReadFile(document.path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := syncParameterTable(string(body), module, document.english, document.heading)
			if err != nil {
				t.Fatalf("%s: %v", document.path, err)
			}
			if want != string(body) {
				t.Errorf("%s parameter table is stale; update all four reviewed tables", document.path)
			}
			start, end, err := markdownTableBounds(string(body), document.heading)
			if err != nil {
				t.Fatalf("%s: %v", document.path, err)
			}
			lines := strings.Split(strings.TrimSpace(string(body)[start:end]), "\n")
			tables++
			rows += len(lines) - 2
		}
	}
	if tables != expectedTables || rows != expectedRows {
		t.Fatalf("parameter documentation coverage = %d tables/%d rows; want %d/%d", tables, rows, expectedTables, expectedRows)
	}
}

func validInventory() inventory {
	return inventory{
		APIVersion:     localizationAPI,
		Module:         "demo",
		ModuleVersion:  "1.0",
		ModuleRevision: 1,
		ReviewedAt:     "2026-08-13",
		Timezone:       timezoneInfo{Status: "container", Mechanism: "TZ"},
		Language: languageInfo{
			Status:        "supported",
			Scope:         "Demo UI",
			Selection:     "browser",
			GlobalDefault: "not_consumed",
			GlobalLocale:  "not_consumed",
			Fallback:      "English",
			Supported:     []string{"en"},
			Evidence:      []evidenceInfo{{Version: "1.0", URL: "https://example.com", Path: "locales"}},
		},
	}
}
