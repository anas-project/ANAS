package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildReportFixture(t *testing.T) (string, string) {
	t.Helper()
	source, stage := t.TempDir(), t.TempDir()
	compose := `services:
  z_second:
    build:
      context: ./image
      additional_contexts:
        shared: ${ANAS_SHARED_BUILD_CONTEXT:-../..}
    environment:
      SECRET: never-report-runtime-secrets
    env_file: ./must-not-be-opened.env
  a_first:
    build:
      context: ./image
      additional_contexts:
        shared: ${ANAS_SHARED_BUILD_CONTEXT:-../..}
    command: never-report-runtime-command
`
	module := map[string]string{
		"modules/example/docker-compose.yml": compose,
		"modules/example/image/Dockerfile":   "COPY --from=shared go.mod go.sum ./\nCOPY --from=shared internal/client ./internal/client\nCOPY *.go ./\n",
		"modules/example/image/main.go":      "package main\nfunc main() {}\n",
	}
	writeBuildFixture(t, source, module)
	writeBuildFixture(t, stage, module)
	writeBuildFixture(t, source, map[string]string{
		"go.mod":                    "module example.test/build\n",
		"go.sum":                    "",
		"internal/client/client.go": "package client\n",
		".github/images.json":       `[{"module":"example","context":"modules/example/image","dockerfile":"modules/example/image/Dockerfile","shared_paths":["go.mod","go.sum","internal/client"]}]`,
		".github/modules.json":      `[{"module":"example","shared_contexts":["go.mod","go.sum","internal/client"]}]`,
	})
	return source, stage
}

// INCUS-R-084: preserve actual Compose path semantics without evaluating or
// disclosing deployment secrets. No Docker daemon is used by these tests.
func TestBuildReportPreservesBothLayoutsWithoutRuntimeInputs(t *testing.T) {
	source, stage := buildReportFixture(t)
	if err := check(source); err != nil {
		t.Fatal(err)
	}
	checkout, err := makeBuildReport(source, "", "")
	if err != nil {
		t.Fatal(err)
	}
	staged, err := makeBuildReport(source, stage, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []*buildContextReport{checkout, staged} {
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if report.Schema != "anas.shared-build-inputs/v1" || report.DockerExecuted || len(report.Images) != 2 {
			t.Fatal("report confused static inputs with an executed build")
		}
		if report.Images[0].Service != "a_first" || report.Images[1].Service != "z_second" {
			t.Fatal("build services are not reported deterministically")
		}
		for _, forbidden := range []string{"never-report", "must-not-be-opened", "env_file", "environment", "command"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("runtime input entered build report: %s", forbidden)
			}
		}
		for i, image := range report.Images {
			if image.InputDigest != checkout.Images[i].InputDigest || image.SharedRoot != source || len(image.InputDigest) != 64 {
				t.Fatal("identical source and staging inputs have different identities")
			}
			if image.Build.Context != "./image" || image.Build.Additional["shared"] != "${ANAS_SHARED_BUILD_CONTEXT:-../..}" {
				t.Fatal("report rewrote Compose path semantics")
			}
		}
	}
	if checkout.BuildRoot != source || staged.BuildRoot != stage || staged.Images[0].ComposeDirectory != filepath.Join(stage, "modules", "example") {
		t.Fatal("report selected the wrong build directory")
	}
}

func TestBuildReportRejectsIncompleteOrUnexpectedBuildInputs(t *testing.T) {
	for _, scenario := range []string{"missing-override", "wrong-shared-root", "changed-stage", "unknown-build-input", "secret-build-argument", "additional-context", "wrong-dockerfile", "empty-selection"} {
		t.Run(scenario, func(t *testing.T) {
			source, stage := buildReportFixture(t)
			override := source
			composePath := filepath.Join(stage, "modules", "example", "docker-compose.yml")
			body, err := os.ReadFile(composePath)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing-override":
				override = ""
			case "wrong-shared-root":
				override = t.TempDir()
			case "changed-stage":
				writeBuildFixture(t, stage, map[string]string{"modules/example/image/main.go": "package wrong_revision\n"})
			case "unknown-build-input":
				body = []byte(strings.ReplaceAll(string(body), "context: ./image", "context: ./image\n      ssh: [default]"))
			case "secret-build-argument":
				body = []byte(strings.ReplaceAll(string(body), "context: ./image", "context: ./image\n      args:\n        TOKEN: private-build-credential"))
			case "additional-context":
				body = []byte(strings.ReplaceAll(string(body), "additional_contexts:", "additional_contexts:\n        remote: https://example.invalid/private"))
			case "wrong-dockerfile":
				body = []byte(strings.ReplaceAll(string(body), "context: ./image", "context: ./image\n      dockerfile: Otherfile"))
			case "empty-selection":
				stage = t.TempDir()
			}
			if err := os.WriteFile(composePath, body, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := makeBuildReport(source, stage, override); err == nil {
				t.Fatal("invalid build report was accepted")
			} else if strings.Contains(err.Error(), "private-build-credential") {
				t.Fatal("rejected build credential was disclosed")
			}
		})
	}
}

func TestBuildReportDigestBindsSharedBytesAndExecutableModes(t *testing.T) {
	source, _ := buildReportFixture(t)
	before, err := makeBuildReport(source, "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "internal", "client", "client.go")
	for _, change := range []func() error{
		func() error { return os.WriteFile(path, []byte("package client\nconst Version = 2\n"), 0644) },
		func() error { return os.Chmod(path, 0755) },
	} {
		if err := change(); err != nil {
			t.Fatal(err)
		}
		after, err := makeBuildReport(source, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if before.Images[0].InputDigest == after.Images[0].InputDigest {
			t.Fatal("changed build input retained its old digest")
		}
		before = after
	}
}
