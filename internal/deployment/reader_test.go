package deployment

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidateIDRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", ".", "..", "../outside", "a/b", `a\b`, "/absolute", "bad\x00id"} {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if err := ValidateID(id); err == nil {
				t.Fatalf("ValidateID(%q) succeeded", id)
			}
		})
	}
	for _, id := range []string{"deployment-1", "20260818T010203Z-a1b2c3", "release name"} {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q): %v", id, err)
		}
	}
}

func TestActiveMissingIsEmptySuccess(t *testing.T) {
	t.Parallel()
	state, err := NewReader(t.TempDir()).Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.APIVersion != StateAPIVersion || state.ActiveDeployment != "" {
		t.Fatalf("state = %#v", state)
	}
	if state.PreviousDeployments == nil || len(state.PreviousDeployments) != 0 {
		t.Fatalf("previous deployments = %#v, want non-nil empty", state.PreviousDeployments)
	}
}

func TestActiveReadsAndValidatesState(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".anas", "state", "active.yml")
	writeTestFile(t, path, []byte("api_version: anas.state/v2\nactive_deployment: dep-2\nprevious_deployments: [dep-1]\nactivated_at: 2026-08-18T01:02:03Z\n"))

	state, err := NewReader(workspace).Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveDeployment != "dep-2" || !reflect.DeepEqual(state.PreviousDeployments, []string{"dep-1"}) {
		t.Fatalf("state = %#v", state)
	}

	writeTestFile(t, path, []byte("api_version: anas.state/v1\n"))
	if _, err := NewReader(workspace).Active(context.Background()); err == nil {
		t.Fatal("unsupported state version succeeded")
	}
}

func TestListIsNonNilAndCreatedAtDescending(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	reader := NewReader(workspace)

	empty, err := reader.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty list = %#v, want non-nil empty", empty)
	}

	dir := filepath.Join(workspace, ".anas", "state", "deployments")
	writeTestFile(t, filepath.Join(dir, "z.yml"), stateYAML("z", "2026-08-18T02:00:00Z"))
	writeTestFile(t, filepath.Join(dir, "b.yml"), stateYAML("b", "2026-08-18T03:00:00Z"))
	writeTestFile(t, filepath.Join(dir, "a.yml"), stateYAML("a", "2026-08-18T03:00:00Z"))
	writeTestFile(t, filepath.Join(dir, "ignored.txt"), []byte("not yaml"))
	if err := os.MkdirAll(filepath.Join(dir, "directory.yml"), 0o700); err != nil {
		t.Fatal(err)
	}

	states, err := reader.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(states))
	for _, state := range states {
		got = append(got, state.ID)
	}
	if want := []string{"a", "b", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestInspectPreservesManifestBytesAndDefaultsMissingState(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	id := "dep-1"
	raw := []byte("# retained comment\napi_version: anas.deployment/v1\nid: dep-1\ncreated_at: 2026-08-18T01:00:00Z\nconfig_fingerprint: sha256:abc\nmodule_order: []\nmodules: {}\n")
	writeTestFile(t, filepath.Join(workspace, ".anas", "deployments", id, "deployment.yml"), raw)

	inspection, err := NewReader(workspace).Inspect(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Manifest == nil || inspection.Manifest.ID != id {
		t.Fatalf("manifest = %#v", inspection.Manifest)
	}
	if !bytes.Equal(inspection.RawManifest, raw) {
		t.Fatalf("raw manifest changed:\n%s", inspection.RawManifest)
	}
	if inspection.State.APIVersion != StateAPIVersion || inspection.State.ID != id {
		t.Fatalf("default state = %#v", inspection.State)
	}
}

func TestManifestReturnsRawBytesWhenDecodeFails(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	raw := []byte("api_version: [unterminated\n")
	writeTestFile(t, filepath.Join(workspace, ".anas", "deployments", "broken", "deployment.yml"), raw)

	manifest, got, err := NewReader(workspace).Manifest(context.Background(), "broken")
	if err == nil || manifest != nil {
		t.Fatalf("manifest = %#v, err = %v", manifest, err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("raw = %q, want %q", got, raw)
	}
}

func TestReaderHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := NewReader(t.TempDir())

	if _, err := reader.Active(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Active error = %v", err)
	}
	if _, err := reader.List(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("List error = %v", err)
	}
	if _, _, err := reader.Manifest(ctx, "dep"); !errors.Is(err, context.Canceled) {
		t.Errorf("Manifest error = %v", err)
	}
	if _, err := reader.State(ctx, "dep"); !errors.Is(err, context.Canceled) {
		t.Errorf("State error = %v", err)
	}
}

func writeTestFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stateYAML(id, createdAt string) []byte {
	return []byte("api_version: anas.state/v2\nid: " + id + "\nstatus: active\ncreated_at: " + createdAt + "\n")
}
