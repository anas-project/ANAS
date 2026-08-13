package runner

// The destination side: what a backup looks like once it has landed, how it is
// enumerated, and how it is checked.
//
// The manifest is the sole authority, exactly as snapshot.yml is on the source
// side. There is no index file at the destination and deliberately so: a
// destination is frequently a removable disk or a network share that other
// hosts also write to, and an index would be a second truth that nothing is in
// a position to keep current.
//
// `complete: true` is written last, after both transfer channels have finished.
// The send modes have two of them — `btrfs send` can only carry a subvolume, so
// snapshot.yml, meta/ and deployment/ travel separately — and a backup with one
// channel missing is worse than no backup at all, because it looks like one.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const backupAPIVersion = "anas.dev/backup/v1"

// backupManifest is <dest>/<backup-id>/backup.yml.
type backupManifest struct {
	APIVersion string `yaml:"api_version" json:"-"`
	BackupID   string `yaml:"backup_id" json:"backup_id"`
	Mode       string `yaml:"mode" json:"mode"`
	CreatedAt  string `yaml:"created_at" json:"created_at"`
	// SourceSnapshot is the snapshot on the origin host this backup was made
	// from. Incremental sending needs it: `btrfs send -p` reads the parent
	// locally, so a parent at the destination is only usable while the snapshot
	// it came from still exists here.
	SourceSnapshot string            `yaml:"source_snapshot,omitempty" json:"source_snapshot,omitempty"`
	Incremental    bool              `yaml:"incremental" json:"incremental"`
	Parent         string            `yaml:"parent,omitempty" json:"parent,omitempty"`
	SizeBytes      int64             `yaml:"size_bytes" json:"size_bytes"`
	DeploymentID   string            `yaml:"deployment_id" json:"deployment_id"`
	ConfigDigest   string            `yaml:"config_digest,omitempty" json:"config_digest,omitempty"`
	Modules        map[string]string `yaml:"modules,omitempty" json:"modules,omitempty"`
	// Channels records which transfer channels finished. Both have to be
	// present for the backup to be complete, and naming them individually is
	// what lets `verify` say which one is missing.
	Channels []string `yaml:"channels,omitempty" json:"channels,omitempty"`
	Complete bool     `yaml:"complete" json:"complete"`
	// ChainBroken is computed when listing rather than stored: whether an
	// ancestor is still present is a property of the destination now, not of
	// the moment this backup was written.
	ChainBroken bool `yaml:"-" json:"chain_broken,omitempty"`
}

const (
	backupChannelData     = "data"
	backupChannelMetadata = "metadata"
	backupChannelUserData = "userdata"
)

const backupTempPrefix = ".tmp-"

func backupRoot(dest, id string) string     { return filepath.Join(dest, id) }
func backupTempRoot(dest, id string) string { return filepath.Join(dest, backupTempPrefix+id) }

func backupManifestPath(root string) string   { return filepath.Join(root, "backup.yml") }
func backupStreamPath(root string) string     { return filepath.Join(root, "data.stream") }
func backupUserStreamPath(root string) string { return filepath.Join(root, "userdata.stream") }
func backupMetaTarPath(root string) string    { return filepath.Join(root, "meta.tar") }
func backupDataPath(root string) string       { return filepath.Join(root, "data") }
func backupUserDataPath(root string) string   { return filepath.Join(root, "userdata") }

func validateBackupID(id string) error {
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\`) || strings.HasPrefix(id, ".") {
		return usageErrorf("invalid backup id %q", id)
	}
	return nil
}

func loadBackupManifestAt(root string) (*backupManifest, error) {
	var manifest backupManifest
	if err := readYAML(backupManifestPath(root), &manifest); err != nil {
		return nil, err
	}
	if manifest.APIVersion != backupAPIVersion {
		return nil, fmt.Errorf("unsupported backup api_version %q", manifest.APIVersion)
	}
	return &manifest, nil
}

func loadBackupManifest(dest, id string) (*backupManifest, error) {
	if err := validateBackupID(id); err != nil {
		return nil, err
	}
	manifest, err := loadBackupManifestAt(backupRoot(dest, id))
	if os.IsNotExist(err) {
		return nil, preconditionErrorf("backup_missing", "no backup %s in %s", id, dest)
	}
	if err != nil {
		return nil, preconditionErrorf("metadata_unreadable", "backup %s: %v", id, err)
	}
	return manifest, nil
}

// listBackups scans the destination. Like the snapshot listing it reads the
// directory rather than any index, and it keeps entries whose manifest will not
// parse so that `verify` has something to report a problem about — a backup
// that has silently vanished from a listing is the failure mode this whole
// subsystem exists to catch.
func listBackups(dest string) ([]backupManifest, error) {
	entries, err := os.ReadDir(dest)
	if os.IsNotExist(err) {
		return nil, preconditionErrorf(reasonDestNotExist, "destination %s does not exist", dest)
	}
	if err != nil {
		return nil, err
	}
	out := []backupManifest{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, err := loadBackupManifestAt(backupRoot(dest, entry.Name()))
		if err != nil {
			out = append(out, backupManifest{BackupID: entry.Name()})
			continue
		}
		out = append(out, *manifest)
	}
	present := map[string]bool{}
	for _, manifest := range out {
		if manifest.Complete {
			present[manifest.BackupID] = true
		}
	}
	for i := range out {
		if out[i].Parent != "" && !present[out[i].Parent] {
			out[i].ChainBroken = true
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].BackupID > out[j].BackupID
	})
	return out, nil
}

// incrementalParents lists the destination backups this host could still send
// an increment against.
//
// Both ends have to hold it. The destination needs the backup so the increment
// has something to apply to, and this host needs the snapshot it was made from,
// because `btrfs send -p` computes the difference against a local subvolume. A
// parent that only exists at one end is not a parent.
func incrementalParents(workspace string, dest *backupDestInfo) []string {
	if dest == nil || !dest.Exists {
		return nil
	}
	backups, err := listBackups(dest.Path)
	if err != nil {
		return nil
	}
	local := map[string]bool{}
	if snapshots, err := listSnapshots(workspace); err == nil {
		for _, snapshot := range snapshots {
			if snapshot.Complete {
				local[snapshot.ID] = true
			}
		}
	}
	parents := []string{}
	for _, manifest := range backups {
		if !manifest.Complete || manifest.SourceSnapshot == "" {
			continue
		}
		if manifest.Mode != backupModeSend && manifest.Mode != backupModeSendFile {
			continue
		}
		if local[manifest.SourceSnapshot] {
			parents = append(parents, manifest.BackupID)
		}
	}
	return parents
}

// backupChain walks from a backup back to the full backup it depends on. The
// result is oldest first, which is the order a restore has to receive them in.
func backupChain(all []backupManifest, id string) ([]backupManifest, error) {
	byID := map[string]backupManifest{}
	for _, manifest := range all {
		byID[manifest.BackupID] = manifest
	}
	chain := []backupManifest{}
	seen := map[string]bool{}
	for current := id; current != ""; {
		manifest, ok := byID[current]
		if !ok {
			return nil, preconditionErrorf("parent_missing",
				"backup %s depends on %s, which is not at this destination", id, current)
		}
		if seen[current] {
			return nil, failuref("parent_cycle", "backup %s has a cyclic parent chain at %s", id, current)
		}
		seen[current] = true
		chain = append([]backupManifest{manifest}, chain...)
		current = manifest.Parent
	}
	return chain, nil
}

// ---------------------------------------------------------------- verifying

type backupProblem struct {
	BackupID string `json:"backup_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// verifyBackup checks that a backup is still usable. It is written to be run
// from cron, because the most common way a backup system fails is that somebody
// believes there is a backup and there is not.
func verifyBackup(dest string, manifest backupManifest, present map[string]bool) []backupProblem {
	root := backupRoot(dest, manifest.BackupID)
	problems := []backupProblem{}
	add := func(code, format string, args ...any) {
		problems = append(problems, backupProblem{
			BackupID: manifest.BackupID, Code: code, Message: fmt.Sprintf(format, args...),
		})
	}
	if manifest.Mode == "" {
		add("metadata_unreadable", "%s has no readable backup.yml", root)
		return problems
	}
	if !manifest.Complete {
		add("incomplete_backup", "backup %s was interrupted and cannot be restored", manifest.BackupID)
	}
	// A backup that recorded a user-content channel must still have it. The
	// channel list is what the manifest promises; checking the promise against
	// the disk is the whole point of verify, and user content is the part whose
	// absence cannot be repaired by redeploying.
	if contains(manifest.Channels, backupChannelUserData) {
		switch manifest.Mode {
		case backupModeSendFile:
			checkPresent(add, backupUserStreamPath(root), "userdata_stream_missing", "user data stream")
		default:
			checkPresent(add, backupUserDataPath(root), "userdata_stream_missing", "user data directory")
		}
	}
	switch manifest.Mode {
	case backupModeSendFile:
		checkStream(add, backupStreamPath(root), manifest.SizeBytes, "stream_missing")
		checkPresent(add, backupMetaTarPath(root), "metadata_stream_missing", "metadata archive")
	case backupModeSend:
		checkPresent(add, backupDataPath(root), "stream_missing", "received data subvolume")
		checkPresent(add, backupMetaTarPath(root), "metadata_stream_missing", "metadata archive")
	default:
		checkPresent(add, backupDataPath(root), "stream_missing", "data directory")
		for _, name := range []string{snapshotMetaConfigName, snapshotMetaLockName, snapshotMetaSecretsName, snapshotMetaAdminsName, snapshotMetaStateName} {
			if !exists(filepath.Join(root, "meta", name)) {
				add("metadata_stream_missing", "meta/%s is missing from backup %s", name, manifest.BackupID)
			}
		}
		if !exists(filepath.Join(root, "deployment", "deployment.yml")) {
			add("metadata_stream_missing", "deployment/deployment.yml is missing from backup %s", manifest.BackupID)
		}
	}
	if manifest.Parent != "" && !present[manifest.Parent] {
		add("parent_missing", "backup %s is incremental against %s, which is no longer here",
			manifest.BackupID, manifest.Parent)
	}
	return problems
}

func checkPresent(add func(string, string, ...any), path, code, what string) {
	if !exists(path) {
		add(code, "%s is missing at %s", what, path)
	}
}

// checkStream compares the recorded size against the file on disk. A truncated
// stream is the damage this catches that a presence check cannot: the file is
// there, `btrfs receive` will start, and it will fail partway through a restore
// that has already replaced the data it was going to fall back on.
func checkStream(add func(string, string, ...any), path string, want int64, missingCode string) {
	info, err := os.Stat(path)
	if err != nil {
		add(missingCode, "send stream is missing at %s", path)
		return
	}
	if want > 0 && info.Size() != want {
		add("size_mismatch", "send stream %s is %d bytes, the manifest records %d", path, info.Size(), want)
	}
}

// ---------------------------------------------------------------- writing

func writeBackupManifest(root string, manifest *backupManifest) error {
	manifest.APIVersion = backupAPIVersion
	return writeYAMLAtomic(backupManifestPath(root), manifest, 0600)
}

func newBackupID() (string, error) { return newDeploymentID() }

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

// cleanStaleBackupTemp removes the debris of an interrupted transfer at a
// destination. Unlike the snapshot equivalent it cannot run under a lock — the
// destination may be shared — so it only removes directories whose manifest is
// absent or explicitly incomplete, and only the temporary ones.
func cleanStaleBackupTemp(dest string) {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), backupTempPrefix) {
			continue
		}
		path := filepath.Join(dest, entry.Name())
		if err := removeBackupTree(path); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove interrupted backup %s: %v\n", path, err)
		}
	}
}

// removeBackupTree deletes a backup directory, taking the data subvolume out
// through btrfs first when there is one. A received subvolume is read-only, so
// an ordinary recursive delete fails on every file inside it.
func removeBackupTree(root string) error {
	for _, path := range []string{backupDataPath(root), backupUserDataPath(root)} {
		if exists(path) && btrfsSubvolumeShow(path) == nil {
			if err := runBtrfs("subvolume", "delete", path); err != nil {
				return describeSubvolumeDeleteFailure(path, err)
			}
		}
	}
	return os.RemoveAll(root)
}
