package runner

// TEST_CASES: TEMP-T-003
// REQUIREMENTS: TEMP-R-008 TEMP-R-009 TEMP-R-040

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

func temporaryManifestFixture(t *testing.T, declaration, compose string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "api_version: anas.module/v1\nkind: Module\nname: demo\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi: {supports: [anas.module-hook/v1]}\nruntime: {type: compose, compose_file: docker-compose.yml}\n" + declaration
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const temporaryValidDeclaration = `temporary_directories:
  - name: runtime
    service: demo
    target: /var/lib/runtime
    lifecycle: container
    uid: 1001
    gid: 1001
    mode: "0750"
    min_free_bytes: 8388608
    min_free_inodes: 100
    filesystem: [btrfs, ext4]
    required_features: [hardlink, exec]
`

func TestTemporaryManifestRejectsUnsafeAndAmbiguousDeclarations(t *testing.T) {
	compose := "services:\n  anas_demo:\n    image: example.invalid/demo\n"
	tests := []struct{ name, declaration, compose string }{
		{"unknown_field", strings.Replace(temporaryValidDeclaration, "target:", "container_target:", 1), compose},
		{"missing_owner", strings.Replace(temporaryValidDeclaration, "    uid: 1001\n", "", 1), compose},
		{"unquoted_mode", strings.Replace(temporaryValidDeclaration, `"0750"`, "0750", 1), compose},
		{"world_write", strings.Replace(temporaryValidDeclaration, `"0750"`, `"0777"`, 1), compose},
		{"group_write", strings.Replace(temporaryValidDeclaration, `"0750"`, `"0770"`, 1), compose},
		{"group_write_other_read", strings.Replace(temporaryValidDeclaration, `"0750"`, `"0775"`, 1), compose},
		{"unknown_service", strings.Replace(temporaryValidDeclaration, "service: demo", "service: missing", 1), compose},
		{"root_target", strings.Replace(temporaryValidDeclaration, "/var/lib/runtime", "/", 1), compose},
		{"relative_target", strings.Replace(temporaryValidDeclaration, "/var/lib/runtime", "runtime", 1), compose},
		{"feature", strings.Replace(temporaryValidDeclaration, "hardlink", "unknown", 1), compose},
		{"lifecycle", strings.Replace(temporaryValidDeclaration, "lifecycle: container", "lifecycle: job", 1), compose},
		{"replicas", temporaryValidDeclaration, compose + "    deploy: {replicas: 2}\n"},
		{"inherited_mounts", temporaryValidDeclaration, compose + "    extends: {file: other.yml, service: base}\n"},
		{"mount_overlap", temporaryValidDeclaration, compose + "    volumes: [data:/var/lib]\n"},
		{"tmpfs_overlap", temporaryValidDeclaration, compose + "    tmpfs: [/var/lib/runtime]\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryManifestFixture(t, test.declaration, test.compose)
			if _, err := loadModuleManifest(dir, "demo"); err == nil {
				t.Fatal("unsafe temporary declaration accepted")
			}
		})
	}
}

func TestTemporaryMountRenderingPersistsOnlyModulePlaceholder(t *testing.T) {
	compose := "services:\n  anas_demo:\n    image: example.invalid/demo\n    volumes: [config:/etc/demo:ro]\n  unrelated:\n    image: example.invalid/other\n"
	dir := temporaryManifestFixture(t, temporaryValidDeclaration, compose)
	module, err := loadModuleManifest(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(module.TemporaryDirectories) != 1 || module.TemporaryDirectories[0].Service != "anas_demo" {
		t.Fatalf("declaration service was not resolved: %+v", module.TemporaryDirectories)
	}
	if err := renderTemporaryDirectoryMounts(module, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, module.ComposeFile))
	if err != nil {
		t.Fatal(err)
	}
	var rendered struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &rendered); err != nil {
		t.Fatal(err)
	}
	mount := rendered.Services["anas_demo"].Volumes[1].(map[string]any)
	if mount["source"] != "${ANAS_TEMP_RUNTIME}" || mount["target"] != "/var/lib/runtime" || mount["type"] != "bind" || mount["bind"].(map[string]any)["create_host_path"] != false {
		t.Fatalf("invalid managed bind: %+v", mount)
	}
	if len(rendered.Services["unrelated"].Volumes) != 0 || strings.Contains(string(b), dir) {
		t.Fatal("runtime path or another service leaked into the sealed artifact")
	}
}

func TestTemporaryRuntimeKeysCannotBeConfiguredOrPublished(t *testing.T) {
	if !isRunnerOwnedRuntimeKey("ANAS_TEMP_UNDECLARED", nil) || !isRunnerOwnedRuntimeKey("TEMP_PATH", nil) {
		t.Fatal("temporary storage namespace is not reserved")
	}
	if moduleHookMayWriteKey(Module{Name: "demo", Exports: []string{"ANAS_TEMP_*"}}, "ANAS_TEMP_RUNTIME", true) {
		t.Fatal("calculate Hook can overwrite temporary storage")
	}
	if err := applyHookEnv(map[string]string{}, map[string]string{"ANAS_TEMP_RUNTIME": "/foreign"}); err == nil {
		t.Fatal("render Hook can overwrite temporary storage")
	}
}

func TestTemporaryDeclarationSurvivesFrozenDeploymentWithoutSource(t *testing.T) {
	source := temporaryManifestFixture(t, temporaryValidDeclaration, "services:\n  anas_demo: {image: example.invalid/demo}\n")
	module, err := loadModuleManifest(source, "demo")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	base := stateDir(workspace)
	const id = "temporary-declaration"
	staged := filepath.Join(base, "staging", id, "modules", "demo")
	if err := copyDir(source, staged); err != nil {
		t.Fatal(err)
	}
	if err := renderTemporaryDirectoryMounts(module, staged); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "config.yml")
	if err := os.WriteFile(configPath, []byte("modules: {demo: {}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{base: base, workspace: workspace, cfg: &config.File{}, reg: map[string]Module{"demo": module}, order: []string{"demo"}, env: map[string]string{}}
	manifest, err := buildDeploymentManifest(a, id, configPath, false)
	if err != nil {
		t.Fatal(err)
	}
	want := cloneTemporaryDirectories(module.TemporaryDirectories)
	module.TemporaryDirectories[0].RequiredFeatures[0] = "changed-current-source"
	if !reflect.DeepEqual(manifest.Modules["demo"].TemporaryDirectories, want) {
		t.Fatal("frozen declaration shares mutable source metadata")
	}
	root := filepath.Join(base, "deployments", id)
	if err := copyDir(staged, filepath.Join(root, "modules", "demo")); err != nil {
		t.Fatal(err)
	}
	if err := writeYAMLAtomic(filepath.Join(root, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeEnv(filepath.Join(root, "modules", globalEnvFile), map[string]string{"DATA_PATH": dataDir(workspace)}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	restored, _, _, err := loadDeploymentApp(base, id, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.reg["demo"].TemporaryDirectories, want) {
		t.Fatalf("frozen declaration was lost: %+v", restored.reg["demo"].TemporaryDirectories)
	}
}
