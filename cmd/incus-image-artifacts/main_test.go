package main

import (
	"bytes"
	"context"
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
