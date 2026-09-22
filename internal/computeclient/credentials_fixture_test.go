package computeclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

// Real TLS material is required by credential admission; text pretending to
// be DER must not turn a cryptographic negative control into a positive one.
func credentialBoundaryLease(t *testing.T) Lease {
	t.Helper()
	pair := func(serial int64) ([]byte, []byte) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		private, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	}
	server, _ := pair(1)
	cert, key := pair(2)
	l := testLease()
	l.ServerCertB64 = base64.StdEncoding.EncodeToString(server)
	l.ServerCertFingerprint, _ = certFingerprint(server)
	l.ClientCertB64 = base64.StdEncoding.EncodeToString(cert)
	l.ClientKeyB64 = base64.StdEncoding.EncodeToString(key)
	return l
}

func credentialDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
