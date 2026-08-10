package runner

// Moving the bytes.
//
// Two rules shape every mode here.
//
// `btrfs send` only ships subvolumes. In a snapshot directory only data/ is
// one; snapshot.yml, meta/ and deployment/ are ordinary files sitting beside
// it, and send will not look at them. So the send modes are two transfers, not
// one, and the destination's completion marker is written only after both have
// finished. A backup missing its metadata channel would restore into a
// workspace with the data but no config, no lock and no secret store.
//
// Nothing here uses `rsync --one-file-system` or `find -xdev`. Those stop at
// subvolume boundaries, and data/ is a subvolume — so the flag that reads like
// "do not wander off this filesystem" in fact means "omit the only thing that
// matters". Exclusions are written out explicitly instead, which has the
// further advantage of being visible in the plan.

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// backupSource is the snapshot-shaped tree a transfer reads from.
//
// When the workspace is on Btrfs this is a real snapshot directory. When it is
// not, there is no such directory anywhere and the pieces are named
// individually out of the live workspace instead — which is the same list, just
// not yet gathered in one place.
type backupSource struct {
	// root is the snapshot directory, or "" when assembling from the workspace.
	root string
	// snapshotID is the snapshot this backup came from, or "" when there was
	// none. Incremental sends key off it.
	snapshotID   string
	deploymentID string
	configDigest string
	casks        map[string]string
	// dataPath is what gets sent or copied.
	dataPath string
	// userDataPath is the user-content tree, or "" when this backup carries
	// none -- because the workspace has none, because it could not be captured,
	// or because the operator asked for a deployment-only backup.
	//
	// It is a separate channel rather than part of dataPath because the two are
	// separate subvolumes with separate restore rules: a backup restore may put
	// application state back without putting files back over newer ones.
	userDataPath string
	// parts are the metadata files, as (source path, destination-relative path).
	parts []backupPart
	// synthesizedSnapshot is written verbatim as snapshot.yml when there is no
	// real snapshot to copy one from.
	synthesizedSnapshot *snapshotMeta
}

type backupPart struct {
	src string
	rel string
	dir bool
}

// snapshotBackupSource describes a backup taken from a real snapshot.
func snapshotBackupSource(workspace string, meta *snapshotMeta) *backupSource {
	root := snapshotRoot(workspace, meta.ID)
	return &backupSource{
		root: root, snapshotID: meta.ID, deploymentID: meta.DeploymentID,
		configDigest: meta.ConfigDigest, casks: meta.Casks,
		dataPath:     snapshotDataPath(root),
		userDataPath: capturedUserDataPath(root, meta),
		parts: []backupPart{
			{src: snapshotMetaFile(root), rel: "snapshot.yml"},
			{src: snapshotMetaDir(root), rel: "meta", dir: true},
			{src: snapshotArtifactDir(root), rel: "deployment", dir: true},
		},
	}
}

// existingUserDataPath returns the live user-content tree, or "" when the
// workspace has none -- a deployment with no file-serving cask never creates
// one, and an empty channel is the honest way to say so.
func existingUserDataPath(workspace string) string {
	path := userDataDir(workspace)
	if !exists(path) {
		return ""
	}
	return path
}

// capturedUserDataPath returns the snapshot's user-content tree, or "" when it
// holds none. The snapshot's own coverage record is the authority: a directory
// may exist beside it from an interrupted run, and shipping that would put
// files into a backup that its metadata says are not there.
func capturedUserDataPath(root string, meta *snapshotMeta) string {
	if !meta.capturedTree(snapshotTreeUserData) {
		return ""
	}
	return snapshotUserDataPath(root)
}

// workspaceBackupSource names the pieces of a live workspace that make up the
// same tree, for hosts where no snapshot can be taken.
//
// This is the contract's include/exclude table expressed as a list of things to
// copy rather than as a filter. The historical deployments and the caches are
// excluded by not being named, which is a much harder thing to get wrong than a
// pattern — and impossible to get wrong in the direction that silently drops
// the data.
func workspaceBackupSource(workspace string) (*backupSource, error) {
	base := stateDir(workspace)
	active, err := loadActiveState(base)
	if err != nil {
		return nil, err
	}
	deploymentID := active.ActiveDeployment
	if deploymentID == "" {
		return nil, preconditionErrorf("no_active_deployment",
			"no active deployment; a backup carries the artifact it belongs to, so there is nothing to capture yet")
	}
	artifact := deploymentArtifactDir(base, deploymentID)
	configSource := deploymentConfigSourcePath(artifact)
	if !exists(configSource) {
		return nil, preconditionErrorf("config_source_missing",
			"deployment %s predates config.source.yml, so a backup of it could not be restored on its own; run `anas apply` once to re-render it", deploymentID)
	}
	lockPath := filepath.Join(artifact, "lock.yml")
	configDigest, err := fileDigest(configSource)
	if err != nil {
		return nil, err
	}
	lockDigest, err := fileDigest(lockPath)
	if err != nil {
		return nil, err
	}
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		return nil, err
	}
	casks := map[string]string{}
	for name, cask := range manifest.Casks {
		casks[name] = cask.Version
	}
	id, err := newDeploymentID()
	if err != nil {
		return nil, err
	}
	source := &backupSource{
		deploymentID: deploymentID, configDigest: configDigest, casks: casks,
		dataPath: dataDir(workspace),
		// Copy mode reads the live workspace, so whatever user content is there
		// is what gets copied. There is no snapshot to consult and nothing to
		// hold it still, which is the same caveat that already applies to data/
		// in this mode.
		userDataPath: existingUserDataPath(workspace),
		parts: []backupPart{
			{src: configSource, rel: filepath.Join("meta", snapshotMetaConfigName)},
			{src: lockPath, rel: filepath.Join("meta", snapshotMetaLockName)},
			{src: artifact, rel: "deployment", dir: true},
		},
		synthesizedSnapshot: &snapshotMeta{
			APIVersion: snapshotAPIVersion, ID: id, Backend: "none",
			Kind: snapshotKindAuto, CreatedAt: nowUTC(), Reason: snapshotReasonPreBackup,
			Source: dataDir(workspace), DeploymentID: deploymentID,
			ConfigDigest: configDigest, LockDigest: lockDigest, Casks: casks,
			ArtifactCopy: "copy", Complete: true,
		},
	}
	return source, nil
}

// writeSynthesizedMeta produces the two metadata files that have no single file
// on disk to copy: the secret store needs a definite placeholder when a
// deployment generated none, and deployment-state.yml has to exist even for a
// deployment that never recorded one.
func writeSynthesizedMeta(workspace, destRoot, deploymentID string) error {
	base := stateDir(workspace)
	metaDir := filepath.Join(destRoot, "meta")
	if err := os.MkdirAll(metaDir, 0700); err != nil {
		return err
	}
	if err := copySecretStore(base, filepath.Join(metaDir, snapshotMetaSecretsName)); err != nil {
		return err
	}
	return copyDeploymentStateFile(base, deploymentID, filepath.Join(metaDir, snapshotMetaStateName))
}

// ---------------------------------------------------------------- transfers

type transferRequest struct {
	source   *backupSource
	dest     string
	destRoot string
	mode     string
	parent   *backupManifest
	// parentSnapshotData is the local subvolume `btrfs send -p` computes the
	// difference against.
	parentSnapshotData string
	workspace          string
	json               bool
}

type transferResult struct {
	bytes    int64
	channels []string
}

// transferBackup writes one backup into destRoot, which is a temporary
// directory the caller renames into place only after the manifest is complete.
func transferBackup(req transferRequest) (*transferResult, error) {
	switch req.mode {
	case backupModeSnapshot:
		return transferBySnapshot(req)
	case backupModeSend:
		return transferBySend(req)
	case backupModeSendFile:
		return transferBySendFile(req)
	case backupModeCopy:
		return transferByCopy(req)
	}
	return nil, usageErrorf("unknown backup mode %q", req.mode)
}

// transferBySnapshot puts a second reference on the same filesystem. Cheap and
// instant, and no protection at all against the disk failing — which is why it
// is offered but never recommended.
func transferBySnapshot(req transferRequest) (*transferResult, error) {
	if err := writeMetadataChannel(req); err != nil {
		return nil, err
	}
	emitProgress(req.json, "snapshot_data", 0, 0, "bytes")
	if err := runBtrfs("subvolume", "snapshot", "-r", req.source.dataPath, backupDataPath(req.destRoot)); err != nil {
		return nil, failuref("data_transfer_failed", "snapshot the data into %s: %v", req.destRoot, err)
	}
	channels := []string{backupChannelData, backupChannelMetadata}
	bytes := treeSize(req.source.dataPath)
	if req.source.userDataPath != "" {
		emitProgress(req.json, "snapshot_userdata", 0, 0, "bytes")
		if err := runBtrfs("subvolume", "snapshot", "-r", req.source.userDataPath, backupUserDataPath(req.destRoot)); err != nil {
			return nil, failuref("userdata_transfer_failed", "snapshot the user data into %s: %v", req.destRoot, err)
		}
		channels = append(channels, backupChannelUserData)
		bytes += treeSize(req.source.userDataPath)
	}
	return &transferResult{bytes: bytes, channels: channels}, nil
}

// transferBySend pipes `btrfs send` into `btrfs receive`, and carries the
// metadata separately because send cannot.
func transferBySend(req transferRequest) (*transferResult, error) {
	if err := writeMetadataTar(req); err != nil {
		return nil, err
	}
	emitProgress(req.json, "send_stream", 0, 0, "bytes")
	if err := sendSubvolumeInto(req, req.source.dataPath, req.parentSnapshotData); err != nil {
		return nil, err
	}
	channels := []string{backupChannelData, backupChannelMetadata}
	bytes := treeSize(backupDataPath(req.destRoot))
	if req.source.userDataPath != "" {
		emitProgress(req.json, "send_userdata_stream", 0, 0, "bytes")
		// No parent: incremental sends key off the previous backup's data
		// subvolume, and there is no corresponding record for user content yet.
		// A full send is correct but slower, which is a cost worth naming
		// rather than a bug.
		if err := sendSubvolumeInto(req, req.source.userDataPath, ""); err != nil {
			return nil, err
		}
		channels = append(channels, backupChannelUserData)
		bytes += treeSize(backupUserDataPath(req.destRoot))
	}
	return &transferResult{bytes: bytes, channels: channels}, nil
}

// sendSubvolumeInto pipes one `btrfs send` into `btrfs receive`. receive names
// the arriving subvolume after its source, which is why the workspace layout
// uses the same names at both ends: data/ arrives as data/ and userdata/ as
// userdata/, with no rename step to get wrong.
func sendSubvolumeInto(req transferRequest, subvolume, parent string) error {
	send := exec.Command("btrfs", sendArgsFor(req, subvolume, parent)...)
	receive := exec.Command("btrfs", "receive", req.destRoot)
	pipe, err := send.StdoutPipe()
	if err != nil {
		return err
	}
	receive.Stdin = pipe
	receive.Stderr = os.Stderr
	var sendErrors strings.Builder
	send.Stderr = &sendErrors
	if err := receive.Start(); err != nil {
		return failuref("data_transfer_failed", "start btrfs receive: %v", err)
	}
	if err := send.Run(); err != nil {
		_ = receive.Process.Kill()
		_ = receive.Wait()
		return describeSendFailure(err, sendErrors.String())
	}
	if err := receive.Wait(); err != nil {
		return failuref("data_transfer_failed", "btrfs receive: %v", err)
	}
	return nil
}

// transferBySendFile writes the stream to a file. The destination only has to
// be writable, but a restore then requires a Btrfs target, which is why the
// mode carries restore_requires_btrfs_target.
func transferBySendFile(req transferRequest) (*transferResult, error) {
	if err := writeMetadataTar(req); err != nil {
		return nil, err
	}
	emitProgress(req.json, "send_stream", 0, 0, "bytes")
	size, err := sendSubvolumeToFile(req, req.source.dataPath, req.parentSnapshotData, backupStreamPath(req.destRoot))
	if err != nil {
		return nil, err
	}
	channels := []string{backupChannelData, backupChannelMetadata}
	if req.source.userDataPath != "" {
		emitProgress(req.json, "send_userdata_stream", 0, 0, "bytes")
		userSize, err := sendSubvolumeToFile(req, req.source.userDataPath, "", backupUserStreamPath(req.destRoot))
		if err != nil {
			return nil, err
		}
		channels = append(channels, backupChannelUserData)
		size += userSize
	}
	return &transferResult{bytes: size, channels: channels}, nil
}

// sendSubvolumeToFile writes one send stream out and returns its size. The size
// is what `verify` compares against later to detect a truncated file, so the
// stream is fsynced before it is measured.
func sendSubvolumeToFile(req transferRequest, subvolume, parent, streamPath string) (int64, error) {
	stream, err := os.OpenFile(streamPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, failuref("data_transfer_failed", "create %s: %v", streamPath, err)
	}
	send := exec.Command("btrfs", sendArgsFor(req, subvolume, parent)...)
	send.Stdout = stream
	var sendErrors strings.Builder
	send.Stderr = &sendErrors
	runErr := send.Run()
	syncErr := stream.Sync()
	closeErr := stream.Close()
	if runErr != nil {
		_ = os.Remove(streamPath)
		return 0, describeSendFailure(runErr, sendErrors.String())
	}
	if syncErr != nil {
		return 0, failuref("data_transfer_failed", "flush %s: %v", streamPath, syncErr)
	}
	if closeErr != nil {
		return 0, failuref("data_transfer_failed", "close %s: %v", streamPath, closeErr)
	}
	info, err := os.Stat(streamPath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// runBtrfsWithStdin is `btrfs receive`'s shape: the stream arrives on standard
// input rather than as an argument. It is separate from runBtrfs because that
// one captures combined output, which cannot be done while stdin is a file
// being consumed at the same time.
var btrfsStdinCommand = func(stdin io.Reader, args ...string) error {
	cmd := exec.Command("btrfs", args...)
	cmd.Stdin = stdin
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

func runBtrfsWithStdin(stdin io.Reader, args ...string) error {
	return btrfsStdinCommand(stdin, args...)
}

func sendArgs(req transferRequest) []string {
	return sendArgsFor(req, req.source.dataPath, req.parentSnapshotData)
}

// sendArgsFor builds the argument list for one subvolume. The user-content
// stream is a second, independent send: it has its own parent for incremental
// runs, and pairing it with data/'s parent would ask btrfs to diff two
// unrelated subvolumes.
func sendArgsFor(req transferRequest, subvolume, parent string) []string {
	args := []string{"send"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return append(args, subvolume)
}

// describeSendFailure turns the bare EPERM an ordinary user gets into the one
// sentence that explains it. Creating and snapshotting subvolumes need no
// privilege, so "send needs root" is genuinely surprising, and a user who has
// just watched `anas snapshot create` succeed deserves better than
// "Operation not permitted".
func describeSendFailure(cause error, stderr string) error {
	detail := strings.TrimSpace(stderr)
	if strings.Contains(detail, "Operation not permitted") || strings.Contains(cause.Error(), "permission") {
		return preconditionErrorf(reasonInsufficientPrivilege,
			"btrfs send needs CAP_SYS_ADMIN and this process does not have it. "+
				"Creating and snapshotting subvolumes do not, which is why the snapshot itself succeeded. "+
				"Run this command as root, grant the service CAP_SYS_ADMIN, or use --mode copy: %s", detail)
	}
	if detail == "" {
		detail = cause.Error()
	}
	return failuref("data_transfer_failed", "btrfs send: %s", detail)
}

// keepClassified preserves an error that already carries an enumerated code
// and exit status. Wrapping it again would replace a precondition a caller can
// act on — "you are not root" — with a generic execution failure.
func keepClassified(err error, code, format string, args ...any) error {
	if classified, ok := err.(*CLIError); ok {
		return classified
	}
	return failuref(code, "%s: %v", fmt.Sprintf(format, args...), err)
}

// describeCopyFailure names the one cause worth distinguishing. "rsync exit
// status 23" tells a user nothing; "the containers own these files and you do
// not" tells them what to do about it.
func describeCopyFailure(src string, cause error, output string) error {
	detail := strings.TrimSpace(output)
	if strings.Contains(detail, "Permission denied") {
		return preconditionErrorf(reasonInsufficientPrivilege,
			"cannot read all of %s: containers write their data as root, so an ordinary user cannot copy it. "+
				"A partial copy would be a backup with holes in it, so it is refused rather than published. "+
				"Run this as root, or use a Btrfs mode, which reads through the filesystem instead: %s",
			src, detail)
	}
	if detail == "" {
		detail = cause.Error()
	}
	return failuref("data_transfer_failed", "rsync %s: %s", src, detail)
}

// transferByCopy writes the tree out file by file. It is the only mode that
// works when the source is not Btrfs, and the only one whose result can be
// restored onto any filesystem.
func transferByCopy(req transferRequest) (*transferResult, error) {
	if err := writeMetadataChannel(req); err != nil {
		return nil, err
	}
	emitProgress(req.json, "copy_files", 0, 0, "bytes")
	if req.source.userDataPath != "" {
		emitProgress(req.json, "copy_userdata", 0, 0, "bytes")
		if err := copyDirectory(req.source.userDataPath, backupUserDataPath(req.destRoot)); err != nil {
			return nil, failuref("userdata_transfer_failed", "copy the user data into %s: %v", req.destRoot, err)
		}
	}
	if err := copyDirectory(req.source.dataPath, backupDataPath(req.destRoot)); err != nil {
		return nil, keepClassified(err, "data_transfer_failed", "copy the data to %s", req.destRoot)
	}
	channels := []string{backupChannelData, backupChannelMetadata}
	bytes := treeSize(backupDataPath(req.destRoot))
	if req.source.userDataPath != "" {
		channels = append(channels, backupChannelUserData)
		bytes += treeSize(backupUserDataPath(req.destRoot))
	}
	return &transferResult{bytes: bytes, channels: channels}, nil
}

// ---------------------------------------------------------------- metadata

// writeMetadataChannel copies snapshot.yml, meta/ and deployment/ as ordinary
// files, for the modes whose destination is a directory tree anyway.
func writeMetadataChannel(req transferRequest) error {
	emitProgress(req.json, "copy_state", 0, int64(len(req.source.parts)), "files")
	for _, part := range req.source.parts {
		target := filepath.Join(req.destRoot, part.rel)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if part.dir {
			if err := copyDirectory(part.src, target); err != nil {
				return keepClassified(err, "metadata_transfer_failed", "copy %s", part.src)
			}
			continue
		}
		if err := copyFileMode(part.src, target, 0600); err != nil {
			return failuref("metadata_transfer_failed", "copy %s: %v", part.src, err)
		}
	}
	return finishSynthesizedMetadata(req)
}

// writeMetadataTar is the second channel for the send modes. One file rather
// than a tree because it travels alongside a stream to a destination that may
// not preserve ownership or modes on its own, and because a single file makes
// "did the second channel finish" a question with one answer.
func writeMetadataTar(req transferRequest) error {
	// The synthesized pieces are staged into the destination first, then
	// archived with the rest, so the tar is the whole metadata channel and not
	// most of it.
	if err := finishSynthesizedMetadata(req); err != nil {
		return err
	}
	staged := []backupPart{}
	staged = append(staged, req.source.parts...)
	if req.source.root == "" {
		staged = append(staged,
			backupPart{src: filepath.Join(req.destRoot, "meta", snapshotMetaSecretsName), rel: filepath.Join("meta", snapshotMetaSecretsName)},
			backupPart{src: filepath.Join(req.destRoot, "meta", snapshotMetaStateName), rel: filepath.Join("meta", snapshotMetaStateName)},
			backupPart{src: filepath.Join(req.destRoot, "snapshot.yml"), rel: "snapshot.yml"},
		)
	}
	emitProgress(req.json, "send_metadata", 0, int64(len(staged)), "files")
	path := backupMetaTarPath(req.destRoot)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return failuref("metadata_transfer_failed", "create %s: %v", path, err)
	}
	archive := tar.NewWriter(file)
	for _, part := range staged {
		if err := appendToTar(archive, part.src, part.rel); err != nil {
			_ = archive.Close()
			_ = file.Close()
			_ = os.Remove(path)
			return failuref("metadata_transfer_failed", "archive %s: %v", part.src, err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return failuref("metadata_transfer_failed", "finish %s: %v", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return failuref("metadata_transfer_failed", "flush %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		return failuref("metadata_transfer_failed", "close %s: %v", path, err)
	}
	// The staging copies are removed once they are inside the archive, so the
	// send modes leave exactly two files behind and `verify` has one shape to
	// check rather than two.
	if req.source.root == "" {
		_ = os.RemoveAll(filepath.Join(req.destRoot, "meta"))
		_ = os.Remove(filepath.Join(req.destRoot, "snapshot.yml"))
	}
	return nil
}

// finishSynthesizedMetadata writes the pieces that only exist when the backup
// was assembled from a live workspace rather than from a snapshot.
func finishSynthesizedMetadata(req transferRequest) error {
	if req.source.synthesizedSnapshot == nil {
		return nil
	}
	if err := writeSynthesizedMeta(req.workspace, req.destRoot, req.source.deploymentID); err != nil {
		return failuref("metadata_transfer_failed", "%v", err)
	}
	return writeYAMLAtomic(filepath.Join(req.destRoot, "snapshot.yml"), req.source.synthesizedSnapshot, 0600)
}

func appendToTar(archive *tar.Writer, src, rel string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return appendFileToTar(archive, src, rel, info)
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		name := filepath.Join(rel, relative)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			header := &tar.Header{Name: name + "/", Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeDir, ModTime: info.ModTime()}
			return archive.WriteHeader(header)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		return appendFileToTar(archive, path, name, info)
	})
}

func appendFileToTar(archive *tar.Writer, src, name string, info os.FileInfo) error {
	header := &tar.Header{
		Name: name, Mode: int64(info.Mode().Perm()), Size: info.Size(),
		Typeflag: tar.TypeReg, ModTime: info.ModTime(),
	}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(archive, file)
	return err
}

func extractTar(path, dest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	archive := tar.NewReader(file)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject anything that would land outside dest. The archive comes from
		// a destination that may be shared with other hosts, so it is not
		// trusted input.
		target := filepath.Join(dest, filepath.Clean("/"+header.Name))
		if !pathWithin(target, dest) {
			return fmt.Errorf("archive entry %q escapes %s", header.Name, dest)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, archive); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %d", header.Name, header.Typeflag)
		}
	}
}

// ---------------------------------------------------------------- copying

// copyDirectory copies a tree, preferring rsync and falling back to a built-in
// walk.
//
// -H is passed because a snapshot's deployment/ may be hard-linked to the
// artifact it was copied from, and without it rsync writes each link as a full
// independent file. -A and -X carry ACLs and extended attributes, which
// container data routinely has.
//
// --one-file-system is conspicuously absent, and must stay absent: data/ is a
// subvolume, so the flag would stop at its boundary and produce a backup with
// no data in it that reported success.
func copyDirectory(src, dst string) error {
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}
	if haveTool("rsync") {
		// Trailing slashes: copy the contents of src into dst rather than
		// creating dst/src.
		cmd := exec.Command("rsync", "-aHAX", "--numeric-ids", "--delete",
			strings.TrimSuffix(src, "/")+"/", strings.TrimSuffix(dst, "/")+"/")
		output, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		// rsync exit 23 is "partial transfer due to error", which unprivileged
		// runs hit on root-owned container files. `capabilities` refuses copy
		// mode before it gets this far, so reaching here means the permissions
		// changed underneath — or a caller went around the check. Either way an
		// incomplete copy must not be published as a backup, and falling
		// through to the built-in walk would hit the same wall one file later.
		return describeCopyFailure(src, err, string(output))
	}
	return copyTreeAll(src, dst)
}

// copyTreeAll is the no-rsync fallback. It preserves modes but not ownership,
// which is the honest limit of what a Go process can do without privilege.
func copyTreeAll(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		return copyFileMode(path, target, info.Mode().Perm())
	})
}
