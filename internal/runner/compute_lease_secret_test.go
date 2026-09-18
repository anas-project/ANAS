package runner

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

func TestComputeLeaseSecretStableIndependentAndPrivate(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo", "ai_agent": "anas-agent"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	a.secrets.path = filepath.Join(a.base, "secrets.yml")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	previous := map[string]ResourceRequest{}
	seen := map[string]bool{}
	for _, req := range a.resourceRequests {
		if err := validateComputeLeaseSecret(req.LeaseSecret); err != nil {
			t.Fatal(err)
		}
		if seen[req.LeaseSecret] || req.LeaseSecret == req.Credential || req.LeaseSecretKey == req.SecretKey {
			t.Fatal("lease secret is not independent")
		}
		seen[req.LeaseSecret] = true
		previous[req.Consumer] = req
		if err := a.publishModuleResources(req.Consumer); err != nil {
			t.Fatal(err)
		}
		key := computeLeaseSecretKey(req.Consumer, req.ID)
		if a.env[key] != req.LeaseSecret || !a.runnerSensitive[key] || a.envOwner[key] != req.Consumer {
			t.Fatal("missing private sensitive projection")
		}
		if a.globalEnv()[key] != "" {
			t.Fatal("lease secret leaked into global environment")
		}
	}
	for _, req := range a.resourceRequests {
		other := "forgejo"
		if req.Consumer == other {
			other = "ai_agent"
		}
		if a.scopedEnv(other)[req.LeaseSecretKey] != "" || a.scopedEnv("incus")[req.LeaseSecretKey] != "" {
			t.Fatal("lease secret crossed consumer boundary")
		}
	}
	if err := a.prepareDeploymentCredentials(); err != nil {
		t.Fatal(err)
	}
	if len(a.credentials) != 0 {
		t.Fatal("naming secret entered credential rotation inventory")
	}
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	// Simulate a new apply process loading the stable store from disk.
	var err error
	a.secrets, err = loadSecretStore(a.base)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	for _, req := range a.resourceRequests {
		if req.LeaseSecret != previous[req.Consumer].LeaseSecret || req.Credential != previous[req.Consumer].Credential {
			t.Fatal("apply replaced stable material")
		}
	}
	// Changing just the certificate bundle must never rotate the naming key.
	first := a.resourceRequests[0]
	replacement, err := generateComputeClientCredential(first.Consumer, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.secrets.SetWithMetadata(first.SecretKey, replacement, a.secrets.metadata[first.SecretKey])
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	for _, req := range a.resourceRequests {
		if req.LeaseSecret != previous[req.Consumer].LeaseSecret {
			t.Fatal("certificate replacement changed naming key")
		}
	}
}

func TestComputeLeaseSecretRejectsCorruptionWithoutRemintingOrEcho(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cases := map[string]string{"empty": "", "invalid": "private-invalid-base64", "short": base64.StdEncoding.EncodeToString(make([]byte, 31)), "long": base64.StdEncoding.EncodeToString(make([]byte, 33)), "newline": valid + "\n", "padding-bits": valid[:42] + "B="}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			a := computeApp(t, map[string]string{"forgejo": "anas-test"})
			key := computeLeaseSecretKey("forgejo", "runners")
			a.secrets.values[key] = value
			a.secrets.metadata[key] = computeLeaseSecretMetadata("forgejo")
			err := a.materializeResourceSecrets()
			if err == nil || (value != "" && strings.Contains(err.Error(), value)) {
				t.Fatal("corrupt secret was accepted or exposed")
			}
			if a.secrets.values[key] != value || len(a.secrets.values) != 1 {
				t.Fatal("failure mutated secrets or minted a certificate")
			}
		})
	}
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	key := computeLeaseSecretKey("forgejo", "runners")
	a.secrets.Set(key, valid)
	if err := a.materializeResourceSecrets(); err == nil {
		t.Fatal("accepted foreign lifecycle metadata")
	}
}

func frozenComputeLeaseFixture(t *testing.T) (*app, *deploymentManifest, string) {
	t.Helper()
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	a.secrets.path = filepath.Join(a.base, "secrets.yml")
	a.cfg = &config.File{}
	id := "20260911T010203Z-aaaaaaaa"
	cfg := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfg, []byte("modules: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(a.base, "staging", id, "modules", "forgejo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	manifest, err := buildDeploymentManifest(a, id, cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.base, "deployments", id, "deployment.yml")
	if err := writeYAMLAtomic(path, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	return a, manifest, path
}

func TestComputeLeaseSecretFrozenReferenceAndLegacyUpgrade(t *testing.T) {
	a, manifest, path := frozenComputeLeaseFixture(t)
	req := a.resourceRequests[0]
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), req.LeaseSecret) || !strings.Contains(string(body), "lease_secret: "+req.LeaseSecretKey) {
		t.Fatal("deployment did not persist a reference only")
	}
	if err := a.saveResourceReady(req, a.env); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(a.base, "state", "resources", "forgejo.runners.yml")
	body, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state resourceState
	if err := yaml.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), req.LeaseSecret) || state.Actual.LeaseSecret != req.LeaseSecretKey {
		t.Fatal("resource state did not persist a reference only")
	}
	restored, _, _, err := loadDeploymentApp(a.base, manifest.ID, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	if restored.resourceRequests[0].LeaseSecret != req.LeaseSecret || restored.secrets.dirty {
		t.Fatal("replay changed the naming key")
	}
	// A dangling reference must fail at load AND on fresh apply, not make new URLs.
	delete(a.secrets.values, req.LeaseSecretKey)
	a.secrets.dirty = true
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadDeploymentApp(a.base, manifest.ID, compose.CLI{}); err == nil {
		t.Fatal("replay accepted missing entry")
	}
	if err := a.materializeResourceSecrets(); err == nil {
		t.Fatal("apply silently replaced lost naming key")
	}
	// Model an actual pre-secret deployment: no reference in either artifact.
	manifest.Resources[0].LeaseSecretKey = ""
	if err := writeYAMLAtomic(path, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	state.Actual.LeaseSecret = ""
	if err := writeYAMLAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	legacy, _, _, err := loadDeploymentApp(a.base, manifest.ID, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.resourceRequests[0].LeaseSecret != "" || legacy.secrets.dirty {
		t.Fatal("legacy replay generated naming material")
	}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if a.resourceRequests[0].Credential != req.Credential || a.resourceRequests[0].LeaseSecret == "" {
		t.Fatal("legacy apply did not preserve certificate and add naming key")
	}
	manifest.Resources[0].LeaseSecretKey = computeLeaseSecretKey("ai_agent", "runners")
	if err := writeYAMLAtomic(path, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := loadDeploymentApp(a.base, manifest.ID, compose.CLI{}); err == nil {
		t.Fatal("cross-lease reference was accepted")
	}
}

func TestComputeLeaseSecretCannotBeClaimedByCredentialRotation(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := a.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	req := a.resourceRequests[0]
	mod := a.reg["forgejo"]
	mod.CredentialProviders = []CredentialProvider{{ID: "forgejo.naming", SecretKey: req.LeaseSecretKey}}
	a.reg["forgejo"] = mod
	if err := a.prepareDeploymentCredentials(); err == nil {
		t.Fatal("credential declaration claimed naming key")
	}
	if err := a.secrets.Merge("forgejo", map[string]string{req.LeaseSecretKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}); err == nil {
		t.Fatal("hook overwrote naming key")
	}
}

func TestComputeLeaseSecretSurvivesRealCopyBackupRestore(t *testing.T) {
	// Exercises real metadata/data copying and restore on disk; it makes no
	// claims about Incus, Btrfs, URL publication, or a running application.
	workspace, id := newSnapshotWorkspace(t)
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = stateDir(workspace)
	a.secrets.path = filepath.Join(a.base, "secrets.yml")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	req := a.resourceRequests[0]
	artifact := deploymentArtifactDir(a.base, id)
	manifest, err := loadDeploymentManifest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Resources = []deploymentResource{{Consumer: req.Consumer, ID: req.ID, Contract: req.Contract, Provider: req.Provider, Interface: req.Interface, Spec: req.Spec, ComputeImages: req.ComputeImages.Clone(), CredentialSecretKey: req.SecretKey, LeaseSecretKey: req.LeaseSecretKey}}
	if err := writeYAMLAtomic(filepath.Join(artifact, "deployment.yml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	outcome, err := createBackup(workspace, &backupPlan{Mode: backupModeCopy, Dest: dest}, backupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	backup, all, err := selectBackup(dest, outcome.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := ensureRuntimeLayout(stateDir(target)); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreBackup(target, dest, backup, all, false); err != nil {
		t.Fatal(err)
	}
	restored, _, _, err := loadDeploymentApp(stateDir(target), id, compose.CLI{})
	if err != nil {
		t.Fatal(err)
	}
	after := restored.resourceRequests[0]
	derive := func(value string) string {
		decoded, _ := base64.StdEncoding.DecodeString(value)
		mac := hmac.New(sha256.New, decoded)
		mac.Write([]byte("workload-42"))
		return hex.EncodeToString(mac.Sum(nil))[:10]
	}
	if after.LeaseSecret != req.LeaseSecret || after.Credential != req.Credential || derive(after.LeaseSecret) != derive(req.LeaseSecret) {
		t.Fatal("backup restore changed key material or derived naming result")
	}
	info, err := os.Stat(filepath.Join(stateDir(target), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("restored Secret Store is not private")
	}
}

func TestComputeLeaseSecretRedactsConfigListAndValidationHook(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-test"})
	a.base = filepath.Join(t.TempDir(), ".anas")
	a.secrets.path = filepath.Join(a.base, "secrets.yml")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := a.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	req := a.resourceRequests[0]
	if err := a.secrets.Save(); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(cfg, []byte("modules:\n  forgejo:\n    config:\n      alias: "+req.LeaseSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mod := a.reg["forgejo"]
	mod.EnvPrefix = "FORGEJO"
	mod.Parameters = []string{"alias"}
	mod.Changes = map[string]ChangePolicy{"alias": {Effect: "container_recreate"}}
	requestFile := filepath.Join(t.TempDir(), "request.json")
	mod.SourceDir = t.TempDir()
	mod.Hook = HookConfig{Command: moduleValidationHookHelperCommand("capture", requestFile), Phases: []string{"validate"}}
	a.reg["forgejo"] = mod
	for _, jsonMode := range []bool{true, false} {
		output, err := captureRunnerStdout(t, func() error { return reportConfigList(cfg, a.reg, "forgejo", jsonMode, a.base) })
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output, req.LeaseSecret) || !strings.Contains(output, "forgejo.alias") {
			t.Fatal("config list exposed the naming key or omitted the tested alias")
		}
	}
	a.env["FORGEJO_ALIAS"] = req.LeaseSecret
	a.setEnvOwner("FORGEJO_ALIAS", "forgejo")
	if err := a.validateModules(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), req.LeaseSecret) || strings.Contains(string(body), req.LeaseSecretKey) {
		t.Fatal("read-only validation hook received naming secret")
	}
}

func TestComputeLeaseSecretReachesConsumerComposeServicesOnly(t *testing.T) {
	for module, resource := range map[string]string{"forgejo": "runners", "ai_agent": "work_instances"} {
		body, err := os.ReadFile(filepath.Join("../..", "modules", module, "docker-compose.yml"))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]struct{ Environment map[string]any }
		}
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		key := computeLeaseSecretKey(module, resource)
		count := 0
		for name, service := range doc.Services {
			if _, ok := service.Environment[key]; !ok {
				continue
			}
			count++
			if service.Environment[key] != "${"+key+":-}" || service.Environment[computeResourcePrefix(module, resource)+"CLIENT_KEY"] == nil {
				t.Fatalf("%s received secret outside compute lease", name)
			}
		}
		expected := 1
		if module == "forgejo" {
			expected = 2
		}
		if count != expected {
			t.Fatalf("%s compute consumers missing lease secret projection", module)
		}
	}
}
