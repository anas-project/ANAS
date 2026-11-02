package main

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, text string) service {
	t.Helper()
	var s service
	if err := yaml.Unmarshal([]byte(text), &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestReleaseDependencyClosureAndCycles(t *testing.T) {
	all := map[string]service{
		"app":     fixture(t, "name: app\nstatus: release\ndependencies: {requires: [gateway]}"),
		"gateway": fixture(t, "name: gateway\nstatus: release\ndependencies: {requires: [app, iam]}"),
		"iam":     fixture(t, "name: iam\nstatus: developing"),
	}
	r, err := inspect(all)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed || len(r.Findings) != 2 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if !reflect.DeepEqual(r.Findings[0].Path, []string{"app", "gateway", "iam"}) {
		t.Fatal(r.Findings)
	}
	if !reflect.DeepEqual(r.Release, []string{"app", "gateway"}) || !reflect.DeepEqual(r.Excluded, []string{"iam"}) {
		t.Fatal(r)
	}
}
func TestConditionalContractMatchesInterfaceAndVersion(t *testing.T) {
	all := map[string]service{
		"app":       fixture(t, "name: app\nstatus: release\ndependencies:\n  contracts:\n    - {name: compute, version: '>=1.0.0 <2.0.0', interfaces: [incus_vm], enabled_by: actions_enabled}"),
		"vm":        fixture(t, "name: vm\nstatus: developing\ncontracts: {provides: [{name: compute, version: 1.0.0, interface: incus_vm}]}"),
		"container": fixture(t, "name: container\nstatus: developing\ncontracts: {provides: [{name: compute, version: 1.0.0, interface: incus_container}]}"),
		"future":    fixture(t, "name: future\nstatus: developing\ncontracts: {provides: [{name: compute, version: 2.0.0, interface: incus_vm}]}"),
	}
	r, err := inspect(all)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || r.Findings[0].Dependency != "vm" || r.Findings[0].Condition != "actions_enabled" {
		t.Fatal(r)
	}
}
func TestCapabilityCandidatesDoNotGuessDeploymentSelection(t *testing.T) {
	all := map[string]service{
		"app":       fixture(t, "name: app\nstatus: release\ndependencies: {requires_capabilities: [{name: iam, interfaces: {any_of: [oidc]}}]}"),
		"stable":    fixture(t, "name: stable\nstatus: release\ncapabilities: {provides: [{name: iam, interfaces: [oidc]}]}"),
		"candidate": fixture(t, "name: candidate\nstatus: developing\ncapabilities: {provides: [{name: iam, interfaces: [oidc]}]}"),
		"saml":      fixture(t, "name: saml\nstatus: developing\ncapabilities: {provides: [{name: iam, interfaces: [saml]}]}"),
	}
	r, err := inspect(all)
	if err != nil {
		t.Fatal(err)
	}
	if r.Passed || len(r.Findings) != 1 || r.Findings[0].Dependency != "candidate" {
		t.Fatal(r)
	}
}
func TestMissingDirectDependencyFailsClosed(t *testing.T) {
	_, err := inspect(map[string]service{"app": fixture(t, "name: app\nstatus: release\ndependencies: {requires: [missing]}")})
	if err == nil {
		t.Fatal("missing dependency accepted")
	}
}
func TestReleaseOnlyClosurePasses(t *testing.T) {
	r, err := inspect(map[string]service{"app": fixture(t, "name: app\nstatus: release\ndependencies: {requires: [backend]}"), "backend": fixture(t, "name: backend\nstatus: release")})
	if err != nil || !r.Passed || len(r.Findings) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestWorkflowReviewPrecedesRevisionAndPublication(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "container-images.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	review, revision := -1, -1
	for i, s := range workflow.Jobs["prepare"].Steps {
		if s.Name == "Review release Module dependencies" {
			review = i
			if strings.TrimSpace(s.Run) != `go run ./cmd/check-release-modules --module "${REQUESTED_MODULE:-all}"` {
				t.Fatal("dependency review bypassed")
			}
		}
		if s.Name == "Calculate and commit release revisions" {
			revision = i
		}
	}
	if review < 0 || revision < 0 || review >= revision {
		t.Fatal("dependency review must run before revision writes")
	}
}

func TestWorkflowCatalogPreservesUnpublishedVersions(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required for workflow shell")
	}
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "container-images.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	script := ""
	for _, s := range workflow.Jobs["catalog"].Steps {
		if s.Name == "Build Module discovery catalog" {
			script = s.Run
		}
	}
	if script == "" {
		t.Fatal("missing catalog step")
	}
	dir := t.TempDir()
	prior := filepath.Join(dir, "prior.json")
	if err := os.WriteFile(prior, []byte(`{"api_version":"anas.module-catalog/v1","modules":[{"module":"stable","release":"1.0.0-r1"},{"module":"developing","release":"0.1.0-r2"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nset -eu\ncase $1 in\nresolve) echo sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa;;\npull) cp \"$PREVIOUS_CATALOG\" \"$4/module-catalog.json\";;\n*) exit 99;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "oras"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "-c", script)
	command.Dir = dir
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "PREVIOUS_CATALOG="+prior, "FIRST_PACKAGE_RELEASE=false", "GHCR_REPOSITORY=example/ghcr", "CNB_REPOSITORY=example/cnb", "RELEASE_SHA=test", "PACKAGE_MATRIX="+`{"include":[{"module":"stable","repository":"anas-module-stable","platforms":["linux/amd64"],"version":"1.0.0","revision":2,"tag":"1.0.0-r2"}]}`)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("catalog shell: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "module-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Modules []struct {
			Module  string
			Release string
		}
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range result.Modules {
		got[m.Module] = m.Release
	}
	if !reflect.DeepEqual(got, map[string]string{"stable": "1.0.0-r2", "developing": "0.1.0-r2"}) {
		t.Fatal(got)
	}
}

func TestTargetedPublicationDoesNotReviewUnrelatedReleaseRoots(t *testing.T) {
	all := map[string]service{"healthy": fixture(t, "name: healthy\nstatus: release"), "other": fixture(t, "name: other\nstatus: release\ndependencies: {requires: [dev]}"), "dev": fixture(t, "name: dev\nstatus: developing")}
	r, err := inspect(all, "healthy")
	if err != nil || !r.Passed || len(r.Findings) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := inspect(all, "dev"); err == nil {
		t.Fatal("targeted developing Module bypassed release status")
	}
}
