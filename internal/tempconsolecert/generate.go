// Package tempconsolecert creates the explicitly requested, disposable leaf
// certificate used to protect the initial console bootstrap channel.
package tempconsolecert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	CertificateFilename = "temp-console.crt"
	PrivateKeyFilename  = "temp-console.key"
	LockFilename        = ".temp-console-cert.lock"

	DefaultValidity = 7 * 24 * time.Hour
	MinimumValidity = time.Hour
	MaximumValidity = 30 * 24 * time.Hour
	clockSkew       = 5 * time.Minute
)

// Options contains the complete input to Generate. Generate never supplements
// these values from the process environment or from host name discovery.
type Options struct {
	// Directory is the absolute path of the private 0700 installation
	// directory. Existing certificate, key and lock files must be regular,
	// non-symlink files with mode 0600.
	Directory string
	// DNSNames and IPAddresses are the only values placed in the certificate
	// SAN extension. DNS names are normalized to lowercase; IP addresses are
	// returned in canonical form.
	DNSNames    []string
	IPAddresses []string
	// Validity defaults to DefaultValidity and cannot exceed MaximumValidity.
	Validity time.Duration
}

// Result describes the installed certificate without returning private key
// material. Fingerprints use the conventional uppercase, colon-separated
// SHA-256 representation printed by certificate tools.
type Result struct {
	CertificatePath              string
	PrivateKeyPath               string
	CertificateSHA256Fingerprint string
	SPKISHA256Fingerprint        string
	NotBefore                    time.Time
	NotAfter                     time.Time
	DNSNames                     []string
	IPAddresses                  []string
}

type normalizedOptions struct {
	directory   string
	dnsNames    []string
	ipAddresses []net.IP
	ipStrings   []string
	validity    time.Duration
}

type dependencies struct {
	random        io.Reader
	now           func() time.Time
	writeTemp     func(string, string, []byte) (string, error)
	rename        func(string, string) error
	remove        func(string) error
	readFile      func(string) ([]byte, error)
	syncDirectory func(string) error
}

var generationMu sync.Mutex

// Generate creates a new ECDSA P-256 key and a self-signed, non-CA ServerAuth
// leaf certificate, then transactionally replaces the fixed pair in Directory.
func Generate(options Options) (Result, error) {
	return generate(options, productionDependencies())
}

func productionDependencies() dependencies {
	return dependencies{
		random:        rand.Reader,
		now:           time.Now,
		writeTemp:     writeTemporaryFile,
		rename:        renameFile,
		remove:        removeFile,
		readFile:      readFile,
		syncDirectory: syncDirectory,
	}
}

func generate(options Options, deps dependencies) (Result, error) {
	normalized, err := normalizeOptions(options)
	if err != nil {
		return Result{}, err
	}

	// flock supplies the cross-process boundary. The mutex also makes the
	// guarantee explicit for goroutines on platforms whose flock ownership
	// semantics are tied to open file descriptions.
	generationMu.Lock()
	defer generationMu.Unlock()

	if err := ensureSecureDirectory(normalized.directory); err != nil {
		return Result{}, fmt.Errorf("prepare temporary console certificate directory: %w", err)
	}
	lock, err := openSecureLockFile(filepath.Join(normalized.directory, LockFilename))
	if err != nil {
		return Result{}, fmt.Errorf("open certificate generation lock: %w", err)
	}
	defer lock.Close()
	if err := lockExclusive(lock); err != nil {
		return Result{}, fmt.Errorf("lock certificate generation: %w", err)
	}
	defer unlockFile(lock)

	certificatePath := filepath.Join(normalized.directory, CertificateFilename)
	privateKeyPath := filepath.Join(normalized.directory, PrivateKeyFilename)
	previous, err := readExistingPair(certificatePath, privateKeyPath, deps.readFile)
	if err != nil {
		return Result{}, err
	}
	if previous.exists {
		defer clear(previous.privateKey)
	}

	certificate, certificatePEM, privateKeyPEM, err := createLeaf(normalized, deps)
	if err != nil {
		return Result{}, err
	}
	defer clear(privateKeyPEM)

	certificateTemp, err := deps.writeTemp(normalized.directory, ".temp-console-cert-*", certificatePEM)
	if err != nil {
		return Result{}, fmt.Errorf("write temporary certificate: %w", err)
	}
	defer deps.remove(certificateTemp)
	privateKeyTemp, err := deps.writeTemp(normalized.directory, ".temp-console-key-*", privateKeyPEM)
	if err != nil {
		return Result{}, fmt.Errorf("write temporary private key: %w", err)
	}
	defer deps.remove(privateKeyTemp)

	if err := installPair(pairInstall{
		directory:       normalized.directory,
		certificatePath: certificatePath,
		privateKeyPath:  privateKeyPath,
		certificateTemp: certificateTemp,
		privateKeyTemp:  privateKeyTemp,
		previous:        previous,
	}, deps); err != nil {
		return Result{}, fmt.Errorf("install temporary certificate pair: %w", err)
	}

	return Result{
		CertificatePath:              certificatePath,
		PrivateKeyPath:               privateKeyPath,
		CertificateSHA256Fingerprint: fingerprint(certificate.Raw),
		SPKISHA256Fingerprint:        fingerprint(certificate.RawSubjectPublicKeyInfo),
		NotBefore:                    certificate.NotBefore,
		NotAfter:                     certificate.NotAfter,
		DNSNames:                     append([]string(nil), normalized.dnsNames...),
		IPAddresses:                  append([]string(nil), normalized.ipStrings...),
	}, nil
}

func createLeaf(options normalizedOptions, deps dependencies) (*x509.Certificate, []byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), deps.random)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate ECDSA private key: %w", err)
	}

	serialBytes := make([]byte, 20)
	if _, err := io.ReadFull(deps.random, serialBytes); err != nil {
		return nil, nil, nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	serialBytes[0] &= 0x7f
	serial := new(big.Int).SetBytes(serialBytes)
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}

	now := deps.now().UTC().Truncate(time.Second)
	notBefore := now.Add(-clockSkew)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "ANAS temporary console",
			Organization: []string{"ANAS"},
		},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(options.validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              append([]string(nil), options.dnsNames...),
		IPAddresses:           cloneIPs(options.ipAddresses),
	}
	certificateDER, err := x509.CreateCertificate(deps.random, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create self-signed leaf certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse generated certificate: %w", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal private key: %w", err)
	}
	defer clear(privateKeyDER)
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	if len(certificatePEM) == 0 || len(privateKeyPEM) == 0 {
		return nil, nil, nil, fmt.Errorf("encode generated certificate pair")
	}
	return certificate, certificatePEM, privateKeyPEM, nil
}

func normalizeOptions(options Options) (normalizedOptions, error) {
	if options.Directory == "" {
		return normalizedOptions{}, fmt.Errorf("temporary certificate directory is required")
	}
	if !filepath.IsAbs(options.Directory) {
		return normalizedOptions{}, fmt.Errorf("temporary certificate directory must be absolute")
	}
	directory := filepath.Clean(options.Directory)

	validity := options.Validity
	if validity == 0 {
		validity = DefaultValidity
	}
	if validity < MinimumValidity {
		return normalizedOptions{}, fmt.Errorf("certificate validity %s is below minimum %s", validity, MinimumValidity)
	}
	if validity > MaximumValidity {
		return normalizedOptions{}, fmt.Errorf("certificate validity %s exceeds maximum %s", validity, MaximumValidity)
	}

	dnsNames, err := normalizeDNSNames(options.DNSNames)
	if err != nil {
		return normalizedOptions{}, err
	}
	ipAddresses, ipStrings, err := normalizeIPAddresses(options.IPAddresses)
	if err != nil {
		return normalizedOptions{}, err
	}
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return normalizedOptions{}, fmt.Errorf("at least one explicit DNS or IP SAN is required")
	}
	return normalizedOptions{
		directory:   directory,
		dnsNames:    dnsNames,
		ipAddresses: ipAddresses,
		ipStrings:   ipStrings,
		validity:    validity,
	}, nil
}

func normalizeDNSNames(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, original := range values {
		if original == "" || strings.TrimSpace(original) != original {
			return nil, fmt.Errorf("invalid DNS SAN %q", original)
		}
		value := strings.ToLower(original)
		if err := validateDNSName(value); err != nil {
			return nil, fmt.Errorf("invalid DNS SAN %q: %w", original, err)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validateDNSName(value string) error {
	if len(value) > 253 {
		return fmt.Errorf("name is longer than 253 bytes")
	}
	if strings.HasSuffix(value, ".") {
		return fmt.Errorf("absolute trailing dot is not allowed")
	}
	if net.ParseIP(value) != nil {
		return fmt.Errorf("IP literals must use an IP SAN")
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("each label must contain 1 to 63 bytes")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("labels cannot begin or end with '-'")
		}
		for _, char := range label {
			if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
				continue
			}
			return fmt.Errorf("labels must use ASCII letters, digits or '-'")
		}
	}
	return nil
}

func normalizeIPAddresses(values []string) ([]net.IP, []string, error) {
	seen := make(map[string]struct{}, len(values))
	addresses := make([]net.IP, 0, len(values))
	stringsResult := make([]string, 0, len(values))
	for _, original := range values {
		if original == "" || strings.TrimSpace(original) != original || strings.Contains(original, "%") {
			return nil, nil, fmt.Errorf("invalid IP SAN %q", original)
		}
		parsed := net.ParseIP(original)
		if parsed == nil {
			return nil, nil, fmt.Errorf("invalid IP SAN %q", original)
		}
		if parsed.IsUnspecified() {
			return nil, nil, fmt.Errorf("invalid IP SAN %q: address must be concrete", original)
		}
		if ipv4 := parsed.To4(); ipv4 != nil {
			parsed = append(net.IP(nil), ipv4...)
		} else {
			parsed = append(net.IP(nil), parsed.To16()...)
		}
		canonical := parsed.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		addresses = append(addresses, parsed)
		stringsResult = append(stringsResult, canonical)
	}
	sort.Slice(addresses, func(left, right int) bool { return addresses[left].String() < addresses[right].String() })
	sort.Strings(stringsResult)
	return addresses, stringsResult, nil
}

func cloneIPs(values []net.IP) []net.IP {
	result := make([]net.IP, len(values))
	for index, value := range values {
		result[index] = append(net.IP(nil), value...)
	}
	return result
}

func fingerprint(body []byte) string {
	digest := sha256.Sum256(body)
	var result strings.Builder
	result.Grow(len(digest)*3 - 1)
	for index, value := range digest {
		if index != 0 {
			result.WriteByte(':')
		}
		fmt.Fprintf(&result, "%02X", value)
	}
	return result.String()
}
