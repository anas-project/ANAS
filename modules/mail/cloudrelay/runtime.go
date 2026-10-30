package cloudrelay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"golang.org/x/sys/unix"
)

var (
	ErrRuntimeConfiguration = errors.New("cloud relay runtime configuration invalid")
	ErrRuntimeBusy          = errors.New("cloud relay source already running")
	ErrRuntimeUnavailable   = errors.New("cloud relay runtime unavailable")
)

// RuntimeConfig must be read from a private owner-only file, never a URL or
// command argument. The current candidate does not provision this file.
type RuntimeConfig struct {
	Schema             string   `json:"schema"`
	Directory          string   `json:"directory"`
	SourceID           string   `json:"source_id"`
	Generation         string   `json:"generation"`
	Domains            []string `json:"domains"`
	CapacityBytes      int64    `json:"capacity_bytes"`
	DeliveredDays      int      `json:"delivered_days"`
	Listen             string   `json:"listen"`
	CertificateHost    string   `json:"certificate_host"`
	Reader             string   `json:"reader"`
	Control            string   `json:"control"`
	TLSCertificate     string   `json:"tls_certificate"`
	TLSPrivateKey      string   `json:"tls_private_key"`
	NotificationOrigin string   `json:"notification_origin"`
	NotificationSecret string   `json:"notification_secret"`
	NotificationCA     string   `json:"notification_ca"`
	SMTPListen         string   `json:"smtp_listen"`
	SMTPHostname       string   `json:"smtp_hostname"`
	ForwardDomain      string   `json:"forward_domain,omitempty"`
}

func LoadRuntimeConfig(path string) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	if !filepath.IsAbs(path) {
		return cfg, ErrRuntimeConfiguration
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return cfg, ErrRuntimeConfiguration
	}
	f := os.NewFile(uintptr(fd), "private relay configuration")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return cfg, ErrRuntimeConfiguration
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	return decodeRuntimeConfig(raw)
}

func decodeRuntimeConfig(raw []byte) (RuntimeConfig, error) {
	var cfg RuntimeConfig
	fields := []string{"schema", "directory", "source_id", "generation", "domains", "capacity_bytes", "delivered_days", "listen", "certificate_host", "reader", "control", "tls_certificate", "tls_private_key", "notification_origin", "notification_secret", "notification_ca", "smtp_listen", "smtp_hostname"}
	if len(raw) > 64<<10 || !exactObject(raw, fields...) && !exactObject(raw, append(fields, "forward_domain")...) || json.Unmarshal(raw, &cfg) != nil {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	if cfg.Schema != "anas.mail-vps-runtime/v1" || !filepath.IsAbs(cfg.Directory) || !identifier.MatchString(cfg.SourceID) || !identifier.MatchString(cfg.Generation) || !validOriginalCapacity(cfg.CapacityBytes) || cfg.DeliveredDays < 1 || cfg.DeliveredDays > 3650 || len(cfg.Domains) == 0 || len(cfg.Domains) > 100 || !secretValue.MatchString(cfg.Reader) || !secretValue.MatchString(cfg.Control) || cfg.Reader == cfg.Control {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	seen := map[string]bool{}
	for _, domain := range cfg.Domains {
		v, e := directorymail.Domain(domain, "")
		if e != nil || v != domain || seen[domain] {
			return RuntimeConfig{}, ErrRuntimeConfiguration
		}
		seen[domain] = true
	}
	host, port, e := net.SplitHostPort(cfg.Listen)
	if e != nil || net.ParseIP(host) == nil {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	p, e := strconv.Atoi(port)
	if e != nil || p < 1 || p > 65535 {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	if net.ParseIP(cfg.CertificateHost) == nil {
		d, e := directorymail.Domain(cfg.CertificateHost, "")
		if e != nil || d != cfg.CertificateHost {
			return RuntimeConfig{}, ErrRuntimeConfiguration
		}
	}
	if (cfg.NotificationOrigin == "") != (cfg.NotificationSecret == "") || cfg.NotificationOrigin == "" && cfg.NotificationCA != "" || len(cfg.NotificationCA) > 32<<10 {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	if (cfg.SMTPListen == "") != (cfg.SMTPHostname == "") {
		return RuntimeConfig{}, ErrRuntimeConfiguration
	}
	if cfg.ForwardDomain != "" {
		if cfg.SMTPListen == "" {
			return RuntimeConfig{}, ErrRuntimeConfiguration
		}
		if _, e := newRouteLedger(cfg.Directory, cfg.SourceID, cfg.Generation, cfg.Domains, cfg.ForwardDomain); e != nil {
			return RuntimeConfig{}, ErrRuntimeConfiguration
		}
	}
	if cfg.SMTPListen != "" {
		host, port, err := net.SplitHostPort(cfg.SMTPListen)
		p, portErr := strconv.Atoi(port)
		name, domainErr := directorymail.Domain(cfg.SMTPHostname, "")
		if err != nil || net.ParseIP(host) == nil || portErr != nil || p < 1 || p > 65535 || domainErr != nil || name != cfg.SMTPHostname || net.ParseIP(name) != nil {
			return RuntimeConfig{}, ErrRuntimeConfiguration
		}
	}
	return cfg, nil
}

// ValidateRuntimeConfig applies the same structural and TLS checks as a
// private runtime file. It makes no network requests and does not establish
// certificate trust, public reachability, or mail delivery.
func ValidateRuntimeConfig(cfg RuntimeConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ErrRuntimeConfiguration
	}
	defer clear(raw)
	parsed, err := decodeRuntimeConfig(raw)
	if err != nil {
		return err
	}
	_, _, err = prepareRuntimeSecurity(parsed)
	return err
}

type preparedRuntime struct {
	cfg         RuntimeConfig
	store       *OriginalStore
	notifier    *Notifier
	handler     http.Handler
	certificate tls.Certificate
	lock        int
}

func (p *preparedRuntime) close() {
	if p != nil && p.lock >= 0 {
		unix.Flock(p.lock, unix.LOCK_UN)
		unix.Close(p.lock)
		p.lock = -1
	}
}
func prepareRuntimeSecurity(cfg RuntimeConfig) (tls.Certificate, *Notifier, error) {
	cert, err := tls.X509KeyPair([]byte(cfg.TLSCertificate), []byte(cfg.TLSPrivateKey))
	if err != nil || len(cert.Certificate) == 0 {
		return tls.Certificate{}, nil, ErrRuntimeConfiguration
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || leaf.VerifyHostname(cfg.CertificateHost) != nil || cfg.SMTPHostname != "" && leaf.VerifyHostname(cfg.SMTPHostname) != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return tls.Certificate{}, nil, ErrRuntimeConfiguration
	}
	if len(leaf.ExtKeyUsage) > 0 {
		allowed := false
		for _, usage := range leaf.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
				allowed = true
			}
		}
		if !allowed {
			return tls.Certificate{}, nil, ErrRuntimeConfiguration
		}
	}
	cert.Leaf = leaf
	var notifier *Notifier
	if cfg.NotificationOrigin != "" {
		roots, e := x509.SystemCertPool()
		if e != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if cfg.NotificationCA != "" && !roots.AppendCertsFromPEM([]byte(cfg.NotificationCA)) {
			return tls.Certificate{}, nil, ErrRuntimeConfiguration
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		notifier, e = newNotifier(NotificationConfig{Origin: cfg.NotificationOrigin, SourceID: cfg.SourceID, Generation: cfg.Generation, Secret: cfg.NotificationSecret, Reader: cfg.Reader, Control: cfg.Control}, transport)
		if e != nil {
			return tls.Certificate{}, nil, ErrRuntimeConfiguration
		}
	}
	return cert, notifier, nil
}

func prepareRuntime(cfg RuntimeConfig) (*preparedRuntime, error) {
	cert, notifier, err := prepareRuntimeSecurity(cfg)
	if err != nil {
		return nil, err
	}
	ledger, err := openRouteLedger(cfg.Directory, cfg.SourceID, cfg.Generation, cfg.Domains, cfg.ForwardDomain)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	dir, err := ledger.openDir()
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	defer unix.Close(dir)
	lock, err := unix.Openat(dir, ".service.lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	if !secureNoticeFile(lock, false) {
		unix.Close(lock)
		return nil, ErrRuntimeUnavailable
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(lock)
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrRuntimeBusy
		}
		return nil, ErrRuntimeUnavailable
	}
	p := &preparedRuntime{cfg: cfg, lock: lock, certificate: cert, notifier: notifier}
	fail := func() (*preparedRuntime, error) { p.close(); return nil, ErrRuntimeUnavailable }
	p.store, err = OpenOriginalStore(ledger, cfg.CapacityBytes, cfg.DeliveredDays)
	if err != nil {
		return fail()
	}
	if _, err = p.store.recoverPending(); err != nil {
		return fail()
	}
	if _, err = p.store.sourceStatus(notifier); err != nil {
		return fail()
	}
	p.handler, err = NewSourceHandler(p.store, cfg.Reader, cfg.Control, notifier)
	if err != nil {
		return fail()
	}
	return p, nil
}

func (p *preparedRuntime) serve(ctx context.Context, listener, smtpListener net.Listener, report func(string)) error {
	if p == nil || p.store == nil || p.handler == nil || listener == nil || (p.cfg.SMTPListen == "") != (smtpListener == nil) {
		return ErrRuntimeConfiguration
	}
	server := &http.Server{Handler: p.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{p.certificate}}}
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	start := func(name string, run func(context.Context, func(error)) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = run(work, func(error) {
				if report != nil {
					report(name)
				}
			})
		}()
	}
	start("retention-unavailable", p.store.RunRetention)
	if p.notifier != nil {
		start("notification-unavailable", func(ctx context.Context, emit func(error)) error {
			return p.store.RunNotificationScheduler(ctx, p.notifier, emit)
		})
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	var smtpServed chan error
	var smtpStop func()
	if smtpListener != nil {
		smtpServer, limited, tracked, gate := newSMTPRuntime(p.store, p.cfg.SMTPHostname, p.certificate, smtpListener)
		smtpServed = make(chan error, 1)
		go func() { smtpServed <- smtpServer.Serve(limited) }()
		smtpStop = func() {
			_ = limited.Close()
			tracked.closeConnections()
			// Serve must return before Shutdown waits: no connection can then
			// be added to the library's wait group after it starts waiting.
			if smtpServed != nil {
				<-smtpServed
			}
			gate.stop()
			_ = smtpServer.Shutdown(context.Background())
		}
	}
	var serveErr error
	var smtpFailed bool
	select {
	case <-ctx.Done():
	case serveErr = <-served:
	case <-smtpServed:
		smtpServed = nil
		smtpFailed = true
	}
	cancel()
	if smtpStop != nil {
		smtpStop()
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
	}
	wg.Wait()
	if smtpFailed || serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return ErrRuntimeUnavailable
	}
	return nil
}

// RunRuntimeFile opens an already initialized private source and starts HTTPS,
// notification and retention workers, and optionally the SMTP listener. It
// never creates a source, modifies DNS, or marks end-to-end delivery ready.
func RunRuntimeFile(ctx context.Context, path string, report func(string)) error {
	if ctx.Err() != nil {
		return nil
	}
	cfg, err := LoadRuntimeConfig(path)
	if err != nil {
		return err
	}
	return runRuntime(ctx, cfg, report)
}

func runRuntime(ctx context.Context, cfg RuntimeConfig, report func(string)) error {
	p, err := prepareRuntime(cfg)
	if err != nil {
		return err
	}
	defer p.close()
	if ctx.Err() != nil {
		return nil
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return ErrRuntimeUnavailable
	}
	defer listener.Close()
	var smtpListener net.Listener
	if cfg.SMTPListen != "" {
		smtpListener, err = net.Listen("tcp", cfg.SMTPListen)
		if err != nil {
			return ErrRuntimeUnavailable
		}
		defer smtpListener.Close()
	}
	return p.serve(ctx, listener, smtpListener, report)
}
