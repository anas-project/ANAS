package runner

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/tempconsolecert"
)

func TestConsoleTokenUsesConfiguredAuditedStore(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, false, "", "")
	t.Setenv("ANASD_CONFIG", filepath.Join(t.TempDir(), "ignored.yml"))
	t.Setenv("ANASD_CONSOLE_STORE", filepath.Join(t.TempDir(), "ignored-store"))

	stdout, err := captureConsoleCLI(t, true, "token", "--config", fixture.configPath, "--ttl", "15m", "--json")
	if err != nil {
		t.Fatal(err)
	}
	document := requireSingleDocument(t, "console token", stdout)
	token, _ := document["token"].(string)
	if len(token) < 40 || strings.Count(stdout, token) != 1 {
		t.Fatalf("token was not emitted exactly once: length=%d output=%q", len(token), stdout)
	}
	if document["transaction_id"] != consoleBootstrapTransactionID || document["state"] != "bootstrap" {
		t.Fatalf("token binding = transaction %v, state %v", document["transaction_id"], document["state"])
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, document["issued_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, document["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if expiresAt.Sub(issuedAt) != 15*time.Minute {
		t.Fatalf("token TTL = %s", expiresAt.Sub(issuedAt))
	}

	stateBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, "bootstrap.json"))
	if strings.Contains(string(stateBody), token) {
		t.Fatal("authentication state persisted the raw token")
	}
	var state struct {
		Token struct {
			TransactionID string   `json:"transaction_id"`
			AllowedRoutes []string `json:"allowed_routes"`
		} `json:"token"`
	}
	if err := json.Unmarshal(stateBody, &state); err != nil {
		t.Fatal(err)
	}
	if state.Token.TransactionID != consoleBootstrapTransactionID || !reflect.DeepEqual(state.Token.AllowedRoutes, consoleBootstrapRoutePatterns()) {
		t.Fatalf("persisted token binding = %#v", state.Token)
	}
	auditBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, audit.Filename))
	if !strings.Contains(string(auditBody), "bootstrap_token.issue") || strings.Contains(string(auditBody), token) {
		t.Fatalf("audit record missing or leaked token: %s", auditBody)
	}
}

func TestConsoleTokenInEnrollmentKeepsTransactionAndOnlyRecoveryRoutes(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, false, "", "")
	writer, err := audit.Open(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := consolestate.Open(context.Background(), fixture.store, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	authStore, err := consoleauth.Open(fixture.store, consoleaudit.AuthSink{Writer: writer, Actor: "test"}, consoleauth.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := authStore.AdvanceToEnrollment(context.Background(), consoleBootstrapTransactionID, consoleauth.EnrollmentRecoveryRoutePatterns(), func(ctx context.Context) error {
		_, err := stateStore.Transition(ctx, consolestate.StateEnrollment, "test-certificate")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	stdout, err := captureConsoleCLI(t, true, "token", "--config", fixture.configPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	document := requireSingleDocument(t, "enrollment console token", stdout)
	if document["transaction_id"] != consoleBootstrapTransactionID || document["state"] != "enrollment" {
		t.Fatalf("token binding = transaction %v, state %v", document["transaction_id"], document["state"])
	}
	stateBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, "bootstrap.json"))
	var state struct {
		Token struct {
			AllowedRoutes []string `json:"allowed_routes"`
		} `json:"token"`
	}
	if err := json.Unmarshal(stateBody, &state); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Token.AllowedRoutes, consoleauth.EnrollmentRecoveryRoutePatterns()) {
		t.Fatalf("enrollment recovery routes = %v", state.Token.AllowedRoutes)
	}
	for _, forbidden := range []string{
		"/api/v1/workspaces/{ws}/config",
		"/api/v1/workspaces/{ws}/plans",
		"/api/v1/workspaces/{ws}/actions/apply",
	} {
		if slicesContainConsoleRoute(state.Token.AllowedRoutes, forbidden) {
			t.Fatalf("enrollment token retained forbidden route %q", forbidden)
		}
	}
}

func TestConsoleTokenRejectsFullCapabilityState(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, false, "", "")
	writer, err := audit.Open(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := consolestate.Open(context.Background(), fixture.store, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.Transition(context.Background(), consolestate.StateEnrollment, "test-certificate"); err != nil {
		t.Fatal(err)
	}
	if _, err := stateStore.Transition(context.Background(), consolestate.StateFull, "test-owner"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := issueConfiguredBootstrapToken(context.Background(), fixture.config, consoleauth.DefaultBootstrapTokenTTL); !errors.Is(err, consoleauth.ErrStateMismatch) {
		t.Fatalf("full-state token issue error = %v", err)
	}
}

func TestConsoleTokenRecoversPendingEnrollmentCommitBeforeIssuing(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, false, "", "")
	writer, err := audit.Open(fixture.store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := consolestate.Open(context.Background(), fixture.store, consoleaudit.StateSink{Writer: writer}); err != nil {
		t.Fatal(err)
	}
	authStore, err := consoleauth.Open(fixture.store, consoleaudit.AuthSink{Writer: writer, Actor: "test"}, consoleauth.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := authStore.AdvanceToEnrollment(context.Background(), consoleBootstrapTransactionID, consoleauth.EnrollmentRecoveryRoutePatterns(), func(context.Context) error {
		return errors.New("publish outcome unknown")
	}); !errors.Is(err, consoleauth.ErrRecoveryRequired) {
		t.Fatalf("ambiguous enrollment commit error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	issued, err := issueConfiguredBootstrapToken(context.Background(), fixture.config, consoleauth.DefaultBootstrapTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	if issued.State != consoleauth.StateBootstrap || issued.TransactionID != consoleBootstrapTransactionID {
		t.Fatalf("issued token after recovery = %#v", issued)
	}
}

func slicesContainConsoleRoute(routes []string, want string) bool {
	for _, route := range routes {
		if route == want {
			return true
		}
	}
	return false
}

func TestConsoleTLSSelfSignedUsesOnlyConfiguredSANsAndPrintsToken(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, true, "", "")
	t.Setenv("HOSTNAME", "must-not-be-a-san.example.test")
	t.Setenv("ANAS_CONSOLE_DNS_NAME", "also-not-a-san.example.test")
	t.Setenv("ANAS_CONSOLE_IP_ADDRESS", "198.51.100.77")

	stdout, err := captureConsoleCLI(t, true, "tls", "--self-signed", "--config", fixture.configPath, "--json")
	if err != nil {
		t.Fatal(err)
	}
	document := requireSingleDocument(t, "console tls", stdout)
	token, _ := document["token"].(string)
	if len(token) < 40 || strings.Count(stdout, token) != 1 {
		t.Fatalf("token was not emitted exactly once: length=%d output=%q", len(token), stdout)
	}
	if document["certificate_path"] != fixture.certificatePath || document["private_key_path"] != fixture.privateKeyPath {
		t.Fatalf("reported paths = %v, %v", document["certificate_path"], document["private_key_path"])
	}

	certificatePEM := mustReadConsoleCLIFile(t, fixture.certificatePath)
	block, rest := pem.Decode(certificatePEM)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("generated certificate is not one strict PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.IsCA || certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature) != nil {
		t.Fatal("temporary certificate is not a self-signed non-CA leaf")
	}
	if !reflect.DeepEqual(certificate.DNSNames, []string{"bootstrap.example.test"}) {
		t.Fatalf("DNS SANs = %q", certificate.DNSNames)
	}
	if got := ipStringsForConsoleCLI(certificate.IPAddresses); !reflect.DeepEqual(got, []string{"192.0.2.10"}) {
		t.Fatalf("IP SANs = %q", got)
	}
	for _, unwanted := range []string{"must-not-be-a-san.example.test", "also-not-a-san.example.test", "198.51.100.77"} {
		if strings.Contains(string(certificate.Raw), unwanted) {
			t.Fatalf("environment-derived SAN %q reached the certificate", unwanted)
		}
	}
	if document["certificate_sha256_fingerprint"] != consoleCLIFingerprint(certificate.Raw) ||
		document["spki_sha256_fingerprint"] != consoleCLIFingerprint(certificate.RawSubjectPublicKeyInfo) {
		t.Fatalf("fingerprints = certificate %v, SPKI %v", document["certificate_sha256_fingerprint"], document["spki_sha256_fingerprint"])
	}
	fingerprintPattern := regexp.MustCompile(`^([0-9A-F]{2}:){31}[0-9A-F]{2}$`)
	if !fingerprintPattern.MatchString(document["certificate_sha256_fingerprint"].(string)) || !fingerprintPattern.MatchString(document["spki_sha256_fingerprint"].(string)) {
		t.Fatal("fingerprints are not uppercase colon-separated SHA-256")
	}
	assertConsoleCLIFileMode(t, fixture.certificatePath, 0o600)
	assertConsoleCLIFileMode(t, fixture.privateKeyPath, 0o600)

	stateBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, "bootstrap.json"))
	auditBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, audit.Filename))
	if strings.Contains(string(stateBody), token) || strings.Contains(string(auditBody), token) {
		t.Fatal("TLS command persisted its raw bootstrap token")
	}
}

func TestConsoleTLSRejectsPathsThatGeneratorCannotMatch(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tls")
	fixture := writeConsoleCLIFixture(t, true,
		filepath.Join(directory, "custom.crt"), filepath.Join(directory, "custom.key"))
	stdout, err := captureConsoleCLI(t, false, "tls", "--self-signed", "--config", fixture.configPath)
	if stdout != "" {
		t.Fatalf("failed command printed success output: %q", stdout)
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != "temporary_tls_path_mismatch" || cliErr.Exit != exitPrecondition {
		t.Fatalf("error = %#v", err)
	}
	if _, err := os.Stat(fixture.certificatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected certificate target state: %v", err)
	}
}

func TestConsoleTLSDoesNotPrintPartialSuccessWhenTokenIssuanceFails(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, true, "", "")
	if err := os.MkdirAll(fixture.store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fixture.store, 0o700); err != nil {
		t.Fatal(err)
	}
	corruptState := filepath.Join(fixture.store, "bootstrap.json")
	if err := os.WriteFile(corruptState, []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(corruptState, 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, err := captureConsoleCLI(t, false, "tls", "--self-signed", "--config", fixture.configPath)
	if stdout != "" {
		t.Fatalf("failed command printed partial success: %q", stdout)
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != "console_token_failed" || cliErr.Exit != exitFailure {
		t.Fatalf("error = %#v", err)
	}
	if _, err := os.Stat(fixture.certificatePath); err != nil {
		t.Fatalf("certificate generation did not precede token issuance: %v", err)
	}
	if _, err := os.Stat(fixture.privateKeyPath); err != nil {
		t.Fatalf("private key generation did not precede token issuance: %v", err)
	}
	auditBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, audit.Filename))
	if !strings.Contains(string(auditBody), `"outcome":"failure"`) || strings.Contains(string(auditBody), "BEGIN PRIVATE KEY") {
		t.Fatalf("failed issuance audit = %s", auditBody)
	}
}

func TestConsoleCommandUsageAndTTLBounds(t *testing.T) {
	fixture := writeConsoleCLIFixture(t, false, "", "")
	for _, args := range [][]string{
		{},
		{"wat"},
		{"token", "extra", "--config", fixture.configPath},
		{"tls", "--config", fixture.configPath},
	} {
		if err := runConsoleWithPolicy(args, false, consoleconfig.CurrentUIDFilePolicy()); ExitCode(err) != exitUsage {
			t.Fatalf("args %q error = %v, exit %d", args, err, ExitCode(err))
		}
	}
	stdout, err := captureConsoleCLI(t, false, "token", "--config", fixture.configPath, "--ttl", "14m")
	if stdout != "" {
		t.Fatalf("invalid TTL printed output: %q", stdout)
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) || cliErr.Code != "console_token_failed" {
		t.Fatalf("invalid TTL error = %#v", err)
	}
	auditBody := mustReadConsoleCLIFile(t, filepath.Join(fixture.store, audit.Filename))
	if !strings.Contains(string(auditBody), `"outcome":"failure"`) {
		t.Fatalf("invalid TTL was not audited: %s", auditBody)
	}
}

func TestConsoleStatusPublishesDirectRecoveryAndProxyAddresses(t *testing.T) {
	config := consoleconfig.Config{
		Mode: consoleconfig.ModeLAN, Port: 8080, AllowedDNSHosts: []string{"anas.example.test"},
		TLS:          consoleconfig.TLSConfig{Temporary: &consoleconfig.TemporaryTLSPaths{}},
		TrustedProxy: &consoleconfig.TrustedProxyConfig{PublicURL: "https://anas.example.test:9000"},
	}
	status, err := ConfiguredConsoleStatus(config, func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::10"), Mask: net.CIDRMask(64, 128)},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://anas.example.test:8080",
		"https://192.0.2.10:8080",
		"https://[2001:db8::10]:8080",
	}
	if !reflect.DeepEqual(status.DirectRecoveryURLs, want) || status.ProxyURL != "https://anas.example.test:9000" || !status.BindsAllInterfaces {
		t.Fatalf("console status = %#v, want direct=%v", status, want)
	}
}

func TestConsoleStatusLoopbackDoesNotPublishLANAddresses(t *testing.T) {
	status, err := ConfiguredConsoleStatus(consoleconfig.Config{Mode: consoleconfig.ModeLoopback, Port: 8080}, func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(status.DirectRecoveryURLs, []string{"http://127.0.0.1:8080"}) || status.BindsAllInterfaces {
		t.Fatalf("loopback console status = %#v", status)
	}
}

type consoleCLIFixture struct {
	configPath      string
	store           string
	config          consoleconfig.Config
	certificatePath string
	privateKeyPath  string
}

func writeConsoleCLIFixture(t *testing.T, withTemporary bool, certificatePath, privateKeyPath string) consoleCLIFixture {
	t.Helper()
	root := t.TempDir()
	fixture := consoleCLIFixture{
		configPath: filepath.Join(root, "anasd.yml"),
		store:      filepath.Join(root, "console-store"),
	}
	if withTemporary {
		directory := filepath.Join(root, "temporary-tls")
		if certificatePath == "" {
			certificatePath = filepath.Join(directory, tempconsolecert.CertificateFilename)
		}
		if privateKeyPath == "" {
			privateKeyPath = filepath.Join(directory, tempconsolecert.PrivateKeyFilename)
		}
		fixture.certificatePath = certificatePath
		fixture.privateKeyPath = privateKeyPath
	}
	source := fmt.Sprintf("api_version: %s\nconsole_store: %q\n", consoleconfig.APIVersion, fixture.store)
	if withTemporary {
		source += fmt.Sprintf("tls:\n  temporary:\n    certificate: %q\n    private_key: %q\n    dns_names: [bootstrap.example.test]\n    ip_addresses: [192.0.2.10]\n",
			fixture.certificatePath, fixture.privateKeyPath)
	}
	config, err := consoleconfig.Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	fixture.config = config
	if err := os.WriteFile(fixture.configPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fixture.configPath, 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func captureConsoleCLI(t *testing.T, jsonMode bool, args ...string) (string, error) {
	t.Helper()
	output, err := os.CreateTemp(t.TempDir(), "console-cli-output")
	if err != nil {
		t.Fatal(err)
	}
	realOutput := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = realOutput }()
	commandErr := runConsoleWithPolicy(args, jsonMode, consoleconfig.CurrentUIDFilePolicy())
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return string(body), commandErr
}

func mustReadConsoleCLIFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertConsoleCLIFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want)
	}
}

func consoleCLIFingerprint(body []byte) string {
	digest := sha256.Sum256(body)
	encoded := strings.ToUpper(hex.EncodeToString(digest[:]))
	parts := make([]string, 0, len(digest))
	for index := 0; index < len(encoded); index += 2 {
		parts = append(parts, encoded[index:index+2])
	}
	return strings.Join(parts, ":")
}

func ipStringsForConsoleCLI(addresses []net.IP) []string {
	result := make([]string, len(addresses))
	for index, address := range addresses {
		result[index] = address.String()
	}
	return result
}
