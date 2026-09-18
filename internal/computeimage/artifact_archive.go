package computeimage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const (
	artifactArchiveMarker = "anas.incus-artifact-archive/v1\n"
	maxArtifactReleases   = 4096
)

// ArtifactArchive is an administrator/release-tool boundary on a private LOCAL
// filesystem, never a consumer-facing upload or download endpoint. One session
// holds an exclusive OS lock. It has no network, builder, shell, daemon, prune,
// or implicit initialize/repair fallback. Preserve this archive and the signed
// release catalog independently; losing history is not permission to rebuild.
type ArtifactArchive struct {
	path     string
	root     *os.Root
	lock     *os.File
	identity os.FileInfo
	lockInfo os.FileInfo
	dirs     map[string]os.FileInfo
	gate     chan struct{}
	closed   bool
}

// OpenArtifactArchive with initialize=true creates a NEW archive only. An
// existing directory, even an empty one, is never adopted or reinitialized.
// Normal record/inspect/catalog callers must pass false. An incomplete init
// leaves its private directory for inspection instead of deleting unknown data.
func OpenArtifactArchive(ctx context.Context, directory string, initialize bool) (*ArtifactArchive, error) {
	if ctx == nil || !artifactArchiveSupported() || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrArtifactInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if initialize {
		if err := os.Mkdir(directory, 0700); err != nil {
			if errors.Is(err, os.ErrExist) {
				return nil, ErrArtifactConflict
			}
			return nil, ErrArtifactUnavailable
		}
	}
	identity, err := os.Lstat(directory)
	if err != nil || !artifactPrivateDirectory(identity) {
		return nil, ErrArtifactUnavailable
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	keep := false
	defer func() {
		if !keep {
			_ = root.Close()
		}
	}()
	archive := &ArtifactArchive{path: directory, root: root, identity: identity, dirs: map[string]os.FileInfo{}, gate: make(chan struct{}, 1)}
	for _, name := range []string{"objects", "releases"} {
		if initialize && root.Mkdir(name, 0700) != nil {
			return nil, ErrArtifactUnavailable
		}
		info, err := root.Lstat(name)
		if err != nil || !artifactPrivateDirectory(info) {
			return nil, ErrArtifactUnavailable
		}
		archive.dirs[name] = info
	}
	flags := os.O_RDWR | artifactNoFollowFlags()
	if initialize {
		flags |= os.O_CREATE | os.O_EXCL
	}
	lock, err := root.OpenFile(".lock", flags, 0600)
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	defer func() {
		if !keep {
			_ = lock.Close()
		}
	}()
	lockInfo, err := lock.Stat()
	if err != nil || !artifactOwnedFile(lockInfo, 0600, true) {
		return nil, ErrArtifactUnavailable
	}
	if err := artifactTryLock(lock); err != nil {
		return nil, err
	}
	archive.lock, archive.lockInfo = lock, lockInfo
	if initialize {
		if err := archive.publishBytes(".format", []byte(artifactArchiveMarker)); err != nil {
			return nil, err
		}
		if lock.Sync() != nil || archive.syncDirectory(".") != nil {
			return nil, ErrArtifactUnavailable
		}
	}
	marker, err := archive.readBytes(".format", len(artifactArchiveMarker))
	if err != nil || string(marker) != artifactArchiveMarker || archive.check() != nil {
		return nil, ErrArtifactUnavailable
	}
	archive.gate <- struct{}{}
	keep = true
	return archive, nil
}

func (archive *ArtifactArchive) Close() error {
	if archive == nil || archive.gate == nil {
		return nil
	}
	<-archive.gate
	defer func() { archive.gate <- struct{}{} }()
	if archive.closed {
		return nil
	}
	archive.closed = true
	err := errors.Join(artifactUnlock(archive.lock), archive.lock.Close(), archive.root.Close())
	if err != nil {
		return ErrArtifactUnavailable
	}
	return nil
}

func (archive *ArtifactArchive) acquire(ctx context.Context) (func(), error) {
	if archive == nil || archive.gate == nil || ctx == nil {
		return nil, ErrArtifactUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-archive.gate:
	}
	release := func() { archive.gate <- struct{}{} }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	if archive.closed || archive.check() != nil {
		release()
		return nil, ErrArtifactUnavailable
	}
	return release, nil
}

func (archive *ArtifactArchive) check() error {
	current, err := os.Lstat(archive.path)
	if err != nil || !artifactPrivateDirectory(current) || !os.SameFile(archive.identity, current) {
		return ErrArtifactUnavailable
	}
	pinned, err := archive.root.Stat(".")
	if err != nil || !os.SameFile(archive.identity, pinned) {
		return ErrArtifactUnavailable
	}
	lock, err := archive.root.Lstat(".lock")
	if err != nil || !artifactOwnedFile(lock, 0600, true) || !os.SameFile(archive.lockInfo, lock) {
		return ErrArtifactUnavailable
	}
	for name, before := range archive.dirs {
		after, err := archive.root.Lstat(name)
		if err != nil || !artifactPrivateDirectory(after) || !os.SameFile(before, after) {
			return ErrArtifactUnavailable
		}
	}
	marker, err := archive.readBytes(".format", len(artifactArchiveMarker))
	if err != nil || string(marker) != artifactArchiveMarker {
		return ErrArtifactUnavailable
	}
	return nil
}

// Record copies already-built bytes, computes the Incus fingerprint and
// publishes a revision only after all content-addressed objects are durable.
// Re-recording the same revision is idempotent and can restore MISSING objects
// from identical original bytes. Different bytes or recipe digests conflict;
// corrupt existing objects are not overwritten. No builder is ever invoked.
// sourcePaths and recipe are trusted release inputs, not consumer parameters.
func (archive *ArtifactArchive) Record(ctx context.Context, reference Reference, target Target, recipe []byte, format string, sourcePaths []string) (release ArtifactRelease, existing bool, returnErr error) {
	unlock, err := archive.acquire(ctx)
	if err != nil {
		return release, false, err
	}
	defer unlock()
	name, err := artifactReleaseFilename(reference, target)
	if err != nil || len(recipe) == 0 || len(recipe) > MaxRecipeBytes || len(sourcePaths) != len(artifactRoles(format)) || len(sourcePaths) == 0 {
		return release, false, ErrArtifactInvalid
	}
	old, err := archive.readRelease(name)
	if err != nil && !errors.Is(err, ErrArtifactNotFound) {
		return release, false, err
	}
	existing = err == nil
	names, err := archive.releaseNames()
	if err != nil {
		return release, false, err
	}
	if !existing && len(names) >= maxArtifactReleases {
		return release, false, ErrArtifactUnavailable
	}
	recipeHash := sha256.Sum256(recipe)
	recipeDigest := hex.EncodeToString(recipeHash[:])
	if existing && (old.Entry.RecipeDigest != recipeDigest || old.Artifact.Format != format) {
		return release, true, ErrArtifactConflict
	}
	type staged struct {
		name string
		file *os.File
		info os.FileInfo
	}
	var stages []staged
	var sources []*os.File
	var sourceInfo []os.FileInfo
	defer func() {
		for _, source := range sources {
			_ = source.Close()
		}
		for _, stage := range stages {
			_ = stage.file.Close()
			if err := archive.removeOwned(stage.name, stage.info); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	readers := make([]io.Reader, 0, len(sourcePaths))
	for i, path := range sourcePaths {
		source, err := os.OpenFile(path, os.O_RDONLY|artifactNoFollowFlags(), 0)
		if err != nil {
			return release, existing, ErrArtifactUnavailable
		}
		sources = append(sources, source)
		info, err := source.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > artifactPartLimit(artifactRoles(format)[i]) {
			return release, existing, ErrArtifactInvalid
		}
		sourceInfo = append(sourceInfo, info)
		stageName, stageFile, err := archive.newTemporary("objects")
		if err != nil {
			return release, existing, err
		}
		stageInfo, err := stageFile.Stat()
		if err != nil {
			_ = stageFile.Close()
			return release, existing, ErrArtifactUnavailable
		}
		stages = append(stages, staged{name: stageName, file: stageFile, info: stageInfo})
		readers = append(readers, io.TeeReader(source, stageFile))
	}
	entry := Entry{Catalog: reference.Catalog, Name: reference.Name, Revision: reference.Revision, Target: target, RecipeDigest: recipeDigest}
	if existing {
		entry.Fingerprint = old.Entry.Fingerprint
	}
	release, err = DescribeArtifactRelease(ctx, entry, format, readers)
	if err != nil {
		return release, existing, err
	}
	artifact := release.Artifact
	if existing && !reflect.DeepEqual(old, release) {
		return release, true, ErrArtifactConflict
	}
	for i, source := range sources {
		after, err := source.Stat()
		if err != nil || !sameArtifactFile(sourceInfo[i], after) || after.Size() != artifact.Parts[i].Size {
			return release, existing, ErrArtifactUnavailable
		}
		if stages[i].file.Chmod(0400) != nil || stages[i].file.Sync() != nil {
			return release, existing, ErrArtifactUnavailable
		}
	}
	if ctx.Err() != nil {
		return release, existing, ctx.Err()
	}
	if archive.check() != nil {
		return release, existing, ErrArtifactUnavailable
	}
	for i, part := range artifact.Parts {
		object := artifactObjectFilename(part.SHA256)
		if err := archive.root.Link(stages[i].name, object); err != nil && !errors.Is(err, os.ErrExist) {
			return release, existing, ErrArtifactUnavailable
		}
		if err := archive.verifyPart(ctx, part); err != nil {
			return release, existing, err
		}
	}
	if archive.syncDirectory("objects") != nil {
		return release, existing, ErrArtifactUnavailable
	}
	if ctx.Err() != nil {
		return release, existing, ctx.Err()
	}
	if !existing {
		body, err := EncodeArtifactRelease(release)
		if err != nil {
			return release, false, err
		}
		if err := archive.publishBytes(name, body); err != nil {
			return release, false, err
		}
	}
	if err := archive.check(); err != nil {
		return release, existing, err
	}
	return release, existing, nil
}

func (archive *ArtifactArchive) Inspect(ctx context.Context, reference Reference, target Target) (ArtifactRelease, error) {
	unlock, err := archive.acquire(ctx)
	if err != nil {
		return ArtifactRelease{}, err
	}
	defer unlock()
	name, err := artifactReleaseFilename(reference, target)
	if err != nil {
		return ArtifactRelease{}, err
	}
	release, err := archive.readRelease(name)
	if err == nil {
		err = archive.verifyRelease(ctx, release)
	}
	if err == nil {
		err = archive.check()
	}
	return release, err
}

// Catalog emits only committed revisions, after checking every object's bytes.
// Supplying the previous trusted catalog prevents lost or changed history from
// becoming a new release. nil is suitable ONLY for an explicitly first release.
func (archive *ArtifactArchive) Catalog(ctx context.Context, previous []Entry) ([]Entry, error) {
	unlock, err := archive.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	names, err := archive.releaseNames()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		release, err := archive.readRelease(name)
		if err != nil {
			return nil, err
		}
		if err := archive.verifyRelease(ctx, release); err != nil {
			return nil, err
		}
		entries = append(entries, release.Entry)
	}
	old, err := NewCatalog(previous, nil)
	if err != nil {
		return nil, ErrArtifactInvalid
	}
	if _, err := NewCatalog(entries, old); err != nil {
		return nil, ErrArtifactConflict
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].key(), entries[j].key()
		return strings.Join([]string{a.Catalog, a.Name, a.Revision, a.Architecture, a.Interface}, "\x00") < strings.Join([]string{b.Catalog, b.Name, b.Revision, b.Architecture, b.Interface}, "\x00")
	})
	if archive.check() != nil {
		return nil, ErrArtifactUnavailable
	}
	return entries, nil
}

func (archive *ArtifactArchive) verifyRelease(ctx context.Context, release ArtifactRelease) error {
	var files []*os.File
	defer func() {
		for _, file := range files {
			_ = file.Close()
		}
	}()
	readers := make([]io.Reader, 0, len(release.Artifact.Parts))
	var before []os.FileInfo
	for _, part := range release.Artifact.Parts {
		file, info, err := archive.openObject(part)
		if err != nil {
			return err
		}
		files, before, readers = append(files, file), append(before, info), append(readers, file)
	}
	actual, err := DescribeArtifact(ctx, release.Artifact.Target, release.Artifact.Format, readers)
	if err != nil || !reflect.DeepEqual(actual, release.Artifact) {
		return ErrArtifactUnavailable
	}
	for i, file := range files {
		after, err := file.Stat()
		current, pathErr := archive.root.Lstat(artifactObjectFilename(release.Artifact.Parts[i].SHA256))
		if err != nil || pathErr != nil || !sameArtifactFile(before[i], after) || !sameArtifactFile(before[i], current) {
			return ErrArtifactUnavailable
		}
	}
	return nil
}

func (archive *ArtifactArchive) verifyPart(ctx context.Context, part ArtifactPart) error {
	file, before, err := archive.openObject(part)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(&artifactContextReader{ctx: ctx, reader: file}, part.Size+1))
	after, statErr := file.Stat()
	current, pathErr := archive.root.Lstat(artifactObjectFilename(part.SHA256))
	if err != nil || statErr != nil || pathErr != nil || n != part.Size || hex.EncodeToString(digest.Sum(nil)) != part.SHA256 || !sameArtifactFile(before, after) || !sameArtifactFile(before, current) {
		return ErrArtifactUnavailable
	}
	return nil
}

func (archive *ArtifactArchive) openObject(part ArtifactPart) (*os.File, os.FileInfo, error) {
	file, err := archive.root.OpenFile(artifactObjectFilename(part.SHA256), os.O_RDONLY|artifactNoFollowFlags(), 0)
	if err != nil {
		return nil, nil, ErrArtifactUnavailable
	}
	info, err := file.Stat()
	// Artifacts contain no secrets. Read-only hard links are permitted so a
	// crash between link publication and temporary-name cleanup is recoverable.
	// Every use rechecks content digests; a matching name is not sufficient.
	if err != nil || !artifactOwnedFile(info, 0400, false) || info.Size() != part.Size {
		_ = file.Close()
		return nil, nil, ErrArtifactUnavailable
	}
	return file, info, nil
}

func (archive *ArtifactArchive) readRelease(name string) (ArtifactRelease, error) {
	body, err := archive.readBytes(name, MaxArtifactRecordBytes)
	if err != nil {
		return ArtifactRelease{}, err
	}
	release, err := DecodeArtifactRelease(body)
	if err != nil {
		return ArtifactRelease{}, err
	}
	want, err := artifactReleaseFilename(Reference{Catalog: release.Entry.Catalog, Name: release.Entry.Name, Revision: release.Entry.Revision}, release.Entry.Target)
	if err != nil || want != name {
		return ArtifactRelease{}, ErrArtifactInvalid
	}
	return release, nil
}

func (archive *ArtifactArchive) releaseNames() ([]string, error) {
	directory, err := archive.root.Open("releases")
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maxArtifactReleases + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ErrArtifactUnavailable
	}
	if len(entries) > maxArtifactReleases {
		return nil, ErrArtifactUnavailable
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if len(name) != 69 || !strings.HasSuffix(name, ".json") || !fingerprintPattern.MatchString(strings.TrimSuffix(name, ".json")) {
			return nil, ErrArtifactUnavailable // Unknown or interrupted metadata needs review.
		}
		names = append(names, "releases/"+name)
	}
	sort.Strings(names)
	return names, nil
}

func artifactReleaseFilename(reference Reference, target Target) (string, error) {
	if reference.validate() != nil || reference.Fingerprint != "" || target.validate() != nil {
		return "", ErrArtifactInvalid
	}
	body, err := json.Marshal(struct {
		Reference Reference `json:"reference"`
		Target    Target    `json:"target"`
	}{reference, target})
	if err != nil {
		return "", ErrArtifactInvalid
	}
	digest := sha256.Sum256(body)
	return "releases/" + hex.EncodeToString(digest[:]) + ".json", nil
}

func artifactObjectFilename(digest string) string { return "objects/" + digest + ".blob" }

func sameArtifactFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.Mode() == after.Mode() && before.ModTime().Equal(after.ModTime())
}

func (archive *ArtifactArchive) readBytes(name string, limit int) ([]byte, error) {
	file, err := archive.root.OpenFile(name, os.O_RDONLY|artifactNoFollowFlags(), 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrArtifactNotFound
	}
	if err != nil {
		return nil, ErrArtifactUnavailable
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !artifactOwnedFile(before, 0400, false) || before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, ErrArtifactUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	after, statErr := file.Stat()
	current, pathErr := archive.root.Lstat(name)
	if err != nil || statErr != nil || pathErr != nil || len(body) > limit || int64(len(body)) != before.Size() || !sameArtifactFile(before, after) || !sameArtifactFile(before, current) {
		return nil, ErrArtifactUnavailable
	}
	return body, nil
}

func (archive *ArtifactArchive) newTemporary(directory string) (string, *os.File, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", nil, ErrArtifactUnavailable
	}
	name := filepath.Join(directory, ".pending-"+hex.EncodeToString(random[:]))
	file, err := archive.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR|artifactNoFollowFlags(), 0600)
	if err != nil {
		return "", nil, ErrArtifactUnavailable
	}
	return name, file, nil
}

// Link publishes without replacing an existing name. A crash can leave an
// orphan temporary hard link, but cannot expose partially written metadata as
// a committed revision. There is deliberately no rename-overwrite fallback.
func (archive *ArtifactArchive) publishBytes(name string, body []byte) (returnErr error) {
	temporary, file, err := archive.newTemporary(".")
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return ErrArtifactUnavailable
	}
	defer func() {
		_ = file.Close()
		if err := archive.removeOwned(temporary, info); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	n, err := file.Write(body)
	if err != nil || n != len(body) || file.Chmod(0400) != nil || file.Sync() != nil {
		return ErrArtifactUnavailable
	}
	if err := archive.root.Link(temporary, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return ErrArtifactUnavailable
		}
		existing, err := archive.readBytes(name, len(body))
		if err != nil || !bytes.Equal(existing, body) {
			return ErrArtifactConflict
		}
	}
	return archive.syncDirectory(filepath.Dir(name))
}

func (archive *ArtifactArchive) removeOwned(name string, identity os.FileInfo) error {
	info, err := archive.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(info, identity) || !info.Mode().IsRegular() {
		return ErrArtifactUnavailable
	}
	if archive.root.Remove(name) != nil {
		return ErrArtifactUnavailable
	}
	return archive.syncDirectory(filepath.Dir(name))
}

func (archive *ArtifactArchive) syncDirectory(name string) error {
	directory, err := archive.root.OpenFile(name, os.O_RDONLY|artifactNoFollowFlags(), 0)
	if err != nil {
		return ErrArtifactUnavailable
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil || !artifactPrivateDirectory(info) || directory.Sync() != nil {
		return ErrArtifactUnavailable
	}
	return nil
}
