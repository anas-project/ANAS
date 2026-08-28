package remotetest

// TEST_CASES: TESTAUTO-T-010, TESTAUTO-T-011

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const validTargets = `api_version: anas.remote-test-targets/v1
targets:
  dedicated-test:
    ssh_alias: anas-test
    remote_work_root: /srv/anas-e2e
    capabilities: [amd64, docker, playwright]
    max_concurrency: 2
`

func TestParseTargetProfilesIsStrictAndSecretFree(t *testing.T) {
	profiles, err := ParseTargetProfiles([]byte(validTargets))
	if err != nil {
		t.Fatal(err)
	}
	if profiles.Targets["dedicated-test"].SSHAlias != "anas-test" {
		t.Fatalf("unexpected profile: %#v", profiles)
	}
	for _, changed := range []string{
		strings.Replace(validTargets, "max_concurrency: 2", "max_concurrency: 2\n    password: secret", 1),
		strings.Replace(validTargets, "ssh_alias: anas-test", "ssh_alias: root@example.test", 1),
		strings.Replace(validTargets, "/srv/anas-e2e", "/", 1),
		strings.Replace(validTargets, "anas-test", "https://token@example.test", 1),
	} {
		if _, err := ParseTargetProfiles([]byte(changed)); err == nil {
			t.Fatalf("unsafe target profile was accepted:\n%s", changed)
		}
	}
}

func TestLoadLocalTargetProfilesRequiresIgnoredMode0600File(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "test-env", "remote"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "test-env/remote/*.local.yml\n", 0o644)
	profileName := "test-env/remote/targets.local.yml"
	write(profileName, validTargets, 0o600)
	for _, args := range [][]string{{"init"}, {"add", ".gitignore"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if _, err := LoadLocalTargetProfiles(root, profileName); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, filepath.FromSlash(profileName)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocalTargetProfiles(root, profileName); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("expected private-mode failure, got %v", err)
	}
	if err := os.Chmod(filepath.Join(root, filepath.FromSlash(profileName)), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocalTargetProfiles(root, profileName); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("expected exact-mode failure, got %v", err)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(profileName))); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "targets.yml")
	if err := os.WriteFile(external, []byte(validTargets), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, filepath.FromSlash(profileName))); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocalTargetProfiles(root, profileName); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected symlinked-profile failure, got %v", err)
	}
}

func TestResolveTargetNeedsCurrentExplicitAuthorization(t *testing.T) {
	profiles, err := ParseTargetProfiles([]byte(validTargets))
	if err != nil {
		t.Fatal(err)
	}
	registered, err := ResolveTarget(profiles, TargetRequest{RegisteredName: "dedicated-test"})
	if err != nil || registered.Authorization != "registered-local-profile" {
		t.Fatalf("registered target failed: %#v %v", registered, err)
	}
	explicitRequest := TargetRequest{
		ExplicitSSHAlias: "production-test", ExplicitAuthorizeAs: "production-test",
		ExplicitRemoteRoot: "/srv/production-e2e", ExplicitCapabilities: []string{"amd64", "docker"}, ExplicitConcurrency: 1,
	}
	explicit, err := ResolveTarget(TargetProfileFile{}, explicitRequest)
	if err != nil || explicit.Authorization != "explicit-current-invocation" || explicit.RegisteredLocal {
		t.Fatalf("explicit target failed: %#v %v", explicit, err)
	}
	explicitRequest.ExplicitAuthorizeAs = "old-target"
	if _, err := ResolveTarget(TargetProfileFile{}, explicitRequest); err == nil || !strings.Contains(err.Error(), "exactly repeat") {
		t.Fatalf("expected exact current authorization failure, got %v", err)
	}
	if _, err := ResolveTarget(profiles, TargetRequest{}); err == nil {
		t.Fatal("missing authorization source was accepted")
	}
}

func TestSSHConfigurationAndCommandAlwaysUseStrictKnownHosts(t *testing.T) {
	configuration, err := ParseSSHConfiguration("anas-test", []byte(`hostname test.example
user tester
port 2222
stricthostkeychecking true
userknownhostsfile /tmp/known_hosts
`))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.HostName != "test.example" || configuration.Port != 2222 {
		t.Fatalf("unexpected SSH configuration: %#v", configuration)
	}
	if _, err := ParseSSHConfiguration("anas-test", []byte(`hostname test.example
user tester
stricthostkeychecking false
userknownhostsfile /dev/null
`)); err == nil {
		t.Fatal("disabled host-key checking was accepted")
	}
	if _, err := ParseSSHConfiguration("anas-test", []byte(`hostname test.example
user root
stricthostkeychecking true
userknownhostsfile /tmp/known_hosts
`)); err == nil {
		t.Fatal("root SSH login was accepted")
	}
	target := ResolvedTarget{SSHAlias: "anas-test"}
	requirements := PreflightRequirements{
		Architecture: "amd64", MinDiskBytes: 1, MinMemoryBytes: 1,
		Ports: []int{}, DNSNames: []string{}, Routes: []string{}, Capabilities: []string{"docker"},
	}
	command, err := BuildRemotePreflightCommand(target, "rt-20260829t120000z-aaaaaaaaaaaa", requirements)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	for _, want := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", RemoteHelperPath, "preflight"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("SSH command lacks %q: %s", want, joined)
		}
	}
	for _, forbidden := range []string{"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", " -i ", "root@"} {
		if strings.Contains(" "+joined+" ", forbidden) {
			t.Fatalf("SSH command contains forbidden %q: %s", forbidden, joined)
		}
	}
	requirements.DNSNames = []string{"safe.test;id"}
	if _, err := BuildRemotePreflightCommand(target, "rt-20260829t120000z-aaaaaaaaaaaa", requirements); err == nil {
		t.Fatal("shell metacharacters in remote preflight arguments were accepted")
	}
}

func TestVerifyKnownHostBindingUsesConfiguredFile(t *testing.T) {
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte("[test.example]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration := SSHConfiguration{HostName: "test.example", Port: 2222, KnownHostsFiles: []string{knownHosts}}
	if err := VerifyKnownHostBinding(context.Background(), configuration); err != nil {
		t.Fatal(err)
	}
}
