package runner

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var (
	computeSandboxPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	computeInstancePrefixPattern = regexp.MustCompile(`^anas-[a-z0-9-]{1,50}$`)
	computeFingerprintPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// computeQuota mirrors the contract's per-instance limits. The provider is what
// turns these into project-wide totals; the runner only checks that they are
// present and inside the range the schema declares.
type computeQuota struct {
	MaxInstances int
	CPU          int
	MemoryMiB    int
	DiskGiB      int
}

func computeResourcePrefix(consumer, id string) string {
	return "ANAS_COMPUTE_RESOURCE__" + defaultEnvPrefix(consumer) + "__" + defaultEnvPrefix(id) + "__"
}

// generateComputeClientCredential mints the consumer's client keypair.
//
// Unlike a database password, this credential has two halves that must stay
// together across applies: the provider registers the certificate in the Incus
// trust store, and the consumer authenticates with the matching key. Storing
// them as one base64 bundle keeps the existing one-secret-per-resource model
// intact -- regenerating either half separately would silently invalidate a
// trust entry that is already registered on the daemon.
func generateComputeClientCredential(consumer, resourceID string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "anas-" + consumer + "-" + resourceID},
		NotBefore:    time.Now().Add(-time.Hour).UTC(),
		// Long-lived on purpose: rotation is an explicit operator action that
		// re-registers the certificate, not a silent expiry that would strand a
		// running consumer mid-job.
		NotAfter:              time.Now().AddDate(10, 0, 0).UTC(),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	bundle := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...,
	)
	return base64.StdEncoding.EncodeToString(bundle), nil
}

// splitComputeCredential separates the stored bundle back into the half the
// provider needs and the half only the consumer may ever see.
func splitComputeCredential(encoded string) (certPEM, keyPEM string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", "", fmt.Errorf("compute credential is not valid base64")
	}
	rest := decoded
	for {
		block, remainder := pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE" && certPEM == "":
			certPEM = string(pem.EncodeToMemory(block))
		case strings.HasSuffix(block.Type, "PRIVATE KEY") && keyPEM == "":
			keyPEM = string(pem.EncodeToMemory(block))
		}
		rest = remainder
	}
	if certPEM == "" || keyPEM == "" {
		return "", "", fmt.Errorf("compute credential must contain one certificate and one private key")
	}
	return certPEM, keyPEM, nil
}

// computeServerFingerprint is the SHA-256 over the pinned server certificate's
// DER body, published so a consumer can pin the same daemon the provider
// verified against.
func computeServerFingerprint(serverCertB64 string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(serverCertB64))
	if err != nil {
		return "", fmt.Errorf("pinned server certificate is not valid base64")
	}
	block, _ := pem.Decode(decoded)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("pinned server certificate is not a PEM certificate")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}

// validateComputeSpec enforces the contract schema at the point where a bad
// value can still be reported against the module that wrote it.
func validateComputeSpec(consumer, id string, spec map[string]any) (computeQuota, []string, error) {
	sandbox, _ := spec["sandbox"].(string)
	if !computeSandboxPattern.MatchString(sandbox) {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute sandbox %q is invalid", consumer, id, sandbox)
	}
	prefix, _ := spec["instance_prefix"].(string)
	if !computeInstancePrefixPattern.MatchString(prefix) {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute instance_prefix %q is invalid", consumer, id, prefix)
	}
	rawQuota, ok := spec["quota"].(map[string]any)
	if !ok {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute quota is missing", consumer, id)
	}
	quota := computeQuota{}
	for _, field := range []struct {
		key      string
		target   *int
		min, max int
	}{
		{"max_instances", &quota.MaxInstances, 1, 256},
		{"cpu", &quota.CPU, 1, 64},
		{"memory_mib", &quota.MemoryMiB, 512, 262144},
		{"disk_gib", &quota.DiskGiB, 4, 2048},
	} {
		value, ok := intFromAny(rawQuota[field.key])
		if !ok || value < field.min || value > field.max {
			return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute quota.%s must be an integer between %d and %d",
				consumer, id, field.key, field.min, field.max)
		}
		*field.target = value
	}
	// image_policy is a reserved opening. The field exists so a future release
	// can widen this without inventing new vocabulary, but "any" is refused
	// here rather than silently accepted: it would make the allowlist -- the
	// one constraint the daemon does not backstop -- mean nothing.
	imagePolicy, _ := spec["image_policy"].(string)
	if imagePolicy == "" {
		imagePolicy = "pinned"
	}
	if imagePolicy == "any" {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute image_policy \"any\" is reserved and not implemented", consumer, id)
	}
	if imagePolicy != "pinned" {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute image_policy must be pinned", consumer, id)
	}
	// The allowlist is a list in the schema, but spec_from can only assign a
	// string. Accepting a comma-separated string as well is what lets a module
	// wire its own configured image parameter into the lease instead of
	// freezing the list into its manifest.
	var entries []string
	switch raw := spec["image_allowlist"].(type) {
	case []any:
		for _, entry := range raw {
			value, _ := entry.(string)
			entries = append(entries, value)
		}
	case string:
		for _, value := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				entries = append(entries, trimmed)
			}
		}
	}
	if len(entries) == 0 {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute image_allowlist must list at least one image fingerprint", consumer, id)
	}
	allowlist := make([]string, 0, len(entries))
	for _, value := range entries {
		// A tag or an alias would let whatever the remote publishes today
		// become what this lease boots, so only pinned digests are accepted.
		if !computeFingerprintPattern.MatchString(value) {
			return computeQuota{}, nil, fmt.Errorf("resource %s.%s compute image_allowlist entries must be SHA-256 fingerprints", consumer, id)
		}
		allowlist = append(allowlist, value)
	}
	policy, _ := spec["deletion_policy"].(string)
	if policy != "retain" && policy != "delete" {
		return computeQuota{}, nil, fmt.Errorf("resource %s.%s deletion_policy must be retain or delete", consumer, id)
	}
	return quota, allowlist, nil
}

func intFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}
