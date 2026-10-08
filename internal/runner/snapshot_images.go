package runner

// PostgreSQL recovery needs the binaries that wrote the captured data, even
// after extension consumers were removed. These private metadata companions
// travel through the existing meta/ channel.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/compose"
	"gopkg.in/yaml.v3"
)

const (
	snapshotImagesArchive = "compose-images.tar"
	snapshotImagesIndex   = "compose-images.yml"
)

var recoveryImageIDPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var snapshotImagesComposeDetect = detectComposeForExecution

type snapshotServiceImage struct {
	Module  string `yaml:"module"`
	Service string `yaml:"service"`
	ID      string `yaml:"id"`
}

type snapshotImageMetadata struct {
	DeploymentID  string                 `yaml:"deployment_id"`
	ArchiveSHA256 string                 `yaml:"archive_sha256"`
	Services      []snapshotServiceImage `yaml:"services"`
}

// Runtime-only inventory is prepared while old containers still exist. It
// carries the same frozen Docker endpoint through down and archive capture.
type snapshotImageInventory struct {
	metadata    snapshotImageMetadata
	ctx         context.Context
	environment []string
}

var snapshotImageDockerCommand = func(ctx context.Context, environment []string, stdout io.Writer, args ...string) error {
	cmd := externalCommandContext(ctx, "docker", args...)
	cmd.Env = append([]string(nil), environment...)
	cmd.Stdout, cmd.Stderr = stdout, io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("docker image recovery command failed: %w", err)
	}
	return nil
}

func deploymentNeedsImageRecovery(manifest *deploymentManifest) bool {
	return len(deploymentPostgresProviders(manifest)) > 0
}

func recoveryComposeFile(module deploymentModule) (string, error) {
	file := module.ComposeFile
	if file == "" {
		file = "docker-compose.yml"
	}
	if filepath.IsAbs(file) || filepath.Clean(file) != file || file == ".." || strings.HasPrefix(file, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid recovery Compose file")
	}
	return file, nil
}

// prepareSnapshotImageInventory must precede a compose down. A container's
// immutable Image field takes precedence over a tag that may have moved. A
// one-shot service with no container uses its locally available configured ID.
func prepareSnapshotImageInventory(artifact string, cli compose.CLI, ctx context.Context, restricted bool, force ...bool) (*snapshotImageInventory, error) {
	manifest, err := loadDeploymentManifest(artifact)
	forced := len(force) != 0 && force[0]
	if err != nil || (!deploymentNeedsImageRecovery(manifest) && !forced) {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	environment := (&app{restrictedProcessEnvironment: restricted}).commandEnvironment(nil)
	if len(cli.Bin) == 0 {
		cli, err = detectComposeForExecution(ctx, restricted)
		if err != nil {
			return nil, err
		}
	}
	inventory := &snapshotImageInventory{metadata: snapshotImageMetadata{DeploymentID: manifest.ID},
		ctx: ctx, environment: cli.Environment(environment, nil)}
	base := filepath.Dir(filepath.Dir(artifact))
	names := make([]string, 0, len(manifest.Modules))
	for name := range manifest.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		module := manifest.Modules[name]
		if module.RuntimeType != "compose" {
			continue
		}
		if !resourceIdentifierPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid recovery module name")
		}
		file, err := recoveryComposeFile(module)
		if err != nil {
			return nil, err
		}
		sourceID := module.ArtifactDeployment
		if sourceID == "" {
			sourceID = manifest.ID
		}
		if err := validateDeploymentID(sourceID); err != nil {
			return nil, err
		}
		dir := filepath.Join(base, "deployments", sourceID, "modules", name)
		env, err := parseEnvFile(filepath.Join(dir, ".env"))
		if err != nil {
			return nil, fmt.Errorf("read recovery environment for %s: %w", name, err)
		}
		project, err := composeProjectName(name, env)
		if err != nil {
			return nil, err
		}
		var ownership bytes.Buffer
		if err := snapshotImageDockerCommand(ctx, inventory.environment, &ownership, "ps", "--all", "--filter", "label=com.docker.compose.project="+project,
			"--format", `{{.Label "com.docker.compose.project.working_dir"}}`); err != nil {
			return nil, err
		}
		if err := (&app{workspace: workspaceOf(base)}).validateComposeProjectOwners(project, parseComposeProjectOwners(ownership.Bytes()), nil); err != nil {
			return nil, err
		}
		commandEnv := (&app{restrictedProcessEnvironment: restricted}).commandEnvironment(env)
		configJSON, err := cli.OutputFileContext(ctx, dir, project, file, commandEnv, true, "config", "--format", "json")
		if err != nil {
			return nil, fmt.Errorf("resolve recovery Compose images for %s: %w", name, err)
		}
		var config struct {
			Services map[string]struct {
				Image string `json:"image"`
			} `json:"services"`
		}
		if err := json.Unmarshal([]byte(configJSON), &config); err != nil || len(config.Services) == 0 {
			return nil, fmt.Errorf("invalid recovery Compose image configuration for %s", name)
		}
		services := make([]string, 0, len(config.Services))
		for service := range config.Services {
			services = append(services, service)
		}
		sort.Strings(services)
		runtimeImages := map[string]string{}
		pendingImages := map[int]string{}
		for _, service := range services {
			image := config.Services[service].Image
			if image == "" {
				return nil, fmt.Errorf("recovery service %s.%s has no image", name, service)
			}
			var containers bytes.Buffer
			if err := snapshotImageDockerCommand(ctx, inventory.environment, &containers, "ps", "--all", "--filter", "label=com.docker.compose.project="+project,
				"--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}"); err != nil {
				return nil, fmt.Errorf("inspect recovery service %s.%s: %w", name, service, err)
			}
			containerIDs := strings.Fields(containers.String())
			if len(containerIDs) == 0 {
				pendingImages[len(inventory.metadata.Services)] = image
				inventory.metadata.Services = append(inventory.metadata.Services, snapshotServiceImage{Module: name, Service: service})
				continue
			}
			args := append([]string{"container", "inspect", "--format", "{{.Image}}"}, containerIDs...)
			var output bytes.Buffer
			if err := snapshotImageDockerCommand(ctx, inventory.environment, &output, args...); err != nil {
				return nil, fmt.Errorf("capture image ID for %s.%s: %w", name, service, err)
			}
			ids := strings.Fields(output.String())
			if len(ids) == 0 || !recoveryImageIDPattern.MatchString(ids[0]) {
				return nil, fmt.Errorf("invalid image ID for recovery service %s.%s", name, service)
			}
			for _, id := range ids {
				if id != ids[0] {
					return nil, fmt.Errorf("recovery service %s.%s has inconsistent container images", name, service)
				}
			}
			if previous := runtimeImages[image]; previous != "" && previous != ids[0] {
				return nil, fmt.Errorf("recovery module %s has inconsistent container images for one configured image", name)
			}
			runtimeImages[image] = ids[0]
			inventory.metadata.Services = append(inventory.metadata.Services, snapshotServiceImage{Module: name, Service: service, ID: ids[0]})
		}
		for index, ref := range pendingImages {
			id := runtimeImages[ref]
			if id == "" {
				var output bytes.Buffer
				if err := snapshotImageDockerCommand(ctx, inventory.environment, &output, "image", "inspect", "--format", "{{.Id}}", ref); err != nil {
					return nil, fmt.Errorf("capture inactive image for %s.%s: %w", name, inventory.metadata.Services[index].Service, err)
				}
				id = strings.TrimSpace(output.String())
				if !recoveryImageIDPattern.MatchString(id) {
					return nil, fmt.Errorf("invalid inactive recovery image ID")
				}
			}
			// Provision jobs using the server's configured image must recover
			// with its actual binary, even if a local tag was overwritten.
			inventory.metadata.Services[index].ID = id
		}
	}
	if len(inventory.metadata.Services) == 0 {
		return nil, fmt.Errorf("PostgreSQL recovery requires Compose service images")
	}
	return inventory, nil
}

// Retain the running deployment before any build can move its image tags.
// Docker's containerd store may stop resolving an untagged image ID even while
// a container still refers to it. These workspace-owned cache references are
// bounded by service identity and replaced with the next active inventory.
// Recovery archives continue to use IDs, never these tags.
func retainActiveSnapshotImages(base string, cli compose.CLI, ctx context.Context, restricted bool) error {
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment == "" {
		return err
	}
	artifact := deploymentArtifactDir(base, active.ActiveDeployment)
	inventory, err := prepareSnapshotImageInventory(artifact, cli, ctx, restricted)
	if err != nil || inventory == nil {
		return err
	}
	return retainSnapshotImageInventory(base, inventory)
}

func retainSnapshotImageInventory(base string, inventory *snapshotImageInventory) error {
	workspaceHash := sha256.Sum256([]byte(filepath.Clean(base)))
	repository := fmt.Sprintf("anas-recovery-%x", workspaceHash[:16])
	for _, service := range inventory.metadata.Services {
		if !recoveryImageIDPattern.MatchString(service.ID) {
			return fmt.Errorf("invalid retained recovery image ID")
		}
		serviceHash := sha256.Sum256([]byte(service.Module + "\x00" + service.Service))
		reference := fmt.Sprintf("%s:%x", repository, serviceHash[:16])
		if err := snapshotImageDockerCommand(inventory.ctx, inventory.environment, io.Discard,
			"image", "tag", service.ID, reference); err != nil {
			return fmt.Errorf("retain actual image for %s.%s before build: %w", service.Module, service.Service, err)
		}
	}
	return nil
}

func saveSnapshotImages(root, sourceArtifact string, inventory *snapshotImageInventory) error {
	if inventory == nil {
		return nil
	}
	manifest, err := loadDeploymentManifest(sourceArtifact)
	if err != nil {
		return err
	}
	if manifest.ID != inventory.metadata.DeploymentID {
		return fmt.Errorf("recovery image inventory belongs to a different deployment")
	}
	// Inherited runtime modules must become self-contained in this recovery
	// copy. Rewriting its manifest atomically does not mutate hard-linked input.
	for name, module := range manifest.Modules {
		if source := module.ArtifactDeployment; source != "" && source != manifest.ID {
			if err := validateDeploymentID(source); err != nil {
				return err
			}
			sourceDir := filepath.Join(filepath.Dir(sourceArtifact), source, "modules", name)
			targetDir := filepath.Join(snapshotArtifactDir(root), "modules", name)
			if err := os.RemoveAll(targetDir); err != nil {
				return err
			}
			if err := copyCandidateArtifact(sourceDir, targetDir); err != nil {
				return err
			}
			base := filepath.Dir(filepath.Dir(sourceArtifact))
			if err := rewriteCandidateTextTree(targetDir, source, manifest.ID, base); err != nil {
				return err
			}
			if err := rewriteCandidateEnvIdentity(filepath.Join(targetDir, ".env"), manifest.ID); err != nil {
				return err
			}
		}
		module.ArtifactDeployment = manifest.ID
		manifest.Modules[name] = module
	}
	if err := writeYAMLAtomic(filepath.Join(snapshotArtifactDir(root), "deployment.yml"), manifest, 0600); err != nil {
		return err
	}
	if err := os.MkdirAll(snapshotMetaDir(root), 0700); err != nil {
		return err
	}
	ids, seen := []string{}, map[string]bool{}
	for _, service := range inventory.metadata.Services {
		if !seen[service.ID] {
			ids = append(ids, service.ID)
			seen[service.ID] = true
		}
	}
	sort.Strings(ids)
	archive := snapshotMetaEntry(root, snapshotImagesArchive)
	args := append([]string{"image", "save", "--output", archive}, ids...)
	if err := snapshotImageDockerCommand(inventory.ctx, inventory.environment, io.Discard, args...); err != nil {
		return err
	}
	if err := os.Chmod(archive, 0600); err != nil {
		return err
	}
	if err := verifyUntaggedImageArchive(archive); err != nil {
		return err
	}
	digest, err := fileDigest(archive)
	if err != nil {
		return err
	}
	inventory.metadata.ArchiveSHA256 = digest
	return writeYAMLAtomic(snapshotMetaEntry(root, snapshotImagesIndex), inventory.metadata, 0600)
}

// Loading a save made from IDs must not overwrite repository tags used by
// unrelated deployments. Reject a tagged archive rather than retagging globally.
func verifyUntaggedImageArchive(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := tar.NewReader(f)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return fmt.Errorf("recovery image archive has no Docker manifest")
		}
		if err != nil {
			return err
		}
		if header.Name != "manifest.json" {
			continue
		}
		var manifest []struct {
			RepoTags []string `json:"RepoTags"`
		}
		if err := json.NewDecoder(io.LimitReader(reader, 4<<20)).Decode(&manifest); err != nil || len(manifest) == 0 {
			return fmt.Errorf("invalid recovery Docker image manifest")
		}
		for _, image := range manifest {
			if len(image.RepoTags) != 0 {
				return fmt.Errorf("recovery image archive contains repository tags")
			}
		}
		return nil
	}
}

func loadSnapshotImageMetadata(root string) (*snapshotImageMetadata, error) {
	var manifest deploymentManifest
	if err := readYAML(filepath.Join(snapshotArtifactDir(root), "deployment.yml"), &manifest); err != nil {
		return nil, err
	}
	if !deploymentNeedsImageRecovery(&manifest) && !exists(snapshotMetaEntry(root, snapshotImagesIndex)) && !exists(snapshotMetaEntry(root, snapshotImagesArchive)) {
		return nil, nil
	}
	var metadata snapshotImageMetadata
	if err := readYAML(snapshotMetaEntry(root, snapshotImagesIndex), &metadata); err != nil {
		return nil, fmt.Errorf("recovery image metadata is missing or unreadable: %w", err)
	}
	if manifest.APIVersion != deploymentAPIVersion || validateDeploymentID(manifest.ID) != nil || metadata.DeploymentID != manifest.ID || len(metadata.Services) == 0 || !recoveryImageIDPattern.MatchString(metadata.ArchiveSHA256) {
		return nil, fmt.Errorf("invalid recovery image metadata")
	}
	seen := map[string]bool{}
	for _, image := range metadata.Services {
		module, exists := manifest.Modules[image.Module]
		key := image.Module + "." + image.Service
		if !exists || module.RuntimeType != "compose" || image.Service == "" || seen[key] || !recoveryImageIDPattern.MatchString(image.ID) {
			return nil, fmt.Errorf("invalid recovery service image metadata")
		}
		seen[key] = true
	}
	expected := 0
	for name, module := range manifest.Modules {
		if module.RuntimeType != "compose" {
			continue
		}
		if !resourceIdentifierPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid recovery module name")
		}
		file, err := recoveryComposeFile(module)
		if err != nil {
			return nil, err
		}
		var tree yaml.Node
		if err := readYAML(filepath.Join(snapshotArtifactDir(root), "modules", name, file), &tree); err != nil {
			return nil, err
		}
		if len(tree.Content) != 1 {
			return nil, fmt.Errorf("invalid recovery Compose YAML")
		}
		services := mappingValue(tree.Content[0], "services")
		if services == nil || services.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("invalid recovery Compose services")
		}
		for i := 0; i < len(services.Content); i += 2 {
			if !seen[name+"."+services.Content[i].Value] || mappingValue(services.Content[i+1], "image") == nil {
				return nil, fmt.Errorf("recovery image inventory is incomplete for a Compose service")
			}
			expected++
		}
	}
	if expected != len(metadata.Services) {
		return nil, fmt.Errorf("recovery image metadata contains an unknown Compose service")
	}
	return &metadata, nil
}

func verifySnapshotImages(root string) error {
	metadata, err := loadSnapshotImageMetadata(root)
	if err != nil || metadata == nil {
		return err
	}
	digest, err := fileDigest(snapshotMetaEntry(root, snapshotImagesArchive))
	if err != nil {
		return fmt.Errorf("recovery image archive is missing or unreadable: %w", err)
	}
	if digest != metadata.ArchiveSHA256 {
		return fmt.Errorf("recovery image archive checksum does not match")
	}
	return verifyUntaggedImageArchive(snapshotMetaEntry(root, snapshotImagesArchive))
}

// A maintenance transition may introduce the first extension declaration.
// Its old deployment still needs matching binaries in the recovery set.
func verifyRequiredSnapshotImages(root string) error {
	if !exists(snapshotMetaEntry(root, snapshotImagesIndex)) || !exists(snapshotMetaEntry(root, snapshotImagesArchive)) {
		return fmt.Errorf("PostgreSQL maintenance recovery requires captured image metadata and archive")
	}
	return verifySnapshotImages(root)
}

func loadSnapshotImages(root string, options ...snapshotOptions) error {
	if err := verifySnapshotImages(root); err != nil {
		return err
	}
	metadata, err := loadSnapshotImageMetadata(root)
	if err != nil || metadata == nil {
		return err
	}
	var opts snapshotOptions
	if len(options) != 0 {
		opts = options[0]
	}
	ctx := opts.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cli, err := snapshotImagesComposeDetect(ctx, opts.restrictedProcessEnvironment)
	if err != nil {
		return err
	}
	environment := cli.Environment((&app{restrictedProcessEnvironment: opts.restrictedProcessEnvironment}).commandEnvironment(nil), nil)
	if err := snapshotImageDockerCommand(ctx, environment, io.Discard, "image", "load", "--input", snapshotMetaEntry(root, snapshotImagesArchive)); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, image := range metadata.Services {
		if seen[image.ID] {
			continue
		}
		seen[image.ID] = true
		var output bytes.Buffer
		if err := snapshotImageDockerCommand(ctx, environment, &output, "image", "inspect", "--format", "{{.Id}}", image.ID); err != nil {
			return err
		}
		if strings.TrimSpace(output.String()) != image.ID {
			return fmt.Errorf("loaded recovery image ID does not match")
		}
	}
	return nil
}

func pinRestoredSnapshotImages(root, targetArtifact string) error {
	metadata, err := loadSnapshotImageMetadata(root)
	if err != nil || metadata == nil {
		return err
	}
	manifest, err := loadDeploymentManifest(targetArtifact)
	if err != nil {
		return err
	}
	byModule := map[string]map[string]string{}
	for _, image := range metadata.Services {
		if byModule[image.Module] == nil {
			byModule[image.Module] = map[string]string{}
		}
		byModule[image.Module][image.Service] = image.ID
	}
	for name, module := range manifest.Modules {
		if module.RuntimeType != "compose" {
			continue
		}
		if !resourceIdentifierPattern.MatchString(name) {
			return fmt.Errorf("invalid recovery module name")
		}
		file, err := recoveryComposeFile(module)
		if err != nil {
			return err
		}
		// Always use the captured module; never mutate a historical artifact
		// that this deployment inherited, or a hard link to the saved copy.
		dir := filepath.Join(targetArtifact, "modules", name)
		if source := module.ArtifactDeployment; source != "" && source != manifest.ID {
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			if _, err := copyDeploymentTree(filepath.Join(snapshotArtifactDir(root), "modules", name), dir); err != nil {
				return err
			}
		}
		path := filepath.Join(dir, file)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var tree yaml.Node
		if err := yaml.Unmarshal(body, &tree); err != nil || len(tree.Content) != 1 {
			return fmt.Errorf("invalid recovery Compose YAML")
		}
		services := mappingValue(tree.Content[0], "services")
		if services == nil || services.Kind != yaml.MappingNode {
			return fmt.Errorf("invalid recovery Compose services")
		}
		for i := 0; i < len(services.Content); i += 2 {
			serviceName := services.Content[i].Value
			imageID := byModule[name][serviceName]
			if imageID == "" {
				return fmt.Errorf("recovery image inventory is incomplete for a Compose service")
			}
			image := mappingValue(services.Content[i+1], "image")
			if image == nil {
				return fmt.Errorf("recovery Compose service has no image field")
			}
			image.Kind, image.Tag, image.Value = yaml.ScalarNode, "!!str", imageID
		}
		if err := writeYAMLAtomic(path, &tree, 0600); err != nil {
			return err
		}
		module.ArtifactDeployment = manifest.ID
		module.RenderDigest, err = normalizedModuleDigest(dir, targetArtifact)
		if err != nil {
			return err
		}
		manifest.Modules[name] = module
	}
	return writeYAMLAtomic(filepath.Join(targetArtifact, "deployment.yml"), manifest, 0600)
}

// Send modes carry the same files inside meta.tar. Verify its image checksum
// without extracting a multi-gigabyte archive into a second temporary copy.
func verifySnapshotImagesTar(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := tar.NewReader(f)
	var manifest deploymentManifest
	var metadata snapshotImageMetadata
	var digest string
	foundManifest, foundMetadata, foundArchive := false, false, false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch filepath.ToSlash(filepath.Clean(header.Name)) {
		case "deployment/deployment.yml":
			if foundManifest {
				return fmt.Errorf("duplicate recovery deployment metadata")
			}
			foundManifest = true
			if err := yaml.NewDecoder(io.LimitReader(reader, 4<<20)).Decode(&manifest); err != nil {
				return err
			}
		case "meta/" + snapshotImagesIndex:
			if foundMetadata {
				return fmt.Errorf("duplicate recovery image metadata")
			}
			foundMetadata = true
			if err := yaml.NewDecoder(io.LimitReader(reader, 4<<20)).Decode(&metadata); err != nil {
				return err
			}
		case "meta/" + snapshotImagesArchive:
			if foundArchive {
				return fmt.Errorf("duplicate recovery image archive")
			}
			foundArchive = true
			hash := sha256.New()
			if _, err := io.Copy(hash, reader); err != nil {
				return err
			}
			digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
		}
	}
	if !foundManifest {
		return fmt.Errorf("metadata archive has no deployment manifest")
	}
	if !deploymentNeedsImageRecovery(&manifest) && !foundMetadata && !foundArchive {
		return nil
	}
	if !foundMetadata || !foundArchive || metadata.DeploymentID != manifest.ID || len(metadata.Services) == 0 || metadata.ArchiveSHA256 != digest {
		return fmt.Errorf("recovery images are missing or corrupt in metadata archive")
	}
	return nil
}
