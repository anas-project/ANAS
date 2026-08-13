package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeBtrfs makes the snapshot bookkeeping testable on whatever filesystem the
// temp directory happens to be. `subvolume snapshot` becomes a plain recursive
// copy and `subvolume delete` a recursive removal, which is enough to observe
// what the subsystem does with the results.
func fakeBtrfs(t *testing.T) {
	t.Helper()
	originalCheck := btrfsSubvolumeCheck
	btrfsSubvolumeCheck = func(path string) error {
		if !exists(path) {
			return os.ErrNotExist
		}
		return nil
	}
	t.Cleanup(func() { btrfsSubvolumeCheck = originalCheck })

	originalCommand := btrfsCommand
	btrfsCommand = func(args ...string) error {
		switch {
		case len(args) >= 4 && args[0] == "subvolume" && args[1] == "snapshot":
			src, dst := args[2], args[3]
			if src == "-r" {
				src, dst = args[3], args[4]
			}
			return copyTree(src, dst, func(from, to string) error {
				info, err := os.Lstat(from)
				if err != nil {
					return err
				}
				return copyFileMode(from, to, info.Mode().Perm())
			})
		case len(args) >= 3 && args[0] == "subvolume" && args[1] == "delete":
			return os.RemoveAll(args[2])
		case len(args) >= 3 && args[0] == "subvolume" && args[1] == "create":
			return os.MkdirAll(args[2], 0700)
		}
		return nil
	}
	t.Cleanup(func() { btrfsCommand = originalCommand })
}

// newSnapshotWorkspace builds the smallest workspace a snapshot can be taken
// from: an active deployment carrying its own config, and some data.
func newSnapshotWorkspace(t *testing.T) (workspace, deploymentID string) {
	t.Helper()
	workspace = t.TempDir()
	if err := os.MkdirAll(dataDir(workspace), 0700); err != nil {
		t.Fatal(err)
	}
	return workspace, seedSnapshotWorkspaceAt(t, workspace)
}

// seedSnapshotWorkspaceAt fills in everything a snapshot needs to exist, given
// a workspace whose data trees are already in place. It is split out so the
// real-Btrfs test can create those trees as actual subvolumes first.
func seedSnapshotWorkspaceAt(t *testing.T, workspace string) (deploymentID string) {
	t.Helper()
	base := stateDir(workspace)
	deploymentID = "20260101T000000Z-deadbeef"
	if err := ensureRuntimeLayout(base); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir(workspace), "marker"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	artifact := deploymentArtifactDir(base, deploymentID)
	if err := os.MkdirAll(filepath.Join(artifact, "modules", "core"), 0700); err != nil {
		t.Fatal(err)
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: deploymentID, ModuleOrder: []string{"core"},
		Modules: map[string]deploymentModule{"core": {Name: "core", Version: "1.0.0", RuntimeType: "builtin"}},
	}
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveModuleLockFile(filepath.Join(artifact, "lock.yml"), &moduleLock{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deploymentConfigSourcePath(artifact), []byte("modules:\n  core: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte("modules:\n  core: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secrets.generated.yml"), []byte("KEY: value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	admins := localAdminState{
		APIVersion: localAdminStateVersion,
		Accounts: map[string]localAdminRecord{
			"core.primary": {Module: "core", ID: "primary", Purpose: "break_glass", Username: "admin_core", SecretKey: "CORE_PASSWORD"},
		},
	}
	if err := writeYAMLAtomic(localAdminStatePath(base), &admins, 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveDeploymentState(base, deploymentState{ID: deploymentID, Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := saveActiveState(base, &activeDeploymentState{ActiveDeployment: deploymentID}); err != nil {
		t.Fatal(err)
	}
	return deploymentID
}

// A snapshot has to be restorable with nothing but itself, because once it has
// been copied to another disk there is no .anas alongside it to fall back on.
func TestSnapshotCarriesEverythingNeededToRestoreItAlone(t *testing.T) {
	fakeBtrfs(t)
	workspace, deploymentID := newSnapshotWorkspace(t)

	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	root := snapshotRoot(workspace, meta.ID)
	for _, rel := range []string{
		filepath.Join("meta", snapshotMetaConfigName),
		filepath.Join("meta", snapshotMetaLockName),
		filepath.Join("meta", snapshotMetaSecretsName),
		filepath.Join("meta", snapshotMetaAdminsName),
		filepath.Join("meta", snapshotMetaStateName),
		filepath.Join("deployment", "deployment.yml"),
		filepath.Join("deployment", deploymentConfigSourceName),
		filepath.Join("data", "marker"),
	} {
		if !exists(filepath.Join(root, rel)) {
			t.Errorf("snapshot is missing %s", rel)
		}
	}
	if meta.DeploymentID != deploymentID {
		t.Errorf("snapshot records deployment %q, want %q", meta.DeploymentID, deploymentID)
	}
	var admins localAdminState
	if err := readYAML(snapshotMetaEntry(root, snapshotMetaAdminsName), &admins); err != nil {
		t.Fatal(err)
	}
	if got := admins.Accounts["core.primary"].Username; got != "admin_core" {
		t.Errorf("snapshot local administrator username = %q, want admin_core", got)
	}
	if !meta.Complete {
		t.Error("complete was not written")
	}
	// active.yml must not be carried: its previous_deployments would name
	// deployments the snapshot does not contain.
	if exists(filepath.Join(root, "meta", "active.yml")) {
		t.Error("the snapshot copied active.yml, whose previous_deployments would be dangling")
	}
	// The config comes from the deployment, not from whatever is on disk now.
	got, err := os.ReadFile(filepath.Join(root, "meta", snapshotMetaConfigName))
	if err != nil || string(got) != "modules:\n  core: {}\n" {
		t.Fatalf("meta config = %q, %v", got, err)
	}
}

// The config a snapshot carries must match its deployment, not the disk. A
// pre-upgrade snapshot is taken after the user has already edited config.yml,
// so copying the disk would capture the very state the snapshot exists to
// escape.
func TestSnapshotTakesConfigFromTheDeploymentNotTheDisk(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte("modules:\n  core: {}\n  nextcloud: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindAuto, reason: snapshotReasonPreApply})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(snapshotMetaEntry(snapshotRoot(workspace, meta.ID), snapshotMetaConfigName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "modules:\n  core: {}\n" {
		t.Fatalf("snapshot captured the edited config %q instead of the deployment's", got)
	}
}

func TestSnapshotRefusesADeploymentWithoutItsSourceConfig(t *testing.T) {
	fakeBtrfs(t)
	workspace, deploymentID := newSnapshotWorkspace(t)
	artifact := deploymentArtifactDir(stateDir(workspace), deploymentID)
	if err := os.Remove(deploymentConfigSourcePath(artifact)); err != nil {
		t.Fatal(err)
	}
	_, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err == nil {
		t.Fatal("a snapshot was taken of a deployment whose config could not be recovered")
	}
	if e, ok := err.(*CLIError); !ok || e.Code != "config_source_missing" {
		t.Fatalf("error = %v, want code config_source_missing", err)
	}
}

func TestRestoreRewindsDataAndLeavesAWayBack(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir(workspace), "marker"), []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir(workspace), "added-later"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	// A config edit made after the snapshot must come back too, since restoring
	// half the state would leave keys that do not match the data.
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte("modules:\n  core: {}\n  drift: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	outcome, err := restoreSnapshot(workspace, meta, false, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dataDir(workspace), "marker"))
	if err != nil || string(got) != "before" {
		t.Fatalf("data written before the snapshot did not come back: %q, %v", got, err)
	}
	if exists(filepath.Join(dataDir(workspace), "added-later")) {
		t.Error("data written after the snapshot survived the restore")
	}
	got, err = os.ReadFile(workspaceConfigPath(workspace))
	if err != nil || string(got) != "modules:\n  core: {}\n" {
		t.Fatalf("config = %q, %v", got, err)
	}
	// The restore itself has to be undoable.
	if outcome.PreRestoreSnapshot == "" {
		t.Fatal("no pre_restore snapshot was taken")
	}
	pre, err := loadSnapshot(workspace, outcome.PreRestoreSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if pre.Reason != snapshotReasonPreRestore {
		t.Errorf("pre-restore snapshot reason = %q", pre.Reason)
	}
	if !exists(filepath.Join(snapshotRoot(workspace, pre.ID), "data", "added-later")) {
		t.Error("the pre-restore snapshot did not capture the data it was about to discard")
	}
	// active.yml is regenerated from the snapshot's own deployment_id.
	active, err := loadActiveState(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveDeployment != meta.DeploymentID || len(active.PreviousDeployments) != 0 {
		t.Fatalf("active state = %+v, want a regenerated pointer at %s", active, meta.DeploymentID)
	}
}

func TestRestoreRefusesAnIncompleteSnapshot(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	meta.Complete = false
	if _, err := restoreSnapshot(workspace, meta, false, false); err == nil {
		t.Fatal("an interrupted snapshot was restored")
	}
}

// Pinned and manual snapshots are excluded from the count as well as from the
// collection: if pinning consumed a slot, pinning three would silently shrink
// how much automatic history survives.
func TestRetentionExcludesPinnedAndManualFromTheCount(t *testing.T) {
	all := []snapshotMeta{
		{ID: "a", Kind: snapshotKindAuto},
		{ID: "b", Kind: snapshotKindManual},
		{ID: "c", Kind: snapshotKindAuto, Pinned: true},
		{ID: "d", Kind: snapshotKindAuto},
		{ID: "e", Kind: snapshotKindAuto},
	}
	collect, retained, pinned := snapshotsToPrune(all, 2)
	if len(collect) != 1 || collect[0].ID != "e" {
		t.Fatalf("collect = %v, want only the oldest automatic snapshot", collect)
	}
	if retained != 2 || pinned != 1 {
		t.Fatalf("retained = %d, pinned = %d, want 2 and 1", retained, pinned)
	}
}

func TestInterruptedSnapshotIsCleanedUp(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	debris := snapshotTempRoot(workspace, "20260101T000000Z-abandoned")
	if err := os.MkdirAll(filepath.Join(debris, "meta"), 0700); err != nil {
		t.Fatal(err)
	}
	cleanStaleSnapshotTemp(workspace)
	if exists(debris) {
		t.Fatalf("%s survived cleanup", debris)
	}
}

// The index is derived, so a mismatch is repairable rather than damage; the
// scan is what defines the truth it is repaired to.
func TestSnapshotIndexIsRebuiltFromTheScan(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	if _, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual}); err != nil {
		t.Fatal(err)
	}
	stale := snapshotIndex{APIVersion: activeStateVersion, Snapshots: []snapshotIndexEntry{{ID: "ghost"}}}
	if err := writeYAMLAtomic(snapshotIndexPath(stateDir(workspace)), &stale, 0600); err != nil {
		t.Fatal(err)
	}
	all, err := listSnapshots(workspace)
	if err != nil {
		t.Fatal(err)
	}
	index, err := loadSnapshotIndex(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if indexMatchesScan(index, all) {
		t.Fatal("a stale index was reported as matching")
	}
	if err := rebuildSnapshotIndex(workspace); err != nil {
		t.Fatal(err)
	}
	index, err = loadSnapshotIndex(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !indexMatchesScan(index, all) {
		t.Fatal("the rebuilt index still does not match the scan")
	}
}

func TestVerifyReportsAMissingDataSubvolume(t *testing.T) {
	fakeBtrfs(t)
	workspace, _ := newSnapshotWorkspace(t)
	meta, err := createSnapshot(workspace, snapshotOptions{kind: snapshotKindManual, reason: snapshotReasonManual})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(snapshotDataPath(snapshotRoot(workspace, meta.ID))); err != nil {
		t.Fatal(err)
	}
	problems := verifySnapshot(workspace, *meta)
	if len(problems) != 1 || problems[0].Code != "subvolume_missing" {
		t.Fatalf("problems = %+v, want one subvolume_missing", problems)
	}
}

// The copy ladder must produce a usable tree whichever tier it lands on, and
// the hard-link tier must genuinely share the inode — that sharing is the
// entire reason deployment artifacts have to be sealed read-only first.
func TestDeploymentCopySharesTheInodeWhenItHardLinks(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "deployment")
	if err := os.MkdirAll(filepath.Join(src, "modules", "core"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(src, "modules", "core", "compose.yml")
	if err := os.WriteFile(file, []byte("services: {}\n"), 0444); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "copy")
	method, err := copyDeploymentTree(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dst, "modules", "core", "compose.yml")
	got, err := os.ReadFile(copied)
	if err != nil || string(got) != "services: {}\n" {
		t.Fatalf("copied file = %q, %v", got, err)
	}
	switch method {
	case "reflink", "copy":
		// Independent storage; nothing further to assert here.
	case "hardlink":
		a, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.Stat(copied)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(a, b) {
			t.Fatal("the hard-link tier did not actually share the inode")
		}
	default:
		t.Fatalf("unknown copy tier %q", method)
	}
}

func TestSnapshotIDRejectsPathTraversal(t *testing.T) {
	for _, id := range []string{"../outside", "a/b", "..", "", ".tmp-x"} {
		if err := validateSnapshotID(id); err == nil {
			t.Fatalf("snapshot id %q was accepted", id)
		}
	}
}
