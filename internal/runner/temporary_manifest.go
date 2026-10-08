package runner

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var temporaryDirectoryNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var temporaryDirectoryModePattern = regexp.MustCompile(`^0[0-7]{3}$`)
var temporaryFilesystemPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

func parseTemporaryDirectoryMode(value string) (os.FileMode, error) {
	mode, err := strconv.ParseUint(value, 8, 12)
	if err != nil || !temporaryDirectoryModePattern.MatchString(value) {
		return 0, fmt.Errorf("temporary mode must be a quoted octal mode such as 0750")
	}
	if mode&0700 != 0700 || mode&0022 != 0 {
		return 0, fmt.Errorf("temporary mode must permit owner access and must not be group or world writable")
	}
	return os.FileMode(mode), nil
}

func cloneTemporaryDirectories(in []TemporaryDirectory) []TemporaryDirectory {
	out := append([]TemporaryDirectory(nil), in...)
	for i := range out {
		out[i].Filesystem = append([]string(nil), in[i].Filesystem...)
		out[i].RequiredFeatures = append([]string(nil), in[i].RequiredFeatures...)
	}
	return out
}

func normalizeTemporaryDirectories(module, runtime, dir, composeFile string, declarations []TemporaryDirectory) ([]TemporaryDirectory, error) {
	if len(declarations) == 0 {
		return nil, nil
	}
	if runtime != "compose" {
		return nil, fmt.Errorf("module %q temporary_directories requires compose runtime", module)
	}
	services, _, err := temporaryComposeServices(filepath.Join(dir, composeFile))
	if err != nil {
		return nil, fmt.Errorf("module %q temporary_directories: %w", module, err)
	}
	names := map[string]bool{}
	out := cloneTemporaryDirectories(declarations)
	for i := range out {
		d := &out[i]
		if !temporaryDirectoryNamePattern.MatchString(d.Name) || names[d.Name] {
			return nil, fmt.Errorf("module %q temporary_directories has invalid or duplicate name %q", module, d.Name)
		}
		names[d.Name] = true
		if d.Lifecycle != "container" {
			return nil, fmt.Errorf("module %q temporary directory %q lifecycle must be container", module, d.Name)
		}
		if d.UID < 0 || d.GID < 0 || !temporaryDirectoryModePattern.MatchString(d.Mode) {
			return nil, fmt.Errorf("module %q temporary directory %q requires nonnegative uid/gid and a quoted mode such as 0750", module, d.Name)
		}
		if _, err := parseTemporaryDirectoryMode(d.Mode); err != nil {
			return nil, fmt.Errorf("module %q temporary directory %q: %w", module, d.Name, err)
		}
		if d.MinFreeBytes == 0 || d.MinFreeInodes == 0 {
			return nil, fmt.Errorf("module %q temporary directory %q requires positive min_free_bytes and min_free_inodes", module, d.Name)
		}
		if !path.IsAbs(d.Target) || path.Clean(d.Target) != d.Target || d.Target == "/" || strings.ContainsAny(d.Target, "\x00\r\n$") {
			return nil, fmt.Errorf("module %q temporary directory %q target must be a clean absolute container directory", module, d.Name)
		}
		available := map[string]bool{}
		for name := range services {
			available[name] = true
		}
		d.Service = resolveComposeService(available, d.Service)
		if d.Service == "" {
			return nil, fmt.Errorf("module %q temporary directory %q names an unknown Compose service", module, d.Name)
		}
		for _, filesystem := range d.Filesystem {
			if !temporaryFilesystemPattern.MatchString(filesystem) {
				return nil, fmt.Errorf("module %q temporary directory %q has invalid filesystem %q", module, d.Name, filesystem)
			}
		}
		for _, feature := range d.RequiredFeatures {
			if feature != "hardlink" && feature != "exec" {
				return nil, fmt.Errorf("module %q temporary directory %q has unknown required feature %q", module, d.Name, feature)
			}
		}
		for j := 0; j < i; j++ {
			if out[j].Service == d.Service && temporaryTargetsOverlap(out[j].Target, d.Target) {
				return nil, fmt.Errorf("module %q temporary directory targets overlap in service %q", module, d.Service)
			}
		}
		if err := validateTemporaryComposeService(services[d.Service], d.Target); err != nil {
			return nil, fmt.Errorf("module %q temporary directory %q: %w", module, d.Name, err)
		}
	}
	return out, nil
}

func temporaryTargetsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, strings.TrimSuffix(b, "/")+"/") || strings.HasPrefix(b, strings.TrimSuffix(a, "/")+"/")
}

func temporaryMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode {
		return temporaryMappingValue(node.Alias, key)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func temporaryComposeServices(file string) (map[string]*yaml.Node, *yaml.Node, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(b, &document); err != nil {
		return nil, nil, err
	}
	if len(document.Content) != 1 {
		return nil, nil, fmt.Errorf("invalid Compose document")
	}
	services := temporaryMappingValue(document.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("Compose services must be a mapping")
	}
	result := map[string]*yaml.Node{}
	for i := 0; i+1 < len(services.Content); i += 2 {
		result[services.Content[i].Value] = services.Content[i+1]
	}
	return result, &document, nil
}

func validateTemporaryComposeService(service *yaml.Node, target string) error {
	if service == nil || service.Kind != yaml.MappingNode {
		return fmt.Errorf("temporary storage service must be an explicit mapping")
	}
	for _, inherited := range []string{"<<", "extends"} {
		if temporaryMappingValue(service, inherited) != nil {
			return fmt.Errorf("temporary storage service must declare its mounts directly, without %s", inherited)
		}
	}
	// A single interpolation key identifies one container instance. Scaling
	// would share that directory, violating the per-instance lease contract.
	for _, replicas := range []*yaml.Node{temporaryMappingValue(service, "scale"), temporaryMappingValue(temporaryMappingValue(service, "deploy"), "replicas")} {
		if replicas != nil && replicas.Value != "1" {
			return fmt.Errorf("temporary storage service must have exactly one replica")
		}
	}
	if temporaryMappingValue(service, "volumes_from") != nil {
		return fmt.Errorf("temporary storage cannot verify volumes_from mount targets")
	}
	for _, key := range []string{"volumes", "tmpfs"} {
		mounts := temporaryMappingValue(service, key)
		if mounts == nil {
			continue
		}
		if mounts.Kind == yaml.ScalarNode && key == "tmpfs" {
			mounts = &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{mounts}}
		}
		if mounts.Kind != yaml.SequenceNode {
			return fmt.Errorf("%s must be a sequence when declaring temporary storage", key)
		}
		for _, mount := range mounts.Content {
			mountTarget := ""
			if mount.Kind == yaml.MappingNode {
				if node := temporaryMappingValue(mount, "target"); node != nil {
					mountTarget = node.Value
				}
			} else if mount.Kind == yaml.ScalarNode {
				parts := strings.Split(mount.Value, ":")
				mountTarget = parts[0]
				if key == "volumes" && len(parts) > 1 {
					mountTarget = parts[1]
				}
			}
			if !path.IsAbs(mountTarget) || strings.Contains(mountTarget, "$") {
				return fmt.Errorf("cannot verify mount target %q", mountTarget)
			}
			if temporaryTargetsOverlap(path.Clean(mountTarget), target) {
				return fmt.Errorf("temporary target %q overlaps existing mount %q", target, mountTarget)
			}
		}
	}
	return nil
}

// renderTemporaryDirectoryMounts persists only placeholders. The runtime
// driver supplies the current module's lease immediately before Compose.
func renderTemporaryDirectoryMounts(mod Module, dir string) error {
	if len(mod.TemporaryDirectories) == 0 {
		return nil
	}
	file := filepath.Join(dir, mod.ComposeFile)
	services, document, err := temporaryComposeServices(file)
	if err != nil {
		return err
	}
	for _, declaration := range mod.TemporaryDirectories {
		service := services[declaration.Service]
		if err := validateTemporaryComposeService(service, declaration.Target); err != nil {
			return fmt.Errorf("module %q temporary directory %q: %w", mod.Name, declaration.Name, err)
		}
		volumes := temporaryMappingValue(service, "volumes")
		if volumes == nil {
			volumes = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			service.Content = append(service.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "volumes"}, volumes)
		}
		var mount yaml.Node
		if err := mount.Encode(map[string]any{
			"type": "bind", "source": "${" + temporaryDirectoryEnvironmentKey(declaration.Name) + "}", "target": declaration.Target,
			"bind": map[string]any{"create_host_path": false},
		}); err != nil {
			return err
		}
		volumes.Content = append(volumes.Content, &mount)
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return os.WriteFile(file, output.Bytes(), 0644)
}
