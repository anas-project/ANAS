package cloudrelay

import (
	"crypto/x509"
	"errors"
	"math"
	"net"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// InstallPreflight is an observation, not an installation or a readiness claim.
// In particular, local binds cannot establish public firewall or MX reachability.
type InstallPreflight struct {
	Configuration      string `json:"configuration"`
	Certificate        string `json:"certificate"`
	CertificateTrust   string `json:"certificate_trust"`
	Directory          string `json:"directory"`
	Disk               string `json:"disk"`
	HTTPSPort          string `json:"https_port"`
	SMTPPort           string `json:"smtp_port"`
	PublicReachability string `json:"public_reachability"`
}

// PreflightRuntimeFile performs no file writes or remote network requests. It
// temporarily holds both TCP listeners to detect overlap as well as conflicts.
// Existing directories need independent recovery checks and are never claimed.
func PreflightRuntimeFile(path string) InstallPreflight {
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		return invalidPreflight()
	}
	roots, _ := x509.SystemCertPool()
	return preflightRuntime(cfg, roots)
}

func invalidPreflight() InstallPreflight {
	return InstallPreflight{Configuration: "failed", Certificate: "unknown", CertificateTrust: "unknown", Directory: "unknown", Disk: "unknown", HTTPSPort: "unknown", SMTPPort: "unknown", PublicReachability: "unknown"}
}

func preflightRuntime(cfg RuntimeConfig, roots *x509.CertPool) InstallPreflight {
	out := InstallPreflight{Configuration: "passed", Certificate: "unknown", CertificateTrust: "unknown", Directory: "unknown", Disk: "unknown", HTTPSPort: "unknown", SMTPPort: "unknown", PublicReachability: "unknown"}
	cert, _, err := prepareRuntimeSecurity(cfg)
	if err != nil {
		out.Certificate = "failed"
		return out
	}
	out.Certificate = "passed"
	if roots != nil {
		intermediates := x509.NewCertPool()
		for _, der := range cert.Certificate[1:] {
			if c, e := x509.ParseCertificate(der); e == nil {
				intermediates.AddCert(c)
			}
		}
		_, err = cert.Leaf.Verify(x509.VerifyOptions{DNSName: cfg.CertificateHost, Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		if err == nil && cfg.SMTPHostname != "" {
			_, err = cert.Leaf.Verify(x509.VerifyOptions{DNSName: cfg.SMTPHostname, Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		}
		if err == nil {
			out.CertificateTrust = "passed"
		} else {
			out.CertificateTrust = "failed"
		}
	}
	if filepath.Clean(cfg.Directory) == cfg.Directory && cfg.Directory != string(filepath.Separator) {
		parent, e := unix.Open(filepath.Dir(cfg.Directory), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			out.Directory = "failed"
		} else {
			defer unix.Close(parent)
			if !secureNoticeFile(parent, true) {
				out.Directory = "failed"
			} else {
				var st unix.Stat_t
				e = unix.Fstatat(parent, filepath.Base(cfg.Directory), &st, unix.AT_SYMLINK_NOFOLLOW)
				if errors.Is(e, unix.ENOENT) {
					out.Directory = "available"
				} else if e == nil {
					out.Directory = "occupied"
				} else {
					out.Directory = "unknown"
				}
				var fs unix.Statfs_t
				if unix.Fstatfs(parent, &fs) == nil && fs.Bsize > 0 {
					available := uint64(fs.Bavail)
					block := uint64(fs.Bsize)
					if available > math.MaxUint64/block || available*block >= uint64(cfg.CapacityBytes) {
						out.Disk = "sufficient-now"
					} else {
						out.Disk = "insufficient-now"
					}
				}
			}
		}
	} else {
		out.Directory = "failed"
	}
	https, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		out.HTTPSPort = "unavailable"
	} else {
		out.HTTPSPort = "available-now"
		defer https.Close()
	}
	if cfg.SMTPListen == "" {
		out.SMTPPort = "not-configured"
	} else if smtp, e := net.Listen("tcp", cfg.SMTPListen); e != nil {
		out.SMTPPort = "unavailable"
	} else {
		out.SMTPPort = "available-now"
		_ = smtp.Close()
	}
	return out
}
