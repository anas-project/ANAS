// Package upgradetest validates the release-to-release E2E test catalog.
package upgradetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

const APIVersion = "anas.upgrade-tests/v1"

var (
	idPattern      = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	modulePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	releasePattern = regexp.MustCompile(`^.+-r[1-9][0-9]*$`)
)

type Catalog struct {
	APIVersion string             `yaml:"api_version"`
	Products   map[string]Product `yaml:"products"`
	Modules    []Module           `yaml:"modules"`
	Suites     []Suite            `yaml:"suites"`
}

type Product struct {
	Current     string       `yaml:"current"`
	Baselines   []Baseline   `yaml:"baselines,omitempty"`
	Transitions []Transition `yaml:"transitions,omitempty"`
}

type Module struct {
	Name        string       `yaml:"name"`
	Current     string       `yaml:"current"`
	Baseline    *Baseline    `yaml:"baseline,omitempty"`
	Transitions []Transition `yaml:"transitions,omitempty"`
}

type Baseline struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Suite   string `yaml:"suite"`
	Reason  string `yaml:"reason,omitempty"`
}

type Transition struct {
	ID      string `yaml:"id"`
	From    string `yaml:"from"`
	FromRef string `yaml:"from_ref,omitempty"`
	To      string `yaml:"to"`
	Suite   string `yaml:"suite"`
}

type Suite struct {
	ID      string   `yaml:"id"`
	Kind    string   `yaml:"kind"`
	Runner  string   `yaml:"runner"`
	Config  string   `yaml:"config,omitempty"`
	Targets string   `yaml:"targets,omitempty"`
	Seed    string   `yaml:"seed,omitempty"`
	Verify  string   `yaml:"verify,omitempty"`
	Report  string   `yaml:"report,omitempty"`
	Modules []string `yaml:"modules,omitempty"`
}

type Options struct {
	Root        string
	CatalogPath string
	BaseRef     string
	Scopes      map[string]bool
}

type Result struct {
	Products      int
	Modules       int
	Suites        int
	Transitions   int
	ModuleConfigs []string
}

// Validate checks both the static catalog contract and, when BaseRef is set,
// that every release changed since that ref has an exact old-to-new case.
func Validate(options Options) (Result, error) {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return Result{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Result{}, err
	}
	if options.CatalogPath == "" {
		options.CatalogPath = "test-env/upgrades/catalog.yml"
	}
	catalogPath, err := safePath(root, options.CatalogPath)
	if err != nil {
		return Result{}, err
	}
	catalog, err := loadCatalog(catalogPath)
	if err != nil {
		return Result{}, err
	}

	validator := &validator{
		root:       root,
		catalog:    catalog,
		suites:     map[string]Suite{},
		ids:        map[string]string{},
		moduleCase: map[string]Module{},
		scopes:     options.Scopes,
	}
	validator.validateSuites()
	validator.validateProducts()
	validator.validateModules()
	validator.validateSuiteModuleOwnership()
	if strings.TrimSpace(options.BaseRef) != "" {
		validator.validateBase(strings.TrimSpace(options.BaseRef))
	}
	if len(validator.errs) > 0 {
		sort.Strings(validator.errs)
		return Result{}, errors.New("upgrade test catalog is invalid:\n  " + strings.Join(validator.errs, "\n  "))
	}
	transitions := 0
	moduleConfigs := []string{}
	for _, product := range catalog.Products {
		transitions += len(product.Transitions)
	}
	for _, module := range catalog.Modules {
		transitions += len(module.Transitions)
	}
	for _, suite := range catalog.Suites {
		if suite.Kind == "module" {
			moduleConfigs = append(moduleConfigs, suite.Config)
		}
	}
	sort.Strings(moduleConfigs)
	return Result{
		Products: len(catalog.Products), Modules: len(catalog.Modules),
		Suites: len(catalog.Suites), Transitions: transitions, ModuleConfigs: moduleConfigs,
	}, nil
}

func loadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Catalog{}, fmt.Errorf("%s: multiple YAML documents are not allowed", path)
		}
		return Catalog{}, fmt.Errorf("%s: %w", path, err)
	}
	return catalog, nil
}

type validator struct {
	root       string
	catalog    Catalog
	suites     map[string]Suite
	ids        map[string]string
	moduleCase map[string]Module
	scopes     map[string]bool
	errs       []string
}

func (v *validator) add(format string, args ...any) {
	v.errs = append(v.errs, fmt.Sprintf(format, args...))
}

func (v *validator) inScope(scope string) bool {
	return len(v.scopes) == 0 || v.scopes["all"] || v.scopes[scope]
}

func (v *validator) addID(id, owner string) {
	if !idPattern.MatchString(id) {
		v.add("%s has invalid id %q", owner, id)
		return
	}
	if previous, exists := v.ids[id]; exists {
		v.add("id %q is shared by %s and %s", id, previous, owner)
		return
	}
	v.ids[id] = owner
}

func (v *validator) validateSuites() {
	if v.catalog.APIVersion != APIVersion {
		v.add("api_version must be %q", APIVersion)
	}
	for _, suite := range v.catalog.Suites {
		owner := "suite " + suite.ID
		v.addID(suite.ID, owner)
		if _, exists := v.suites[suite.ID]; exists {
			v.add("suite %q is duplicated", suite.ID)
			continue
		}
		v.suites[suite.ID] = suite
		switch suite.Kind {
		case "core", "web":
			if len(suite.Modules) != 0 || suite.Config != "" || suite.Targets != "" || suite.Seed != "" || suite.Verify != "" || suite.Report != "" {
				v.add("%s may only declare runner", owner)
			}
		case "module":
			if suite.Config == "" || suite.Targets == "" || suite.Seed == "" || suite.Verify == "" || suite.Report == "" || len(suite.Modules) == 0 {
				v.add("%s requires config, targets, seed, verify, report, and modules", owner)
			}
		default:
			v.add("%s kind must be core, web, or module", owner)
		}
		v.validateFile(owner+" runner", suite.Runner, true)
		if suite.Config != "" {
			v.validateFile(owner+" config", suite.Config, false)
			v.validateSuiteConfig(owner, suite)
		}
		if suite.Targets != "" {
			v.validateFile(owner+" targets", suite.Targets, false)
			v.validateSuiteTargets(owner, suite)
		}
		if suite.Seed != "" {
			v.validateFile(owner+" seed", suite.Seed, true)
		}
		if suite.Verify != "" {
			v.validateFile(owner+" verify", suite.Verify, true)
		}
		if suite.Report != "" {
			v.validateFile(owner+" report", suite.Report, true)
		}
		seen := map[string]bool{}
		for _, name := range suite.Modules {
			if !modulePattern.MatchString(name) || seen[name] {
				v.add("%s has invalid or repeated module %q", owner, name)
			}
			seen[name] = true
		}
	}
	if len(v.catalog.Suites) == 0 {
		v.add("at least one suite is required")
	}
}

func (v *validator) validateSuiteTargets(owner string, suite Suite) {
	wantPath := strings.TrimSuffix(suite.Config, filepath.Ext(suite.Config)) + ".targets"
	if suite.Targets != wantPath {
		v.add("%s targets must be the config companion %s", owner, wantPath)
	}
	path, err := safePath(v.root, suite.Targets)
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return // validateFile reports the filesystem error.
	}
	seen := map[string]bool{}
	actual := []string{}
	for lineNumber, raw := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		name := strings.TrimSpace(raw)
		if name == "" || name != raw || !modulePattern.MatchString(name) || seen[name] {
			v.add("%s targets has invalid or repeated module on line %d", owner, lineNumber+1)
			continue
		}
		seen[name] = true
		actual = append(actual, name)
	}
	want := append([]string(nil), suite.Modules...)
	sort.Strings(actual)
	sort.Strings(want)
	if !equalStrings(actual, want) {
		v.add("%s targets %v do not match catalog modules %v", owner, actual, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *validator) validateSuiteConfig(owner string, suite Suite) {
	path, err := safePath(v.root, suite.Config)
	if err != nil {
		return // validateFile already reports the path error.
	}
	configured, err := loadConfiguredModules(path)
	if err != nil {
		v.add("%s config: %v", owner, err)
		return
	}
	for _, name := range suite.Modules {
		if !configured[name] {
			v.add("%s declares module %s but config %s does not select it", owner, name, suite.Config)
		}
	}
	if err := validateReleasedImageConfig(path); err != nil {
		v.add("%s config %s cannot reuse immutable old release images: %v", owner, suite.Config, err)
	}
}

func (v *validator) validateFile(owner, path string, executable bool) {
	resolved, err := safePath(v.root, path)
	if err != nil {
		v.add("%s: %v", owner, err)
		return
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		v.add("%s: %v", owner, err)
		return
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		v.add("%s must be a regular non-symlink file", owner)
	}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		v.add("%s: resolve symlinks: %v", owner, err)
	} else if relative, relErr := filepath.Rel(v.root, real); relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		v.add("%s resolves outside the repository", owner)
	}
	if executable && info.Mode().Perm()&0111 == 0 {
		v.add("%s must be executable", owner)
	}
}

func (v *validator) validateProducts() {
	for _, name := range []string{"core", "web"} {
		product, exists := v.catalog.Products[name]
		if !exists {
			v.add("product %q is missing", name)
			continue
		}
		if product.Current != "worktree" {
			v.add("product %s current must be worktree", name)
		}
		for _, baseline := range product.Baselines {
			v.validateBaseline("product "+name, baseline, name, "")
		}
		for _, transition := range product.Transitions {
			v.validateTransition("product "+name, transition, name, product.Current, "")
			if transition.FromRef == "" {
				v.add("product %s transition %s requires from_ref", name, transition.ID)
			}
		}
		if len(product.Baselines) == 0 && len(product.Transitions) == 0 {
			v.add("product %s needs a baseline or transition", name)
		}
	}
	for name := range v.catalog.Products {
		if name != "core" && name != "web" {
			v.add("unknown product %q", name)
		}
	}
}

func (v *validator) validateModules() {
	registered, err := loadRegisteredModules(v.root)
	if err != nil {
		v.add("load .github/modules.json: %v", err)
		return
	}
	for _, module := range v.catalog.Modules {
		owner := "module " + module.Name
		if !modulePattern.MatchString(module.Name) {
			v.add("invalid module name %q", module.Name)
		}
		if _, duplicate := v.moduleCase[module.Name]; duplicate {
			v.add("module %q is duplicated", module.Name)
			continue
		}
		v.moduleCase[module.Name] = module
		if !registered[module.Name] {
			v.add("catalog contains unregistered module %q", module.Name)
		}
		actual, err := loadModuleRelease(filepath.Join(v.root, "modules", module.Name, "module.yml"))
		if err != nil {
			v.add("%s: %v", owner, err)
		} else if module.Current != actual {
			v.add("%s current is %q, module.yml is %q", owner, module.Current, actual)
		}
		if !releasePattern.MatchString(module.Current) {
			v.add("%s current %q is not version-rN", owner, module.Current)
		}
		if module.Baseline != nil {
			v.validateBaseline(owner, *module.Baseline, "module", module.Name)
			if module.Baseline.Version != module.Current {
				v.add("%s baseline version %q must equal current %q", owner, module.Baseline.Version, module.Current)
			}
		}
		for _, transition := range module.Transitions {
			v.validateTransition(owner, transition, "module", module.Current, module.Name)
			if transition.FromRef != "" {
				v.add("%s transition %s must not declare from_ref", owner, transition.ID)
			}
		}
		if module.Baseline == nil && len(module.Transitions) == 0 {
			v.add("%s needs a first-release baseline or an upgrade transition", owner)
		}
	}
	for name := range registered {
		if _, exists := v.moduleCase[name]; !exists {
			v.add("registered module %q has no upgrade test entry", name)
		}
	}
}

func (v *validator) validateSuiteModuleOwnership() {
	for _, suite := range v.catalog.Suites {
		if suite.Kind != "module" {
			continue
		}
		for _, name := range suite.Modules {
			module, exists := v.moduleCase[name]
			if !exists {
				v.add("suite %s declares unknown module %s", suite.ID, name)
				continue
			}
			owned := false
			for _, transition := range module.Transitions {
				if transition.Suite == suite.ID {
					owned = true
					break
				}
			}
			if !owned {
				v.add("suite %s declares module %s without a transition assigned to that suite", suite.ID, name)
			}
		}
	}
}

func (v *validator) validateBaseline(owner string, baseline Baseline, kind, module string) {
	v.addID(baseline.ID, owner+" baseline")
	if baseline.Version == "" {
		v.add("%s baseline %s requires version", owner, baseline.ID)
	}
	if module == "" {
		if baseline.Suite == "" {
			v.add("%s baseline %s requires suite", owner, baseline.ID)
		} else {
			v.validateSuiteReference(owner+" baseline "+baseline.ID, baseline.Suite, kind, module)
		}
		return
	}
	if baseline.Reason != "no_prior_release" {
		v.add("%s baseline %s reason must be no_prior_release", owner, baseline.ID)
	}
	if baseline.Suite != "" {
		v.validateSuiteReference(owner+" baseline "+baseline.ID, baseline.Suite, kind, module)
	}
}

func (v *validator) validateTransition(owner string, transition Transition, kind, current, module string) {
	v.addID(transition.ID, owner+" transition")
	if transition.From == "" || transition.To == "" || transition.Suite == "" {
		v.add("%s transition %s requires from, to, and suite", owner, transition.ID)
	}
	if transition.From == transition.To {
		v.add("%s transition %s has identical endpoints", owner, transition.ID)
	}
	if transition.To != current {
		v.add("%s transition %s ends at %q, current is %q", owner, transition.ID, transition.To, current)
	}
	if kind == "module" {
		fromVersion, fromRevision, fromErr := parseRelease(transition.From)
		toVersion, toRevision, toErr := parseRelease(transition.To)
		if fromErr != nil || toErr != nil {
			v.add("%s transition %s endpoints must both be valid version-rN releases", owner, transition.ID)
		} else if comparison := toVersion.Compare(fromVersion); comparison < 0 || (comparison == 0 && toRevision <= fromRevision) {
			v.add("%s transition %s is not a forward upgrade (%s -> %s)", owner, transition.ID, transition.From, transition.To)
		}
	}
	v.validateSuiteReference(owner+" transition "+transition.ID, transition.Suite, kind, module)
}

func (v *validator) validateSuiteReference(owner, suiteID, kind, module string) {
	suite, exists := v.suites[suiteID]
	if !exists {
		v.add("%s references unknown suite %q", owner, suiteID)
		return
	}
	if suite.Kind != kind {
		v.add("%s references %s suite %q, want %s", owner, suite.Kind, suiteID, kind)
	}
	if module != "" && !contains(suite.Modules, module) {
		v.add("%s references suite %q which does not exercise module %s", owner, suiteID, module)
	}
}

func (v *validator) validateBase(base string) {
	commit, err := v.git("rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		v.add("base ref %q is not a commit: %v", base, err)
		return
	}
	commit = strings.TrimSpace(commit)
	for _, productName := range []string{"core", "web"} {
		if !v.inScope(productName) {
			continue
		}
		product := v.catalog.Products[productName]
		baseHasProduct := productName == "core" || v.gitPathExists(base, "web/package.json") || v.gitPathExists(base, "internal/webui/dist/index.html")
		if !baseHasProduct {
			if len(product.Baselines) == 0 {
				v.add("product %s is new since %s but has no first-release baseline", productName, base)
			}
			continue
		}
		matched := false
		for _, transition := range product.Transitions {
			fromCommit, resolveErr := v.git("rev-parse", "--verify", transition.FromRef+"^{commit}")
			if resolveErr == nil && strings.TrimSpace(fromCommit) == commit && transition.To == product.Current {
				matched = true
				break
			}
		}
		if !matched {
			v.add("product %s has no transition from exact base %s (%s) to %s", productName, base, commit, product.Current)
		}
	}
	if !v.inScope("modules") {
		return
	}
	for name, module := range v.moduleCase {
		body, showErr := v.git("show", base+":modules/"+name+"/module.yml")
		if showErr != nil {
			if module.Baseline == nil || module.Baseline.Version != module.Current {
				v.add("module %s is new since %s but has no current first-release baseline", name, base)
			}
			continue
		}
		oldRelease, parseErr := decodeModuleRelease([]byte(body))
		if parseErr != nil {
			v.add("module %s at %s: %v", name, base, parseErr)
			continue
		}
		if oldRelease == module.Current {
			continue
		}
		matched := false
		for _, transition := range module.Transitions {
			if transition.From == oldRelease && transition.To == module.Current {
				matched = true
				break
			}
		}
		if !matched {
			v.add("module %s changed %s -> %s since %s without an exact upgrade transition", name, oldRelease, module.Current, base)
		}
	}
}

func (v *validator) git(args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = v.root
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (v *validator) gitPathExists(ref, path string) bool {
	_, err := v.git("cat-file", "-e", ref+":"+path)
	return err == nil
}

func loadRegisteredModules(root string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(root, ".github", "modules.json"))
	if err != nil {
		return nil, err
	}
	var entries []struct {
		Module         string   `json:"module"`
		Repository     string   `json:"repository"`
		Platforms      []string `json:"platforms"`
		SharedContexts []string `json:"shared_contexts"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return nil, err
	}
	registered := map[string]bool{}
	for _, entry := range entries {
		if registered[entry.Module] {
			return nil, fmt.Errorf("duplicate module %q", entry.Module)
		}
		registered[entry.Module] = true
	}
	return registered, nil
}

func loadModuleRelease(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return decodeModuleRelease(data)
}

func loadConfiguredModules(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document struct {
		Modules map[string]any `yaml:"modules"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not allowed")
		}
		return nil, err
	}
	if len(document.Modules) == 0 {
		return nil, errors.New("modules must be a non-empty mapping")
	}
	configured := make(map[string]bool, len(document.Modules))
	for name := range document.Modules {
		name = strings.ToLower(strings.TrimSpace(name))
		if !modulePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid configured module %q", name)
		}
		configured[name] = true
	}
	return configured, nil
}

func validateReleasedImageConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document struct {
		Global struct {
			ChineseBuildSpeedup *bool `yaml:"chinese_build_speedup"`
		} `yaml:"global"`
		Env map[string]any `yaml:"env"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	var conflicts []string
	if document.Global.ChineseBuildSpeedup != nil && *document.Global.ChineseBuildSpeedup {
		conflicts = append(conflicts, "global.chinese_build_speedup is true")
	}
	if len(document.Env) != 0 {
		conflicts = append(conflicts, "top-level env sets build inputs")
	}
	if len(conflicts) != 0 {
		return errors.New(strings.Join(conflicts, "; "))
	}
	return nil
}

func decodeModuleRelease(data []byte) (string, error) {
	var manifest struct {
		Name     string `yaml:"name"`
		Version  string `yaml:"version"`
		Revision int    `yaml:"revision"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.Name == "" || manifest.Version == "" || manifest.Revision < 1 {
		return "", errors.New("manifest requires name, version, and positive revision")
	}
	return manifest.Version + "-r" + strconv.Itoa(manifest.Revision), nil
}

func parseRelease(release string) (*semver.Version, int, error) {
	match := releasePattern.FindStringSubmatch(release)
	if match == nil {
		return nil, 0, fmt.Errorf("invalid release %q", release)
	}
	position := strings.LastIndex(release, "-r")
	if position < 1 {
		return nil, 0, fmt.Errorf("invalid release %q", release)
	}
	version, err := semver.NewVersion(release[:position])
	if err != nil {
		return nil, 0, err
	}
	revision, err := strconv.Atoi(release[position+2:])
	if err != nil || revision < 1 {
		return nil, 0, fmt.Errorf("invalid release revision %q", release)
	}
	return version, revision, nil
}

func safePath(root, path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q must be repository-relative", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the repository", path)
	}
	resolved := filepath.Join(root, clean)
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the repository", path)
	}
	return resolved, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
