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
	if err := os.WriteFile(filepath.Join(root, "internal/client/client_linux.go"), []byte("package client\n"), 0644); err != nil {
		t.Fatal(err)
	}
	compose := filepath.Join(root, "modules/example/docker-compose.yml")
	body, err := os.ReadFile(compose)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte("  other-image:\n    build:\n      context: ./image\n      dockerfile: Other.Dockerfile\n")...)
	if err := os.WriteFile(compose, body, 0644); err != nil {
		t.Fatal(err)
	}
	if err := check(root); err != nil {
		t.Fatal("unrelated Dockerfile inherited this image's shared context requirement:", err)
	}
}

func TestSharedBuildOnlyScansCopiedLocalSources(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		"modules/example/Dockerfile":          "COPY service ./service\nCOPY --from=builder /out/bin /bin\n",
		"modules/example/service/main.go":     "package main\nfunc main() {}\n",
		"modules/example/other/main_linux.go": "package main\nimport _ \"github.com/anas-project/ANAS/internal/not-copied\"\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	image := imageEntry{Context: "modules/example", Dockerfile: "modules/example/Dockerfile"}
	if err := checkSharedGoImports(root, image, nil); err != nil {
		t.Fatal("uncopied program affected this image:", err)
	}
	if err := os.WriteFile(filepath.Join(root, image.Dockerfile), []byte("COPY . .\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := checkSharedGoImports(root, image, nil); err == nil || !strings.Contains(err.Error(), "internal/not-copied") {
		t.Fatalf("copied Linux import was not checked: %v", err)
	}
}
