package moduledocs

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/runner"
)

const (
	configurationSummaryBlock     = "configuration-summary"
	configurationOwnersBlock      = "configuration-owners"
	configurationConstraintsBlock = "configuration-constraints"
	configurationBareParamsBlock  = "configuration-bare-parameters"
	configurationEffectsBlock     = "configuration-effects"
	architectureModulesBlock      = "builtin-module-inventory"
)

// SyncConfiguration replaces the required generated inventory blocks in a
// configuration reference page. Text outside the markers is preserved.
func SyncConfiguration(base string, inventory runner.BuiltinInventory, english bool) (string, error) {
	rendered := base
	blocks := []struct {
		name string
		body string
	}{
		{configurationSummaryBlock, renderConfigurationSummary(inventory.Summary, english)},
		{configurationOwnersBlock, renderConfigurationOwners(inventory, english)},
		{configurationConstraintsBlock, renderConfigurationConstraints(inventory.Parameters, english)},
		{configurationBareParamsBlock, renderConfigurationBareParameters(inventory.Parameters, english)},
		{configurationEffectsBlock, renderConfigurationEffects(inventory.Summary, english)},
	}

	// Validate every marker pair against the original text before replacing any
	// block. This gives callers one all-or-nothing result on malformed pages.
	for _, block := range blocks {
		if _, _, err := generatedBlockBounds(base, block.name); err != nil {
			return "", err
		}
	}
	for _, block := range blocks {
		var err error
		rendered, err = replaceGeneratedBlock(rendered, block.name, block.body)
		if err != nil {
			return "", err
		}
	}
	return rendered, nil
}

// SyncArchitectureModules replaces the required current built-in Module table
// in an architecture page. Text outside the marker pair is preserved.
func SyncArchitectureModules(base string, inventory runner.BuiltinInventory, english bool) (string, error) {
	return replaceGeneratedBlock(base, architectureModulesBlock, renderArchitectureModules(inventory.Modules, english))
}

func renderConfigurationSummary(summary runner.BuiltinInventorySummary, english bool) string {
	var out strings.Builder
	if english {
		fmt.Fprintf(&out, "- Built-in Modules: `%d`\n", summary.ModuleCount)
		fmt.Fprintf(
			&out,
			"- Declared parameters: `%d` total (`%d` global and `%d` Module-owned; `%d` structured Module parameters and `%d` bare `env.*` parameters)\n",
			summary.ParameterCount,
			summary.GlobalParameterCount,
			summary.ModuleParameterCount,
			summary.StructuredModuleParameterCount,
			summary.BareEnvParameterCount,
		)
		fmt.Fprintf(
			&out,
			"- Resolution phases: `input_required` `%d`, `must_resolve` `%d`, unknown types `%d`\n",
			summary.InputRequiredCount,
			summary.MustResolveCount,
			summary.UnknownCount,
		)
		fmt.Fprintf(&out, "- Type distribution: %s\n", renderDistribution(summary.ByType, ", ", "none"))
		fmt.Fprintf(&out, "- Default-source distribution: %s", renderDistribution(summary.ByDefaultSource, ", ", "none"))
		return out.String()
	}

	fmt.Fprintf(&out, "- 内置 Module：`%d`\n", summary.ModuleCount)
	fmt.Fprintf(
		&out,
		"- 已声明参数：共 `%d` 个（全局 `%d` 个、Module 所有 `%d` 个；结构化 Module 参数 `%d` 个、裸 `env.*` 参数 `%d` 个）\n",
		summary.ParameterCount,
		summary.GlobalParameterCount,
		summary.ModuleParameterCount,
		summary.StructuredModuleParameterCount,
		summary.BareEnvParameterCount,
	)
	fmt.Fprintf(
		&out,
		"- 解析阶段：`input_required` `%d` 个、`must_resolve` `%d` 个、未知类型 `%d` 个\n",
		summary.InputRequiredCount,
		summary.MustResolveCount,
		summary.UnknownCount,
	)
	fmt.Fprintf(&out, "- 类型分布：%s\n", renderDistribution(summary.ByType, "、", "无"))
	fmt.Fprintf(&out, "- 默认值来源分布：%s", renderDistribution(summary.ByDefaultSource, "、", "无"))
	return out.String()
}

func renderDistribution(distribution map[string]int, separator, empty string) string {
	keys := sortedMapKeys(distribution)
	if len(keys) == 0 {
		return empty
	}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("`%s` `%d`", escapeInlineCode(key), distribution[key]))
	}
	return strings.Join(parts, separator)
}

func renderConfigurationOwners(inventory runner.BuiltinInventory, english bool) string {
	pathsByOwner := make(map[string][]string, len(inventory.Summary.ByOwner))
	for owner := range inventory.Summary.ByOwner {
		pathsByOwner[owner] = nil
	}
	for _, parameter := range inventory.Parameters {
		pathsByOwner[parameter.Module] = append(pathsByOwner[parameter.Module], parameter.Path)
	}
	owners := sortedMapKeys(pathsByOwner)
	sort.SliceStable(owners, func(i, j int) bool {
		if owners[i] == "global" {
			return owners[j] != "global"
		}
		if owners[j] == "global" {
			return false
		}
		return owners[i] < owners[j]
	})

	var out strings.Builder
	if english {
		out.WriteString("| Owner | Parameters | Parameter paths |\n| --- | ---: | --- |\n")
	} else {
		out.WriteString("| Owner | 参数数 | 参数路径 |\n| --- | ---: | --- |\n")
	}
	for _, owner := range owners {
		paths := pathsByOwner[owner]
		sort.Strings(paths)
		formattedPaths := make([]string, 0, len(paths))
		for _, path := range paths {
			formattedPaths = append(formattedPaths, "`"+escapeInlineCode(path)+"`")
		}
		if len(formattedPaths) == 0 {
			formattedPaths = append(formattedPaths, "—")
		}
		fmt.Fprintf(
			&out,
			"| `%s` | %d | %s |\n",
			escapeInlineCode(owner),
			len(paths),
			strings.Join(formattedPaths, "<br>"),
		)
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func renderConfigurationConstraints(parameters []runner.ConfigParameterInventoryEntry, english bool) string {
	constrained := make([]runner.ConfigParameterInventoryEntry, 0)
	for _, parameter := range parameters {
		if hasConstraints(parameter.Constraints) {
			constrained = append(constrained, parameter)
		}
	}
	sort.SliceStable(constrained, func(i, j int) bool { return constrained[i].Path < constrained[j].Path })

	var out strings.Builder
	if english {
		fmt.Fprintf(&out, "Current explicit portable constraints: `%d`.\n\n", len(constrained))
		out.WriteString("| Parameter path | Portable constraints |\n| --- | --- |\n")
	} else {
		fmt.Fprintf(&out, "当前显式可移植约束：`%d` 项。\n\n", len(constrained))
		out.WriteString("| 参数路径 | 可移植约束 |\n| --- | --- |\n")
	}
	for _, parameter := range constrained {
		fmt.Fprintf(
			&out,
			"| `%s` | <code>%s</code> |\n",
			escapeInlineCode(parameter.Path),
			escapeInventoryTableCell(html.EscapeString(portableConstraints(parameter.Constraints))),
		)
	}
	if len(constrained) == 0 {
		if english {
			out.WriteString("| — | None |\n")
		} else {
			out.WriteString("| — | 无 |\n")
		}
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func renderConfigurationBareParameters(parameters []runner.ConfigParameterInventoryEntry, english bool) string {
	bare := make([]runner.ConfigParameterInventoryEntry, 0)
	for _, parameter := range parameters {
		if parameter.Module != "global" && strings.HasPrefix(parameter.Path, "env.") {
			bare = append(bare, parameter)
		}
	}
	sort.SliceStable(bare, func(i, j int) bool { return bare[i].Path < bare[j].Path })

	var out strings.Builder
	if english {
		out.WriteString("| YAML path | Owner | Environment key |\n| --- | --- | --- |\n")
	} else {
		out.WriteString("| YAML 地址 | 所有者 | 环境变量 |\n| --- | --- | --- |\n")
	}
	for _, parameter := range bare {
		fmt.Fprintf(
			&out,
			"| `%s` | `%s` | `%s` |\n",
			escapeInlineCode(parameter.Path),
			escapeInlineCode(parameter.Module),
			escapeInlineCode(parameter.EnvKey),
		)
	}
	if len(bare) == 0 {
		out.WriteString("| — | — | — |\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func renderConfigurationEffects(summary runner.BuiltinInventorySummary, english bool) string {
	var out strings.Builder
	if english {
		out.WriteString("| Module parameter effect | Parameters | Change outcome |\n| --- | ---: | --- |\n")
	} else {
		out.WriteString("| Module 参数 effect | 参数数 | 修改结果 |\n| --- | ---: | --- |\n")
	}
	keys := sortedMapKeys(summary.ModuleByEffect)
	for _, effect := range keys {
		fmt.Fprintf(&out, "| `%s` | %d | %s |\n", escapeInlineCode(effect), summary.ModuleByEffect[effect], effectOutcome(effect, english))
	}
	if len(keys) == 0 {
		out.WriteString("| — | 0 | — |\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func effectOutcome(effect string, english bool) string {
	englishOutcomes := map[string]string{
		"container_recreate": "Re-render and recreate the affected container or Compose project",
		"credential_rotate":  "Use a credential-rotation transaction to update application state and the Secret Store together",
		"data_migrate":       "Migrate persistent data, a database, or membership before activation",
		"hot_reload":         "Apply through the declared management command; the current executor may conservatively recreate the container",
		"image_rebuild":      "Rebuild the Module image before activating the deployment",
		"immutable":          "Use a replacement or dedicated migration workflow",
		"reconcile":          "Reconcile application, API, or file state through the Module lifecycle",
	}
	chineseOutcomes := map[string]string{
		"container_recreate": "重新渲染，并重建受影响容器或 Compose project",
		"credential_rotate":  "通过凭据轮换事务同步应用状态与 Secret Store",
		"data_migrate":       "激活前迁移持久数据、数据库或成员身份",
		"hot_reload":         "通过声明的管理命令应用；当前执行器可能保守地重建容器",
		"image_rebuild":      "激活 deployment 前重建 Module 镜像",
		"immutable":          "使用替换或专用迁移流程",
		"reconcile":          "通过 Module 生命周期调和应用、API 或文件状态",
	}
	if english {
		if outcome := englishOutcomes[effect]; outcome != "" {
			return outcome
		}
		return "See `config explain` for the declared lifecycle"
	}
	if outcome := chineseOutcomes[effect]; outcome != "" {
		return outcome
	}
	return "通过 `config explain` 查看声明的生命周期"
}

func renderArchitectureModules(modules []runner.BuiltinModuleInventoryEntry, english bool) string {
	sorted := append([]runner.BuiltinModuleInventoryEntry(nil), modules...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var out strings.Builder
	if english {
		out.WriteString("| Name | Category | Status | Description |\n| --- | --- | --- | --- |\n")
	} else {
		out.WriteString("| Name | 类别 | 状态 | 描述 |\n| --- | --- | --- | --- |\n")
	}
	for _, module := range sorted {
		link := "/reference/modules/" + module.Name + "/"
		if english {
			link = "/en" + link
		}
		fmt.Fprintf(
			&out,
			"| [`%s`](%s) | `%s` | `%s` | %s |\n",
			escapeInlineCode(module.Name),
			link,
			escapeInlineCode(module.Category),
			escapeInlineCode(module.Status),
			escapeInventoryTableCell(module.Description),
		)
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func portableConstraints(constraints configschema.Constraints) string {
	parts := make([]string, 0, 6)
	if constraints.Minimum != nil {
		parts = append(parts, "minimum="+strconv.Itoa(*constraints.Minimum))
	}
	if constraints.Maximum != nil {
		parts = append(parts, "maximum="+strconv.Itoa(*constraints.Maximum))
	}
	if constraints.MinLength != nil {
		parts = append(parts, "min_length="+strconv.Itoa(*constraints.MinLength))
	}
	if constraints.MaxLength != nil {
		parts = append(parts, "max_length="+strconv.Itoa(*constraints.MaxLength))
	}
	if constraints.Pattern != "" {
		parts = append(parts, "pattern="+strconv.Quote(constraints.Pattern))
	}
	if constraints.Format != "" {
		parts = append(parts, "format="+strconv.Quote(constraints.Format))
	}
	return strings.Join(parts, "; ")
}

func hasConstraints(constraints configschema.Constraints) bool {
	return constraints.Minimum != nil || constraints.Maximum != nil ||
		constraints.MinLength != nil || constraints.MaxLength != nil ||
		constraints.Pattern != "" || constraints.Format != ""
}

func replaceGeneratedBlock(base, name, body string) (string, error) {
	start, end, err := generatedBlockBounds(base, name)
	if err != nil {
		return "", err
	}
	startMarker, endMarker := generatedBlockMarkers(name)
	replacement := startMarker + "\n" + strings.TrimSuffix(body, "\n") + "\n" + endMarker
	return base[:start] + replacement + base[end+len(endMarker):], nil
}

func generatedBlockBounds(base, name string) (int, int, error) {
	startMarker, endMarker := generatedBlockMarkers(name)
	startCount, endCount := strings.Count(base, startMarker), strings.Count(base, endMarker)
	if startCount == 0 {
		return 0, 0, fmt.Errorf("required generated block %q is missing start marker %q", name, startMarker)
	}
	if endCount == 0 {
		return 0, 0, fmt.Errorf("required generated block %q is missing end marker %q", name, endMarker)
	}
	if startCount != 1 {
		return 0, 0, fmt.Errorf("required generated block %q has %d start markers; want exactly one", name, startCount)
	}
	if endCount != 1 {
		return 0, 0, fmt.Errorf("required generated block %q has %d end markers; want exactly one", name, endCount)
	}
	start, end := strings.Index(base, startMarker), strings.Index(base, endMarker)
	if start >= end {
		return 0, 0, fmt.Errorf("required generated block %q has its end marker before its start marker", name)
	}
	return start, end, nil
}

func generatedBlockMarkers(name string) (string, string) {
	return "<!-- generated:" + name + ":start -->", "<!-- generated:" + name + ":end -->"
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func escapeInlineCode(value string) string {
	return strings.ReplaceAll(value, "`", "&#96;")
}

func escapeInventoryTableCell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
