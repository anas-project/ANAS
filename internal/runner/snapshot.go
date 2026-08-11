package runner

// A snapshot is a point in time, not a step in a transition.
//
// The previous data snapshot was bound to one specific rollback: it recorded
// from_deployment and to_deployment and refused to restore unless the rollback
// being attempted matched that exact pair. That made sense while a snapshot was
// nothing but a side effect of an upgrade. It stops making sense the moment a
// snapshot is self-sufficient, because then restoring it means "put the
// workspace back the way it was at that moment" and there is nothing to pair it
// against.
//
// Self-sufficient is the load-bearing property. A snapshot carries the config,
// the lock, the secret store, the one piece of state that cannot be rebuilt, a
// full copy of the deployment artifact, and a read-only copy of the data. Once
// it has been sent to another disk there is no .anas over there to consult, so
// every one of those has to be a real copy rather than a reference. The only
// thing it cannot carry is the upstream base images, which still come from a
// registry.
//
//	<workspace>/snapshots/
//	  .tmp-<id>/          being built; renamed into place when finished
//	  <id>/
//	    snapshot.yml      metadata, 0600, complete written last
//	    meta/
//	      config.yml              the deployment's config.source.yml, 0600
//	      config.lock.yml         the deployment's resolution lock
//	      secrets.generated.yml   the secret store at that moment, 0600
//	      deployment-state.yml    state/deployments/<id>.yml
//	    deployment/       a full copy of .anas/deployments/<id>/
//	    data/             a read-only Btrfs snapshot of <workspace>/data

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/config"
)

const (
	snapshotAPIVersion = "anas.dev/snapshot/v1"
	// snapshotDefaultKeepAuto is how many automatic snapshots survive a prune.
	// Manual and pinned snapshots neither count towards it nor are collected by
	// it, so a user who pins something never has it silently aged out.
	snapshotDefaultKeepAuto = 5
	snapshotTempPrefix      = ".tmp-"
)

const (
	snapshotKindAuto   = "auto"
	snapshotKindManual = "manual"
)

// The reason enumeration. cask_upgrade_breaking and setting_data_migrate are
// declared here but only produced once the breaking-upgrade detection lands;
// pre_apply is what the existing apply-time trigger records until then.
const (
	snapshotReasonManual              = "manual"
	snapshotReasonPreApply            = "pre_apply"
	snapshotReasonPreRestore          = "pre_restore"
	snapshotReasonPreBackup           = "pre_backup"
	snapshotReasonCaskUpgradeBreaking = "cask_upgrade_breaking"
	snapshotReasonSettingDataMigrate  = "setting_data_migrate"
)

func validSnapshotReason(reason string) bool {
	switch reason {
	case snapshotReasonManual, snapshotReasonPreApply, snapshotReasonPreRestore,
		snapshotReasonPreBackup, snapshotReasonCaskUpgradeBreaking, snapshotReasonSettingDataMigrate:
		return true
	}
	return false
}

// snapshotMeta is the sole authority for a snapshot. .anas/state/snapshots.yml
// is a derived index that a full scan of snapshots/*/snapshot.yml can rebuild,
// so there is deliberately no second authoritative direction to drift out of
// sync with.
type snapshotMeta struct {
	APIVersion string `yaml:"api_version" json:"api_version"`
	ID         string `yaml:"id" json:"id"`
	Backend    string `yaml:"backend" json:"backend"`
	Kind       string `yaml:"kind" json:"kind"`
	Pinned     bool   `yaml:"pinned" json:"pinned"`
	CreatedAt  string `yaml:"created_at" json:"created_at"`
	Reason     string `yaml:"reason" json:"reason"`
	Label      string `yaml:"label" json:"label"`
	Source     string `yaml:"source" json:"source"`
	Path       string `yaml:"path" json:"path"`
	// FromDeployment and ToDeployment are recorded when a snapshot happened to
	// be taken during a transition. They are context for a human reading the
	// history, never a precondition for restoring it.
	FromDeployment string            `yaml:"from_deployment,omitempty" json:"from_deployment,omitempty"`
	ToDeployment   string            `yaml:"to_deployment,omitempty" json:"to_deployment,omitempty"`
	DeploymentID   string            `yaml:"deployment_id" json:"deployment_id"`
	ConfigDigest   string            `yaml:"config_digest" json:"config_digest"`
	LockDigest     string            `yaml:"lock_digest" json:"lock_digest"`
	Casks          map[string]string `yaml:"casks,omitempty" json:"casks,omitempty"`
	// ArtifactCopy records which tier of the reflink -> hard link -> full copy
	// ladder actually produced deployment/, because the three have very
	// different disk costs and only the last is independent of the source.
	ArtifactCopy string `yaml:"artifact_copy,omitempty" json:"artifact_copy,omitempty"`
	// Complete is written last. Anything without it is the debris of an
	// interrupted create and must never be restored.
	Complete bool `yaml:"complete" json:"complete"`
}

const (
	snapshotMetaConfigName  = "config.yml"
	snapshotMetaLockName    = "config.lock.yml"
	snapshotMetaSecretsName = "secrets.generated.yml"
	snapshotMetaStateName   = "deployment-state.yml"
)

func snapshotRoot(workspace, id string) string {
	return filepath.Join(snapshotsDir(workspace), id)
}

func snapshotTempRoot(workspace, id string) string {
	return filepath.Join(snapshotsDir(workspace), snapshotTempPrefix+id)
}

func snapshotMetaFile(root string) string     { return filepath.Join(root, "snapshot.yml") }
func snapshotMetaDir(root string) string      { return filepath.Join(root, "meta") }
func snapshotArtifactDir(root string) string  { return filepath.Join(root, "deployment") }
func snapshotDataPath(root string) string     { return filepath.Join(root, "data") }
func snapshotIndexPath(base string) string    { return filepath.Join(base, "state", "snapshots.yml") }
func snapshotMetaEntry(root, n string) string { return filepath.Join(snapshotMetaDir(root), n) }
func deploymentArtifactDir(base, id string) string {
	return filepath.Join(base, "deployments", id)
}

func validateSnapshotID(id string) error {
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return usageErrorf("invalid snapshot id %q", id)
	}
	if strings.HasPrefix(id, ".") {
		return usageErrorf("invalid snapshot id %q", id)
	}
	return nil
}

// ---------------------------------------------------------------- reading

func loadSnapshotAt(root string) (*snapshotMeta, error) {
	var meta snapshotMeta
	if err := readYAML(snapshotMetaFile(root), &meta); err != nil {
		return nil, err
	}
	if meta.APIVersion != snapshotAPIVersion {
		return nil, fmt.Errorf("unsupported snapshot api_version %q", meta.APIVersion)
	}
	return &meta, nil
}

func loadSnapshot(workspace, id string) (*snapshotMeta, error) {
	if err := validateSnapshotID(id); err != nil {
		return nil, err
	}
	meta, err := loadSnapshotAt(snapshotRoot(workspace, id))
	if os.IsNotExist(err) {
		return nil, preconditionErrorf("snapshot_missing", "no snapshot %s in %s", id, snapshotsDir(workspace))
	}
	if err != nil {
		return nil, preconditionErrorf("metadata_unreadable", "snapshot %s: %v", id, err)
	}
	return meta, nil
}

// listSnapshots scans the directory rather than reading the index, because the
// index is derived and the scan is what would rebuild it.
func listSnapshots(workspace string) ([]snapshotMeta, error) {
	entries, err := os.ReadDir(snapshotsDir(workspace))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []snapshotMeta{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		meta, err := loadSnapshotAt(snapshotRoot(workspace, entry.Name()))
		if err != nil {
			// A snapshot whose metadata will not parse still exists on disk and
			// still occupies space, so it is listed with what is known rather
			// than dropped. `verify` is what reports why.
			out = append(out, snapshotMeta{ID: entry.Name(), Path: snapshotDataPath(snapshotRoot(workspace, entry.Name()))})
			continue
		}
		out = append(out, *meta)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// ---------------------------------------------------------------- creating

type snapshotOptions struct {
	kind   string
	reason string
	label  string
	pinned bool
	// from and to describe the transition a snapshot was taken during, when
	// there is one. Neither participates in restoring it.
	from string
	to   string
	json bool
}

// createSnapshot builds a snapshot in snapshots/.tmp-<id>/, flushes it, renames
// it into place, and only then writes complete: true. The caller must already
// hold the exclusive runtime lock.
func createSnapshot(workspace string, opts snapshotOptions) (*snapshotMeta, error) {
	base := stateDir(workspace)
	source := dataDir(workspace)
	if err := btrfsSubvolumeShow(source); err != nil {
		return nil, preconditionErrorf("not_btrfs",
			"%s is not a Btrfs subvolume, so no snapshot can be taken from it: %v", source, err)
	}
	active, err := loadActiveState(base)
	if err != nil {
		return nil, err
	}
	deploymentID := active.ActiveDeployment
	if deploymentID == "" {
		return nil, preconditionErrorf("no_active_deployment",
			"no active deployment; a snapshot carries the artifact it belongs to, so there is nothing to capture yet")
	}
	artifact := deploymentArtifactDir(base, deploymentID)
	configSource := deploymentConfigSourcePath(artifact)
	if !exists(configSource) {
		return nil, preconditionErrorf("config_source_missing",
			"deployment %s predates config.source.yml, so a snapshot of it could not be restored on its own; run `anas apply` once to re-render it", deploymentID)
	}

	id, err := newDeploymentID()
	if err != nil {
		return nil, err
	}
	final := snapshotRoot(workspace, id)
	tmp := snapshotTempRoot(workspace, id)
	if exists(final) || exists(tmp) {
		return nil, failuref("id_collision", "snapshot id collision %s", id)
	}
	if err := os.MkdirAll(snapshotsDir(workspace), 0700); err != nil {
		return nil, err
	}
	// The whole tree is 0700 because meta/config.yml and the secret store hold
	// plaintext keys.
	if err := os.MkdirAll(snapshotMetaDir(tmp), 0700); err != nil {
		return nil, err
	}
	cleanup := func() { _ = removeSnapshotTree(tmp) }

	emitProgress(opts.json, "copy-metadata", 0, 4, "files")
	if err := copyFileMode(configSource, snapshotMetaEntry(tmp, snapshotMetaConfigName), 0600); err != nil {
		cleanup()
		return nil, err
	}
	if err := copyFileMode(filepath.Join(artifact, "lock.yml"), snapshotMetaEntry(tmp, snapshotMetaLockName), 0600); err != nil {
		cleanup()
		return nil, err
	}
	if err := copySecretStore(base, snapshotMetaEntry(tmp, snapshotMetaSecretsName)); err != nil {
		cleanup()
		return nil, err
	}
	// Only state/deployments/<id>.yml is copied. active.yml is regenerated at
	// restore from the snapshot's own deployment_id: its previous_deployments
	// list names deployments the snapshot does not contain, so copying it would
	// carry dangling references onto another disk. index.yml is rebuildable by
	// definition, transactions/ are diagnostic, and lock is a runtime artifact.
	if err := copyDeploymentStateFile(base, deploymentID, snapshotMetaEntry(tmp, snapshotMetaStateName)); err != nil {
		cleanup()
		return nil, err
	}
	emitProgress(opts.json, "copy-metadata", 4, 4, "files")

	emitProgress(opts.json, "copy-deployment", 0, 0, "bytes")
	method, err := copyDeploymentTree(artifact, snapshotArtifactDir(tmp))
	if err != nil {
		cleanup()
		return nil, failuref("deployment_copy_failed", "copy deployment %s into the snapshot: %v", deploymentID, err)
	}

	emitProgress(opts.json, "snapshot-data", 0, 0, "bytes")
	if err := runBtrfs("subvolume", "snapshot", "-r", source, snapshotDataPath(tmp)); err != nil {
		cleanup()
		return nil, failuref("data_snapshot_failed", "create Btrfs data snapshot: %v", err)
	}

	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		cleanup()
		return nil, err
	}
	casks := map[string]string{}
	for name, cask := range manifest.Casks {
		casks[name] = formatCaskRelease(cask.Version, cask.Revision)
	}
	configDigest, err := fileDigest(configSource)
	if err != nil {
		cleanup()
		return nil, err
	}
	lockDigest, err := fileDigest(filepath.Join(artifact, "lock.yml"))
	if err != nil {
		cleanup()
		return nil, err
	}
	meta := snapshotMeta{
		APIVersion: snapshotAPIVersion, ID: id, Backend: "btrfs",
		Kind: opts.kind, Pinned: opts.pinned,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Reason:    opts.reason, Label: opts.label,
		Source: source, Path: snapshotDataPath(final),
		FromDeployment: opts.from, ToDeployment: opts.to,
		DeploymentID: deploymentID, ConfigDigest: configDigest, LockDigest: lockDigest,
		Casks: casks, ArtifactCopy: method, Complete: false,
	}
	if err := writeYAMLAtomic(snapshotMetaFile(tmp), &meta, 0600); err != nil {
		cleanup()
		return nil, err
	}
	// Everything must be on disk before the rename, or a crash could leave a
	// directory that looks finished and is not.
	if err := fsyncTree(tmp); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Rename(tmp, final); err != nil {
		cleanup()
		return nil, err
	}
	meta.Complete = true
	if err := writeYAMLAtomic(snapshotMetaFile(final), &meta, 0600); err != nil {
		return nil, err
	}
	if err := rebuildSnapshotIndex(workspace); err != nil {
		return nil, err
	}
	return &meta, nil
}

func copySecretStore(base, dst string) error {
	src := filepath.Join(base, "secrets.generated.yml")
	if !exists(src) {
		// A deployment that generated no secrets still needs the file present,
		// so that restore has something definite to put back and verify can
		// treat a missing one as damage rather than as an ambiguity.
		return os.WriteFile(dst, []byte("{}\n"), 0600)
	}
	return copyFileMode(src, dst, 0600)
}

func copyDeploymentStateFile(base, id, dst string) error {
	src := deploymentStatePath(base, id)
	if !exists(src) {
		state := deploymentState{APIVersion: activeStateVersion, ID: id, Status: "active"}
		return writeYAMLAtomic(dst, &state, 0600)
	}
	return copyFileMode(src, dst, 0600)
}

// copyDeploymentTree copies a deployment artifact by the cheapest mechanism the
// filesystem supports, and reports which one it used.
//
// Copying the artifact at all is what removes a cross-subsystem invariant:
// deployment garbage collection no longer has to consult the snapshot index to
// avoid reclaiming something a snapshot points at, and pinning a snapshot no
// longer implies pinning a deployment.
//
// snapshots/ is an ordinary directory rather than a subvolume, so there is no
// subvolume boundary between it and .anas/ and therefore no EXDEV to rule out
// either of the cheap tiers. The hard-link tier is only sound because
// deployment artifacts are sealed read-only: a hard link shares the inode, so
// an in-place write to the deployment would otherwise rewrite the snapshot too.
func copyDeploymentTree(src, dst string) (string, error) {
	// --reflink=always rather than auto: auto silently degrades to a full copy,
	// which would make the recorded tier a lie.
	if err := exec.Command("cp", "-a", "--reflink=always", src, dst).Run(); err == nil {
		return "reflink", nil
	}
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := copyTree(src, dst, os.Link); err == nil {
		return "hardlink", nil
	}
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := copyTree(src, dst, func(from, to string) error {
		info, err := os.Lstat(from)
		if err != nil {
			return err
		}
		return copyFileMode(from, to, info.Mode().Perm())
	}); err != nil {
		return "", err
	}
	return "copy", nil
}

func copyTree(src, dst string, file func(from, to string) error) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		return file(path, target)
	})
}

// fsyncTree flushes every file and directory under root. os.Rename only orders
// the rename against the data if the data has already reached the disk.
func fsyncTree(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// The data subvolume is a read-only Btrfs snapshot; it is durable by
		// construction and its files cannot be opened for the sync anyway.
		if path == snapshotDataPath(root) {
			return filepath.SkipDir
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	})
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil)), nil
}

// ---------------------------------------------------------------- removing

// removeSnapshotTree deletes a snapshot directory. The data subvolume has to go
// first and through btrfs: a read-only snapshot's contents cannot be unlinked,
// so an ordinary recursive delete fails on every file in it.
func removeSnapshotTree(root string) error {
	data := snapshotDataPath(root)
	if exists(data) {
		if err := btrfsSubvolumeShow(data); err == nil {
			if err := runBtrfs("subvolume", "delete", data); err != nil {
				return describeSubvolumeDeleteFailure(data, err)
			}
		}
	}
	return os.RemoveAll(root)
}

// describeSubvolumeDeleteFailure turns the one failure mode users will actually
// hit into an explanation with a remedy.
//
// Creating and snapshotting subvolumes work unprivileged, so it is natural to
// assume deleting them does too. It does not: BTRFS_IOC_SNAP_DESTROY requires
// CAP_SYS_ADMIN unless the filesystem was mounted with user_subvol_rm_allowed,
// and on a filesystem without it every snapshot command that reclaims space —
// delete, prune, retention after apply, and clearing an interrupted create —
// fails with a bare EPERM that says nothing about why. Falling back to a
// recursive delete is not an option either: a read-only snapshot's files cannot
// be unlinked at all.
func describeSubvolumeDeleteFailure(path string, cause error) error {
	if !strings.Contains(cause.Error(), "Operation not permitted") {
		return fmt.Errorf("delete Btrfs subvolume %s: %w", path, cause)
	}
	return preconditionErrorf("subvolume_delete_denied",
		"cannot delete Btrfs subvolume %s: deleting a subvolume needs CAP_SYS_ADMIN unless the filesystem is mounted with user_subvol_rm_allowed. "+
			"Creating and snapshotting need neither, so snapshots can be taken here but not reclaimed. "+
			"Remount with -o remount,user_subvol_rm_allowed (and add it to fstab), or run the reclaiming commands as root: %v",
		path, cause)
}

// cleanStaleSnapshotTemp removes the debris of an interrupted create. It runs
// under the exclusive runtime lock, so it can never race a create in progress:
// the create holds that same lock for its whole duration.
func cleanStaleSnapshotTemp(workspace string) {
	entries, err := os.ReadDir(snapshotsDir(workspace))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), snapshotTempPrefix) {
			continue
		}
		path := filepath.Join(snapshotsDir(workspace), entry.Name())
		if err := removeSnapshotTree(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove interrupted snapshot %s: %v\n", path, err)
		}
	}
}

func deleteSnapshot(workspace, id string) error {
	if err := validateSnapshotID(id); err != nil {
		return err
	}
	root := snapshotRoot(workspace, id)
	if !exists(root) {
		return preconditionErrorf("snapshot_missing", "no snapshot %s in %s", id, snapshotsDir(workspace))
	}
	if err := removeSnapshotTree(root); err != nil {
		if _, ok := err.(*CLIError); ok {
			return err
		}
		return failuref("delete_failed", "%s", err.Error())
	}
	return rebuildSnapshotIndex(workspace)
}

// ---------------------------------------------------------------- retention

// snapshotsToPrune selects what an automatic collection would reclaim: the
// auto, unpinned snapshots beyond the newest keep. Manual and pinned snapshots
// are excluded from the count as well as from the collection, so pinning three
// snapshots does not quietly reduce how much automatic history survives.
func snapshotsToPrune(all []snapshotMeta, keep int) (collect []snapshotMeta, retained, pinned int) {
	if keep < 0 {
		keep = 0
	}
	for _, meta := range all {
		if meta.Pinned {
			pinned++
			continue
		}
		if meta.Kind != snapshotKindAuto {
			continue
		}
		if retained < keep {
			retained++
			continue
		}
		collect = append(collect, meta)
	}
	return collect, retained, pinned
}

// workspaceKeepAuto reads the retention setting from the workspace's own
// config. A config that cannot be loaded falls back to the default rather than
// failing the command: retention must keep working while the user is midway
// through editing the file that snapshots exist to protect them from.
func workspaceKeepAuto(workspace string) int {
	cfg, err := config.Load(workspaceConfigPath(workspace))
	if err != nil || cfg.Rollback.Snapshot.KeepAuto == nil {
		return snapshotDefaultKeepAuto
	}
	keep := *cfg.Rollback.Snapshot.KeepAuto
	if keep < 0 {
		return 0
	}
	return keep
}

// ---------------------------------------------------------------- index

type snapshotIndexEntry struct {
	ID           string `yaml:"id"`
	Kind         string `yaml:"kind"`
	Pinned       bool   `yaml:"pinned"`
	CreatedAt    string `yaml:"created_at"`
	Reason       string `yaml:"reason"`
	Label        string `yaml:"label,omitempty"`
	DeploymentID string `yaml:"deployment_id"`
	Complete     bool   `yaml:"complete"`
}

type snapshotIndex struct {
	APIVersion  string               `yaml:"api_version"`
	GeneratedAt string               `yaml:"generated_at"`
	Snapshots   []snapshotIndexEntry `yaml:"snapshots"`
}

func buildSnapshotIndex(all []snapshotMeta) snapshotIndex {
	index := snapshotIndex{
		APIVersion:  activeStateVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Snapshots:   []snapshotIndexEntry{},
	}
	for _, meta := range all {
		index.Snapshots = append(index.Snapshots, snapshotIndexEntry{
			ID: meta.ID, Kind: meta.Kind, Pinned: meta.Pinned, CreatedAt: meta.CreatedAt,
			Reason: meta.Reason, Label: meta.Label, DeploymentID: meta.DeploymentID,
			Complete: meta.Complete,
		})
	}
	return index
}

func rebuildSnapshotIndex(workspace string) error {
	all, err := listSnapshots(workspace)
	if err != nil {
		return err
	}
	index := buildSnapshotIndex(all)
	return writeYAMLAtomic(snapshotIndexPath(stateDir(workspace)), &index, 0600)
}

func loadSnapshotIndex(workspace string) (*snapshotIndex, error) {
	var index snapshotIndex
	err := readYAML(snapshotIndexPath(stateDir(workspace)), &index)
	if os.IsNotExist(err) {
		return &snapshotIndex{APIVersion: activeStateVersion, Snapshots: []snapshotIndexEntry{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if index.Snapshots == nil {
		index.Snapshots = []snapshotIndexEntry{}
	}
	return &index, nil
}

// indexMatchesScan compares the derived index against the authoritative scan.
// Only the fields the index claims to hold are compared; generated_at is
// deliberately excluded, since a differing timestamp is not staleness.
func indexMatchesScan(index *snapshotIndex, all []snapshotMeta) bool {
	want := buildSnapshotIndex(all)
	if len(index.Snapshots) != len(want.Snapshots) {
		return false
	}
	for i := range want.Snapshots {
		if index.Snapshots[i] != want.Snapshots[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- verifying

type snapshotProblem struct {
	ID      string `json:"id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// verifySnapshot checks that the metadata and what is actually on disk agree.
// It is written to be run from cron: metadata that survives while somebody
// manually `btrfs subvolume delete`s the data underneath it is otherwise
// invisible until the moment of a restore, which is the worst possible moment
// to discover it.
func verifySnapshot(workspace string, meta snapshotMeta) []snapshotProblem {
	root := snapshotRoot(workspace, meta.ID)
	problems := []snapshotProblem{}
	add := func(code, format string, args ...any) {
		problems = append(problems, snapshotProblem{ID: meta.ID, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	if meta.APIVersion != snapshotAPIVersion {
		add("metadata_unreadable", "%s has no readable snapshot.yml", root)
		return problems
	}
	if !meta.Complete {
		add("snapshot_incomplete", "%s is the debris of an interrupted create and cannot be restored", meta.ID)
	}
	data := snapshotDataPath(root)
	if !exists(data) {
		add("subvolume_missing", "data subvolume %s is gone", data)
	} else if err := btrfsSubvolumeShow(data); err != nil {
		add("subvolume_missing", "%s is no longer a Btrfs subvolume: %v", data, err)
	}
	for _, name := range []string{snapshotMetaConfigName, snapshotMetaLockName, snapshotMetaSecretsName, snapshotMetaStateName} {
		if !exists(snapshotMetaEntry(root, name)) {
			add("meta_incomplete", "meta/%s is missing from %s", name, meta.ID)
		}
	}
	artifact := snapshotArtifactDir(root)
	switch {
	case !exists(artifact):
		add("deployment_incomplete", "deployment/ is missing from %s", meta.ID)
	case !exists(filepath.Join(artifact, "deployment.yml")):
		add("deployment_incomplete", "deployment/deployment.yml is missing from %s", meta.ID)
	case !exists(deploymentConfigSourcePath(artifact)):
		add("deployment_incomplete", "deployment/%s is missing from %s", deploymentConfigSourceName, meta.ID)
	}
	return problems
}
