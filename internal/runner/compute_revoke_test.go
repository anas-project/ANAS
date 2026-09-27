package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/compose"
	"gopkg.in/yaml.v3"
)

type recordingWarnings struct {
	application.NopEventSink
	codes []string
}

func (r *recordingWarnings) Warning(event application.WarningEvent) {
	r.codes = append(r.codes, event.Code)
}

// revokeFixture is an app holding two compute leases through the real incus
// manifest, with a compose stub that records every Provider invocation.
func revokeFixture(t *testing.T, exitCode string) (*app, string, *recordingWarnings) {
	t.Helper()
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners", "ai_agent": "anas-ai-agent"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	a.artifactRoot = t.TempDir()
	providerDir := filepath.Join(a.artifactRoot, "incus")
	if err := os.MkdirAll(providerDir, 0700); err != nil {
		t.Fatal(err)
	}
	mod, err := loadModuleManifest(filepath.Join("..", "..", "modules", "incus"), "incus")
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
	log := filepath.Join(t.TempDir(), "calls.log")
	script := filepath.Join(t.TempDir(), "compose-probe")
	body := `#!/bin/sh
set -eu
printf '%s|%s|key=%s|%s\n' "$ANAS_RESOURCE_CONSUMER" "$ANAS_RESOURCE_SANDBOX" "${ANAS_RESOURCE_CLIENT_KEY:-}" "$*" >> "` + log + `"
exit ` + exitCode + `
`
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	a.compose = compose.CLI{Bin: []string{script}}
	previous := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = previous })
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	warnings := &recordingWarnings{}
	a.events = warnings
	return a, log, warnings
}

func leaseManifest(consumers ...string) *deploymentManifest {
	manifest := &deploymentManifest{}
	for _, consumer := range consumers {
		manifest.Resources = append(manifest.Resources, deploymentResource{Consumer: consumer, ID: "runners", Contract: "compute", Provider: "incus"})
	}
	return manifest
}

func TestRemovedComputeLeaseIsRevokedThroughItsProvider(t *testing.T) {
	a, log, _ := revokeFixture(t, "0")
	outcomes, err := revokeRemovedComputeLeases(a, leaseManifest("forgejo", "ai_agent"), leaseManifest("ai_agent"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes["forgejo.runners"] != resourceRevocationConfirmed {
		t.Fatalf("outcomes = %v, want only forgejo.runners confirmed", outcomes)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 1 {
		t.Fatalf("provider calls = %q, want exactly the removed lease", calls)
	}
	fields := strings.SplitN(lines[0], "|", 4)
	if fields[0] != "forgejo" || fields[1] != "anas-forgejo-runners" {
		t.Fatalf("revoke targeted %q/%q", fields[0], fields[1])
	}
	// The private key never crosses into the provider, revoke included.
	if fields[2] != "key=" {
		t.Fatal("revoke handed the consumer private key to the provider")
	}
	if !strings.HasSuffix(fields[3], "anas_incus_provision revoke --isolation vm") {
		t.Fatalf("compose args = %q, want the declared revoke operation", fields[3])
	}
}

func TestKeptLeasesAndOtherContractsAreNotRevoked(t *testing.T) {
	a, log, _ := revokeFixture(t, "0")
	current := leaseManifest("forgejo", "ai_agent")
	target := leaseManifest("forgejo", "ai_agent")
	if outcomes, err := revokeRemovedComputeLeases(a, current, target, false); err != nil || len(outcomes) != 0 {
		t.Fatalf("outcomes = %v, err = %v", outcomes, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("a lease the target still declares was revoked")
	}
	if outcomes, err := revokeRemovedComputeLeases(nil, current, nil, false); err != nil || len(outcomes) != 0 {
		t.Fatalf("first activation has nothing to revoke: %v %v", outcomes, err)
	}
}

func TestFailedRevocationBlocksActivationUnlessRiskIsAccepted(t *testing.T) {
	a, _, warnings := revokeFixture(t, "1")
	_, err := revokeRemovedComputeLeases(a, leaseManifest("forgejo", "ai_agent"), leaseManifest("ai_agent"), false)
	if err == nil || !strings.Contains(err.Error(), "forgejo.runners") || !strings.Contains(err.Error(), "--allow-risky") {
		t.Fatalf("err = %v, want a refusal naming the lease and the override", err)
	}
	outcomes, err := revokeRemovedComputeLeases(a, leaseManifest("forgejo", "ai_agent"), leaseManifest("ai_agent"), true)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes["forgejo.runners"] != resourceRevocationUnconfirmed {
		t.Fatalf("outcomes = %v, want unconfirmed rather than confirmed", outcomes)
	}
	if len(warnings.codes) != 1 || warnings.codes[0] != "compute_lease_revoke_unconfirmed" {
		t.Fatalf("warnings = %v", warnings.codes)
	}
}

func TestProviderWithoutRevokeIsRecordedAsUnsupported(t *testing.T) {
	a, log, warnings := revokeFixture(t, "0")
	mod := a.reg["incus"]
	providers := cloneContractProviders(mod.ContractProviders)
	for i := range providers {
		delete(providers[i].Operations, "revoke")
	}
	mod.ContractProviders = providers
	a.reg["incus"] = mod
	outcomes, err := revokeRemovedComputeLeases(a, leaseManifest("forgejo", "ai_agent"), leaseManifest("ai_agent"), false)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes["forgejo.runners"] != resourceRevocationUnsupported || len(warnings.codes) != 1 {
		t.Fatalf("outcomes = %v warnings = %v", outcomes, warnings.codes)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("a provider without revoke was invoked")
	}
}

func TestRetainedLeaseRecordsItsRevocationOutcome(t *testing.T) {
	base := t.TempDir()
	statePath := filepath.Join(base, "state", "resources", "forgejo.runners.yml")
	if err := writeYAMLAtomic(statePath, resourceState{
		APIVersion: resourceStateAPIVersion, Consumer: "forgejo", ResourceID: "runners",
		Contract: "compute", Provider: "incus", Interface: "incus_container", Status: "ready", DeletionPolicy: "retain",
	}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := retainRemovedResources(base, leaseManifest("forgejo"), &deploymentManifest{}, map[string]string{"forgejo.runners": resourceRevocationConfirmed}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var got resourceState
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	// The project stays (retained); the access grant is what went away.
	if got.Status != "retained" || got.Revocation != resourceRevocationConfirmed {
		t.Fatalf("state = %+v", got)
	}
}

// The contract's ensure result schema is the lease Core records and
// projects, not the Provider's stdout. Every field it requires must reach the
// consumer, or the schema documents an interface that does not exist.
func TestComputeProjectionCoversEnsureResultSchema(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "contracts", "compute", "schemas", "sandbox-result.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required []string `yaml:"required"`
	}
	if err := yaml.Unmarshal(body, &schema); err != nil {
		t.Fatal(err)
	}
	projected := map[string][]string{
		"endpoint":                       {"ENDPOINT"},
		"sandbox":                        {"SANDBOX"},
		"instance_prefix":                {"INSTANCE_PREFIX"},
		"profile":                        {"PROFILE"},
		"server_certificate_fingerprint": {"SERVER_CERT_FINGERPRINT"},
		"client_certificate_secret":      {"CLIENT_CERT", "CLIENT_KEY"},
		"lease_secret":                   {"LEASE_SECRET"},
		"quota":                          {"MAX_INSTANCES", "CPU", "MEMORY_MIB", "DISK_GIB"},
	}
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := a.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	prefix := computeResourcePrefix("forgejo", "runners")
	for _, field := range schema.Required {
		keys, ok := projected[field]
		if !ok {
			t.Errorf("sandbox-result requires %q but the consumer projection has no mapping for it", field)
			continue
		}
		for _, key := range keys {
			if a.env[prefix+key] == "" {
				t.Errorf("sandbox-result field %q is not projected as %s", field, prefix+key)
			}
		}
	}
}
