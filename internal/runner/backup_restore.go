package runner

// Putting a backup back.
//
// Every mode lands the same shape at the destination, so restore has one job
// before it starts: get a snapshot-shaped directory it can read — snapshot.yml,
// meta/, deployment/, data/ — and then install it. The modes differ only in how
// that directory is produced, and the difference is confined to
// materializeBackup.
//
// Restoring is all or nothing, for the same reason a snapshot restore is. The
// secret store goes back with the data it belongs to; restoring the metadata
// while keeping the current data would leave keys that no longer match what
// they authenticate against, so there is no flag for it.
//
// It never starts anything. The operator may well want to look at what came
// back before putting it into service, and a restore that starts services is a
// restore that cannot be inspected.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type backupRestoreOutcome struct {
	Workspace    string           `json:"workspace"`
	BackupID     string           `json:"backup_id"`
	Mode         string           `json:"mode"`
	Restored     []string         `json:"restored"`
	Verify       backupVerifyPart `json:"verify"`
	DeploymentID string           `json:"deployment_id"`
	NextSteps    []string         `json:"next_steps"`
}

type backupVerifyPart struct {
	OK       bool              `json:"ok"`
	Checked  int               `json:"checked"`
	Problems []snapshotProblem `json:"problems"`
}

// materialized is a snapshot-shaped directory the installer can read, plus
// however much cleanup producing it required.
type materialized struct {
	root    string
	cleanup func()
}

// selectBackup resolves --backup-id, or picks the newest complete backup when
// none was named. An incomplete backup is never selected implicitly: it is the
// debris of an interrupted transfer, and restoring from it would produce a
// workspace that looks restored.
func selectBackup(dest, id string) (*backupManifest, []backupManifest, error) {
	all, err := listBackups(dest)
	if err != nil {
		return nil, nil, err
	}
	if id != "" {
		for _, manifest := range all {
			if manifest.BackupID == id {
				if !manifest.Complete {
					return nil, nil, preconditionErrorf("incomplete_backup",
						"backup %s was interrupted and cannot be restored", id)
				}
				return &manifest, all, nil
			}
		}
		return nil, nil, preconditionErrorf("backup_missing", "no backup %s in %s", id, dest)
	}
	for i := range all {
		if all[i].Complete {
			return &all[i], all, nil
		}
	}
	return nil, nil, preconditionErrorf("backup_missing", "no complete backup in %s", dest)
}

// restoreTargetsFor lists what a restore replaces, for --dry-run.
func backupRestoreTargets(workspace string, manifest *backupManifest) []string {
	base := stateDir(workspace)
	return []string{
		workspaceConfigPath(workspace),
		managedConfigStatePath(base),
		projectLockPath(workspaceConfigPath(workspace)),
		filepath.Join(base, "secrets.yml"),
		localAdminStatePath(base),
		deploymentStatePath(base, manifest.DeploymentID),
		activeStatePath(base),
		deploymentArtifactDir(base, manifest.DeploymentID),
		dataDir(workspace),
	}
}

// restoreBackup installs a backup into an existing workspace. The caller must
// already hold the exclusive lock.
func restoreBackup(workspace, dest string, manifest *backupManifest, all []backupManifest, jsonMode bool) (*backupRestoreOutcome, error) {
	base := stateDir(workspace)
	emitProgress(jsonMode, "materialize", 0, 0, "bytes")
	source, err := materializeBackup(workspace, dest, manifest, all, jsonMode)
	if err != nil {
		return nil, err
	}
	if source.cleanup != nil {
		defer source.cleanup()
	}

	// The materialised tree is checked before anything on the workspace is
	// touched. A backup that turns out to be missing its metadata channel has
	// to be discovered now, not after the data has been replaced.
	problems := verifyMaterialized(source.root)
	if len(problems) > 0 {
		return nil, preconditionErrorf(problems[0].Code,
			"backup %s is not restorable: %s", manifest.BackupID, problems[0].Message)
	}

	restored := []string{}
	emitProgress(jsonMode, "restore-metadata", 0, 5, "files")
	metaDir := filepath.Join(source.root, "meta")
	if err := copyFileMode(filepath.Join(metaDir, snapshotMetaConfigName), workspaceConfigPath(workspace), 0600); err != nil {
		return nil, failuref("restore_failed", "restore config.yml: %v", err)
	}
	restored = append(restored, "config")
	if err := copyFileMode(filepath.Join(metaDir, snapshotMetaConfigStateName), managedConfigStatePath(base), 0600); err != nil {
		return nil, failuref("restore_failed", "restore managed config state: %v", err)
	}
	if err := copyFileMode(filepath.Join(metaDir, snapshotMetaLockName), projectLockPath(workspaceConfigPath(workspace)), 0600); err != nil {
		return nil, failuref("restore_failed", "restore config.lock.yml: %v", err)
	}
	restored = append(restored, "lock")
	if err := copyFileMode(filepath.Join(metaDir, snapshotMetaSecretsName), filepath.Join(base, "secrets.yml"), 0600); err != nil {
		return nil, failuref("restore_failed", "restore the secret store: %v", err)
	}
	restored = append(restored, "secrets")
	if err := copyFileMode(filepath.Join(metaDir, snapshotMetaAdminsName), localAdminStatePath(base), 0600); err != nil {
		return nil, failuref("restore_failed", "restore local administrator state: %v", err)
	}
	restored = append(restored, "local_admins")

	emitProgress(jsonMode, "restore-deployment", 0, 0, "bytes")
	if err := restoreBackupArtifact(source.root, base, manifest.DeploymentID); err != nil {
		return nil, failuref("restore_failed", "restore deployment %s: %v", manifest.DeploymentID, err)
	}
	restored = append(restored, "active_deployment")

	if err := copyFileMode(filepath.Join(metaDir, snapshotMetaStateName), deploymentStatePath(base, manifest.DeploymentID), 0600); err != nil {
		return nil, failuref("restore_failed", "restore deployment state: %v", err)
	}
	// active.yml is rebuilt rather than carried. The backup's own deployment_id
	// already says which deployment was live; a copied active.yml would also
	// assert a previous_deployments history naming deployments this backup does
	// not contain.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := saveActiveState(base, &activeDeploymentState{
		APIVersion: activeStateVersion, ActiveDeployment: manifest.DeploymentID,
		RuntimeStatus: "stopped",
		ActivatedAt:   now, VerifiedAt: now,
	}); err != nil {
		return nil, failuref("restore_failed", "rewrite active.yml: %v", err)
	}
	if err := rebuildDeploymentIndex(base); err != nil {
		return nil, err
	}
	restored = append(restored, "state")

	emitProgress(jsonMode, "restore-data", 0, 0, "bytes")
	if err := installBackupData(filepath.Join(source.root, "data"), workspace, dataDir(workspace)); err != nil {
		return nil, failuref("restore_failed", "%s", err.Error())
	}
	restored = append(restored, "data")

	// Restoring a backup is not the same act as rolling a deployment back: the
	// workspace being restored into is usually empty or broken, and the files
	// are the reason the backup was taken. So user content is put back when the
	// backup carries it, rather than being held out the way a snapshot restore
	// holds it out.
	if userSource := filepath.Join(source.root, workspaceUserDataDir); exists(userSource) {
		emitProgress(jsonMode, "restore-userdata", 0, 0, "bytes")
		if err := installBackupData(userSource, workspace, userDataDir(workspace)); err != nil {
			return nil, failuref("restore_failed", "restore user data: %s", err.Error())
		}
		restored = append(restored, workspaceUserDataDir)
	}

	if err := rebuildSnapshotIndex(workspace); err != nil {
		return nil, err
	}
	verify := verifyRestoredWorkspace(workspace, manifest)
	return &backupRestoreOutcome{
		Workspace: workspace, BackupID: manifest.BackupID, Mode: manifest.Mode,
		Restored: restored, Verify: verify, DeploymentID: manifest.DeploymentID,
		NextSteps: []string{"anas start -w " + workspace},
	}, nil
}

// materializeBackup produces the snapshot-shaped directory to read from.
func materializeBackup(workspace, dest string, manifest *backupManifest, all []backupManifest, jsonMode bool) (*materialized, error) {
	root := backupRoot(dest, manifest.BackupID)
	switch manifest.Mode {
	case backupModeCopy, backupModeSnapshot:
		// Already the right shape, and readable in place. Nothing is copied
		// twice and nothing has to be cleaned up.
		return &materialized{root: root}, nil
	case backupModeSend:
		return materializeFromTree(workspace, root, manifest)
	case backupModeSendFile:
		return materializeFromStream(workspace, dest, manifest, all, jsonMode)
	}
	return nil, preconditionErrorf("metadata_unreadable", "backup %s has unknown mode %q", manifest.BackupID, manifest.Mode)
}

// materializeFromTree handles the `send` mode, whose data arrived as a real
// subvolume at the destination.
//
// It copies rather than sending the subvolume onward. Sending would preserve
// the copy-on-write sharing, but it needs CAP_SYS_ADMIN at both ends, and
// requiring privilege to *restore* would mean the mode that needs root to
// create also needs root to recover from — on a machine which, by the time
// anyone is restoring, may be a freshly installed one.
func materializeFromTree(workspace, root string, manifest *backupManifest) (*materialized, error) {
	staging := filepath.Join(stateDir(workspace), "staging", "backup-restore-"+manifest.BackupID)
	if err := os.RemoveAll(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := extractTar(backupMetaTarPath(root), staging); err != nil {
		cleanup()
		return nil, failuref("restore_failed", "read the metadata archive: %v", err)
	}
	if err := copyDirectory(backupDataPath(root), filepath.Join(staging, "data")); err != nil {
		cleanup()
		return nil, failuref("restore_failed", "copy the backed-up data: %v", err)
	}
	if exists(backupUserDataPath(root)) {
		if err := copyDirectory(backupUserDataPath(root), filepath.Join(staging, workspaceUserDataDir)); err != nil {
			cleanup()
			return nil, failuref("restore_failed", "copy the backed-up user data: %v", err)
		}
	}
	return &materialized{root: staging, cleanup: cleanup}, nil
}

// materializeFromStream handles `send-file`, receiving the whole incremental
// chain in order.
//
// The chain matters: an increment is a difference against its parent and means
// nothing on its own, so every ancestor back to a full backup has to be
// received first. Receiving them in the wrong order, or skipping one, produces
// a subvolume that btrfs will refuse rather than a subtly wrong one — but the
// refusal would come after the workspace had already been disturbed, so the
// chain is resolved up front.
func materializeFromStream(workspace, dest string, manifest *backupManifest, all []backupManifest, jsonMode bool) (*materialized, error) {
	if !hasSysAdmin() {
		return nil, preconditionErrorf(reasonInsufficientPrivilege,
			"restoring a send-file backup runs `btrfs receive`, which needs CAP_SYS_ADMIN; run this command as root")
	}
	if err := btrfsSubvolumeShow(dataDir(workspace)); err != nil {
		return nil, preconditionErrorf(reasonSourceNotBtrfs,
			"a send-file backup can only be restored onto Btrfs, and %s is not: %v", dataDir(workspace), err)
	}
	chain, err := backupChain(all, manifest.BackupID)
	if err != nil {
		return nil, err
	}
	staging := filepath.Join(snapshotsDir(workspace), snapshotTempPrefix+"restore-"+manifest.BackupID)
	if err := removeBackupTree(staging); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		return nil, err
	}
	cleanup := func() { _ = removeBackupTree(staging) }
	for i, link := range chain {
		emitProgress(jsonMode, "receive_stream", int64(i), int64(len(chain)), "streams")
		stream, err := os.Open(backupStreamPath(backupRoot(dest, link.BackupID)))
		if err != nil {
			cleanup()
			return nil, preconditionErrorf("stream_missing", "backup %s: %v", link.BackupID, err)
		}
		err = runBtrfsWithStdin(stream, "receive", staging)
		_ = stream.Close()
		if err != nil {
			cleanup()
			return nil, failuref("restore_failed", "receive backup %s: %v", link.BackupID, err)
		}
	}
	// The user-content stream is full rather than incremental, so only the
	// backup being restored carries one and there is no chain to walk.
	if userStream := backupUserStreamPath(backupRoot(dest, manifest.BackupID)); exists(userStream) {
		stream, err := os.Open(userStream)
		if err != nil {
			cleanup()
			return nil, preconditionErrorf("stream_missing", "backup %s user data: %v", manifest.BackupID, err)
		}
		err = runBtrfsWithStdin(stream, "receive", staging)
		_ = stream.Close()
		if err != nil {
			cleanup()
			return nil, failuref("restore_failed", "receive backup %s user data: %v", manifest.BackupID, err)
		}
	}
	if err := extractTar(backupMetaTarPath(backupRoot(dest, manifest.BackupID)), staging); err != nil {
		cleanup()
		return nil, failuref("restore_failed", "read the metadata archive: %v", err)
	}
	return &materialized{root: staging, cleanup: cleanup}, nil
}

// verifyMaterialized checks the tree before the workspace is touched.
func verifyMaterialized(root string) []snapshotProblem {
	problems := []snapshotProblem{}
	add := func(code, format string, args ...any) {
		problems = append(problems, snapshotProblem{Code: code, Message: fmt.Sprintf(format, args...)})
	}
	if !exists(filepath.Join(root, "data")) {
		add("subvolume_missing", "the backup's data is not present at %s", filepath.Join(root, "data"))
	}
	for _, name := range []string{snapshotMetaConfigName, snapshotMetaConfigStateName, snapshotMetaLockName, snapshotMetaSecretsName, snapshotMetaAdminsName, snapshotMetaStateName} {
		if !exists(filepath.Join(root, "meta", name)) {
			add("meta_incomplete", "meta/%s is missing", name)
		}
	}
	switch {
	case !exists(filepath.Join(root, "deployment")):
		add("deployment_incomplete", "deployment/ is missing")
	case !exists(filepath.Join(root, "deployment", "deployment.yml")):
		add("deployment_incomplete", "deployment/deployment.yml is missing")
	case !exists(filepath.Join(root, "deployment", deploymentConfigSourceName)):
		add("deployment_incomplete", "deployment/%s is missing", deploymentConfigSourceName)
	}
	return problems
}

// verifyRestoredWorkspace is the structural check the contract asks for after a
// restore. It answers the question the operator actually has, which is not "did
// the copy succeed" but "is what is on disk now something that could start".
func verifyRestoredWorkspace(workspace string, manifest *backupManifest) backupVerifyPart {
	base := stateDir(workspace)
	problems := []snapshotProblem{}
	add := func(code, format string, args ...any) {
		problems = append(problems, snapshotProblem{ID: manifest.BackupID, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	checked := 0
	for _, path := range []string{
		workspaceConfigPath(workspace),
		projectLockPath(workspaceConfigPath(workspace)),
		filepath.Join(base, "secrets.yml"),
		deploymentStatePath(base, manifest.DeploymentID),
		activeStatePath(base),
		dataDir(workspace),
	} {
		checked++
		if !exists(path) {
			add("meta_incomplete", "%s was not restored", path)
		}
	}
	checked++
	artifact := deploymentArtifactDir(base, manifest.DeploymentID)
	switch {
	case !exists(filepath.Join(artifact, "deployment.yml")):
		add("deployment_incomplete", "the restored deployment %s has no deployment.yml", manifest.DeploymentID)
	case !exists(deploymentConfigSourcePath(artifact)):
		add("deployment_incomplete", "the restored deployment %s has no %s", manifest.DeploymentID, deploymentConfigSourceName)
	}
	if active, err := loadActiveState(base); err != nil || active.ActiveDeployment != manifest.DeploymentID {
		add("metadata_unreadable", "active.yml does not name the restored deployment %s", manifest.DeploymentID)
	}
	return backupVerifyPart{OK: len(problems) == 0, Checked: checked, Problems: problems}
}

// restoreBackupArtifact puts the deployment artifact back. One already present
// is left alone: it is sealed read-only and its id is never reused, so the copy
// on disk is the same one by construction.
func restoreBackupArtifact(root, base, id string) error {
	target := deploymentArtifactDir(base, id)
	if exists(target) {
		return nil
	}
	staged := target + ".restoring"
	if err := os.RemoveAll(staged); err != nil {
		return err
	}
	if err := copyDirectory(filepath.Join(root, "deployment"), staged); err != nil {
		return err
	}
	return os.Rename(staged, target)
}

// installBackupData replaces one of the workspace's trees.
//
// The current tree is moved aside first so a failure part way through can put
// it straight back, and only removed once the replacement is in place. On Btrfs
// the replacement is created as a subvolume even when it has to be filled by
// copying, because a tree that is not a subvolume silently costs the workspace
// every future snapshot of it.
func installBackupData(source, workspace, target string) error {
	onBtrfs, _ := filesystemIsBtrfs(workspace)
	aside := ""
	if exists(target) {
		aside = target + ".restoring-backup"
		if exists(aside) {
			return fmt.Errorf("%s already exists; remove it and retry", aside)
		}
		if err := os.Rename(target, aside); err != nil {
			return fmt.Errorf("move the current data aside: %w", err)
		}
	}
	restore := func(cause error) error {
		_ = os.RemoveAll(target)
		if aside == "" {
			return cause
		}
		if err := os.Rename(aside, target); err != nil {
			return fmt.Errorf("%v; and the original data is left at %s: %v", cause, aside, err)
		}
		return cause
	}

	// A materialised subvolume on the same filesystem can simply be snapshotted
	// into place, which is instant and shares extents.
	if onBtrfs && btrfsSubvolumeShow(source) == nil && sameBtrfsFilesystem(filepath.Dir(source), workspace) {
		if err := runBtrfs("subvolume", "snapshot", source, target); err != nil {
			return restore(fmt.Errorf("snapshot the backup data into place: %w", err))
		}
	} else {
		if onBtrfs {
			if err := runBtrfs("subvolume", "create", target); err != nil {
				return restore(fmt.Errorf("create the data subvolume: %w", err))
			}
		} else if err := os.MkdirAll(target, 0755); err != nil {
			return restore(err)
		}
		if err := copyDirectory(source, target); err != nil {
			return restore(fmt.Errorf("copy the backed-up data into place: %w", err))
		}
	}
	if aside == "" {
		return nil
	}
	if err := discardReplacedData(aside); err != nil {
		// Not fatal. The restore succeeded, and a directory left behind is a
		// disk-space problem the operator can see and fix, whereas failing here
		// would report a completed restore as a failure.
		fmt.Fprintf(os.Stderr, "warning: the data replaced by this restore is still at %s: %v\n", aside, err)
	}
	return nil
}

func discardReplacedData(path string) error {
	if btrfsSubvolumeShow(path) == nil {
		if err := runBtrfs("subvolume", "delete", path); err != nil {
			return describeSubvolumeDeleteFailure(path, err)
		}
		return nil
	}
	return os.RemoveAll(path)
}

// workspaceLooksUsed reports whether restoring would overwrite something the
// user would miss. An empty freshly-initialised workspace is the expected
// target on a rebuilt machine and should not demand a confirmation.
func workspaceLooksUsed(workspace string) bool {
	if exists(workspaceConfigPath(workspace)) {
		return true
	}
	entries, err := os.ReadDir(dataDir(workspace))
	return err == nil && len(entries) > 0
}

func describeRestoreSummary(outcome *backupRestoreOutcome) string {
	return fmt.Sprintf("restored %s (%s)\nactive deployment: %s\nrestored: %s\n\nnext:\n  %s\n",
		outcome.BackupID, outcome.Mode, outcome.DeploymentID,
		strings.Join(outcome.Restored, ", "), strings.Join(outcome.NextSteps, "\n  "))
}
