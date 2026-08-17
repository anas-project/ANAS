// Command gen-contract-docs validates Contract documentation metadata and
// generates reference blocks in the Contract root READMEs plus VitePress pages.
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

	"gopkg.in/yaml.v3"
)

const (
	docAPI     = "anas.contract-documentation/v1"
	blockStart = "<!-- generated:contract-reference:start -->"
	blockEnd   = "<!-- generated:contract-reference:end -->"
)

type localized struct {
	ZH string `yaml:"zh"`
	EN string `yaml:"en"`
}

type metadata struct {
	APIVersion string    `yaml:"api_version"`
	Contract   string    `yaml:"contract"`
	Status     string    `yaml:"status"`
	ReviewedAt string    `yaml:"reviewed_at"`
	Summary    localized `yaml:"summary"`
}

type contractManifest struct {
	APIVersion string               `yaml:"api_version"`
	Kind       string               `yaml:"kind"`
	Name       string               `yaml:"name"`
	Version    string               `yaml:"version"`
	Interfaces []string             `yaml:"interfaces"`
	Resource   resource             `yaml:"resource"`
	Operations map[string]operation `yaml:"operations"`
}

type resource struct {
	Schema   string   `yaml:"schema"`
	Identity []string `yaml:"identity"`
}

type operation struct {
	RequestSchema string `yaml:"request_schema"`
	ResultSchema  string `yaml:"result_schema"`
	Required      bool   `yaml:"required"`
}

type schemaDoc struct {
	Path       string
	Type       string
	Required   []string
	Properties []string
}

type moduleManifest struct {
	Name      string `yaml:"name"`
	Contracts struct {
		Provides []struct {
			Name           string `yaml:"name"`
			Version        string `yaml:"version"`
			Interface      string `yaml:"interface"`
			Implementation string `yaml:"implementation"`
		} `yaml:"provides"`
	} `yaml:"contracts"`
	Dependencies struct {
		Contracts []struct {
			Name       string   `yaml:"name"`
			Version    string   `yaml:"version"`
			Interfaces []string `yaml:"interfaces"`
		} `yaml:"contracts"`
	} `yaml:"dependencies"`
}

type providerRef struct{ Module, Version, Interface, Implementation string }
type consumerRef struct {
	Module, Version string
	Interfaces      []string
}

type contractDoc struct {
	Dir       string
	Manifest  contractManifest
	Metadata  metadata
	Schemas   []schemaDoc
	Providers []providerRef
	Consumers []consumerRef
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

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, "go.mod")) && exists(filepath.Join(dir, "contracts")) {
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
	docs, err := loadContracts(root)
	if err != nil {
		return err
	}
	providers, consumers, err := scanModules(root)
	if err != nil {
		return err
	}
	var stale []string
	for i := range docs {
		docs[i].Providers = providers[docs[i].Manifest.Name]
		docs[i].Consumers = consumers[docs[i].Manifest.Name]
		for _, english := range []bool{false, true} {
			name := "README.md"
			technicalName := "technical.md"
			if english {
				name = "README.en.md"
				technicalName = "technical.en.md"
			}
			path := filepath.Join(docs[i].Dir, name)
			current, readErr := os.ReadFile(path)
			if readErr != nil && !os.IsNotExist(readErr) {
				return readErr
			}
			base := string(current)
			if len(current) == 0 {
				title := docs[i].Manifest.Name + " Contract"
				summary := docs[i].Metadata.Summary.ZH
				if english {
					summary = docs[i].Metadata.Summary.EN
				}
				base = "# " + title + "\n\n" + summary + "\n"
			}
			want, replaceErr := replaceGeneratedBlock(base, renderBlock(docs[i], english))
			if replaceErr != nil {
				return fmt.Errorf("%s: %w", path, replaceErr)
			}
			if err := update(path, []byte(want), check, &stale); err != nil {
				return err
			}
			site := filepath.Join(root, "docs", "reference", "contracts", docs[i].Manifest.Name+".md")
			if english {
				site = filepath.Join(root, "docs", "en", "reference", "contracts", docs[i].Manifest.Name+".md")
			}
			banner := "> This page is generated from the Contract root README. Do not edit it directly.\n\n"
			if !english {
				banner = "> 本页由 Contract 根目录 README 生成，请勿直接编辑。\n\n"
			}
			siteREADME := rewriteSiteLinks(want, docs[i].Manifest.Name, english, false)
			if err := update(site, []byte(banner+siteREADME), check, &stale); err != nil {
				return err
			}

			technicalSource := filepath.Join(docs[i].Dir, "docs", technicalName)
			technical, readErr := os.ReadFile(technicalSource)
			if readErr != nil {
				return fmt.Errorf("%s: every Contract needs bilingual technical documentation: %w", technicalSource, readErr)
			}
			technicalSite := filepath.Join(root, "docs", "reference", "contracts", docs[i].Manifest.Name+"-technical.md")
			if english {
				technicalSite = filepath.Join(root, "docs", "en", "reference", "contracts", docs[i].Manifest.Name+"-technical.md")
			}
			technicalBanner := "> This page is generated from the Contract technical documentation. Do not edit it directly.\n\n"
			if !english {
				technicalBanner = "> 本页由 Contract 技术文档生成，请勿直接编辑。\n\n"
			}
			siteTechnical := rewriteSiteLinks(string(technical), docs[i].Manifest.Name, english, true)
			if err := update(technicalSite, []byte(technicalBanner+siteTechnical), check, &stale); err != nil {
				return err
			}
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("generated Contract documentation is stale:\n  %s\nrun: go run ./cmd/gen-contract-docs", strings.Join(stale, "\n  "))
	}
	return nil
}

func rewriteSiteLinks(markdown, contractName string, english, technical bool) string {
	if technical {
		source := "../README.md"
		if english {
			source = "../README.en.md"
		}
		return strings.ReplaceAll(markdown, "]("+source+")", "](./"+contractName+".md)")
	}
	source := "docs/technical.md"
	if english {
		source = "docs/technical.en.md"
	}
	return strings.ReplaceAll(markdown, "]("+source+")", "](./"+contractName+"-technical.md)")
}

func loadContracts(root string) ([]contractDoc, error) {
	entries, err := os.ReadDir(filepath.Join(root, "contracts"))
	if err != nil {
		return nil, err
	}
	var out []contractDoc
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, "contracts", entry.Name())
		var manifest contractManifest
		if err := decode(filepath.Join(dir, "contract.yml"), &manifest, true); err != nil {
			return nil, err
		}
		var meta metadata
		metaPath := filepath.Join(dir, "documentation.yml")
		if err := decode(metaPath, &meta, true); err != nil {
			return nil, fmt.Errorf("%s: every Contract needs documentation metadata: %w", metaPath, err)
		}
		if err := validate(entry.Name(), manifest, meta); err != nil {
			return nil, fmt.Errorf("%s: %w", metaPath, err)
		}
		schemas, err := loadSchemas(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, contractDoc{Dir: dir, Manifest: manifest, Metadata: meta, Schemas: schemas})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	if len(out) == 0 {
		return nil, errors.New("no Contracts found")
	}
	return out, nil
}

func validate(dirname string, manifest contractManifest, meta metadata) error {
	if manifest.APIVersion != "anas.contract/v1" || manifest.Kind != "Contract" {
		return errors.New("contract.yml is not an anas.contract/v1 Contract")
	}
	if manifest.Name != dirname || meta.Contract != dirname {
		return fmt.Errorf("directory, contract.yml name, and documentation.yml contract must all be %q", dirname)
	}
	if meta.APIVersion != docAPI {
		return fmt.Errorf("api_version must be %s", docAPI)
	}
	if !map[string]bool{"implemented": true, "partial": true, "pending": true, "proposal": true, "deprecated": true}[meta.Status] {
		return fmt.Errorf("invalid status %q", meta.Status)
	}
	if manifest.Version == "" || len(manifest.Interfaces) == 0 || len(manifest.Operations) == 0 {
		return errors.New("version, interfaces, and operations are required")
	}
	if meta.ReviewedAt == "" || meta.Summary.ZH == "" || meta.Summary.EN == "" {
		return errors.New("reviewed_at and both summaries are required")
	}
	return nil
}

func loadSchemas(dir string) ([]schemaDoc, error) {
	root := filepath.Join(dir, "schemas")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []schemaDoc
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		var raw struct {
			Type       string               `yaml:"type"`
			Required   []string             `yaml:"required"`
			Properties map[string]yaml.Node `yaml:"properties"`
		}
		if err := decode(path, &raw, false); err != nil {
			return nil, err
		}
		props := make([]string, 0, len(raw.Properties))
		for name := range raw.Properties {
			props = append(props, name)
		}
		sort.Strings(props)
		out = append(out, schemaDoc{Path: filepath.ToSlash(filepath.Join("schemas", entry.Name())), Type: raw.Type, Required: raw.Required, Properties: props})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func scanModules(root string) (map[string][]providerRef, map[string][]consumerRef, error) {
	providers := map[string][]providerRef{}
	consumers := map[string][]consumerRef{}
	entries, err := os.ReadDir(filepath.Join(root, "modules"))
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, "modules", entry.Name(), "module.yml")
		if !exists(path) {
			continue
		}
		var module moduleManifest
		if err := decode(path, &module, false); err != nil {
			return nil, nil, err
		}
		for _, item := range module.Contracts.Provides {
			providers[item.Name] = append(providers[item.Name], providerRef{module.Name, item.Version, item.Interface, item.Implementation})
		}
		for _, item := range module.Dependencies.Contracts {
			consumers[item.Name] = append(consumers[item.Name], consumerRef{module.Name, item.Version, item.Interfaces})
		}
	}
	for key := range providers {
		sort.Slice(providers[key], func(i, j int) bool { return providers[key][i].Module < providers[key][j].Module })
	}
	for key := range consumers {
		sort.Slice(consumers[key], func(i, j int) bool { return consumers[key][i].Module < consumers[key][j].Module })
	}
	return providers, consumers, nil
}

func renderBlock(doc contractDoc, english bool) string {
	var out strings.Builder
	fmt.Fprintln(&out, blockStart)
	if english {
		fmt.Fprint(&out, "## Generated contract reference\n\n> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.\n\n")
	} else {
		fmt.Fprint(&out, "## 生成的 Contract 参考\n\n> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。\n\n")
	}
	fmt.Fprintf(&out, "- Version / 版本：`%s`\n- Status / 状态：`%s`（reviewed %s）\n- Interfaces / 接口：%s\n- Resource identity / 资源标识：%s\n- Resource schema / 资源 Schema：`%s`\n\n",
		doc.Manifest.Version, doc.Metadata.Status, doc.Metadata.ReviewedAt, codeList(doc.Manifest.Interfaces), codeList(doc.Manifest.Resource.Identity), doc.Manifest.Resource.Schema)
	if english {
		fmt.Fprint(&out, "### Operations\n\n")
	} else {
		fmt.Fprint(&out, "### Operations / 操作\n\n")
	}
	fmt.Fprintln(&out, "| Operation | Required | Request schema | Result schema |\n| --- | --- | --- | --- |")
	ops := make([]string, 0, len(doc.Manifest.Operations))
	for name := range doc.Manifest.Operations {
		ops = append(ops, name)
	}
	sort.Strings(ops)
	for _, name := range ops {
		op := doc.Manifest.Operations[name]
		fmt.Fprintf(&out, "| `%s` | `%t` | %s | %s |\n", name, op.Required, codeOrDash(op.RequestSchema), codeOrDash(op.ResultSchema))
	}
	if english {
		fmt.Fprint(&out, "\n### Schemas\n\n")
	} else {
		fmt.Fprint(&out, "\n### Schemas / 字段\n\n")
	}
	fmt.Fprintln(&out, "| Schema | Type | Required fields | All fields |\n| --- | --- | --- | --- |")
	for _, schema := range doc.Schemas {
		fmt.Fprintf(&out, "| `%s` | `%s` | %s | %s |\n", schema.Path, placeholder(schema.Type), codeListOrDash(schema.Required), codeListOrDash(schema.Properties))
	}
	if english {
		fmt.Fprint(&out, "\n### Current providers and consumers\n\n")
	} else {
		fmt.Fprint(&out, "\n### 当前 Provider 与 Consumer\n\n")
	}
	fmt.Fprintln(&out, "| Role | Module | Version constraint | Interface | Implementation |\n| --- | --- | --- | --- | --- |")
	for _, item := range doc.Providers {
		fmt.Fprintf(&out, "| provider | `%s` | `%s` | `%s` | `%s` |\n", item.Module, item.Version, item.Interface, item.Implementation)
	}
	for _, item := range doc.Consumers {
		fmt.Fprintf(&out, "| consumer | `%s` | `%s` | %s | - |\n", item.Module, item.Version, codeList(item.Interfaces))
	}
	if len(doc.Providers)+len(doc.Consumers) == 0 {
		fmt.Fprintln(&out, "| - | - | - | - | - |")
	}
	fmt.Fprintln(&out, blockEnd)
	return out.String()
}

func replaceGeneratedBlock(base, block string) (string, error) {
	start, end := strings.Index(base, blockStart), strings.Index(base, blockEnd)
	if (start < 0) != (end < 0) {
		return "", errors.New("generated markers are unbalanced")
	}
	if start >= 0 {
		if end < start {
			return "", errors.New("generated markers are reversed")
		}
		end += len(blockEnd)
		return strings.TrimRight(base[:start], " \n") + "\n\n" + block + strings.TrimLeft(base[end:], " \n"), nil
	}
	return strings.TrimRight(base, " \n") + "\n\n" + block, nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, want, 0644)
}

func decode(path string, out any, strict bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(strict)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func codeList(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = "`" + value + "`"
	}
	return strings.Join(out, ", ")
}
func codeListOrDash(values []string) string { return codeList(values) }
func codeOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return "`" + value + "`"
}
func placeholder(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
func exists(path string) bool { _, err := os.Stat(path); return err == nil }
