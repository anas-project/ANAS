package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/runtimeissues"
)

// ISSUE-R-006, R-008: `anas issues` reads both stores without anasd; an open
// error or an unreadable store is ok: false with exit 1, never "no issues".
func TestIssuesCommandReportsOpenErrorsAndUnreadableStores(t *testing.T) {
	workspace := newWorkspace(t)
	previous := issuesHostPath
	t.Cleanup(func() { issuesHostPath = previous })
	issuesHostPath = filepath.Join(t.TempDir(), "host-issues.json")

	stdout, _, exit := capture(t, "issues", "-w", workspace, "--json")
	document := requireSingleDocument(t, "empty", stdout)
	if exit != 0 || document["ok"] != true || document["open_errors"] != float64(0) {
		t.Fatalf("empty stores = %v, exit %d", document, exit)
	}

	store := runtimeissues.Open(runtimeissues.WorkspacePath(workspace), nil)
	if err := store.Record(runtimeissues.SourceApply, runtimeissues.Finding{Key: "incus.port-binding/forgejo.runners/tcp/30022", Message: "port in use"}); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit = capture(t, "issues", "-w", workspace, "--json")
	document = requireSingleDocument(t, "open", stdout)
	if exit != exitFailure || document["ok"] != false || document["open_errors"] != float64(1) {
		t.Fatalf("open error = %v, exit %d", document, exit)
	}
	if err := store.Reconcile(runtimeissues.SourceApply, "incus.port-binding/", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, exit = capture(t, "issues", "-w", workspace, "--json"); exit != 0 {
		t.Fatalf("a resolved issue still fails the command: exit %d", exit)
	}

	if err := os.WriteFile(issuesHostPath, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, _, exit = capture(t, "issues", "-w", workspace, "--json")
	document = requireSingleDocument(t, "unreadable", stdout)
	if exit != exitFailure || document["ok"] != false {
		t.Fatalf("unreadable host store = %v, exit %d", document, exit)
	}
}
