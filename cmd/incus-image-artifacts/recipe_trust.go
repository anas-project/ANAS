package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/anas-project/ANAS/internal/computeimage"
	"gopkg.in/yaml.v3"
)

var errDebianBootstrapTrust = errors.New("Debian debootstrap build requires the distribution-provided archive keyring and enabled signature verification")

// debootstrap can merely warn and CONTINUE when the host lacks Debian's
// archive keyring. HTTPS and checksums from that same download are not a
// substitute for authenticated Release metadata. Check the default Debian
// source before reserving a revision or starting the root-capable builder.
// Other reviewed source downloaders retain their own verification contract;
// this is not a sandbox or a verifier for arbitrary release recipes.
func validateRecipeBuildTrust(ctx context.Context, recipe []byte, checkKeyring func(context.Context) error) error {
	if ctx == nil || checkKeyring == nil {
		return computeimage.ErrArtifactInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(recipe))
	if decoder.Decode(&document) != nil || decoder.Decode(&yaml.Node{}) != io.EOF || len(document.Content) != 1 {
		return computeimage.ErrArtifactInvalid
	}
	root := document.Content[0]
	source, err := recipeTrustField(root, "source")
	if err != nil {
		return computeimage.ErrArtifactInvalid
	}
	if source == nil {
		return nil // The builder, not this narrow preflight, validates other recipes.
	}
	downloader, err := recipeTrustField(source, "downloader")
	if err != nil || downloader == nil || downloader.Kind != yaml.ScalarNode || downloader.Tag != "!!str" {
		return computeimage.ErrArtifactInvalid
	}
	if downloader.Value != "debootstrap" {
		return nil
	}
	image, err := recipeTrustField(root, "image")
	if err != nil || image == nil {
		return computeimage.ErrArtifactInvalid
	}
	distribution, err := recipeTrustField(image, "distribution")
	if err != nil || distribution == nil || distribution.Kind != yaml.ScalarNode || distribution.Tag != "!!str" {
		return computeimage.ErrArtifactInvalid
	}
	if !strings.EqualFold(distribution.Value, "debian") {
		return nil
	}
	skip, err := recipeTrustField(source, "skip_verification")
	if err != nil || (skip != nil && (skip.Kind != yaml.ScalarNode || skip.Tag != "!!bool" || skip.Value != "false")) {
		return errDebianBootstrapTrust
	}
	if err := checkKeyring(ctx); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return errDebianBootstrapTrust
	}
	return ctx.Err()
}

// Do not interpret aliases, case variations or duplicate fields differently
// from the downstream YAML parser at a verification-sensitive boundary.
func recipeTrustField(mapping *yaml.Node, wanted string) (*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode || len(mapping.Content)%2 != 0 {
		return nil, computeimage.ErrArtifactInvalid
	}
	seen := map[string]bool{}
	var result *yaml.Node
	for index := 0; index < len(mapping.Content); index += 2 {
		key, value := mapping.Content[index], mapping.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" || seen[key.Value] ||
			(strings.EqualFold(key.Value, wanted) && key.Value != wanted) {
			return nil, computeimage.ErrArtifactInvalid
		}
		seen[key.Value] = true
		if key.Value == wanted {
			result = value
		}
	}
	return result, nil
}

func checkInstalledDebianBootstrapKeyring(ctx context.Context) error {
	return checkBootstrapKeyringAt(ctx, "/usr/share/keyrings", 0)
}
