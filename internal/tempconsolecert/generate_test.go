package tempconsolecert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerateCreatesExplicitNonCALeaf(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "certificate")
	before := time.Now().UTC()
	result, err := Generate(Options{
		Directory:   directory,
		DNSNames:    []string{"NAS.Example.COM", "nas.example.com", "localhost"},
		IPAddresses: []string{"2001:db8::10", "192.0.2.10", "192.0.2.10"},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	assertMode(t, directory, os.ModeDir|0o700)
	assertMode(t, result.CertificatePath, 0o600)
	assertMode(t, result.PrivateKeyPath, 0o600)
	assertMode(t, filepath.Join(directory, LockFilename), 0o600)

	certificate, privateKey, certificatePEM, privateKeyPEM := readPair(t, result.CertificatePath, result.PrivateKeyPath)
	if !certificate.BasicConstraintsValid {
		t.Error("BasicConstraintsValid = false, want true")
	}
	if certificate.IsCA {
		t.Error("IsCA = true, want false")
	}
	if certificate.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Errorf("KeyUsage = %v, want DigitalSignature", certificate.KeyUsage)
	}
	if !reflect.DeepEqual(certificate.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Errorf("ExtKeyUsage = %v, want only ServerAuth", certificate.ExtKeyUsage)
	}
	if err := certificate.CheckSignature(certificate.SignatureAlgorithm, certificate.RawTBSCertificate, certificate.Signature); err != nil {
		t.Errorf("self-signature verification failed: %v", err)
	}
	publicKey, ok := certificate.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("certificate public key type = %T, want *ecdsa.PublicKey", certificate.PublicKey)
	}
	if privateKey.Curve.Params().Name != "P-256" {
		t.Errorf("private key curve = %s, want P-256", privateKey.Curve.Params().Name)
	}
	if !publicKey.Equal(&privateKey.PublicKey) {
		t.Error("certificate public key does not match private key")
	}

	wantDNSNames := []string{"localhost", "nas.example.com"}
	if !reflect.DeepEqual(certificate.DNSNames, wantDNSNames) {
		t.Errorf("certificate DNS SANs = %v, want %v", certificate.DNSNames, wantDNSNames)
	}
	if !reflect.DeepEqual(result.DNSNames, wantDNSNames) {
		t.Errorf("result DNS SANs = %v, want %v", result.DNSNames, wantDNSNames)
	}
	wantIPs := []string{"192.0.2.10", "2001:db8::10"}
	if got := ipStrings(certificate.IPAddresses); !reflect.DeepEqual(got, wantIPs) {
		t.Errorf("certificate IP SANs = %v, want %v", got, wantIPs)
	}
	if !reflect.DeepEqual(result.IPAddresses, wantIPs) {
		t.Errorf("result IP SANs = %v, want %v", result.IPAddresses, wantIPs)
	}
	if got := certificate.NotAfter.Sub(certificate.NotBefore); got != DefaultValidity {
		t.Errorf("certificate lifetime = %s, want %s", got, DefaultValidity)
	}
	if certificate.NotBefore.Before(before.Add(-clockSkew-time.Second)) || certificate.NotBefore.After(time.Now().UTC()) {
		t.Errorf("NotBefore = %s, outside expected issuance window", certificate.NotBefore)
	}
	if !result.NotBefore.Equal(certificate.NotBefore) || !result.NotAfter.Equal(certificate.NotAfter) {
		t.Errorf("result validity = [%s, %s], certificate validity = [%s, %s]", result.NotBefore, result.NotAfter, certificate.NotBefore, certificate.NotAfter)
	}

	if got, want := result.CertificateSHA256Fingerprint, testFingerprint(certificate.Raw); got != want {
		t.Errorf("certificate fingerprint = %q, want %q", got, want)
	}
	if got, want := result.SPKISHA256Fingerprint, testFingerprint(certificate.RawSubjectPublicKeyInfo); got != want {
		t.Errorf("SPKI fingerprint = %q, want %q", got, want)
	}
	assertFingerprintFormat(t, result.CertificateSHA256Fingerprint)
	assertFingerprintFormat(t, result.SPKISHA256Fingerprint)

	second, err := Generate(Options{Directory: directory, DNSNames: []string{"nas.example.com"}})
	if err != nil {
		t.Fatalf("second Generate() error = %v", err)
	}
	_, _, secondCertificatePEM, secondPrivateKeyPEM := readPair(t, second.CertificatePath, second.PrivateKeyPath)
	if bytes.Equal(certificatePEM, secondCertificatePEM) {
		t.Error("second generation reused the certificate")
	}
	if bytes.Equal(privateKeyPEM, secondPrivateKeyPEM) {
		t.Error("second generation reused the private key")
	}
}

func TestGenerateRejectsInvalidOptions(t *testing.T) {
	validDirectory := filepath.Join(t.TempDir(), "valid")
	tests := []struct {
		name    string
		options Options
	}{
		{name: "missing directory", options: Options{DNSNames: []string{"nas.example"}}},
		{name: "relative directory", options: Options{Directory: "relative", DNSNames: []string{"nas.example"}}},
		{name: "no SAN", options: Options{Directory: validDirectory}},
		{name: "blank DNS", options: Options{Directory: validDirectory, DNSNames: []string{""}}},
		{name: "DNS whitespace", options: Options{Directory: validDirectory, DNSNames: []string{" nas.example"}}},
		{name: "wildcard DNS", options: Options{Directory: validDirectory, DNSNames: []string{"*.example.com"}}},
		{name: "trailing DNS dot", options: Options{Directory: validDirectory, DNSNames: []string{"nas.example.com."}}},
		{name: "DNS underscore", options: Options{Directory: validDirectory, DNSNames: []string{"nas_console.example.com"}}},
		{name: "DNS scheme", options: Options{Directory: validDirectory, DNSNames: []string{"https://nas.example.com"}}},
		{name: "IP in DNS field", options: Options{Directory: validDirectory, DNSNames: []string{"192.0.2.10"}}},
		{name: "invalid IP", options: Options{Directory: validDirectory, IPAddresses: []string{"192.0.2.999"}}},
		{name: "unspecified IP", options: Options{Directory: validDirectory, IPAddresses: []string{"0.0.0.0"}}},
		{name: "IP whitespace", options: Options{Directory: validDirectory, IPAddresses: []string{" 192.0.2.10"}}},
		{name: "zoned IP", options: Options{Directory: validDirectory, IPAddresses: []string{"fe80::1%en0"}}},
		{name: "negative validity", options: Options{Directory: validDirectory, DNSNames: []string{"nas.example"}, Validity: -time.Second}},
		{name: "validity below minimum", options: Options{Directory: validDirectory, DNSNames: []string{"nas.example"}, Validity: MinimumValidity - time.Nanosecond}},
		{name: "validity above maximum", options: Options{Directory: validDirectory, DNSNames: []string{"nas.example"}, Validity: MaximumValidity + time.Nanosecond}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Generate(test.options)
			if err == nil {
				t.Fatal("Generate() error = nil, want rejection")
			}
		})
	}
}

func TestGenerateRejectsUnsafeExistingPaths(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "wide directory",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o755)
			},
		},
		{
			name: "directory symlink",
			setup: func(t *testing.T, directory string) {
				target := filepath.Join(filepath.Dir(directory), "real")
				mustMkdir(t, target, 0o700)
				if err := os.Symlink(target, directory); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
			},
		},
		{
			name: "wide certificate",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o700)
				mustWriteFile(t, filepath.Join(directory, CertificateFilename), 0o644, []byte("certificate"))
				mustWriteFile(t, filepath.Join(directory, PrivateKeyFilename), 0o600, []byte("key"))
			},
		},
		{
			name: "certificate symlink",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o700)
				target := filepath.Join(filepath.Dir(directory), "target")
				mustWriteFile(t, target, 0o600, []byte("do not replace"))
				if err := os.Symlink(target, filepath.Join(directory, CertificateFilename)); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
			},
		},
		{
			name: "non-regular certificate",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o700)
				mustMkdir(t, filepath.Join(directory, CertificateFilename), 0o700)
			},
		},
		{
			name: "only certificate exists",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o700)
				mustWriteFile(t, filepath.Join(directory, CertificateFilename), 0o600, []byte("certificate"))
			},
		},
		{
			name: "wide lock",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o700)
				mustWriteFile(t, filepath.Join(directory, LockFilename), 0o644, nil)
			},
		},
		{
			name: "lock symlink",
			setup: func(t *testing.T, directory string) {
				mustMkdir(t, directory, 0o700)
				target := filepath.Join(filepath.Dir(directory), "lock-target")
				mustWriteFile(t, target, 0o600, nil)
				if err := os.Symlink(target, filepath.Join(directory, LockFilename)); err != nil {
					t.Fatalf("Symlink() error = %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "certificate")
			test.setup(t, directory)
			if _, err := Generate(Options{Directory: directory, DNSNames: []string{"nas.example"}}); err == nil {
				t.Fatal("Generate() error = nil, want unsafe path rejection")
			}
		})
	}
}

func TestGenerateWriteFailurePreservesExistingPair(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "certificate")
	options := Options{Directory: directory, DNSNames: []string{"nas.example"}}
	if _, err := Generate(options); err != nil {
		t.Fatalf("initial Generate() error = %v", err)
	}
	oldCertificate := mustReadFile(t, filepath.Join(directory, CertificateFilename))
	oldPrivateKey := mustReadFile(t, filepath.Join(directory, PrivateKeyFilename))

	deps := productionDependencies()
	realWriteTemp := deps.writeTemp
	writes := 0
	deps.writeTemp = func(directory, pattern string, body []byte) (string, error) {
		writes++
		// New certificate, new key and certificate rollback have all been
		// durably prepared. Fail before installation when preparing the last
		// rollback file.
		if writes == 4 {
			return "", errors.New("injected private key rollback write failure")
		}
		return realWriteTemp(directory, pattern, body)
	}
	if _, err := generate(options, deps); err == nil {
		t.Fatal("generate() error = nil, want injected write failure")
	}
	assertUnchangedPair(t, directory, oldCertificate, oldPrivateKey)
	assertNoTemporaryFiles(t, directory)
}

func TestGenerateInstallFailureRollsBackExistingPair(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "certificate")
	options := Options{Directory: directory, IPAddresses: []string{"127.0.0.1"}}
	if _, err := Generate(options); err != nil {
		t.Fatalf("initial Generate() error = %v", err)
	}
	oldCertificate := mustReadFile(t, filepath.Join(directory, CertificateFilename))
	oldPrivateKey := mustReadFile(t, filepath.Join(directory, PrivateKeyFilename))

	deps := productionDependencies()
	realRename := deps.rename
	renames := 0
	deps.rename = func(oldPath, newPath string) error {
		renames++
		if renames == 2 {
			return errors.New("injected certificate rename failure")
		}
		return realRename(oldPath, newPath)
	}
	if _, err := generate(options, deps); err == nil {
		t.Fatal("generate() error = nil, want injected install failure")
	}
	assertUnchangedPair(t, directory, oldCertificate, oldPrivateKey)
	assertNoTemporaryFiles(t, directory)
}

func TestConcurrentProcessesLeaveMatchingPair(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess contention test in short mode")
	}
	directory := filepath.Join(t.TempDir(), "certificate")
	const processCount = 8
	type runningCommand struct {
		command *exec.Cmd
		output  *bytes.Buffer
	}
	commands := make([]runningCommand, 0, processCount)
	for index := 0; index < processCount; index++ {
		command := exec.Command(os.Args[0], "-test.run=^TestGenerateProcessHelper$")
		command.Env = append(os.Environ(),
			"ANAS_TEMP_CONSOLE_CERT_HELPER=1",
			"ANAS_TEMP_CONSOLE_CERT_DIRECTORY="+directory,
		)
		output := new(bytes.Buffer)
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			t.Fatalf("start helper process %d: %v", index, err)
		}
		commands = append(commands, runningCommand{command: command, output: output})
	}
	for index, running := range commands {
		if err := running.command.Wait(); err != nil {
			t.Fatalf("helper process %d failed: %v\n%s", index, err, running.output.Bytes())
		}
	}
	readPair(t, filepath.Join(directory, CertificateFilename), filepath.Join(directory, PrivateKeyFilename))
	assertNoTemporaryFiles(t, directory)
}

func TestGenerateProcessHelper(t *testing.T) {
	if os.Getenv("ANAS_TEMP_CONSOLE_CERT_HELPER") != "1" {
		return
	}
	directory := os.Getenv("ANAS_TEMP_CONSOLE_CERT_DIRECTORY")
	if _, err := Generate(Options{Directory: directory, DNSNames: []string{"nas.example"}}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestConcurrentGoroutinesLeaveMatchingPair(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "certificate")
	const goroutineCount = 16
	errorsByGenerator := make(chan error, goroutineCount)
	var generators sync.WaitGroup
	for index := 0; index < goroutineCount; index++ {
		generators.Add(1)
		go func() {
			defer generators.Done()
			_, err := Generate(Options{Directory: directory, IPAddresses: []string{"127.0.0.1"}})
			errorsByGenerator <- err
		}()
	}
	generators.Wait()
	close(errorsByGenerator)
	for err := range errorsByGenerator {
		if err != nil {
			t.Errorf("Generate() error = %v", err)
		}
	}
	readPair(t, filepath.Join(directory, CertificateFilename), filepath.Join(directory, PrivateKeyFilename))
	assertNoTemporaryFiles(t, directory)
}

func readPair(t *testing.T, certificatePath, privateKeyPath string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte) {
	t.Helper()
	certificatePEM := mustReadFile(t, certificatePath)
	privateKeyPEM := mustReadFile(t, privateKeyPath)
	if _, err := tls.X509KeyPair(certificatePEM, privateKeyPEM); err != nil {
		t.Fatalf("tls.X509KeyPair() error = %v", err)
	}
	certificateBlock, rest := pem.Decode(certificatePEM)
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("certificate file does not contain exactly one CERTIFICATE PEM block")
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		t.Fatalf("x509.ParseCertificate() error = %v", err)
	}
	privateKeyBlock, rest := pem.Decode(privateKeyPEM)
	if privateKeyBlock == nil || privateKeyBlock.Type != "PRIVATE KEY" || len(rest) != 0 {
		t.Fatal("private key file does not contain exactly one PRIVATE KEY PEM block")
	}
	parsedPrivateKey, err := x509.ParsePKCS8PrivateKey(privateKeyBlock.Bytes)
	if err != nil {
		t.Fatalf("x509.ParsePKCS8PrivateKey() error = %v", err)
	}
	privateKey, ok := parsedPrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("private key type = %T, want *ecdsa.PrivateKey", parsedPrivateKey)
	}
	return certificate, privateKey, certificatePEM, privateKeyPEM
}

func assertUnchangedPair(t *testing.T, directory string, wantCertificate, wantPrivateKey []byte) {
	t.Helper()
	certificatePath := filepath.Join(directory, CertificateFilename)
	privateKeyPath := filepath.Join(directory, PrivateKeyFilename)
	if got := mustReadFile(t, certificatePath); !bytes.Equal(got, wantCertificate) {
		t.Error("certificate changed after failed generation")
	}
	if got := mustReadFile(t, privateKeyPath); !bytes.Equal(got, wantPrivateKey) {
		t.Error("private key changed after failed generation")
	}
	readPair(t, certificatePath, privateKeyPath)
}

func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", directory, err)
	}
	allowed := map[string]bool{
		CertificateFilename: true,
		PrivateKeyFilename:  true,
		LockFilename:        true,
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			t.Errorf("unexpected file left after generation: %s", entry.Name())
		}
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", path, err)
	}
	got := info.Mode() & (os.ModeType | os.ModePerm)
	if got != want {
		t.Errorf("mode of %s = %s, want %s", path, got, want)
	}
}

func assertFingerprintFormat(t *testing.T, value string) {
	t.Helper()
	parts := strings.Split(value, ":")
	if len(parts) != sha256.Size {
		t.Fatalf("fingerprint has %d octets, want %d: %q", len(parts), sha256.Size, value)
	}
	for _, part := range parts {
		if len(part) != 2 || strings.ToUpper(part) != part || strings.ContainsAny(part, "GHIJKLMNOPQRSTUVWXYZ") {
			t.Fatalf("fingerprint is not uppercase colon-separated hexadecimal: %q", value)
		}
		for _, character := range part {
			if !(character >= '0' && character <= '9' || character >= 'A' && character <= 'F') {
				t.Fatalf("fingerprint is not uppercase colon-separated hexadecimal: %q", value)
			}
		}
	}
}

func testFingerprint(body []byte) string {
	digest := sha256.Sum256(body)
	parts := make([]string, len(digest))
	for index, value := range digest {
		parts[index] = fmt.Sprintf("%02X", value)
	}
	return strings.Join(parts, ":")
}

func ipStrings(addresses []net.IP) []string {
	result := make([]string, len(addresses))
	for index, address := range addresses {
		result[index] = address.String()
	}
	sort.Strings(result)
	return result
}

func mustMkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatalf("Mkdir(%q) error = %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, mode os.FileMode, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return body
}
