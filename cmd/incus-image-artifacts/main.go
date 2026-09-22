// incus-image-artifacts prepares immutable release artifacts. Only the explicit
// build command starts a pinned distrobuilder; it never imports, prunes, or
// connects to the deployment daemon.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/computeimage"
)

const usage = `Usage:
  incus-image-artifacts init --archive ABSOLUTE_NEW_DIRECTORY
  incus-image-artifacts recipe --image forgejo-runner --architecture amd64|arm64 --interface incus_container|incus_vm
  incus-image-artifacts build --archive DIR --name NAME --revision REVISION --architecture amd64|arm64 --interface incus_container|incus_vm --recipe FILE --distrobuilder ABSOLUTE_BINARY --distrobuilder-sha256 SHA256 --forgejo-runner ABSOLUTE_BINARY --forgejo-runner-sha256 SHA256
  incus-image-artifacts record --archive DIR --name NAME --revision REVISION --architecture amd64|arm64 --interface incus_container|incus_vm --recipe FILE --format split --metadata FILE --rootfs FILE
  incus-image-artifacts record --archive DIR --name NAME --revision REVISION --architecture amd64|arm64 --interface incus_container|incus_vm --recipe FILE --format unified --image FILE
  incus-image-artifacts inspect --archive DIR --name NAME --revision REVISION --architecture amd64|arm64 --interface incus_container|incus_vm
  incus-image-artifacts export --archive DIR --name NAME --revision REVISION --architecture amd64|arm64 --interface incus_container|incus_vm --output-dir ABSOLUTE_NEW_DIRECTORY
  incus-image-artifacts catalog --archive DIR --previous-catalog FILE
  incus-image-artifacts catalog --archive DIR --first-release
  incus-image-artifacts bundle --archive DIR --previous-catalog FILE --output-dir ABSOLUTE_NEW_DIRECTORY
  incus-image-artifacts bundle --archive DIR --first-release --output-dir ABSOLUTE_NEW_DIRECTORY

All operations support --timeout (default 1h, maximum 24h).
Only JSON metadata is printed. Image bytes remain in the private local archive.
init never adopts an existing directory. record never invokes a builder or overwrites a revision.
build is release preparation on an isolated native Linux builder, not an apply/host action.
An existing revision is verified without rebuilding. Incomplete attempts require explicit recovery.
recipe prints a reviewed distrobuilder YAML recipe; it does not build.
export restores identical recorded bytes into a new private directory; it does not import.
bundle restores all split revisions in the Provider's images/ layout, writing catalog.json last.
bundle requires explicit history, never overwrites a destination, and does not sign or publish.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, output, diagnostic io.Writer) int {
	if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
		_, _ = io.WriteString(diagnostic, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if err := execute(ctx, args, output); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(diagnostic, usage)
			return 0
		}
		// Errors below are stable categories, not filesystem paths, input
		// fragments, recipe content or tool output.
		_, _ = fmt.Fprintln(diagnostic, err)
		return 1
	}
	return 0
}

func execute(parent context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return computeimage.ErrArtifactInvalid
	}
	command := args[0]
	if command != "init" && command != "record" && command != "inspect" && command != "catalog" && command != "build" && command != "export" && command != "recipe" && command != "bundle" {
		return computeimage.ErrArtifactInvalid
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	archivePath := flags.String("archive", "", "private local archive directory")
	timeout := flags.Duration("timeout", time.Hour, "operation time budget")
	var name, revision, architecture, iface, recipePath, format, metadata, rootfs, image, previousPath, builderPath, builderDigest, runnerPath, runnerDigest, outputDir, recipeImage string
	var firstRelease bool
	if command == "record" || command == "inspect" || command == "build" || command == "export" {
		flags.StringVar(&name, "name", "", "catalog name")
		flags.StringVar(&revision, "revision", "", "immutable revision")
	}
	if command == "record" || command == "inspect" || command == "build" || command == "export" || command == "recipe" {
		flags.StringVar(&architecture, "architecture", "", "amd64 or arm64")
		flags.StringVar(&iface, "interface", "", "incus_container or incus_vm")
	}
	if command == "recipe" {
		flags.StringVar(&recipeImage, "image", "", "reviewed default recipe name")
	}
	if command == "record" || command == "build" {
		flags.StringVar(&recipePath, "recipe", "", "reviewed self-contained recipe")
	}
	if command == "build" {
		flags.StringVar(&builderPath, "distrobuilder", "", "absolute trusted distrobuilder executable")
		flags.StringVar(&builderDigest, "distrobuilder-sha256", "", "independently verified builder digest")
		flags.StringVar(&runnerPath, "forgejo-runner", "", "absolute trusted forgejo-runner executable")
		flags.StringVar(&runnerDigest, "forgejo-runner-sha256", "", "independently verified forgejo-runner digest")
	}
	if command == "record" {
		flags.StringVar(&format, "format", computeimage.ArtifactSplit, "split or unified")
		flags.StringVar(&metadata, "metadata", "", "split metadata file")
		flags.StringVar(&rootfs, "rootfs", "", "split rootfs or qcow2 file")
		flags.StringVar(&image, "image", "", "unified tarball")
	}
	if command == "catalog" || command == "bundle" {
		flags.StringVar(&previousPath, "previous-catalog", "", "previous trusted catalog")
		flags.BoolVar(&firstRelease, "first-release", false, "explicitly no prior published history")
	}
	if command == "export" || command == "bundle" {
		flags.StringVar(&outputDir, "output-dir", "", "new private directory for restored artifact bytes")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return flag.ErrHelp
		}
		return computeimage.ErrArtifactInvalid
	}
	if flags.NArg() != 0 || *timeout < time.Second || *timeout > 24*time.Hour || parent == nil || output == nil {
		return computeimage.ErrArtifactInvalid
	}
	if command != "recipe" && *archivePath == "" {
		return computeimage.ErrArtifactInvalid
	}
	if command == "recipe" && *archivePath != "" {
		return computeimage.ErrArtifactInvalid
	}
	if (command == "catalog" || command == "bundle") && ((previousPath != "") == firstRelease) {
		return computeimage.ErrArtifactInvalid
	}
	if command == "bundle" && outputDir == "" {
		return computeimage.ErrArtifactInvalid
	}
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	if command == "recipe" {
		if recipeImage != "forgejo-runner" {
			return computeimage.ErrArtifactInvalid
		}
		body, err := computeimage.ForgejoRunnerRecipe(computeimage.Target{Architecture: architecture, Interface: iface})
		if err != nil {
			return err
		}
		if n, err := output.Write(body); err != nil || n != len(body) {
			return errors.New("recipe output failed")
		}
		return nil
	}
	directory, err := filepath.Abs(*archivePath)
	if err != nil {
		return computeimage.ErrArtifactInvalid
	}
	archive, err := computeimage.OpenArtifactArchive(ctx, directory, command == "init")
	if err != nil {
		return err
	}
	defer archive.Close()
	reference := computeimage.Reference{Catalog: "anas", Name: name, Revision: revision}
	target := computeimage.Target{Architecture: architecture, Interface: iface}
	var result any
	switch command {
	case "init":
		result = struct {
			Initialized bool `json:"initialized"`
		}{true}
	case "build":
		if builderPath == "" || !filepath.IsAbs(builderPath) || filepath.Clean(builderPath) != builderPath {
			return computeimage.ErrArtifactInvalid
		}
		recipe, err := computeimage.ReadArtifactRecipe(ctx, recipePath)
		if err != nil {
			return err
		}
		// Complete platform, privilege and executable preflight before reserving
		// a revision. A missing tool must not consume a build attempt.
		builder, err := prepareDistrobuilder(ctx, builderPath, builderDigest, target)
		if err != nil {
			return err
		}
		defer builder.Close()
		input, err := prepareForgejoRunnerInput(ctx, runnerPath, runnerDigest, target)
		if err != nil {
			return err
		}
		release, existing, err := archive.BuildOnce(ctx, reference, target, recipe, builderDigest, []computeimage.ArtifactBuildInput{input}, builder.Build)
		if err != nil {
			return err
		}
		result = struct {
			Existing bool                         `json:"existing"`
			Release  computeimage.ArtifactRelease `json:"release"`
		}{existing, release}
	case "record":
		var sources []string
		switch format {
		case computeimage.ArtifactSplit:
			if metadata == "" || rootfs == "" || image != "" {
				return computeimage.ErrArtifactInvalid
			}
			sources = []string{metadata, rootfs}
		case computeimage.ArtifactUnified:
			if image == "" || metadata != "" || rootfs != "" {
				return computeimage.ErrArtifactInvalid
			}
			sources = []string{image}
		default:
			return computeimage.ErrArtifactInvalid
		}
		recipe, err := computeimage.ReadArtifactRecipe(ctx, recipePath)
		if err != nil {
			return err
		}
		release, existing, err := archive.Record(ctx, reference, target, recipe, format, sources)
		if err != nil {
			return err
		}
		result = struct {
			Existing bool                         `json:"existing"`
			Release  computeimage.ArtifactRelease `json:"release"`
		}{existing, release}
	case "inspect":
		release, err := archive.Inspect(ctx, reference, target)
		if err != nil {
			return err
		}
		result = release
	case "export":
		if outputDir == "" {
			return computeimage.ErrArtifactInvalid
		}
		destination, err := filepath.Abs(outputDir)
		if err != nil {
			return computeimage.ErrArtifactInvalid
		}
		exported, err := archive.Export(ctx, reference, target, destination)
		if err != nil {
			return err
		}
		result = exported
	case "catalog", "bundle":
		var previous []computeimage.Entry
		if !firstRelease {
			previous, err = computeimage.ReadArtifactCatalog(ctx, previousPath)
			if err != nil {
				return err
			}
		}
		if command == "bundle" {
			destination, err := filepath.Abs(outputDir)
			if err != nil {
				return computeimage.ErrArtifactInvalid
			}
			bundle, err := archive.ExportBundle(ctx, previous, destination)
			if err != nil {
				return err
			}
			result = bundle
		} else {
			entries, err := archive.Catalog(ctx, previous)
			if err != nil {
				return err
			}
			result = entries
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return computeimage.ErrArtifactInvalid
	}
	body = append(body, '\n')
	if n, err := output.Write(body); err != nil || n != len(body) {
		return errors.New("artifact metadata output failed; inspect the existing archive before retrying")
	}
	return nil
}
