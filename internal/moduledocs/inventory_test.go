package moduledocs

import (
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/runner"
)

func TestSyncConfigurationRendersAllDerivedBlocksAndIsIdempotent(t *testing.T) {
	inventory := inventoryRendererFixture()
	base := configurationRendererBase()

	got, err := SyncConfiguration(base, inventory, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"人工前言\n<!-- generated:configuration-summary:start -->",
		"<!-- generated:configuration-summary:end -->\n人工说明一",
		"- 内置 Module：`2`",
		"- 已声明参数：共 `4` 个（全局 `1` 个、Module 所有 `3` 个；结构化 Module 参数 `2` 个、裸 `env.*` 参数 `1` 个）",
		"- 解析阶段：`input_required` `1` 个、`must_resolve` `2` 个、未知类型 `0` 个",
		"- 类型分布：`bool` `1`、`int` `1`、`string` `2`",
		"- 默认值来源分布：`none` `1`、`runtime` `1`、`static` `2`",
		"| `global` | 1 | `global.domain` |",
		"| `alpha` | 2 | `alpha.token`<br>`env.ALPHA_FLAG` |",
		"| `beta` | 1 | `beta.port` |",
		"| `alpha.token` | <code>min_length=2; pattern=&#34;^a\\|b$&#34;</code> |",
		"| `beta.port` | <code>minimum=1; maximum=9</code> |",
		"| `global.domain` | <code>format=&#34;dns_name&#34;</code> |",
		"| `env.ALPHA_FLAG` | `alpha` | `ALPHA_FLAG` |",
		"| `credential_rotate` | 1 |",
		"| `hot_reload` | 1 |",
		"| `reconcile` | 1 |",
		"<!-- generated:configuration-effects:end -->\n人工结尾",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("synchronized configuration lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "旧生成内容") {
		t.Fatalf("old generated content was retained:\n%s", got)
	}
	if strings.Index(got, "| `global` |") > strings.Index(got, "| `alpha` |") {
		t.Fatalf("global owner must be first:\n%s", got)
	}
	if strings.Index(got, "| `alpha.token` | <code>") > strings.Index(got, "| `beta.port` | <code>") {
		t.Fatalf("constraint rows are not sorted by path:\n%s", got)
	}

	again, err := SyncConfiguration(got, inventory, false)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("SyncConfiguration is not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

func TestSyncConfigurationRendersEnglishLabels(t *testing.T) {
	got, err := SyncConfiguration(configurationRendererBase(), inventoryRendererFixture(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- Built-in Modules: `2`",
		"- Declared parameters: `4` total (`1` global and `3` Module-owned; `2` structured Module parameters and `1` bare `env.*` parameters)",
		"- Resolution phases: `input_required` `1`, `must_resolve` `2`, unknown types `0`",
		"| Owner | Parameters | Parameter paths |",
		"| Parameter path | Portable constraints |",
		"| YAML path | Owner | Environment key |",
		"| Module parameter effect | Parameters | Change outcome |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("English synchronized configuration lacks %q:\n%s", want, got)
		}
	}
}

func TestSyncConfigurationBareParametersSortsAndExcludesGlobalPaths(t *testing.T) {
	inventory := inventoryRendererFixture()
	inventory.Parameters = append(inventory.Parameters,
		runner.ConfigParameterInventoryEntry{Path: "env.AAA_FIRST", Module: "beta", EnvKey: "AAA_FIRST"},
		runner.ConfigParameterInventoryEntry{Path: "env.GLOBAL_ESCAPE", Module: "global", EnvKey: "GLOBAL_ESCAPE"},
	)

	got, err := SyncConfiguration(configurationRendererBase(), inventory, true)
	if err != nil {
		t.Fatal(err)
	}
	first := "| `env.AAA_FIRST` | `beta` | `AAA_FIRST` |"
	second := "| `env.ALPHA_FLAG` | `alpha` | `ALPHA_FLAG` |"
	if strings.Index(got, first) < 0 || strings.Index(got, second) < 0 || strings.Index(got, first) > strings.Index(got, second) {
		t.Fatalf("bare parameter rows are missing or unsorted:\n%s", got)
	}
	if strings.Contains(got, "| `env.GLOBAL_ESCAPE` | `global` |") {
		t.Fatalf("global env path was included in the Module-only bare table:\n%s", got)
	}
}

func TestSyncConfigurationRejectsMissingDuplicateAndReversedMarkers(t *testing.T) {
	base := configurationRendererBase()
	start, end := generatedBlockMarkers(configurationSummaryBlock)
	_, effectEnd := generatedBlockMarkers(configurationEffectsBlock)
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "missing", base: strings.Replace(base, effectEnd, "", 1), want: "missing end marker"},
		{name: "duplicate start", base: strings.Replace(base, start, start+"\n"+start, 1), want: "2 start markers"},
		{name: "duplicate end", base: strings.Replace(base, end, end+"\n"+end, 1), want: "2 end markers"},
		{
			name: "reversed",
			base: strings.Replace(base, start+"\n旧生成内容\n"+end, end+"\n旧生成内容\n"+start, 1),
			want: "end marker before its start marker",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := SyncConfiguration(test.base, inventoryRendererFixture(), false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSyncArchitectureModulesSortsEscapesPreservesTextAndIsIdempotent(t *testing.T) {
	start, end := generatedBlockMarkers(architectureModulesBlock)
	base := "architecture intro\n" + start + "\n旧表\n" + end + "\narchitecture outro\n"
	inventory := inventoryRendererFixture()

	got, err := SyncArchitectureModules(base, inventory, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"architecture intro\n" + start,
		"| Name | 类别 | 状态 | 描述 |",
		"| [`alpha`](/reference/modules/alpha/) | `core` | `release` | Alpha service |",
		"| [`beta`](/reference/modules/beta/) | `media` | `developing` | Beta \\| archive |",
		end + "\narchitecture outro\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("synchronized architecture lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "[`alpha`]") > strings.Index(got, "[`beta`]") {
		t.Fatalf("Module rows are not sorted:\n%s", got)
	}
	if inventory.Modules[0].Name != "beta" {
		t.Fatalf("SyncArchitectureModules mutated its input: %#v", inventory.Modules)
	}

	again, err := SyncArchitectureModules(got, inventory, false)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("SyncArchitectureModules is not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

func TestSyncArchitectureModulesUsesEnglishHeadersAndValidatesMarkers(t *testing.T) {
	start, end := generatedBlockMarkers(architectureModulesBlock)
	base := start + "\nold\n" + end
	got, err := SyncArchitectureModules(base, inventoryRendererFixture(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "| Name | Category | Status | Description |") {
		t.Fatalf("English architecture header missing:\n%s", got)
	}
	if !strings.Contains(got, "[`alpha`](/en/reference/modules/alpha/)") {
		t.Fatalf("English architecture Module link missing:\n%s", got)
	}

	for _, malformed := range []string{
		"old\n" + end,
		start + "\nold",
		start + "\n" + start + "\nold\n" + end,
		end + "\nold\n" + start,
	} {
		if _, err := SyncArchitectureModules(malformed, inventoryRendererFixture(), true); err == nil {
			t.Fatalf("malformed markers were accepted:\n%s", malformed)
		}
	}
}

func configurationRendererBase() string {
	blocks := []string{
		configurationSummaryBlock,
		configurationOwnersBlock,
		configurationConstraintsBlock,
		configurationBareParamsBlock,
		configurationEffectsBlock,
	}
	var out strings.Builder
	manualLabels := []string{"一", "二", "三", "四"}
	out.WriteString("人工前言\n")
	for index, block := range blocks {
		start, end := generatedBlockMarkers(block)
		out.WriteString(start)
		out.WriteString("\n旧生成内容\n")
		out.WriteString(end)
		if index < len(blocks)-1 {
			out.WriteString("\n人工说明")
			out.WriteString(manualLabels[index])
			out.WriteString("\n")
		}
	}
	out.WriteString("\n人工结尾\n")
	return out.String()
}

func inventoryRendererFixture() runner.BuiltinInventory {
	minimum, maximum, minLength := 1, 9, 2
	return runner.BuiltinInventory{
		APIVersion: runner.BuiltinInventoryAPIVersion,
		Modules: []runner.BuiltinModuleInventoryEntry{
			{Name: "beta", Category: "media", Status: "developing", Description: "Beta | archive"},
			{Name: "alpha", Category: "core", Status: "release", Description: "Alpha service"},
		},
		Parameters: []runner.ConfigParameterInventoryEntry{
			{
				Path: "beta.port", Module: "beta", Type: "int", DefaultSource: "static", Effect: "reconcile",
				Constraints: configschema.Constraints{Minimum: &minimum, Maximum: &maximum},
			},
			{
				Path: "global.domain", Module: "global", Type: "string", DefaultSource: "runtime", Effect: "hot_reload",
				Constraints: configschema.Constraints{Format: configschema.FormatDNSName},
			},
			{
				Path: "alpha.token", Module: "alpha", Type: "string", DefaultSource: "static", Effect: "credential_rotate",
				Constraints: configschema.Constraints{MinLength: &minLength, Pattern: "^a|b$"},
			},
			{Path: "env.ALPHA_FLAG", Module: "alpha", EnvKey: "ALPHA_FLAG", Type: "bool", DefaultSource: "none", Effect: "hot_reload"},
		},
		Summary: runner.BuiltinInventorySummary{
			ModuleCount:                    2,
			ParameterCount:                 4,
			GlobalParameterCount:           1,
			ModuleParameterCount:           3,
			StructuredModuleParameterCount: 2,
			BareEnvParameterCount:          1,
			InputRequiredCount:             1,
			MustResolveCount:               2,
			UnknownCount:                   0,
			ByOwner:                        map[string]int{"beta": 1, "global": 1, "alpha": 2},
			ByType:                         map[string]int{"string": 2, "bool": 1, "int": 1},
			ByDefaultSource:                map[string]int{"static": 2, "runtime": 1, "none": 1},
			ModuleByEffect:                 map[string]int{"reconcile": 1, "credential_rotate": 1, "hot_reload": 1},
		},
	}
}
