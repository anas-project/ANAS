package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
)

// A crashed publisher can release flock without withdrawing its network
// artifacts. A nonempty, durable fence must not be treated as an idle lock.
func TestRuntimeWriteLockRejectsRetainedHTTPFence(t *testing.T) {
	base := stateDir(t.TempDir())
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "state", "lock")
	body := []byte(`{"schema":"anas.compute-http-workspace-fence/v1","state_directory_digest":"` + strings.Repeat("a", 64) + `"}` + "\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireRuntimeLockModeContext(context.Background(), base, syscall.LOCK_EX)
	if unlock != nil {
		unlock()
	}
	if err == nil {
		t.Fatal("workspace writer ignored retained HTTP publisher fence")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(body) {
		t.Fatal("writer changed retained evidence", err)
	}
	// Metadata readers must remain usable for diagnosis and authorized drain.
	unlock, err = acquireRuntimeSharedLock(base)
	if err != nil {
		t.Fatal("fence blocked read-only diagnosis", err)
	}
	unlock()
}

func TestRuntimeLockRejectsSymlinkInsteadOfLockingAnotherInode(t *testing.T) {
	base := stateDir(t.TempDir())
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(other, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(base, "state", "lock")); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireRuntimeLockModeContext(context.Background(), base, syscall.LOCK_EX)
	if unlock != nil {
		unlock()
	}
	if err == nil {
		t.Fatal("workspace writer followed a substituted lock")
	}
}

func TestStandaloneCredentialAndAdminRotationCannotBypassWorkspaceFence(t *testing.T) {
	workspace := t.TempDir()
	base := stateDir(workspace)
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	directory := filepath.Join(base, "state", "http-ingress")
	if err = os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	store := computeingressruntime.WorkspaceStateStore{Workspace: workspace, Directory: directory}
	if err = store.WithExclusive(context.Background(), func(computeingressruntime.Journal) error { return nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "state", "lock")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"credential", "rotate", "--all", "--force", "-y", "-w", workspace, "--json"},
		{"admin", "local", "rotate", "traefik", "-w", workspace, "--json"},
	} {
		out, stderr, code := capture(t, args...)
		if code != exitPrecondition || !strings.Contains(out+stderr, "HTTP ingress is active or requires recovery") || strings.Contains(out+stderr, "state_directory_digest") {
			t.Fatalf("standalone rotation bypassed or leaked fence: code=%d output=%s %s", code, out, stderr)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("CLI erased publisher recovery evidence", err)
	}
}

func TestRuntimeWriterFailsPromptlyWhileWorkspacePublisherHoldsReadLock(t *testing.T) {
	workspace := t.TempDir()
	base := stateDir(workspace)
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	directory := filepath.Join(base, "state", "http-ingress")
	if err = os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	store := computeingressruntime.WorkspaceStateStore{Workspace: workspace, Directory: directory}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.WithExclusive(context.Background(), func(computeingressruntime.Journal) error { close(entered); <-release; return nil })
	}()
	t.Cleanup(func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	select {
	case <-entered:
	case err := <-done:
		done <- err
		t.Fatal("publisher could not start", err)
	case <-time.After(3 * time.Second):
		t.Fatal("publisher did not establish fence")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err = acquireRuntimeLockContext(ctx, base)
	if unlock != nil {
		unlock()
		t.Fatal("writer overlapped active publisher")
	}
	if !errors.Is(err, computeingressruntime.ErrWorkspaceIngressActive) {
		t.Fatal("writer waited until cancellation or ignored fence", err)
	}
}
