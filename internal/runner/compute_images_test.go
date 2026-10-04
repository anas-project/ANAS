package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/computeimage"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
	"gopkg.in/yaml.v3"
)

func catalogFixture(t *testing.T, a *app) (string, []computeimage.Entry) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "images"), 0700); err != nil {
		t.Fatal(err)
	}
	entries := []computeimage.Entry{{Catalog: "anas", Name: "runner", Revision: "r1", Target: computeimage.Target{Architecture: "amd64", Interface: "incus_vm"}, Fingerprint: strings.Repeat("b", 64), RecipeDigest: strings.Repeat("c", 64)}}
	path := filepath.Join(dir, "images", "catalog.json")
	body, _ := json.Marshal(entries)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	a.reg["incus"] = Module{Name: "incus", EnvPrefix: "INCUS", SourceDir: dir}
	return path, entries
}

func TestStructuredComputeImagesThroughRealConsumerManifests(t *testing.T) {
	for _, name := range []string{"forgejo", "ai_agent"} {
		t.Run(name, func(t *testing.T) {
			a := computeApp(t, map[string]string{name: "anas-test"})
			mod, err := loadModuleManifest(filepath.Join("../..", "modules", name), name)
			if err != nil {
				t.Fatal(err)
			}
			var resource ResourceRequirement
			for _, r := range mod.Resources {
				if r.Contract == "compute" {
					resource = r
				}
			}
			resource.EnabledBy = ""
			mod.Resources = []ResourceRequirement{resource}
			a.reg[name] = mod
			parameter := "actions_runner_image"
			var value any = map[string]any{"fingerprint": strings.Repeat("a", 64)}
			if name == "ai_agent" {
				parameter = "agent_runtime_images"
				value = map[string]any{"codex": value, "claude_code": map[string]any{"catalog": "anas", "name": "runner", "revision": "r1"}}
			}
			a.cfg = &config.File{Modules: config.ModuleSelection{Order: []string{name}, Values: map[string]config.ModuleConfig{name: {Config: map[string]any{parameter: value}}}}}
			for k, v := range configBaseEnv(a.cfg, a.reg) {
				a.env[k] = v
			}
			catalogFixture(t, a)
			if err := a.materializeResourceSecrets(); err != nil {
				t.Fatal(err)
			}
			if err := a.publishModuleResources(name); err != nil {
				t.Fatal(err)
			}
			req := a.resourceRequests[0]
			prefix := computeResourcePrefix(name, resource.ID)
			if strings.Contains(a.env[prefix+"IMAGE_ALLOWLIST"], "catalog") {
				t.Fatal("consumer received unresolved image")
			}
			if name == "ai_agent" {
				want := map[string]string{"codex": strings.Repeat("a", 64), "claude_code": strings.Repeat("b", 64)}
				var got map[string]string
				json.Unmarshal([]byte(a.env[prefix+"IMAGE_BINDINGS"]), &got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("bindings=%v", got)
				}
				if req.ComputeImages.Images[0].CatalogDigest == "" {
					t.Fatal("catalog digest was not frozen")
				}
			}
			if _, _, err := validateComputeRequest(req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestComputeDeploymentRoundTripUsesFrozenCatalogAndDetectsTampering(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	a.cfg = &config.File{}
	a.secrets.path = filepath.Join(a.base, "secrets.yml")
	configPath := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(configPath, []byte("modules: {}\n"), 0600)
	path, _ := catalogFixture(t, a)
	mod := a.reg["forgejo"]
	mod.Resources[0].Spec["image_allowlist"] = []any{map[string]any{"catalog": "anas", "name": "runner", "revision": "r1"}}
	a.reg["forgejo"] = mod
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	id := "20260910T010203Z-aaaaaaaa"
	os.MkdirAll(filepath.Join(a.base, "staging", id, "modules", "forgejo"), 0700)
	manifest, err := buildDeploymentManifest(a, id, configPath, false)
	if err != nil {
		t.Fatal(err)
	}
	// A change in live inputs cannot modify the deployment snapshot.
	a.resourceRequests[0].Spec["quota"].(map[string]any)["cpu"] = 32
	a.resourceRequests[0].ComputeImages.Images[0].Fingerprint = strings.Repeat("d", 64)
	resource := manifest.Resources[0]
	if resource.ComputeImages.Images[0].Fingerprint != strings.Repeat("b", 64) || resource.Spec["quota"].(map[string]any)["cpu"] != 4 {
		t.Fatal("snapshot aliases live inputs")
	}
	root := filepath.Join(a.base, "deployments", id)
	os.MkdirAll(root, 0700)
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	// Missing or replaced currently installed catalog must not affect rollback.
	os.WriteFile(path, []byte("invalid latest catalog"), 0600)
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	restored, _, _, err := loadDeploymentApp(a.base, id, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	if _, pins, err := validateComputeRequest(restored.resourceRequests[0]); err != nil || !reflect.DeepEqual(pins, []string{strings.Repeat("b", 64)}) {
		t.Fatalf("pins=%v err=%v", pins, err)
	}
	restored.env["INCUS_SERVER_CERT_B64"] = a.env["INCUS_SERVER_CERT_B64"]
	restored.env["INCUS_ENDPOINT"] = a.env["INCUS_ENDPOINT"]
	if err := restored.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	if restored.env[computeResourcePrefix("forgejo", "runners")+"IMAGE_ALLOWLIST"] != strings.Repeat("b", 64) {
		t.Fatal("rollback projection changed")
	}
	// Old metadata is still readable so a new apply can replace the current
	// deployment, but it must fail before starting any service or host action.
	legacy := *manifest
	legacy.Resources = append([]deploymentResource{}, manifest.Resources...)
	legacy.Resources[0].Spec = cloneAnyMap(manifest.Resources[0].Spec)
	legacy.Resources[0].Spec["image_allowlist"] = []any{strings.Repeat("b", 64)}
	legacy.Resources[0].ComputeImages = nil
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), &legacy, 0600); err != nil {
		t.Fatal(err)
	}
	old, _, _, err := loadDeploymentApp(a.base, id, compose.CLI{})
	if err != nil {
		t.Fatalf("old metadata cannot be loaded for replacement: %v", err)
	}
	if err := startDeployment(old, root, old.order, false); err == nil || !strings.Contains(err.Error(), "image_allowlist") {
		t.Fatalf("legacy execution was not refused at the image boundary: %v", err)
	}
	manifest.Resources[0].ComputeImages.Images[0].CatalogDigest = strings.Repeat("f", 64)
	writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600)
	if _, _, _, err := loadDeploymentApp(a.base, id, compose.CLI{}); err == nil {
		t.Fatal("accepted corrupted snapshot")
	}
}

func TestComputeCatalogHistorySurvivesDeploymentRetention(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	path, entries := catalogFixture(t, a)
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := a.recordComputeImageHistory(); err != nil {
		t.Fatal(err)
	}
	entries[0].Fingerprint = strings.Repeat("d", 64)
	body, _ := json.Marshal(entries)
	os.WriteFile(path, body, 0600)
	if err := a.materializeResourceSecrets(); err == nil {
		t.Fatal("accepted changed version key after deployment retention")
	}
}

func TestComputeTargetAndUnresolvedReferenceFailBeforeSecrets(t *testing.T) {
	for _, mode := range []string{"missing architecture", "missing catalog", "wrong target", "bad object"} {
		t.Run(mode, func(t *testing.T) {
			a := computeApp(t, map[string]string{"forgejo": "anas-test"})
			switch mode {
			case "missing architecture":
				delete(a.env, "INCUS_IMAGE_ARCHITECTURE")
			case "missing catalog", "wrong target":
				if mode == "wrong target" {
					catalogFixture(t, a)
					a.env["INCUS_IMAGE_ARCHITECTURE"] = "arm64"
				}
				a.reg["forgejo"].Resources[0].Spec["image_allowlist"] = []any{map[string]any{"catalog": "anas", "name": "runner", "revision": "r1"}}
			case "bad object":
				a.reg["forgejo"].Resources[0].Spec["image_allowlist"] = []any{map[string]any{"fingerprint": strings.Repeat("a", 64), "url": "https://untrusted.example"}}
			}
			if err := a.materializeResourceSecrets(); err == nil {
				t.Fatal("accepted invalid reference")
			}
			if len(a.secrets.values) > 0 {
				t.Fatal("minted credential before image validation")
			}
		})
	}
}

func TestStructuredSpecFromAndConfigImport(t *testing.T) {
	reg := map[string]Module{"demo": {Name: "demo", EnvPrefix: "CUSTOM", Types: map[string]ParamType{"image": {Kind: "string", Constraints: configschema.Constraints{Format: configschema.FormatJSONObject}}}}}

	body := []byte("modules:\n  demo:\n    config:\n      image: {fingerprint: abc}\n")
	var tree yaml.Node
	if err := yaml.Unmarshal(body, &tree); err != nil {
		t.Fatal(err)
	}
	if err := normalizeImportedParameterNodes(tree.Content[0], reg); err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(&tree)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.File
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := configBaseEnv(&cfg, reg)["CUSTOM_IMAGE"]; got != `{"fingerprint":"abc"}` {
		t.Fatalf("import projection=%s", got)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, body, 0600)
	settings, err := config.Settings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 1 || settings["modules.demo.config.image"] != `{"fingerprint":"abc"}` {
		t.Fatalf("structured parameter lost setting identity: %v", settings)
	}

	for _, source := range []SpecSource{{Parameter: "image", Projection: "singleton"}, {Parameter: "image", Projection: "values"}} {
		if _, _, err := source.project(strings.Repeat("a", 64)); err == nil {
			t.Fatal("CSV/string accepted by structured projection")
		}
	}
	value, keys, err := (SpecSource{Parameter: "images", Projection: "values"}).project(`{"z":{"fingerprint":"second"},"a":{"fingerprint":"first"}}`)
	if err != nil || !reflect.DeepEqual(keys, []string{"a", "z"}) || len(value.([]any)) != 2 {
		t.Fatalf("projection=%v keys=%v err=%v", value, keys, err)
	}
}

func TestComputeEnsureProjectsFrozenImagesAndOnlyPublicCertificate(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	modules := t.TempDir()
	providerDir := filepath.Join(modules, "incus")
	os.MkdirAll(providerDir, 0700)
	mod, err := loadModuleManifest(filepath.Join("../..", "modules", "incus"), "incus")
	if err != nil {
		t.Fatal(err)
	}
	a.reg["incus"] = mod
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(providerDir, ".env"), []byte("INCUS_ENDPOINT="+a.env["INCUS_ENDPOINT"]+"\nINCUS_SERVER_CERT_B64="+a.env["INCUS_SERVER_CERT_B64"]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "compose-probe")
	// The subprocess checks the same environment Compose interpolates. It does
	// not inspect or dump inherited environment variables or any private keys.
	body := `#!/bin/sh
set -eu
[ "$ANAS_RESOURCE_IMAGE_ARCHITECTURE" = amd64 ]
[ "$ANAS_RESOURCE_IMAGE_ALLOWLIST" = ` + strings.Repeat("a", 64) + ` ]
[ -n "$ANAS_RESOURCE_CLIENT_CERT" ]
[ -z "${ANAS_RESOURCE_CLIENT_KEY:-}" ]
[ -z "${ANAS_COMPUTE_RESOURCE__FORGEJO__RUNNERS__LEASE_SECRET:-}" ]
[ "$ANAS_RESOURCE_NETWORK" = '{"egress":"internet","module_access":false,"intra_lease":false,"ingress":"none"}' ]
` + fakeComputeEnsureEcho
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	a.compose = compose.CLI{Bin: []string{script}}
	previous := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = previous })
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	if err := a.ensureResourcesFor("forgejo", modules); err != nil {
		t.Fatal(err)
	}
	var state resourceState
	if err := readYAML(filepath.Join(a.base, "state", "resources", "forgejo.runners.yml"), &state); err != nil {
		t.Fatal(err)
	}
	if state.Actual.ComputeImages.Images[0].Fingerprint != strings.Repeat("a", 64) || state.Actual.ClientCertificateSecret != a.resourceRequests[0].SecretKey {
		t.Fatal("resource state lost its frozen resolution or secret reference")
	}
	// INCUS-R-159: the bridge, subnet and gateway the Provider reported.
	if n := state.Actual.ComputeNetwork; n == nil || n.Bridge != "lease120067207a" || n.IPv4Subnet != "10.101.0.0/24" || n.IPv4Gateway != "10.101.0.1" {
		t.Fatalf("resource state network = %+v", state.Actual.ComputeNetwork)
	}
}
