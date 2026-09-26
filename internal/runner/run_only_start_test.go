package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
)

func TestStartDeploymentNeverExpandsAnEmptyRuntimeSelectionToAllServices(t *testing.T) {
	for _, mode := range []string{"only operations", "all disabled", "mixed runtime and operation"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, ".anas")
			modules := filepath.Join(base, "deployments", "run-only-test", "modules")
			dir := filepath.Join(modules, "provider")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "calls")
			services := "provision"
			if mode == "all disabled" {
				services = "runtime"
			}
			if mode == "mixed runtime and operation" {
				services = "provision runtime"
			}
			composeScript := filepath.Join(root, "compose.sh")
			body := "#!/bin/sh\ncase \" $* \" in\n *\" config --services \"*) printf '%s\\n' " + services + " ;;\n *\" up -d \"*) printf '%s\\n' \"$*\" >> '" + log + "' ;;\n *) exit 7 ;;\nesac\n"
			if err := os.WriteFile(composeScript, []byte(body), 0755); err != nil {
				t.Fatal(err)
			}
			hookScript := filepath.Join(root, "hook.sh")
			hook := "#!/bin/sh\npayload=$(cat)\ncase \"$payload\" in\n *'\"phase\":\"after_start\"'*) echo ready >> '" + log + "'; echo '{}' ;;\n"
			if mode == "all disabled" {
				hook += " *'\"phase\":\"services\"'*) echo '{\"disable_services\":[\"runtime\"]}' ;;\n"
			}
			hook += " *) echo '{}' ;;\nesac\n"
			if err := os.WriteFile(hookScript, []byte(hook), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CONTAINER_PREFIX=test_\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			mod := Module{Name: "provider", RuntimeType: "compose", ComposeFile: "docker-compose.yml", SourceDir: dir,
				Hook:              HookConfig{Command: []string{hookScript}, Phases: []string{"services", "after_start"}},
				ContractProviders: []ContractProvider{{OperationSvcs: []string{"provision"}}}}
			old := inspectComposeProjectOwners
			inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
			defer func() { inspectComposeProjectOwners = old }()
			a := &app{workspace: root, base: base, compose: compose.CLI{Bin: []string{composeScript}},
				reg: map[string]Module{"provider": mod}, order: []string{"provider"}, env: map[string]string{}, envOwner: map[string]string{},
				secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}}
			if err := startDeployment(a, modules, a.order, false); err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if mode != "mixed runtime and operation" {
				if len(lines) != 1 || lines[0] != "ready" {
					t.Fatal("empty selected services became Compose up ALL", string(calls))
				}
			} else if len(lines) != 2 || lines[1] != "ready" || !strings.HasSuffix(lines[0], "up -d --remove-orphans runtime") || strings.Contains(lines[0], "provision") {
				t.Fatal("normal runtime selection or ready barrier changed", string(calls))
			}
		})
	}
}
