// Command gen-module-docs validates each module's configuration metadata and
// versioned localization inventory, then renders the corresponding module and
// reference documentation.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/localization"
	"github.com/anas-project/ANAS/internal/moduledocs"
	"github.com/anas-project/ANAS/internal/runner"
	"gopkg.in/yaml.v3"
)

const (
	localizationAPI    = "anas.module-localization/v1"
	blockStart         = "<!-- generated:localization:start -->"
	blockEnd           = "<!-- generated:localization:end -->"
	factsBlockStart    = "<!-- generated:module-facts:start -->"
	factsBlockEnd      = "<!-- generated:module-facts:end -->"
	identityBlockStart = "<!-- generated:module-identity:start -->"
	identityBlockEnd   = "<!-- generated:module-identity:end -->"
	topologyBlockStart = "<!-- generated:compose-topology:start -->"
	topologyBlockEnd   = "<!-- generated:compose-topology:end -->"
)

type composeDocument struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image    string    `yaml:"image"`
	Build    yaml.Node `yaml:"build"`
	Networks yaml.Node `yaml:"networks"`
	Volumes  yaml.Node `yaml:"volumes"`
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
	Dir        string
	Manifest   runner.BuiltinModuleInventoryEntry
	Inventory  inventory
	Compose    composeDocument
	Parameters []runner.ConfigParameterInventoryEntry
}

type imageCatalogEntry struct {
	Module string `json:"module"`
	Image  string `json:"image"`
}

func main() {
	check := flag.Bool("check", false, "fail if generated documentation is stale")
	printManagedFiles := flag.Bool("print-managed-files", false, "print repository-relative generated files without writing them")
	root := flag.String("root", "", "repository root (auto-detected by default)")
	flag.Parse()
	if *check && *printManagedFiles {
		fatal(errors.New("--check and --print-managed-files are mutually exclusive"))
	}
	if *root == "" {
		var err error
		*root, err = findRepoRoot()
		if err != nil {
			fatal(err)
		}
	}
	if *printManagedFiles {
		paths, err := managedOutputPaths(*root)
		if err != nil {
			fatal(err)
		}
		for _, path := range paths {
			fmt.Println(path)
		}
		return
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
	outputs, err := renderOutputs(root, !check)
	if err != nil {
		return err
	}
	var stale []string
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

func renderOutputs(root string, projectParameterTables bool) (map[string][]byte, error) {
	builtin, err := runner.LoadBuiltinInventory(root)
	if err != nil {
		return nil, fmt.Errorf("load built-in inventory: %w", err)
	}
	modules, err := loadModules(root, builtin)
	if err != nil {
		return nil, err
	}
	outputs := make(map[string][]byte)
	for _, module := range modules {
		for _, document := range []struct {
			Path             string
			Transform        func(string, moduleDoc) (string, error)
			ParameterHeading string
			English          bool
		}{
			{filepath.Join(module.Dir, "README.md"), syncChineseReadme, "## 所有可用配置参数", false},
			{filepath.Join(module.Dir, "README.en.md"), syncEnglishReadme, "## All configuration parameters", true},
			{filepath.Join(module.Dir, "docs", "technical.md"), syncChineseTechnical, "## 配置契约", false},
			{filepath.Join(module.Dir, "docs", "technical.en.md"), syncEnglishTechnical, "## Configuration contract", true},
		} {
			current, readErr := os.ReadFile(document.Path)
			if readErr != nil {
				return nil, readErr
			}
			base := string(current)
			if len(module.Parameters) > 0 || strings.Contains(base, document.ParameterHeading+"\n") {
				projected, projectErr := syncParameterTable(base, module, document.English, document.ParameterHeading)
				if projectErr != nil {
					return nil, fmt.Errorf("%s: validate parameter table: %w", document.Path, projectErr)
				}
				if projected != base && !projectParameterTables {
					return nil, fmt.Errorf("%s: parameter table is stale; update its machine-derived columns and reviewed purpose text together", document.Path)
				}
				base = projected
			}
			want, transformErr := document.Transform(base, module)
			if transformErr != nil {
				return nil, fmt.Errorf("%s: %w", document.Path, transformErr)
			}
			outputs[document.Path] = []byte(want)
		}
	}
	outputs[filepath.Join(root, "docs", "reference", "module-localization.md")] = renderReference(modules, false)
	outputs[filepath.Join(root, "docs", "en", "reference", "module-localization.md")] = renderReference(modules, true)
	catalogEntries := make([]moduledocs.CatalogEntry, 0, len(builtin.Modules))
	for _, module := range builtin.Modules {
		catalogEntries = append(catalogEntries, moduledocs.CatalogEntry{
			Name: module.Name, Title: module.Title, Version: module.Version, Revision: module.Revision,
			Status: module.Status, Category: module.Category, Description: module.Description,
		})
	}
	outputs[filepath.Join(root, "docs", "reference", "modules.md")] = moduledocs.RenderCatalog(catalogEntries, false)
	outputs[filepath.Join(root, "docs", "en", "reference", "modules.md")] = moduledocs.RenderCatalog(catalogEntries, true)
	for _, document := range []struct {
		Path      string
		English   bool
		Transform func(string, runner.BuiltinInventory, bool) (string, error)
	}{
		{filepath.Join(root, "docs", "reference", "configuration.md"), false, moduledocs.SyncConfiguration},
		{filepath.Join(root, "docs", "en", "reference", "configuration.md"), true, moduledocs.SyncConfiguration},
		{filepath.Join(root, "docs", "architecture", "module-contract-resource-design.md"), false, moduledocs.SyncArchitectureModules},
		{filepath.Join(root, "docs", "en", "architecture", "module-contract-resource-design.md"), true, moduledocs.SyncArchitectureModules},
	} {
		current, readErr := os.ReadFile(document.Path)
		if readErr != nil {
			return nil, readErr
		}
		want, transformErr := document.Transform(string(current), builtin, document.English)
		if transformErr != nil {
			return nil, fmt.Errorf("%s: %w", document.Path, transformErr)
		}
		outputs[document.Path] = []byte(want)
	}
	surface, err := json.MarshalIndent(builtin.Surface(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode built-in inventory golden: %w", err)
	}
	outputs[filepath.Join(root, "internal", "runner", "testdata", "builtin-inventory.golden.json")] = append(surface, '\n')
	return outputs, nil
}

func managedOutputPaths(root string) ([]string, error) {
	outputs, err := renderOutputs(root, true)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(outputs))
	for path := range outputs {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return nil, fmt.Errorf("generated path escapes repository root: %s", path)
		}
		paths = append(paths, filepath.ToSlash(relative))
	}
	sort.Strings(paths)
	return paths, nil
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

func loadModules(root string, builtin runner.BuiltinInventory) ([]moduleDoc, error) {
	parametersByModule := map[string][]runner.ConfigParameterInventoryEntry{}
	for _, parameter := range builtin.Parameters {
		if parameter.Module != "global" {
			parametersByModule[parameter.Module] = append(parametersByModule[parameter.Module], parameter)
		}
	}
	managedImages, err := loadManagedImages(root)
	if err != nil {
		return nil, err
	}
	var modules []moduleDoc
	for _, m := range builtin.Modules {
		dir := filepath.Join(root, "modules", m.Name)
		var inv inventory
		inventoryPath := filepath.Join(dir, "localization.yml")
		if err := decodeYAML(inventoryPath, &inv, true); err != nil {
			return nil, fmt.Errorf("%s: every module must declare versioned localization metadata: %w", inventoryPath, err)
		}
		if err := validate(m, inv); err != nil {
			return nil, fmt.Errorf("%s: %w", inventoryPath, err)
		}
		var compose composeDocument
		composeFile := m.ComposeFile
		if composeFile == "" {
			composeFile = "docker-compose.yml"
		}
		composePath := filepath.Join(dir, composeFile)
		if err := decodeYAML(composePath, &compose, false); err != nil {
			return nil, fmt.Errorf("%s: %w", composePath, err)
		}
		if len(compose.Services) == 0 {
			return nil, fmt.Errorf("%s: services must not be empty", composePath)
		}
		if err := validateManagedComposeImages(compose, managedImages[m.Name], m.Version, m.Revision); err != nil {
			return nil, fmt.Errorf("%s: %w", composePath, err)
		}
		modules = append(modules, moduleDoc{
			Dir: dir, Manifest: m, Inventory: inv, Compose: compose,
			Parameters: parametersByModule[m.Name],
		})
	}
	// BuiltinInventory is already sorted, but keep moduleDoc ordering explicit
	// for callers that construct an inventory directly in tests.
	sort.Slice(modules, func(i, j int) bool { return modules[i].Manifest.Name < modules[j].Manifest.Name })
	return modules, nil
}

func loadManagedImages(root string) (map[string][]string, error) {
	path := filepath.Join(root, ".github", "images.json")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var catalog []imageCatalogEntry
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	images := make(map[string][]string)
	for _, entry := range catalog {
		if entry.Module == "" || entry.Image == "" {
			return nil, fmt.Errorf("%s: every entry requires module and image", path)
		}
		images[entry.Module] = append(images[entry.Module], entry.Image)
	}
	return images, nil
}

func validateManagedComposeImages(compose composeDocument, managed []string, version string, revision int) error {
	expectedTag := fmt.Sprintf("%s-r%d", version, revision)
	for _, managedImage := range managed {
		found := false
		for serviceName, service := range compose.Services {
			image := strings.TrimSpace(service.Image)
			lastSlash := strings.LastIndex(image, "/")
			nameAndTag := image[lastSlash+1:]
			separator := strings.LastIndex(nameAndTag, ":")
			if separator < 0 || nameAndTag[:separator] != managedImage {
				continue
			}
			found = true
			if actual := nameAndTag[separator+1:]; actual != expectedTag {
				return fmt.Errorf("service %s image %s has tag %q, want %q", serviceName, managedImage, actual, expectedTag)
			}
		}
		if !found {
			return fmt.Errorf("managed image %s is not referenced by a service", managedImage)
		}
	}
	return nil
}

func syncChineseReadme(base string, module moduleDoc) (string, error) {
	want, err := replaceRequiredGeneratedBlock(base, factsBlockStart, factsBlockEnd, renderModuleFacts(module, false))
	if err != nil {
		return "", err
	}
	return replaceRequiredGeneratedBlock(want, blockStart, blockEnd, renderModuleBlock(module))
}

func syncEnglishReadme(base string, module moduleDoc) (string, error) {
	want, err := replaceRequiredGeneratedBlock(base, factsBlockStart, factsBlockEnd, renderModuleFacts(module, true))
	if err != nil {
		return "", err
	}
	return want, nil
}

func syncChineseTechnical(base string, module moduleDoc) (string, error) {
	want, err := replaceRequiredGeneratedBlock(base, identityBlockStart, identityBlockEnd, renderModuleIdentity(module, false))
	if err != nil {
		return "", err
	}
	topology, err := renderComposeTopology(module)
	if err != nil {
		return "", err
	}
	want, err = replaceRequiredGeneratedBlock(want, topologyBlockStart, topologyBlockEnd, topology)
	if err != nil {
		return "", err
	}
	return want, nil
}

func syncEnglishTechnical(base string, module moduleDoc) (string, error) {
	want, err := replaceRequiredGeneratedBlock(base, identityBlockStart, identityBlockEnd, renderModuleIdentity(module, true))
	if err != nil {
		return "", err
	}
	topology, err := renderComposeTopology(module)
	if err != nil {
		return "", err
	}
	want, err = replaceRequiredGeneratedBlock(want, topologyBlockStart, topologyBlockEnd, topology)
	if err != nil {
		return "", err
	}
	return want, nil
}

func syncParameterTable(base string, module moduleDoc, english bool, heading string) (string, error) {
	tableStart, tableEnd, err := markdownTableBounds(base, heading)
	if err != nil {
		if len(module.Parameters) == 0 && strings.Contains(err.Error(), "has no parameter table") {
			return base, nil
		}
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(base[tableStart:tableEnd]), "\n")
	if len(lines) < 3 {
		return "", fmt.Errorf("%s parameter table has no rows", heading)
	}
	header := splitMarkdownRow(lines[0])
	if len(header) < 2 {
		return "", fmt.Errorf("%s parameter table header is invalid", heading)
	}
	purposeByPath := map[string]string{}
	recoveredTrailer := ""
	for rowIndex, line := range lines[2:] {
		cells := splitMarkdownRow(line)
		if len(cells) < len(header) {
			return "", fmt.Errorf("%s parameter row has %d cells, want at least %d: %s", heading, len(cells), len(header), line)
		}
		if len(cells) > len(header) {
			if rowIndex != len(lines[2:])-1 || recoveredTrailer != "" {
				return "", fmt.Errorf("%s parameter row has unexpected extra cells: %s", heading, line)
			}
			// Older generator output could consume the separator after the final
			// table row. Recover that following heading or paragraph once, then
			// always emit an explicit blank line below the generated table.
			recoveredTrailer = strings.Join(cells[len(header):], " | ")
			cells = cells[:len(header)]
		}
		path := strings.Trim(strings.TrimSpace(cells[0]), "`")
		if path == "" {
			return "", fmt.Errorf("%s parameter row has an empty path", heading)
		}
		if _, duplicate := purposeByPath[path]; duplicate {
			return "", fmt.Errorf("%s parameter table repeats %s", heading, path)
		}
		purpose := strings.TrimSpace(cells[len(header)-1])
		if purpose == "" {
			return "", fmt.Errorf("%s parameter table has no reviewed purpose for %s", heading, path)
		}
		purposeByPath[path] = purpose
	}
	parameters := append([]runner.ConfigParameterInventoryEntry(nil), module.Parameters...)
	sort.Slice(parameters, func(i, j int) bool { return parameters[i].Path < parameters[j].Path })
	wantPaths := map[string]bool{}
	for _, parameter := range parameters {
		wantPaths[parameter.Path] = true
		if _, ok := purposeByPath[parameter.Path]; !ok {
			return "", fmt.Errorf("%s parameter table is missing %s and its manual purpose", heading, parameter.Path)
		}
	}
	for path := range purposeByPath {
		if !wantPaths[path] {
			return "", fmt.Errorf("%s parameter table contains undeclared path %s", heading, path)
		}
	}

	var out strings.Builder
	if english {
		out.WriteString("| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |\n")
	} else {
		out.WriteString("| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |\n")
	}
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, parameter := range parameters {
		row, err := renderParameterRow(parameter, purposeByPath[parameter.Path], english)
		if err != nil {
			return "", fmt.Errorf("%s: %w", parameter.Path, err)
		}
		out.WriteString(row)
		out.WriteByte('\n')
	}
	suffix := strings.TrimLeft(base[tableEnd:], "\r\n")
	if recoveredTrailer != "" {
		suffix = recoveredTrailer + "\n\n" + suffix
	}
	want := base[:tableStart] + strings.TrimRight(out.String(), "\n")
	if suffix != "" {
		want += "\n\n" + suffix
	} else {
		want += "\n"
	}
	return want, nil
}

func markdownTableBounds(base, heading string) (int, int, error) {
	headingAt := strings.Index(base, heading+"\n")
	if headingAt < 0 {
		return 0, 0, fmt.Errorf("missing heading %q", heading)
	}
	sectionStart := headingAt + len(heading) + 1
	sectionEnd := len(base)
	if next := strings.Index(base[sectionStart:], "\n## "); next >= 0 {
		sectionEnd = sectionStart + next
	}
	tableAt := strings.Index(base[sectionStart:sectionEnd], "\n|")
	if tableAt < 0 {
		return 0, 0, fmt.Errorf("%s has no parameter table", heading)
	}
	start := sectionStart + tableAt + 1
	end := start
	for end < len(base) {
		lineEnd := strings.IndexByte(base[end:], '\n')
		if lineEnd < 0 {
			lineEnd = len(base) - end
		}
		if !strings.HasPrefix(strings.TrimSpace(base[end:end+lineEnd]), "|") {
			break
		}
		end += lineEnd
		if end < len(base) && base[end] == '\n' {
			end++
		}
	}
	return start, end, nil
}

func splitMarkdownRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var raw []string
	var cell strings.Builder
	escaped := false
	for _, char := range line {
		if escaped {
			cell.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			cell.WriteRune(char)
			escaped = true
			continue
		}
		if char == '|' {
			raw = append(raw, strings.TrimSpace(cell.String()))
			cell.Reset()
			continue
		}
		cell.WriteRune(char)
	}
	raw = append(raw, strings.TrimSpace(cell.String()))
	return raw
}

func renderParameterRow(parameter runner.ConfigParameterInventoryEntry, purpose string, english bool) (string, error) {
	typeText := parameter.Type
	if len(parameter.AllowedValues) > 0 {
		allowed := make([]string, 0, len(parameter.AllowedValues))
		for _, value := range parameter.AllowedValues {
			code, err := markdownCode(value)
			if err != nil {
				return "", err
			}
			allowed = append(allowed, code)
		}
		typeText += " (" + strings.Join(allowed, ", ") + ")"
	}
	defaultText := "—"
	if parameter.HasDefault {
		value := parameter.Default
		if value == "" {
			value = `""`
		}
		var err error
		defaultText, err = markdownCode(value)
		if err != nil {
			return "", err
		}
	}
	defaultSource := "—"
	if parameter.DefaultSource != "" && parameter.DefaultSource != "none" {
		var err error
		defaultSource, err = markdownCode(parameter.DefaultSource)
		if err != nil {
			return "", err
		}
	}
	constraintText, err := renderConstraints(parameter.Constraints)
	if err != nil {
		return "", err
	}
	envKey, err := markdownCode(parameter.EnvKey)
	if err != nil {
		return "", err
	}
	effect, err := markdownCode(parameter.Effect)
	if err != nil {
		return "", err
	}
	yes, no := "yes", "no"
	if !english {
		yes, no = "是", "否"
	}
	editable := yes
	if !parameter.Editable {
		command, err := markdownCode(parameter.EditCommand)
		if err != nil {
			return "", err
		}
		if english {
			editable = "no: " + command
		} else {
			editable = "否：" + command
		}
	}
	return fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |",
		parameter.Path, typeText, constraintText, defaultText, defaultSource, envKey,
		boolWord(parameter.InputRequired, yes, no), boolWord(parameter.MustResolve, yes, no),
		boolWord(parameter.Sensitive, yes, no), editable, effect, purpose), nil
}

func renderConstraints(constraints configschema.Constraints) (string, error) {
	parts := []string{}
	if constraints.Minimum != nil && constraints.Maximum != nil {
		parts = append(parts, fmt.Sprintf("%d..%d", *constraints.Minimum, *constraints.Maximum))
	} else if constraints.Minimum != nil {
		parts = append(parts, fmt.Sprintf(">= %d", *constraints.Minimum))
	} else if constraints.Maximum != nil {
		parts = append(parts, fmt.Sprintf("<= %d", *constraints.Maximum))
	}
	if constraints.MinLength != nil && constraints.MaxLength != nil {
		parts = append(parts, fmt.Sprintf("length: %d..%d", *constraints.MinLength, *constraints.MaxLength))
	} else if constraints.MinLength != nil {
		parts = append(parts, fmt.Sprintf("min length: %d", *constraints.MinLength))
	} else if constraints.MaxLength != nil {
		parts = append(parts, fmt.Sprintf("max length: %d", *constraints.MaxLength))
	}
	if constraints.Pattern != "" {
		parts = append(parts, "pattern: "+constraints.Pattern)
	}
	if constraints.Format != "" {
		parts = append(parts, "format: "+constraints.Format)
	}
	if len(parts) == 0 {
		return "—", nil
	}
	for i, part := range parts {
		code, err := markdownCode(part)
		if err != nil {
			return "", err
		}
		parts[i] = code
	}
	return strings.Join(parts, "; "), nil
}

func markdownCode(value string) (string, error) {
	if strings.ContainsAny(value, "`|\r\n") {
		return "", fmt.Errorf("value %q cannot be represented safely in a Markdown table", value)
	}
	return "`" + value + "`", nil
}

func boolWord(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}

func replaceRequiredGeneratedBlock(base, startMarker, endMarker, block string) (string, error) {
	starts := strings.Count(base, startMarker)
	ends := strings.Count(base, endMarker)
	if starts != 1 || ends != 1 {
		return "", fmt.Errorf("generated block %s must have exactly one start and end marker (found %d/%d)", strings.TrimSuffix(strings.TrimPrefix(startMarker, "<!-- generated:"), ":start -->"), starts, ends)
	}
	return replaceMarkedBlock(base, startMarker, endMarker, block)
}

func replaceMarkedBlock(base, startMarker, endMarker, block string) (string, error) {
	start := strings.Index(base, startMarker)
	end := strings.Index(base, endMarker)
	if start < 0 || end < 0 {
		return "", errors.New("generated block markers are missing")
	}
	if end < start {
		return "", errors.New("generated block markers are reversed")
	}
	end += len(endMarker)
	prefix := strings.TrimRight(base[:start], " \n")
	suffix := strings.TrimLeft(base[end:], " \n")
	result := prefix + "\n\n" + strings.TrimRight(block, " \n")
	if suffix != "" {
		result += "\n\n" + suffix
	} else {
		result += "\n"
	}
	return result, nil
}

func renderModuleFacts(module moduleDoc, english bool) string {
	m := module.Manifest
	release := fmt.Sprintf("%s-r%d", m.Version, m.Revision)
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", factsBlockStart)
	if english {
		out.WriteString("| Item | Value |\n| --- | --- |\n")
		fmt.Fprintf(&out, "| Module | `%s` |\n", m.Name)
		fmt.Fprintf(&out, "| Version / revision | `%s` |\n", release)
		fmt.Fprintf(&out, "| Status | `%s` |\n", m.Status)
		fmt.Fprintf(&out, "| Category | `%s` |\n", m.Category)
		fmt.Fprintf(&out, "| Runtime | `%s` |\n", m.RuntimeType)
	} else {
		out.WriteString("| 项目 | 值 |\n| --- | --- |\n")
		fmt.Fprintf(&out, "| Module | `%s` |\n", m.Name)
		fmt.Fprintf(&out, "| 版本 / revision | `%s` |\n", release)
		fmt.Fprintf(&out, "| 状态 | `%s` |\n", m.Status)
		fmt.Fprintf(&out, "| 类别 | `%s` |\n", m.Category)
		fmt.Fprintf(&out, "| 运行时 | `%s` |\n", m.RuntimeType)
	}
	fmt.Fprintf(&out, "%s\n", factsBlockEnd)
	return out.String()
}

func renderModuleIdentity(module moduleDoc, english bool) string {
	m := module.Manifest
	release := fmt.Sprintf("%s-r%d", m.Version, m.Revision)
	var line string
	if english {
		line = fmt.Sprintf("> Status: current implementation; based on `%s` / `%s`.", release, m.APIVersion)
	} else {
		line = fmt.Sprintf("> 状态：当前实现；对应 `%s` / `%s`.", release, m.APIVersion)
	}
	return identityBlockStart + "\n" + line + "\n" + identityBlockEnd + "\n"
}

func renderComposeTopology(module moduleDoc) (string, error) {
	names := make([]string, 0, len(module.Compose.Services))
	for name := range module.Compose.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n", topologyBlockStart)
	out.WriteString("| Service | Image/build | Networks | Volumes |\n| --- | --- | --- | --- |\n")
	for _, name := range names {
		service := module.Compose.Services[name]
		image := strings.TrimSpace(service.Image)
		if image == "" {
			context := composeBuildContext(service.Build)
			if context == "" {
				return "", fmt.Errorf("Compose service %s must declare image or build context", name)
			}
			image = "build: " + context
		}
		networks := composeNodeNames(service.Networks)
		values := append([]string{name, image}, networks...)
		for _, value := range values {
			if strings.ContainsAny(value, "`|\r\n") {
				return "", fmt.Errorf("Compose value %q cannot be represented safely in Markdown", value)
			}
		}
		fmt.Fprintf(&out, "| `%s` | `%s` | `%s` | %d |\n", name, image, strings.Join(networks, ", "), composeSequenceLength(service.Volumes))
	}
	fmt.Fprintf(&out, "%s\n", topologyBlockEnd)
	return out.String(), nil
}

func composeBuildContext(node yaml.Node) string {
	node = unwrapYAMLNode(node)
	if node.Kind == yaml.ScalarNode {
		return strings.TrimSpace(node.Value)
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "context" {
				return strings.TrimSpace(node.Content[i+1].Value)
			}
		}
	}
	return ""
}

func composeNodeNames(node yaml.Node) []string {
	node = unwrapYAMLNode(node)
	values := []string{}
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			value := nodePointerValue(item)
			if value.Kind == yaml.ScalarNode {
				values = append(values, strings.TrimSpace(value.Value))
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			values = append(values, strings.TrimSpace(node.Content[i].Value))
		}
	}
	return values
}

func composeSequenceLength(node yaml.Node) int {
	node = unwrapYAMLNode(node)
	if node.Kind == yaml.SequenceNode {
		return len(node.Content)
	}
	return 0
}

func unwrapYAMLNode(node yaml.Node) yaml.Node {
	for (node.Kind == yaml.DocumentNode || node.Kind == yaml.AliasNode) && len(node.Content) > 0 {
		node = *node.Content[0]
	}
	return node
}

func nodePointerValue(node *yaml.Node) yaml.Node {
	if node == nil {
		return yaml.Node{}
	}
	return unwrapYAMLNode(*node)
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

func validate(m runner.BuiltinModuleInventoryEntry, inv inventory) error {
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
