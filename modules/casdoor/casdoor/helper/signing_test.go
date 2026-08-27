package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestApplySigningMaterialUsesFingerprintNamesAndRetainsActiveTrust(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	current := testSigningMaterial(t, "current", now)
	old := testSigningMaterial(t, "old", now.Add(-time.Hour))
	expired := testSigningMaterial(t, "expired", now.Add(-2*time.Hour))
	current.TrustedCertificates = []signingTrust{
		{Certificate: old.Certificate, RetainUntil: now.Add(time.Hour).Format(time.RFC3339)},
		{Certificate: expired.Certificate, RetainUntil: now.Add(-time.Second).Format(time.RFC3339)},
	}
	body, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]interface{}{
		"certs": []interface{}{map[string]interface{}{
			"owner": managedSigningOwner, "name": legacySigningName,
			"certificate": signingCertificatePlaceholder, "privateKey": signingPrivateKeyPlaceholder,
		}},
		"applications": []interface{}{
			map[string]interface{}{"owner": managedSigningOwner, "name": portalApplication, "cert": legacySigningName},
			map[string]interface{}{"owner": managedSigningOwner, "name": "app-anas-nextcloud", "cert": legacySigningName},
			map[string]interface{}{"owner": managedSigningOwner, "name": "foreign", "cert": "foreign-cert"},
		},
	}
	if err := applySigningMaterial(doc, string(body), now); err != nil {
		t.Fatal(err)
	}
	certs := doc["certs"].([]interface{})
	if len(certs) != 2 {
		t.Fatalf("certificate inventory = %#v", certs)
	}
	currentName := managedSigningCertificateName(current.Certificate)
	trustedName := managedSigningCertificateName(old.Certificate)
	seen := map[string]map[string]interface{}{}
	for _, raw := range certs {
		cert := raw.(map[string]interface{})
		seen[cert["name"].(string)] = cert
	}
	if seen[currentName]["privateKey"] != current.PrivateKey || seen[currentName]["certificate"] != current.Certificate {
		t.Fatalf("current certificate = %#v", seen[currentName])
	}
	if seen[trustedName]["privateKey"] != "" || seen[trustedName]["certificate"] != old.Certificate {
		t.Fatalf("trusted certificate = %#v", seen[trustedName])
	}
	applications := doc["applications"].([]interface{})
	for index := 0; index < 2; index++ {
		if applications[index].(map[string]interface{})["cert"] != currentName {
			t.Fatalf("managed application %d did not advance: %#v", index, applications[index])
		}
	}
	if applications[2].(map[string]interface{})["cert"] != "foreign-cert" {
		t.Fatal("unmanaged application certificate was changed")
	}
}

func TestParseSigningMaterialRejectsMismatchedCertificate(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	one := testSigningMaterial(t, "one", now)
	two := testSigningMaterial(t, "two", now)
	one.Certificate = two.Certificate
	body, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseSigningMaterial(string(body)); err == nil {
		t.Fatal("mismatched signing keypair was accepted")
	}
}

func TestParseSigningMaterialRejectsInvalidTrustEntry(t *testing.T) {
	now := time.Date(2026, 8, 27, 1, 2, 3, 0, time.UTC)
	material := testSigningMaterial(t, "current", now)
	material.TrustedCertificates = []signingTrust{{
		Certificate: "not a certificate", RetainUntil: now.Add(time.Hour).Format(time.RFC3339),
	}}
	body, err := json.Marshal(material)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseSigningMaterial(string(body)); err == nil {
		t.Fatal("invalid trust entry was accepted")
	}
}

func testSigningMaterial(t *testing.T, commonName string, now time.Time) signingMaterial {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return signingMaterial{
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{
			Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
		})),
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	}
}
