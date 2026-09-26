package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func requireArtifactArchivePlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("artifact archive requires Linux or macOS")
	}
}

func TestArtifactCLIRecordInspectAndCatalog(t *testing.T) {
	requireArtifactArchivePlatform(t)
	base := t.TempDir()
	archive := filepath.Join(base, "archive")
	var output, diagnostic bytes.Buffer
	invoke := func(args ...string) int {
		t.Helper()
		output.Reset()
		diagnostic.Reset()
		return run(context.Background(), args, &output, &diagnostic)
	}
	if code := invoke("init", "--archive", archive); code != 0 {
		t.Fatalf("init = %d: %s", code, diagnostic.String())
	}
	for name, body := range map[string]string{
		"metadata": "metadata-private-payload",
		"rootfs":   "rootfs-private-payload",
		"recipe":   "recipe-private-payload",
	} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	common := []string{"--archive", archive, "--name", "fixture", "--revision", "r1", "--architecture", "amd64", "--interface", "incus_container"}
	record := append([]string{"record"}, common...)
	record = append(record, "--recipe", filepath.Join(base, "recipe"), "--metadata", filepath.Join(base, "metadata"), "--rootfs", filepath.Join(base, "rootfs"))
	if code := invoke(record...); code != 0 {
		t.Fatalf("record = %d: %s", code, diagnostic.String())
	}
	var first struct {
		Existing bool                         `json:"existing"`
		Release  computeimage.ArtifactRelease `json:"release"`
	}
	if err := json.Unmarshal(output.Bytes(), &first); err != nil || first.Existing || first.Release.Validate() != nil {
		t.Fatalf("record output: %s, %v", output.String(), err)
	}
	if strings.Contains(output.String(), "private-payload") || strings.Contains(output.String(), base) {
		t.Fatal("control output contains image bytes, recipe content or a source path")
	}
	if code := invoke(record...); code != 0 {
		t.Fatalf("repeat = %d: %s", code, diagnostic.String())
	}
	var repeated struct {
		Existing bool                         `json:"existing"`
		Release  computeimage.ArtifactRelease `json:"release"`
	}
	if err := json.Unmarshal(output.Bytes(), &repeated); err != nil || !repeated.Existing || repeated.Release.Entry != first.Release.Entry {
		t.Fatalf("repeat output: %s, %v", output.String(), err)
	}
	if code := invoke(append([]string{"inspect"}, common...)...); code != 0 {
		t.Fatalf("inspect = %d: %s", code, diagnostic.String())
	}
	if code := invoke("catalog", "--archive", archive, "--first-release"); code != 0 {
		t.Fatalf("first catalog = %d: %s", code, diagnostic.String())
	}
	previous := filepath.Join(base, "previous-catalog.json")
	if err := os.WriteFile(previous, output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if code := invoke("catalog", "--archive", archive, "--previous-catalog", previous); code != 0 {
		t.Fatalf("catalog = %d: %s", code, diagnostic.String())
	}
	var entries []computeimage.Entry
	if err := json.Unmarshal(output.Bytes(), &entries); err != nil || len(entries) != 1 || entries[0] != first.Release.Entry {
		t.Fatalf("catalog output: %s, %v", output.String(), err)
	}
	if err := os.WriteFile(filepath.Join(base, "rootfs"), []byte("changed bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := invoke(record...); code == 0 || output.Len() != 0 || strings.Contains(diagnostic.String(), base) {
		t.Fatalf("conflict = %d: stdout=%s stderr=%s", code, output.String(), diagnostic.String())
	}
	exportDir := filepath.Join(base, "export")
	if code := invoke(append([]string{"export"}, append(common, "--output-dir", exportDir)...)...); code != 0 {
		t.Fatalf("export = %d: %s", code, diagnostic.String())
	}
	if _, err := os.Stat(filepath.Join(exportDir, "artifact.json")); err != nil {
		t.Fatalf("export did not write descriptor: %v", err)
	}
	bundleDir := filepath.Join(base, "images")
	if code := invoke("bundle", "--archive", archive, "--previous-catalog", previous, "--output-dir", bundleDir); code != 0 {
		t.Fatalf("bundle = %d: %s", code, diagnostic.String())
	}
	var bundle computeimage.ArtifactBundle
	if err := json.Unmarshal(output.Bytes(), &bundle); err != nil || bundle.ImageCount != 1 || len(bundle.CatalogDigest) != 64 {
		t.Fatalf("bundle output: %s, %v", output.String(), err)
	}
	for _, forbidden := range []string{"private-payload", base, base64.StdEncoding.EncodeToString([]byte("rootfs-private-payload"))} {
		if strings.Contains(output.String()+diagnostic.String(), forbidden) {
			t.Fatal("bundle control output leaked an image payload or local path")
		}
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "artifacts", "anas", "fixture", "r1", "amd64", "incus_container", "rootfs.squashfs")); err != nil {
		t.Fatalf("bundle did not write the Provider layout: %v", err)
	}
	if code := invoke("bundle", "--archive", archive, "--previous-catalog", previous, "--output-dir", bundleDir); code == 0 || output.Len() != 0 {
		t.Fatal("bundle adopted an existing destination")
	}
	if code := invoke("recipe", "--image", "forgejo-runner", "--architecture", "amd64", "--interface", "incus_vm"); code != 0 {
		t.Fatalf("recipe = %d: %s", code, diagnostic.String())
	}
	if strings.Contains(output.String(), "alias") || !strings.Contains(output.String(), "source:") {
		t.Fatalf("unexpected recipe output: %s", output.String())
	}
}

func TestRecipeCLIFreezesExplicitChineseBuildSpeedup(t *testing.T) {
	t.Setenv("CHINESE_BUILD_SPEEDUP", "true")
	for _, option := range []string{"", "--chinese-build-speedup", "--chinese-build-speedup=false"} {
		args := []string{"recipe", "--image", "forgejo-runner", "--architecture", "amd64", "--interface", "incus_container"}
		if option != "" {
			args = append(args, option)
		}
		var output, diagnostic bytes.Buffer
		if code := run(context.Background(), args, &output, &diagnostic); code != 0 {
			t.Fatalf("recipe: %s", diagnostic.String())
		}
		wantMirror := option == "--chinese-build-speedup"
		if strings.Contains(output.String(), "url: https://mirrors.aliyun.com/debian") != wantMirror {
			t.Fatal("recipe ignored explicit source choice or inherited process environment")
		}
	}
}

func TestArtifactCLIBundleRequiresExplicitLocalDestinationAndHistory(t *testing.T) {
	requireArtifactArchivePlatform(t)
	base := t.TempDir()
	archivePath, destination := filepath.Join(base, "archive"), filepath.Join(base, "images")
	archive, err := computeimage.OpenArtifactArchive(context.Background(), archivePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	metadata, rootfs := filepath.Join(base, "metadata"), filepath.Join(base, "rootfs")
	for _, path := range []string{metadata, rootfs} {
		if err := os.WriteFile(path, []byte("fixture bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := archive.Record(context.Background(), computeimage.Reference{Catalog: "anas", Name: "fixture", Revision: "r1"},
		computeimage.Target{Architecture: "amd64", Interface: "incus_container"}, []byte("reviewed fixture recipe"),
		computeimage.ArtifactSplit, []string{metadata, rootfs}); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{
		{"--output-dir", destination},
		{"--first-release"},
		{"--first-release", "--previous-catalog", "previous.json", "--output-dir", destination},
		{"--first-release", "--output-dir", destination, "--download-url", "https://invalid.example/image"},
		{"--first-release", "--output-dir", destination, "--image-base64", "cGF5bG9hZA=="},
	} {
		var output, diagnostic bytes.Buffer
		args := append([]string{"bundle", "--archive", archivePath}, flags...)
		if code := run(context.Background(), args, &output, &diagnostic); code == 0 || output.Len() != 0 {
			t.Fatalf("accepted incomplete or external bulk-data input: %v", flags)
		}
		if strings.Contains(diagnostic.String(), "invalid.example") || strings.Contains(diagnostic.String(), "cGF5bG9hZA") {
			t.Fatal("rejected bulk-data input was echoed into diagnostics")
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatal("invalid bundle arguments created output")
		}
	}
}

func TestArtifactCLIRejectsImplicitInitializationAndAmbiguousHistory(t *testing.T) {
	requireArtifactArchivePlatform(t)
	base := t.TempDir()
	archive := filepath.Join(base, "missing")
	for name, args := range map[string][]string{
		"no implicit init":     {"record", "--archive", archive},
		"no history choice":    {"catalog", "--archive", archive},
		"both history choices": {"catalog", "--archive", archive, "--first-release", "--previous-catalog", "missing.json"},
		"unknown option":       {"init", "--archive", archive, "--private-secret-option", "value"},
		"too long":             {"init", "--archive", archive, "--timeout", "25h"},
		"extra positional":     {"init", "--archive", archive, "unexpected"},
	} {
		t.Run(name, func(t *testing.T) {
			var output, diagnostic bytes.Buffer
			if code := run(context.Background(), args, &output, &diagnostic); code == 0 || output.Len() != 0 {
				t.Fatalf("accepted invalid call: %d, %s", code, output.String())
			}
			if _, err := os.Stat(archive); !os.IsNotExist(err) {
				t.Fatalf("invalid call created an archive: %v", err)
			}
			if strings.Contains(diagnostic.String(), "private-secret-option") || strings.Contains(diagnostic.String(), archive) {
				t.Fatal("invalid-argument diagnostic exposed supplied values")
			}
		})
	}
}

func TestArtifactCLIBuildPreflightDoesNotConsumeARevision(t *testing.T) {
	requireArtifactArchivePlatform(t)
	base := t.TempDir()
	archive := filepath.Join(base, "archive")
	recipe := filepath.Join(base, "recipe.yml")
	if err := os.WriteFile(recipe, []byte("reviewed recipe"), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostic bytes.Buffer
	if code := run(context.Background(), []string{"init", "--archive", archive}, &output, &diagnostic); code != 0 {
		t.Fatal(diagnostic.String())
	}
	output.Reset()
	diagnostic.Reset()
	args := []string{"build", "--archive", archive, "--name", "fixture", "--revision", "r1", "--architecture", runtime.GOARCH, "--interface", "incus_container", "--recipe", recipe, "--distrobuilder", filepath.Join(base, "missing-builder"), "--distrobuilder-sha256", strings.Repeat("a", 64)}
	if code := run(context.Background(), args, &output, &diagnostic); code == 0 || output.Len() != 0 {
		t.Fatal("unavailable native builder reported success")
	}
	files, err := filepath.Glob(filepath.Join(archive, "build-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("preflight failure consumed a revision: %v", files)
	}
	if strings.Contains(diagnostic.String(), base) {
		t.Fatal("preflight leaked source paths")
	}
}
