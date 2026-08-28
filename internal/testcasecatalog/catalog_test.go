package testcasecatalog

// TEST_CASES: TESTAUTO-T-001, TESTAUTO-T-002, TESTAUTO-T-003, TESTAUTO-T-004, TESTAUTO-T-005, TESTAUTO-T-006, TESTAUTO-T-007, TESTAUTO-T-008, TESTAUTO-T-009

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureRequirements = `---
doc_type: requirement
status: current
---

# Demo

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| DEMO-R-001 | 返回可观察结果 | 单元 |
| DEMO-R-002 | 在真实环境拒绝错误输入 | e2e |
| DEMO-R-003 | 架构保持简单 | 审阅 |
`

func TestRunGeneratesAndChecksCatalog(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	var output bytes.Buffer
	if err := Run(Options{Root: root, Output: &output}); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(root, "test-env", "cases", "demo", "README.md")
	content, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Generated from cases.yml") || !strings.Contains(string(content), "DEMO-T-002") || !strings.Contains(string(content), "执行步骤") {
		t.Fatalf("generated README lacks source marker or case: %s", content)
	}
	if err := Run(Options{Root: root, Check: true, Output: &output}); err != nil {
		t.Fatalf("fresh generated catalog failed --check: %v", err)
	}
	if err := os.WriteFile(readme, append(content, []byte("manual edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(Options{Root: root, Check: true, Output: &output})
	if err == nil || !strings.Contains(err.Error(), "documentation is stale") {
		t.Fatalf("expected stale README failure, got %v", err)
	}
}

func TestRunRejectsStaleRequirementDigest(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	path := filepath.Join(root, "dev-docs", "requirements", "demo.md")
	changed := strings.Replace(fixtureRequirements, "返回可观察结果", "返回新的可观察结果", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(Options{Root: root, Check: true})
	if err == nil || !strings.Contains(err.Error(), "requirement_digest is stale") || !strings.Contains(err.Error(), "review the requirement change") {
		t.Fatalf("expected stale requirement digest failure, got %v", err)
	}

	var output bytes.Buffer
	if err := Run(Options{Root: root, PrintDigests: true, Output: &output}); err != nil {
		t.Fatalf("--print-digests should permit a stale digest: %v", err)
	}
	if !strings.Contains(output.String(), "DEMO-T-001 requirement_digest=sha256:") || !strings.Contains(output.String(), "implementation_digest=sha256:") {
		t.Fatalf("missing printed digest: %s", output.String())
	}
}

func TestRunRejectsStaleImplementationDigest(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	path := filepath.Join(root, "impl", "catalog_test.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("// reviewed assertion changed\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(Options{Root: root, Check: true})
	if err == nil || !strings.Contains(err.Error(), "implementation_digest is stale") || !strings.Contains(err.Error(), "review the implementation diff") {
		t.Fatalf("expected stale implementation digest failure, got %v", err)
	}

	var output bytes.Buffer
	if err := Run(Options{Root: root, PrintDigests: true, Output: &output}); err != nil {
		t.Fatalf("--print-digests should permit a stale implementation digest: %v", err)
	}
	if !strings.Contains(output.String(), "implementation_digest=sha256:") {
		t.Fatalf("missing implementation digest: %s", output.String())
	}
}

func TestRunRejectsWeakOracleAndMissingValidityEvidence(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	path := filepath.Join(root, "test-env", "cases", "demo", "cases.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "sources: [return-value]", "sources: [exit-status, logs]", 1)
	changed = strings.Replace(changed, "commands: [go test ./impl]\n      evidence:", "commands: []\n      evidence:", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(Options{Root: root, Check: true})
	if err == nil {
		t.Fatal("expected oracle and validity validation failure")
	}
	for _, want := range []string{
		"exit-status, logs, or generation alone cannot prove behavior",
		"validity.commands must not be empty for counterexample evidence",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in error:\n%s", want, err)
		}
	}
}

func TestRunRequiresNegativeCoverageForRiskSensitiveRequirement(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	path := filepath.Join(root, "test-env", "cases", "demo", "cases.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "negative_cases: [supply invalid input]", "negative_cases: []", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(Options{Root: root, Check: true})
	if err == nil || !strings.Contains(err.Error(), "risk-sensitive requirement DEMO-R-002 has no negative or fault-injection case") {
		t.Fatalf("expected risk-sensitive coverage failure, got %v", err)
	}
}

func TestRunAllowsManualValidityOnlyWithReason(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	path := filepath.Join(root, "test-env", "cases", "demo", "cases.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(data), "method: counterexample\n      commands: [go test ./impl]\n      evidence: replacing the return behavior makes the assertion fail", "method: manual", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(Options{Root: root, Check: true})
	if err == nil || !strings.Contains(err.Error(), "validity.manual_review_reason is required") {
		t.Fatalf("expected manual validity reason failure, got %v", err)
	}
}

func TestRunRendersThreeLayerReviewDiff(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	if err := Run(Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.invalid"},
		{"add", "."},
		{"commit", "-m", "fixture"},
	} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	requirementPath := filepath.Join(root, "dev-docs", "requirements", "demo.md")
	requirementData, _ := os.ReadFile(requirementPath)
	_ = os.WriteFile(requirementPath, []byte(strings.Replace(string(requirementData), "返回可观察结果", "返回可审阅结果", 1)), 0o644)
	casePath := filepath.Join(root, "test-env", "cases", "demo", "cases.yml")
	caseData, _ := os.ReadFile(casePath)
	changedCase := strings.Replace(string(caseData), "Unit behavior", "Reviewed unit behavior", 1)
	changedCase = strings.Replace(changedCase, "files: [impl/catalog_test.go]", "files: [impl/catalog_test.go, impl/new_test.py]", 1)
	_ = os.WriteFile(casePath, []byte(changedCase), 0o644)
	implementationPath := filepath.Join(root, "impl", "catalog_test.go")
	implementationData, _ := os.ReadFile(implementationPath)
	_ = os.WriteFile(implementationPath, append(implementationData, []byte("// complete assertion\n")...), 0o644)
	_ = os.WriteFile(filepath.Join(root, "impl", "new_test.py"), []byte("# TEST_CASES: DEMO-T-001\n# untracked assertion\n"), 0o644)

	var output bytes.Buffer
	if err := Run(Options{Root: root, ReviewBase: "HEAD", Output: &output}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 需求差异", "返回可审阅结果", "## 用例差异", "Reviewed unit behavior", "## 测试代码差异", "complete assertion", "untracked assertion", "mock/fixture"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("review diff lacks %q:\n%s", want, output.String())
		}
	}
}

func TestRunRejectsCoverageLevelAndMarkerDrift(t *testing.T) {
	root := writeFixture(t, "unit", "DEMO-T-001, DEMO-T-999")
	err := Run(Options{Root: root, Check: true})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	message := err.Error()
	for _, want := range []string{
		"e2e requirement DEMO-R-002 has no e2e-level test case",
		"declares unknown TEST_CASES ID DEMO-T-999",
		"implementation file impl/catalog_test.go must declare TEST_CASES: DEMO-T-002",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected %q in error:\n%s", want, message)
		}
	}
}

// A nested checkout -- an agent worktree under .claude, or a rendered deployment
// under .anas-test -- duplicates every marker in the tree. Without the skip the
// gate fails on a second copy of files the developer never edited.
func TestRunIgnoresMarkersInNestedCheckouts(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	nested := filepath.Join(root, ".claude", "worktrees", "scratch", "impl")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(nested, "catalog_test.go")
	if err := os.WriteFile(copied, []byte("// TEST_CASES: DEMO-T-404\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Run(Options{Root: root, Output: &output}); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{Root: root, Check: true, Output: &output}); err != nil {
		t.Fatalf("markers under .claude must be ignored: %v", err)
	}
}

func TestRunUsesStrictYAMLFields(t *testing.T) {
	root := writeFixture(t, "e2e", "DEMO-T-001, DEMO-T-002")
	path := filepath.Join(root, "test-env", "cases", "demo", "cases.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("unexpected_field: true\n")...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(Options{Root: root, Check: true})
	if err == nil || !strings.Contains(err.Error(), "field unexpected_field not found") {
		t.Fatalf("expected strict YAML field failure, got %v", err)
	}
}

func TestValidateCommandDiscoversSupportedEntrypoints(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"pkg", "scripts"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"catalog":"go test ./pkg"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "case.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"npm run catalog",
		"go test ./pkg",
		"./scripts/case.sh",
		"bash scripts/case.sh --check",
	} {
		if err := validateCommand(root, command); err != nil {
			t.Errorf("%q should be discoverable: %v", command, err)
		}
	}
	for _, command := range []string{
		"npm run missing",
		"go test ./missing",
		"bash scripts/missing.sh",
		"curl https://example.invalid",
	} {
		if err := validateCommand(root, command); err == nil {
			t.Errorf("%q should be rejected", command)
		}
	}
}

func TestParseRequirementMatrixUsesDedicatedRetirementSignals(t *testing.T) {
	matrix := parseRequirementMatrix(`
| ID | 要求 | 验证方式 | 状态 |
| --- | --- | --- | --- |
| DEMO-R-001 | 正文解释“已废弃”这个词的规则 | 单元 | current |
| DEMO-R-002 | 被新要求替代 | 单元 | 已废弃 |
| DEMO-R-003 | 旧写法（已废弃） | 单元 | |
`)
	if len(matrix.Requirements) != 3 {
		t.Fatalf("got %d requirements", len(matrix.Requirements))
	}
	if matrix.Requirements[0].Retired {
		t.Fatal("mentioning retirement in requirement text must not retire the row")
	}
	if !matrix.Requirements[1].Retired || !matrix.Requirements[2].Retired {
		t.Fatal("dedicated status and legacy suffix must retire their rows")
	}
}

func writeFixture(t *testing.T, secondLevel, markerIDs string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		"dev-docs/requirements",
		"dev-docs/plans",
		"test-env/cases/demo",
		"impl",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("dev-docs/requirements/demo.md", fixtureRequirements)
	write("dev-docs/plans/demo.md", "# Demo plan\n")
	write("package.json", `{"scripts":{}}`)
	write("impl/catalog_test.go", "package impl\n\n// TEST_CASES: "+markerIDs+"\n")

	matrix := parseRequirementMatrix(fixtureRequirements)
	byID := requirementsByID(matrix)
	digestOne, err := digestRequirements(byID, []string{"DEMO-R-001"})
	if err != nil {
		t.Fatal(err)
	}
	digestTwo, err := digestRequirements(byID, []string{"DEMO-R-002"})
	if err != nil {
		t.Fatal(err)
	}
	implementationDigest, err := digestImplementation(root, &TestCase{Implementation: Implementation{
		Files: []string{"impl/catalog_test.go"}, Commands: []string{"go test ./impl"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	catalog := `api_version: anas.test-cases/v2
topic: demo
title: Demo cases
requirement_document: dev-docs/requirements/demo.md
plan_document: dev-docs/plans/demo.md
requirement_scope:
  - DEMO-R-001
  - DEMO-R-002
  - DEMO-R-003
cases:
  - id: DEMO-T-001
    title: Unit behavior
    status: active
    level: unit
    requirements: [DEMO-R-001]
    requirement_digest: ` + digestOne + `
    implementation_digest: ` + implementationDigest + `
    fixture: repository
    capabilities: [go]
    preconditions: []
    steps: [run the implementation]
    implementation:
      files: [impl/catalog_test.go]
      commands: [go test ./impl]
    assertions: [observable result is returned]
    oracle:
      sources: [return-value]
    negative_cases: []
    validity:
      method: counterexample
      commands: [go test ./impl]
      evidence: replacing the return behavior makes the assertion fail
    cleanup: [temporary directory is removed]
    timeout: 30s
    sensitive_data: none
  - id: DEMO-T-002
    title: Remote rejection
    status: active
    level: ` + secondLevel + `
    requirements: [DEMO-R-002]
    requirement_digest: ` + digestTwo + `
    implementation_digest: ` + implementationDigest + `
    fixture: isolated server
    capabilities: [docker]
    preconditions: [server is isolated]
    steps: [submit invalid input]
    implementation:
      files: [impl/catalog_test.go]
      commands: [go test ./impl]
    assertions: [invalid input is rejected]
    oracle:
      sources: [error-contract]
    negative_cases: [supply invalid input]
    validity:
      method: fault-injection
      commands: [go test ./impl]
      evidence: the fixture submits invalid input and requires rejection
    cleanup: [temporary directory is removed]
    timeout: 1m
    sensitive_data: synthetic only
`
	write("test-env/cases/demo/cases.yml", catalog)
	return root
}
