package runner

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func testServerCertB64(t *testing.T) (encoded, fingerprint string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "incus"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return base64.StdEncoding.EncodeToString(certPEM), hex.EncodeToString(sum[:])
}

func validComputeSpec() map[string]any {
	return map[string]any{
		"sandbox":         "anas-forgejo-runners",
		"instance_prefix": "anas-fj-",
		"quota": map[string]any{
			"max_instances": 8, "cpu": 4, "memory_mib": 8192, "disk_gib": 40,
		},
		"image_allowlist": []any{strings.Repeat("a", 64)},
		"credential":      map[string]any{"policy": "generated"},
		"deletion_policy": "retain",
	}
}

func TestComputeCredentialRoundTripsToAUsableKeypair(t *testing.T) {
	bundle, err := generateComputeClientCredential("forgejo", "runners")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(bundle, "\n\r") {
		t.Fatal("credential must stay a single line for the secret store")
	}
	certPEM, keyPEM, err := splitComputeCredential(bundle)
	if err != nil {
		t.Fatal(err)
	}
	// The halves must actually belong together: the provider registers the
	// certificate and the consumer authenticates with the key.
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		t.Fatalf("generated certificate and key are not a pair: %v", err)
	}
	parsed, err := x509.ParseCertificate(decodePEM(t, certPEM))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Subject.CommonName != "anas-forgejo-runners" {
		t.Errorf("common name = %q", parsed.Subject.CommonName)
	}
	if len(parsed.ExtKeyUsage) != 1 || parsed.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("certificate is not restricted to client authentication")
	}
}

func TestComputeCredentialsAreUniquePerResource(t *testing.T) {
	first, err := generateComputeClientCredential("forgejo", "runners")
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateComputeClientCredential("forgejo", "runners")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two mints produced the same keypair")
	}
}

func TestSplitComputeCredentialRejectsIncompleteBundles(t *testing.T) {
	certOnly := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}))
	for name, value := range map[string]string{
		"not base64":       "!!!!",
		"not PEM":          base64.StdEncoding.EncodeToString([]byte("nothing here")),
		"certificate only": certOnly,
		"empty":            "",
	} {
		if _, _, err := splitComputeCredential(value); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestComputeServerFingerprintMatchesTheDER(t *testing.T) {
	encoded, want := testServerCertB64(t)
	got, err := computeServerFingerprint(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
	for name, value := range map[string]string{
		"not base64": "!!!",
		"not PEM":    base64.StdEncoding.EncodeToString([]byte("plain")),
		"empty":      "",
	} {
		if _, err := computeServerFingerprint(value); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestValidateComputeSpecAcceptsAWellFormedLease(t *testing.T) {
	quota, allowlist, err := validateComputeSpec("forgejo", "runners", validComputeSpec())
	if err != nil {
		t.Fatal(err)
	}
	if quota != (computeQuota{MaxInstances: 8, CPU: 4, MemoryMiB: 8192, DiskGiB: 40}) {
		t.Fatalf("quota = %+v", quota)
	}
	if len(allowlist) != 1 {
		t.Fatalf("allowlist = %v", allowlist)
	}
}

func TestValidateComputeSpecRejectsUnsafeLeases(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"image tag instead of digest": func(s map[string]any) { s["image_allowlist"] = []any{"debian/13"} },
		"empty allowlist":             func(s map[string]any) { s["image_allowlist"] = []any{} },
		"missing allowlist":           func(s map[string]any) { delete(s, "image_allowlist") },
		"prefix without anas":         func(s map[string]any) { s["instance_prefix"] = "runner-" },
		"uppercase sandbox":           func(s map[string]any) { s["sandbox"] = "Anas-Runners" },
		"sandbox with slash":          func(s map[string]any) { s["sandbox"] = "anas/runners" },
		"missing quota":               func(s map[string]any) { delete(s, "quota") },
		"cpu above range": func(s map[string]any) {
			s["quota"].(map[string]any)["cpu"] = 65
		},
		"memory below range": func(s map[string]any) {
			s["quota"].(map[string]any)["memory_mib"] = 128
		},
		"instances below range": func(s map[string]any) {
			s["quota"].(map[string]any)["max_instances"] = 0
		},
		"fractional cpu": func(s map[string]any) {
			s["quota"].(map[string]any)["cpu"] = 2.5
		},
		"quota as string": func(s map[string]any) {
			s["quota"].(map[string]any)["disk_gib"] = "40"
		},
		"unknown deletion policy": func(s map[string]any) { s["deletion_policy"] = "purge" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := validComputeSpec()
			mutate(spec)
			if _, _, err := validateComputeSpec("forgejo", "runners", spec); err == nil {
				t.Fatalf("%s should be rejected", name)
			}
		})
	}
}

func TestImagePolicyAnyIsReservedNotHonoured(t *testing.T) {
	spec := validComputeSpec()
	spec["image_policy"] = "any"
	_, _, err := validateComputeSpec("forgejo", "runners", spec)
	if err == nil || !strings.Contains(err.Error(), "reserved and not implemented") {
		t.Fatalf("error = %v, want a refusal that says the opening is reserved", err)
	}

	// Absent and explicit "pinned" mean the same thing today.
	if _, _, err := validateComputeSpec("forgejo", "runners", validComputeSpec()); err != nil {
		t.Fatalf("an absent policy must default to pinned: %v", err)
	}
	spec = validComputeSpec()
	spec["image_policy"] = "pinned"
	if _, _, err := validateComputeSpec("forgejo", "runners", spec); err != nil {
		t.Fatalf("an explicit pinned policy must be accepted: %v", err)
	}
	spec = validComputeSpec()
	spec["image_policy"] = "loose"
	if _, _, err := validateComputeSpec("forgejo", "runners", spec); err == nil {
		t.Fatal("an unknown policy should be rejected")
	}
}

func TestValidateComputeSpecAcceptsASpecFromAllowlistString(t *testing.T) {
	// spec_from can only assign a string, so the comma-separated form is the
	// only way a module can wire a configured image into its lease.
	spec := validComputeSpec()
	spec["image_allowlist"] = strings.Repeat("a", 64) + "," + strings.Repeat("b", 64)
	_, allowlist, err := validateComputeSpec("forgejo", "runners", spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowlist) != 2 {
		t.Fatalf("allowlist = %v, want both fingerprints", allowlist)
	}

	for name, value := range map[string]string{
		"alias in the string": "images:debian/13",
		"one bad entry":       strings.Repeat("a", 64) + ",debian",
		"only separators":     ",,",
		"empty string":        "",
	} {
		spec := validComputeSpec()
		spec["image_allowlist"] = value
		if _, _, err := validateComputeSpec("forgejo", "runners", spec); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func computeApp(t *testing.T, consumers map[string]string) *app {
	t.Helper()
	serverCert, _ := testServerCertB64(t)
	reg := map[string]Module{}
	bindings := map[string]map[string]string{}
	order := []string{}
	for consumer, sandbox := range consumers {
		spec := validComputeSpec()
		spec["sandbox"] = sandbox
		// Instance prefixes live in Incus instance names, which have no
		// underscores; a module name like ai_agent has to be spelled out.
		spec["instance_prefix"] = "anas-" + strings.ReplaceAll(consumer, "_", "-") + "-"
		reg[consumer] = Module{Name: consumer, Resources: []ResourceRequirement{{
			ID: "runners", Contract: "compute", Binding: "isolation", Spec: spec,
		}}}
		bindings[consumer] = map[string]string{"compute": "incus", "compute.interface": "incus_vm"}
		order = append(order, consumer)
	}
	return &app{
		order:            order,
		reg:              reg,
		contracts:        map[string]Contract{"compute": {Name: "compute", Version: "1.0.0", Interfaces: []string{"incus_vm", "incus_container"}}},
		resolvedBindings: bindings,
		env: map[string]string{
			"INCUS_ENDPOINT":        "https://incus.example:8443",
			"INCUS_SERVER_CERT_B64": serverCert,
		},
		envOwner: map[string]string{},
		secrets:  &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
}

func TestComputeLeaseIsPublishedToItsConsumerAlone(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners"})
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := a.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	prefix := computeResourcePrefix("forgejo", "runners")
	for key, want := range map[string]string{
		"INTERFACE":       "incus_vm",
		"ENDPOINT":        "https://incus.example:8443",
		"SANDBOX":         "anas-forgejo-runners",
		"INSTANCE_PREFIX": "anas-forgejo-",
		"MAX_INSTANCES":   "8",
		"CPU":             "4",
		"MEMORY_MIB":      "8192",
		"DISK_GIB":        "40",
	} {
		if a.env[prefix+key] != want {
			t.Errorf("%s = %q, want %q", prefix+key, a.env[prefix+key], want)
		}
	}
	_, wantFingerprint := "", a.env[prefix+"SERVER_CERT_FINGERPRINT"]
	if len(wantFingerprint) != 64 {
		t.Errorf("server fingerprint = %q, want a 64-character digest", wantFingerprint)
	}
	if !a.runnerSensitive[prefix+"CLIENT_KEY"] {
		t.Error("client key is not marked sensitive")
	}
	if a.envOwner[prefix+"CLIENT_KEY"] != "forgejo" {
		t.Error("client key is not scoped to its consumer")
	}
	// The published cert and key must still be the pair the provider will trust.
	certPEM := decodeB64(t, a.env[prefix+"CLIENT_CERT"])
	keyPEM := decodeB64(t, a.env[prefix+"CLIENT_KEY"])
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("published certificate and key are not a pair: %v", err)
	}
}

func TestComputeLeaseCredentialIsStableAcrossApplies(t *testing.T) {
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners"})
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	first := a.resourceRequests[0].Credential
	// A second apply must not mint a new keypair: the old certificate is
	// already registered in the daemon's trust store.
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if a.resourceRequests[0].Credential != first {
		t.Fatal("compute resource replaced its stable client certificate")
	}
}

func TestComputeLeasesAreIsolatedBetweenConsumers(t *testing.T) {
	a := computeApp(t, map[string]string{
		"forgejo":  "anas-forgejo-runners",
		"ai_agent": "anas-agent-sandbox",
	})
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(a.resourceRequests) != 2 {
		t.Fatalf("resource requests = %d, want 2", len(a.resourceRequests))
	}
	if a.resourceRequests[0].Credential == a.resourceRequests[1].Credential {
		t.Fatal("two consumers received the same client certificate")
	}
	for _, consumer := range []string{"forgejo", "ai_agent"} {
		if err := a.publishModuleResources(consumer); err != nil {
			t.Fatal(err)
		}
	}
	forgejoPrefix := computeResourcePrefix("forgejo", "runners")
	agentPrefix := computeResourcePrefix("ai_agent", "runners")
	if a.env[forgejoPrefix+"SANDBOX"] == a.env[agentPrefix+"SANDBOX"] {
		t.Fatal("two consumers share one sandbox")
	}
	if a.scopedEnv("forgejo")[agentPrefix+"CLIENT_KEY"] != "" {
		t.Fatal("compute client key leaked to another consumer")
	}
	if a.scopedEnv("ai_agent")[forgejoPrefix+"CLIENT_KEY"] != "" {
		t.Fatal("compute client key leaked to another consumer")
	}
}

func TestComputeResourceRejectsASharedSandbox(t *testing.T) {
	a := computeApp(t, map[string]string{
		"forgejo":  "anas-shared",
		"ai_agent": "anas-shared",
	})
	err := a.materializeResourceSecrets()
	if err == nil || !strings.Contains(err.Error(), "same sandbox") {
		t.Fatalf("shared sandbox error = %v, want a refusal", err)
	}
}

func decodePEM(t *testing.T, value string) []byte {
	t.Helper()
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		t.Fatal("value is not PEM")
	}
	return block.Bytes
}

func decodeB64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// --- conditional contract dependencies and resources ---

func conditionalComputeApp(t *testing.T, switchValue string) *app {
	t.Helper()
	a := computeApp(t, map[string]string{"forgejo": "anas-forgejo-runners"})
	module := a.reg["forgejo"]
	module.EnvPrefix = "FORGEJO"
	module.Defaults = map[string]string{"FORGEJO_ACTIONS_ENABLED": "false"}
	module.Resources[0].EnabledBy = "actions_enabled"
	module.RequiresContracts = []ContractDependency{{
		Name: "compute", Version: ">=1.0.0 <2.0.0", SelectedBy: "actions_isolation",
		Interfaces: []string{"incus_vm", "incus_container"}, Default: "incus_vm",
		EnabledBy: "actions_enabled",
	}}
	a.reg["forgejo"] = module
	if switchValue != "" {
		a.env["FORGEJO_ACTIONS_ENABLED"] = switchValue
	}
	return a
}

func TestDisabledComputeResourceMintsNothing(t *testing.T) {
	a := conditionalComputeApp(t, "false")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(a.resourceRequests) != 0 {
		t.Fatalf("resource requests = %d, want none for a switched-off subsystem", len(a.resourceRequests))
	}
	// No certificate should exist either: minting one would register trust on a
	// daemon this deployment never intends to talk to.
	if len(a.secrets.values) != 0 {
		t.Fatalf("secrets = %v, want none", a.secrets.values)
	}
	if err := a.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	for key := range a.env {
		if strings.HasPrefix(key, "ANAS_COMPUTE_RESOURCE__") {
			t.Fatalf("a disabled resource published %s", key)
		}
	}
}

func TestDisabledComputeResourceFallsBackToTheDeclaredDefault(t *testing.T) {
	// Nothing written down: the manifest default decides, not an empty a.env.
	a := conditionalComputeApp(t, "")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(a.resourceRequests) != 0 {
		t.Fatalf("resource requests = %d, want none while the default is false", len(a.resourceRequests))
	}
}

func TestEnabledComputeResourceIsProvisionedAsUsual(t *testing.T) {
	a := conditionalComputeApp(t, "true")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(a.resourceRequests) != 1 {
		t.Fatalf("resource requests = %d, want 1 once the switch is on", len(a.resourceRequests))
	}
	if err := a.publishModuleResources("forgejo"); err != nil {
		t.Fatal(err)
	}
	if a.env[computeResourcePrefix("forgejo", "runners")+"SANDBOX"] != "anas-forgejo-runners" {
		t.Fatal("an enabled resource did not publish its lease")
	}
}

func TestResourceEnabledByMustMatchItsContractDependency(t *testing.T) {
	types := map[string]ParamType{
		"actions_enabled": {Kind: "bool"},
		"other_switch":    {Kind: "bool"},
		"db_type":         {Kind: "enum"},
	}
	deps := []ContractDependency{{Name: "compute", EnabledBy: "actions_enabled"}}
	resource := manifestResourceRequirement{
		ID: "runners", Contract: "compute", Spec: map[string]any{"sandbox": "x"},
	}

	// A resource that forgets the condition would drag in a provider the moment
	// it asked for one, defeating the switch on the dependency above it.
	if _, err := normalizeResourceRequirements("forgejo", []manifestResourceRequirement{resource}, deps, types); err == nil {
		t.Fatal("a resource on a conditional contract must carry the same condition")
	}
	mismatched := resource
	mismatched.EnabledBy = "other_switch"
	if _, err := normalizeResourceRequirements("forgejo", []manifestResourceRequirement{mismatched}, deps, types); err == nil {
		t.Fatal("a resource must not carry a different condition from its contract")
	}
	matched := resource
	matched.EnabledBy = "actions_enabled"
	if _, err := normalizeResourceRequirements("forgejo", []manifestResourceRequirement{matched}, deps, types); err != nil {
		t.Fatalf("a matching condition must be accepted: %v", err)
	}
}

func TestContractEnabledByMustNameABoolParameterOfTheSameModule(t *testing.T) {
	types := map[string]ParamType{"actions_enabled": {Kind: "bool"}, "db_type": {Kind: "enum"}}
	dep := manifestContractDependency{
		Name: "compute", Version: ">=1.0.0 <2.0.0", SelectedBy: "actions_isolation",
		Interfaces: []string{"incus_vm"}, Default: "incus_vm",
	}
	for name, enabledBy := range map[string]string{
		"undeclared parameter": "not_declared",
		"not a bool":           "db_type",
		"a global":             "global.base_domain",
		"an environment key":   "FORGEJO_ACTIONS_ENABLED",
	} {
		candidate := dep
		candidate.EnabledBy = enabledBy
		if _, err := normalizeContractDependencies("forgejo", []manifestContractDependency{candidate}, types); err == nil {
			t.Errorf("%s should be rejected as enabled_by", name)
		}
	}
	valid := dep
	valid.EnabledBy = "actions_enabled"
	out, err := normalizeContractDependencies("forgejo", []manifestContractDependency{valid}, types)
	if err != nil || out[0].EnabledBy != "actions_enabled" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
