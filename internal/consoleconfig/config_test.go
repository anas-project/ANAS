package consoleconfig

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseDefaultsAndNormalizes(t *testing.T) {
	config, err := Parse([]byte(`api_version: anas.console-config/v1
console_store: /srv/anas/./.anas/console
allowed_dns_hosts:
  - ANAS.Example.Test.
workspaces:
  - id: main
    path: /srv/anas/./workspace
tls:
  lego:
    base_domain: Example.Test
    certificate: /srv/anas/certs/./anas.crt
    private_key: /srv/anas/certs/anas.key
    issuer: /srv/anas/certs/issuer.crt
    trust_bundle: /srv/anas/certs/trust.crt
    internal_ca: /srv/anas/certs/anas-internal-ca.crt
    issuer_marker: /srv/anas/certs/.issuer
  temporary:
    certificate: /var/lib/anas/temp.crt
    private_key: /var/lib/anas/temp.key
    ip_addresses: [192.0.2.10]
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeLAN || config.Port != DefaultPort {
		t.Fatalf("defaults = mode %q, port %d", config.Mode, config.Port)
	}
	if !reflect.DeepEqual(config.AllowedDNSHosts, []string{"anas.example.test"}) {
		t.Fatalf("allowed DNS hosts = %#v", config.AllowedDNSHosts)
	}
	if config.ConsoleStore != "/srv/anas/.anas/console" || config.Workspaces[0].Path != "/srv/anas/workspace" {
		t.Fatalf("normalized paths = store %q, workspace %q", config.ConsoleStore, config.Workspaces[0].Path)
	}
	if config.TLS.Lego == nil || config.TLS.Lego.BaseDomain != "example.test" || config.TLS.Lego.Certificate != "/srv/anas/certs/anas.crt" || config.TLS.Temporary == nil || config.TLS.Temporary.IPAddresses[0] != "192.0.2.10" {
		t.Fatalf("TLS paths = %#v", config.TLS)
	}
}

func TestParseExplicitLoopbackAndPort(t *testing.T) {
	config, err := Parse([]byte(`api_version: anas.console-config/v1
mode: loopback
port: 8443
console_store: /var/lib/anas/console
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeLoopback || config.Port != 8443 || config.Workspaces == nil || config.AllowedDNSHosts == nil {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseTrustedProxyRequiresExactSourceAndPinnedMutualTLSIdentity(t *testing.T) {
	source := `api_version: anas.console-config/v1
mode: lan
port: 8080
console_store: /var/lib/anas/console
allowed_dns_hosts: [anas.example.test]
tls:
  temporary:
    certificate: /var/lib/anas/temporary/console.crt
    private_key: /var/lib/anas/temporary/console.key
    dns_names: [anas.example.test]
trusted_proxy:
  bind_address: 0.0.0.0
  port: 8443
  public_url: https://ANAS.EXAMPLE.TEST:9000/
  allowed_source_ips: [172.19.0.2]
  allowed_dns_hosts: [ANAS.EXAMPLE.TEST.]
  oidc_issuer: https://iam.example.test
  platform_admin_group: NAS Admins
  client_ca: /var/lib/anas/traefik/client-identities/ANAS_CONSOLE_MTLS/ca.crt
  client_spki_sha256: [0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef]
`
	config, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	proxy := config.TrustedProxy
	if proxy == nil || proxy.PublicURL != "https://anas.example.test:9000" || proxy.BindAddress != "0.0.0.0" || !reflect.DeepEqual(proxy.AllowedDNSHosts, []string{"anas.example.test"}) {
		t.Fatalf("trusted proxy = %#v", proxy)
	}
	for name, replacement := range map[string]string{
		"source CIDR":          "allowed_source_ips: [172.19.0.0/24]",
		"unlisted public host": "public_url: https://other.example.test:9000",
		"non HTTPS public URL": "public_url: http://ANAS.EXAMPLE.TEST:9000/",
		"missing client pin":   "client_spki_sha256: []",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := source
			switch name {
			case "source CIDR":
				candidate = strings.Replace(candidate, "allowed_source_ips: [172.19.0.2]", replacement, 1)
			case "unlisted public host", "non HTTPS public URL":
				candidate = strings.Replace(candidate, "public_url: https://ANAS.EXAMPLE.TEST:9000/", replacement, 1)
			case "missing client pin":
				candidate = strings.Replace(candidate, "client_spki_sha256: [0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef]", replacement, 1)
			}
			if _, err := Parse([]byte(candidate)); err == nil {
				t.Fatal("invalid trusted proxy configuration was accepted")
			}
		})
	}
}

func TestParseRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{name: "unknown field", source: validSource() + "unknown: true\n", message: "field unknown not found"},
		{name: "multiple documents", source: validSource() + "---\n{}\n", message: "multiple YAML documents"},
		{name: "wrong version", source: strings.Replace(validSource(), APIVersion, "anas.console-config/v2", 1), message: "api_version"},
		{name: "bad mode", source: validSource() + "mode: automatic\n", message: "mode must be"},
		{name: "zero port", source: validSource() + "port: 0\n", message: "port must be"},
		{name: "port string", source: validSource() + "port: \"8080\"\n", message: "cannot unmarshal"},
		{name: "relative store", source: strings.Replace(validSource(), "/var/lib/anas/console", "console", 1), message: "console_store must be an absolute path"},
		{name: "IP host", source: validSource() + "allowed_dns_hosts: [192.0.2.1]\n", message: "DNS name"},
		{name: "host port", source: validSource() + "allowed_dns_hosts: [anas.example.test:8080]\n", message: "without an IP literal or port"},
		{name: "wildcard host", source: validSource() + "allowed_dns_hosts: ['*.example.test']\n", message: "valid ASCII DNS name"},
		{name: "duplicate normalized host", source: validSource() + "allowed_dns_hosts: [ANAS.EXAMPLE, anas.example.]\n", message: "duplicate host"},
		{name: "bad workspace ID", source: validSource() + "workspaces: [{id: ../main, path: /srv/main}]\n", message: "workspace ID"},
		{name: "relative workspace", source: validSource() + "workspaces: [{id: main, path: srv/main}]\n", message: "absolute path"},
		{name: "duplicate workspace ID", source: validSource() + "workspaces: [{id: main, path: /srv/main}, {id: main, path: /srv/lab}]\n", message: "registered more than once"},
		{name: "duplicate clean workspace path", source: validSource() + "workspaces: [{id: main, path: /srv/main}, {id: lab, path: /srv/./main}]\n", message: "same path"},
		{name: "partial lego pair", source: validSource() + "tls: {lego: {base_domain: example.test, certificate: /certs/anas.crt, issuer: /certs/issuer.crt, trust_bundle: /certs/trust.crt, issuer_marker: /certs/.issuer}}\n", message: "tls.lego.private_key"},
		{name: "relative lego path", source: validSource() + "tls: {lego: {base_domain: example.test, certificate: cert.pem, private_key: /certs/key.pem, issuer: /certs/issuer.crt, trust_bundle: /certs/trust.crt, issuer_marker: /certs/.issuer}}\n", message: "tls.lego.certificate"},
		{name: "same lego pair path", source: validSource() + "tls: {lego: {base_domain: example.test, certificate: /certs/pair.pem, private_key: /certs/pair.pem, issuer: /certs/issuer.crt, trust_bundle: /certs/trust.crt, issuer_marker: /certs/.issuer}}\n", message: "different paths"},
		{name: "missing lego base domain", source: validSource() + "tls: {lego: {certificate: /certs/cert.pem, private_key: /certs/key.pem, issuer: /certs/issuer.crt, trust_bundle: /certs/trust.crt, issuer_marker: /certs/.issuer}}\n", message: "base_domain"},
		{name: "missing lego issuer", source: validSource() + "tls: {lego: {base_domain: example.test, certificate: /certs/cert.pem, private_key: /certs/key.pem, trust_bundle: /certs/trust.crt, issuer_marker: /certs/.issuer}}\n", message: "tls.lego.issuer must be an absolute path"},
		{name: "missing lego internal CA", source: validSource() + "tls: {lego: {base_domain: example.test, certificate: /certs/cert.pem, private_key: /certs/key.pem, issuer: /certs/issuer.crt, trust_bundle: /certs/trust.crt, issuer_marker: /certs/.issuer}}\n", message: "tls.lego.internal_ca must be an absolute path"},
		{name: "partial temporary pair", source: validSource() + "tls: {temporary: {private_key: /certs/key.pem, ip_addresses: [192.0.2.10]}}\n", message: "tls.temporary.certificate"},
		{name: "temporary without SAN", source: validSource() + "tls: {temporary: {certificate: /certs/cert.pem, private_key: /certs/key.pem}}\n", message: "at least one explicit"},
		{name: "temporary unspecified IP", source: validSource() + "tls: {temporary: {certificate: /certs/cert.pem, private_key: /certs/key.pem, ip_addresses: [0.0.0.0]}}\n", message: "concrete IP"},
		{name: "store inside workspace", source: "api_version: " + APIVersion + "\nconsole_store: /srv/main/.anas/console\nworkspaces: [{id: main, path: /srv/main}]\n", message: "outside registered workspace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.source))
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Parse error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestParseDoesNotReadOrExpandEnvironment(t *testing.T) {
	t.Setenv("ANASD_MODE", "loopback")
	t.Setenv("ANASD_PORT", "9443")
	t.Setenv("ANASD_CONSOLE_STORE", "/tmp/from-environment")
	config, err := Parse([]byte(validSource()))
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != ModeLAN || config.Port != DefaultPort || config.ConsoleStore != "/var/lib/anas/console" {
		t.Fatalf("environment affected config: %#v", config)
	}

	_, err = Parse([]byte("api_version: " + APIVersion + "\nconsole_store: ${ANASD_CONSOLE_STORE}\n"))
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("environment-looking path error = %v", err)
	}
}

func TestLoadUsesExplicitSecurityPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anasd.yml")
	if err := os.WriteFile(path, []byte(validSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	policy := FileSecurityPolicyFunc(func(gotPath string, info fs.FileInfo) error {
		calls++
		if gotPath != path || !info.Mode().IsRegular() {
			t.Fatalf("policy input = %q, %v", gotPath, info.Mode())
		}
		return nil
	})
	config, err := Load(path, policy)
	if err != nil {
		t.Fatal(err)
	}
	if config.APIVersion != APIVersion || calls != 4 {
		t.Fatalf("loaded config = %#v, policy calls = %d", config, calls)
	}
}

func TestLoadRejectsReplacementAndMutationDuringRead(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, path string)
	}{
		{
			name: "atomic replacement",
			mutate: func(t *testing.T, path string) {
				replacement := filepath.Join(filepath.Dir(path), "replacement.yml")
				if err := os.WriteFile(replacement, []byte(validSource()), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "in-place mutation",
			mutate: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte(validSource()+"# changed while open\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "anasd.yml")
			if err := os.WriteFile(path, []byte(validSource()), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			policy := FileSecurityPolicyFunc(func(string, fs.FileInfo) error {
				calls++
				if calls == 2 {
					test.mutate(t, path)
				}
				return nil
			})
			_, err := Load(path, policy)
			if err == nil || !strings.Contains(err.Error(), "changed while it was being read") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadRejectsOversizedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anasd.yml")
	body := []byte(validSource() + "padding: " + strings.Repeat("x", maximumConfigBytes) + "\n")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, FileSecurityPolicyFunc(func(string, fs.FileInfo) error { return nil }))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized Load error = %v", err)
	}
}

func TestLoadRejectsConsoleStoreThatResolvesInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".anas"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "outside-looking-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	path := filepath.Join(root, "anasd.yml")
	source := "api_version: " + APIVersion + "\n" +
		"console_store: " + filepath.Join(alias, ".anas", "console") + "\n" +
		"workspaces: [{id: main, path: " + workspace + "}]\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, CurrentUIDFilePolicy())
	if err == nil || !strings.Contains(err.Error(), "resolves inside registered workspace") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRequiresAbsolutePathAndPolicy(t *testing.T) {
	if _, err := Load("anasd.yml", CurrentUIDFilePolicy()); err == nil || !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("relative path error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "anasd.yml")
	if err := os.WriteFile(path, []byte(validSource()), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, nil); err == nil || !strings.Contains(err.Error(), "policy is required") {
		t.Fatalf("nil policy error = %v", err)
	}
}

func validSource() string {
	return "api_version: " + APIVersion + "\nconsole_store: /var/lib/anas/console\n"
}
