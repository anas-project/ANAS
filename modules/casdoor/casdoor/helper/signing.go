package main

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

type signingTrust struct {
	Certificate string `json:"certificate"`
	RetainUntil string `json:"retain_until"`
}

const (
	signingCertificatePlaceholder = "__ANAS_SIGNING_CERTIFICATE__"
	signingPrivateKeyPlaceholder  = "__ANAS_SIGNING_PRIVATE_KEY__"
)

type signingMaterial struct {
	PrivateKey          string         `json:"private_key"`
	Certificate         string         `json:"certificate"`
	TrustedCertificates []signingTrust `json:"trusted_certificates,omitempty"`
}

func parseSigningMaterial(value string) (signingMaterial, error) {
	var material signingMaterial
	if err := json.Unmarshal([]byte(value), &material); err != nil {
		return material, fmt.Errorf("invalid Casdoor signing material")
	}
	privateBlock, _ := pem.Decode([]byte(material.PrivateKey))
	certificateBlock, _ := pem.Decode([]byte(material.Certificate))
	if privateBlock == nil || privateBlock.Type != "RSA PRIVATE KEY" ||
		certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" {
		return material, fmt.Errorf("invalid Casdoor signing keypair")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
	if err != nil {
		return material, fmt.Errorf("parse Casdoor signing key: %w", err)
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		return material, fmt.Errorf("parse Casdoor signing certificate: %w", err)
	}
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || publicKey.E != privateKey.PublicKey.E || publicKey.N.Cmp(privateKey.PublicKey.N) != 0 {
		return material, fmt.Errorf("Casdoor signing certificate does not match its private key")
	}
	for index, trust := range material.TrustedCertificates {
		block, _ := pem.Decode([]byte(trust.Certificate))
		if block == nil || block.Type != "CERTIFICATE" {
			return material, fmt.Errorf("invalid Casdoor trusted certificate %d", index)
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return material, fmt.Errorf("parse Casdoor trusted certificate %d: %w", index, err)
		}
		if _, err := time.Parse(time.RFC3339, trust.RetainUntil); err != nil {
			return material, fmt.Errorf("invalid Casdoor trusted certificate %d retention deadline", index)
		}
	}
	return material, nil
}

func applySigningMaterial(doc map[string]interface{}, value string, now time.Time) error {
	material, err := parseSigningMaterial(value)
	if err != nil {
		return err
	}
	certs, ok := doc["certs"].([]interface{})
	if !ok {
		return fmt.Errorf("init data has no certificate inventory")
	}
	found := false
	currentName := managedSigningCertificateName(material.Certificate)
	if currentName == "" {
		return fmt.Errorf("managed signing certificate has no fingerprint")
	}
	for _, raw := range certs {
		certificate, ok := raw.(map[string]interface{})
		if !ok || certificate["name"] != "anas-signing" {
			continue
		}
		certificate["name"] = currentName
		certificate["certificate"] = material.Certificate
		certificate["privateKey"] = material.PrivateKey
		found = true
	}
	if !found {
		return fmt.Errorf("init data has no managed signing certificate")
	}
	seen := map[string]bool{}
	for _, trust := range material.TrustedCertificates {
		deadline, err := time.Parse(time.RFC3339, trust.RetainUntil)
		if err != nil || !deadline.After(now) || strings.TrimSpace(trust.Certificate) == "" {
			continue
		}
		fingerprint := certificateFingerprint(trust.Certificate)
		if fingerprint == "" || seen[fingerprint] || trust.Certificate == material.Certificate {
			continue
		}
		seen[fingerprint] = true
		certs = append(certs, map[string]interface{}{
			"owner": "admin", "name": managedSigningPrefix + fingerprint,
			"displayName": "ANAS signing trust overlap", "scope": "JWT", "type": "x509",
			"cryptoAlgorithm": "RS256", "bitSize": 2048, "expireInYears": 10,
			"certificate": trust.Certificate, "privateKey": "",
		})
	}
	doc["certs"] = certs
	applications, ok := doc["applications"].([]interface{})
	if !ok {
		return fmt.Errorf("init data has no application inventory")
	}
	for _, raw := range applications {
		application, ok := raw.(map[string]interface{})
		name, _ := application["name"].(string)
		if !ok || application["owner"] != managedSigningOwner || !isManagedSigningApplication(name) {
			continue
		}
		application["cert"] = currentName
	}
	return nil
}

func managedSigningCertificateName(certificate string) string {
	fingerprint := certificateFingerprint(certificate)
	if fingerprint == "" {
		return ""
	}
	return managedSigningPrefix + fingerprint
}

func isManagedSigningApplication(name string) bool {
	return name == portalApplication || strings.HasPrefix(name, "app-anas-")
}

func certificateFingerprint(certificate string) string {
	block, _ := pem.Decode([]byte(certificate))
	if block == nil || block.Type != "CERTIFICATE" {
		return ""
	}
	digest := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(digest[:6])
}
