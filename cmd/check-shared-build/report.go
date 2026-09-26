package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// This report describes build inputs, not deployment validity or permission to
// execute them. It never resolves a runtime .env or emits a service's
// environment, mounts, credentials, commands or network attachments.
type buildContextReport struct {
	Schema         string             `json:"schema"`
	DockerExecuted bool               `json:"docker_executed"`
	SourceRoot     string             `json:"source_root"`
	BuildRoot      string             `json:"build_root"`
	Images         []buildContextItem `json:"images"`
}

type buildContextItem struct {
	Module           string                  `json:"module"`
	Service          string                  `json:"service"`
	ComposeDirectory string                  `json:"compose_directory"`
	SharedRoot       string                  `json:"shared_root"`
	InputDigest      string                  `json:"input_digest"`
	Build            reportedBuildDefinition `json:"build"`
}

type reportedBuildDefinition struct {
	Context    string            `yaml:"context" json:"context"`
	Dockerfile string            `yaml:"dockerfile" json:"dockerfile,omitempty"`
	Additional map[string]string `yaml:"additional_contexts" json:"additional_contexts"`
	Network    string            `yaml:"network" json:"network,omitempty"`
	Args       map[string]string `yaml:"args" json:"args,omitempty"`
	Other      map[string]any    `yaml:",inline" json:"-"`
}

// New secret/SSH/privileged build inputs need explicit review rather than
// being silently omitted or copied from a runtime configuration into a report.
func (build reportedBuildDefinition) validateProjection() error {
	if len(build.Other) != 0 || len(build.Additional) != 1 || build.Additional["shared"] != "${ANAS_SHARED_BUILD_CONTEXT:-../..}" {
		return fmt.Errorf("build-only report contains unsupported build inputs")
	}
	if build.Network != "" && build.Network != "${DOCKER_BUILD_NETWORK:-default}" {
		return fmt.Errorf("build-only report requires the explicit build network selector")
	}
	allowed := map[string]string{
		"DOCKER_HUB_REGISTRY": "${DOCKER_HUB_REGISTRY:-docker.io}",
		"GO_BUILDER_REGISTRY": "${GO_BUILDER_REGISTRY:-${DOCKER_HUB_REGISTRY:-docker.io}}",
		"GO_MODULE_PROXY":     "${GO_MODULE_PROXY:-${GOPROXY_URL:-https://proxy.golang.org,direct}}",
	}
	for key, value := range build.Args {
		if expected, ok := allowed[key]; !ok || value != expected {
			return fmt.Errorf("build-only report permits only declared transport selectors, not credentials")
		}
	}
	return nil
}

func makeBuildReport(sourceRoot, stagingRoot, sharedOverride string) (*buildContextReport, error) {
	var err error
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		return nil, err
	}
	buildRoot := sourceRoot
	if stagingRoot != "" {
		if err := checkStagedBuildContexts(sourceRoot, stagingRoot, sharedOverride); err != nil {
			return nil, err
		}
		buildRoot, err = filepath.Abs(stagingRoot)
		if err != nil {
			return nil, err
		}
	}
	body, err := os.ReadFile(filepath.Join(sourceRoot, ".github", "images.json"))
	if err != nil {
		return nil, err
	}
	var images []imageEntry
	if err := json.Unmarshal(body, &images); err != nil {
		return nil, err
	}
	report := &buildContextReport{Schema: "anas.shared-build-inputs/v1", SourceRoot: sourceRoot, BuildRoot: buildRoot}
	for _, image := range images {
		if len(image.Shared) == 0 {
			continue
		}
		if !filepath.IsLocal(image.Module) || !filepath.IsLocal(image.Context) || !filepath.IsLocal(image.Dockerfile) {
			return nil, fmt.Errorf("build report paths must be local")
		}
		directory := filepath.Join(buildRoot, "modules", image.Module)
		if _, err := os.Stat(directory); stagingRoot != "" && os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		shared := sharedOverride
		if shared == "" {
			shared = "../.."
		}
		if !filepath.IsAbs(shared) {
			shared = filepath.Join(directory, shared)
		}
		shared = filepath.Clean(shared)
		inputs := make(map[string]map[string]buildInputSignature)
		for _, path := range image.Shared {
			if !filepath.IsLocal(path) || filepath.Clean(path) != path {
				return nil, fmt.Errorf("shared build input path must be local")
			}
			if err := sameBuildInputTree(filepath.Join(sourceRoot, path), filepath.Join(shared, path)); err != nil {
				return nil, fmt.Errorf("%s: selected shared source does not match: %w", image.Module, err)
			}
			inputs["shared/"+path], err = buildInputDigests(filepath.Join(shared, path))
			if err != nil {
				return nil, err
			}
		}
		contextPath := filepath.Join(buildRoot, image.Context)
		if err := sameBuildInputTree(filepath.Join(sourceRoot, image.Context), contextPath); err != nil {
			return nil, fmt.Errorf("%s: selected module source does not match: %w", image.Module, err)
		}
		inputs["module"], err = buildInputDigests(contextPath)
		if err != nil {
			return nil, err
		}
		body, err := os.ReadFile(filepath.Join(directory, "docker-compose.yml"))
		if err != nil {
			return nil, err
		}
		var compose struct {
			Services map[string]struct {
				Build reportedBuildDefinition `yaml:"build"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(body, &compose); err != nil {
			return nil, fmt.Errorf("%s: invalid Compose build declarations", image.Module)
		}
		var names []string
		for name := range compose.Services {
			names = append(names, name)
		}
		sort.Strings(names)
		matched := false
		for _, name := range names {
			build := compose.Services[name].Build
			if build.Context == "" || filepath.Clean(filepath.Join(directory, build.Context)) != contextPath {
				continue
			}
			if err := build.validateProjection(); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", image.Module, name, err)
			}
			dockerfile := build.Dockerfile
			if dockerfile == "" {
				dockerfile = "Dockerfile"
			}
			if !filepath.IsLocal(dockerfile) || filepath.Clean(filepath.Join(contextPath, dockerfile)) != filepath.Join(buildRoot, image.Dockerfile) {
				return nil, fmt.Errorf("%s/%s: Dockerfile does not match image inventory", image.Module, name)
			}
			encoded, err := json.Marshal(struct {
				Build  reportedBuildDefinition                   `json:"build"`
				Inputs map[string]map[string]buildInputSignature `json:"inputs"`
			}{build, inputs})
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(encoded)
			report.Images = append(report.Images, buildContextItem{image.Module, name, directory, shared, hex.EncodeToString(sum[:]), build})
			matched = true
		}
		if !matched {
			return nil, fmt.Errorf("%s: no shared-context build was reported", image.Module)
		}
	}
	if len(report.Images) == 0 {
		return nil, fmt.Errorf("no shared-context builds were reported")
	}
	return report, nil
}
