package cloudrelay

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func verifySystemdProcess(pid string, binary []byte) error {
	number, err := strconv.ParseUint(pid, 10, 31)
	if err != nil || number == 0 {
		return ErrPublishUnknown
	}
	base := filepath.Join("/proc", strconv.FormatUint(number, 10))
	executable, err := os.Open(filepath.Join(base, "exe"))
	if err != nil {
		return ErrPublishUnknown
	}
	defer executable.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(executable, 128<<20+1))
	expected := sha256.Sum256(binary)
	if err != nil || size != int64(len(binary)) || subtle.ConstantTimeCompare(digest.Sum(nil), expected[:]) != 1 {
		return ErrPublishUnknown
	}
	status, err := os.Open(filepath.Join(base, "status"))
	if err != nil {
		return ErrPublishUnknown
	}
	defer status.Close()
	raw, err := io.ReadAll(io.LimitReader(status, 64<<10+1))
	if err != nil || len(raw) > 64<<10 {
		return ErrPublishUnknown
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if found || len(fields) != 5 {
				return ErrPublishUnknown
			}
			uid, e := strconv.ParseUint(fields[2], 10, 32)
			if e != nil || uid == 0 {
				return ErrPublishUnknown
			}
			found = true
		}
	}
	if !found {
		return ErrPublishUnknown
	}
	return nil
}

// localSourceStatus uses the reader credential only over verified TLS to a
// loopback listener. It observes local source state, not public mail delivery.
func localSourceStatus(ctx context.Context, cfg RuntimeConfig, roots *x509.CertPool) error {
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return ErrPublishUnknown
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ErrPublishUnknown
	}
	if ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	} else if !ip.IsLoopback() {
		return ErrPublishUnknown
	}
	address := net.JoinHostPort(host, port)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: cfg.CertificateHost}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", address)
	}, ResponseHeaderTimeout: 3 * time.Second, MaxResponseHeaderBytes: 8192}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(cfg.CertificateHost, port)+"/v1/status", nil)
	if err != nil {
		return ErrPublishUnknown
	}
	request.Header.Set("Authorization", "Bearer "+cfg.Reader)
	response, err := client.Do(request)
	if err != nil {
		return ErrPublishUnknown
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrPublishUnknown
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(raw) > 4096 {
		return ErrPublishUnknown
	}
	status, err := ingress.DecodeSourceStatus(raw, cfg.SourceID, cfg.Generation)
	if err != nil || status.CapacityBytes != cfg.CapacityBytes || status.DeliveredDays != cfg.DeliveredDays || status.ForwardDomain != cfg.ForwardDomain {
		return ErrPublishUnknown
	}
	if cfg.NotificationOrigin == "" && status.Notifications != "unconfigured" || cfg.NotificationOrigin != "" && status.Notifications != "configured" {
		return ErrPublishUnknown
	}
	return nil
}
