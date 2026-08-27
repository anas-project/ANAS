package testcasecatalog

// TEST_CASES: TESTAUTO-T-001, TESTAUTO-T-002, TESTAUTO-T-003, TESTAUTO-T-004

import (
	"bytes"
	"os"
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
	path := filepath.Join(root, "docs", "requirements", "demo.md")
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
	if !strings.Contains(output.String(), "DEMO-T-001 sha256:") {
		t.Fatalf("missing printed digest: %s", output.String())
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
		"docs/requirements",
		"docs/plans",
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
	write("docs/requirements/demo.md", fixtureRequirements)
	write("docs/plans/demo.md", "# Demo plan\n")
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
	catalog := `api_version: anas.test-cases/v1
topic: demo
title: Demo cases
requirement_document: docs/requirements/demo.md
plan_document: docs/plans/demo.md
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
    fixture: repository
    capabilities: [go]
    preconditions: []
    steps: [run the implementation]
    implementation:
      files: [impl/catalog_test.go]
      commands: [go test ./impl]
    assertions: [observable result is returned]
    negative_cases: []
    cleanup: [temporary directory is removed]
    timeout: 30s
    sensitive_data: none
  - id: DEMO-T-002
    title: Remote rejection
    status: active
    level: ` + secondLevel + `
    requirements: [DEMO-R-002]
    requirement_digest: ` + digestTwo + `
    fixture: isolated server
    capabilities: [docker]
    preconditions: [server is isolated]
    steps: [submit invalid input]
    implementation:
      files: [impl/catalog_test.go]
      commands: [go test ./impl]
    assertions: [invalid input is rejected]
    negative_cases: [supply invalid input]
    cleanup: [temporary directory is removed]
    timeout: 1m
    sensitive_data: synthetic only
`
	write("test-env/cases/demo/cases.yml", catalog)
	return root
}
