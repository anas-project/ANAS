package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestModuleCredentialDeclarationsRequireExplicitLifecyclePhases(t *testing.T) {
	dir := t.TempDir()
	manifest := `api_version: anas.module/v1
kind: Module
name: demo
version: 1.0.0
revision: 1
abi:
  supports: [anas.module-hook/v1]
status: release
runtime:
  type: builtin
credentials:
  provides:
    - id: demo.secret
      secret_key: DEMO_SECRET
      type: shared_secret
      rotation_mode: reconcile
      generation: {kind: hex, length: 16}
      lifecycle:
        probe: probe-demo
        reconcile: reconcile-demo
        verify: verify-demo
logic:
  hook:
    command: [sh, -c, "exit 0"]
    phases: [credential_probe, credential_reconcile, credential_verify]
`
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	mod, err := loadModuleManifest(dir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.CredentialProviders) != 1 || mod.CredentialProviders[0].ID != "demo.secret" {
		t.Fatalf("providers = %#v", mod.CredentialProviders)
	}

	legacy := strings.Replace(manifest, "    phases: [credential_probe, credential_reconcile, credential_verify]\n", "", 1)
	if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadModuleManifest(dir, "demo"); err == nil || !strings.Contains(err.Error(), "explicit credential_probe") {
		t.Fatalf("legacy credential lifecycle error = %v", err)
	}
}

func TestCredentialConsumerAddsProviderActivationEdge(t *testing.T) {
	a := &app{
		cfg: &config.File{Modules: config.NewModuleSelection("consumer")},
		reg: map[string]Module{
			"owner": {Name: "owner", CredentialProviders: []CredentialProvider{{ID: "owner.secret"}}},
			"consumer": {Name: "consumer", CredentialConsumers: []CredentialConsumer{{
				Credential: "owner.secret", Projection: "CONSUMER_SECRET",
			}}},
		},
		env: map[string]string{}, registryOnlyResolution: true,
	}
	order, err := a.resolveOrder([]string{"consumer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "owner" || order[1] != "consumer" {
		t.Fatalf("credential activation order = %v", order)
	}
	if !contains(a.deps["consumer"], "owner") {
		t.Fatalf("resolved dependencies = %#v", a.deps)
	}
}

func TestRegistryCredentialDeclarationsRejectMissingProviderAndKeyCollision(t *testing.T) {
	provider := CredentialProvider{ID: "owner.secret", SecretKey: "SHARED_SECRET"}
	if err := validateRegistryCredentials(map[string]Module{
		"owner":    {Name: "owner", CredentialProviders: []CredentialProvider{provider}},
		"consumer": {Name: "consumer", CredentialConsumers: []CredentialConsumer{{Credential: "missing.secret", Projection: "CONSUMER_SECRET"}}},
	}); err == nil || !strings.Contains(err.Error(), "no installed Module provides") {
		t.Fatalf("missing provider error = %v", err)
	}
	if err := validateRegistryCredentials(map[string]Module{
		"one": {Name: "one", CredentialProviders: []CredentialProvider{{ID: "one.secret", SecretKey: "SHARED_SECRET"}}},
		"two": {Name: "two", CredentialProviders: []CredentialProvider{{ID: "two.secret", SecretKey: "SHARED_SECRET"}}},
	}); err == nil || !strings.Contains(err.Error(), "both own Secret key") {
		t.Fatalf("Secret key collision error = %v", err)
	}
}

func TestPrepareDeploymentCredentialsFreezesAuthorityGenerationAndProjection(t *testing.T) {
	provider := CredentialProvider{
		ID: "owner.secret", SecretKey: "OWNER_SECRET", Kind: "shared_secret", RotationMode: "reconcile",
		Generator: deploymentCredentialGenerator{Kind: "hex", Length: 16},
		Lifecycle: deploymentCredentialLifecycle{Probe: "probe", Reconcile: "reconcile", Verify: "verify"},
	}
	a := &app{
		order: []string{"owner", "consumer"},
		reg: map[string]Module{
			"owner": {Name: "owner", CredentialProviders: []CredentialProvider{provider}},
			"consumer": {Name: "consumer", CredentialConsumers: []CredentialConsumer{{
				Credential: provider.ID, Projection: "CONSUMER_SECRET",
			}}},
		},
		env: map[string]string{"OWNER_SECRET": "value"}, envOwner: map[string]string{},
		secrets: &secretStore{
			values: map[string]string{"OWNER_SECRET": "value"},
			metadata: map[string]secretMetadata{"OWNER_SECRET": {
				Owner: "owner", Kind: "generated", Provenance: "module-hook",
			}},
		},
	}
	if err := a.projectProvidedCredentials("owner"); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareDeploymentCredentials(); err != nil {
		t.Fatal(err)
	}
	if a.env["CONSUMER_SECRET"] != "value" {
		t.Fatal("consumer projection was not published")
	}
	if len(a.credentials) != 1 || a.credentials[0].Authority != "anas" || a.credentials[0].Generation != 1 {
		t.Fatalf("credentials = %#v", a.credentials)
	}
	if a.secrets.metadata["OWNER_SECRET"].Generation != 1 || !a.secrets.dirty {
		t.Fatalf("metadata = %#v, dirty = %t", a.secrets.metadata["OWNER_SECRET"], a.secrets.dirty)
	}
}

func TestCredentialReadyBarrierReconcilesMismatchThenVerifies(t *testing.T) {
	dir := t.TempDir()
	script := `payload=$(cat)
case "$payload" in *ANAS_CREDENTIAL_DESIRED*) ;; *) exit 8;; esac
case "$payload" in
  *credential_probe*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"mismatch"}}' ;;
  *credential_reconcile*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"reconciled","changed":true}}' ;;
  *credential_verify*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"match"}}' ;;
  *) exit 9 ;;
esac`
	mod := Module{Name: "demo", SourceDir: dir, Hook: HookConfig{
		Command: []string{"sh", "-c", script},
		Phases:  []string{"credential_probe", "credential_reconcile", "credential_verify"},
	}}
	a := hookBoundaryApp(t, mod, nil, nil)
	a.credentials = []deploymentCredential{{
		ID: "demo.secret", SecretKey: "DEMO_SECRET", Owner: "demo", Authority: "anas",
		RotationMode: "reconcile", Generation: 2,
		Lifecycle: deploymentCredentialLifecycle{Probe: "probe", Reconcile: "reconcile", Verify: "verify"},
	}}
	if err := a.coordinateModuleCredentials(mod, dir, map[string]string{"DEMO_SECRET": "desired"}); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialReadyBarrierWaitsForAuthorityStartup(t *testing.T) {
	dir := t.TempDir()
	counter := filepath.Join(dir, "probe-count")
	script := `payload=$(cat)
case "$payload" in
  *credential_probe*)
    count=0
    test ! -f ` + counter + ` || count=$(cat ` + counter + `)
    count=$((count + 1))
    printf '%s' "$count" > ` + counter + `
    if test "$count" -lt 3; then
      printf '%s' '{"credential":{"credential_id":"demo.secret","status":"unavailable"}}'
    else
      printf '%s' '{"credential":{"credential_id":"demo.secret","status":"match"}}'
    fi ;;
  *credential_verify*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"match"}}' ;;
  *credential_reconcile*) exit 21 ;;
  *) exit 22 ;;
esac`
	mod := Module{Name: "demo", SourceDir: dir, Hook: HookConfig{
		Command: []string{"sh", "-c", script},
		Phases:  []string{"credential_probe", "credential_reconcile", "credential_verify"},
	}}
	a := hookBoundaryApp(t, mod, nil, nil)
	a.credentials = []deploymentCredential{{
		ID: "demo.secret", SecretKey: "DEMO_SECRET", Owner: "demo", Authority: "anas",
		RotationMode: "reconcile", Generation: 2,
		Lifecycle: deploymentCredentialLifecycle{Probe: "probe", Reconcile: "reconcile", Verify: "verify"},
	}}
	originalPause := credentialBarrierRetryPause
	credentialBarrierRetryPause = func() {}
	t.Cleanup(func() { credentialBarrierRetryPause = originalPause })
	if err := a.coordinateModuleCredentials(mod, dir, map[string]string{"DEMO_SECRET": "desired"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "3" {
		t.Fatalf("probe count = %s", body)
	}
}

func TestCredentialReadyBarrierNeverReconcilesExternalAuthority(t *testing.T) {
	dir := t.TempDir()
	script := `payload=$(cat)
case "$payload" in
  *credential_probe*) printf '%s' '{"credential":{"credential_id":"demo.secret","status":"mismatch"}}' ;;
  *) exit 19 ;;
esac`
	mod := Module{Name: "demo", SourceDir: dir, Hook: HookConfig{
		Command: []string{"sh", "-c", script},
		Phases:  []string{"credential_probe", "credential_reconcile", "credential_verify"},
	}}
	a := hookBoundaryApp(t, mod, nil, nil)
	a.credentials = []deploymentCredential{{
		ID: "demo.secret", SecretKey: "DEMO_SECRET", Owner: "demo", Authority: "external",
		RotationMode: "reconcile", Generation: 2,
		Lifecycle: deploymentCredentialLifecycle{Probe: "probe", Reconcile: "reconcile", Verify: "verify"},
	}}
	err := a.coordinateModuleCredentials(mod, dir, map[string]string{"DEMO_SECRET": "desired"})
	if err == nil || !strings.Contains(err.Error(), "manual action is required") {
		t.Fatalf("external mismatch error = %v", err)
	}
}
