package remotetest

// TEST_CASES: TESTAUTO-T-012, TESTAUTO-T-014

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testRunID = "rt-20260829t120000z-aaaaaaaaaaaa"

func TestAllocateRunScopesEveryMutableResource(t *testing.T) {
	first, err := AllocateRun(testRunID, "/srv/anas-e2e", 0, 2, 20000, 128)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AllocateRun("rt-20260829t120001z-bbbbbbbbbbbb", "/srv/anas-e2e", 1, 2, 20000, 128)
	if err != nil {
		t.Fatal(err)
	}
	for label, values := range map[string][2]string{
		"workspace":         {first.Workspace, second.Workspace},
		"report":            {first.ReportDirectory, second.ReportDirectory},
		"container prefix":  {first.ContainerPrefix, second.ContainerPrefix},
		"network prefix":    {first.NetworkPrefix, second.NetworkPrefix},
		"netns":             {first.NetworkNamespace, second.NetworkNamespace},
		"docker socket":     {first.DockerSocket, second.DockerSocket},
		"docker root":       {first.DockerDataRoot, second.DockerDataRoot},
		"containerd socket": {first.ContainerdSocket, second.ContainerdSocket},
		"containerd root":   {first.ContainerdRoot, second.ContainerdRoot},
	} {
		if values[0] == values[1] {
			t.Errorf("%s is shared across runs: %q", label, values[0])
		}
	}
	if first.PortEnd >= second.PortStart {
		t.Fatalf("port blocks overlap: %#v %#v", first, second)
	}
	for label, value := range map[string]string{
		"container prefix": first.ContainerPrefix, "network prefix": first.NetworkPrefix,
		"network namespace": first.NetworkNamespace, "containerd namespace": first.ContainerdNamespace,
	} {
		if !strings.Contains(value, strings.ReplaceAll(testRunID, "-", "_")) && !strings.Contains(value, testRunID) {
			t.Fatalf("%s does not retain the full run id: %s", label, value)
		}
	}
	if _, err := NewRunID(time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePreflightChecksCapacityNetworkIsolationAndLeastPrivilege(t *testing.T) {
	target := ResolvedTarget{
		RemoteWorkRoot: "/srv/anas-e2e", Capabilities: []string{"amd64", "docker", "playwright"}, MaxConcurrency: 2,
		SSHUser: "anas-test",
	}
	requirements := PreflightRequirements{
		Architecture: "amd64", MinDiskBytes: 10_000, MinMemoryBytes: 5_000,
		Ports: []int{22000}, DNSNames: []string{"auth.test"}, Routes: []string{"10.0.0.0/24"},
		Capabilities: []string{"docker", "playwright"},
	}
	facts := validHostFacts()
	allocation, err := ValidatePreflight(target, testRunID, requirements, facts)
	if err != nil {
		t.Fatal(err)
	}
	if allocation.Slot != 1 || allocation.PortStart != 20128 {
		t.Fatalf("unexpected allocation: %#v", allocation)
	}
	mismatchedAccount := facts
	mismatchedAccount.Account.User = "different-user"
	if _, err := ValidatePreflight(target, testRunID, requirements, mismatchedAccount); err == nil || !strings.Contains(err.Error(), "does not match SSH login user") {
		t.Fatalf("expected SSH account mismatch failure, got %v", err)
	}

	facts.Architecture = "arm64"
	facts.DiskAvailableBytes = 1
	facts.MemoryAvailableBytes = 1
	facts.OccupiedPorts = []int{22000, 20128}
	facts.UnresolvedDNSNames = []string{"auth.test"}
	facts.UnavailableRoutes = []string{"10.0.0.0/24"}
	facts.Capabilities = []string{"docker"}
	facts.ActiveRuns = 2
	facts.DockerIsolationGuard = "not-run"
	facts.ComposeWorkspaceGuard = "disabled"
	facts.Account.UID = 0
	facts.Account.Groups = []string{"docker"}
	facts.Account.ArbitraryPasswordlessSudo = true
	facts.Account.HelperOwnerUID = 1000
	facts.Account.HelperMode = 0o777
	_, err = ValidatePreflight(target, testRunID, requirements, facts)
	if err == nil {
		t.Fatal("unsafe preflight facts were accepted")
	}
	message := err.Error()
	for _, want := range []string{
		"architecture", "available disk", "available memory", "already occupied", "DNS name", "route",
		"playwright", "quota is exhausted", "isolated Docker", "Compose workspace-owner",
		"non-root", "docker", "arbitrary passwordless sudo", "not owned by root", "writable by group",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("preflight failure lacks %q:\n%s", want, message)
		}
	}
}

func TestDecodeHostFactsRejectsUnknownOrMultipleValues(t *testing.T) {
	facts := validHostFacts()
	data, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHostFacts(data); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeHostFacts(append(data, []byte(`{"unexpected":true}`)...)); err == nil {
		t.Fatal("multiple host-fact values were accepted")
	}
	withUnknown := strings.Replace(string(data), `"run_id":`, `"unknown":true,"run_id":`, 1)
	if _, err := DecodeHostFacts([]byte(withUnknown)); err == nil {
		t.Fatal("unknown host-fact field was accepted")
	}
}

func TestHelperConfigIsStrictAndPinsIsolationPolicy(t *testing.T) {
	config := `api_version: anas.remote-test-helper/v1
remote_work_root: /srv/anas-e2e
capabilities: [amd64, docker]
max_concurrency: 2
port_base: 20000
port_block_size: 128
test_user: anas-test
docker_host: unix:///run/anas-e2e/docker.sock
compose_workspace_guard: true
`
	if _, err := ParseHelperConfig([]byte(config)); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		strings.Replace(config, "compose_workspace_guard: true", "compose_workspace_guard: false", 1),
		strings.Replace(config, "unix:///run/anas-e2e/docker.sock", "unix:///var/run/docker.sock", 1),
		strings.Replace(config, "unix:///run/anas-e2e/docker.sock", "unix:///run/anas-e2e/../docker.sock", 1),
		strings.Replace(config, "port_block_size: 128", "port_block_size: 9223372036854775807", 1),
		config + "shell: /bin/sh\n",
	} {
		if _, err := ParseHelperConfig([]byte(changed)); err == nil {
			t.Fatalf("unsafe helper configuration was accepted:\n%s", changed)
		}
	}
}

func validHostFacts() HostFacts {
	return HostFacts{
		APIVersion: HostFactsAPI, RunID: testRunID, RemoteWorkRoot: "/srv/anas-e2e",
		Architecture: "amd64", DiskAvailableBytes: 20_000, MemoryAvailableBytes: 10_000,
		Capabilities: []string{"amd64", "docker", "playwright"}, ActiveRuns: 1, MaxConcurrency: 2,
		AvailableSlot: 1, PortBase: 20000, PortBlockSize: 128,
		DockerIsolationGuard:  "server-require-isolated-docker.sh:passed",
		ComposeWorkspaceGuard: "runner-compose-owner:enabled",
		Account: AccountFacts{
			User: "anas-test", UID: 1001, Groups: []string{"anas-test"}, HelperPath: RemoteHelperPath,
			HelperOwnerUID: 0, HelperMode: 0o755,
		},
	}
}
