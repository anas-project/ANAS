package consoletls

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testCurrentTime = time.Date(2026, time.August, 29, 8, 0, 0, 0, time.UTC)
	testSerial      atomic.Int64
)

type testAuthority struct {
	certificate *x509.Certificate
	privateKey  ed25519.PrivateKey
	pem         []byte
}

type testLeaf struct {
	certificate    *x509.Certificate
	certificatePEM []byte
	privateKeyPEM  []byte
}

type testMaterial struct {
	leaf   testLeaf
	issuer []byte
	trust  []byte
	source Source
}

func TestManagerLoadsInternalSnapshotAndReturnsDefensiveCertificate(t *testing.T) {
	baseDomain := "example.test"
	material := newInternalMaterial(t, baseDomain, leafOptions{})
	candidate := writeCandidate(t, t.TempDir(), "lego", baseDomain, material, true)
	manager := newTestManager(t, Options{Lego: &candidate})
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := manager.Current()
	if !ok || snapshot.Source() != SourceInternal {
		t.Fatalf("snapshot = %#v, %v", snapshot, ok)
	}
	wantSPKI := sha256.Sum256(material.leaf.certificate.RawSubjectPublicKeyInfo)
	if snapshot.SPKISHA256() != wantSPKI || snapshot.SPKISHA256Hex() == "" {
		t.Fatalf("SPKI = %x, want %x", snapshot.SPKISHA256(), wantSPKI)
	}
	if got := snapshot.DNSNames(); len(got) != 2 || got[0] != baseDomain || got[1] != "*."+baseDomain {
		t.Fatalf("DNS names = %q", got)
	}
	if snapshot.NotBefore() != material.leaf.certificate.NotBefore || snapshot.NotAfter() != material.leaf.certificate.NotAfter {
		t.Fatal("snapshot validity did not match leaf")
	}
	chain := snapshot.CertificateChain()
	if len(chain) != 1 || !bytes.Equal(chain[0], material.leaf.certificate.Raw) {
		t.Fatalf("internal service chain has %d certificates", len(chain))
	}
	chain[0][0] ^= 0xff
	if bytes.Equal(chain[0], snapshot.CertificateChain()[0]) {
		t.Fatal("CertificateChain returned mutable snapshot storage")
	}
	leaf := snapshot.Leaf()
	leaf.DNSNames[0] = "mutated.invalid"
	if snapshot.Leaf().DNSNames[0] != baseDomain {
		t.Fatal("Leaf returned mutable snapshot storage")
	}

	first, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Certificate[0][0] ^= 0xff
	second, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Certificate[0], second.Certificate[0]) || !bytes.Equal(second.Certificate[0], material.leaf.certificate.Raw) {
		t.Fatal("GetCertificate did not return a defensive chain copy")
	}
}

func TestManagerBuildsACMEServiceChainWithoutTrustAnchor(t *testing.T) {
	baseDomain := "example.test"
	material := newACMEMaterial(t, baseDomain, leafOptions{})
	candidate := writeCandidate(t, t.TempDir(), "lego", baseDomain, material, true)
	manager := newTestManager(t, Options{Lego: &candidate})
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := manager.Current()
	chain := snapshot.CertificateChain()
	issuerCertificates, err := parseCertificatePEM(material.issuer, FileRoleIssuer)
	if err != nil {
		t.Fatal(err)
	}
	trustCertificates, err := parseCertificatePEM(material.trust, FileRoleTrustBundle)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Source() != SourceACME || len(chain) != 2 || !bytes.Equal(chain[0], material.leaf.certificate.Raw) || !bytes.Equal(chain[1], issuerCertificates[0].Raw) {
		t.Fatalf("ACME service chain/source = %s, %d certificates", snapshot.Source(), len(chain))
	}
	if bytes.Equal(chain[len(chain)-1], trustCertificates[0].Raw) {
		t.Fatal("service chain included the trust anchor")
	}
}

func TestStrictCandidateValidationRejectsInvalidMaterial(t *testing.T) {
	baseDomain := "example.test"
	tests := []struct {
		name     string
		material func(*testing.T) testMaterial
		mutate   func(*testing.T, Candidate)
		want     string
	}{
		{
			name:     "certificate trailing garbage",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate:   func(t *testing.T, candidate Candidate) { appendFile(t, candidate.CertificatePath, []byte("garbage")) },
			want:     "trailing non-certificate",
		},
		{
			name:     "private key trailing block",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate:   func(t *testing.T, candidate Candidate) { appendFile(t, candidate.PrivateKeyPath, []byte("garbage")) },
			want:     "trailing data",
		},
		{
			name:     "issuer trailing garbage",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate:   func(t *testing.T, candidate Candidate) { appendFile(t, candidate.IssuerPath, []byte("garbage")) },
			want:     "trailing non-certificate",
		},
		{
			name:     "trust bundle trailing garbage",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate:   func(t *testing.T, candidate Candidate) { appendFile(t, candidate.TrustBundlePath, []byte("garbage")) },
			want:     "trailing non-certificate",
		},
		{
			name:     "marker whitespace",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate: func(t *testing.T, candidate Candidate) {
				writeFile(t, candidate.IssuerMarkerPath, []byte(" internal\n"), 0o644)
			},
			want: "exactly internal or acme",
		},
		{
			name:     "mismatched key",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate: func(t *testing.T, candidate Candidate) {
				other := newInternalMaterial(t, baseDomain, leafOptions{})
				writeFile(t, candidate.PrivateKeyPath, other.leaf.privateKeyPEM, 0o600)
			},
			want: "private key",
		},
		{
			name: "expired leaf",
			material: func(t *testing.T) testMaterial {
				return newInternalMaterial(t, baseDomain, leafOptions{notBefore: testCurrentTime.Add(-48 * time.Hour), notAfter: testCurrentTime.Add(-time.Hour)})
			},
			want: "not valid",
		},
		{
			name: "missing console SAN",
			material: func(t *testing.T) testMaterial {
				return newInternalMaterial(t, baseDomain, leafOptions{dnsNames: []string{baseDomain}})
			},
			want: "does not cover console name",
		},
		{
			name: "not server auth",
			material: func(t *testing.T) testMaterial {
				return newInternalMaterial(t, baseDomain, leafOptions{extKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
			},
			want: "incompatible key usage",
		},
		{
			name:     "untrusted chain",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate: func(t *testing.T, candidate Candidate) {
				other := newRootAuthority(t, "other root")
				writeFile(t, candidate.TrustBundlePath, other.pem, 0o644)
			},
			want: "unknown authority",
		},
		{
			name:     "marker disagrees with chain",
			material: func(t *testing.T) testMaterial { return newInternalMaterial(t, baseDomain, leafOptions{}) },
			mutate: func(t *testing.T, candidate Candidate) {
				writeFile(t, candidate.IssuerMarkerPath, []byte("acme\n"), 0o644)
			},
			want: "declared source",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := writeCandidate(t, t.TempDir(), "candidate", baseDomain, test.material(t), true)
			if test.mutate != nil {
				test.mutate(t, candidate)
			}
			manager := newTestManager(t, Options{Lego: &candidate})
			err := manager.Reload()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Reload error = %v, want substring %q", err, test.want)
			}
			if _, ok := manager.Current(); ok {
				t.Fatal("invalid initial candidate was published")
			}
		})
	}
}

func TestFileSafetyRejectsSymlinkNonRegularAndLooseModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission and symlink policy")
	}
	baseDomain := "example.test"
	tests := []struct {
		name   string
		mutate func(*testing.T, Candidate)
		want   string
	}{
		{
			name: "certificate symlink",
			mutate: func(t *testing.T, candidate Candidate) {
				target := candidate.CertificatePath + ".target"
				body, err := os.ReadFile(candidate.CertificatePath)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, target, body, 0o644)
				if err := os.Remove(candidate.CertificatePath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, candidate.CertificatePath); err != nil {
					t.Fatal(err)
				}
			},
			want: "symbolic links",
		},
		{
			name: "issuer directory",
			mutate: func(t *testing.T, candidate Candidate) {
				if err := os.Remove(candidate.IssuerPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(candidate.IssuerPath, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a regular file",
		},
		{
			name: "private key group readable",
			mutate: func(t *testing.T, candidate Candidate) {
				if err := os.Chmod(candidate.PrivateKeyPath, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			want: "group or others",
		},
		{
			name: "public certificate group writable",
			mutate: func(t *testing.T, candidate Candidate) {
				if err := os.Chmod(candidate.CertificatePath, 0o664); err != nil {
					t.Fatal(err)
				}
			},
			want: "writable by group",
		},
		{
			name: "executable trust bundle",
			mutate: func(t *testing.T, candidate Candidate) {
				if err := os.Chmod(candidate.TrustBundlePath, 0o744); err != nil {
					t.Fatal(err)
				}
			},
			want: "must not be executable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			material := newInternalMaterial(t, baseDomain, leafOptions{})
			candidate := writeCandidate(t, t.TempDir(), "candidate", baseDomain, material, true)
			test.mutate(t, candidate)
			manager := newTestManager(t, Options{Lego: &candidate})
			err := manager.Reload()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Reload error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestFileSecurityCheckIsInjectable(t *testing.T) {
	baseDomain := "example.test"
	material := newInternalMaterial(t, baseDomain, leafOptions{})
	candidate := writeCandidate(t, t.TempDir(), "candidate", baseDomain, material, true)
	want := errors.New("owner is not trusted")
	roles := []FileRole{}
	manager := newTestManager(t, Options{
		Lego: &candidate,
		CheckFile: func(_ string, role FileRole, _ os.FileInfo) error {
			roles = append(roles, role)
			if role == FileRoleIssuer {
				return want
			}
			return nil
		},
	})
	err := manager.Reload()
	if !errors.Is(err, want) {
		t.Fatalf("Reload error = %v", err)
	}
	if len(roles) < 3 || roles[0] != FileRoleCertificate || roles[1] != FileRolePrivateKey || roles[2] != FileRoleIssuer {
		t.Fatalf("checked roles = %q", roles)
	}
}

func TestReloadKeepsLastKnownGoodAcrossPartialAndMissingUpdate(t *testing.T) {
	baseDomain := "example.test"
	root := newRootAuthority(t, "internal root")
	firstLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{})
	firstMaterial := testMaterial{leaf: firstLeaf, issuer: root.pem, trust: root.pem, source: SourceInternal}
	candidate := writeCandidate(t, t.TempDir(), "lego", baseDomain, firstMaterial, true)
	manager := newTestManager(t, Options{Lego: &candidate})
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	first, _ := manager.Current()

	secondLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{})
	writeFile(t, candidate.CertificatePath, secondLeaf.certificatePEM, 0o644)
	if err := manager.Reload(); err == nil {
		t.Fatal("half-updated pair was accepted")
	}
	if current, _ := manager.Current(); current != first {
		t.Fatal("half update replaced last-known-good")
	}

	writeFile(t, candidate.PrivateKeyPath, secondLeaf.privateKeyPEM, 0o600)
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	second, _ := manager.Current()
	if second == first || second.SPKISHA256() == first.SPKISHA256() {
		t.Fatal("complete replacement was not published")
	}
	if err := os.Remove(candidate.CertificatePath); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("missing replacement certificate was accepted")
	}
	if current, _ := manager.Current(); current != second {
		t.Fatal("missing files replaced last-known-good")
	}
}

func TestLegoCandidateTakesPriorityAndNeverDowngrades(t *testing.T) {
	baseDomain := "example.test"
	directory := t.TempDir()
	temporaryMaterial := newTemporaryMaterial(t, baseDomain)
	temporary := writeCandidate(t, directory, "temporary", baseDomain, temporaryMaterial, false)
	legoMaterial := newInternalMaterial(t, baseDomain, leafOptions{})
	lego := candidatePaths(directory, "lego", baseDomain, "", true)
	manager := newTestManager(t, Options{Lego: &lego, Temporary: &temporary})
	if err := manager.Reload(); err == nil {
		t.Fatal("missing preferred lego candidate was not reported")
	}
	temporarySnapshot, ok := manager.Current()
	if !ok || temporarySnapshot.Source() != SourceTemporary {
		t.Fatalf("fallback snapshot = %#v, %v", temporarySnapshot, ok)
	}

	writeCandidateFiles(t, lego, legoMaterial, true)
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	legoSnapshot, _ := manager.Current()
	if legoSnapshot.Source() != SourceInternal {
		t.Fatalf("source = %s", legoSnapshot.Source())
	}
	if err := os.Remove(lego.CertificatePath); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("missing lego replacement was not reported")
	}
	if current, _ := manager.Current(); current != legoSnapshot {
		t.Fatal("manager downgraded a lego last-known-good to temporary")
	}
}

func TestCandidateSourceAndIdentityPolicies(t *testing.T) {
	baseDomain := "example.test"
	internalMaterial := newInternalMaterial(t, baseDomain, leafOptions{})
	lego := writeCandidate(t, t.TempDir(), "lego", baseDomain, internalMaterial, true)
	lego.Source = SourceInternal
	lego.IssuerMarkerPath = ""
	if _, err := NewManager(Options{Lego: &lego}); err == nil || !strings.Contains(err.Error(), "explicit issuer marker") {
		t.Fatalf("NewManager error = %v, want explicit issuer marker error", err)
	}

	temporaryMaterial := newTemporaryMaterial(t, baseDomain)
	temporary := writeCandidate(t, t.TempDir(), "temporary", baseDomain, temporaryMaterial, false)
	temporary.BaseDomain = baseDomain
	if _, err := NewManager(Options{Temporary: &temporary}); err == nil || !strings.Contains(err.Error(), "explicit DNS/IP identities") {
		t.Fatalf("NewManager error = %v, want explicit identity error", err)
	}
}

func TestTemporaryCandidateSupportsExplicitIPAddressOnly(t *testing.T) {
	address := net.ParseIP("192.0.2.10")
	material := newTemporaryIPMaterial(t, address)
	candidate := writeCandidate(t, t.TempDir(), "temporary", "", material, false)
	candidate.RequiredIPAddresses = []string{address.String()}
	manager := newTestManager(t, Options{Temporary: &candidate})
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := manager.Current()
	if !ok || snapshot.Source() != SourceTemporary {
		t.Fatalf("temporary snapshot = %#v, %v", snapshot, ok)
	}
}

func TestGetCertificateRefreshesChangesAndKeepsLastKnownGood(t *testing.T) {
	baseDomain := "example.test"
	root := newRootAuthority(t, "internal root")
	firstLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{})
	candidate := writeCandidate(t, t.TempDir(), "lego", baseDomain, testMaterial{
		leaf: firstLeaf, issuer: root.pem, trust: root.pem, source: SourceInternal,
	}, true)
	var warningCount atomic.Int64
	manager := newTestManager(t, Options{
		Lego: &candidate,
		OnReloadError: func(error) {
			warningCount.Add(1)
		},
	})

	firstServed, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstServed.Certificate[0], firstLeaf.certificate.Raw) {
		t.Fatal("first handshake did not load the initial certificate")
	}

	secondLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{
		dnsNames: []string{baseDomain, "*." + baseDomain, "second." + baseDomain},
	})
	replaceFile(t, candidate.CertificatePath, secondLeaf.certificatePEM, 0o644)
	replaceFile(t, candidate.PrivateKeyPath, secondLeaf.privateKeyPEM, 0o600)
	secondServed, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secondServed.Certificate[0], secondLeaf.certificate.Raw) {
		t.Fatal("handshake did not observe the complete certificate replacement")
	}

	thirdLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{
		dnsNames: []string{baseDomain, "*." + baseDomain, "third." + baseDomain, "extra." + baseDomain},
	})
	replaceFile(t, candidate.CertificatePath, thirdLeaf.certificatePEM, 0o644)
	lastKnownGood, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatalf("half update failed a handshake despite last-known-good: %v", err)
	}
	if !bytes.Equal(lastKnownGood.Certificate[0], secondLeaf.certificate.Raw) {
		t.Fatal("half update replaced the last-known-good certificate")
	}
	if warningCount.Load() != 1 {
		t.Fatalf("reload warning count = %d, want 1", warningCount.Load())
	}

	if _, err := manager.GetCertificate(nil); err != nil {
		t.Fatal(err)
	}
	if warningCount.Load() != 1 {
		t.Fatalf("unchanged failed candidate repeated warning, count = %d", warningCount.Load())
	}

	replaceFile(t, candidate.PrivateKeyPath, thirdLeaf.privateKeyPEM, 0o600)
	thirdServed, err := manager.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(thirdServed.Certificate[0], thirdLeaf.certificate.Raw) {
		t.Fatal("handshake did not publish the completed replacement")
	}
}

func TestGetCertificateFailsWhenNoCandidateHasEverValidated(t *testing.T) {
	candidate := candidatePaths(t.TempDir(), "missing", "example.test", "", true)
	var warningCount atomic.Int64
	manager := newTestManager(t, Options{
		Lego: &candidate,
		OnReloadError: func(error) {
			warningCount.Add(1)
		},
	})
	if _, err := manager.GetCertificate(nil); !errors.Is(err, ErrNoCertificate) {
		t.Fatalf("GetCertificate error = %v, want ErrNoCertificate", err)
	}
	if warningCount.Load() != 0 {
		t.Fatalf("warning called without a last-known-good certificate")
	}
}

func TestGetCertificateIsSafeDuringConcurrentReloads(t *testing.T) {
	baseDomain := "example.test"
	root := newRootAuthority(t, "internal root")
	firstLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{})
	secondLeaf := newLeafCertificate(t, root, baseDomain, leafOptions{})
	candidate := writeCandidate(t, t.TempDir(), "lego", baseDomain, testMaterial{leaf: firstLeaf, issuer: root.pem, trust: root.pem, source: SourceInternal}, true)
	manager := newTestManager(t, Options{Lego: &candidate})
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errChannel := make(chan error, 16)
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 500; iteration++ {
				certificate, err := manager.GetCertificate(nil)
				if err != nil {
					errChannel <- err
					return
				}
				if len(certificate.Certificate) == 0 {
					errChannel <- errors.New("empty service chain")
					return
				}
			}
		}()
	}
	for iteration := 0; iteration < 20; iteration++ {
		leaf := firstLeaf
		if iteration%2 == 0 {
			leaf = secondLeaf
		}
		writeFile(t, candidate.CertificatePath, leaf.certificatePEM, 0o644)
		writeFile(t, candidate.PrivateKeyPath, leaf.privateKeyPEM, 0o600)
		if err := manager.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
	close(errChannel)
	for err := range errChannel {
		t.Fatal(err)
	}
}

type leafOptions struct {
	dnsNames    []string
	extKeyUsage []x509.ExtKeyUsage
	notBefore   time.Time
	notAfter    time.Time
}

func newRootAuthority(t *testing.T, commonName string) testAuthority {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             testCurrentTime.Add(-24 * time.Hour),
		NotAfter:              testCurrentTime.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testAuthority{certificate: certificate, privateKey: privateKey, pem: certificatePEM(der)}
}

func newIntermediateAuthority(t *testing.T, parent testAuthority, commonName string) testAuthority {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             testCurrentTime.Add(-24 * time.Hour),
		NotAfter:              testCurrentTime.Add(180 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent.certificate, publicKey, parent.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testAuthority{certificate: certificate, privateKey: privateKey, pem: certificatePEM(der)}
}

func newLeafCertificate(t *testing.T, authority testAuthority, baseDomain string, options leafOptions) testLeaf {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dnsNames := options.dnsNames
	if dnsNames == nil {
		dnsNames = []string{baseDomain, "*." + baseDomain}
	}
	extKeyUsage := options.extKeyUsage
	if extKeyUsage == nil {
		extKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	notBefore := options.notBefore
	if notBefore.IsZero() {
		notBefore = testCurrentTime.Add(-time.Hour)
	}
	notAfter := options.notAfter
	if notAfter.IsZero() {
		notAfter = testCurrentTime.Add(24 * time.Hour)
	}
	template := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: baseDomain},
		DNSNames:     dnsNames,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  extKeyUsage,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, authority.certificate, publicKey, authority.privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return testLeaf{
		certificate:    certificate,
		certificatePEM: certificatePEM(der),
		privateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	}
}

func newTemporaryMaterial(t *testing.T, baseDomain string) testMaterial {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: "temporary " + baseDomain},
		DNSNames:     []string{baseDomain, "*." + baseDomain},
		NotBefore:    testCurrentTime.Add(-time.Hour),
		NotAfter:     testCurrentTime.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf := testLeaf{
		certificate:    certificate,
		certificatePEM: certificatePEM(der),
		privateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	}
	return testMaterial{leaf: leaf, issuer: leaf.certificatePEM, trust: leaf.certificatePEM, source: SourceTemporary}
}

func newTemporaryIPMaterial(t *testing.T, address net.IP) testMaterial {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: "temporary IP certificate"},
		IPAddresses:  []net.IP{address},
		NotBefore:    testCurrentTime.Add(-time.Hour),
		NotAfter:     testCurrentTime.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf := testLeaf{
		certificate:    certificate,
		certificatePEM: certificatePEM(der),
		privateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	}
	return testMaterial{leaf: leaf, issuer: leaf.certificatePEM, trust: leaf.certificatePEM, source: SourceTemporary}
}

func newInternalMaterial(t *testing.T, baseDomain string, options leafOptions) testMaterial {
	t.Helper()
	root := newRootAuthority(t, "internal root")
	return testMaterial{leaf: newLeafCertificate(t, root, baseDomain, options), issuer: root.pem, trust: root.pem, source: SourceInternal}
}

func newACMEMaterial(t *testing.T, baseDomain string, options leafOptions) testMaterial {
	t.Helper()
	root := newRootAuthority(t, "public root")
	intermediate := newIntermediateAuthority(t, root, "ACME intermediate")
	return testMaterial{leaf: newLeafCertificate(t, intermediate, baseDomain, options), issuer: intermediate.pem, trust: root.pem, source: SourceACME}
}

func nextSerial() *big.Int {
	return big.NewInt(testSerial.Add(1))
}

func certificatePEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeCandidate(t *testing.T, directory, prefix, baseDomain string, material testMaterial, marker bool) Candidate {
	t.Helper()
	source := material.source
	if marker {
		source = ""
	}
	candidate := candidatePaths(directory, prefix, baseDomain, source, marker)
	if material.source == SourceTemporary {
		candidate.BaseDomain = ""
		if baseDomain != "" {
			candidate.RequiredDNSNames = []string{baseDomain, "anas." + baseDomain}
		}
	}
	writeCandidateFiles(t, candidate, material, marker)
	return candidate
}

func candidatePaths(directory, prefix, baseDomain string, source Source, marker bool) Candidate {
	candidate := Candidate{
		CertificatePath: filepath.Join(directory, prefix+".crt"),
		PrivateKeyPath:  filepath.Join(directory, prefix+".key"),
		IssuerPath:      filepath.Join(directory, prefix+".issuer.crt"),
		TrustBundlePath: filepath.Join(directory, prefix+".trust.crt"),
		Source:          source,
		BaseDomain:      baseDomain,
	}
	if marker {
		candidate.IssuerMarkerPath = filepath.Join(directory, prefix+".issuer")
	}
	return candidate
}

func writeCandidateFiles(t *testing.T, candidate Candidate, material testMaterial, marker bool) {
	t.Helper()
	writeFile(t, candidate.CertificatePath, material.leaf.certificatePEM, 0o644)
	writeFile(t, candidate.PrivateKeyPath, material.leaf.privateKeyPEM, 0o600)
	writeFile(t, candidate.IssuerPath, material.issuer, 0o644)
	writeFile(t, candidate.TrustBundlePath, material.trust, 0o644)
	if marker {
		writeFile(t, candidate.IssuerMarkerPath, []byte(string(material.source)+"\n"), 0o644)
	}
}

func writeFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func replaceFile(t *testing.T, path string, body []byte, mode os.FileMode) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	writeFile(t, path, body, mode)
}

func appendFile(t *testing.T, path string, body []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func newTestManager(t *testing.T, options Options) *Manager {
	t.Helper()
	options.CurrentTime = func() time.Time { return testCurrentTime }
	manager, err := NewManager(options)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}
