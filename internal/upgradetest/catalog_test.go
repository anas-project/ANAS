package upgradetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCatalogAndExactModuleTransition(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]}]`, 0o644)
	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
	write(t, root, "test-env/config.yml", "modules: {demo: {}}\n", 0o644)
	write(t, root, "test-env/config.targets", "demo\n", 0o644)
	write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r1", "0.9.0-r1"), 0o644)

	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "upgrade-test@example.invalid")
	git(t, root, "config", "user.name", "Upgrade Test")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "base")
	base := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))

	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 2\n", 0o644)
	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r2", "0.9.0-r1"), 0o644)
	_, err := Validate(Options{Root: root, BaseRef: base, Scopes: map[string]bool{"modules": true}})
	if err == nil || !strings.Contains(err.Error(), "changed 1.0.0-r1 -> 1.0.0-r2") {
		t.Fatalf("missing exact transition error = %v", err)
	}

	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r2", "1.0.0-r1"), 0o644)
	result, err := Validate(Options{Root: root, BaseRef: base, Scopes: map[string]bool{"modules": true}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Modules != 1 || result.Suites != 3 || result.Transitions != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.ModuleConfigs) != 1 || result.ModuleConfigs[0] != "test-env/config.yml" {
		t.Fatalf("module configs = %#v", result.ModuleConfigs)
	}
}

func TestValidateRequiresEveryRegisteredModule(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]},{"module":"missing","repository":"anas-module-missing","platforms":["linux/amd64"]}]`, 0o644)
	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
	write(t, root, "test-env/config.yml", "modules: {demo: {}}\n", 0o644)
	write(t, root, "test-env/config.targets", "demo\n", 0o644)
	write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r1", "0.9.0-r1"), 0o644)

	_, err := Validate(Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), `registered module "missing" has no upgrade test entry`) {
		t.Fatalf("missing module error = %v", err)
	}
}

func TestValidateRequiresSuiteModuleInConfig(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]}]`, 0o644)
	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
	write(t, root, "test-env/config.yml", "modules: {another: {}}\n", 0o644)
	write(t, root, "test-env/config.targets", "demo\n", 0o644)
	write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r1", "0.9.0-r1"), 0o644)

	_, err := Validate(Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "declares module demo but config test-env/config.yml does not select it") {
		t.Fatalf("suite config coverage error = %v", err)
	}
}

func TestValidateRejectsSuiteTargetInventoryDrift(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]}]`, 0o644)
	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
	write(t, root, "test-env/config.yml", "modules: {demo: {}}\n", 0o644)
	write(t, root, "test-env/config.targets", "unassigned\n", 0o644)
	write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r1", "0.9.0-r1"), 0o644)

	_, err := Validate(Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "targets [unassigned] do not match catalog modules [demo]") {
		t.Fatalf("suite target drift error = %v", err)
	}
}

func TestValidateRejectsModuleFixtureBuildInputs(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "global build speedup",
			config: "modules: {demo: {}}\nglobal: {chinese_build_speedup: true}\n",
			want:   "global.chinese_build_speedup is true",
		},
		{
			name:   "top-level build environment",
			config: "modules: {demo: {}}\nenv: {APT_MIRROR_URL: https://mirror.invalid}\n",
			want:   "top-level env sets build inputs",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]}]`, 0o644)
			write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
			write(t, root, "test-env/config.yml", test.config, 0o644)
			write(t, root, "test-env/config.targets", "demo\n", 0o644)
			write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
			write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
			write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
			write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r1", "0.9.0-r1"), 0o644)

			_, err := Validate(Options{Root: root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("build input error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateRejectsSuiteModuleWithoutAssignedTransition(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]}]`, 0o644)
	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
	write(t, root, "test-env/config.yml", "modules: {demo: {}}\n", 0o644)
	write(t, root, "test-env/config.targets", "demo\n", 0o644)
	write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
	catalog := catalogFixture("1.0.0-r1", "0.9.0-r1") + `  - id: stray-suite
    kind: module
    runner: test-env/runner.sh
    config: test-env/config.yml
    targets: test-env/config.targets
    seed: test-env/seed.sh
    verify: test-env/verify.sh
    report: test-env/verify.sh
    modules: [demo]
`
	write(t, root, "test-env/upgrades/catalog.yml", catalog, 0o644)

	_, err := Validate(Options{Root: root})
	if err == nil || !strings.Contains(err.Error(), "suite stray-suite declares module demo without a transition assigned to that suite") {
		t.Fatalf("stray suite module error = %v", err)
	}
}

func TestValidateRejectsWebBaselineAfterWebWasReleased(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".github/modules.json", `[{"module":"demo","repository":"anas-module-demo","platforms":["linux/amd64"]}]`, 0o644)
	write(t, root, "modules/demo/module.yml", "name: demo\nversion: 1.0.0\nrevision: 1\n", 0o644)
	write(t, root, "web/package.json", "{}\n", 0o644)
	write(t, root, "test-env/config.yml", "modules: {demo: {}}\n", 0o644)
	write(t, root, "test-env/config.targets", "demo\n", 0o644)
	write(t, root, "test-env/runner.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/seed.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/verify.sh", "#!/bin/sh\nexit 0\n", 0o755)
	write(t, root, "test-env/upgrades/catalog.yml", catalogFixture("1.0.0-r1", "0.9.0-r1"), 0o644)
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "upgrade-test@example.invalid")
	git(t, root, "config", "user.name", "Upgrade Test")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "released web")
	base := strings.TrimSpace(git(t, root, "rev-parse", "HEAD"))

	_, err := Validate(Options{Root: root, BaseRef: base, Scopes: map[string]bool{"web": true}})
	if err == nil || !strings.Contains(err.Error(), "product web has no transition from exact base") {
		t.Fatalf("released Web baseline error = %v", err)
	}
}

func TestDecodeModuleReleaseUsesTopLevelVersion(t *testing.T) {
	release, err := decodeModuleRelease([]byte("name: demo\nversion: 2.3.4\nrevision: 7\ncompatibility:\n  version: \"<9\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if release != "2.3.4-r7" {
		t.Fatalf("release = %q", release)
	}
}

func catalogFixture(current, from string) string {
	return `api_version: anas.upgrade-tests/v1
products:
  core:
    current: worktree
    transitions:
      - id: core-test
        from: v0.1.0
        from_ref: v0.1.0
        to: worktree
        suite: core-suite
  web:
    current: worktree
    baselines:
      - id: web-test
        version: worktree
        suite: web-suite
modules:
  - name: demo
    current: ` + current + `
    transitions:
      - id: demo-test
        from: ` + from + `
        to: ` + current + `
        suite: module-suite
suites:
  - id: core-suite
    kind: core
    runner: test-env/runner.sh
  - id: web-suite
    kind: web
    runner: test-env/runner.sh
  - id: module-suite
    kind: module
    runner: test-env/runner.sh
    config: test-env/config.yml
    targets: test-env/config.targets
    seed: test-env/seed.sh
    verify: test-env/verify.sh
    report: test-env/verify.sh
    modules: [demo]
`
}

func write(t *testing.T, root, name, body string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
