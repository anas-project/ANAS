//go:build linux

package runner

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/incusprovision"
	"gopkg.in/yaml.v3"
)

const coreNativeInputs = "/opt/anas-core-inputs"
const coreNativeRoot = "/srv/anas/native-core-projection"
const coreNativeReports = "/opt/anas-core-projection"

// coreNativeWorkspace is the workspace the installed anasd registers as
// "native", so workspace-wide host actions such as image prune read exactly
// the deployments this test creates.
const coreNativeWorkspace = "/srv/anas/host-action-native"

// coreNativeStateFile hands the cleanup test what the projection test left
// running. It holds identities and paths only, never key material.
const coreNativeStateFile = coreNativeReports + "/private/core-state.json"

type coreNativeState struct {
	ID              string            `json:"id"`
	DockerID        string            `json:"docker_id"`
	PinR1           string            `json:"pin_r1"`
	PinR2           string            `json:"pin_r2"`
	ModuleRoots     map[string]string `json:"module_roots"`
	ComposeProjects map[string]string `json:"compose_projects"`
	Images          []string          `json:"images"`
}

type coreNativeManifest struct {
	Schema string            `json:"schema"`
	Files  map[string]string `json:"files"`
}

func coreNativeGuard(t *testing.T) (string, coreNativeManifest) {
	t.Helper()
	if os.Getenv("ANAS_REQUIRE_CORE_COMPUTE_NATIVE") != "1" {
		t.Skip("requires the separately supervised, source-bound disposable Core/Compose VM")
	}
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, vendorErr := os.ReadFile("/sys/devices/virtual/dmi/id/sys_vendor")
	id := strings.TrimSpace(string(identity))
	if err != nil || vendorErr != nil || strings.TrimSpace(string(vendor)) != "QEMU" ||
		!regexp.MustCompile(`^anas-incus-host-[a-f0-9]{6}$`).MatchString(id) || os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Fatal("native VM identity/root guard failed")
	}
	readInput := func(path string) []byte {
		t.Helper()
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			info, err := os.Lstat(dir)
			if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 {
				t.Fatal("native input ancestor is not root-owned and protected")
			}
			if dir == "/" {
				break
			}
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 96<<20 ||
			info.Sys().(*syscall.Stat_t).Uid != 0 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
			t.Fatal("native input is not a bounded root-owned regular file")
		}
		body, err := os.ReadFile(path)
		if err != nil || int64(len(body)) != info.Size() {
			t.Fatal("native input read failed")
		}
		return body
	}
	var manifest coreNativeManifest
	if json.Unmarshal(readInput(coreNativeInputs+"/core-manifest.json"), &manifest) != nil || manifest.Schema != "anas.native-core-inputs/v1" || len(manifest.Files) < 10 || len(manifest.Files) > 4096 {
		t.Fatal("invalid native input manifest")
	}
	for path, digest := range manifest.Files {
		if filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, "../") ||
			!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) || sha256Hex(readInput(filepath.Join(coreNativeInputs, path))) != digest {
			t.Fatal("native input changed after delivery")
		}
	}
	self, err := os.Executable()
	if err != nil || manifest.Files["core.test"] == "" || sha256Hex(readInput(self)) != manifest.Files["core.test"] ||
		manifest.Files["anas"] == "" || sha256Hex(readInput("/usr/local/bin/anas")) != manifest.Files["anas"] {
		t.Fatal("native test or installed CLI does not match the delivered product")
	}
	return id, manifest
}

type coreNativeBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *coreNativeBuffer) Write(data []byte) (int, error) {
	n := len(data)
	remaining := (2 << 20) - b.Len()
	if n > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.Buffer.Write(data)
	return n, nil
}

// Child output may contain product configuration errors. Keep it exclusively
// in this experiment's private diagnostic directory, never in test2json.
func coreNativeCommand(t *testing.T, name string, input []byte, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C", "LANG=C", "GOPROXY=off"}
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr coreNativeBuffer
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &stdout, &stderr, 3*time.Second
	err := cmd.Run()
	if err != nil || stdout.truncated || stderr.truncated {
		_ = os.MkdirAll(coreNativeReports+"/private", 0700)
		body := append(append([]byte{}, stdout.Bytes()...), stderr.Bytes()...)
		_ = os.WriteFile(filepath.Join(coreNativeReports, "private", name+".log"), body, 0600)
		t.Fatalf("native command %s failed; private diagnostics retained", name)
	}
	return stdout.Bytes()
}

func coreNativeCLI(t *testing.T, label string, args ...string) map[string]json.RawMessage {
	t.Helper()
	args = append([]string{"/usr/local/bin/anas"}, args...)
	args = append(args, "--json")
	body := coreNativeCommand(t, label, nil, args...)
	var result map[string]json.RawMessage
	if json.Unmarshal(body, &result) != nil || !bytes.Equal(result["ok"], []byte("true")) {
		t.Fatal("native CLI did not return its success envelope")
	}
	return result
}

func coreNativeRejectActiveApply(t *testing.T, workspace, deploymentID string) {
	t.Helper()
	before, err := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	active, err := loadActiveState(stateDir(workspace))
	if err != nil || active.ActiveDeployment != deploymentID {
		t.Fatal("replay control needs the actual active deployment", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/local/bin/anas", "apply", "-w", workspace,
		"--deployment", deploymentID, "--yes", "--no-snapshot", "--json")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C"}
	var stdout, stderr coreNativeBuffer
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &stdout, &stderr, 3*time.Second
	err = cmd.Run()
	var failure struct {
		APIVersion string `json:"api_version"`
		OK         bool
		Error      struct{ Code string }
	}
	if err == nil || ctx.Err() != nil || stdout.truncated || stderr.truncated || cmd.ProcessState == nil ||
		cmd.ProcessState.ExitCode() != exitPrecondition || json.Unmarshal(stdout.Bytes(), &failure) != nil ||
		failure.APIVersion != cliAPIVersion || failure.OK || failure.Error.Code != "deployment_not_ready" {
		t.Fatal("reapplying a consumed deployment did not preserve the ready-only contract")
	}
	after, err := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	current, stateErr := loadActiveState(stateDir(workspace))
	priorJSON, _ := json.Marshal(active)
	currentJSON, _ := json.Marshal(current)
	if err != nil || stateErr != nil || !bytes.Equal(before, after) || !bytes.Equal(priorJSON, currentJSON) {
		t.Fatal("rejected active deployment replay changed the active identity or private store")
	}
}

func coreNativeImage(t *testing.T, id, source, executable, tag string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(coreNativeInputs, source))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	if err := w.WriteHeader(&tar.Header{Name: strings.TrimPrefix(executable, "/"), Mode: 0555, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil || w.Close() != nil {
		t.Fatal("fixture image archive failed")
	}
	entry, _ := json.Marshal([]string{executable})
	result := strings.TrimSpace(string(coreNativeCommand(t, "import-"+source, archive.Bytes(), "/usr/bin/docker", "import",
		"--change", "USER 65532:65532", "--change", "ENTRYPOINT "+string(entry),
		"--change", "LABEL dev.anas.native-core="+id, "-", tag)))
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(result) {
		t.Fatal("test image did not return immutable Docker identity")
	}
	return result
}

func coreNativeConsumerManifest(name string) string {
	return coreNativeConsumerManifestRevision(name, "lab-r1")
}

func coreNativeConsumerManifestRevision(name, revision string) string {
	return `api_version: anas.module/v1
kind: Module
name: ` + name + `
version: 0.0.0
revision: 1
status: developing
abi: {supports: [anas.module-hook/v1]}
runtime: {type: compose, compose_file: docker-compose.yml}
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
        sandbox: anas-` + strings.ReplaceAll(name, "_", "-") + `
        instance_prefix: anas-native-
        quota: {max_instances: 1, cpu: 1, memory_mib: 512, disk_gib: 4}
        image_allowlist: [{catalog: anas, name: native-core, revision: ` + revision + `}]
        credential: {policy: generated}
        deletion_policy: retain
`
}

func coreNativeCompose(name, id string) string {
	return coreNativeComposeWithExtraNetwork(name, id, "")
}

// coreNativeComposeWithExtraNetwork optionally attaches a named external
// network. A network that does not exist makes Compose refuse to start the
// service locally and deterministically, after Core has ensured resources.
func coreNativeComposeWithExtraNetwork(name, id, missing string) string {
	prefix := computeResourcePrefix(name, "workers")
	extraService, extraNetwork := "", ""
	if missing != "" {
		extraService = "\n      missing: {}"
		extraNetwork = "\n  missing:\n    external: true\n    name: " + missing
	}
	return `services:
  consumer:
    image: ${ANAS_IMAGE_REGISTRY}/anas-native-core-consumer:r1
    container_name: ${CONTAINER_PREFIX}` + name + `
    command: [` + name + `]
    env_file: [.env]
    user: '65532:65532'
    read_only: true
    cap_drop: [ALL]
    security_opt: ['no-new-privileges:true']
    labels: {dev.anas.native-core: '` + id + `'}
    networks:
      business: {gw_priority: 100}
      control: {gw_priority: 0}` + extraService + `
networks:
  business:
    name: ${NETWORK_PREFIX}` + name + `
    labels: {dev.anas.native-core: '` + id + `'}
  control:
    external: ${` + prefix + `CONTROL_NETWORK_EXTERNAL:-false}
    name: ${` + prefix + `CONTROL_NETWORK_NAME:-unresolved-control}` + extraNetwork + `
`
}

func TestNativeCoreComputeProjection(t *testing.T) {
	id, _ := coreNativeGuard(t)
	if _, err := os.Lstat(coreNativeRoot); !os.IsNotExist(err) {
		t.Fatal("fresh Core experiment root required")
	}
	// The orchestrator created the registered workspace with the installed
	// `anas init` before anasd started. Its config must still be the skeleton
	// init wrote: the managed state names init and matches the current bytes.
	if info, err := os.Stat(filepath.Join(stateDir(coreNativeWorkspace), "state", "deployments")); err != nil || !info.IsDir() {
		t.Fatal("registered workspace must be a freshly initialized workspace")
	}
	current, err := os.ReadFile(workspaceConfigPath(coreNativeWorkspace))
	if err != nil {
		t.Fatal("registered workspace has no init config skeleton")
	}
	stored, err := os.ReadFile(managedConfigStatePath(stateDir(coreNativeWorkspace)))
	if err != nil {
		t.Fatal("registered workspace has no managed config state")
	}
	wantBytes, err := managedConfigStateBytes(current, "init")
	if err != nil {
		t.Fatal(err)
	}
	var got, want managedConfigState
	if yaml.Unmarshal(stored, &got) != nil || yaml.Unmarshal(wantBytes, &want) != nil ||
		got.UpdatedBy != "init" || got.ContentDigest != want.ContentDigest {
		t.Fatal("registered workspace config was changed after anas init")
	}
	if entries, err := os.ReadDir(filepath.Join(stateDir(coreNativeWorkspace), "state", "deployments")); err != nil || len(entries) != 0 {
		t.Fatal("registered workspace already holds deployments")
	}
	for _, dir := range []string{coreNativeRoot, coreNativeReports + "/private", coreNativeReports + "/reports"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	step := func(name string, fn func(*testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}
	write := func(path string, body []byte, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
	var bundle incusprovision.ConnectionBundle
	var dockerID string
	step("approved_host_and_empty_experiment", func(t *testing.T) {
		body, err := os.ReadFile(incusprovision.DefaultBundlePath)
		if err != nil || json.Unmarshal(body, &bundle) != nil || bundle.Architecture != "amd64" || bundle.ControlNetwork != "anas-incus-control" {
			t.Fatal("installed host connection was not established by prior approved actions")
		}
		var info struct{ ID, DockerRootDir string }
		if json.Unmarshal(coreNativeCommand(t, "docker-info", nil, "/usr/bin/docker", "info", "--format", "{{json .}}"), &info) != nil || info.DockerRootDir != "/var/lib/anas-host-provision-test" || info.ID == "" {
			t.Fatal("not the dedicated VM Docker daemon")
		}
		dockerID = info.ID
		if len(bytes.TrimSpace(coreNativeCommand(t, "docker-empty", nil, "/usr/bin/docker", "ps", "-aq"))) != 0 {
			t.Fatal("preexisting containers are not test targets")
		}
	})
	moduleRoot := filepath.Join(coreNativeRoot, "source", "modules")
	provider := filepath.Join(moduleRoot, "incus")
	workspace := coreNativeWorkspace
	registry := "anas-native-core-" + strings.TrimPrefix(id, "anas-incus-host-")
	containerPrefix, networkPrefix := "anascore"+strings.TrimPrefix(id, "anas-incus-host-")+"_", "anascore"+strings.TrimPrefix(id, "anas-incus-host-")+"_"
	var images []string
	var pin string
	var entry computeimage.Entry
	step("trusted_fixture_and_actual_provider_compose", func(t *testing.T) {
		if err := copyDir(coreNativeInputs+"/bundle", filepath.Join(coreNativeRoot, "source")); err != nil {
			t.Fatal(err)
		}
		// These are binary transport fixtures, not a claim about building or
		// publishing release Dockerfiles. The Provider Compose/Hook are unchanged.
		images = append(images, coreNativeImage(t, id, "provisioner", "/usr/local/bin/anas-incus-provisioner", registry+"/anas-incus-provisioner:7.3.0-r2"))
		images = append(images, coreNativeImage(t, id, "consumer", "/usr/local/bin/consumer", registry+"/anas-native-core-consumer:r1"))
		entry = coreNativeFixtureImage(t, provider, "lab-r1")
		pin = entry.Fingerprint
		catalog, _ := json.Marshal([]computeimage.Entry{entry})
		write(filepath.Join(provider, "images", "catalog.json"), catalog, 0600)
		for _, name := range []string{"core_one", "core_two"} {
			write(filepath.Join(moduleRoot, name, "module.yml"), []byte(coreNativeConsumerManifest(name)), 0600)
			write(filepath.Join(moduleRoot, name, "docker-compose.yml"), []byte(coreNativeCompose(name, id)), 0600)
		}
		// No Incus endpoint, architecture, certificate, key, storage or control
		// bridge input is supplied by this config or driver.
		config := "modules:\n  core_one: {}\n  core_two: {}\nglobal:\n  base_domain: core.native.test\n  email: admin@core.native.test\n  virtual_domain: true\nrollback:\n  snapshot: {backend: none}\nenv:\n  IPv6: 'false'\n  ANAS_IMAGE_REGISTRY: " + registry + "\n  CONTAINER_PREFIX: " + containerPrefix + "\n  NETWORK_PREFIX: " + networkPrefix + "\n"
		write(filepath.Join(coreNativeRoot, "source-config.yml"), []byte(config), 0600)
	})
	var deploymentID, deploymentRoot string
	var originalKeys = map[string]string{}
	composeProjects := map[string]string{}
	step("cli_render_without_manual_host_connection", func(t *testing.T) {
		// Use the public import operation to establish the managed workspace
		// configuration. Raw config-file writes must remain rejected by render;
		// never manufacture config-managed.yml or disable its digest check.
		imported := coreNativeCLI(t, "config-import", "config", "import", filepath.Join(coreNativeRoot, "source-config.yml"),
			"-w", workspace, "--root", moduleRoot)
		if !bytes.Equal(imported["secrets_imported"], []byte("0")) {
			t.Fatal("fixture supplied credentials instead of deriving the installed host connection")
		}
		result := coreNativeCLI(t, "render", "render", "-w", workspace, "--root", moduleRoot, "--update-lock")
		if json.Unmarshal(result["deployment_id"], &deploymentID) != nil || validateDeploymentID(deploymentID) != nil {
			t.Fatal("rendered deployment identity invalid")
		}
		deploymentRoot = filepath.Join(stateDir(workspace), "deployments", deploymentID)
	})
	step("frozen_target_and_private_resource_projections", func(t *testing.T) {
		manifest, err := loadDeploymentManifest(deploymentRoot)
		if err != nil || len(manifest.Resources) != 2 {
			t.Fatal("two resources were not frozen", err)
		}
		store, err := loadSecretStore(stateDir(workspace))
		if err != nil || store.values["INCUS_HOST_CONNECTION_SOURCE"] != "host-bundle:v1" {
			t.Fatal("host Hook did not import the actual private bundle", err)
		}
		for _, resource := range manifest.Resources {
			if resource.ComputeImages == nil || len(resource.ComputeImages.Images) != 1 || resource.ComputeImages.Images[0].Fingerprint != pin || resource.ComputeImages.Images[0].Target.Architecture != bundle.Architecture {
				t.Fatal("image resolution was not frozen from the approved host target")
			}
			name := resource.Consumer
			env, err := parseEnvFile(filepath.Join(deploymentRoot, "modules", name, ".env"))
			if err != nil {
				t.Fatal(err)
			}
			lease, err := computeclient.LeaseFromLookup(func(key string) string { return env[key] }, name, "workers")
			if err != nil || lease.Endpoint != bundle.Endpoint || env[computeResourcePrefix(name, "workers")+"CONTROL_NETWORK_NAME"] != bundle.ControlNetwork {
				t.Fatal("automatic private projection mismatch", err)
			}
			originalKeys[name] = lease.ClientKeyB64
			for _, other := range []string{"core_one", "core_two"} {
				if other != name && env[computeResourcePrefix(other, "workers")+"CLIENT_KEY"] != "" {
					t.Fatal("sibling private key leaked")
				}
			}
			if env["INCUS_ADMIN_KEY_B64"] != "" || env["INCUS_HOST_CONNECTION_BINDING"] != "" {
				t.Fatal("provider authority leaked to consumer")
			}
		}
		if originalKeys["core_one"] == originalKeys["core_two"] {
			t.Fatal("shared consumer private key")
		}
		body, err := os.ReadFile(filepath.Join(deploymentRoot, "deployment.yml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range append([]string{bundle.AdminPrivateKeyPEM, base64.StdEncoding.EncodeToString([]byte(bundle.AdminPrivateKeyPEM))}, originalKeys["core_one"], originalKeys["core_two"]) {
			if bytes.Contains(body, []byte(secret)) {
				t.Fatal("secret was embedded in public deployment metadata")
			}
		}
	})
	step("real_compose_apply_and_nonroot_image_import", func(t *testing.T) {
		coreNativeCLI(t, "apply", "apply", "-w", workspace, "--deployment", deploymentID, "--yes", "--no-snapshot")
		containers := strings.Fields(string(coreNativeCommand(t, "no-persistent-provider", nil, "/usr/bin/docker", "ps", "-aq",
			"--filter", "label=dev.anas.native-core="+id)))
		if len(containers) != 2 {
			t.Fatal("run-only Provider was started as a persistent service or a required consumer is missing")
		}
		for _, name := range []string{"core_one", "core_two"} {
			container := containerPrefix + name
			deadline := time.Now().Add(25 * time.Second)
			for {
				body := coreNativeCommand(t, "consumer-log", nil, "/usr/bin/docker", "logs", container)
				var result struct {
					Schema, Consumer string
					Passed           bool
					FailureCode      string `json:"failure_code"`
					HTTPStatus       int    `json:"http_status"`
					ProbeScope       string `json:"probe_scope"`
				}
				if json.Unmarshal(bytes.TrimSpace(body), &result) == nil && result.Schema == "anas.native-core-consumer/v1" && result.Consumer == name && result.Passed && result.ProbeScope == "own_project_ready" {
					break
				}
				if result.Schema == "anas.native-core-consumer/v1" && result.Consumer == name && !result.Passed {
					switch result.FailureCode {
					case "probe_failed", "container_marker", "process_permissions", "environment_scope", "lease_projection", "client_key_pair", "server_certificate", "server_pin", "peer_pin", "request_failed", "response_read", "response_metadata", "own_project_status", "foreign_project_status":
						if result.HTTPStatus == 0 || (result.HTTPStatus >= 100 && result.HTTPStatus <= 599) {
							t.Fatalf("native consumer rejected at fixed stage %s (HTTP status %d)", result.FailureCode, result.HTTPStatus)
						}
					}
					t.Fatal("native consumer returned a failed or invalid diagnostic")
				}
				if time.Now().After(deadline) {
					t.Fatal("real consumer could not use its own restricted lease")
				}
				time.Sleep(200 * time.Millisecond)
			}
			var inspected []struct {
				State  struct{ Running bool }
				Config struct {
					User   string
					Labels map[string]string
				}
				NetworkSettings struct {
					Networks map[string]struct {
						NetworkID  string
						GwPriority int
					}
				}
			}
			if json.Unmarshal(coreNativeCommand(t, "consumer-inspect", nil, "/usr/bin/docker", "inspect", container), &inspected) != nil || len(inspected) != 1 ||
				!inspected[0].State.Running || inspected[0].Config.User != "65532:65532" || inspected[0].Config.Labels["dev.anas.native-core"] != id || len(inspected[0].NetworkSettings.Networks) != 2 ||
				inspected[0].NetworkSettings.Networks[networkPrefix+name].GwPriority != 100 || inspected[0].NetworkSettings.Networks[bundle.ControlNetwork].NetworkID == "" {
				t.Fatal("consumer did not retain its independent business gateway and actual control bridge")
			}
			composeProjects[name] = inspected[0].Config.Labels["com.docker.compose.project"]
			if composeProjects[name] == "" {
				t.Fatal("native consumer has no actual Compose project ownership")
			}
			var state resourceState
			if readYAML(filepath.Join(stateDir(workspace), "state", "resources", name+".workers.yml"), &state) != nil || state.Status != "ready" || state.Actual.ClientCertificateSecret == "" {
				t.Fatal("actual Provider operation was not recorded ready through Core")
			}
		}
	})
	step("two_existing_projects_are_mutually_restricted", func(t *testing.T) {
		// Core activates modules in dependency order. Each service starts with
		// its own readiness probe only; a missing later project is not isolation
		// evidence (and some daemons return 500). Independently prove all projects
		// exist, then run a finite fresh probe from BOTH actual containers.
		for _, name := range []string{"core_one", "core_two", "default"} {
			project := "default"
			if name != "default" {
				project = "anas-" + strings.ReplaceAll(name, "_", "-")
			}
			var observed struct {
				Name   string            `json:"name"`
				Config map[string]string `json:"config"`
			}
			if coreNativeQuery(t, http.MethodGet, "/1.0/projects/"+project, &observed) != 200 || observed.Name != project ||
				(name != "default" && observed.Config["restricted"] != "true") {
				t.Fatal("project absence cannot substitute for cross-project rejection")
			}
		}
		for _, name := range []string{"core_one", "core_two"} {
			body := coreNativeCommand(t, "post-apply-project-probe", nil, "/usr/bin/docker", "exec", containerPrefix+name,
				"/usr/local/bin/consumer", name, "--once")
			var result struct {
				Schema, Consumer string
				Passed           bool
				ProbeScope       string `json:"probe_scope"`
			}
			if json.Unmarshal(body, &result) != nil || result.Schema != "anas.native-core-consumer/v1" || result.Consumer != name || !result.Passed || result.ProbeScope != "existing_project_isolation" {
				t.Fatal("post-apply existing-project authorization was not verified")
			}
		}
	})
	step("repeat_cli_render_and_apply_preserves_credentials", func(t *testing.T) {
		// Frozen deployment states are not replayable commands. Assert the
		// established ready-only rejection, then exercise a second real render
		// and apply rather than editing state or weakening the activation guard.
		coreNativeRejectActiveApply(t, workspace, deploymentID)
		result := coreNativeCLI(t, "repeat-render", "render", "-w", workspace, "--root", moduleRoot)
		var next string
		if json.Unmarshal(result["deployment_id"], &next) != nil || next == deploymentID {
			t.Fatal("new frozen deployment was not created")
		}
		for _, name := range []string{"core_one", "core_two"} {
			env, err := parseEnvFile(filepath.Join(stateDir(workspace), "deployments", next, "modules", name, ".env"))
			if err != nil || env[computeResourcePrefix(name, "workers")+"CLIENT_KEY"] != originalKeys[name] {
				t.Fatal("new deployment replaced an established client identity", err)
			}
		}
		coreNativeCLI(t, "apply-new-render", "apply", "-w", workspace, "--deployment", next, "--yes", "--no-snapshot")
		active, err := loadActiveState(stateDir(workspace))
		if err != nil || active.ActiveDeployment != next {
			t.Fatal("the newly rendered deployment was not actually activated", err)
		}
		for _, name := range []string{"core_one", "core_two"} {
			body := coreNativeCommand(t, "repeat-project-probe", nil, "/usr/bin/docker", "exec", containerPrefix+name,
				"/usr/local/bin/consumer", name, "--once")
			var result struct {
				Schema, Consumer string
				Passed           bool
				ProbeScope       string `json:"probe_scope"`
			}
			if json.Unmarshal(body, &result) != nil || result.Schema != "anas.native-core-consumer/v1" ||
				result.Consumer != name || !result.Passed || result.ProbeScope != "existing_project_isolation" {
				t.Fatal("new deployment activation lost its existing restricted consumer identity")
			}
		}
		deploymentID, deploymentRoot = next, filepath.Join(stateDir(workspace), "deployments", next)
	})
	// INCUS-R-111: removing a consumer through the public config and apply path
	// must withdraw its restricted certificate while the project stays.
	moduleRoots := map[string]string{"core_one": "", "core_two": ""}
	step("removed_consumer_lease_is_revoked", func(t *testing.T) {
		moduleRoots["core_two"] = filepath.Join(deploymentRoot, "modules", "core_two")
		certificate := coreNativeLeaseFingerprint(t, "core_two", filepath.Join(moduleRoots["core_two"], ".env"))
		if coreNativeQuery(t, http.MethodGet, "/1.0/certificates/"+certificate, nil) != 200 {
			t.Fatal("removal control needs the consumer certificate to be trusted first")
		}
		config := "modules:\n  core_one: {}\nglobal:\n  base_domain: core.native.test\n  email: admin@core.native.test\n  virtual_domain: true\nrollback:\n  snapshot: {backend: none}\nenv:\n  IPv6: 'false'\n  ANAS_IMAGE_REGISTRY: " + registry + "\n  CONTAINER_PREFIX: " + containerPrefix + "\n  NETWORK_PREFIX: " + networkPrefix + "\n"
		write(filepath.Join(coreNativeRoot, "source-config-removal.yml"), []byte(config), 0600)
		imported := coreNativeCLI(t, "config-import-removal", "config", "import", filepath.Join(coreNativeRoot, "source-config-removal.yml"),
			"-w", workspace, "--root", moduleRoot)
		if !bytes.Equal(imported["secrets_imported"], []byte("0")) {
			t.Fatal("removal import supplied credentials")
		}
		result := coreNativeCLI(t, "removal-render", "render", "-w", workspace, "--root", moduleRoot, "--update-lock")
		var next string
		if json.Unmarshal(result["deployment_id"], &next) != nil || next == deploymentID {
			t.Fatal("removal did not render a new deployment")
		}
		nextRoot := filepath.Join(stateDir(workspace), "deployments", next)
		manifest, err := loadDeploymentManifest(nextRoot)
		if err != nil || len(manifest.Resources) != 1 || manifest.Resources[0].Consumer != "core_one" {
			t.Fatal("removed consumer's lease is still declared", err)
		}
		coreNativeCLI(t, "apply-removal", "apply", "-w", workspace, "--deployment", next, "--yes", "--no-snapshot")
		if coreNativeQuery(t, http.MethodGet, "/1.0/certificates/"+certificate, nil) != 404 {
			t.Fatal("removed consumer's restricted certificate is still trusted")
		}
		var project struct {
			Config map[string]string `json:"config"`
		}
		if coreNativeQuery(t, http.MethodGet, "/1.0/projects/anas-core-two", &project) != 200 || project.Config["restricted"] != "true" {
			t.Fatal("revocation removed or unfenced the retained lease project")
		}
		var state resourceState
		if readYAML(filepath.Join(stateDir(workspace), "state", "resources", "core_two.workers.yml"), &state) != nil ||
			state.Status != "retained" || state.Revocation != resourceRevocationConfirmed {
			t.Fatal("Core did not record the confirmed revocation of the retained lease")
		}
		body := coreNativeCommand(t, "survivor-probe", nil, "/usr/bin/docker", "exec", containerPrefix+"core_one",
			"/usr/local/bin/consumer", "core_one", "--once")
		var survivor struct {
			Schema, Consumer string
			Passed           bool
			ProbeScope       string `json:"probe_scope"`
		}
		if json.Unmarshal(body, &survivor) != nil || survivor.Consumer != "core_one" || !survivor.Passed || survivor.ProbeScope != "existing_project_isolation" {
			t.Fatal("revoking the removed lease disturbed the remaining consumer")
		}
		deploymentID, deploymentRoot = next, nextRoot
	})
	// INCUS-R-072: a new revision imported by an apply that then fails to
	// activate is referenced by no rollback target. Apply itself deletes
	// nothing; only the explicit, confirmed host prune may remove it.
	var pinR2 string
	step("failed_activation_leaves_new_revision_outside_rollback", func(t *testing.T) {
		second := coreNativeFixtureImage(t, provider, "lab-r2")
		pinR2 = second.Fingerprint
		catalog, _ := json.Marshal([]computeimage.Entry{entry, second})
		write(filepath.Join(provider, "images", "catalog.json"), catalog, 0600)
		manifestPath := filepath.Join(moduleRoot, "core_one", "module.yml")
		composePath := filepath.Join(moduleRoot, "core_one", "docker-compose.yml")
		write(manifestPath, []byte(coreNativeConsumerManifestRevision("core_one", "lab-r2")), 0600)
		write(composePath, []byte(coreNativeComposeWithExtraNetwork("core_one", id, "anas-native-core-missing-network")), 0600)
		result := coreNativeCLI(t, "failing-render", "render", "-w", workspace, "--root", moduleRoot, "--update-lock")
		var failing string
		if json.Unmarshal(result["deployment_id"], &failing) != nil || failing == deploymentID {
			t.Fatal("revision change did not render a new deployment")
		}
		manifest, err := loadDeploymentManifest(filepath.Join(stateDir(workspace), "deployments", failing))
		if err != nil || len(manifest.Resources) != 1 || manifest.Resources[0].ComputeImages == nil || manifest.Resources[0].ComputeImages.Images[0].Fingerprint != pinR2 {
			t.Fatal("new revision was not frozen into the failing deployment", err)
		}
		if code := coreNativeCLIFailure(t, "failing-apply", "apply", "-w", workspace, "--deployment", failing, "--yes", "--no-snapshot"); code != "start_failed" {
			t.Fatalf("activation failed with %q, want a start failure after resources were ensured", code)
		}
		active, err := loadActiveState(stateDir(workspace))
		if err != nil || active.ActiveDeployment != deploymentID || slices.Contains(active.PreviousDeployments, failing) {
			t.Fatal("failed activation changed the active deployment or rollback history", err)
		}
		var imported []struct {
			Fingerprint string `json:"fingerprint"`
		}
		coreNativeQuery(t, http.MethodGet, "/1.0/images?recursion=1&project=anas-core-one", &imported)
		seen := map[string]bool{}
		for _, image := range imported {
			seen[image.Fingerprint] = true
		}
		if len(imported) != 2 || !seen[pin] || !seen[pinR2] {
			t.Fatal("apply deleted an older revision or never imported the new one")
		}
		// Revert the consumer as an operator would: restore its sources and
		// relock with a render that is never applied. The failed deployment
		// stays as recorded; the catalog keeps lab-r2, so the lock must follow.
		write(manifestPath, []byte(coreNativeConsumerManifest("core_one")), 0600)
		write(composePath, []byte(coreNativeCompose("core_one", id)), 0600)
		reverted := coreNativeCLI(t, "revert-render", "render", "-w", workspace, "--root", moduleRoot, "--update-lock")
		var revertedID string
		if json.Unmarshal(reverted["deployment_id"], &revertedID) != nil {
			t.Fatal("revert render returned no deployment")
		}
		revertedManifest, err := loadDeploymentManifest(filepath.Join(stateDir(workspace), "deployments", revertedID))
		if err != nil || len(revertedManifest.Resources) != 1 || revertedManifest.Resources[0].ComputeImages == nil ||
			revertedManifest.Resources[0].ComputeImages.Images[0].Fingerprint != pin {
			t.Fatal("revert render did not freeze the active revision again", err)
		}
	})
	step("stopped_for_explicit_prune", func(t *testing.T) {
		coreNativeCLI(t, "stop", "stop", "-w", workspace)
		active, err := loadActiveState(stateDir(workspace))
		if err != nil || active.RuntimeStatus != "stopped" {
			t.Fatal("workspace did not record a stopped runtime", err)
		}
		moduleRoots["core_one"] = filepath.Join(deploymentRoot, "modules", "core_one")
		body, err := json.Marshal(coreNativeState{ID: id, DockerID: dockerID, PinR1: pin, PinR2: pinR2,
			ModuleRoots: moduleRoots, ComposeProjects: composeProjects, Images: images})
		if err != nil {
			t.Fatal(err)
		}
		write(coreNativeStateFile, body, 0600)
	})
}

// TestNativeCoreComputeCleanup runs after the orchestrator's confirmed image
// prune. Each lease must hold exactly the lab-r1 image again, which is the
// independent proof that prune removed only the failed deployment's revision.
func TestNativeCoreComputeCleanup(t *testing.T) {
	id, _ := coreNativeGuard(t)
	body, err := os.ReadFile(coreNativeStateFile)
	var state coreNativeState
	if err != nil || json.Unmarshal(body, &state) != nil || state.ID != id || state.PinR1 == "" {
		t.Fatal("completed Core projection state required first")
	}
	for _, name := range []string{"core_one", "core_two"} {
		dir := state.ModuleRoots[name]
		coreNativeCommand(t, "down-"+name, nil, "/usr/bin/docker", "compose", "--project-name", state.ComposeProjects[name], "--project-directory", dir, "-f", filepath.Join(dir, "docker-compose.yml"), "down")
		coreNativeCleanupLease(t, name, state.PinR1, filepath.Join(dir, ".env"), name == "core_two")
	}
	for _, image := range state.Images {
		coreNativeCommand(t, "remove-fixture-image", nil, "/usr/bin/docker", "image", "rm", image)
	}
	if len(bytes.TrimSpace(coreNativeCommand(t, "containers-empty", nil, "/usr/bin/docker", "ps", "-aq"))) != 0 || strings.TrimSpace(string(coreNativeCommand(t, "same-daemon", nil, "/usr/bin/docker", "info", "--format", "{{.ID}}"))) != state.DockerID {
		t.Fatal("test cleanup did not restore empty container inventory and daemon identity")
	}
	if err := os.MkdirAll(coreNativeReports+"/reports", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coreNativeReports+"/reports/core-stage.json", []byte(`{"core_cli_compose_passed":true,"synthetic_consumers":2,"production_hook_and_provider":true,"bootable_guest_or_signed_release":false}`), 0600); err != nil {
		t.Fatal(err)
	}
}

// coreNativeFixtureImage builds one measured split fixture and places it in the
// Provider's trusted artifact tree. Different releases have different bytes.
func coreNativeFixtureImage(t *testing.T, provider, revision string) computeimage.Entry {
	t.Helper()
	artifactDir := filepath.Join(coreNativeRoot, "image-fixture-"+revision)
	for path, body := range map[string]string{
		filepath.Join(artifactDir, "rootfs", "fixture.txt"): "native Core image import fixture " + revision + "; not a bootable production image\n",
		filepath.Join(artifactDir, "metadata.yaml"):         "architecture: x86_64\ncreation_date: 1780000000\nproperties:\n  description: ANAS native Core projection fixture\n  os: fixture\n  release: " + revision + "\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0444); err != nil {
			t.Fatal(err)
		}
	}
	meta, rootfs := filepath.Join(artifactDir, "incus.tar.xz"), filepath.Join(artifactDir, "rootfs.squashfs")
	coreNativeCommand(t, "metadata-archive-"+revision, nil, "/usr/bin/tar", "-C", artifactDir, "-cJf", meta, "metadata.yaml")
	coreNativeCommand(t, "rootfs-archive-"+revision, nil, "/usr/bin/mksquashfs", filepath.Join(artifactDir, "rootfs"), rootfs, "-noappend", "-processors", "1", "-no-progress", "-all-root")
	metadata, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, err := os.ReadFile(rootfs)
	if err != nil {
		t.Fatal(err)
	}
	entry := computeimage.Entry{Catalog: "anas", Name: "native-core", Revision: revision,
		Target: computeimage.Target{Architecture: "amd64", Interface: "incus_container"}, Fingerprint: sha256Hex(append(append([]byte{}, metadata...), rootBytes...)),
		RecipeDigest: sha256Hex([]byte("explicit native import fixture, not distrobuilder or signed release"))}
	writeProviderArtifactFixture(t, provider, entry, metadata, rootBytes)
	return entry
}

// coreNativeCLIFailure runs a command that must fail and returns its stable
// CLI error code; output stays in the private diagnostics directory.
func coreNativeCLIFailure(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/local/bin/anas", append(args, "--json")...)
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LC_ALL=C", "LANG=C", "GOPROXY=off"}
	var stdout, stderr coreNativeBuffer
	cmd.Stdout, cmd.Stderr, cmd.WaitDelay = &stdout, &stderr, 3*time.Second
	err := cmd.Run()
	_ = os.MkdirAll(coreNativeReports+"/private", 0700)
	_ = os.WriteFile(filepath.Join(coreNativeReports, "private", name+".log"), append(append([]byte{}, stdout.Bytes()...), stderr.Bytes()...), 0600)
	var failure struct {
		OK    bool                  `json:"ok"`
		Error struct{ Code string } `json:"error"`
	}
	if err == nil || ctx.Err() != nil || stdout.truncated || json.Unmarshal(stdout.Bytes(), &failure) != nil || failure.OK {
		t.Fatalf("command %s did not fail with a CLI error envelope", name)
	}
	return failure.Error.Code
}

// Only GET/DELETE of independently verified objects in the two fresh test
// leases. It never provisions a project or uses a management key in a consumer.
func coreNativeQuery(t *testing.T, method, path string, out any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", "/var/lib/incus/unix.socket")
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, method, "http://unix"+path, nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal("read/delete of exact native test object failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	var envelope struct {
		Type      string          `json:"type"`
		Code      int             `json:"status_code"`
		Operation string          `json:"operation"`
		Metadata  json.RawMessage `json:"metadata"`
	}
	if err != nil || len(body) > 4<<20 || json.Unmarshal(body, &envelope) != nil {
		t.Fatal("invalid test cleanup response")
	}
	if response.StatusCode == 404 {
		return 404
	}
	if response.StatusCode == 202 && method == http.MethodDelete && regexp.MustCompile(`^/1\.0/operations/[a-f0-9-]{36}$`).MatchString(envelope.Operation) {
		var operation struct {
			StatusCode int    `json:"status_code"`
			Err        string `json:"err"`
		}
		coreNativeQuery(t, http.MethodGet, envelope.Operation+"/wait?timeout=30", &operation)
		if operation.StatusCode != 200 || operation.Err != "" {
			t.Fatal("asynchronous test cleanup was not completed")
		}
		return 200
	}
	if response.StatusCode != 200 || envelope.Type != "sync" || envelope.Code != 200 {
		t.Fatal("test cleanup operation rejected")
	}
	if out != nil && json.Unmarshal(envelope.Metadata, out) != nil {
		t.Fatal("test cleanup inventory decode failed")
	}
	return 200
}

func coreNativeLeaseFingerprint(t *testing.T, name, envPath string) string {
	t.Helper()
	env, err := parseEnvFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	certBytes, err := base64.StdEncoding.DecodeString(env[computeResourcePrefix(name, "workers")+"CLIENT_CERT"])
	block, _ := pem.Decode(certBytes)
	if err != nil || block == nil {
		t.Fatal("test client public identity missing")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// revoked marks a lease whose certificate Core already withdrew; its absence
// is asserted rather than deleted.
func coreNativeCleanupLease(t *testing.T, name, pin, envPath string, revoked bool) {
	t.Helper()
	project := "anas-" + strings.ReplaceAll(name, "_", "-")
	var got struct {
		Description string            `json:"description"`
		Config      map[string]string `json:"config"`
	}
	coreNativeQuery(t, http.MethodGet, "/1.0/projects/"+project, &got)
	if got.Description != "ANAS compute lease for "+name+" (container tier)" || got.Config["restricted"] != "true" {
		t.Fatal("test project ownership changed")
	}
	var instances []json.RawMessage
	coreNativeQuery(t, http.MethodGet, "/1.0/instances?recursion=1&project="+project, &instances)
	if instances == nil || len(instances) != 0 {
		t.Fatal("test cleanup will not delete guest data")
	}
	fingerprint := coreNativeLeaseFingerprint(t, name, envPath)
	var trust struct {
		Name       string   `json:"name"`
		Restricted bool     `json:"restricted"`
		Projects   []string `json:"projects"`
	}
	status := coreNativeQuery(t, http.MethodGet, "/1.0/certificates/"+fingerprint, &trust)
	switch {
	case revoked && status != 404:
		t.Fatal("revoked test certificate reappeared")
	case !revoked && (status != 200 || trust.Name != "anas-"+name || !trust.Restricted || !slices.Equal(trust.Projects, []string{project})):
		t.Fatal("test trust identity changed")
	case !revoked:
		coreNativeQuery(t, http.MethodDelete, "/1.0/certificates/"+fingerprint, nil)
	}
	var imported []struct {
		Fingerprint string `json:"fingerprint"`
	}
	coreNativeQuery(t, http.MethodGet, "/1.0/images?recursion=1&project="+project, &imported)
	if len(imported) != 1 || imported[0].Fingerprint != pin {
		t.Fatal("test image inventory changed")
	}
	coreNativeQuery(t, http.MethodDelete, "/1.0/images/"+pin+"?project="+project, nil)
	coreNativeQuery(t, http.MethodDelete, "/1.0/profiles/anas-lease?project="+project, nil)
	coreNativeQuery(t, http.MethodDelete, "/1.0/projects/"+project, nil)
	bridge := computeclient.NetworkName(project)
	var network struct {
		Config map[string]string `json:"config"`
		UsedBy []string          `json:"used_by"`
	}
	coreNativeQuery(t, http.MethodGet, "/1.0/networks/"+bridge, &network)
	if network.Config["user.anas.consumer"] != name || network.Config["user.anas.sandbox"] != project || len(network.UsedBy) != 0 {
		t.Fatal("test bridge is not exclusively owned and unused")
	}
	coreNativeQuery(t, http.MethodDelete, "/1.0/networks/"+bridge, nil)
	// The source fence ACL shares the bridge name and is freed after it.
	var acl struct {
		Config map[string]string `json:"config"`
		UsedBy []string          `json:"used_by"`
	}
	coreNativeQuery(t, http.MethodGet, "/1.0/network-acls/"+bridge, &acl)
	if acl.Config["user.anas.consumer"] != name || acl.Config["user.anas.sandbox"] != project || len(acl.UsedBy) != 0 {
		t.Fatal("test source fence ACL is not exclusively owned and unused")
	}
	coreNativeQuery(t, http.MethodDelete, "/1.0/network-acls/"+bridge, nil)
}

func TestNativeCoreRejectsRevokedAutomaticConnection(t *testing.T) {
	coreNativeGuard(t)
	if _, err := os.Stat(coreNativeReports + "/reports/core-stage.json"); err != nil {
		t.Fatal("completed Core projection gate required first")
	}
	if _, err := os.Stat(incusprovision.DefaultBundlePath); !os.IsNotExist(err) {
		t.Fatal("host connection must first be revoked by the approved host action")
	}
	workspace := coreNativeWorkspace
	before, err := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/local/bin/anas", "render", "-w", workspace, "--root", filepath.Join(coreNativeRoot, "source", "modules"), "--json")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "HOME=/root"}
	var stdout, stderr coreNativeBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil || ctx.Err() != nil || stdout.truncated || stderr.truncated {
		t.Fatal("revoked host bundle was replaced by previously persisted credentials")
	}
	var failure struct {
		OK    bool                           `json:"ok"`
		Error struct{ Code, Message string } `json:"error"`
	}
	if json.Unmarshal(stdout.Bytes(), &failure) != nil || failure.OK || failure.Error.Code != "calculate_failed" ||
		!strings.Contains(failure.Error.Message, "incus") || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != exitFailure {
		// The code is a fixed identifier and safe for the private report.
		t.Fatalf("unrelated CLI failure (code %q) cannot prove revoked automatic credentials were rejected", failure.Error.Code)
	}
	after, readErr := os.ReadFile(filepath.Join(stateDir(workspace), "secrets.yml"))
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatal("failed render modified previous private credential state")
	}
	entries, readErr := os.ReadDir(filepath.Join(stateDir(workspace), "staging"))
	if readErr != nil || len(entries) != 0 {
		t.Fatal("rejected render left a staged deployment")
	}
	if bytes.Contains(stdout.Bytes(), []byte("PRIVATE KEY")) || bytes.Contains(stderr.Bytes(), []byte("PRIVATE KEY")) {
		t.Fatal("rejected render disclosed private material")
	}
}
