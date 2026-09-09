package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedBuildRejectsDrift(t *testing.T) {
	for _, mode := range []string{"valid", "missing-path", "missing-copy", "missing-revision", "missing-compose"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				".github/images.json":                `[{"module":"example","context":"modules/example/image","dockerfile":"modules/example/image/Dockerfile","shared_paths":["internal/client"]}]`,
				".github/modules.json":               `[{"module":"example","shared_contexts":["internal/client"]}]`,
				"internal/client/client.go":          "package client\n",
				"modules/example/image/Dockerfile":   "COPY --from=shared internal/client ./internal/client\n",
				"modules/example/docker-compose.yml": "services:\n  example:\n    build:\n      context: ./image\n      additional_contexts:\n        shared: ${ANAS_SHARED_BUILD_CONTEXT:-../..}\n",
			}
			if mode == "missing-path" {
				delete(files, "internal/client/client.go")
			}
			if mode == "missing-copy" {
				files["modules/example/image/Dockerfile"] = "FROM scratch\n"
			}
			if mode == "missing-revision" {
				files[".github/modules.json"] = `[{"module":"example"}]`
			}
			if mode == "missing-compose" {
				files["modules/example/docker-compose.yml"] = strings.ReplaceAll(files["modules/example/docker-compose.yml"], "${ANAS_SHARED_BUILD_CONTEXT:-../..}", "../wrong")
			}
			for path, body := range files {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			err := check(root)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}
