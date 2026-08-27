package runner

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestOverlapCredentialAcceptsAndGeneratesRSAKey(t *testing.T) {
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
    - id: demo.signing_key
      secret_key: DEMO_SIGNING_KEY
      type: key
      rotation_mode: overlap
      generation: {kind: rsa_private_key, length: 2048, overlap_seconds: 3600}
      lifecycle: {probe: probe-key, reconcile: reconcile-key, verify: verify-key}
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
	provider := mod.CredentialProviders[0]
	credential := deploymentCredential{
		ID: provider.ID, Generator: provider.Generator,
	}
	value, err := generateCredentialValue(credential)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(value))
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		t.Fatalf("generated key PEM block = %#v", block)
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if key.N.BitLen() != 2048 || key.PublicKey.E != 65537 {
		t.Fatalf("generated RSA key = %d bits, exponent %d", key.N.BitLen(), key.PublicKey.E)
	}
	credential.Owner = "demo"
	credential.SecretKey = provider.SecretKey
	credential.Authority = "anas"
	credential.RotationMode = "overlap"
	credential.DesiredProjection = "deployment-secret://demo.signing_key"
	credential.Lifecycle = provider.Lifecycle
	credential.Projections = []deploymentCredentialProjection{{Module: "demo", EnvKey: provider.SecretKey}}
	plan := planCredentialRotation(&deploymentManifest{
		ID: "demo-deployment", ModuleOrder: []string{"demo"},
		Modules:     map[string]deploymentModule{"demo": {Name: "demo"}},
		Credentials: []deploymentCredential{credential},
	}, []string{credential.ID}, false, false)
	if len(plan.Blockers) != 0 || !reflect.DeepEqual(plan.CredentialOrder, []string{credential.ID}) {
		t.Fatalf("overlap rotation plan = %#v", plan)
	}
	store := &secretStore{
		values: map[string]string{provider.SecretKey: value},
		metadata: map[string]secretMetadata{provider.SecretKey: {
			Owner: "demo", Kind: "generated", Provenance: "module-hook", Generation: credential.Generation,
		}},
	}
	records := credentialInventory(&deploymentManifest{
		ID: "demo-deployment", Modules: map[string]deploymentModule{"demo": {Name: "demo"}},
		Credentials: []deploymentCredential{credential},
	}, nil, store)
	if len(records) != 1 || records[0].Status != "rotatable" {
		t.Fatalf("overlap credential inventory = %#v", records)
	}
}

func TestX509RotationRejectsCorruptedPreviousBundle(t *testing.T) {
	credential := deploymentCredential{
		ID: "demo.signing_key",
		Generator: deploymentCredentialGenerator{
			Kind: "x509_rsa_bundle", Length: 2048, OverlapSeconds: 3600,
		},
	}
	previous, err := generateCredentialValue(credential)
	if err != nil {
		t.Fatal(err)
	}
	var bundle x509CredentialBundle
	if err := json.Unmarshal([]byte(previous), &bundle); err != nil {
		t.Fatal(err)
	}
	bundle.Certificate = "not a certificate"
	corrupt, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateCredentialValueWithPrevious(credential, string(corrupt)); err == nil {
		t.Fatal("corrupted previous X.509 bundle was accepted")
	}
}

func TestPrepareDeploymentCredentialsFreezesX509PublicProjections(t *testing.T) {
	bundle := `{"private_key":"private","certificate":"public-certificate"}`
	provider := CredentialProvider{
		ID: "owner.signing", SecretKey: "OWNER_SIGNING_MATERIAL", Kind: "key", RotationMode: "overlap",
		Generator: deploymentCredentialGenerator{Kind: "x509_rsa_bundle", Length: 2048, OverlapSeconds: 3600},
		Lifecycle: deploymentCredentialLifecycle{Probe: "probe", Reconcile: "reconcile", Verify: "verify"},
	}
	a := &app{
		order: []string{"owner", "consumer"},
		reg: map[string]Module{
			"owner":    {Name: "owner", CredentialProviders: []CredentialProvider{provider}},
			"consumer": {Name: "consumer", Consumes: []string{"SAML_SIGNING_CERT"}},
		},
		env: map[string]string{
			"OWNER_SIGNING_MATERIAL": bundle,
			"OWNER_SIGNING_CERT":     "public-certificate",
			"SAML_SIGNING_CERT":      "public-certificate",
		},
		envOwner: map[string]string{
			"OWNER_SIGNING_MATERIAL": "owner",
			"OWNER_SIGNING_CERT":     "owner",
			"SAML_SIGNING_CERT":      "owner",
		},
		secrets: &secretStore{
			values: map[string]string{"OWNER_SIGNING_MATERIAL": bundle},
			metadata: map[string]secretMetadata{"OWNER_SIGNING_MATERIAL": {
				Owner: "owner", Kind: "generated", Provenance: "module-hook",
			}},
		},
	}
	if err := a.prepareDeploymentCredentials(); err != nil {
		t.Fatal(err)
	}
	want := []deploymentCredentialProjection{
		{Module: "consumer", EnvKey: "SAML_SIGNING_CERT"},
		{Module: "owner", EnvKey: "OWNER_SIGNING_CERT"},
		{Module: "owner", EnvKey: "SAML_SIGNING_CERT"},
	}
	if !reflect.DeepEqual(a.credentials[0].PublicProjections, want) {
		t.Fatalf("public projections = %#v, want %#v", a.credentials[0].PublicProjections, want)
	}
}

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
		order: []string{"owner", "iam", "consumer"},
		reg: map[string]Module{
			"owner": {Name: "owner", CredentialProviders: []CredentialProvider{provider}},
			"iam":   {Name: "iam", Consumes: []string{"ANAS_IAM_CLIENT__*"}},
			"consumer": {Name: "consumer", Consumes: []string{"CONSUMER_SECRET"}, CredentialConsumers: []CredentialConsumer{{
				Credential: provider.ID, Projection: "CONSUMER_SECRET",
			}}},
		},
		env: map[string]string{
			"OWNER_SECRET": "value", "ANAS_IAM_CLIENT__OWNER__CLIENT_SECRET": "value",
		},
		envOwner: map[string]string{"ANAS_IAM_CLIENT__OWNER__CLIENT_SECRET": "owner"},
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
	wantProjections := []deploymentCredentialProjection{
		{Module: "consumer", EnvKey: "CONSUMER_SECRET"},
		{Module: "iam", EnvKey: "ANAS_IAM_CLIENT__OWNER__CLIENT_SECRET"},
		{Module: "owner", EnvKey: "ANAS_IAM_CLIENT__OWNER__CLIENT_SECRET"},
		{Module: "owner", EnvKey: "CONSUMER_SECRET"},
		{Module: "owner", EnvKey: "OWNER_SECRET"},
	}
	if !reflect.DeepEqual(a.credentials[0].Projections, wantProjections) {
		t.Fatalf("credential projections = %#v, want %#v", a.credentials[0].Projections, wantProjections)
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
