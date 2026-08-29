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
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/anas-project/ANAS/internal/moduledocs"
	"github.com/anas-project/ANAS/internal/runner"
	"gopkg.in/yaml.v3"
)

const defaultVersionLimit = 5

var (
	releaseTagPattern  = regexp.MustCompile(`^module/([a-z0-9_]+)/(.+)-r([1-9][0-9]*)$`)
	versionLinePattern = regexp.MustCompile(`(?m)^- Module version / 版本：.*$`)
	siteLinkPattern    = regexp.MustCompile(`\]\((/[^)]*)\)`)
)

// repositoryBlobBase addresses documentation that the built site cannot serve.
const repositoryBlobBase = "https://github.com/anas-project/ANAS/blob/"

type manifest struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Revision    int    `yaml:"revision"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	Status      string `yaml:"status"`
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

	// Release builds pair the current Module tree with the core `docs/` tree of
	// the released core tag, so Module documentation may legitimately reference
	// core pages the site does not contain yet. Builds that take both trees from
	// the same source have no such gap: there an unresolvable link is an
	// authoring error and must keep failing the dead-link check.
	resolves := siteLinkResolver(alwaysResolves)
	if releaseMode {
		resolves = documentationPageResolver(docsRoot)
	}
	for _, build := range builds {
		if err := renderModule(docsRoot, build, resolves); err != nil {
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
	inventory, err := runner.LoadBuiltinInventory(root)
	if err != nil {
		return nil, err
	}
	modules := make([]currentModule, 0, len(inventory.Modules))
	for _, item := range inventory.Modules {
		dir := filepath.Join(root, "modules", item.Name)
		docs, err := readBundleFromDisk(dir)
		if err != nil {
			return nil, err
		}
		modules = append(modules, currentModule{Manifest: manifest{
			Name: item.Name, Version: item.Version, Revision: item.Revision,
			Title: item.Title, Description: item.Description, Category: item.Category, Status: item.Status,
		}, Docs: docs})
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
		fingerprint := documentFingerprint(release.Docs, release.ID())
		if canonical, ok := seen[fingerprint]; ok {
			aliases[release.ID()] = canonical
			continue
		}
		seen[fingerprint] = release.ID()
		pages = append(pages, versionPage{Release: release, Fingerprint: fingerprint})
	}
	return pages, aliases
}

func documentFingerprint(docs documentBundle, releaseID string) string {
	parts := []string{docs.ReadmeZH, docs.ReadmeEN, docs.TechnicalZH, docs.TechnicalEN}
	for i, value := range parts {
		value = strings.ReplaceAll(value, "\r\n", "\n")
		var localizationFound bool
		value, localizationFound = normalizeFingerprintBlock(value, "<!-- generated:localization:start -->", "<!-- generated:localization:end -->", func(block string) string {
			return versionLinePattern.ReplaceAllString(block, "- Module version / 版本：`<release>`")
		})
		if !localizationFound {
			value = versionLinePattern.ReplaceAllString(value, "- Module version / 版本：`<release>`")
		}
		foundBlocks := make([]bool, 3)
		for blockIndex, markers := range [][2]string{
			{"<!-- generated:module-facts:start -->", "<!-- generated:module-facts:end -->"},
			{"<!-- generated:module-identity:start -->", "<!-- generated:module-identity:end -->"},
			{"<!-- generated:compose-topology:start -->", "<!-- generated:compose-topology:end -->"},
		} {
			transform := func(block string) string { return normalizeFactsRelease(block, releaseID) }
			if blockIndex == 1 {
				transform = func(block string) string { return normalizeIdentityRelease(block, releaseID) }
			} else if blockIndex == 2 {
				transform = func(block string) string { return normalizeTopologyRelease(block, releaseID) }
			}
			value, foundBlocks[blockIndex] = normalizeFingerprintBlock(value, markers[0], markers[1], transform)
		}
		value = normalizeLegacyReleaseLabels(value, releaseID, !foundBlocks[0], !foundBlocks[1], !foundBlocks[2])
		lines := strings.Split(value, "\n")
		for j := range lines {
			lines[j] = strings.TrimRight(lines[j], " \t")
		}
		parts[i] = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n\x00\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizeFingerprintBlock(value, startMarker, endMarker string, transform func(string) string) (string, bool) {
	start := strings.Index(value, startMarker)
	end := strings.Index(value, endMarker)
	if start < 0 || end < start {
		return value, false
	}
	contentStart := start + len(startMarker)
	content := strings.Trim(value[contentStart:end], " \n")
	normalized := transform(content)
	prefix := strings.TrimRight(value[:start], " \n")
	suffix := strings.TrimLeft(value[end+len(endMarker):], " \n")
	result := prefix + "\n\n" + normalized
	if suffix != "" {
		result += "\n\n" + suffix
	}
	return result, true
}

func normalizeFactsRelease(value, releaseID string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "| 版本 / revision |") || strings.HasPrefix(line, "| Version / revision |") {
			lines[i] = strings.Replace(line, "`"+releaseID+"`", "`<release>`", 1)
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeIdentityRelease(value, releaseID string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "> 状态：当前实现；对应 `") || strings.HasPrefix(line, "> Status: current implementation; based on `") {
			lines[i] = strings.Replace(line, "`"+releaseID+"`", "`<release>`", 1)
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeLegacyReleaseLabels(value, releaseID string, facts, identity, topology bool) string {
	lines := strings.Split(value, "\n")
	inTopology := false
	for i, line := range lines {
		if topology && (line == "## Compose 拓扑" || line == "## Compose topology") {
			inTopology = true
			continue
		}
		if inTopology && strings.HasPrefix(line, "## ") {
			inTopology = false
		}
		if facts && (strings.HasPrefix(line, "| 版本 / revision |") || strings.HasPrefix(line, "| Version / revision |")) {
			lines[i] = normalizeFactsRelease(line, releaseID)
		} else if identity && (strings.HasPrefix(line, "> 状态：当前实现；对应 `") || strings.HasPrefix(line, "> Status: current implementation; based on `")) {
			lines[i] = normalizeIdentityRelease(line, releaseID)
		} else if inTopology {
			lines[i] = normalizeTopologyRelease(line, releaseID)
		}
	}
	return strings.Join(lines, "\n")
}

func normalizeTopologyRelease(value, releaseID string) string {
	const ownedPrefix = "`${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-"
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.Contains(line, ownedPrefix) && !strings.Contains(line, "/anas-mirror-") {
			lines[i] = strings.ReplaceAll(line, ":"+releaseID+"`", ":<release>`")
		}
	}
	return strings.Join(lines, "\n")
}

func renderModule(docsRoot string, build moduleBuild, resolves siteLinkResolver) error {
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
		if err := writePage(filepath.Join(base, "index.md"), renderUserPage(build.Manifest, current, "master", readme, versions, english, false, resolves)); err != nil {
			return err
		}
		if err := writePage(filepath.Join(base, "technical.md"), renderTechnicalPage(build.Manifest, current, "master", technical, versions, english, false, resolves)); err != nil {
			return err
		}
		for _, page := range build.Pages {
			releaseBase := filepath.Join(base, page.Release.ID())
			pageReadme := page.Release.Docs.ReadmeZH
			pageTechnical := page.Release.Docs.TechnicalZH
			if english {
				pageReadme, pageTechnical = page.Release.Docs.ReadmeEN, page.Release.Docs.TechnicalEN
			}
			if err := writePage(filepath.Join(releaseBase, "index.md"), renderUserPage(build.Manifest, page.Release.ID(), page.Release.Commit, pageReadme, versions, english, true, resolves)); err != nil {
				return err
			}
			if err := writePage(filepath.Join(releaseBase, "technical.md"), renderTechnicalPage(build.Manifest, page.Release.ID(), page.Release.Commit, pageTechnical, versions, english, true, resolves)); err != nil {
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

func renderUserPage(m manifest, release, ref, source string, versions []string, english, fixed bool, resolves siteLinkResolver) []byte {
	body := rewriteSiteLinks(rewriteUserLinks(source, m.Name, ref), ref, resolves)
	return renderPage(m, release, ref, body, versions, english, fixed, false)
}

func renderTechnicalPage(m manifest, release, ref, source string, versions []string, english, fixed bool, resolves siteLinkResolver) []byte {
	body := rewriteSiteLinks(rewriteTechnicalLinks(source, m.Name, ref, english), ref, resolves)
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

func rewriteUserLinks(source, module, ref string) string {
	source = strings.ReplaceAll(source, "](docs/technical.md)", "](./technical)")
	source = strings.ReplaceAll(source, "](docs/technical.en.md)", "](./technical)")
	source = rewriteRepositoryLinks(source, module, ref, "../../dev-docs/")
	return strings.ReplaceAll(source, "](../../docs/", "](/")
}

// siteLinkResolver reports whether a site-absolute Module link still addresses
// a page of the documentation tree currently being built.
type siteLinkResolver func(target string) bool

// alwaysResolves keeps the dead-link check strict for builds whose Module and
// core documentation come from the same tree.
func alwaysResolves(string) bool { return true }

// documentationPageResolver answers against the disposable --docs-root, which is
// the exact tree VitePress renders and therefore the only authority on whether a
// link resolves.
func documentationPageResolver(docsRoot string) siteLinkResolver {
	return func(target string) bool {
		page, _ := splitSiteLink(target)
		for _, candidate := range sitePageCandidates(page) {
			info, err := os.Stat(filepath.Join(docsRoot, filepath.FromSlash(candidate)))
			if err == nil && !info.IsDir() {
				return true
			}
		}
		return false
	}
}

// rewriteSiteLinks redirects site-absolute links the built site cannot serve to
// the repository copy, mirroring how rewriteTechnicalLinks addresses files that
// are not published as pages at all.
func rewriteSiteLinks(source, ref string, resolves siteLinkResolver) string {
	return siteLinkPattern.ReplaceAllStringFunc(source, func(match string) string {
		target := siteLinkPattern.FindStringSubmatch(match)[1]
		page, _ := splitSiteLink(target)
		// Pages this command generates are written to --docs-root as the build
		// progresses, so their absence here proves nothing; assets are never
		// dead-link checked and must keep their site paths.
		if generatedModuleRoute(page) || !sitePage(page) || resolves(target) {
			return match
		}
		return "](" + repositoryDocumentURL(target, ref) + ")"
	})
}

// splitSiteLink separates the page path from the anchor or query that VitePress
// ignores when it resolves a link.
func splitSiteLink(target string) (string, string) {
	if index := strings.IndexAny(target, "#?"); index >= 0 {
		return target[:index], target[index:]
	}
	return target, ""
}

// sitePageCandidates lists the source files VitePress would accept for a link,
// which may omit the extension or name a directory index.
func sitePageCandidates(page string) []string {
	route := strings.TrimPrefix(page, "/")
	switch {
	case route == "" || strings.HasSuffix(route, "/"):
		return []string{route + "index.md"}
	case strings.HasSuffix(route, ".md"):
		return []string{route}
	case strings.HasSuffix(route, ".html"):
		return []string{strings.TrimSuffix(route, ".html") + ".md"}
	}
	return []string{route + ".md", route + "/index.md"}
}

// sitePage excludes public assets, which are served verbatim and would be
// destroyed by a repository fallback.
func sitePage(page string) bool {
	base := page
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	dot := strings.LastIndex(base, ".")
	if dot < 0 {
		return true
	}
	return base[dot:] == ".md" || base[dot:] == ".html"
}

// generatedModuleRoute reports whether a link addresses a page this command
// generates rather than a page of the core documentation tree.
func generatedModuleRoute(page string) bool {
	route := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(page, "/"), "en/"), ".md")
	return route == "reference/modules" || strings.HasPrefix(route, "reference/modules/")
}

// repositoryDocumentURL addresses the core document behind a site path, so the
// reader still reaches the content the Module documentation cited.
func repositoryDocumentURL(target, ref string) string {
	page, suffix := splitSiteLink(target)
	document := strings.TrimPrefix(page, "/")
	switch {
	case document == "" || strings.HasSuffix(document, "/"):
		document += "index.md"
	case strings.HasSuffix(document, ".html"):
		document = strings.TrimSuffix(document, ".html") + ".md"
	case !strings.HasSuffix(document, ".md"):
		document += ".md"
	}
	return repositoryBlobBase + ref + "/docs/" + document + suffix
}

func rewriteTechnicalLinks(source, module, ref string, english bool) string {
	source = strings.ReplaceAll(source, "](../README.md)", "](./)")
	source = strings.ReplaceAll(source, "](../README.en.md)", "](./)")
	// Technical documentation sits one directory deeper than a README, so core
	// documentation is three levels up. Map it into the site exactly as
	// rewriteUserLinks does: a page the site publishes must not send readers to
	// the repository, and rewriteSiteLinks still redirects the ones it cannot
	// serve.
	source = strings.ReplaceAll(source, "](../../../docs/", "](/")
	re := regexp.MustCompile(`\]\(\.\./([^)]+)\)`)
	return re.ReplaceAllStringFunc(source, func(match string) string {
		parts := re.FindStringSubmatch(match)
		return "](" + repositoryBlobURL(ref, path.Join("modules", module, parts[1])) + ")"
	})
}

// Development artefacts under dev-docs/ are repository-only: they are not part
// of the documentation site, so a relative link into them would render as a dead
// link on the published page. Point those at the repository instead, where the
// file actually exists.
func rewriteRepositoryLinks(source, module, ref, prefix string) string {
	re := regexp.MustCompile(`\]\(` + regexp.QuoteMeta(prefix) + `([^)]+)\)`)
	return re.ReplaceAllStringFunc(source, func(match string) string {
		parts := re.FindStringSubmatch(match)
		return "](" + repositoryBlobURL(ref, path.Join("modules", module, prefix+parts[1])) + ")"
	})
}

func repositoryBlobURL(ref, repositoryPath string) string {
	return repositoryBlobBase + ref + "/" + path.Clean(repositoryPath)
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
	entries := make([]moduledocs.CatalogEntry, 0, len(builds))
	for _, build := range builds {
		entries = append(entries, moduledocs.CatalogEntry{
			Name:        build.Manifest.Name,
			Title:       build.Manifest.Title,
			Version:     build.Manifest.Version,
			Revision:    build.Manifest.Revision,
			Status:      build.Manifest.Status,
			Category:    build.Manifest.Category,
			Description: build.Manifest.Description,
		})
	}
	for _, english := range []bool{false, true} {
		path := filepath.Join(docsRoot, "reference", "modules.md")
		if english {
			path = filepath.Join(docsRoot, "en", "reference", "modules.md")
		}
		if err := writePage(path, moduledocs.RenderCatalog(entries, english)); err != nil {
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
