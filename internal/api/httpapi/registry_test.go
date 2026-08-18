package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRegistryCanonicalizesPathsAndPreservesIDOrder(t *testing.T) {
	realWorkspace := t.TempDir()
	makeWorkspaceMarker(t, realWorkspace)
	linkRoot := t.TempDir()
	link := filepath.Join(linkRoot, "workspace")
	if err := os.Symlink(realWorkspace, link); err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	makeWorkspaceMarker(t, second)

	registry, err := NewRegistry([]Workspace{{ID: "primary", Path: link}, {ID: "second-2", Path: second}})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(realWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := registry.Resolve("primary"); !ok || got != canonical {
		t.Fatalf("Resolve(primary) = %q, %v; want %q, true", got, ok, canonical)
	}
	if got := registry.IDs(); !reflect.DeepEqual(got, []string{"primary", "second-2"}) {
		t.Fatalf("IDs() = %v", got)
	}
	ids := registry.IDs()
	ids[0] = "mutated"
	if got := registry.IDs()[0]; got != "primary" {
		t.Fatalf("IDs returned mutable registry storage: %q", got)
	}
}

func TestRegistryRejectsUnsafeRegistrations(t *testing.T) {
	workspace := t.TempDir()
	makeWorkspaceMarker(t, workspace)
	notDirectory := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDirectory, []byte("not a workspace"), 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		workspaces []Workspace
		contains   string
	}{
		{name: "relative path", workspaces: []Workspace{{ID: "main", Path: "relative"}}, contains: "absolute"},
		{name: "missing path", workspaces: []Workspace{{ID: "main", Path: filepath.Join(workspace, "missing")}}, contains: "resolve"},
		{name: "not directory", workspaces: []Workspace{{ID: "main", Path: notDirectory}}, contains: "not a directory"},
		{name: "missing marker", workspaces: []Workspace{{ID: "main", Path: t.TempDir()}}, contains: "state directory"},
		{name: "marker not directory", workspaces: []Workspace{{ID: "main", Path: workspaceWithFileMarker(t)}}, contains: "not a directory"},
		{name: "duplicate ID", workspaces: []Workspace{{ID: "main", Path: workspace}, {ID: "main", Path: workspace}}, contains: "more than once"},
		{name: "duplicate path", workspaces: []Workspace{{ID: "main", Path: workspace}, {ID: "alias", Path: workspace}}, contains: "same path"},
		{name: "path-like ID", workspaces: []Workspace{{ID: "../main", Path: workspace}}, contains: "must start"},
		{name: "reserved ID", workspaces: []Workspace{{ID: "..", Path: workspace}}, contains: "reserved"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewRegistry(test.workspaces)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("NewRegistry() error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func makeWorkspaceMarker(t *testing.T, workspace string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(workspace, ".anas"), 0700); err != nil {
		t.Fatal(err)
	}
}

func workspaceWithFileMarker(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".anas"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestParseWorkspace(t *testing.T) {
	got, err := ParseWorkspace("main=/srv/anas")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Workspace{ID: "main", Path: "/srv/anas"}); got != want {
		t.Fatalf("ParseWorkspace() = %#v, want %#v", got, want)
	}
	for _, value := range []string{"", "main", "=/srv/anas", "main="} {
		if _, err := ParseWorkspace(value); err == nil {
			t.Errorf("ParseWorkspace(%q) succeeded", value)
		}
	}
}
