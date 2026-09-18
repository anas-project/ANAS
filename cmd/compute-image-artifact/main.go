// compute-image-artifact inspects or verifies completed local split images.
// It does not build, import, download, sign or publish an image or catalog.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("compute-image-artifact", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	metadataPath := flags.String("metadata", "", "completed split metadata file (read only)")
	rootfsPath := flags.String("rootfs", "", "completed split rootfs/disk file (read only)")
	verifyPath := flags.String("verify", "", "canonical artifact release to verify; omit to inspect a new bake")
	name := flags.String("name", "", "catalog image name (inspection only)")
	revision := flags.String("revision", "", "immutable catalog revision (inspection only)")
	architecture := flags.String("architecture", "", "expected target: amd64 or arm64")
	isolation := flags.String("interface", "", "expected target: incus_vm or incus_container")
	recipeDigest := flags.String("recipe-digest", "", "SHA-256 of all trusted recipe inputs (inspection only)")
	fingerprint := flags.String("expected-fingerprint", "", "independently trusted image fingerprint; required for verification")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *metadataPath == "" || *rootfsPath == "" || ctx == nil {
		return errors.New("metadata and rootfs files are required; positional arguments are not accepted")
	}
	target := computeimage.Target{Architecture: *architecture, Interface: *isolation}
	var release computeimage.ArtifactRelease
	var frozen computeimage.Resolution
	if *verifyPath != "" {
		if *fingerprint == "" || *name != "" || *revision != "" || *recipeDigest != "" {
			return errors.New("verification requires an independent expected fingerprint and does not accept inspection-only fields")
		}
		file, before, err := openArtifactInput(*verifyPath, computeimage.MaxArtifactRecordBytes)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(file, computeimage.MaxArtifactRecordBytes+1))
		stableErr := artifactInputUnchanged(file, before)
		closeErr := file.Close()
		if readErr != nil || stableErr != nil || closeErr != nil {
			return computeimage.ErrArtifactUnavailable
		}
		release, err = computeimage.DecodeArtifactRelease(body)
		if err != nil {
			return err
		}
		if release.Artifact.Format != computeimage.ArtifactSplit {
			return errors.New("this command accepts split artifacts only")
		}
		frozen = computeimage.Resolution{Reference: computeimage.Reference{Fingerprint: *fingerprint}, Target: target, Fingerprint: *fingerprint}
	} else {
		release.Entry = computeimage.Entry{Catalog: "anas", Name: *name, Revision: *revision, Target: target, Fingerprint: *fingerprint, RecipeDigest: *recipeDigest}
	}
	metadata, metadataBefore, err := openArtifactInput(*metadataPath, computeimage.MaxMetadataBytes)
	if err != nil {
		return err
	}
	defer metadata.Close()
	rootfs, rootfsBefore, err := openArtifactInput(*rootfsPath, computeimage.MaxImageBytes)
	if err != nil {
		return err
	}
	defer rootfs.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	readers := []io.Reader{metadata, rootfs}
	if *verifyPath != "" {
		err = computeimage.VerifyArtifactRelease(ctx, release, frozen, readers)
	} else {
		release, err = computeimage.DescribeArtifactRelease(ctx, release.Entry, computeimage.ArtifactSplit, readers)
	}
	if err != nil {
		return err
	}
	if artifactInputUnchanged(metadata, metadataBefore) != nil || artifactInputUnchanged(rootfs, rootfsBefore) != nil {
		return computeimage.ErrArtifactUnavailable
	}
	if *verifyPath != "" {
		_, err = fmt.Fprintln(output, "Image bytes match the expected fingerprint and descriptor; import, format validity and boot were not tested.")
		return err
	}
	body, err := computeimage.EncodeArtifactRelease(release)
	if err != nil {
		return err
	}
	_, err = output.Write(body)
	return err
}

func artifactInputUnchanged(file *os.File, before os.FileInfo) error {
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) ||
		before.Size() != after.Size() || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return computeimage.ErrArtifactUnavailable
	}
	return nil
}
