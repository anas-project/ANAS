package consoletls

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	maximumArtifactBytes    = 1 << 20
	maximumTrustBundleBytes = 16 << 20
	maximumIssuerMarkBytes  = 32
)

type artifact struct {
	path string
	role FileRole
	info fs.FileInfo
	body []byte
}

// DefaultFileSecurityCheck rejects permission bits that make private keys
// readable outside their owner or make public certificate material writable
// outside its owner. Execute bits are never meaningful for these files. The
// check is skipped on Windows, where FileMode permission bits do not model an
// ACL; callers can inject an ACL-aware check there.
func DefaultFileSecurityCheck(path string, role FileRole, info fs.FileInfo) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	permissions := info.Mode().Perm()
	if permissions&0o111 != 0 {
		return fmt.Errorf("%s %s must not be executable (mode %04o)", role, path, permissions)
	}
	if role == FileRolePrivateKey {
		if permissions&0o077 != 0 {
			return fmt.Errorf("private key %s must not be accessible by group or others (mode %04o)", path, permissions)
		}
		return nil
	}
	if permissions&0o022 != 0 {
		return fmt.Errorf("%s %s must not be writable by group or others (mode %04o)", role, path, permissions)
	}
	return nil
}

func validateCandidateConfig(candidate Candidate) error {
	for _, item := range []struct {
		name string
		path string
	}{
		{"certificate", candidate.CertificatePath},
		{"private key", candidate.PrivateKeyPath},
		{"issuer", candidate.IssuerPath},
		{"trust bundle", candidate.TrustBundlePath},
	} {
		if err := validateArtifactPath(item.name, item.path); err != nil {
			return err
		}
	}
	if candidate.IssuerMarkerPath != "" {
		if err := validateArtifactPath("issuer marker", candidate.IssuerMarkerPath); err != nil {
			return err
		}
		if candidate.Source != "" && candidate.Source != SourceInternal && candidate.Source != SourceACME {
			return fmt.Errorf("issuer marker can only be pinned to internal or acme source")
		}
	} else if candidate.Source != SourceInternal && candidate.Source != SourceACME && candidate.Source != SourceTemporary {
		return fmt.Errorf("source must be internal, acme, or temporary when no issuer marker is configured")
	}
	_, _, err := candidateIdentities(candidate)
	return err
}

func validateArtifactPath(name, path string) error {
	if path == "" {
		return fmt.Errorf("%s path is required", name)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s path must be absolute", name)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("%s path must be clean", name)
	}
	return nil
}

func canonicalBaseDomain(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.HasSuffix(value, ".") || strings.Contains(value, "*") {
		return "", fmt.Errorf("base domain must be a non-empty DNS name without whitespace, wildcard, or trailing dot")
	}
	value = strings.ToLower(value)
	if len(value) > 248 { // leaves room for the required "anas." label
		return "", fmt.Errorf("base domain is too long")
	}
	if address, err := netip.ParseAddr(value); err == nil && address.IsValid() || looksLikeIPv4Name(value) {
		return "", fmt.Errorf("base domain must be a DNS name, not an IP address")
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("base domain contains an invalid DNS label")
		}
		for _, character := range label {
			if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
				continue
			}
			return "", fmt.Errorf("base domain contains a non-ASCII DNS character")
		}
	}
	return value, nil
}

func candidateIdentities(candidate Candidate) ([]string, []string, error) {
	dnsNames := make([]string, 0, len(candidate.RequiredDNSNames)+2)
	seenDNS := map[string]bool{}
	addDNS := func(value string) error {
		name, err := canonicalBaseDomain(value)
		if err != nil {
			return err
		}
		if !seenDNS[name] {
			seenDNS[name] = true
			dnsNames = append(dnsNames, name)
		}
		return nil
	}
	if candidate.BaseDomain != "" {
		baseDomain, err := canonicalBaseDomain(candidate.BaseDomain)
		if err != nil {
			return nil, nil, err
		}
		if err := addDNS(baseDomain); err != nil {
			return nil, nil, err
		}
		if err := addDNS("anas." + baseDomain); err != nil {
			return nil, nil, err
		}
	}
	for _, value := range candidate.RequiredDNSNames {
		if err := addDNS(value); err != nil {
			return nil, nil, fmt.Errorf("required DNS name %q: %w", value, err)
		}
	}

	ipAddresses := make([]string, 0, len(candidate.RequiredIPAddresses))
	seenIP := map[string]bool{}
	for _, value := range candidate.RequiredIPAddresses {
		if value == "" || value != strings.TrimSpace(value) {
			return nil, nil, fmt.Errorf("required IP address must not be empty or contain whitespace")
		}
		address, err := netip.ParseAddr(value)
		if err != nil || !address.IsValid() || address.Zone() != "" {
			return nil, nil, fmt.Errorf("required IP address %q is invalid", value)
		}
		address = address.Unmap()
		canonical := address.String()
		if !seenIP[canonical] {
			seenIP[canonical] = true
			ipAddresses = append(ipAddresses, canonical)
		}
	}
	if len(dnsNames) == 0 && len(ipAddresses) == 0 {
		return nil, nil, fmt.Errorf("at least one required DNS name or IP address must be configured")
	}
	return dnsNames, ipAddresses, nil
}

func looksLikeIPv4Name(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
		number, err := strconv.Atoi(part)
		if err != nil || number > 255 {
			return false
		}
	}
	return true
}

func loadCandidate(candidate Candidate, check FileSecurityCheck, now time.Time) (*Snapshot, error) {
	requiredDNSNames, requiredIPAddresses, err := candidateIdentities(candidate)
	if err != nil {
		return nil, err
	}
	artifacts := make([]artifact, 0, 5)
	read := func(path string, role FileRole, maximum int64) ([]byte, error) {
		item, err := readArtifact(path, role, maximum, check)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, item)
		return item.body, nil
	}

	certificatePEM, err := read(candidate.CertificatePath, FileRoleCertificate, maximumArtifactBytes)
	if err != nil {
		return nil, err
	}
	privateKeyPEM, err := read(candidate.PrivateKeyPath, FileRolePrivateKey, maximumArtifactBytes)
	if err != nil {
		return nil, err
	}
	issuerPEM, err := read(candidate.IssuerPath, FileRoleIssuer, maximumArtifactBytes)
	if err != nil {
		return nil, err
	}
	trustBundlePEM, err := read(candidate.TrustBundlePath, FileRoleTrustBundle, maximumTrustBundleBytes)
	if err != nil {
		return nil, err
	}

	source := candidate.Source
	if candidate.IssuerMarkerPath != "" {
		marker, err := read(candidate.IssuerMarkerPath, FileRoleIssuerMark, maximumIssuerMarkBytes)
		if err != nil {
			return nil, err
		}
		source, err = parseIssuerMarker(marker)
		if err != nil {
			return nil, err
		}
		if candidate.Source != "" && candidate.Source != source {
			return nil, fmt.Errorf("issuer marker does not match configured source")
		}
	}

	certificateFileCertificates, err := parseCertificatePEM(certificatePEM, FileRoleCertificate)
	if err != nil {
		return nil, err
	}
	if err := validatePrivateKeyPEM(privateKeyPEM); err != nil {
		return nil, err
	}
	issuerCertificates, err := parseCertificatePEM(issuerPEM, FileRoleIssuer)
	if err != nil {
		return nil, err
	}
	trustCertificates, err := parseCertificatePEM(trustBundlePEM, FileRoleTrustBundle)
	if err != nil {
		return nil, err
	}

	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("certificate/private key pair: %w", err)
	}
	leaf := certificateFileCertificates[0]
	pair.Leaf = leaf
	if leaf.IsCA {
		return nil, fmt.Errorf("serving leaf certificate must not be a CA")
	}
	now = now.UTC()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("serving leaf certificate is not valid at the current time")
	}
	if candidate.BaseDomain != "" {
		baseDomain, _ := canonicalBaseDomain(candidate.BaseDomain)
		if err := leaf.VerifyHostname(baseDomain); err != nil {
			return nil, fmt.Errorf("serving leaf does not cover base domain %s: %w", baseDomain, err)
		}
		consoleName := "anas." + baseDomain
		if err := leaf.VerifyHostname(consoleName); err != nil {
			return nil, fmt.Errorf("serving leaf does not cover console name %s: %w", consoleName, err)
		}
	}
	for _, name := range requiredDNSNames {
		if err := leaf.VerifyHostname(name); err != nil {
			return nil, fmt.Errorf("serving leaf does not cover required DNS name %s: %w", name, err)
		}
	}
	for _, address := range requiredIPAddresses {
		if err := leaf.VerifyHostname(address); err != nil {
			return nil, fmt.Errorf("serving leaf does not cover required IP address %s: %w", address, err)
		}
	}

	roots := x509.NewCertPool()
	for _, certificate := range trustCertificates {
		roots.AddCert(certificate)
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range certificateFileCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	for _, certificate := range issuerCertificates {
		intermediates.AddCert(certificate)
	}
	var verifyIdentity string
	if len(requiredDNSNames) > 0 {
		verifyIdentity = requiredDNSNames[0]
	} else {
		verifyIdentity = requiredIPAddresses[0]
	}
	chains, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       verifyIdentity,
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return nil, fmt.Errorf("verify serving certificate chain: %w", err)
	}
	chain, err := selectServingChain(chains, certificateFileCertificates, issuerCertificates, source)
	if err != nil {
		return nil, err
	}
	if err := verifyArtifactsStable(artifacts, check); err != nil {
		return nil, err
	}

	serviceLength := len(chain)
	if serviceLength > 1 {
		serviceLength-- // a server does not send the configured trust anchor
	}
	serviceChain := make([][]byte, serviceLength)
	for index := 0; index < serviceLength; index++ {
		serviceChain[index] = append([]byte{}, chain[index].Raw...)
	}
	pair.Certificate = cloneByteSlices(serviceChain)
	pair.Leaf = leaf
	spki := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return &Snapshot{
		source:      source,
		spkiSHA256:  spki,
		leafDER:     append([]byte{}, leaf.Raw...),
		chainDER:    cloneByteSlices(serviceChain),
		dnsNames:    append([]string{}, leaf.DNSNames...),
		notBefore:   leaf.NotBefore,
		notAfter:    leaf.NotAfter,
		certificate: pair,
	}, nil
}

func readArtifact(path string, role FileRole, maximum int64, check FileSecurityCheck) (artifact, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return artifact{}, fmt.Errorf("read %s %s: %w", role, path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return artifact{}, fmt.Errorf("read %s %s: symbolic links are not allowed", role, path)
	}
	if !pathInfo.Mode().IsRegular() {
		return artifact{}, fmt.Errorf("read %s %s: artifact is not a regular file", role, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return artifact{}, fmt.Errorf("read %s %s: %w", role, path, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return artifact{}, fmt.Errorf("inspect %s %s: %w", role, path, err)
	}
	if !sameFileVersion(pathInfo, openedInfo) || !openedInfo.Mode().IsRegular() {
		return artifact{}, fmt.Errorf("read %s %s: artifact changed while it was opened", role, path)
	}
	if err := check(path, role, openedInfo); err != nil {
		return artifact{}, err
	}
	body, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return artifact{}, fmt.Errorf("read %s %s: %w", role, path, err)
	}
	if int64(len(body)) > maximum {
		return artifact{}, fmt.Errorf("read %s %s: artifact exceeds %d bytes", role, path, maximum)
	}
	if len(body) == 0 {
		return artifact{}, fmt.Errorf("read %s %s: artifact is empty", role, path)
	}
	afterRead, err := file.Stat()
	if err != nil || !sameFileVersion(openedInfo, afterRead) {
		return artifact{}, fmt.Errorf("read %s %s: artifact changed during read", role, path)
	}
	return artifact{path: path, role: role, info: openedInfo, body: body}, nil
}

func verifyArtifactsStable(artifacts []artifact, check FileSecurityCheck) error {
	for _, item := range artifacts {
		current, err := os.Lstat(item.path)
		if err != nil || !sameFileVersion(item.info, current) || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
			return fmt.Errorf("validate %s %s: artifact changed while candidate was validated", item.role, item.path)
		}
		if err := check(item.path, item.role, current); err != nil {
			return err
		}
	}
	return nil
}

func sameFileVersion(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Size() == right.Size() && left.Mode() == right.Mode() && left.ModTime().Equal(right.ModTime())
}

func parseIssuerMarker(body []byte) (Source, error) {
	switch string(body) {
	case "internal", "internal\n":
		return SourceInternal, nil
	case "acme", "acme\n":
		return SourceACME, nil
	default:
		return "", fmt.Errorf("issuer marker must contain exactly internal or acme")
	}
}

func parseCertificatePEM(body []byte, role FileRole) ([]*x509.Certificate, error) {
	remaining := body
	certificates := []*x509.Certificate{}
	seen := map[[sha256.Size]byte]bool{}
	for len(remaining) > 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, fmt.Errorf("%s PEM contains leading or trailing non-certificate data", role)
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("%s PEM contains an invalid certificate block", role)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse %s certificate: %w", role, err)
		}
		digest := sha256.Sum256(certificate.Raw)
		if seen[digest] && role != FileRoleTrustBundle {
			return nil, fmt.Errorf("%s PEM contains a duplicate certificate", role)
		}
		seen[digest] = true
		certificates = append(certificates, certificate)
		remaining = rest
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("%s PEM contains no certificates", role)
	}
	return certificates, nil
}

func validatePrivateKeyPEM(body []byte) error {
	if !bytes.HasPrefix(body, []byte("-----BEGIN ")) {
		return fmt.Errorf("private key PEM contains leading data")
	}
	block, rest := pem.Decode(body)
	if block == nil || len(block.Headers) != 0 {
		return fmt.Errorf("private key PEM is invalid")
	}
	switch block.Type {
	case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
	default:
		return fmt.Errorf("private key PEM uses unsupported block type")
	}
	if len(rest) != 0 {
		return fmt.Errorf("private key PEM contains trailing data or multiple keys")
	}
	return nil
}

func selectServingChain(chains [][]*x509.Certificate, certificateFile, issuerFile []*x509.Certificate, source Source) ([]*x509.Certificate, error) {
	for _, chain := range chains {
		if !issuerParticipates(chain, issuerFile) || !allCertificatesParticipate(chain, certificateFile[1:]) || !allCertificatesParticipate(chain, issuerFile) {
			continue
		}
		root := chain[len(chain)-1]
		switch source {
		case SourceInternal:
			if !selfSigned(root) || !containsCertificate(issuerFile, root) {
				continue
			}
		case SourceACME:
			if len(chain) < 2 || selfSigned(chain[0]) || selfSigned(root) && containsCertificate(issuerFile, root) {
				continue
			}
		case SourceTemporary:
		default:
			continue
		}
		return chain, nil
	}
	return nil, fmt.Errorf("verified chain does not match issuer file and declared source")
}

func issuerParticipates(chain, issuers []*x509.Certificate) bool {
	if len(chain) == 1 {
		return containsCertificate(issuers, chain[0])
	}
	return containsCertificate(issuers, chain[1])
}

func allCertificatesParticipate(chain, expected []*x509.Certificate) bool {
	for _, certificate := range expected {
		if !containsCertificate(chain, certificate) {
			return false
		}
	}
	return true
}

func containsCertificate(certificates []*x509.Certificate, target *x509.Certificate) bool {
	for _, certificate := range certificates {
		if bytes.Equal(certificate.Raw, target.Raw) {
			return true
		}
	}
	return false
}

func selfSigned(certificate *x509.Certificate) bool {
	return bytes.Equal(certificate.RawSubject, certificate.RawIssuer) && certificate.CheckSignatureFrom(certificate) == nil
}
