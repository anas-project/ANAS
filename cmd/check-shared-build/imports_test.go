package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedBuildRejectsMissingTransitiveGoPackage(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		".github/images.json":                `[{"module":"example","context":"modules/example/image","dockerfile":"modules/example/image/Dockerfile","shared_paths":["internal/client"]}]`,
		".github/modules.json":               `[{"module":"example","shared_contexts":["internal/client"]}]`,
		"internal/client/client_linux.go":    "package client\nimport _ \"github.com/anas-project/ANAS/internal/protection\"\n",
		"internal/protection/protection.go":  "package protection\n",
		"modules/example/image/main.go":      "package main\nimport _ \"github.com/anas-project/ANAS/internal/client\"\nfunc main() {}\n",
		"modules/example/image/Dockerfile":   "COPY --from=shared internal/client ./internal/client\n",
		"modules/example/docker-compose.yml": "services:\n  example:\n    build:\n      context: ./image\n      additional_contexts:\n        shared: ${ANAS_SHARED_BUILD_CONTEXT:-../..}\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := check(root); err == nil || !strings.Contains(err.Error(), "internal/protection") {
		t.Fatal("matching COPY/manifests hid a missing Linux transitive dependency", err)
	}
}
