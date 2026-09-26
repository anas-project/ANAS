package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"time"
)

const (
	runnerTrustPath     = "/etc/ssl/certs/anas-internal-ca.crt"
	maxRunnerTrustBytes = 32 << 10
)

var errRunnerTrust = errors.New("Runner trust projection must contain only bounded, current public CA certificates")

// This is public deployment trust, not a credential, workload-supplied path or
// a replacement for TLS verification. Standalone controllers without the
// Module's fixed mount retain the guest's public system roots. A present but
// unreadable/invalid mount must not silently fall back to another trust source.
func loadRunnerTrust() ([]byte, error) {
	body, err := readRunnerTrustFile(runnerTrustPath, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errRunnerTrust
	}
	return normalizeRunnerTrust(body, time.Now())
}

func normalizeRunnerTrust(body []byte, now time.Time) ([]byte, error) {
	if len(body) == 0 || len(body) > maxRunnerTrustBytes {
		return nil, errRunnerTrust
	}
	seen := map[[32]byte]bool{}
	var out []byte
	for len(bytes.TrimSpace(body)) > 0 {
		body = bytes.TrimSpace(body)
		if !bytes.HasPrefix(body, []byte("-----BEGIN CERTIFICATE-----\n")) {
			return nil, errRunnerTrust
		}
		block, rest := pem.Decode(body)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errRunnerTrust
		}
		// pem.Decode scans forward past malformed PEM. Reject such skipped
		// prefixes rather than accepting a later valid block as the whole input.
		consumed := body[:len(body)-len(rest)]
		if bytes.Count(consumed, []byte("-----BEGIN")) != 1 || bytes.Count(consumed, []byte("-----END")) != 1 {
			return nil, errRunnerTrust
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		sum := sha256.Sum256(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 ||
			now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) || seen[sum] {
			return nil, errRunnerTrust
		}
		seen[sum] = true
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
		body = rest
	}
	if len(out) == 0 || len(out) > maxRunnerTrustBytes {
		return nil, errRunnerTrust
	}
	return out, nil
}
