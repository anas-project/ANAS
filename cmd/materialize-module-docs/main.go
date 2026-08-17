// Command materialize-module-docs renders Module source documentation into a
// temporary VitePress source tree. It never edits the repository documentation
// tree: callers must pass a disposable copy through --docs-root.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

const defaultVersionLimit = 5

var (
	releaseTagPattern  = regexp.MustCompile(`^module/([a-z0-9_]+)/(.+)-r([1-9][0-9]*)$`)
	versionLinePattern = regexp.MustCompile(`(?m)^- Module version / 版本：.*$`)
)

type manifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Revision    int    `yaml:"revision"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	Status      string `yaml:"status"`
}

type catalogEntry struct {
	Module string `json:"module"`
}

type documentBundle struct {
	ReadmeZH    string
	ReadmeEN    string
	TechnicalZH string
	TechnicalEN string
}

type moduleRelease struct {
	Module   string
	Version  string
	Revision int
	Tag      string
	Commit   string
	Parsed   *semver.Version
	Manifest manifest
	Docs     documentBundle
	Complete bool
}

func (r moduleRelease) ID() string { return fmt.Sprintf("%s-r%d", r.Version, r.Revision) }

type versionPage struct {
	Release     moduleRelease
	Fingerprint string
}

type moduleBuild struct {
	Manifest manifest
	Current  documentBundle
	Pages    []versionPage
	Aliases  map[string]string
}

type indexFile struct {
	Modules []indexModule `json:"modules"`
}

type indexModule struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Category    string            `json:"category"`
	Status      string            `json:"status"`
	Current     string            `json:"current"`
	LinkZH      string            `json:"link_zh"`
	LinkEN      string            `json:"link_en"`
	Versions    []indexVersion    `json:"versions"`
	Aliases     map[string]string `json:"aliases,omitempty"`
}

type indexVersion struct {
	Release     string `json:"release"`
	Fingerprint string `json:"fingerprint"`
	LinkZH      string `json:"link_zh"`
	LinkEN      string `json:"link_en"`
}

func main() {
	root := flag.String("root", "", "repository root")
	docsRoot := flag.String("docs-root", "", "disposable VitePress docs root")
	limit := flag.Int("version-limit", defaultVersionLimit, "maximum upstream versions per Module before deduplication")
	releaseMode := flag.Bool("release-mode", false, "use the newest complete Module release as the current page")
	flag.Parse()

	if *root == "" || *docsRoot == "" {
		fatal(errors.New("--root and --docs-root are required"))
	}
	if *limit < 1 {
		fatal(errors.New("--version-limit must be positive"))
	}
	if err := run(*root, *docsRoot, *limit, *releaseMode); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func run(root, docsRoot string, limit int, releaseMode bool) error {
	modules, err := loadCurrentModules(root)
	if err != nil {
		return err
	}
	if err := validateCatalog(root, modules); err != nil {
		return err
	}

	releases, err := loadReleases(root)
	if err != nil {
		return err
	}
	byModule := make(map[string][]moduleRelease)
	for _, release := range releases {
		byModule[release.Module] = append(byModule[release.Module], release)
	}

	builds := make([]moduleBuild, 0, len(modules))
	for _, current := range modules {
		selected, oldRevisionAliases := selectReleases(byModule[current.Manifest.Name], limit)
		for i := range selected {
			docs, complete, err := readBundleFromGit(root, selected[i])
			if err != nil {
				return err
			}
			selected[i].Docs, selected[i].Complete = docs, complete
		}
		pages, duplicateAliases := deduplicateReleases(selected)
		for source, target := range duplicateAliases {
			oldRevisionAliases[source] = target
		}
		available := make(map[string]bool, len(pages))
		for _, page := range pages {
			available[page.Release.ID()] = true
		}
		for source, target := range oldRevisionAliases {
			if !available[target] {
				delete(oldRevisionAliases, source)
			}
		}
		currentManifest, currentDocs := current.Manifest, current.Docs
		if releaseMode && len(selected) > 0 && selected[0].Complete {
			currentManifest, currentDocs = selected[0].Manifest, selected[0].Docs
		}
		builds = append(builds, moduleBuild{
			Manifest: currentManifest,
			Current:  currentDocs,
			Pages:    pages,
			Aliases:  oldRevisionAliases,
		})
	}

	for _, build := range builds {
		if err := renderModule(docsRoot, build); err != nil {
			return err
		}
	}
	if err := renderCatalogs(docsRoot, builds); err != nil {
		return err
	}
	return renderIndex(docsRoot, builds)
}

type currentModule struct {
	Manifest manifest
	Docs     documentBundle
}

func loadCurrentModules(root string) ([]currentModule, error) {
	entries, err := os.ReadDir(filepath.Join(root, "modules"))
	if err != nil {
		return nil, err
	}
	var modules []currentModule
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, "modules", entry.Name())
		manifestPath := filepath.Join(dir, "module.yml")
		if _, err := os.Stat(manifestPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var item manifest
		if err := decodeYAML(manifestPath, &item); err != nil {
			return nil, err
		}
		if item.Name != entry.Name() || item.Version == "" || item.Revision < 1 || item.Title == "" {
			return nil, fmt.Errorf("%s: directory/name, version, revision, and title must be valid", manifestPath)
		}
		docs, err := readBundleFromDisk(dir)
		if err != nil {
			return nil, err
		}
		modules = append(modules, currentModule{Manifest: item, Docs: docs})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Manifest.Name < modules[j].Manifest.Name })
	if len(modules) == 0 {
		return nil, errors.New("no Modules found")
	}
	return modules, nil
}

func readBundleFromDisk(dir string) (documentBundle, error) {
	paths := []string{"README.md", "README.en.md", "docs/technical.md", "docs/technical.en.md"}
	values := make([]string, len(paths))
	for i, name := range paths {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return documentBundle{}, fmt.Errorf("%s: every Module requires bilingual user and technical documentation: %w", filepath.Join(dir, name), err)
		}
		values[i] = string(data)
	}
	return documentBundle{values[0], values[1], values[2], values[3]}, nil
}

func validateCatalog(root string, modules []currentModule) error {
	data, err := os.ReadFile(filepath.Join(root, ".github", "modules.json"))
	if err != nil {
		return err
	}
	var entries []catalogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf(".github/modules.json: %w", err)
	}
	want := make(map[string]bool, len(modules))
	for _, item := range modules {
		want[item.Manifest.Name] = true
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Module == "" || seen[entry.Module] {
			return fmt.Errorf(".github/modules.json contains an empty or duplicate Module %q", entry.Module)
		}
		seen[entry.Module] = true
	}
	var differences []string
	for name := range want {
		if !seen[name] {
			differences = append(differences, "missing from .github/modules.json: "+name)
		}
	}
	for name := range seen {
		if !want[name] {
			differences = append(differences, "missing modules/"+name+"/module.yml")
		}
	}
	if len(differences) > 0 {
		sort.Strings(differences)
		return errors.New(strings.Join(differences, "\n"))
	}
	return nil
}

func loadReleases(root string) ([]moduleRelease, error) {
	cmd := exec.Command("git", "tag", "--list", "module/*")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list Module tags: %w", err)
	}
	var releases []moduleRelease
	for _, raw := range strings.Split(string(out), "\n") {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		match := releaseTagPattern.FindStringSubmatch(tag)
		if match == nil {
			return nil, fmt.Errorf("invalid Module release tag %q", tag)
		}
		parsed, err := semver.NewVersion(match[2])
		if err != nil {
			return nil, fmt.Errorf("invalid Module version in tag %q: %w", tag, err)
		}
		revision, _ := strconv.Atoi(match[3])
		commit, err := gitOutput(root, "rev-list", "-n1", tag)
		if err != nil {
			return nil, err
		}
		release := moduleRelease{Module: match[1], Version: match[2], Revision: revision, Tag: tag, Commit: strings.TrimSpace(commit), Parsed: parsed}
		manifestSource, err := gitOutput(root, "show", tag+":modules/"+release.Module+"/module.yml")
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal([]byte(manifestSource), &release.Manifest); err != nil {
			return nil, fmt.Errorf("%s module.yml: %w", tag, err)
		}
		if release.Manifest.Name != release.Module || release.Manifest.Version != release.Version || release.Manifest.Revision != release.Revision {
			return nil, fmt.Errorf("%s does not match its Module manifest identity", tag)
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func readBundleFromGit(root string, release moduleRelease) (documentBundle, bool, error) {
	paths := []string{"README.md", "README.en.md", "docs/technical.md", "docs/technical.en.md"}
	values := make([]string, len(paths))
	for i, name := range paths {
		value, err := gitOutput(root, "show", release.Tag+":modules/"+release.Module+"/"+name)
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				// Releases created before versioned Module documentation was
				// introduced are intentionally not synthesized from newer files.
				return documentBundle{}, false, nil
			}
			return documentBundle{}, false, err
		}
		values[i] = value
	}
	return documentBundle{values[0], values[1], values[2], values[3]}, true, nil
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return string(out), nil
}

func selectReleases(all []moduleRelease, limit int) ([]moduleRelease, map[string]string) {
	byVersion := make(map[string][]moduleRelease)
	for _, release := range all {
		byVersion[release.Version] = append(byVersion[release.Version], release)
	}
	var latest []moduleRelease
	aliases := make(map[string]string)
	for _, revisions := range byVersion {
		sort.Slice(revisions, func(i, j int) bool { return revisions[i].Revision > revisions[j].Revision })
		selected := revisions[0]
		latest = append(latest, selected)
		for _, old := range revisions[1:] {
			aliases[old.ID()] = selected.ID()
		}
	}
	sort.Slice(latest, func(i, j int) bool {
		return latest[i].Parsed.GreaterThan(latest[j].Parsed)
	})
	if len(latest) > limit {
		latest = latest[:limit]
	}
	return latest, aliases
}

func deduplicateReleases(selected []moduleRelease) ([]versionPage, map[string]string) {
	seen := make(map[string]string)
	aliases := make(map[string]string)
	var pages []versionPage
	for _, release := range selected {
		if !release.Complete {
			continue
		}
		fingerprint := documentFingerprint(release.Docs)
		if canonical, ok := seen[fingerprint]; ok {
			aliases[release.ID()] = canonical
			continue
		}
		seen[fingerprint] = release.ID()
		pages = append(pages, versionPage{Release: release, Fingerprint: fingerprint})
	}
	return pages, aliases
}

func documentFingerprint(docs documentBundle) string {
	parts := []string{docs.ReadmeZH, docs.ReadmeEN, docs.TechnicalZH, docs.TechnicalEN}
	for i, value := range parts {
		value = strings.ReplaceAll(value, "\r\n", "\n")
		value = versionLinePattern.ReplaceAllString(value, "- Module version / 版本：`<release>`")
		lines := strings.Split(value, "\n")
		for j := range lines {
			lines[j] = strings.TrimRight(lines[j], " \t")
		}
		parts[i] = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n\x00\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func renderModule(docsRoot string, build moduleBuild) error {
	current := fmt.Sprintf("%s-r%d", build.Manifest.Version, build.Manifest.Revision)
	versions := make([]string, 0, len(build.Pages))
	for _, page := range build.Pages {
		versions = append(versions, page.Release.ID())
	}

	for _, english := range []bool{false, true} {
		base := filepath.Join(docsRoot, "reference", "modules", build.Manifest.Name)
		if english {
			base = filepath.Join(docsRoot, "en", "reference", "modules", build.Manifest.Name)
		}
		readme := build.Current.ReadmeZH
		technical := build.Current.TechnicalZH
		if english {
			readme, technical = build.Current.ReadmeEN, build.Current.TechnicalEN
		}
		if err := writePage(filepath.Join(base, "index.md"), renderUserPage(build.Manifest, current, "master", readme, versions, english, false)); err != nil {
			return err
		}
		if err := writePage(filepath.Join(base, "technical.md"), renderTechnicalPage(build.Manifest, current, "master", technical, versions, english, false)); err != nil {
			return err
		}
		for _, page := range build.Pages {
			releaseBase := filepath.Join(base, page.Release.ID())
			pageReadme := page.Release.Docs.ReadmeZH
			pageTechnical := page.Release.Docs.TechnicalZH
			if english {
				pageReadme, pageTechnical = page.Release.Docs.ReadmeEN, page.Release.Docs.TechnicalEN
			}
			if err := writePage(filepath.Join(releaseBase, "index.md"), renderUserPage(build.Manifest, page.Release.ID(), page.Release.Commit, pageReadme, versions, english, true)); err != nil {
				return err
			}
			if err := writePage(filepath.Join(releaseBase, "technical.md"), renderTechnicalPage(build.Manifest, page.Release.ID(), page.Release.Commit, pageTechnical, versions, english, true)); err != nil {
				return err
			}
		}
		for source, target := range build.Aliases {
			if err := writePage(filepath.Join(base, source, "index.md"), renderAliasPage(build.Manifest.Title, source, target, english)); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderUserPage(m manifest, release, ref, source string, versions []string, english, fixed bool) []byte {
	body := rewriteUserLinks(source)
	return renderPage(m, release, ref, body, versions, english, fixed, false)
}

func renderTechnicalPage(m manifest, release, ref, source string, versions []string, english, fixed bool) []byte {
	body := rewriteTechnicalLinks(source, m.Name, ref, english)
	return renderPage(m, release, ref, body, versions, english, fixed, true)
}

func renderPage(m manifest, release, ref, body string, versions []string, english, fixed, technical bool) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "---\ntitle: %s\nmodule: %s\nmoduleRelease: %s\nmodulePage: true\nmoduleTechnical: %t\neditLink: false\nlastUpdated: false\n---\n\n", strconv.Quote(m.Title), m.Name, strconv.Quote(release), technical)
	if english {
		fmt.Fprintf(&out, "> Generated from the Module source documentation for `%s`. Do not edit this page directly.\n\n", release)
	} else {
		fmt.Fprintf(&out, "> 本页由 Module `%s` 的源文档生成，请勿直接编辑。\n\n", release)
	}
	out.WriteString(renderVersionNavigation(m.Name, release, versions, english, fixed, technical))
	out.WriteString("\n")
	out.WriteString(strings.TrimSpace(body))
	out.WriteString("\n")
	return []byte(out.String())
}

func renderVersionNavigation(module, release string, versions []string, english, fixed, technical bool) string {
	prefix := "/reference/modules/"
	label, availableLabel, currentLabel := "Module 文档版本", "可选版本", "最新文档"
	userLabel, technicalLabel, snapshotLabel := "用户文档", "技术文档", "固定快照"
	if english {
		prefix, label, availableLabel, currentLabel = "/en/reference/modules/", "Module documentation release", "Available releases", "Latest documentation"
		userLabel, technicalLabel, snapshotLabel = "User documentation", "Technical documentation", "immutable snapshot"
	}
	currentSuffix := ""
	if technical {
		currentSuffix = "technical"
	}
	items := []string{fmt.Sprintf("[%s](%s%s/%s)", currentLabel, prefix, module, currentSuffix)}
	for _, version := range versions {
		suffix := "/"
		if technical {
			suffix = "/technical"
		}
		items = append(items, fmt.Sprintf("[%s](%s%s/%s%s)", version, prefix, module, version, suffix))
	}
	pageBase := prefix + module + "/"
	mode := currentLabel
	if fixed {
		pageBase += release + "/"
		mode = snapshotLabel
	}
	versionList := strings.Join(items, " · ")
	if len(versions) == 0 {
		versionList = currentLabel
	}
	return fmt.Sprintf("**%s：** `%s`（%s） · [%s](%s) · [%s](%stechnical)  \n**%s：** %s", label, release, mode, userLabel, pageBase, technicalLabel, pageBase, availableLabel, versionList)
}

func rewriteUserLinks(source string) string {
	source = strings.ReplaceAll(source, "](docs/technical.md)", "](./technical)")
	source = strings.ReplaceAll(source, "](docs/technical.en.md)", "](./technical)")
	return strings.ReplaceAll(source, "](../../docs/", "](/")
}

func rewriteTechnicalLinks(source, module, ref string, english bool) string {
	source = strings.ReplaceAll(source, "](../README.md)", "](./)")
	source = strings.ReplaceAll(source, "](../README.en.md)", "](./)")
	re := regexp.MustCompile(`\]\(\.\./([^)]+)\)`)
	return re.ReplaceAllStringFunc(source, func(match string) string {
		parts := re.FindStringSubmatch(match)
		return "](" + "https://github.com/anas-project/ANAS/blob/" + ref + "/modules/" + module + "/" + parts[1] + ")"
	})
}

func renderAliasPage(title, source, target string, english bool) []byte {
	heading := "文档版本已合并"
	message := fmt.Sprintf("`%s` 的文档与 `%s` 完全一致，仅保留较新的文档正文。", source, target)
	link := "查看保留的文档"
	if english {
		heading = "Documentation version consolidated"
		message = fmt.Sprintf("The documentation for `%s` is identical to `%s`; only the newer body is retained.", source, target)
		link = "View the retained documentation"
	}
	return []byte(fmt.Sprintf("---\neditLink: false\nlastUpdated: false\n---\n\n# %s — %s\n\n%s\n\n[%s](../%s/)\n", title, heading, message, link, target))
}

func renderCatalogs(docsRoot string, builds []moduleBuild) error {
	for _, english := range []bool{false, true} {
		var out strings.Builder
		if english {
			out.WriteString("---\neditLink: false\nlastUpdated: false\n---\n\n# Module catalog\n\nThis page is generated at documentation build time from the current Module manifests. Module pages are generated from their source READMEs; do not maintain a second mapping list.\n\n")
		} else {
			out.WriteString("---\neditLink: false\nlastUpdated: false\n---\n\n# Module 目录\n\n本页在文档构建时由当前 Module manifests 生成。Module 页面直接来自各自的 README，不维护第二份映射清单。\n\n")
		}
		out.WriteString("| Module | Version | Status | Category | Description |\n| --- | --- | --- | --- | --- |\n")
		for _, build := range builds {
			link := "/reference/modules/" + build.Manifest.Name + "/"
			if english {
				link = "/en" + link
			}
			fmt.Fprintf(&out, "| [%s](%s) | `%s-r%d` | `%s` | `%s` | %s |\n", build.Manifest.Title, link, build.Manifest.Version, build.Manifest.Revision, build.Manifest.Status, build.Manifest.Category, strings.ReplaceAll(build.Manifest.Description, "|", "\\|"))
		}
		path := filepath.Join(docsRoot, "reference", "modules.md")
		if english {
			path = filepath.Join(docsRoot, "en", "reference", "modules.md")
		}
		if err := writePage(path, []byte(out.String())); err != nil {
			return err
		}
	}
	return nil
}

func renderIndex(docsRoot string, builds []moduleBuild) error {
	index := indexFile{Modules: make([]indexModule, 0, len(builds))}
	for _, build := range builds {
		item := indexModule{
			Name: build.Manifest.Name, Title: build.Manifest.Title, Description: build.Manifest.Description,
			Category: build.Manifest.Category, Status: build.Manifest.Status,
			Current: fmt.Sprintf("%s-r%d", build.Manifest.Version, build.Manifest.Revision),
			LinkZH:  "/reference/modules/" + build.Manifest.Name + "/",
			LinkEN:  "/en/reference/modules/" + build.Manifest.Name + "/",
			Aliases: build.Aliases,
		}
		for _, page := range build.Pages {
			item.Versions = append(item.Versions, indexVersion{
				Release: page.Release.ID(), Fingerprint: page.Fingerprint,
				LinkZH: "/reference/modules/" + build.Manifest.Name + "/" + page.Release.ID() + "/",
				LinkEN: "/en/reference/modules/" + build.Manifest.Name + "/" + page.Release.ID() + "/",
			})
		}
		index.Modules = append(index.Modules, item)
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writePage(filepath.Join(docsRoot, ".vitepress", "generated", "module-docs.json"), data)
}

func writePage(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func decodeYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
