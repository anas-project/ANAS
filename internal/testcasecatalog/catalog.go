// Package testcasecatalog validates traceability from requirement matrices to
// executable test cases and renders the human-readable test case catalog.
package testcasecatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	catalogAPI = "anas.test-cases/v2"
	readmeName = "README.md"
)

var (
	topicPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	requirementPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)*)-R-(\d{3})$`)
	casePattern        = regexp.MustCompile(`^([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)*)-T-(\d{3})$`)
	markerPattern      = regexp.MustCompile(`^\s*(?://|#)\s*TEST_CASES:\s*(.*)$`)
	markedCasePattern  = regexp.MustCompile(`[A-Z][A-Z0-9]*(?:-[A-Z0-9]+)*-T-\d{3}`)
)

// Options controls catalog generation and validation.
type Options struct {
	Root         string
	Check        bool
	PrintDigests bool
	ReviewBase   string
	Output       io.Writer
}

type Catalog struct {
	APIVersion          string     `yaml:"api_version"`
	Topic               string     `yaml:"topic"`
	Title               string     `yaml:"title"`
	RequirementDocument string     `yaml:"requirement_document"`
	PlanDocument        string     `yaml:"plan_document"`
	RequirementScope    []string   `yaml:"requirement_scope"`
	Cases               []TestCase `yaml:"cases"`

	dir          string
	manifestPath string
	requirements requirementMatrix
}

type TestCase struct {
	ID                   string         `yaml:"id"`
	Title                string         `yaml:"title"`
	Status               string         `yaml:"status"`
	ReplacedBy           []string       `yaml:"replaced_by,omitempty"`
	Level                string         `yaml:"level,omitempty"`
	Requirements         []string       `yaml:"requirements,omitempty"`
	RequirementDigest    string         `yaml:"requirement_digest,omitempty"`
	ImplementationDigest string         `yaml:"implementation_digest,omitempty"`
	Fixture              string         `yaml:"fixture,omitempty"`
	Capabilities         []string       `yaml:"capabilities,omitempty"`
	Preconditions        []string       `yaml:"preconditions,omitempty"`
	Steps                []string       `yaml:"steps,omitempty"`
	Implementation       Implementation `yaml:"implementation,omitempty"`
	Assertions           []string       `yaml:"assertions,omitempty"`
	Oracle               Oracle         `yaml:"oracle,omitempty"`
	NegativeCases        []string       `yaml:"negative_cases,omitempty"`
	Validity             Validity       `yaml:"validity,omitempty"`
	Cleanup              []string       `yaml:"cleanup,omitempty"`
	Timeout              string         `yaml:"timeout,omitempty"`
	SensitiveData        string         `yaml:"sensitive_data,omitempty"`
}

type Implementation struct {
	Files    []string `yaml:"files"`
	Commands []string `yaml:"commands"`
}

type Oracle struct {
	Sources []string `yaml:"sources"`
}

type Validity struct {
	Method             string   `yaml:"method"`
	Commands           []string `yaml:"commands,omitempty"`
	Evidence           string   `yaml:"evidence,omitempty"`
	ManualReviewReason string   `yaml:"manual_review_reason,omitempty"`
}

type requirement struct {
	ID           string
	Prefix       string
	Number       string
	Text         string
	Verification string
	Retired      bool
}

type requirementMatrix struct {
	Prefixes     []string
	Requirements []requirement
}

type packageManifest struct {
	Scripts map[string]string `json:"scripts"`
}

// Run validates every catalog below test-env/cases and either writes or checks
// its generated README.
func Run(options Options) error {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return err
	}
	if options.Output == nil {
		options.Output = io.Discard
	}

	catalogs, err := loadCatalogs(root)
	if err != nil {
		return err
	}
	if len(catalogs) == 0 {
		return errors.New("test-env/cases contains no cases.yml catalogs")
	}

	allCases := make(map[string]*TestCase)
	caseCatalog := make(map[string]*Catalog)
	var validationErrors []string
	skipDigests := options.PrintDigests || options.ReviewBase != ""
	for i := range catalogs {
		catalog := &catalogs[i]
		validationErrors = append(validationErrors, validateCatalog(root, catalog, allCases, caseCatalog, skipDigests)...)
	}
	validationErrors = append(validationErrors, validateReplacements(allCases)...)
	validationErrors = append(validationErrors, validateImplementationMarkers(root, allCases, caseCatalog)...)

	if options.PrintDigests {
		for i := range catalogs {
			catalog := &catalogs[i]
			byID := requirementsByID(catalog.requirements)
			for _, testCase := range catalog.Cases {
				if testCase.Status != "active" {
					continue
				}
				digest, digestErr := digestRequirements(byID, testCase.Requirements)
				if digestErr != nil {
					validationErrors = append(validationErrors, fmt.Sprintf("%s: %v", testCase.ID, digestErr))
					continue
				}
				implementationDigest, digestErr := digestImplementation(root, &testCase)
				if digestErr != nil {
					validationErrors = append(validationErrors, fmt.Sprintf("%s: %v", testCase.ID, digestErr))
					continue
				}
				fmt.Fprintf(options.Output, "%s requirement_digest=%s implementation_digest=%s\n", testCase.ID, digest, implementationDigest)
			}
		}
	}

	if len(validationErrors) > 0 {
		sort.Strings(validationErrors)
		return fmt.Errorf("test case catalog validation failed:\n  - %s", strings.Join(validationErrors, "\n  - "))
	}
	if options.PrintDigests {
		return nil
	}
	if options.ReviewBase != "" {
		return renderReviewDiff(root, catalogs, options.ReviewBase, options.Output)
	}

	var stale []string
	for i := range catalogs {
		catalog := &catalogs[i]
		path := filepath.Join(catalog.dir, readmeName)
		want := renderCatalog(*catalog)
		got, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if bytes.Equal(got, want) {
			continue
		}
		if options.Check {
			stale = append(stale, relativePath(root, path))
			continue
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(options.Output, "generated %s\n", relativePath(root, path))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return fmt.Errorf("generated test case documentation is stale:\n  %s\nrun: go run ./cmd/gen-test-case-docs", strings.Join(stale, "\n  "))
	}

	active := 0
	for _, testCase := range allCases {
		if testCase.Status == "active" {
			active++
		}
	}
	fmt.Fprintf(options.Output, "test case catalogs valid: %d topics, %d active cases\n", len(catalogs), active)
	return nil
}

func loadCatalogs(root string) ([]Catalog, error) {
	casesRoot := filepath.Join(root, "test-env", "cases")
	entries, err := os.ReadDir(casesRoot)
	if err != nil {
		return nil, err
	}
	var catalogs []Catalog
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(casesRoot, entry.Name(), "cases.yml")
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, err
		}
		var catalog Catalog
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&catalog); err != nil {
			return nil, fmt.Errorf("%s: %w", relativePath(root, manifestPath), err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err == nil {
				return nil, fmt.Errorf("%s: multiple YAML documents are not allowed", relativePath(root, manifestPath))
			}
			return nil, fmt.Errorf("%s: %w", relativePath(root, manifestPath), err)
		}
		catalog.dir = filepath.Dir(manifestPath)
		catalog.manifestPath = manifestPath
		catalogs = append(catalogs, catalog)
	}
	sort.Slice(catalogs, func(i, j int) bool { return catalogs[i].Topic < catalogs[j].Topic })
	return catalogs, nil
}

func validateCatalog(root string, catalog *Catalog, allCases map[string]*TestCase, caseCatalog map[string]*Catalog, skipDigests bool) []string {
	var errs []string
	path := relativePath(root, catalog.manifestPath)
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf("%s: %s", path, fmt.Sprintf(format, args...)))
	}

	if catalog.APIVersion != catalogAPI {
		add("api_version must be %q", catalogAPI)
	}
	if !topicPattern.MatchString(catalog.Topic) {
		add("topic %q is invalid", catalog.Topic)
	}
	if filepath.Base(catalog.dir) != catalog.Topic {
		add("topic %q must match directory %q", catalog.Topic, filepath.Base(catalog.dir))
	}
	if strings.TrimSpace(catalog.Title) == "" {
		add("title is required")
	}
	wantRequirement := filepath.ToSlash(filepath.Join("dev-docs", "requirements", catalog.Topic+".md"))
	wantPlan := filepath.ToSlash(filepath.Join("dev-docs", "plans", catalog.Topic+".md"))
	wantArchivedPlan := filepath.ToSlash(filepath.Join("dev-docs", "plans", "archived", catalog.Topic+".md"))
	if catalog.RequirementDocument != wantRequirement {
		add("requirement_document must be %q", wantRequirement)
	}
	if catalog.PlanDocument != wantPlan && catalog.PlanDocument != wantArchivedPlan {
		add("plan_document must be %q or %q", wantPlan, wantArchivedPlan)
	}
	requirementPath, requirementErr := safeRepoPath(root, catalog.RequirementDocument)
	if requirementErr != nil {
		add("requirement_document: %v", requirementErr)
		return errs
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(catalog.PlanDocument))); err != nil {
		add("plan_document does not exist: %v", err)
	}
	requirementMarkdown, err := os.ReadFile(requirementPath)
	if err != nil {
		add("read requirement_document: %v", err)
		return errs
	}
	catalog.requirements = parseRequirementMatrix(string(requirementMarkdown))
	if len(catalog.requirements.Requirements) == 0 {
		add("requirement_document contains no requirement matrix")
		return errs
	}
	if len(catalog.requirements.Prefixes) != 1 {
		add("requirement matrix must use exactly one prefix, got %v", catalog.requirements.Prefixes)
		return errs
	}

	byRequirement := requirementsByID(catalog.requirements)
	if len(byRequirement) != len(catalog.requirements.Requirements) {
		add("requirement matrix contains duplicate IDs")
	}
	scope := make(map[string]bool)
	for _, id := range catalog.RequirementScope {
		if scope[id] {
			add("requirement_scope repeats %s", id)
			continue
		}
		scope[id] = true
		requirement, ok := byRequirement[id]
		if !ok {
			add("requirement_scope references unknown %s", id)
		} else if requirement.Retired {
			add("requirement_scope references retired %s", id)
		}
	}
	if len(scope) == 0 {
		add("requirement_scope must not be empty")
	}

	covered := make(map[string][]string)
	coveredByE2E := make(map[string]bool)
	coveredByNegativePath := make(map[string]bool)
	for i := range catalog.Cases {
		testCase := &catalog.Cases[i]
		if _, exists := allCases[testCase.ID]; exists {
			add("case ID %s is duplicated across catalogs", testCase.ID)
		} else {
			allCases[testCase.ID] = testCase
			caseCatalog[testCase.ID] = catalog
		}
		match := casePattern.FindStringSubmatch(testCase.ID)
		if match == nil || match[1] != catalog.requirements.Prefixes[0] {
			add("case ID %q must use %s-T-###", testCase.ID, catalog.requirements.Prefixes[0])
		}
		if strings.TrimSpace(testCase.Title) == "" {
			add("%s title is required", testCase.ID)
		}
		switch testCase.Status {
		case "active":
			errs = append(errs, validateActiveCase(root, path, catalog, testCase, byRequirement, scope, skipDigests)...)
			for _, id := range testCase.Requirements {
				covered[id] = append(covered[id], testCase.ID)
				if testCase.Level == "e2e" {
					coveredByE2E[id] = true
				}
				if len(testCase.NegativeCases) > 0 {
					coveredByNegativePath[id] = true
				}
			}
		case "retired":
			if len(testCase.ReplacedBy) == 0 {
				add("retired case %s must declare replaced_by", testCase.ID)
			}
		default:
			add("%s status must be active or retired", testCase.ID)
		}
	}

	for id := range scope {
		requirement, ok := byRequirement[id]
		if !ok || requirement.Retired || !isAutomaticallyVerified(requirement.Verification) {
			continue
		}
		if len(covered[id]) == 0 {
			add("automatically verified requirement %s has no active test case", id)
		}
		if strings.Contains(requirement.Verification, "e2e") && !coveredByE2E[id] {
			add("e2e requirement %s has no e2e-level test case", id)
		}
		if requiresNegativePath(requirement.Text) && !coveredByNegativePath[id] {
			add("risk-sensitive requirement %s has no negative or fault-injection case", id)
		}
	}
	return errs
}

func validateActiveCase(root, manifestPath string, catalog *Catalog, testCase *TestCase, byRequirement map[string]requirement, scope map[string]bool, skipDigests bool) []string {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf("%s: %s: %s", manifestPath, testCase.ID, fmt.Sprintf(format, args...)))
	}
	levels := map[string]bool{"unit": true, "contract": true, "e2e": true, "ci": true, "review": true}
	if !levels[testCase.Level] {
		add("level must be unit, contract, e2e, ci, or review")
	}
	if len(testCase.Requirements) == 0 {
		add("requirements must not be empty")
	}
	seen := make(map[string]bool)
	for _, id := range testCase.Requirements {
		if seen[id] {
			add("requirements repeats %s", id)
			continue
		}
		seen[id] = true
		requirement, ok := byRequirement[id]
		if !ok {
			add("references unknown requirement %s", id)
			continue
		}
		if requirement.Retired {
			add("references retired requirement %s", id)
		}
		if !scope[id] {
			add("requirement %s is outside requirement_scope", id)
		}
	}
	if !skipDigests {
		expected, err := digestRequirements(byRequirement, testCase.Requirements)
		if err == nil && testCase.RequirementDigest != expected {
			add("requirement_digest is stale: got %q, want %q; review the requirement change before updating it", testCase.RequirementDigest, expected)
		}
	}
	if strings.TrimSpace(testCase.Fixture) == "" {
		add("fixture is required")
	}
	if len(testCase.Capabilities) == 0 {
		add("capabilities must not be empty")
	}
	if testCase.Preconditions == nil {
		add("preconditions must be declared, use [] when none")
	}
	if len(testCase.Steps) == 0 {
		add("steps must not be empty")
	}
	if len(testCase.Implementation.Files) == 0 {
		add("implementation.files must not be empty")
	}
	for _, name := range testCase.Implementation.Files {
		full, err := safeRepoPath(root, name)
		if err != nil {
			add("implementation file %q: %v", name, err)
			continue
		}
		info, err := os.Stat(full)
		if err != nil {
			add("implementation file %q does not exist", name)
		} else if !info.Mode().IsRegular() {
			add("implementation file %q is not a regular file", name)
		}
	}
	if !skipDigests {
		expected, err := digestImplementation(root, testCase)
		if err == nil && testCase.ImplementationDigest != expected {
			add("implementation_digest is stale: got %q, want %q; review the implementation diff before updating it", testCase.ImplementationDigest, expected)
		}
	}
	if len(testCase.Implementation.Commands) == 0 {
		add("implementation.commands must not be empty")
	}
	for _, command := range testCase.Implementation.Commands {
		if err := validateCommand(root, command); err != nil {
			add("command %q: %v", command, err)
		}
	}
	if len(testCase.Assertions) == 0 {
		add("assertions must not be empty")
	}
	errs = append(errs, validateOracle(manifestPath, testCase)...)
	if testCase.NegativeCases == nil {
		add("negative_cases must be declared, use [] when none")
	}
	errs = append(errs, validateValidity(root, manifestPath, testCase)...)
	if len(testCase.Cleanup) == 0 {
		add("cleanup must not be empty")
	}
	if _, err := time.ParseDuration(testCase.Timeout); err != nil {
		add("timeout %q is invalid: %v", testCase.Timeout, err)
	}
	if strings.TrimSpace(testCase.SensitiveData) == "" {
		add("sensitive_data is required")
	}
	return errs
}

func validateOracle(manifestPath string, testCase *TestCase) []string {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf("%s: %s: %s", manifestPath, testCase.ID, fmt.Sprintf(format, args...)))
	}
	strongSources := map[string]bool{
		"api": true, "database": true, "error-contract": true, "filesystem": true,
		"network": true, "report": true, "return-value": true, "runtime": true, "ui": true,
	}
	weakSources := map[string]bool{"exit-status": true, "generation": true, "logs": true}
	seen := make(map[string]bool)
	hasStrongSource := false
	if len(testCase.Oracle.Sources) == 0 {
		add("oracle.sources must not be empty")
	}
	for _, source := range testCase.Oracle.Sources {
		if seen[source] {
			add("oracle.sources repeats %q", source)
			continue
		}
		seen[source] = true
		if strongSources[source] {
			hasStrongSource = true
		} else if !weakSources[source] {
			add("oracle source %q is unsupported", source)
		}
	}
	if len(testCase.Oracle.Sources) > 0 && !hasStrongSource {
		add("oracle must include an externally observable source; exit-status, logs, or generation alone cannot prove behavior")
	}
	return errs
}

func validateValidity(root, manifestPath string, testCase *TestCase) []string {
	var errs []string
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf("%s: %s: %s", manifestPath, testCase.ID, fmt.Sprintf(format, args...)))
	}
	switch testCase.Validity.Method {
	case "mutation", "counterexample", "fault-injection":
		if len(testCase.Validity.Commands) == 0 {
			add("validity.commands must not be empty for %s evidence", testCase.Validity.Method)
		}
		for _, command := range testCase.Validity.Commands {
			if err := validateCommand(root, command); err != nil {
				add("validity command %q: %v", command, err)
			}
		}
		if strings.TrimSpace(testCase.Validity.Evidence) == "" {
			add("validity.evidence is required for %s evidence", testCase.Validity.Method)
		}
		if strings.TrimSpace(testCase.Validity.ManualReviewReason) != "" {
			add("validity.manual_review_reason is only allowed when method is manual")
		}
	case "manual":
		if len(testCase.Validity.Commands) > 0 {
			add("validity.commands must be empty when method is manual")
		}
		if strings.TrimSpace(testCase.Validity.Evidence) != "" {
			add("validity.evidence must be empty when method is manual")
		}
		if strings.TrimSpace(testCase.Validity.ManualReviewReason) == "" {
			add("validity.manual_review_reason is required when method is manual")
		}
	default:
		add("validity.method must be mutation, counterexample, fault-injection, or manual")
	}
	return errs
}

func requiresNegativePath(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"拒绝", "安全", "回滚", "故障", "降级", "恢复",
		"reject", "denial", "security", "rollback", "failure", "degrad", "recover",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func validateReplacements(allCases map[string]*TestCase) []string {
	var errs []string
	for id, testCase := range allCases {
		if testCase.Status != "retired" {
			continue
		}
		for _, replacement := range testCase.ReplacedBy {
			if replacement == id {
				errs = append(errs, fmt.Sprintf("%s cannot replace itself", id))
				continue
			}
			next, ok := allCases[replacement]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s replacement %s does not exist", id, replacement))
			} else if next.Status != "active" {
				errs = append(errs, fmt.Sprintf("%s replacement %s is not active", id, replacement))
			}
		}
	}
	return errs
}

func validateImplementationMarkers(root string, allCases map[string]*TestCase, caseCatalog map[string]*Catalog) []string {
	markers, err := scanImplementationMarkers(root)
	if err != nil {
		return []string{err.Error()}
	}
	var errs []string
	for id, testCase := range allCases {
		if testCase.Status != "active" {
			continue
		}
		for _, name := range testCase.Implementation.Files {
			if !markers[name][id] {
				errs = append(errs, fmt.Sprintf("%s: implementation file %s must declare TEST_CASES: %s", id, name, id))
			}
		}
	}
	for name, ids := range markers {
		for id := range ids {
			testCase, ok := allCases[id]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s declares unknown TEST_CASES ID %s", name, id))
				continue
			}
			if testCase.Status != "active" {
				errs = append(errs, fmt.Sprintf("%s declares retired TEST_CASES ID %s", name, id))
				continue
			}
			if !contains(testCase.Implementation.Files, name) {
				catalog := caseCatalog[id]
				errs = append(errs, fmt.Sprintf("%s declares %s but %s does not list the file", name, id, relativePath(root, catalog.manifestPath)))
			}
		}
	}
	return errs
}

func scanImplementationMarkers(root string) (map[string]map[string]bool, error) {
	markers := make(map[string]map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			// .anas-test holds rendered deployments produced by the local test
			// suite, and a rendered deployment contains a copy of every module's
			// source. .claude holds agent worktrees, which are whole checkouts of
			// this repository. Either way the walk finds a second copy of the tree
			// and reports every marker again under a path no catalog will ever
			// list, so a developer who has run the tests -- or has a worktree open
			// -- sees the gate fail on files they did not write.
			if name == ".git" || name == "node_modules" || name == "reports" || name == ".vitepress" || name == ".anas-test" || name == ".claude" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".mjs" && ext != ".js" && ext != ".ts" && ext != ".py" && ext != ".sh" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel := relativePath(root, path)
		for _, line := range strings.Split(string(data), "\n") {
			match := markerPattern.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			for _, id := range markedCasePattern.FindAllString(match[1], -1) {
				if markers[rel] == nil {
					markers[rel] = make(map[string]bool)
				}
				markers[rel][id] = true
			}
		}
		return nil
	})
	return markers, err
}

func validateCommand(root, command string) error {
	if strings.TrimSpace(command) != command || command == "" || strings.ContainsAny(command, "\r\n") {
		return errors.New("must be one non-empty line without surrounding whitespace")
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return errors.New("is empty")
	}
	switch fields[0] {
	case "npm":
		if len(fields) < 3 || fields[1] != "run" {
			return errors.New("only npm run <script> is discoverable")
		}
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		if err != nil {
			return err
		}
		var manifest packageManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		if _, ok := manifest.Scripts[fields[2]]; !ok {
			return fmt.Errorf("package.json has no script %q", fields[2])
		}
		return nil
	case "go":
		if len(fields) < 3 || fields[1] != "test" {
			return errors.New("only go test <package> is discoverable")
		}
		for _, field := range fields[2:] {
			if !strings.HasPrefix(field, "./") {
				continue
			}
			path := strings.TrimSuffix(field, "/...")
			if path == "." {
				return nil
			}
			full, err := safeRepoPath(root, path)
			if err != nil {
				return err
			}
			if info, err := os.Stat(full); err != nil || !info.IsDir() {
				return fmt.Errorf("package path %q does not exist", field)
			}
			return nil
		}
		return errors.New("go test command has no repository package path")
	case "bash", "sh", "python3", "node":
		if len(fields) < 2 {
			return fmt.Errorf("%s command has no script path", fields[0])
		}
		_, err := requireRegularRepoFile(root, fields[1])
		return err
	default:
		if strings.HasPrefix(fields[0], "./") {
			full, err := requireRegularRepoFile(root, fields[0])
			if err != nil {
				return err
			}
			info, err := os.Stat(full)
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("script %q is not executable", fields[0])
			}
			return nil
		}
		return fmt.Errorf("unsupported entrypoint %q", fields[0])
	}
}

func requireRegularRepoFile(root, name string) (string, error) {
	full, err := safeRepoPath(root, name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("script %q does not exist", name)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("script %q is not a regular file", name)
	}
	return full, nil
}

func safeRepoPath(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", errors.New("must be a non-empty repository-relative slash path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("must stay inside the repository")
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("must stay inside the repository")
	}
	return full, nil
}

func parseRequirementMatrix(markdown string) requirementMatrix {
	lines := strings.Split(markdown, "\n")
	var requirements []requirement
	prefixes := make(map[string]bool)
	inTable := false
	waitingSeparator := false
	statusColumn := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			inTable = false
			waitingSeparator = false
			continue
		}
		cells := splitMarkdownRow(trimmed)
		if !inTable {
			if len(cells) >= 3 && cells[0] == "ID" {
				waitingSeparator = true
				statusColumn = -1
				for index, cell := range cells {
					if cell == "状态" || strings.EqualFold(cell, "status") {
						statusColumn = index
					}
				}
			}
			if waitingSeparator && allSeparatorCells(cells) {
				inTable = true
				waitingSeparator = false
			}
			continue
		}
		id := strings.Trim(cells[0], "` ")
		match := requirementPattern.FindStringSubmatch(id)
		if match == nil {
			continue
		}
		prefixes[match[1]] = true
		retired := strings.HasSuffix(cells[1], "（已废弃）") ||
			strings.HasSuffix(strings.ToLower(cells[1]), "(deprecated)") ||
			strings.HasSuffix(strings.ToLower(cells[1]), "(retired)")
		if statusColumn >= 0 && statusColumn < len(cells) {
			status := strings.ToLower(strings.TrimSpace(cells[statusColumn]))
			retired = retired || status == "已废弃" || status == "deprecated" || status == "retired"
		}
		requirements = append(requirements, requirement{
			ID:           id,
			Prefix:       match[1],
			Number:       match[2],
			Text:         cells[1],
			Verification: cells[2],
			Retired:      retired,
		})
	}
	var prefixList []string
	for prefix := range prefixes {
		prefixList = append(prefixList, prefix)
	}
	sort.Strings(prefixList)
	return requirementMatrix{Prefixes: prefixList, Requirements: requirements}
}

func splitMarkdownRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var current strings.Builder
	escaped := false
	for _, r := range line {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			current.WriteRune(r)
			continue
		}
		if r == '|' {
			cells = append(cells, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	cells = append(cells, strings.TrimSpace(current.String()))
	return cells
}

func allSeparatorCells(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func requirementsByID(matrix requirementMatrix) map[string]requirement {
	out := make(map[string]requirement, len(matrix.Requirements))
	for _, requirement := range matrix.Requirements {
		out[requirement.ID] = requirement
	}
	return out
}

func digestRequirements(byID map[string]requirement, ids []string) (string, error) {
	unique := make(map[string]bool)
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return "", fmt.Errorf("cannot digest unknown requirement %s", id)
		}
		unique[id] = true
	}
	ordered := make([]string, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	hash := sha256.New()
	for _, id := range ordered {
		requirement := byID[id]
		fmt.Fprintf(hash, "%s\x00%s\x00%s\n", requirement.ID, requirement.Text, requirement.Verification)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func digestImplementation(root string, testCase *TestCase) (string, error) {
	files := append([]string(nil), testCase.Implementation.Files...)
	sort.Strings(files)
	commands := append([]string(nil), testCase.Implementation.Commands...)
	sort.Strings(commands)
	hash := sha256.New()
	for _, name := range files {
		full, err := safeRepoPath(root, name)
		if err != nil {
			return "", fmt.Errorf("implementation file %q: %w", name, err)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return "", fmt.Errorf("read implementation file %q: %w", name, err)
		}
		fmt.Fprintf(hash, "file\x00%s\x00%d\x00", name, len(data))
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{'\n'})
	}
	for _, command := range commands {
		fmt.Fprintf(hash, "command\x00%s\n", command)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func isAutomaticallyVerified(verification string) bool {
	for _, marker := range []string{"单元", "契约", "e2e", "CI", "静态"} {
		if strings.Contains(verification, marker) {
			return true
		}
	}
	return false
}

func renderReviewDiff(root string, catalogs []Catalog, base string, output io.Writer) error {
	if strings.TrimSpace(base) != base || base == "" || strings.HasPrefix(base, "-") || strings.ContainsAny(base, "\r\n") {
		return errors.New("review base must be one non-empty Git revision without surrounding whitespace")
	}
	resolved, err := gitOutput(root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve review base %q: %w", base, err)
	}
	baseCommit := strings.TrimSpace(string(resolved))

	requirementPaths := make([]string, 0, len(catalogs))
	casePaths := make([]string, 0, len(catalogs))
	implementationSet := make(map[string]bool)
	for _, catalog := range catalogs {
		requirementPaths = append(requirementPaths, catalog.RequirementDocument)
		casePaths = append(casePaths, relativePath(root, catalog.manifestPath))
		for _, testCase := range catalog.Cases {
			if testCase.Status != "active" {
				continue
			}
			for _, name := range testCase.Implementation.Files {
				implementationSet[name] = true
			}
		}
	}
	implementationPaths := make([]string, 0, len(implementationSet))
	for name := range implementationSet {
		implementationPaths = append(implementationPaths, name)
	}
	sort.Strings(requirementPaths)
	sort.Strings(casePaths)
	sort.Strings(implementationPaths)

	sections := []struct {
		title string
		paths []string
	}{
		{title: "需求差异", paths: requirementPaths},
		{title: "用例差异", paths: casePaths},
		{title: "测试代码差异", paths: implementationPaths},
	}
	fmt.Fprintf(output, "# 测试变更审阅补丁\n\n基线：`%s`（`%s`）\n", base, baseCommit)
	for _, section := range sections {
		fmt.Fprintf(output, "\n## %s\n\n", section.title)
		diff, diffErr := gitDiff(root, baseCommit, section.paths)
		if diffErr != nil {
			return fmt.Errorf("render %s: %w", section.title, diffErr)
		}
		if len(diff) == 0 {
			fmt.Fprintln(output, "无差异。")
			continue
		}
		fmt.Fprintln(output, "```diff")
		_, _ = output.Write(diff)
		if diff[len(diff)-1] != '\n' {
			fmt.Fprintln(output)
		}
		fmt.Fprintln(output, "```")
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "## 审阅结论")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "- 确认需求语义、用例步骤和测试断言按上述三层差异一致演进。")
	fmt.Fprintln(output, "- 确认 oracle 读取外部可观察状态，而不是只看退出码、日志或生成动作。")
	fmt.Fprintln(output, "- 确认 mock/fixture 没有复制被测实现逻辑，validity 证据能在行为缺失或损坏时失败。")
	return nil
}

func gitDiff(root, base string, paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := []string{"diff", "--no-ext-diff", "--unified=3", base, "--"}
	args = append(args, paths...)
	trackedDiff, err := gitOutput(root, args...)
	if err != nil {
		return nil, err
	}
	otherArgs := []string{"ls-files", "--others", "--exclude-standard", "--"}
	otherArgs = append(otherArgs, paths...)
	untrackedOutput, err := gitOutput(root, otherArgs...)
	if err != nil {
		return nil, err
	}
	var diff bytes.Buffer
	diff.Write(trackedDiff)
	for _, name := range strings.Split(strings.TrimSuffix(string(untrackedOutput), "\n"), "\n") {
		if name == "" {
			continue
		}
		full, pathErr := safeRepoPath(root, name)
		if pathErr != nil {
			return nil, pathErr
		}
		data, readErr := os.ReadFile(full)
		if readErr != nil {
			return nil, readErr
		}
		content := string(data)
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		if len(data) == 0 {
			lines = nil
		}
		fmt.Fprintf(&diff, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, len(lines))
		for _, line := range lines {
			fmt.Fprintf(&diff, "+%s\n", line)
		}
	}
	return diff.Bytes(), nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func renderCatalog(catalog Catalog) []byte {
	var out strings.Builder
	fmt.Fprintln(&out, "<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->")
	fmt.Fprintln(&out)
	fmt.Fprintf(&out, "# %s\n\n", catalog.Title)
	fmt.Fprintf(&out, "> 需求来源：[`%s`](../../../%s)\n>\n", filepath.Base(catalog.RequirementDocument), catalog.RequirementDocument)
	fmt.Fprintf(&out, "> 实施计划：[`%s`](../../../%s)\n", filepath.Base(catalog.PlanDocument), catalog.PlanDocument)
	fmt.Fprintln(&out, "> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "## 覆盖总览")
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "| 用例 ID | 级别 | 需求 ID | 实现 |")
	fmt.Fprintln(&out, "| --- | --- | --- | --- |")
	for _, testCase := range catalog.Cases {
		level := testCase.Level
		if testCase.Status == "retired" {
			level = "已废弃"
		}
		files := strings.Join(testCase.Implementation.Files, "<br>")
		fmt.Fprintf(&out, "| `%s` | %s | %s | %s |\n", testCase.ID, level, codeList(testCase.Requirements), files)
	}
	for _, testCase := range catalog.Cases {
		fmt.Fprintln(&out)
		fmt.Fprintf(&out, "## `%s` %s\n\n", testCase.ID, testCase.Title)
		if testCase.Status == "retired" {
			fmt.Fprintf(&out, "- 状态：已废弃；替代用例：%s\n", codeList(testCase.ReplacedBy))
			continue
		}
		fmt.Fprintf(&out, "- 级别：`%s`\n", testCase.Level)
		fmt.Fprintf(&out, "- 覆盖需求：%s\n", codeList(testCase.Requirements))
		fmt.Fprintf(&out, "- 需求复核摘要：`%s`\n", testCase.RequirementDigest)
		fmt.Fprintf(&out, "- 实现复核摘要：`%s`\n", testCase.ImplementationDigest)
		fmt.Fprintf(&out, "- Fixture：%s\n", testCase.Fixture)
		fmt.Fprintf(&out, "- 目标能力：%s\n", codeList(testCase.Capabilities))
		fmt.Fprintf(&out, "- Oracle 来源：%s\n", codeList(testCase.Oracle.Sources))
		fmt.Fprintf(&out, "- 有效性证明：`%s`\n", testCase.Validity.Method)
		if testCase.Validity.Evidence != "" {
			fmt.Fprintf(&out, "- 有效性证据：%s\n", testCase.Validity.Evidence)
		}
		if testCase.Validity.ManualReviewReason != "" {
			fmt.Fprintf(&out, "- 人工复核理由：%s\n", testCase.Validity.ManualReviewReason)
		}
		fmt.Fprintf(&out, "- 超时：`%s`\n", testCase.Timeout)
		fmt.Fprintf(&out, "- 敏感数据：%s\n", testCase.SensitiveData)
		renderList(&out, "前置条件", testCase.Preconditions)
		renderList(&out, "执行步骤", testCase.Steps)
		renderList(&out, "可观察断言", testCase.Assertions)
		renderList(&out, "反例与故障路径", testCase.NegativeCases)
		renderList(&out, "清理", testCase.Cleanup)
		fmt.Fprintln(&out)
		fmt.Fprintln(&out, "执行入口：")
		fmt.Fprintln(&out)
		fmt.Fprintln(&out, "```bash")
		for _, command := range testCase.Implementation.Commands {
			fmt.Fprintln(&out, command)
		}
		fmt.Fprintln(&out, "```")
		if len(testCase.Validity.Commands) > 0 {
			fmt.Fprintln(&out)
			fmt.Fprintln(&out, "有效性验证入口：")
			fmt.Fprintln(&out)
			fmt.Fprintln(&out, "```bash")
			for _, command := range testCase.Validity.Commands {
				fmt.Fprintln(&out, command)
			}
			fmt.Fprintln(&out, "```")
		}
	}
	return []byte(out.String())
}

func renderList(out *strings.Builder, title string, values []string) {
	fmt.Fprintf(out, "\n%s：\n\n", title)
	if len(values) == 0 {
		fmt.Fprintln(out, "- 无。")
		return
	}
	for _, value := range values {
		fmt.Fprintf(out, "- %s\n", value)
	}
}

func codeList(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "`" + value + "`"
	}
	return strings.Join(quoted, "、")
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
