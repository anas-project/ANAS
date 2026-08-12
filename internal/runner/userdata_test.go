package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seedUserData gives a snapshot workspace the sibling tree that holds what
// people store, with one file standing in for everyone's documents.
func seedUserData(t *testing.T, workspace, contents string) string {
	t.Helper()
	dir := userDataDir(workspace)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "document")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The reason this separation exists: a rollback restores the deployment, and
// the files somebody saved afterwards are not part of the deployment. Before
// user content moved out of data/, every rollback silently deleted them.
func TestRestoreLeavesUserDataAloneByDefault(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	document := seedUserData(t, workspace, "before")

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindManual, reason: snapshotReasonManual, includeUserData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Somebody saves a file after the snapshot, and another one that did not
	// exist when it was taken.
	if err := os.WriteFile(document, []byte("edited after the snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(userDataDir(workspace), "written-later")
	if err := os.WriteFile(added, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := restoreSnapshot(workspace, meta, false, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(document)
	if err != nil || string(got) != "edited after the snapshot" {
		t.Fatalf("a restore rewound user content: %q, %v", got, err)
	}
	if !exists(added) {
		t.Fatal("a restore deleted a file saved after the snapshot")
	}
	// The deployment state it was supposed to restore still came back.
	data, err := os.ReadFile(filepath.Join(dataDir(workspace), "marker"))
	if err != nil || string(data) != "before" {
		t.Fatalf("application state was not restored: %q, %v", data, err)
	}
}

// Asking for it explicitly is the other half: recovering a workspace, rather
// than undoing a deploy, does mean taking the files back too.
func TestRestoreReplacesUserDataWhenAsked(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	document := seedUserData(t, workspace, "before")

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindManual, reason: snapshotReasonManual, includeUserData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !meta.capturedTree(snapshotTreeUserData) {
		t.Fatal("--include-userdata did not capture the tree")
	}
	if err := os.WriteFile(document, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}

	outcome, err := restoreSnapshot(workspace, meta, true, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(document)
	if err != nil || string(got) != "before" {
		t.Fatalf("user content was not restored: %q, %v", got, err)
	}
	if !contains(outcome.Restored, "userdata") {
		t.Fatalf("the outcome does not report userdata: %v", outcome.Restored)
	}
	// The undo has to cover what was replaced, or the irreplaceable tree is the
	// one thing with no way back.
	pre, err := loadSnapshot(workspace, outcome.PreRestoreSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !pre.capturedTree(snapshotTreeUserData) {
		t.Fatal("the pre-restore snapshot omitted the user content it was about to overwrite")
	}
}

// A snapshot says what it did not capture. Without that, a restore reports
// success while leaving the largest tree in the workspace untouched and
// nothing on disk records it.
func TestSnapshotRecordsUserDataCoverage(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	seedUserData(t, workspace, "before")

	excluded, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindAuto, reason: snapshotReasonPreApply})
	if err != nil {
		t.Fatal(err)
	}
	if excluded.capturedTree(snapshotTreeUserData) {
		t.Fatal("an automatic snapshot captured user content; a rollback could then delete files")
	}
	if !excluded.capturedTree(snapshotTreeData) {
		t.Fatal("application state must always be captured")
	}
	reason := ""
	for _, entry := range excluded.Coverage {
		if entry.Tree == snapshotTreeUserData {
			reason = entry.Reason
		}
	}
	if reason != coverageReasonExcluded {
		t.Fatalf("coverage reason = %q, want %q", reason, coverageReasonExcluded)
	}

	// A workspace with no user content at all says so, rather than claiming
	// the tree was captured.
	bare, _ := newSnapshotWorkspace(t)
	missing, err := createSnapshot(bare, snapshotOptions{
		kind: snapshotKindManual, reason: snapshotReasonManual, includeUserData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing.capturedTree(snapshotTreeUserData) {
		t.Fatal("a workspace without user content reported it as captured")
	}
}

// --restore-userdata against a snapshot that has none must fail loudly. The
// alternative is reporting a successful restore of something it never held.
func TestResolveUserDataRestoreRejectsUncapturedTree(t *testing.T) {
	excluded := &snapshotMeta{ID: "s1", Coverage: []snapshotCoverage{
		{Tree: snapshotTreeData, Captured: true},
		{Tree: snapshotTreeUserData, Reason: coverageReasonExcluded},
	}}
	if _, err := resolveUserDataRestore(true, excluded, true); err == nil {
		t.Fatal("expected an error when the snapshot has no user content")
	}

	// Without the flag, a non-interactive restore never touches user content,
	// including under -y: it means "do not ask me", not "do the destructive
	// thing".
	captured := &snapshotMeta{ID: "s2", Coverage: []snapshotCoverage{
		{Tree: snapshotTreeData, Captured: true},
		{Tree: snapshotTreeUserData, Captured: true},
	}}
	got, err := resolveUserDataRestore(false, captured, true)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("-y opted into replacing user content")
	}
	if got, err := resolveUserDataRestore(true, captured, true); err != nil || !got {
		t.Fatalf("an explicit request was refused: %v %v", got, err)
	}
}

// The dry run has to be trustworthy about the one tree whose loss is not
// recoverable from a redeploy.
func TestRestoreTargetsListUserDataOnlyWhenItIsReplaced(t *testing.T) {
	workspace := t.TempDir()
	meta := &snapshotMeta{ID: "s1", DeploymentID: "d1"}
	if contains(restoreTargets(workspace, meta, false), userDataDir(workspace)) {
		t.Fatal("a restore that keeps user content listed it as replaced")
	}
	if !contains(restoreTargets(workspace, meta, true), userDataDir(workspace)) {
		t.Fatal("a restore that replaces user content did not list it")
	}
}

// requireACLCapableRsync skips on hosts whose rsync predates -A, which is the
// stock macOS one. copyDirectory needs it to preserve ACLs, so these two cases
// exercise the real transfer on Linux rather than asserting against a stub.
func requireACLCapableRsync(t *testing.T) {
	t.Helper()
	out, err := exec.Command("rsync", "--version").Output()
	if err != nil || !strings.Contains(string(out), "ACLs") {
		t.Skip("rsync here does not support ACLs (-A); the copy transfer is exercised on Linux")
	}
}

// A backup exists so that a dead disk costs nothing, and user files are the
// part no redeploy can reproduce. So backups carry them by default -- the
// opposite of snapshots, and deliberately so.
func TestBackupCarriesUserDataByDefault(t *testing.T) {
	requireACLCapableRsync(t)
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	seedUserData(t, workspace, "irreplaceable")

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: snapshotReasonPreBackup, includeUserData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := snapshotBackupSource(workspace, meta)
	if source.userDataPath == "" {
		t.Fatal("a backup source built from a snapshot holding user content did not carry it")
	}

	dest := t.TempDir()
	result, err := transferByCopy(transferRequest{
		mode: backupModeCopy, source: source, destRoot: dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.channels, backupChannelUserData) {
		t.Fatalf("the transfer did not report a user data channel: %v", result.channels)
	}
	got, err := os.ReadFile(filepath.Join(backupUserDataPath(dest), "document"))
	if err != nil || string(got) != "irreplaceable" {
		t.Fatalf("user content did not reach the backup: %q, %v", got, err)
	}
}

// A snapshot that could not capture user content must not produce a backup
// that claims to carry it. The coverage record is the authority, not whatever
// directory happens to sit beside the snapshot.
func TestBackupOmitsUserDataTheSnapshotDidNotCapture(t *testing.T) {
	requireACLCapableRsync(t)
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	seedUserData(t, workspace, "irreplaceable")

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: snapshotReasonPreBackup,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := snapshotBackupSource(workspace, meta)
	if source.userDataPath != "" {
		t.Fatal("a backup claimed user content its snapshot never captured")
	}

	dest := t.TempDir()
	result, err := transferByCopy(transferRequest{
		mode: backupModeCopy, source: source, destRoot: dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if contains(result.channels, backupChannelUserData) {
		t.Fatalf("channels claim user content: %v", result.channels)
	}
	if exists(backupUserDataPath(dest)) {
		t.Fatal("the backup wrote a user data tree it did not record")
	}
}

// verify has to notice a promised channel that is not on disk. A backup whose
// files quietly did not arrive is worse than no backup, because it is trusted.
func TestVerifyDetectsMissingUserDataChannel(t *testing.T) {
	dest := t.TempDir()
	root := backupRoot(dest, "b1")
	if err := os.MkdirAll(filepath.Join(root, "data"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{snapshotMetaConfigName, snapshotMetaLockName, snapshotMetaSecretsName, snapshotMetaAdminsName, snapshotMetaStateName} {
		if err := os.MkdirAll(filepath.Join(root, "meta"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "meta", name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "deployment"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deployment", "deployment.yml"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := &backupManifest{
		APIVersion: backupAPIVersion, BackupID: "b1", Mode: backupModeCopy,
		Complete: true, Channels: []string{backupChannelData, backupChannelMetadata, backupChannelUserData},
	}
	if err := writeYAMLAtomic(backupManifestPath(root), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	problems := verifyBackup(dest, *manifest, map[string]bool{"b1": true})
	found := false
	for _, problem := range problems {
		if problem.Code == "userdata_stream_missing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("verify accepted a backup whose promised user data is absent: %+v", problems)
	}
}

// The same guarantee against a real filesystem. fakeBtrfs stands in for
// `btrfs subvolume snapshot` with a directory copy, which is the right stub for
// the logic but proves nothing about the two trees actually being independent
// subvolumes. Set ANAS_BTRFS_TESTDIR to a directory on Btrfs to run it.
func TestRealBtrfsRestoreKeepsUserData(t *testing.T) {
	root := os.Getenv("ANAS_BTRFS_TESTDIR")
	if root == "" {
		t.Skip("set ANAS_BTRFS_TESTDIR to a Btrfs directory to exercise real subvolumes")
	}
	workspace := filepath.Join(root, "ws-"+t.Name())
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if entries, err := os.ReadDir(snapshotsDir(workspace)); err == nil {
			for _, entry := range entries {
				_ = removeSnapshotTree(filepath.Join(snapshotsDir(workspace), entry.Name()))
			}
		}
		for _, tree := range []string{dataDir(workspace), userDataDir(workspace)} {
			_ = runBtrfs("subvolume", "delete", tree)
		}
		_ = os.RemoveAll(workspace)
	})
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	// Real subvolumes, created the way `anas init` creates them.
	for _, tree := range []string{dataDir(workspace), userDataDir(workspace)} {
		if err := runBtrfs("subvolume", "create", tree); err != nil {
			t.Skipf("%s is not usable Btrfs: %v", root, err)
		}
	}
	// Deleting a subvolume needs the user_subvol_rm_allowed mount option. A
	// host without it can create the snapshots but never remove them, so the
	// run would leave read-only subvolumes behind that the next one trips over.
	// Better to say so than to pass once and poison the directory.
	probe := filepath.Join(workspace, "rm-probe")
	if err := runBtrfs("subvolume", "create", probe); err != nil {
		t.Skipf("%s is not usable Btrfs: %v", root, err)
	}
	if err := runBtrfs("subvolume", "delete", probe); err != nil {
		t.Skipf("%s cannot delete subvolumes unprivileged (mount -o remount,user_subvol_rm_allowed): %v", root, err)
	}
	seedSnapshotWorkspaceAt(t, workspace)
	document := filepath.Join(userDataDir(workspace), "document")
	if err := os.WriteFile(document, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindManual, reason: snapshotReasonManual, includeUserData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !meta.capturedTree(snapshotTreeUserData) {
		t.Fatal("the snapshot did not capture the user data subvolume")
	}
	if err := btrfsSubvolumeShow(snapshotUserDataPath(snapshotRoot(workspace, meta.ID))); err != nil {
		t.Fatalf("the captured user data is not a real subvolume: %v", err)
	}

	if err := os.WriteFile(document, []byte("saved after the snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir(workspace), "marker"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreSnapshot(workspace, meta, false, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(document)
	if err != nil || string(got) != "saved after the snapshot" {
		t.Fatalf("a real restore rewound user content: %q, %v", got, err)
	}
	data, err := os.ReadFile(filepath.Join(dataDir(workspace), "marker"))
	if err != nil || string(data) != "before" {
		t.Fatalf("application state was not rewound: %q, %v", data, err)
	}
}

// removeSnapshotTree has to take both captured trees out through btrfs. A
// snapshotted subvolume is read-only, so anything it misses cannot then be
// removed by an ordinary recursive delete -- `snapshot delete` and `prune`
// would fail permanently, and only on the snapshots that carry the files worth
// keeping.
func TestRemoveSnapshotTreeDeletesBothSubvolumes(t *testing.T) {
	fakeBtrfs(t)
	deleted := []string{}
	previous := btrfsCommand
	btrfsCommand = func(args ...string) error {
		if len(args) >= 3 && args[0] == "subvolume" && args[1] == "delete" {
			deleted = append(deleted, filepath.Base(args[2]))
		}
		return previous(args...)
	}
	t.Cleanup(func() { btrfsCommand = previous })

	root := t.TempDir()
	for _, tree := range []string{snapshotDataPath(root), snapshotUserDataPath(root)} {
		if err := os.MkdirAll(tree, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeSnapshotTree(root); err != nil {
		t.Fatal(err)
	}
	if !contains(deleted, "data") || !contains(deleted, "userdata") {
		t.Fatalf("removeSnapshotTree deleted %v, want both data and userdata", deleted)
	}
	if exists(root) {
		t.Fatal("the snapshot directory survived")
	}
}

// The round trip. Unit tests so far have checked that a backup carries user
// content and that a restore puts something back; neither followed one file
// from the workspace, through a transfer, into a restored workspace. That is
// the only path that matters for the tree no redeploy can reproduce.
func TestBackupUserDataSurvivesARoundTrip(t *testing.T) {
	requireACLCapableRsync(t)
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	seedUserData(t, workspace, "the only copy")

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: snapshotReasonPreBackup, includeUserData: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	result, err := transferByCopy(transferRequest{
		mode: backupModeCopy, source: snapshotBackupSource(workspace, meta), destRoot: dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result.channels, backupChannelUserData) {
		t.Fatalf("the backup did not record a user data channel: %v", result.channels)
	}

	// Restore into a workspace whose user content has since been lost, which is
	// the situation a backup exists for.
	target, _ := newSnapshotWorkspace(t)
	if err := os.RemoveAll(userDataDir(target)); err != nil {
		t.Fatal(err)
	}
	if err := installBackupData(backupUserDataPath(dest), target, userDataDir(target)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(userDataDir(target), "document"))
	if err != nil || string(got) != "the only copy" {
		t.Fatalf("user content did not survive the round trip: %q, %v", got, err)
	}
}

// The same trip when the source could not be captured: the backup must carry
// no user data channel, and a restore must not invent an empty tree that looks
// like the files were restored and found to be gone.
func TestBackupRoundTripOmitsUncapturedUserData(t *testing.T) {
	requireACLCapableRsync(t)
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	seedUserData(t, workspace, "not captured")

	meta, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: snapshotReasonPreBackup,
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if _, err := transferByCopy(transferRequest{
		mode: backupModeCopy, source: snapshotBackupSource(workspace, meta), destRoot: dest,
	}); err != nil {
		t.Fatal(err)
	}
	if exists(backupUserDataPath(dest)) {
		t.Fatal("a backup that captured no user content still wrote a user data tree")
	}
}
