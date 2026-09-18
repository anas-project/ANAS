package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStagedBuildRequiresMatchingSources(t *testing.T) {
	for _, scenario := range []string{
		"matching-checkout", "matching-copy", "missing-override", "relative-override",
		"missing-shared-file", "changed-shared-file", "extra-shared-file",
		"changed-module-file", "changed-dockerfile", "changed-executable-mode", "missing-compose-override",
		"different-dockerfile", "no-selected-images", "symlink-shared-file",
	} {
		t.Run(scenario, func(t *testing.T) {
			source, stage, sharedCopy := t.TempDir(), t.TempDir(), t.TempDir()
			compose := "services:\n  example:\n    build:\n      context: ./image\n      additional_contexts:\n        shared: ${ANAS_SHARED_BUILD_CONTEXT:-../..}\n"
			moduleFiles := map[string]string{
				"modules/example/docker-compose.yml": compose,
				"modules/example/image/Dockerfile":   "COPY --from=shared internal/client ./internal/client\nCOPY *.go ./\n",
				"modules/example/image/main.go":      "package main\nfunc main() {}\n",
			}
			sharedFiles := map[string]string{
				"go.mod":                    "module example.test/build\n",
				"go.sum":                    "",
				"internal/client/client.go": "package client\n",
			}
			writeBuildFixture(t, source, moduleFiles)
			writeBuildFixture(t, stage, moduleFiles)
			writeBuildFixture(t, source, sharedFiles)
			writeBuildFixture(t, sharedCopy, sharedFiles)
			writeBuildFixture(t, source, map[string]string{
				".github/images.json": `[{"module":"example","context":"modules/example/image","dockerfile":"modules/example/image/Dockerfile","shared_paths":["go.mod","go.sum","internal/client"]}]`,
			})
			override := source
			switch scenario {
			case "matching-copy":
				override = sharedCopy
			case "missing-override":
				override = ""
			case "relative-override":
				override = "../.."
			case "missing-shared-file":
				override = sharedCopy
				if err := os.Remove(filepath.Join(sharedCopy, "go.sum")); err != nil {
					t.Fatal(err)
				}
			case "changed-shared-file":
				override = sharedCopy
				writeBuildFixture(t, sharedCopy, map[string]string{"internal/client/client.go": "package wrong_revision\n"})
			case "extra-shared-file":
				override = sharedCopy
				writeBuildFixture(t, sharedCopy, map[string]string{"internal/client/extra.go": "package client\n"})
			case "changed-module-file":
				writeBuildFixture(t, stage, map[string]string{"modules/example/image/main.go": "package wrong_revision\n"})
			case "changed-dockerfile":
				writeBuildFixture(t, stage, map[string]string{"modules/example/image/Dockerfile": "FROM scratch\n"})
			case "changed-executable-mode":
				if err := os.Chmod(filepath.Join(stage, "modules", "example", "image", "main.go"), 0755); err != nil {
					t.Fatal(err)
				}
			case "missing-compose-override":
				writeBuildFixture(t, stage, map[string]string{"modules/example/docker-compose.yml": strings.ReplaceAll(compose, "${ANAS_SHARED_BUILD_CONTEXT:-../..}", "../..")})
			case "different-dockerfile":
				writeBuildFixture(t, stage, map[string]string{"modules/example/docker-compose.yml": strings.ReplaceAll(compose, "      context: ./image", "      context: ./image\n      dockerfile: Otherfile")})
			case "no-selected-images":
				stage = t.TempDir()
			case "symlink-shared-file":
				override = sharedCopy
				path := filepath.Join(sharedCopy, "internal", "client", "client.go")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(source, "internal", "client", "client.go"), path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			err := checkStagedBuildContexts(source, stage, override)
			valid := scenario == "matching-checkout" || scenario == "matching-copy"
			if (err == nil) != valid {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func writeBuildFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
