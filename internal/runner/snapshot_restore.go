package runner

// Restoring is the only operation in anas that rewinds data.
//
// It is deliberately not the same thing as a rollback, and neither is a weaker
// version of the other. A rollback swaps the artifact and leaves the data
// alone, which is the right answer for the two most common cases by far — a
// mistyped domain or port that stops a service starting, and a patch-level
// regression — where the data is perfectly good and rewinding it would throw
// away every file, mail and database write since the last apply. A restore is
// the right answer when the data itself is what went wrong. Collapsing the two
// would leave the common case with no correct option.
//
// It is also all-or-nothing. The secret store is append-generational, so
// restoring an older one discards the generations created after it; that is
// self-consistent when the data goes back with it and incoherent when it does
// not, because the keys would then no longer match the data they encrypt or
// authenticate against. So there is no flag for restoring the metadata while
// keeping the current data.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anas-project/ANAS/internal/compose"
)

type restoreOutcome struct {
	Workspace          string   `json:"workspace"`
	RestoredFrom       string   `json:"restored_from"`
	PreRestoreSnapshot string   `json:"pre_restore_snapshot,omitempty"`
	Restored           []string `json:"restored"`
	DeploymentID       string   `json:"deployment_id"`
	NextSteps          []string `json:"next_steps"`
}

// restoreTargets lists what a restore replaces, in the order it replaces them.
func restoreTargets(workspace string, meta *snapshotMeta, restoreUserData bool) []string {
	base := stateDir(workspace)
	targets := []string{
		workspaceConfigPath(workspace),
		projectLockPath(workspaceConfigPath(workspace)),
		filepath.Join(base, "secrets.generated.yml"),
		localAdminStatePath(base),
		deploymentStatePath(base, meta.DeploymentID),
		activeStatePath(base),
		deploymentArtifactDir(base, meta.DeploymentID),
		dataDir(workspace),
	}
	// userdata appears here only when it is actually going to be replaced.
	// Listing it unconditionally would tell a user checking what a restore
	// touches that their files are at risk when they are not, and the whole
	// point of --dry-run is that its answer can be trusted.
	if restoreUserData {
		targets = append(targets, userDataDir(workspace))
	}
	return targets
}

// restoreSnapshot puts the workspace back to the moment the snapshot captured.
// The caller must already hold the exclusive runtime lock.
func restoreSnapshot(workspace string, meta *snapshotMeta, restoreUserData, jsonMode bool) (*restoreOutcome, error) {
	base := stateDir(workspace)
	root := snapshotRoot(workspace, meta.ID)
	if !meta.Complete {
		return nil, preconditionErrorf("snapshot_incomplete",
			"snapshot %s never finished being written and cannot be restored", meta.ID)
	}
	if problems := verifySnapshot(workspace, *meta); len(problems) > 0 {
		return nil, preconditionErrorf(problems[0].Code,
			"snapshot %s is not restorable: %s", meta.ID, problems[0].Message)
	}
	source := dataDir(workspace)
	if err := btrfsSubvolumeShow(source); err != nil {
		return nil, preconditionErrorf("not_btrfs",
			"%s is not a Btrfs subvolume, so it cannot be replaced by one: %v", source, err)
	}

	// Stopping first: the containers hold open file descriptors into the data
	// directory that is about to be renamed out from under them, and a service
	// that keeps writing during the swap writes into a directory nothing will
	// ever look at again.
	emitProgress(jsonMode, "stop-containers", 0, 0, "containers")
	if err := stopActiveDeployment(base); err != nil {
		return nil, failuref("stop_failed", "stop the running deployment before restoring: %v", err)
	}

	// The restore itself has to be undoable. Six paths cannot be swapped by a
	// single atomic rename, so "all or nothing" is guaranteed by having a
	// complete snapshot of the pre-restore state to go back to rather than by
	// pretending the sequence below is atomic.
	emitProgress(jsonMode, "pre-restore-snapshot", 0, 0, "snapshots")
	pre, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: snapshotReasonPreRestore,
		label: "before restoring " + meta.ID, json: jsonMode,
		// The undo has to cover whatever this restore is about to replace. A
		// pre-restore snapshot that omitted user content while the restore
		// went on to overwrite it would leave the one thing that cannot be
		// regenerated with no way back.
		includeUserData: restoreUserData,
	})
	if err != nil {
		return nil, err
	}

	restored := []string{}
	emitProgress(jsonMode, "restore-metadata", 0, 5, "files")
	if err := copyFileMode(snapshotMetaEntry(root, snapshotMetaConfigName), workspaceConfigPath(workspace), 0600); err != nil {
		return nil, failuref("restore_failed", "restore config.yml: %v", err)
	}
	restored = append(restored, "config")
	if err := copyFileMode(snapshotMetaEntry(root, snapshotMetaLockName), projectLockPath(workspaceConfigPath(workspace)), 0600); err != nil {
		return nil, failuref("restore_failed", "restore config.lock.yml: %v", err)
	}
	restored = append(restored, "lock")
	if err := copyFileMode(snapshotMetaEntry(root, snapshotMetaSecretsName), filepath.Join(base, "secrets.generated.yml"), 0600); err != nil {
		return nil, failuref("restore_failed", "restore the secret store: %v", err)
	}
	restored = append(restored, "secrets")
	if err := copyFileMode(snapshotMetaEntry(root, snapshotMetaAdminsName), localAdminStatePath(base), 0600); err != nil {
		return nil, failuref("restore_failed", "restore local administrator state: %v", err)
	}
	restored = append(restored, "local_admins")

	emitProgress(jsonMode, "restore-deployment", 0, 0, "bytes")
	if err := restoreDeploymentArtifact(root, base, meta.DeploymentID); err != nil {
		return nil, failuref("restore_failed", "restore deployment %s: %v", meta.DeploymentID, err)
	}
	restored = append(restored, "deployment")

	if err := copyFileMode(snapshotMetaEntry(root, snapshotMetaStateName), deploymentStatePath(base, meta.DeploymentID), 0600); err != nil {
		return nil, failuref("restore_failed", "restore deployment state: %v", err)
	}
	// active.yml is regenerated rather than copied. The snapshot's own
	// deployment_id already says which deployment was active; a copied
	// active.yml would additionally assert a previous_deployments history
	// pointing at deployments this snapshot does not contain.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := saveActiveState(base, &activeDeploymentState{
		APIVersion: activeStateVersion, ActiveDeployment: meta.DeploymentID,
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
	if err := restoreDataSubvolume(snapshotDataPath(root), source, meta.ID); err != nil {
		return nil, failuref("restore_failed", "%s", err.Error())
	}
	restored = append(restored, "data")

	// User content is restored only when it was asked for. A restore exists to
	// put the deployment back; the files people saved in the meantime are not
	// part of the deployment, and rewinding them is a deletion nobody
	// requested. The caller has already established consent by this point.
	if restoreUserData {
		emitProgress(jsonMode, "restore-userdata", 0, 0, "bytes")
		if err := restoreDataSubvolume(snapshotUserDataPath(root), userDataDir(workspace), meta.ID); err != nil {
			return nil, failuref("restore_failed", "restore user data: %s", err.Error())
		}
		restored = append(restored, "userdata")
	}

	if err := rebuildSnapshotIndex(workspace); err != nil {
		return nil, err
	}
	return &restoreOutcome{
		Workspace: workspace, RestoredFrom: meta.ID, PreRestoreSnapshot: pre.ID,
		Restored: restored, DeploymentID: meta.DeploymentID,
		// Restoring deliberately does not start anything. The user may want to
		// look at what came back before putting it back into service.
		NextSteps: []string{"anas start -w " + workspace},
	}, nil
}

// restoreDeploymentArtifact puts the snapshot's copy of the deployment back
// under .anas/deployments/. An artifact already present is left alone: it is
// sealed read-only and identified by an id that is never reused, so the one on
// disk is the same one by construction.
func restoreDeploymentArtifact(snapshot, base, id string) error {
	target := deploymentArtifactDir(base, id)
	if exists(target) {
		return nil
	}
	staged := target + ".restoring"
	if err := os.RemoveAll(staged); err != nil {
		return err
	}
	if _, err := copyDeploymentTree(snapshotArtifactDir(snapshot), staged); err != nil {
		return err
	}
	if err := fsyncTree(staged); err != nil {
		return err
	}
	return os.Rename(staged, target)
}

// restoreDataSubvolume replaces the live data with a writable copy of the
// snapshot's. The current data is moved aside first so a failure mid-way can
// put it straight back; on success it is deleted, because the pre-restore
// snapshot taken moments earlier already holds exactly those extents and a
// second copy would only be an unnamed directory nobody knows the meaning of.
func restoreDataSubvolume(snapshotData, source, snapshotID string) error {
	aside := source + ".restoring-" + snapshotID
	if exists(aside) {
		return fmt.Errorf("%s already exists; remove it and retry", aside)
	}
	if err := os.Rename(source, aside); err != nil {
		return fmt.Errorf("move the current data aside: %w", err)
	}
	if err := runBtrfs("subvolume", "snapshot", snapshotData, source); err != nil {
		_ = os.RemoveAll(source)
		if renameErr := os.Rename(aside, source); renameErr != nil {
			return fmt.Errorf("restore data snapshot: %v; and the original data is left at %s: %v", err, aside, renameErr)
		}
		return fmt.Errorf("restore data snapshot: %w", err)
	}
	if err := runBtrfs("subvolume", "delete", aside); err != nil {
		// Not fatal: the restore succeeded, and a subvolume left behind is a
		// disk-space problem the user can see and fix, whereas failing here
		// would report a completed restore as a failure. On a filesystem
		// without user_subvol_rm_allowed this is the expected outcome, so the
		// message has to say so rather than print a bare EPERM.
		fmt.Fprintf(os.Stderr, "warning: the data replaced by this restore is still at %s: %v\n",
			aside, describeSubvolumeDeleteFailure(aside, err))
	}
	return nil
}

func stopActiveDeployment(base string) error {
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	if active.ActiveDeployment == "" {
		return nil
	}
	cli, err := compose.Detect()
	if err != nil {
		return err
	}
	app, root, _, err := loadDeploymentApp(base, active.ActiveDeployment, cli)
	if err != nil {
		return err
	}
	return app.stopRelease(root, false)
}
