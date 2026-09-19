package computeimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrArtifactBuildIncomplete = errors.New("image build attempt is incomplete; retain its files and recover verified original outputs, or use a new revision")

// ArtifactBuildRequest contains only release-owned paths and a validated target.
// It is NOT a host-action request or a consumer API. Implementations run an
// independently trusted, pinned distrobuilder on an isolated release builder.
// Recipes can run root commands: neither this type nor the archive is a sandbox.
type ArtifactBuildRequest struct {
	Target           Target
	RecipeFile       string
	OutputDirectory  string
	CacheDirectory   string
	SourcesDirectory string
}

type ArtifactBuildFunc func(context.Context, ArtifactBuildRequest) error

type ArtifactBuildInput struct {
	Name   string
	Path   string
	SHA256 string
}

// BuildOnce bakes before deployment preparation, never from Provider ensure.
// A recorded revision is verified and reused without calling the builder.
// Missing/corrupt recorded objects are errors, not permission to rebuild.
// Before launching a new build, a durable, exclusive attempt directory freezes
// its recipe and builder digests. Crashes and errors retain that directory and
// block an implicit second build of the same revision across archive reopens.
// Recovery can use Record with the original outputs; no mounts are recursively
// removed and no incomplete attempt is silently adopted or overwritten.
func (archive *ArtifactArchive) BuildOnce(ctx context.Context, reference Reference, target Target, recipe []byte, builderDigest string, inputs []ArtifactBuildInput, build ArtifactBuildFunc) (ArtifactRelease, bool, error) {
	unlock, err := archive.acquire(ctx)
	if err != nil {
		return ArtifactRelease{}, false, err
	}
	defer unlock()
	name, err := artifactReleaseFilename(reference, target)
	if err != nil || len(recipe) == 0 || len(recipe) > MaxRecipeBytes || !fingerprintPattern.MatchString(builderDigest) || build == nil {
		return ArtifactRelease{}, false, ErrArtifactInvalid
	}
	if len(inputs) != 1 || inputs[0].Name != "forgejo-runner" || !fingerprintPattern.MatchString(inputs[0].SHA256) || !filepath.IsAbs(inputs[0].Path) || filepath.Clean(inputs[0].Path) != inputs[0].Path {
		return ArtifactRelease{}, false, ErrArtifactInvalid
	}
	recipe = appendBuildProvenance(recipe, builderDigest, inputs)
	if len(recipe) > MaxRecipeBytes {
		return ArtifactRelease{}, false, ErrArtifactInvalid
	}
	// Freeze caller memory before handing anything to the trusted builder.
	recipe = bytes.Clone(recipe)
	hash := sha256.Sum256(recipe)
	recipeDigest := hex.EncodeToString(hash[:])
	old, err := archive.readRelease(name)
	if err == nil {
		if old.Entry.RecipeDigest != recipeDigest || old.Artifact.Format != ArtifactSplit {
			return ArtifactRelease{}, true, ErrArtifactConflict
		}
		if err := archive.verifyRelease(ctx, old); err != nil {
			return ArtifactRelease{}, true, err
		}
		if err := archive.check(); err != nil {
			return ArtifactRelease{}, true, err
		}
		return old, true, nil
	}
	if !errors.Is(err, ErrArtifactNotFound) {
		return ArtifactRelease{}, false, err
	}
	names, err := archive.releaseNames()
	if err != nil || len(names) >= maxArtifactReleases {
		return ArtifactRelease{}, false, ErrArtifactUnavailable
	}
	directory := "build-" + strings.TrimSuffix(filepath.Base(name), ".json")
	if err := archive.root.Mkdir(directory, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ArtifactRelease{}, false, ErrArtifactBuildIncomplete
		}
		return ArtifactRelease{}, false, ErrArtifactUnavailable
	}
	// A failed setup also leaves its name reserved. Never run a builder from
	// an adopted or partially initialized tree on a later invocation.
	for _, child := range []string{"", "/output", "/cache", "/sources"} {
		path := directory + child
		if child != "" && archive.root.Mkdir(path, 0700) != nil {
			return ArtifactRelease{}, false, ErrArtifactUnavailable
		}
		info, err := archive.root.Lstat(path)
		if err != nil || !artifactPrivateDirectory(info) {
			return ArtifactRelease{}, false, ErrArtifactUnavailable
		}
		// distrobuilder may remove its own cache; only persistent directories
		// participate in the archive's identity checks.
		if child == "" || child == "/output" {
			archive.dirs[path] = info
		}
	}
	if err := archive.publishBytes(directory+"/recipe.yml", recipe); err != nil {
		return ArtifactRelease{}, false, err
	}
	intent, err := json.Marshal(struct {
		Schema        string    `json:"schema"`
		Reference     Reference `json:"reference"`
		Target        Target    `json:"target"`
		RecipeDigest  string    `json:"recipe_digest"`
		BuilderDigest string    `json:"builder_digest"`
		Inputs        []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"inputs"`
	}{"anas.incus-image-build/v1", reference, target, recipeDigest, builderDigest, []struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
	}{{Name: inputs[0].Name, SHA256: inputs[0].SHA256}}})
	if err != nil {
		return ArtifactRelease{}, false, ErrArtifactInvalid
	}
	if err := archive.publishBytes(directory+"/attempt.json", append(intent, '\n')); err != nil {
		return ArtifactRelease{}, false, err
	}
	if err := copyBuildInput(ctx, inputs[0], filepath.Join(archive.path, directory, "sources", inputs[0].Name)); err != nil {
		return ArtifactRelease{}, false, err
	}
	if archive.syncDirectory(directory) != nil || archive.syncDirectory(".") != nil || archive.check() != nil {
		return ArtifactRelease{}, false, ErrArtifactUnavailable
	}
	if ctx.Err() != nil {
		return ArtifactRelease{}, false, ctx.Err()
	}
	base := filepath.Join(archive.path, directory)
	request := ArtifactBuildRequest{
		Target: target, RecipeFile: filepath.Join(base, "recipe.yml"),
		OutputDirectory: filepath.Join(base, "output"), CacheDirectory: filepath.Join(base, "cache"),
		SourcesDirectory: filepath.Join(base, "sources"),
	}
	if err := build(ctx, request); err != nil {
		if ctx.Err() != nil {
			return ArtifactRelease{}, false, errors.Join(ErrArtifactBuildIncomplete, ctx.Err())
		}
		// Builder errors can include recipe content, commands and credentials.
		return ArtifactRelease{}, false, ErrArtifactBuildIncomplete
	}
	if ctx.Err() != nil {
		return ArtifactRelease{}, false, errors.Join(ErrArtifactBuildIncomplete, ctx.Err())
	}
	frozen, err := archive.readBytes(directory+"/recipe.yml", MaxRecipeBytes)
	if err != nil || !bytes.Equal(frozen, recipe) || archive.check() != nil {
		return ArtifactRelease{}, false, ErrArtifactUnavailable
	}
	rootfs := "rootfs.squashfs"
	if target.Interface == "incus_vm" {
		rootfs = "disk.qcow2"
	}
	return archive.recordLocked(ctx, reference, target, recipe, ArtifactSplit, []string{
		filepath.Join(request.OutputDirectory, "incus.tar.xz"),
		filepath.Join(request.OutputDirectory, rootfs),
	})
}

func appendBuildProvenance(recipe []byte, builderDigest string, inputs []ArtifactBuildInput) []byte {
	out := bytes.Clone(recipe)
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, []byte("# anas-provenance-builder-sha256: "+builderDigest+"\n")...)
	for _, input := range inputs {
		out = append(out, []byte("# anas-provenance-input-"+input.Name+"-sha256: "+input.SHA256+"\n")...)
	}
	return out
}

func copyBuildInput(ctx context.Context, input ArtifactBuildInput, destination string) error {
	source, err := os.OpenFile(input.Path, os.O_RDONLY|artifactNoFollowFlags(), 0)
	if err != nil {
		return ErrArtifactUnavailable
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 4096 || info.Size() > 256<<20 || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return ErrArtifactInvalid
	}
	target, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY|artifactNoFollowFlags(), 0500)
	if err != nil {
		return ErrArtifactUnavailable
	}
	keep := false
	defer func() {
		_ = target.Close()
		if !keep {
			_ = os.Remove(destination)
		}
	}()
	hash := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(target, hash), io.LimitReader(&artifactContextReader{ctx: ctx, reader: source}, info.Size()+1), make([]byte, 128<<10))
	after, statErr := source.Stat()
	if err != nil || statErr != nil || n != info.Size() || !sameArtifactFile(info, after) || hex.EncodeToString(hash.Sum(nil)) != input.SHA256 || target.Chmod(0500) != nil || target.Sync() != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrArtifactUnavailable
	}
	keep = true
	return nil
}
