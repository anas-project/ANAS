package remotetest

// TEST_CASES: TESTAUTO-T-013

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceBundleRoundTripsCommitAndWorktreeWithSameDigest(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.txt", "committed\n")
	runGitFixture(t, root, "init")
	runGitFixture(t, root, "config", "user.name", "Test")
	runGitFixture(t, root, "config", "user.email", "test@example.invalid")
	runGitFixture(t, root, "add", ".")
	runGitFixture(t, root, "commit", "-m", "base")

	outputRoot := t.TempDir()
	committedBundle := filepath.Join(outputRoot, "committed.tar.gz")
	committed, err := BuildSourceBundle(context.Background(), root, committedBundle, SourceCommitted, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	committedDestination := filepath.Join(outputRoot, "committed")
	remoteCommitted, err := MaterializeSourceBundle(context.Background(), committedBundle, committed.BundleSHA256, committedDestination)
	if err != nil {
		t.Fatal(err)
	}
	if committed.SourceSHA256 != remoteCommitted.SourceSHA256 || readFixture(t, committedDestination, "tracked.txt") != "committed\n" {
		t.Fatalf("committed source identity or content mismatch: %#v %#v", committed, remoteCommitted)
	}

	write("tracked.txt", "worktree\n")
	write("nested/untracked.txt", "untracked\n")
	worktreeBundle := filepath.Join(outputRoot, "worktree.tar.gz")
	worktree, err := BuildSourceBundle(context.Background(), root, worktreeBundle, SourceWorktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	worktreeDestination := filepath.Join(outputRoot, "worktree")
	remoteWorktree, err := MaterializeSourceBundle(context.Background(), worktreeBundle, worktree.BundleSHA256, worktreeDestination)
	if err != nil {
		t.Fatal(err)
	}
	if worktree.SourceSHA256 != remoteWorktree.SourceSHA256 || worktree.BaseCommit != committed.BaseCommit {
		t.Fatalf("worktree identity mismatch: %#v %#v", worktree, remoteWorktree)
	}
	if readFixture(t, worktreeDestination, "tracked.txt") != "worktree\n" || readFixture(t, worktreeDestination, "nested/untracked.txt") != "untracked\n" {
		t.Fatal("worktree patch or untracked source was not materialized")
	}
	info, err := os.Stat(worktreeBundle)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("source bundle mode is not 0600: %v %v", info, err)
	}
}

func TestSourceBundleRejectsDigestMismatchBeforeExtraction(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}, {"add", "."}, {"commit", "-m", "base"}} {
		runGitFixture(t, root, args...)
	}
	bundle := filepath.Join(t.TempDir(), "source.tar.gz")
	identity, err := BuildSourceBundle(context.Background(), root, bundle, SourceCommitted, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "destination")
	if _, err := MaterializeSourceBundle(context.Background(), bundle, strings.Replace(identity.BundleSHA256, "a", "b", 1), destination); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected sender/receiver digest mismatch, got %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("digest mismatch changed destination: %v", err)
	}
	if _, err := BuildSourceBundle(context.Background(), root, bundle, SourceCommitted, "HEAD"); err == nil {
		t.Fatal("source bundle silently overwrote an existing artifact")
	}
	realDestination := filepath.Join(t.TempDir(), "real-destination")
	if err := os.Mkdir(realDestination, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkDestination := filepath.Join(t.TempDir(), "destination-link")
	if err := os.Symlink(realDestination, symlinkDestination); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeSourceBundle(context.Background(), bundle, identity.BundleSHA256, symlinkDestination); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("expected symlinked destination failure, got %v", err)
	}
}

func runGitFixture(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func readFixture(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
