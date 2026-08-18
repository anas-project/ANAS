package modulepackage

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCatalogCoversEveryModule(t *testing.T) {
	root := repositoryRoot(t)
	entries, err := LoadCatalog(filepath.Join(root, ".github", "modules.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCatalog(root, entries); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeTrackedFilesExcludeDocumentationAndTests(t *testing.T) {
	files, err := runtimeTrackedFiles(repositoryRoot(t), "nextcloud")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, "\n")
	for _, excluded := range []string{"README.md", "localization.yml", "docs/technical.md", "docs/technical.en.md", "main_test.go", "iam_test.go"} {
		if strings.Contains(joined, excluded) {
			t.Errorf("runtime files contain %s", excluded)
		}
	}
	for _, required := range []string{"modules/nextcloud/module.yml", "modules/nextcloud/docker-compose.yml", "modules/nextcloud/hook/main.go", "modules/nextcloud/nextcloud/Dockerfile"} {
		if !strings.Contains(joined, required) {
			t.Errorf("runtime files omit %s", required)
		}
	}
}

func TestDocumentationExclusionsAreScopedToCatalogRoots(t *testing.T) {
	for _, path := range []string{
		"modules/demo/docs/technical.md",
		"contracts/database/docs/technical.md",
		"contracts/database/documentation.yml",
	} {
		if !excludedRuntimeFile(path) {
			t.Errorf("excludedRuntimeFile(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		"modules/demo/runtime/docs/payload.txt",
		"contracts/database/runtime/docs/payload.txt",
		"contracts/database/runtime/documentation.yml",
	} {
		if excludedRuntimeFile(path) {
			t.Errorf("excludedRuntimeFile(%q) = true, want false", path)
		}
	}
	for path, want := range map[string]bool{
		"modules/demo/docs":                           true,
		"contracts/database/docs":                     true,
		"modules/demo/runtime/docs":                   false,
		"contracts/database/runtime/docs":             false,
		"contracts/work/ANAS/contracts/database/docs": false,
	} {
		if got := excludedRuntimeDirectory(path); got != want {
			t.Errorf("excludedRuntimeDirectory(%q) = %t, want %t", path, got, want)
		}
	}
}

func TestExpandContextFilesExcludesExplicitDocumentationFile(t *testing.T) {
	root := t.TempDir()
	documentation := filepath.Join("contracts", "database", "documentation.yml")
	schema := filepath.Join("contracts", "database", "schemas", "resource.json")
	for path, body := range map[string]string{documentation: "documentation\n", schema: "{}\n"} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := expandContextFiles(root, []string{documentation, schema})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(files, "\n"); got != schema {
		t.Fatalf("expanded files = %q, want %q", got, schema)
	}
}

func TestBuildImageOnlyModulePackage(t *testing.T) {
	root := repositoryRoot(t)
	output := filepath.Join(t.TempDir(), "freeradius.tar.gz")
	result, err := Build(BuildOptions{
		RepoRoot:      root,
		CatalogPath:   filepath.Join(".github", "modules.json"),
		Module:        "freeradius",
		Platform:      "all",
		OutputPath:    output,
		SkipHookBuild: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Name != "freeradius" || result.Metadata.ContextDigest == "" || result.Metadata.ContentDigest == "" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
	jsonBody, err := json.Marshal(result.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonBody), `"api_version":"anas.module-package/v1"`) || strings.Contains(string(jsonBody), `"APIVersion"`) {
		t.Fatalf("metadata JSON does not use the public snake_case schema: %s", jsonBody)
	}
	entries := archiveEntries(t, output)
	for _, required := range []string{"package.yml", "module.yml", "docker-compose.yml", "contracts/relational_database/contract.yml"} {
		if !entries[required] {
			t.Errorf("package omits %s", required)
		}
	}
	for _, excluded := range []string{
		"README.md", "README.en.md", "localization.yml", "docs/technical.md", "docs/technical.en.md",
		"contracts/relational_database/README.md", "contracts/relational_database/README.en.md",
		"contracts/relational_database/documentation.yml", "contracts/relational_database/docs/technical.md",
	} {
		if entries[excluded] {
			t.Errorf("package contains %s", excluded)
		}
	}
	for entry := range entries {
		base := filepath.Base(entry)
		if strings.HasPrefix(base, "README") && strings.HasSuffix(base, ".md") {
			t.Errorf("package contains documentation %s", entry)
		}
		if strings.Contains(entry, "/docs/") || strings.HasSuffix(entry, "/docs") || strings.Contains(entry, "/__pycache__/") {
			t.Errorf("package contains excluded directory %s", entry)
		}
	}
	metadata, err := VerifyUnpackedArchiveForTest(output, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "freeradius" {
		t.Fatalf("verified metadata = %#v", metadata)
	}
}

func VerifyUnpackedArchiveForTest(archive, root string) (PackageMetadata, error) {
	file, err := os.Open(archive)
	if err != nil {
		return PackageMetadata{}, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return PackageMetadata{}, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return PackageMetadata{}, err
		}
		target := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(header.Name, "/")))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0755); err != nil {
				return PackageMetadata{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return PackageMetadata{}, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
		if err != nil {
			return PackageMetadata{}, err
		}
		_, copyErr := io.Copy(out, reader)
		closeErr := out.Close()
		if copyErr != nil {
			return PackageMetadata{}, copyErr
		}
		if closeErr != nil {
			return PackageMetadata{}, closeErr
		}
	}
	return VerifyUnpacked(root)
}

func archiveEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(header.Name, "/")] = true
	}
	return out
}
