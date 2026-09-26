package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
)

// A full materializeDeployment call, with a real dependency graph, trusted
// module input, calculate subprocess, private store, render and manifest. No
// Docker or host connection is fabricated here; that is a separate native test.
func TestPrepareDeploymentUsesCalculatedProviderBeforeFreezingCompute(t *testing.T) {
	bundle, workspace := t.TempDir(), t.TempDir()
	modules := filepath.Join(bundle, "modules")
	provider := filepath.Join(modules, "sandbox_provider")
	consumer := filepath.Join(modules, "worker")
	for _, dir := range []string{provider, consumer, filepath.Join(bundle, "contracts")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyDir(filepath.Join("..", "..", "contracts", "compute"), filepath.Join(bundle, "contracts", "compute")); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(provider, "module.yml"), `api_version: anas.module/v1
kind: Module
name: sandbox_provider
version: 1.0.0
revision: 1
status: release
abi: {supports: [anas.module-hook/v1]}
runtime: {type: compose, compose_file: docker-compose.yml}
contracts:
  provides:
    - {name: compute, version: 1.0.0, interface: incus_container, implementation: provider.yml}
config:
  must_resolve: [image_architecture]
  types:
    image_architecture: {enum: [amd64, arm64], default_source: host}
logic:
  hook:
    command: [sh, -c, 'cat >/dev/null; cat response.json']
    phases: [calculate]
`)
	write(filepath.Join(provider, "docker-compose.yml"), "services:\n  provision: {image: example.invalid/native-provider}\n")
	write(filepath.Join(provider, "provider.yml"), `api_version: anas.provider/v1
kind: ContractProvider
contract: compute
contract_version: 1.0.0
interface: incus_container
operations:
  ensure: {runtime: compose_run, service: provision, command: [ensure]}
  inspect: {runtime: compose_run, service: provision, command: [inspect]}
`)
	server, _ := testServerCertB64(t)
	response, err := json.Marshal(hookResponse{Env: map[string]string{
		"SANDBOX_PROVIDER_IMAGE_ARCHITECTURE":   "arm64",
		"SANDBOX_PROVIDER_ENDPOINT":             "https://192.0.2.1:18443",
		"SANDBOX_PROVIDER_SERVER_CERT_B64":      server,
		"SANDBOX_PROVIDER_CONTROL_NETWORK_NAME": "anas-incus-control",
	}, Secrets: map[string]string{"SANDBOX_PROVIDER_SERVER_CERT_B64": server}})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(provider, "response.json"), string(response))
	write(filepath.Join(consumer, "module.yml"), `api_version: anas.module/v1
kind: Module
name: worker
version: 1.0.0
revision: 1
status: release
abi: {supports: [anas.module-hook/v1]}
runtime: {type: builtin}
dependencies:
  contracts:
    - name: compute
      version: '>=1.0.0 <2.0.0'
      selected_by: isolation
      interfaces: [incus_container]
      default: incus_container
config:
  defaults: {isolation: auto}
  types:
    isolation: {enum: [auto, incus_container]}
resources:
  requires:
    - id: workers
      contract: compute
      binding: isolation
      spec:
        sandbox: anas-native-worker
        instance_prefix: anas-worker-
        quota: {max_instances: 1, cpu: 1, memory_mib: 512, disk_gib: 4}
        image_allowlist: [{fingerprint: '`+strings.Repeat("a", 64)+`'}]
        credential: {policy: generated}
        deletion_policy: retain
`)
	config := filepath.Join(bundle, "source-config.yml")
	write(config, `modules:
  worker: {}
global:
  base_domain: native.test
  email: admin@native.test
env:
  IPv6: 'false'
rollback:
  snapshot: {backend: none}
`)
	opts := prepareOptions{workspace: workspace, base: stateDir(workspace), cfgPath: config,
		moduleRoot: modules, updateLock: true, context: context.Background()}
	// Exercise the same import-only workspace boundary as the actual CLI.
	// Writing a config and calling materializeDeployment directly would miss
	// the managed-config precondition in runPrepare/finalizePrepareOptions.
	if err := runInit([]string{workspace, "--yes"}, true); err != nil {
		t.Fatal("public workspace initialization failed", err)
	}
	if err := runConfig([]string{"import", config, "-w", workspace, "--root", modules}, true); err != nil {
		t.Fatal("public configuration import failed", err)
	}
	opts.cfgPath = workspaceConfigPath(workspace)
	opts, err = finalizePrepareOptions(opts)
	if err != nil {
		t.Fatal("public preparation boundary rejected imported config", err)
	}
	var original string
	for attempt := 0; attempt < 2; attempt++ {
		id, err := materializeDeployment(opts, false, false)
		if err != nil {
			t.Fatal("automatic provider calculation failed before deployment rendering", err)
		}
		a, root, manifest, err := loadDeploymentApp(opts.base, id, compose.CLI{})
		if err != nil || len(manifest.Resources) != 1 || len(a.resourceRequests) != 1 {
			t.Fatal("calculated resource was not persisted into the frozen deployment", err)
		}
		resource := a.resourceRequests[0]
		if resource.ComputeImages.Images[0].Target.Architecture != "arm64" {
			t.Fatal("image target was inferred from CLI host instead of the provider")
		}
		if attempt == 0 {
			original = resource.Credential
		} else if original != resource.Credential {
			t.Fatal("a new deployment rotated the consumer's existing credential")
		}
		env, err := parseEnvFile(filepath.Join(root, "worker", ".env"))
		if err != nil || env[computeResourcePrefix("worker", "workers")+"CLIENT_KEY"] == "" || env["SANDBOX_PROVIDER_SERVER_CERT_B64"] != "" {
			t.Fatal("consumer was not rendered with its own key isolated from the provider secret", err)
		}
		body, err := os.ReadFile(filepath.Join(opts.base, "deployments", id, "deployment.yml"))
		if err != nil || strings.Contains(string(body), original) || strings.Contains(string(body), server) {
			t.Fatal("deployment metadata contains secret values instead of references", err)
		}
		opts.updateLock = false
	}
}
