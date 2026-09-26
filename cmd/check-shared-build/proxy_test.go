package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// INCUS-R-084: an operator may select a reachable source for pinned external
// dependencies without making the repository's shared code network-resolved.
func TestComposeExternalModuleProxyIsExplicitAndBoundToBuildArgs(t *testing.T) {
	for _, item := range []struct {
		module, context string
		services        int
	}{
		{"forgejo", "./actions-controller", 2},
		{"ai_agent", "./orchestrator", 1},
	} {
		t.Run(item.module, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "modules", item.module, "docker-compose.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var compose struct {
				Services map[string]struct {
					Build struct {
						Context string            `yaml:"context"`
						Args    map[string]string `yaml:"args"`
					} `yaml:"build"`
					Environment map[string]any `yaml:"environment"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal(body, &compose); err != nil {
				t.Fatal(err)
			}
			count := 0
			for name, service := range compose.Services {
				if service.Build.Context != item.context {
					continue
				}
				count++
				if service.Build.Args["GO_MODULE_PROXY"] != "${GO_MODULE_PROXY:-${GOPROXY_URL:-https://proxy.golang.org,direct}}" {
					t.Errorf("%s does not expose the explicit build-only module proxy", name)
				}
				for _, key := range []string{"GO_MODULE_PROXY", "GOPROXY", "GOSUMDB", "GONOSUMDB"} {
					if _, exists := service.Environment[key]; exists {
						t.Errorf("%s unexpectedly projects build network setting %s at runtime", name, key)
					}
				}
			}
			if count != item.services {
				t.Fatalf("checked %d consumers, want %d", count, item.services)
			}
		})
	}
}

func TestDockerfileProxyDoesNotWeakenOfflineSharedCodeOrChecksums(t *testing.T) {
	for _, item := range []struct {
		path, stage string
		external    bool
	}{
		{"modules/incus/provisioner/Dockerfile", "build", false},
		{"modules/forgejo/actions-controller/Dockerfile", "controller-build", false},
		{"modules/forgejo/actions-controller/Dockerfile", "incus-build", true},
		{"modules/ai_agent/orchestrator/Dockerfile", "build", true},
	} {
		t.Run(item.path+"/"+item.stage, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", item.path))
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			selected := false
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				fields := strings.Fields(line)
				if strings.EqualFold(fields[0], "FROM") {
					selected = len(fields) >= 4 && strings.EqualFold(fields[len(fields)-2], "AS") && fields[len(fields)-1] == item.stage
				}
				if selected {
					lines = append(lines, line)
				}
			}
			stage := strings.Join(lines, "\n")
			if stage == "" {
				t.Fatal("expected build stage is missing")
			}
			if strings.Contains(stage, "GOSUMDB=") || strings.Contains(stage, "GONOSUMDB=") || strings.Contains(stage, "GOPRIVATE=") {
				t.Fatal("build stage overrides the default checksum verification boundary")
			}
			if item.external {
				if !strings.Contains(stage, "ARG GO_MODULE_PROXY=https://proxy.golang.org,direct") || !strings.Contains(stage, `GOPROXY="$GO_MODULE_PROXY"`) {
					t.Fatal("external dependency stage does not explicitly consume its proxy argument")
				}
			} else if !strings.Contains(stage, "GOPROXY=off") || strings.Contains(stage, "GO_MODULE_PROXY") {
				t.Fatal("shared repository-only build stage no longer stays offline")
			}
		})
	}
}

func TestAllComputeComposeBuildsForwardRegistryAndNetworkSelectors(t *testing.T) {
	for _, item := range []struct {
		module, context string
		services        int
	}{
		{"incus", "./provisioner", 1},
		{"forgejo", "./actions-controller", 2},
		{"ai_agent", "./orchestrator", 1},
	} {
		t.Run(item.module, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "modules", item.module, "docker-compose.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var compose struct {
				Services map[string]struct {
					Build       reportedBuildDefinition `yaml:"build"`
					Environment map[string]any          `yaml:"environment"`
				} `yaml:"services"`
			}
			if err := yaml.Unmarshal(body, &compose); err != nil {
				t.Fatal(err)
			}
			count := 0
			for name, service := range compose.Services {
				if service.Build.Context != item.context {
					continue
				}
				count++
				if service.Build.Args["GO_BUILDER_REGISTRY"] != "${GO_BUILDER_REGISTRY:-${DOCKER_HUB_REGISTRY:-docker.io}}" || service.Build.Network != "${DOCKER_BUILD_NETWORK:-default}" || service.Build.Args["DOCKER_HUB_REGISTRY"] != "${DOCKER_HUB_REGISTRY:-docker.io}" {
					t.Errorf("%s does not forward the declared build transport selectors", name)
				}
				for _, key := range []string{"GO_BUILDER_REGISTRY", "DOCKER_HUB_REGISTRY", "DOCKER_BUILD_NETWORK"} {
					if _, exists := service.Environment[key]; exists {
						t.Errorf("%s exposes a build transport selector at runtime", name)
					}
				}
				if item.module == "incus" {
					if _, exists := service.Build.Args["GO_MODULE_PROXY"]; exists {
						t.Fatal("repository-only provisioner build must not resolve external shared code")
					}
				}
			}
			if count != item.services {
				t.Fatalf("checked %d builds, expected %d", count, item.services)
			}
		})
	}
}

func TestComputeRuntimeLayersUseTheRepositoryRegistryPolicy(t *testing.T) {
	for _, path := range []string{"modules/incus/provisioner/Dockerfile", "modules/forgejo/actions-controller/Dockerfile", "modules/ai_agent/orchestrator/Dockerfile"} {
		t.Run(path, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", path))
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			if !strings.Contains(text, "ARG DOCKER_HUB_REGISTRY=docker.io\n") || !strings.Contains(text, "ARG GO_BUILDER_REGISTRY=${DOCKER_HUB_REGISTRY}\n") || !strings.Contains(text, "FROM ${DOCKER_HUB_REGISTRY}/library/alpine:3.22.1\n") {
				t.Fatal("both builder and runtime bases must honor the repository's existing build registry setting")
			}
		})
	}
}
