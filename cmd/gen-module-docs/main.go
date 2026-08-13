// Command gen-module-docs validates each module's versioned localization
// inventory and renders the corresponding module and reference documentation.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/localization"
	"gopkg.in/yaml.v3"
)

const (
	localizationAPI = "anas.module-localization/v1"
	blockStart      = "<!-- generated:localization:start -->"
	blockEnd        = "<!-- generated:localization:end -->"
)

type manifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Revision    int    `yaml:"revision"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type inventory struct {
	APIVersion     string       `yaml:"api_version"`
	Module         string       `yaml:"module"`
	ModuleVersion  string       `yaml:"module_version"`
	ModuleRevision int          `yaml:"module_revision"`
	ReviewedAt     string       `yaml:"reviewed_at"`
	Timezone       timezoneInfo `yaml:"timezone"`
	Language       languageInfo `yaml:"language"`
}

type timezoneInfo struct {
	Status    string `yaml:"status"`
	Mechanism string `yaml:"mechanism"`
}

type languageInfo struct {
	Status         string         `yaml:"status"`
	Scope          string         `yaml:"scope"`
	Selection      string         `yaml:"selection"`
	GlobalDefault  string         `yaml:"global_default"`
	GlobalLocale   string         `yaml:"global_locale"`
	UpstreamFormat string         `yaml:"upstream_format"`
	Fallback       string         `yaml:"fallback"`
	Supported      []string       `yaml:"supported"`
	Evidence       []evidenceInfo `yaml:"evidence"`
	Notes          string         `yaml:"notes"`
}

type evidenceInfo struct {
	Version string `yaml:"version"`
	URL     string `yaml:"url"`
	Path    string `yaml:"path"`
}

type moduleDoc struct {
	Dir       string
	Manifest  manifest
	Inventory inventory
}

func main() {
	check := flag.Bool("check", false, "fail if generated documentation is stale")
	root := flag.String("root", "", "repository root (auto-detected by default)")
	flag.Parse()
	if *root == "" {
		var err error
		*root, err = findRepoRoot()
		if err != nil {
			fatal(err)
		}
	}
	if err := run(*root, *check); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, "go.mod")) && exists(filepath.Join(dir, "modules")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find repository root")
		}
		dir = parent
	}
}

func run(root string, check bool) error {
	modules, err := loadModules(root)
	if err != nil {
		return err
	}
	var stale []string
	for _, module := range modules {
		path := filepath.Join(module.Dir, "README.md")
		current, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		base := string(current)
		if len(current) == 0 {
			base = "# " + module.Manifest.Title + "\n\n" + module.Manifest.Description + "\n"
		}
		want, err := replaceGeneratedBlock(base, renderModuleBlock(module))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := update(path, []byte(want), check, &stale); err != nil {
			return err
		}
	}
	outputs := map[string][]byte{
		filepath.Join(root, "docs", "reference", "module-localization.md"):       renderReference(modules, false),
		filepath.Join(root, "docs", "en", "reference", "module-localization.md"): renderReference(modules, true),
	}
	for path, content := range outputs {
		if err := update(path, content, check, &stale); err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("generated module documentation is stale:\n  %s\nrun: go run ./cmd/gen-module-docs", strings.Join(stale, "\n  "))
	}
	return nil
}

func update(path string, want []byte, check bool, stale *[]string) error {
	got, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if bytes.Equal(got, want) {
		return nil
	}
	if check {
		*stale = append(*stale, path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, want, 0o644)
}

func loadModules(root string) ([]moduleDoc, error) {
	entries, err := os.ReadDir(filepath.Join(root, "modules"))
	if err != nil {
		return nil, err
	}
	var modules []moduleDoc
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, "modules", entry.Name())
		manifestPath := filepath.Join(dir, "module.yml")
		if !exists(manifestPath) {
			continue
		}
		var m manifest
		if err := decodeYAML(manifestPath, &m, false); err != nil {
			return nil, err
		}
		var inv inventory
		inventoryPath := filepath.Join(dir, "localization.yml")
		if err := decodeYAML(inventoryPath, &inv, true); err != nil {
			return nil, fmt.Errorf("%s: every module must declare versioned localization metadata: %w", inventoryPath, err)
		}
		if err := validate(m, inv); err != nil {
			return nil, fmt.Errorf("%s: %w", inventoryPath, err)
		}
		modules = append(modules, moduleDoc{Dir: dir, Manifest: m, Inventory: inv})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Manifest.Name < modules[j].Manifest.Name })
	if len(modules) == 0 {
		return nil, errors.New("no modules found")
	}
	return modules, nil
}

func decodeYAML(path string, out any, strict bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(strict)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func validate(m manifest, inv inventory) error {
	if m.Name == "" || m.Version == "" || m.Title == "" {
		return errors.New("module.yml must declare name, version, and title")
	}
	if inv.APIVersion != localizationAPI {
		return fmt.Errorf("api_version must be %s", localizationAPI)
	}
	if inv.Module != m.Name {
		return fmt.Errorf("module %q does not match module.yml name %q", inv.Module, m.Name)
	}
	if inv.ModuleVersion != m.Version {
		return fmt.Errorf("module_version %q is stale; module.yml is %q", inv.ModuleVersion, m.Version)
	}
	if inv.ModuleRevision != m.Revision {
		return fmt.Errorf("module_revision %d is stale; module.yml revision is %d", inv.ModuleRevision, m.Revision)
	}
	if inv.ReviewedAt == "" || inv.Timezone.Status == "" || inv.Timezone.Mechanism == "" {
		return errors.New("reviewed_at and timezone status/mechanism are required")
	}
	statuses := map[string]bool{"supported": true, "fixed": true, "not_applicable": true}
	if !statuses[inv.Language.Status] {
		return fmt.Errorf("unknown language status %q", inv.Language.Status)
	}
	selections := map[string]bool{"browser": true, "integration": true, "application": true, "deployment_default": true, "fixed": true, "none": true, "client": true}
	if !selections[inv.Language.Selection] {
		return fmt.Errorf("unknown language selection %q", inv.Language.Selection)
	}
	globalBehaviors := map[string]bool{"applied": true, "fallback": true, "not_consumed": true, "not_applicable": true}
	if !globalBehaviors[inv.Language.GlobalDefault] {
		return fmt.Errorf("unknown global.default_language behavior %q", inv.Language.GlobalDefault)
	}
	if !globalBehaviors[inv.Language.GlobalLocale] {
		return fmt.Errorf("unknown global.default_locale behavior %q", inv.Language.GlobalLocale)
	}
	if inv.Language.Scope == "" || inv.Language.Fallback == "" || len(inv.Language.Evidence) == 0 {
		return errors.New("language scope, fallback, and evidence are required")
	}
	if inv.Language.Status != "not_applicable" && len(inv.Language.Supported) == 0 {
		return errors.New("supported/fixed language inventory must not be empty")
	}
	seen := map[string]bool{}
	for _, value := range inv.Language.Supported {
		normalized, err := localization.NormalizeLanguage(value)
		if err != nil {
			return fmt.Errorf("invalid supported language %q: %w", value, err)
		}
		if normalized != value {
			return fmt.Errorf("supported language %q must use canonical BCP 47 spelling %q", value, normalized)
		}
		if seen[value] {
			return fmt.Errorf("duplicate supported language %q", value)
		}
		seen[value] = true
	}
	for _, item := range inv.Language.Evidence {
		if item.Version == "" || item.URL == "" || item.Path == "" {
			return errors.New("each evidence item requires version, url, and path")
		}
	}
	return nil
}

func replaceGeneratedBlock(base, block string) (string, error) {
	start := strings.Index(base, blockStart)
	end := strings.Index(base, blockEnd)
	if (start < 0) != (end < 0) {
		return "", errors.New("localization generated markers are unbalanced")
	}
	if start >= 0 {
		if end < start {
			return "", errors.New("localization generated markers are reversed")
		}
		end += len(blockEnd)
		return strings.TrimRight(base[:start], " \n") + "\n\n" + block + strings.TrimLeft(base[end:], " \n"), nil
	}
	return strings.TrimRight(base, " \n") + "\n\n" + block, nil
}

func renderModuleBlock(module moduleDoc) string {
	inv := module.Inventory
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n## 时区与语言 / Timezone and language\n\n", blockStart)
	out.WriteString("> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.\n\n")
	fmt.Fprintf(&out, "- Module version / 版本：`%s-r%d`（reviewed %s）\n", inv.ModuleVersion, inv.ModuleRevision, inv.ReviewedAt)
	fmt.Fprintf(&out, "- Timezone / 时区：`%s` — %s\n", inv.Timezone.Status, inv.Timezone.Mechanism)
	fmt.Fprintf(&out, "- Language scope / 语言范围：%s\n", inv.Language.Scope)
	fmt.Fprintf(&out, "- Selection / 选择方式：`%s`\n", inv.Language.Selection)
	fmt.Fprintf(&out, "- ANAS global defaults / 全局默认：`default_language=%s`; `default_locale=%s`\n", inv.Language.GlobalDefault, inv.Language.GlobalLocale)
	if inv.Language.UpstreamFormat != "" {
		fmt.Fprintf(&out, "- Upstream format / 上游格式：%s\n", inv.Language.UpstreamFormat)
	}
	fmt.Fprintf(&out, "- Fallback / 回退：%s\n", inv.Language.Fallback)
	if len(inv.Language.Supported) == 0 {
		out.WriteString("- Supported languages / 支持语言：not applicable / 不适用\n")
	} else {
		fmt.Fprintf(&out, "- Supported languages / 支持语言（%d）：%s\n", len(inv.Language.Supported), codeList(inv.Language.Supported))
	}
	if inv.Language.Notes != "" {
		fmt.Fprintf(&out, "- Notes / 说明：%s\n", inv.Language.Notes)
	}
	out.WriteString("\nEvidence / 证据：\n\n")
	for _, item := range inv.Language.Evidence {
		fmt.Fprintf(&out, "- [%s — %s](%s)\n", item.Version, item.Path, item.URL)
	}
	fmt.Fprintf(&out, "%s\n", blockEnd)
	return out.String()
}

func renderReference(modules []moduleDoc, english bool) []byte {
	var out strings.Builder
	if english {
		out.WriteString("# Module timezone and language matrix\n\n")
		out.WriteString("This generated reference records the behavior verified against each current upstream version. Edit `modules/*/localization.yml`, then run `go run ./cmd/gen-module-docs`; do not edit this page directly.\n\n")
		out.WriteString("When omitted, timezone and language inherit the host. Locale uses an explicit region-bearing language first, then host locale, CLDR inference, and finally `en-US`. `TZ` is a widely consumed runtime convention, but `DEFAULT_LANGUAGE` and `DEFAULT_LOCALE` affect an application only when the matrix marks them `applied` or `fallback`. Browser-selected modules continue to prefer the user's or browser's language.\n\n")
	} else {
		out.WriteString("# Module 时区与语言支持矩阵\n\n")
		out.WriteString("本页记录按各 Module 当前上游版本核实的行为，由 `modules/*/localization.yml` 生成。修改清单后运行 `go run ./cmd/gen-module-docs`，不要直接编辑本页。\n\n")
		out.WriteString("未填写时，时区和语言继承宿主机；locale 依次采用带明确地区的显式语言、宿主 locale、CLDR 推断，最后回退 `en-US`。`TZ` 是应用广泛消费的运行时约定；`DEFAULT_LANGUAGE` 和 `DEFAULT_LOCALE` 只有在表中标为 `applied` 或 `fallback` 时才真正影响应用。标记为浏览器选择的 Module 仍优先采用用户或浏览器语言。\n\n")
	}
	out.WriteString("| Module | Version | Timezone | Language | Selection | Global language | Global locale | Count |\n| --- | --- | --- | --- | --- | --- | --- | ---: |\n")
	for _, module := range modules {
		inv := module.Inventory
		fmt.Fprintf(&out, "| [%s](#%s) | %s-r%d | %s | %s | %s | %s | %s | %d |\n", module.Manifest.Name, module.Manifest.Name, inv.ModuleVersion, inv.ModuleRevision, inv.Timezone.Status, inv.Language.Status, inv.Language.Selection, inv.Language.GlobalDefault, inv.Language.GlobalLocale, len(inv.Language.Supported))
	}
	for _, module := range modules {
		inv := module.Inventory
		fmt.Fprintf(&out, "\n## %s\n\n", module.Manifest.Name)
		fmt.Fprintf(&out, "- **Version / 版本：** `%s-r%d`; reviewed %s\n", inv.ModuleVersion, inv.ModuleRevision, inv.ReviewedAt)
		fmt.Fprintf(&out, "- **Timezone / 时区：** `%s` — %s\n", inv.Timezone.Status, inv.Timezone.Mechanism)
		fmt.Fprintf(&out, "- **Language / 语言：** `%s`, `%s` — %s\n", inv.Language.Status, inv.Language.Selection, inv.Language.Scope)
		fmt.Fprintf(&out, "- **ANAS globals / 全局默认：** `default_language=%s`; `default_locale=%s`\n", inv.Language.GlobalDefault, inv.Language.GlobalLocale)
		fmt.Fprintf(&out, "- **Fallback / 回退：** %s\n", inv.Language.Fallback)
		if len(inv.Language.Supported) == 0 {
			out.WriteString("- **Supported / 支持语言：** not applicable / 不适用\n")
		} else {
			fmt.Fprintf(&out, "- **Supported / 支持语言：** %s\n", codeList(inv.Language.Supported))
		}
		if inv.Language.Notes != "" {
			fmt.Fprintf(&out, "- **Notes / 说明：** %s\n", inv.Language.Notes)
		}
		out.WriteString("- **Evidence / 证据：**")
		for i, item := range inv.Language.Evidence {
			if i > 0 {
				out.WriteString(";")
			}
			fmt.Fprintf(&out, " [%s — %s](%s)", item.Version, item.Path, item.URL)
		}
		out.WriteString("\n")
	}
	return []byte(out.String())
}

func codeList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
