// Package consoletls loads and validates the certificates served by the ANAS
// management endpoint. It consumes certificate material only; certificate
// issuance and console enrollment state belong to other packages.
package consoletls

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Source identifies the authority class of a validated serving certificate.
type Source string

const (
	SourceInternal  Source = "internal"
	SourceACME      Source = "acme"
	SourceTemporary Source = "temporary"
)

var (
	ErrNoCertificate = errors.New("no validated console TLS certificate is available")
	ErrNoCandidates  = errors.New("no console TLS certificate candidates are configured")
)

// FileRole identifies why one artifact is being read. A custom
// FileSecurityCheck can use it to enforce role-specific ownership or mode
// rules in addition to the package's unconditional regular-file and symlink
// checks.
type FileRole string

const (
	FileRoleCertificate FileRole = "certificate"
	FileRolePrivateKey  FileRole = "private_key"
	FileRoleIssuer      FileRole = "issuer"
	FileRoleTrustBundle FileRole = "trust_bundle"
	FileRoleInternalCA  FileRole = "internal_ca"
	FileRoleIssuerMark  FileRole = "issuer_marker"
)

// FileSecurityCheck validates metadata for the file descriptor that will be
// read. It runs in addition to DefaultFileSecurityCheck, which makes ownership
// and platform ACL policies injectable without allowing callers to weaken the
// default mode checks. Symlinks and non-regular files are rejected regardless
// of this callback.
type FileSecurityCheck func(path string, role FileRole, info fs.FileInfo) error

// Candidate describes one complete certificate input. A Lego candidate must
// provide IssuerMarkerPath; its exact internal/acme value determines Source,
// which may be empty or may pin the expected marker value. A Temporary
// candidate has a fixed temporary source and uses only explicit DNS/IP
// identities, without a marker or BaseDomain-derived identities.
type Candidate struct {
	CertificatePath     string
	PrivateKeyPath      string
	IssuerPath          string
	TrustBundlePath     string
	InternalCAPath      string
	IssuerMarkerPath    string
	Source              Source
	BaseDomain          string
	RequiredDNSNames    []string
	RequiredIPAddresses []string
}

// Options configures a Manager. Lego and Temporary are separate so a valid
// lego certificate can always take precedence over bootstrap-only material.
type Options struct {
	Lego        *Candidate
	Temporary   *Candidate
	CheckFile   FileSecurityCheck
	CurrentTime func() time.Time
	// OnReloadError reports a failed refresh only when a last-known-good
	// certificate remains available. The callback must be concurrency-safe.
	OnReloadError func(error)
}

// Snapshot is an immutable validated certificate view. Its fields are kept
// private and accessors return values or defensive copies.
type Snapshot struct {
	source      Source
	spkiSHA256  [sha256.Size]byte
	leafDER     []byte
	chainDER    [][]byte
	internalCA  []byte
	dnsNames    []string
	notBefore   time.Time
	notAfter    time.Time
	certificate tls.Certificate
}

func (s *Snapshot) Source() Source {
	if s == nil {
		return ""
	}
	return s.source
}

func (s *Snapshot) SPKISHA256() [sha256.Size]byte {
	if s == nil {
		return [sha256.Size]byte{}
	}
	return s.spkiSHA256
}

func (s *Snapshot) SPKISHA256Hex() string {
	if s == nil {
		return ""
	}
	return hex.EncodeToString(s.spkiSHA256[:])
}

func (s *Snapshot) DNSNames() []string {
	if s == nil {
		return []string{}
	}
	return append([]string{}, s.dnsNames...)
}

func (s *Snapshot) NotBefore() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.notBefore
}

func (s *Snapshot) NotAfter() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.notAfter
}

// CertificateChain returns the exact DER chain offered to TLS clients, leaf
// first. Trust anchors are omitted unless the leaf itself is the trust anchor
// (the supported temporary self-signed case).
func (s *Snapshot) CertificateChain() [][]byte {
	if s == nil {
		return [][]byte{}
	}
	return cloneByteSlices(s.chainDER)
}

// InternalCAPEM returns the validated public lego internal trust anchor. It
// remains available when the serving certificate has moved to ACME so clients
// can still install the stable ANAS trust root. Temporary certificates never
// expose an internal CA.
func (s *Snapshot) InternalCAPEM() []byte {
	if s == nil {
		return nil
	}
	return append([]byte{}, s.internalCA...)
}

// Leaf returns a newly parsed copy of the validated leaf certificate.
func (s *Snapshot) Leaf() *x509.Certificate {
	if s == nil {
		return nil
	}
	certificate, err := x509.ParseCertificate(append([]byte{}, s.leafDER...))
	if err != nil {
		// leafDER was parsed before Snapshot construction, so this cannot fail
		// without memory corruption.
		return nil
	}
	return certificate
}

// Manager serializes reload attempts and publishes complete Snapshots through
// an atomic pointer. A failed reload never changes the current snapshot.
type Manager struct {
	reloadMu      sync.Mutex
	current       atomic.Pointer[Snapshot]
	lego          *Candidate
	temporary     *Candidate
	checkFile     FileSecurityCheck
	now           func() time.Time
	onReloadError func(error)
	lastStamp     []fileStamp
	lastStampOK   bool
}

func NewManager(options Options) (*Manager, error) {
	manager := &Manager{now: options.CurrentTime, onReloadError: options.OnReloadError}
	manager.checkFile = func(path string, role FileRole, info fs.FileInfo) error {
		if err := DefaultFileSecurityCheck(path, role, info); err != nil {
			return err
		}
		if options.CheckFile != nil {
			return options.CheckFile(path, role, info)
		}
		return nil
	}
	if manager.now == nil {
		manager.now = time.Now
	}
	if options.Lego != nil {
		candidate := cloneCandidate(*options.Lego)
		if err := validateCandidateConfig(candidate); err != nil {
			return nil, fmt.Errorf("lego certificate candidate: %w", err)
		}
		if candidate.BaseDomain == "" {
			return nil, fmt.Errorf("lego certificate candidate requires base domain validation")
		}
		if candidate.IssuerMarkerPath == "" {
			return nil, fmt.Errorf("lego certificate candidate requires an explicit issuer marker path")
		}
		if candidate.InternalCAPath == "" {
			return nil, fmt.Errorf("lego certificate candidate requires an explicit internal CA path")
		}
		if candidate.Source == SourceTemporary {
			return nil, fmt.Errorf("lego certificate candidate cannot use temporary source")
		}
		manager.lego = &candidate
	}
	if options.Temporary != nil {
		candidate := cloneCandidate(*options.Temporary)
		if err := validateCandidateConfig(candidate); err != nil {
			return nil, fmt.Errorf("temporary certificate candidate: %w", err)
		}
		if candidate.Source != SourceTemporary || candidate.IssuerMarkerPath != "" {
			return nil, fmt.Errorf("temporary certificate candidate must use explicit temporary source without an issuer marker")
		}
		if candidate.BaseDomain != "" {
			return nil, fmt.Errorf("temporary certificate candidate must use explicit DNS/IP identities instead of a base domain")
		}
		manager.temporary = &candidate
	}
	if manager.lego == nil && manager.temporary == nil {
		return nil, ErrNoCandidates
	}
	return manager, nil
}

func cloneCandidate(candidate Candidate) Candidate {
	candidate.RequiredDNSNames = append([]string{}, candidate.RequiredDNSNames...)
	candidate.RequiredIPAddresses = append([]string{}, candidate.RequiredIPAddresses...)
	return candidate
}

// Reload forces validation of the configured candidates before publishing a
// complete replacement. A usable temporary fallback may be published when the
// preferred lego candidate is unavailable, but Reload still returns the lego
// error so callers can report the degraded condition. Once a lego snapshot has
// been published, failure to reload it never downgrades the manager to
// temporary material. When a reload error leaves a last-known-good snapshot in
// place, OnReloadError is invoked after the reload lock has been released.
func (m *Manager) Reload() error {
	if m == nil {
		return ErrNoCandidates
	}
	return m.reload(true)
}

func (m *Manager) reload(force bool) error {
	m.reloadMu.Lock()
	hadCurrent := m.current.Load() != nil
	before := captureFileStamp(m.candidatePaths())
	if !force && m.lastStampOK && equalFileStamps(before, m.lastStamp) && m.currentCertificateIsTimeValid() {
		m.reloadMu.Unlock()
		return nil
	}

	err := m.reloadLocked()
	after := captureFileStamp(m.candidatePaths())
	m.lastStamp = after
	m.lastStampOK = equalFileStamps(before, after)
	warn := err != nil && hadCurrent && m.current.Load() != nil && m.onReloadError != nil
	m.reloadMu.Unlock()
	if warn {
		m.onReloadError(err)
	}
	return err
}

func (m *Manager) reloadLocked() error {
	var legoErr error
	if m.lego != nil {
		snapshot, err := loadCandidate(*m.lego, m.checkFile, m.now())
		if err == nil {
			m.current.Store(snapshot)
			return nil
		}
		legoErr = fmt.Errorf("reload lego certificate: %w", err)
		if current := m.current.Load(); current != nil && current.source != SourceTemporary {
			return legoErr
		}
	}

	var temporaryErr error
	if m.temporary != nil {
		snapshot, err := loadCandidate(*m.temporary, m.checkFile, m.now())
		if err == nil {
			m.current.Store(snapshot)
			return legoErr
		}
		temporaryErr = fmt.Errorf("reload temporary certificate: %w", err)
	}
	if current := m.current.Load(); current != nil {
		return errors.Join(legoErr, temporaryErr)
	}
	return errors.Join(legoErr, temporaryErr, ErrNoCertificate)
}

func (m *Manager) candidatePaths() []string {
	paths := make([]string, 0, 10)
	appendCandidate := func(candidate *Candidate) {
		if candidate == nil {
			return
		}
		paths = append(paths,
			candidate.CertificatePath,
			candidate.PrivateKeyPath,
			candidate.IssuerPath,
			candidate.TrustBundlePath,
		)
		if candidate.InternalCAPath != "" {
			paths = append(paths, candidate.InternalCAPath)
		}
		if candidate.IssuerMarkerPath != "" {
			paths = append(paths, candidate.IssuerMarkerPath)
		}
	}
	appendCandidate(m.lego)
	appendCandidate(m.temporary)
	return paths
}

type fileStamp struct {
	path string
	info fs.FileInfo
	err  string
}

func captureFileStamp(paths []string) []fileStamp {
	stamp := make([]fileStamp, len(paths))
	for index, path := range paths {
		stamp[index].path = path
		info, err := os.Lstat(path)
		if err != nil {
			stamp[index].err = err.Error()
			continue
		}
		stamp[index].info = info
	}
	return stamp
}

func equalFileStamps(left, right []fileStamp) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].path != right[index].path || left[index].err != right[index].err {
			return false
		}
		if left[index].err == "" && !sameFileVersion(left[index].info, right[index].info) {
			return false
		}
	}
	return true
}

func (m *Manager) currentCertificateIsTimeValid() bool {
	snapshot := m.current.Load()
	if snapshot == nil {
		return false
	}
	now := m.now().UTC()
	return !now.Before(snapshot.notBefore) && !now.After(snapshot.notAfter)
}

// Current returns the immutable currently served snapshot, if one exists.
func (m *Manager) Current() (*Snapshot, bool) {
	if m == nil {
		return nil, false
	}
	snapshot := m.current.Load()
	return snapshot, snapshot != nil
}

// GetCertificate is safe for concurrent TLS handshakes and observes certificate
// file changes without relying on a watcher. An unchanged-file metadata fast
// path avoids reparsing the chain for every handshake. If a changed candidate
// is invalid, an existing last-known-good certificate is returned and the
// reload error is reported through OnReloadError; without a last-known-good
// certificate, the handshake fails.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if m == nil {
		return nil, ErrNoCertificate
	}
	reloadErr := m.reload(false)
	snapshot, ok := m.Current()
	if !ok {
		if reloadErr != nil {
			return nil, errors.Join(reloadErr, ErrNoCertificate)
		}
		return nil, ErrNoCertificate
	}
	return cloneTLSCertificate(snapshot.certificate), nil
}

func cloneTLSCertificate(source tls.Certificate) *tls.Certificate {
	clone := source
	clone.Certificate = cloneByteSlices(source.Certificate)
	// nil means "use the TLS stack defaults" while a non-nil empty slice
	// means "support no signature algorithms". Preserve that distinction or
	// every real handshake using an otherwise-valid cloned certificate fails.
	clone.SupportedSignatureAlgorithms = append([]tls.SignatureScheme(nil), source.SupportedSignatureAlgorithms...)
	clone.OCSPStaple = append([]byte{}, source.OCSPStaple...)
	clone.SignedCertificateTimestamps = cloneByteSlices(source.SignedCertificateTimestamps)
	if len(clone.Certificate) > 0 {
		clone.Leaf, _ = x509.ParseCertificate(clone.Certificate[0])
	}
	return &clone
}

func cloneByteSlices(source [][]byte) [][]byte {
	clone := make([][]byte, len(source))
	for index := range source {
		clone[index] = append([]byte{}, source[index]...)
	}
	return clone
}
