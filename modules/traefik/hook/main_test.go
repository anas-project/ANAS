package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestTransportClientIdentityIsStableAndOnlyMaterializedForTraefik(t *testing.T) {
	env := map[string]string{
		"ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__SERVER_NAME": "anas.example.test",
		"TRAEFIK_LOCAL_ADMIN_USERNAME":                                   "admin_traefik", "TRAEFIK_LOCAL_ADMIN_PASSWORD": "secret",
	}
	secrets := &secretStore{values: map[string]string{}}
	if err := calculate("traefik", env, "", secrets); err != nil {
		t.Fatal(err)
	}
	key := transportIdentitySecretKey("ANAS_CONSOLE_MTLS")
	first := secrets.values[key]
	if first == "" {
		t.Fatal("transport identity was not persisted in the scoped Secret Store")
	}
	if err := calculate("traefik", env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if secrets.values[key] != first {
		t.Fatal("recalculation rotated the transport identity")
	}
	files, err := renderRuntimeEnv("traefik", env, secrets.values)
	if err != nil {
		t.Fatal(err)
	}
	root := "dynamic/client-identities/ANAS_CONSOLE_MTLS/"
	if files[root+"ca.key"] != "" || files[root+"ca.crt"] == "" || files[root+"client.crt"] == "" || files[root+"client.key"] == "" {
		t.Fatalf("materialized transport files = %v", files)
	}
	caBlock, _ := pem.Decode([]byte(files[root+"ca.crt"]))
	clientBlock, _ := pem.Decode([]byte(files[root+"client.crt"]))
	if caBlock == nil || clientBlock == nil {
		t.Fatal("transport certificates are not PEM")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	client, err := x509.ParseCertificate(clientBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := client.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client identity does not verify: %v", err)
	}
	digest := sha256.Sum256(client.RawSubjectPublicKeyInfo)
	if got := strings.TrimSpace(files[root+"client.spki-sha256"]); got != hex.EncodeToString(digest[:]) {
		t.Fatalf("client SPKI file = %q", got)
	}
}

func TestEntrypointRendersMutualTLSUpstreamTransport(t *testing.T) {
	configDir := t.TempDir()
	identityDir := filepath.Join(configDir, "client-identities", "ANAS_CONSOLE_MTLS")
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ca.crt", "client.crt", "client.key", "client.spki-sha256"} {
		if err := os.WriteFile(filepath.Join(identityDir, name), []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("/bin/sh", "../traefik/anas-entrypoint.sh")
	command.Env = append(os.Environ(),
		"ANAS_CONFIG_DIR="+configDir,
		"ANAS_TRAEFIK_BINARY=/usr/bin/true",
		"LEGO_CERT_NAME=example.test.crt",
		"LEGO_KEY_NAME=example.test.key",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__RULE=Host(`anas.example.test`)",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__URL=https://host.docker.internal:8443",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__MIDDLEWARES=anas-forward-auth@docker",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__SERVERS_TRANSPORT=ANAS_CONSOLE_MTLS",
		"ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__SERVER_NAME=anas.example.test",
		"ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__ROOT_CAS=/certs/anas-trust-bundle.crt",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("entrypoint: %v\n%s", err, output)
	}
	routes, err := os.ReadFile(filepath.Join(configDir, "routes.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(routes)
	for _, want := range []string{
		`serversTransport: "ANAS_CONSOLE_MTLS"`,
		"serversTransports:",
		`serverName: "anas.example.test"`,
		`- "/certs/anas-trust-bundle.crt"`,
		`certFile: "` + configDir + `/client-identities/ANAS_CONSOLE_MTLS/client.crt"`,
		`keyFile: "` + configDir + `/client-identities/ANAS_CONSOLE_MTLS/client.key"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("routes.yml is missing %q:\n%s", want, text)
		}
	}
}

func TestEntrypointRejectsUnpinnedUpstreamTransport(t *testing.T) {
	command := exec.Command("/bin/sh", "../traefik/anas-entrypoint.sh")
	command.Env = append(os.Environ(),
		"ANAS_CONFIG_DIR="+t.TempDir(),
		"ANAS_TRAEFIK_BINARY=/usr/bin/true",
		"LEGO_CERT_NAME=example.test.crt",
		"LEGO_KEY_NAME=example.test.key",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__RULE=Host(`anas.example.test`)",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__URL=https://host.docker.internal:8443",
		"ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__SERVERS_TRANSPORT=ANAS_CONSOLE_MTLS",
		"ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__SERVER_NAME=anas.example.test",
	)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "ROOT_CAS") {
		t.Fatalf("incomplete transport result err=%v output=%s", err, output)
	}
}

func TestRenderRuntimeEnvUsesOnlyManagedDashboardCredential(t *testing.T) {
	files, err := renderRuntimeEnv("traefik", map[string]string{
		"TRAEFIK_LOCAL_ADMIN_USERNAME": "admin_traefik",
		"TRAEFIK_LOCAL_ADMIN_PASSWORD": "s3cret",
		"BASICAUTH_PASSWD":             "must-not-win",
	}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files["dynamic/dashboard-auth.yml"], "s3cret") {
		t.Fatal("plaintext password entered rendered application state")
	}
	if err := verifyTraefikAuthFile([]byte(files["dynamic/dashboard-auth.yml"]), "admin_traefik", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if _, err := renderRuntimeEnv("traefik", map[string]string{}, map[string]string{}); err == nil {
		t.Fatal("missing managed credential was accepted")
	}
}

func TestRuntimeRestoreReturnsNoArtifactFiles(t *testing.T) {
	resp, err := handle(hookRequest{Module: "traefik", Phase: "runtime_restore", Env: map[string]string{
		"TRAEFIK_LOCAL_ADMIN_USERNAME": "admin_traefik",
		"TRAEFIK_LOCAL_ADMIN_PASSWORD": "s3cret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 0 {
		t.Fatalf("runtime restore returned artifact files: %v", resp.Files)
	}
	if _, ok := resp.RuntimeFiles["dynamic/dashboard-auth.yml"]; !ok {
		t.Fatal("runtime restore did not reconstruct dashboard authentication")
	}
}

func verifyTraefikAuthFile(content []byte, username, password string) error {
	prefix := username + ":"
	i := strings.Index(string(content), prefix)
	if i < 0 {
		return fmt.Errorf("username missing")
	}
	hash := string(content)[i+len(prefix):]
	if j := strings.IndexAny(hash, "\"\r\n"); j >= 0 {
		hash = hash[:j]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func TestComposeUsesRenderedTraefikNetworkName(t *testing.T) {
	b, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--providers.docker.network=${NETWORK_PREFIX}traefik") {
		t.Fatal("Docker provider must select the rendered Traefik network name")
	}
	if !strings.Contains(string(b), "--providers.docker.constraints=Label(`anas.traefik.instance`,`${NETWORK_PREFIX}traefik`)") {
		t.Fatal("Docker provider must only discover containers assigned to this Traefik instance")
	}
	if !strings.Contains(string(b), "${ANAS_MODULE_RUNTIME_STATE_PATH}/dynamic:/run/anas") {
		t.Fatal("Traefik file-provider state is not mounted outside the sealed artifact")
	}
	for _, required := range []string{
		"--accesslog=true",
		"--accesslog.format=json",
		"--accesslog.fields.headers.defaultmode=drop",
		"--accesslog.fields.queryparameters.defaultmode=drop",
	} {
		if !strings.Contains(string(b), required) {
			t.Fatalf("Traefik compose is missing safe access-log option %s", required)
		}
	}

	composeFiles, err := filepath.Glob("../../*/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, composeFile := range composeFiles {
		compose, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(compose), "traefik.docker.network=traefik") {
			t.Fatalf("%s contains an unrendered Traefik network label", composeFile)
		}
		if strings.Contains(string(compose), "forwardedHeaders.insecure") {
			t.Fatalf("%s enables insecure forwarded-header trust", composeFile)
		}
		if strings.Contains(string(compose), "--trusted-proxy-ip=10.0.0.0/8") ||
			strings.Contains(string(compose), "--trusted-proxy-ip=172.16.0.0/12") ||
			strings.Contains(string(compose), "--trusted-proxy-ip=192.168.0.0/16") {
			t.Fatalf("%s trusts an entire private address range for forwarded headers", composeFile)
		}
		lines := strings.Split(string(compose), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "traefik.enable=true") {
				continue
			}
			serviceEnd := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "  ") && !strings.HasPrefix(lines[j], "    ") {
					serviceEnd = j
					break
				}
			}
			service := strings.Join(lines[i:serviceEnd], "\n")
			if !strings.Contains(service, "anas.traefik.instance=${NETWORK_PREFIX}traefik") {
				t.Fatalf("%s has a Traefik-enabled service without an instance isolation label near line %d", composeFile, i+1)
			}
			if !strings.Contains(service, "anas.client-ip.mode=application") && !strings.Contains(service, "anas.client-ip.mode=edge") {
				t.Fatalf("%s has a Traefik-enabled service without a client-IP handling mode near line %d", composeFile, i+1)
			}
		}
	}
}

func TestTrustedProxyCIDRValidation(t *testing.T) {
	for _, value := range []string{"", "192.0.2.10", "192.0.2.0/24", "2001:db8::/32", "192.0.2.0/24, 2001:db8::1"} {
		if err := validateTrustedProxyCIDRs(value); err != nil {
			t.Fatalf("validateTrustedProxyCIDRs(%q): %v", value, err)
		}
	}
	for _, value := range []string{"proxy.example.com", "0.0.0.0/999", "192.0.2.1,not-an-ip"} {
		if err := validateTrustedProxyCIDRs(value); err == nil {
			t.Fatalf("validateTrustedProxyCIDRs(%q) accepted invalid input", value)
		}
	}
}
