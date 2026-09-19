package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func materials(t *testing.T) (certB64, keyB64 string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "incus"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return base64.StdEncoding.EncodeToString(certPEM), base64.StdEncoding.EncodeToString(keyPEM)
}

func validEnv(t *testing.T) map[string]string {
	t.Helper()
	certB64, keyB64 := materials(t)
	return map[string]string{
		"NETWORK_PREFIX":               "anas_",
		"INCUS_ENDPOINT":               "https://incus.example:8443",
		"INCUS_SERVER_CERTIFICATE_B64": certB64,
		"INCUS_ADMIN_CERTIFICATE_B64":  certB64,
		"INCUS_ADMIN_KEY_B64":          keyB64,
		"INCUS_IMAGE_ARCHITECTURE":     "amd64",
	}
}

func calculateForTest(module string, env map[string]string, secrets map[string]string) error {
	return calculate(module, env, &secretStore{values: secrets})
}

func TestCalculateDerivesNetworkName(t *testing.T) {
	env := validEnv(t)
	if err := calculateForTest("incus", env, map[string]string{}); err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if got := env["INCUS_NETWORK_NAME"]; got != "anas_incus" {
		t.Fatalf("INCUS_NETWORK_NAME = %q, want anas_incus", got)
	}
}

func TestCalculateRefusesIncompleteCredentials(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"no endpoint":        func(e map[string]string) { e["INCUS_ENDPOINT"] = "" },
		"plaintext endpoint": func(e map[string]string) { e["INCUS_ENDPOINT"] = "http://incus.example:8443" },
		"no server cert":     func(e map[string]string) { e["INCUS_SERVER_CERTIFICATE_B64"] = "" },
		"no admin cert":      func(e map[string]string) { e["INCUS_ADMIN_CERTIFICATE_B64"] = "" },
		"no admin key":       func(e map[string]string) { e["INCUS_ADMIN_KEY_B64"] = "" },
		"cert is not base64": func(e map[string]string) { e["INCUS_ADMIN_CERTIFICATE_B64"] = "!!!" },
		"cert is not PEM": func(e map[string]string) {
			e["INCUS_ADMIN_CERTIFICATE_B64"] = base64.StdEncoding.EncodeToString([]byte("nope"))
		},
		"key slot holds cert": func(e map[string]string) { e["INCUS_ADMIN_KEY_B64"] = e["INCUS_ADMIN_CERTIFICATE_B64"] },
	} {
		t.Run(name, func(t *testing.T) {
			env := validEnv(t)
			mutate(env)
			if err := calculateForTest("incus", env, map[string]string{}); err == nil {
				t.Fatalf("%s should be refused", name)
			}
		})
	}
}

func TestCalculateProjectsOnlyCanonicalCertificateInputs(t *testing.T) {
	input := validEnv(t)
	input["INCUS_SERVER_CERT_B64"] = "stale-server-alias"
	input["INCUS_ADMIN_CERT_B64"] = "stale-admin-alias"
	response, err := handle(hookRequest{Module: "incus", Phase: "calculate", Env: input})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"SERVER", "ADMIN"} {
		if response.Env["INCUS_"+role+"_CERT_B64"] != input["INCUS_"+role+"_CERTIFICATE_B64"] {
			t.Fatal("canonical config did not reach the provider projection")
		}
	}
	delete(input, "INCUS_SERVER_CERTIFICATE_B64")
	if _, err := handle(hookRequest{Module: "incus", Phase: "calculate", Env: input}); err == nil {
		t.Fatal("stale raw-env alias replaced a missing canonical certificate")
	}
}

func TestCalculateRejectsSensitiveEndpointDecorationsWithoutEcho(t *testing.T) {
	for _, endpoint := range []string{"https://user:private-marker@example.test", "https://example.test/?private-marker", "https://example.test/private-marker", "https://example.test#private-marker"} {
		env := validEnv(t)
		env["INCUS_ENDPOINT"] = endpoint
		err := calculateForTest("incus", env, map[string]string{})
		if err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatalf("unsafe endpoint validation: %v", err)
		}
	}
}

func TestCalculateNeverEchoesCredentials(t *testing.T) {
	env := validEnv(t)
	secret := env["INCUS_ADMIN_KEY_B64"]
	env["INCUS_ADMIN_KEY_B64"] = secret + "!!not-base64"
	err := calculateForTest("incus", env, map[string]string{})
	if err == nil {
		t.Fatal("expected a validation failure")
	}
	if strings.Contains(err.Error(), secret[:32]) {
		t.Fatalf("hook error echoed key material: %v", err)
	}
}

func TestCalculateIgnoresOtherModules(t *testing.T) {
	env := map[string]string{}
	if err := calculateForTest("forgejo", env, map[string]string{}); err != nil {
		t.Fatalf("calculate for another module must be inert: %v", err)
	}
	if len(env) != 0 {
		t.Fatalf("env was modified for another module: %v", env)
	}
}

func TestNetworkIPv6RequiresBothTheSwitchAndTheHost(t *testing.T) {
	for name, tc := range map[string]struct {
		ipv6, hostHas, want string
	}{
		"wanted and available":        {"", "true", "true"},
		"explicitly on and available": {"true", "true", "true"},
		"wanted but host has none":    {"", "false", "false"},
		"switched off but available":  {"false", "true", "false"},
		"neither":                     {"false", "false", "false"},
	} {
		t.Run(name, func(t *testing.T) {
			env := validEnv(t)
			if tc.ipv6 != "" {
				env["IPv6"] = tc.ipv6
			}
			env["HOST_HAS_IPV6"] = tc.hostHas
			if err := calculateForTest("incus", env, map[string]string{}); err != nil {
				t.Fatal(err)
			}
			if got := env["INCUS_NETWORK_IPV6"]; got != tc.want {
				t.Fatalf("INCUS_NETWORK_IPV6 = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCalculateExplicitRemoteRequiresCompleteConnectionAndArchitecture(t *testing.T) {
	env := validEnv(t)
	delete(env, "INCUS_IMAGE_ARCHITECTURE")
	if err := calculateForTest("incus", env, map[string]string{}); err == nil {
		t.Fatal("explicit remote without image_architecture should be refused")
	}
	env = validEnv(t)
	env["INCUS_ADMIN_KEY_B64"] = ""
	resetHostBundleTestSeam(t, writeHostBundleFixture(t, hostConnectionBundleFixture(t)))
	if err := calculateForTest("incus", env, map[string]string{}); err == nil {
		t.Fatal("partial explicit connection should fail without reading the host bundle")
	}
}

func TestCalculateAutoProjectsHostBundleIntoEnvAndSecrets(t *testing.T) {
	bundle := hostConnectionBundleFixture(t)
	resetHostBundleTestSeam(t, writeHostBundleFixture(t, bundle))
	env := map[string]string{"NETWORK_PREFIX": "anas_"}
	secrets := map[string]string{}
	if err := calculateForTest("incus", env, secrets); err != nil {
		t.Fatalf("calculate: %v", err)
	}
	if env["INCUS_ENDPOINT"] != bundle.Endpoint || env["INCUS_IMAGE_ARCHITECTURE"] != "amd64" || env["INCUS_STORAGE_POOL"] != "anas-btrfs" {
		t.Fatalf("host bundle was not projected into env: %#v", env)
	}
	if secrets["INCUS_ADMIN_KEY_B64"] != env["INCUS_ADMIN_KEY_B64"] || secrets[autoSourceSecretKey] != autoSourceValue || secrets[autoBindingSecretKey] == "" {
		t.Fatalf("host bundle was not projected into secrets: %#v", secrets)
	}
	second := map[string]string{"NETWORK_PREFIX": "anas_"}
	if err := calculateForTest("incus", second, secrets); err != nil {
		t.Fatalf("idempotent calculate: %v", err)
	}
	if second["INCUS_ADMIN_KEY_B64"] != env["INCUS_ADMIN_KEY_B64"] {
		t.Fatal("idempotent automatic binding changed key material")
	}
}

func TestCalculateAutoRejectsBundleDriftAndMissingBundle(t *testing.T) {
	bundle := hostConnectionBundleFixture(t)
	path := writeHostBundleFixture(t, bundle)
	resetHostBundleTestSeam(t, path)
	env := map[string]string{"NETWORK_PREFIX": "anas_"}
	secrets := map[string]string{}
	if err := calculateForTest("incus", env, secrets); err != nil {
		t.Fatal(err)
	}
	bundle.Endpoint = "https://10.77.0.2:18443"
	bundle.ControlGateway = "10.77.0.2"
	writeHostBundleFixtureAt(t, path, bundle, 0600)
	if err := calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, secrets); err == nil {
		t.Fatal("changed host bundle should be refused when an automatic binding already exists")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, secrets); err == nil {
		t.Fatal("missing host bundle should not fall back to historical secrets")
	}
}

func TestCalculateAutomaticReplayRechecksBundleEvenWithCompletePersistedEnvironment(t *testing.T) {
	bundle := hostConnectionBundleFixture(t)
	path := writeHostBundleFixture(t, bundle)
	resetHostBundleTestSeam(t, path)
	env := map[string]string{"NETWORK_PREFIX": "anas_"}
	secrets := map[string]string{}
	if err := calculateForTest("incus", env, secrets); err != nil {
		t.Fatal(err)
	}
	// The real Runner restores Secret values into Env before calculate. This
	// is not an explicitly configured remote connection and must not bypass
	// revocation/drift checks merely because all four values are present.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := calculateForTest("incus", cloneMap(env), cloneMap(secrets)); err == nil {
		t.Fatal("persisted automatic credentials bypassed bundle removal")
	}
	writeHostBundleFixtureAt(t, path, bundle, 0600)
	bundle.Architecture = "arm64"
	writeHostBundleFixtureAt(t, path, bundle, 0600)
	if err := calculateForTest("incus", cloneMap(env), cloneMap(secrets)); err == nil {
		t.Fatal("persisted automatic credentials bypassed binding drift")
	}
}

func TestCalculateAutoRejectsInvalidBundleContent(t *testing.T) {
	for name, mutate := range map[string]func(*hostConnectionBundle){
		"wrong key pair":    func(b *hostConnectionBundle) { _, key := materials(t); b.AdminPrivateKeyPEM = mustDecodeB64(t, key) },
		"wrong fingerprint": func(b *hostConnectionBundle) { b.ManagementFingerprint = strings.Repeat("a", 64) },
		"wrong endpoint":    func(b *hostConnectionBundle) { b.Endpoint = "https://10.77.0.2:18443" },
		"missing arch":      func(b *hostConnectionBundle) { b.Architecture = "" },
		"wrong storage":     func(b *hostConnectionBundle) { b.StoragePool = "default" },
	} {
		t.Run(name, func(t *testing.T) {
			bundle := hostConnectionBundleFixture(t)
			mutate(&bundle)
			resetHostBundleTestSeam(t, writeHostBundleFixture(t, bundle))
			if err := calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, map[string]string{}); err == nil {
				t.Fatal("invalid host bundle should be refused")
			}
		})
	}
}

func TestCalculateAutoRejectsUnsafeBundleFile(t *testing.T) {
	bundle := hostConnectionBundleFixture(t)
	for name, mode := range map[string]os.FileMode{
		"group readable": 0640,
		"world readable": 0604,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "connection.json")
			writeHostBundleFixtureAt(t, path, bundle, mode)
			resetHostBundleTestSeam(t, path)
			if err := calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, map[string]string{}); err == nil {
				t.Fatal("unsafe host bundle mode should be refused")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		writeHostBundleFixtureAt(t, target, bundle, 0600)
		link := filepath.Join(dir, "connection.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		resetHostBundleTestSeamRaw(t, link)
		if err := calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, map[string]string{}); err == nil {
			t.Fatal("symlink host bundle should be refused")
		}
	})
	t.Run("duplicate JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "connection.json")
		if err := os.WriteFile(path, []byte(`{"schema":"anas.incus-connection-bundle/v1","schema":"anas.incus-connection-bundle/v1"}`), 0600); err != nil {
			t.Fatal(err)
		}
		resetHostBundleTestSeam(t, path)
		if err := calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, map[string]string{}); err == nil {
			t.Fatal("duplicate JSON fields should be refused")
		}
	})
}

func TestCalculateAutoDoesNotLeakPrivateMaterial(t *testing.T) {
	bundle := hostConnectionBundleFixture(t)
	path := writeHostBundleFixture(t, bundle)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "anas.incus-connection-bundle/v1", "wrong", 1))
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	resetHostBundleTestSeam(t, path)
	err = calculateForTest("incus", map[string]string{"NETWORK_PREFIX": "anas_"}, map[string]string{})
	if err == nil {
		t.Fatal("expected invalid bundle")
	}
	for _, marker := range []string{bundle.AdminPrivateKeyPEM, bundle.AdminCertificatePEM, bundle.ServerCertificatePEM} {
		if strings.Contains(err.Error(), marker[:24]) {
			t.Fatalf("error leaked bundle material: %v", err)
		}
	}
}

func hostConnectionBundleFixture(t *testing.T) hostConnectionBundle {
	t.Helper()
	certB64, keyB64 := materials(t)
	certPEM := mustDecodeB64(t, certB64)
	keyPEM := mustDecodeB64(t, keyB64)
	cert, err := parseCertificatePEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Raw)
	return hostConnectionBundle{
		Schema: hostBundleSchema, Endpoint: "https://10.77.0.1:18443", ServerCertificatePEM: certPEM, AdminCertificatePEM: certPEM,
		AdminPrivateKeyPEM: keyPEM, ControlNetwork: "anas-incus-control", ControlSubnet: "10.77.0.0/24", ControlGateway: "10.77.0.1",
		RelayService: "anas-incus-control-relay.service", ManagementFingerprint: hex.EncodeToString(sum[:]), Architecture: "amd64", StoragePool: "anas-btrfs",
	}
}

func writeHostBundleFixture(t *testing.T, bundle hostConnectionBundle) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "connection.json")
	writeHostBundleFixtureAt(t, path, bundle, 0600)
	return path
}

func writeHostBundleFixtureAt(t *testing.T, path string, bundle hostConnectionBundle, mode os.FileMode) {
	t.Helper()
	body, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func resetHostBundleTestSeam(t *testing.T, path string) {
	t.Helper()
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	resetHostBundleTestSeamRaw(t, realPath)
}

func resetHostBundleTestSeamRaw(t *testing.T, path string) {
	t.Helper()
	uid := uint32(os.Getuid())
	hostBundleTestPath = path
	hostBundleTrustedUID = &uid
	t.Cleanup(func() {
		hostBundleTestPath = ""
		hostBundleTrustedUID = nil
	})
}

func mustDecodeB64(t *testing.T, value string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
