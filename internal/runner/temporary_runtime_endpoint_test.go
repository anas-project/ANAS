package runner

// TEST_CASES: TEMP-T-010
// REQUIREMENTS: TEMP-R-024

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestTemporaryRuntimeProbeKeepsProcessDockerEndpoint(t *testing.T) {
	selectors := []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"}
	for _, test := range []struct {
		name   string
		values map[string]string
	}{
		{"host-and-tls", map[string]string{"DOCKER_HOST": "unix:///run/operator.sock", "DOCKER_CONTEXT": "", "DOCKER_CONFIG": "/operator/config", "DOCKER_TLS_VERIFY": "1", "DOCKER_CERT_PATH": "/operator/certs"}},
		{"context", map[string]string{"DOCKER_CONTEXT": "operator-context", "DOCKER_CONFIG": "/operator/config"}},
		{"default-unset", map[string]string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			id := "runtime-endpoint"
			root := filepath.Join(stateDir(workspace), "deployments", id)
			moduleDir := filepath.Join(root, "modules", "demo")
			if err := os.MkdirAll(moduleDir, 0700); err != nil {
				t.Fatal(err)
			}
			manifest := &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"demo"}, Modules: map[string]deploymentModule{
				"demo": {Name: "demo", RuntimeType: "compose", ComposeFile: "docker-compose.yml"},
			}}
			if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
			if err := writeEnv(filepath.Join(root, "modules", globalEnvFile), map[string]string{"DATA_PATH": dataDir(workspace)}); err != nil {
				t.Fatal(err)
			}
			overlay := map[string]string{"CONTAINER_PREFIX": "anas_", "PATH": "/workspace/bin", "HOME": "/workspace/home", "LANG": "workspace-locale"}
			for _, key := range selectors {
				overlay[key] = "workspace-override"
				t.Setenv(key, "")
				if value, present := test.values[key]; present {
					if err := os.Setenv(key, value); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeEnv(filepath.Join(moduleDir, ".env"), overlay); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(moduleDir, "docker-compose.yml"), []byte("services: {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			calls := filepath.Join(bin, "calls")
			t.Setenv("ANAS_DAEMON_AMBIENT_SECRET", "must-not-escape")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			var script strings.Builder
			script.WriteString("#!/bin/sh\nset -eu\n[ \"${ANAS_DAEMON_AMBIENT_SECRET+x}\" = '' ] || exit 91\n")
			for _, key := range selectors {
				if value, present := test.values[key]; present {
					script.WriteString("[ \"${" + key + "+x}\" = x ] && [ \"${" + key + "}\" = '" + value + "' ] || exit 92\n")
				} else {
					script.WriteString("[ \"${" + key + "+x}\" = '' ] || exit 93\n")
				}
			}
			composePS := "compose --project-name anas_demo --env-file .env --file docker-compose.yml ps --all --format json"
			script.WriteString("printf '%s\\n' \"$*\" >> '" + calls + "'\ncase \"$*\" in\n  'compose version') exit 0 ;;\n  'context inspect') printf '%s\\n' '[{\"Endpoints\":{\"docker\":{\"Host\":\"unix:///run/operator-context.sock\"}}}]' ;;\n  'info --format {{json .}}') printf '%s\\n' '{\"ID\":\"operator-daemon\",\"OSType\":\"linux\"}' ;;\n  'ps --all --quiet --no-trunc') exit 0 ;;\n  '" + composePS + "') printf '%s\\n' '[{\"Service\":\"app\",\"State\":\"running\",\"Health\":\"healthy\"}]' ;;\n  *) exit 94 ;;\nesac\n")
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script.String()), 0700); err != nil {
				t.Fatal(err)
			}
			result, err := (workspaceRuntimeProbe{}).InspectRuntime(context.Background(), workspace, id)
			if err != nil || result.Status != "running" || result.Healthy == nil || !*result.Healthy || len(result.Modules) != 1 || result.Modules[0].Containers != 1 {
				t.Fatalf("query did not inspect the operator endpoint: %+v, %v", result, err)
			}
			observed, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(observed)), "\n")
			want := []string{"compose version"}
			if runtime.GOOS == "linux" {
				// Linux can inspect storage topology and therefore completes the
				// endpoint/inventory probe even when no temporary leases exist.
				if test.values["DOCKER_CONTEXT"] != "" || test.values["DOCKER_HOST"] == "" {
					want = append(want, "context inspect")
				}
				want = append(want, "info --format {{json .}}", "ps --all --quiet --no-trunc")
			}
			want = append(want, composePS)
			if !slices.Equal(lines, want) {
				t.Fatalf("unexpected status subprocesses: %q", observed)
			}
		})
	}
}

func TestTemporaryRuntimeProbePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (workspaceRuntimeProbe{}).InspectRuntime(ctx, t.TempDir(), "runtime-endpoint")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime detection error = %v, want context canceled", err)
	}
}
