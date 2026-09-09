// check-shared-build validates the repository's named Docker build contexts.
package main

import (
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
)

type imageEntry struct {
	Module, Context, Dockerfile string
	Shared                      []string `json:"shared_paths"`
}
type moduleEntry struct {
	Module string
	Shared []string `json:"shared_contexts"`
}

func main() {
	if err := check("."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("shared build contexts valid")
}
func check(root string) error {
	var images []imageEntry
	var modules []moduleEntry
	for path, target := range map[string]any{".github/images.json": &images, ".github/modules.json": &modules} {
		body, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		if err = json.Unmarshal(body, target); err != nil {
			return err
		}
	}
	contexts := map[string][]string{}
	for _, m := range modules {
		contexts[m.Module] = m.Shared
	}
	for _, image := range images {
		body, err := os.ReadFile(filepath.Join(root, image.Dockerfile))
		if err != nil {
			return err
		}
		copied, err := sharedCopies(string(body))
		if err != nil {
			return fmt.Errorf("%s: %w", image.Dockerfile, err)
		}
		if len(image.Shared) == 0 && len(copied) == 0 {
			continue
		}
		declared := map[string]bool{}
		for _, path := range image.Shared {
			if !filepath.IsLocal(path) || filepath.Clean(path) != path {
				return fmt.Errorf("%s: invalid shared path %q", image.Module, path)
			}
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				return fmt.Errorf("%s: shared path %s: %w", image.Module, path, err)
			}
			if declared[path] {
				return fmt.Errorf("%s: duplicate shared path %s", image.Module, path)
			}
			declared[path] = true
			if !covers(contexts[image.Module], path) {
				return fmt.Errorf("%s: shared path %s missing from module revision contexts", image.Module, path)
			}
			if !covers(copied, path) {
				return fmt.Errorf("%s: shared path %s not copied by Dockerfile", image.Module, path)
			}
		}
		for _, path := range copied {
			if !covers(image.Shared, path) {
				return fmt.Errorf("%s: COPY source %s missing from shared_paths", image.Module, path)
			}
		}
		composePath := filepath.Join("modules", image.Module, "docker-compose.yml")
		body, err = os.ReadFile(filepath.Join(root, composePath))
		if err != nil {
			return err
		}
		var compose struct {
			Services map[string]struct {
				Build struct {
					Context    string            `yaml:"context"`
					Additional map[string]string `yaml:"additional_contexts"`
				} `yaml:"build"`
			} `yaml:"services"`
		}
		if err = yaml.Unmarshal(body, &compose); err != nil {
			return err
		}
		found := false
		for name, service := range compose.Services {
			if filepath.Clean(filepath.Join(filepath.Dir(composePath), service.Build.Context)) != image.Context {
				continue
			}
			found = true
			if service.Build.Additional["shared"] != "${ANAS_SHARED_BUILD_CONTEXT:-../..}" {
				return fmt.Errorf("%s/%s: shared Compose context must resolve the repository root", image.Module, name)
			}
		}
		if !found {
			return fmt.Errorf("%s: no Compose build for %s", image.Module, image.Context)
		}
	}
	return nil
}
func covers(paths []string, path string) bool {
	for _, p := range paths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// Accept the simple source-path COPY syntax used by this repository. Reject
// other shared COPY forms explicitly so a parser miss never passes silently.
func sharedCopies(body string) ([]string, error) {
	body = strings.ReplaceAll(body, "\\\n", " ")
	var result []string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.ToUpper(fields[0]) != "COPY" {
			continue
		}
		if !strings.Contains(line, "--from=shared") {
			continue
		}
		if len(fields) < 4 || fields[1] != "--from=shared" {
			return nil, fmt.Errorf("unsupported shared COPY: %s", line)
		}
		for _, source := range fields[2 : len(fields)-1] {
			if strings.ContainsAny(source, "[]\"*$") || !filepath.IsLocal(source) {
				return nil, fmt.Errorf("unsupported shared COPY source %s", source)
			}
			result = append(result, filepath.Clean(source))
		}
	}
	return result, nil
}
