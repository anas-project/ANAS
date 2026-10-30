// TEST_CASES: MAILMOD-T-037
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestOutboundSMTPProbeImplicitTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "smtp.example.test"}, DNSNames: []string{"smtp.example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	for _, mode := range []string{"verified", "wrong-host", "untrusted"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				server, err := listener.Accept()
				if err != nil {
					return
				}
				defer server.Close()
				secure := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
				if secure.HandshakeContext(ctx) == nil {
					_, _ = io.WriteString(secure, "220 test ESMTP\r\n")
				}
			}()
			dial := func(ctx context.Context, network, address string) (net.Conn, error) {
				if network != "tcp" || address != "8.8.8.8:465" {
					t.Errorf("unexpected target %s %s", network, address)
				}
				return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
			}
			host, trust := "smtp.example.test", roots
			if mode == "wrong-host" {
				host = "other.example.test"
			}
			if mode == "untrusted" {
				trust = x509.NewCertPool()
			}
			status, tlsStatus := smtpProbePort(ctx, dial, netip.MustParseAddr("8.8.8.8"), host, 465, "tls", trust)
			if mode == "verified" && (status != "smtp-greeting" || tlsStatus != "verified") || mode != "verified" && (status != "unknown" || tlsStatus != "invalid-certificate") {
				t.Fatalf("incorrect observation %s %s", status, tlsStatus)
			}
			<-done
		})
	}
}

// Explicit, read-only finance observation. No credentials, SMTP AUTH or mail
// are sent; network results do not establish delivery or production readiness.
func TestOutboundSMTPProbeFinanceLive(t *testing.T) {
	if os.Getenv("MAIL_OUTBOUND_SMTP_PROBE_FINANCE_LIVE") != "1" {
		t.Skip("explicit finance network fixture required")
	}
	for _, tc := range []struct {
		host string
		port int
		mode string
	}{{"smtp.resend.com", 587, "starttls"}, {"smtp.eusend.dev", 465, "tls"}} {
		t.Run(tc.host, func(t *testing.T) {
			h, login, _, directory := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			h.directory = &setupHTTPDirectory{httpDirectory: directory, admin: true}
			raw, err := json.Marshal(map[string]any{"host": tc.host, "port": tc.port, "tls": tc.mode})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-smtp-probe", strings.NewReader(string(raw)))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", "https://mail.example.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var response struct {
				Host    string          `json:"host"`
				Port    int             `json:"port"`
				TLS     string          `json:"tls"`
				IPv4    smtpProbeFamily `json:"ipv4"`
				IPv6    smtpProbeFamily `json:"ipv6"`
				Checked string          `json:"checked_at"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Host != tc.host || response.Port != tc.port || response.TLS != tc.mode || response.Checked == "" {
				t.Fatal("probe response invalid", w.Code)
			}
			t.Logf("ipv4=%+v ipv6=%+v", response.IPv4, response.IPv6)
		})
	}
}

func TestOutboundSMTPProbeHTTPGuardAndTargets(t *testing.T) {
	for _, mode := range []string{"valid", "invalid-port", "invalid-tls", "duplicate", "private-only", "not-admin", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, directory := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			admin := &setupHTTPDirectory{httpDirectory: directory, admin: mode != "not-admin"}
			h.directory = admin
			var mu sync.Mutex
			var calls []string
			h.lookupIP = func(_ context.Context, family, _ string) ([]net.IP, error) {
				if family == "ip6" {
					return nil, &net.DNSError{IsNotFound: true}
				}
				if mode == "private-only" {
					return []net.IP{net.ParseIP("127.0.0.1")}, nil
				}
				return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("10.0.0.1")}, nil
			}
			h.probeDial = func(_ context.Context, _, address string) (net.Conn, error) {
				mu.Lock()
				calls = append(calls, address)
				mu.Unlock()
				return nil, syscall.ECONNREFUSED
			}
			body := `{"host":"smtp.example.test","port":587,"tls":"starttls"}`
			switch mode {
			case "invalid-port":
				body = strings.Replace(body, "587", "0", 1)
			case "invalid-tls":
				body = strings.Replace(body, "starttls", "none", 1)
			case "duplicate":
				body = strings.Replace(body, `"port":587`, `"port":465,"port":587`, 1)
			case "revoked":
				directory.afterRead = func() {
					if directory.reads >= 2 {
						admin.admin = false
					}
				}
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-smtp-probe", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", "https://mail.example.test")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if mode == "invalid-port" || mode == "invalid-tls" || mode == "duplicate" || mode == "not-admin" {
				if w.Code < 400 || len(calls) != 0 {
					t.Fatal("invalid request probed", w.Code, calls)
				}
				return
			}
			if mode == "revoked" {
				if w.Code != 403 || strings.Contains(w.Body.String(), "8.8.8.8") {
					t.Fatal("revoked caller received result", w.Code)
				}
				return
			}
			var result struct {
				Host    string          `json:"host"`
				Port    int             `json:"port"`
				TLS     string          `json:"tls"`
				IPv4    smtpProbeFamily `json:"ipv4"`
				Checked string          `json:"checked_at"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Host != "smtp.example.test" || result.Port != 587 || result.TLS != "starttls" || result.Checked == "" {
				t.Fatal("invalid result", w.Code, w.Body.String())
			}
			if mode == "private-only" && (len(calls) != 0 || result.IPv4.Addresses[0].Status != "non-public") {
				t.Fatal("private address was probed", calls)
			}
			if mode == "valid" && (len(calls) != 1 || calls[0] != "8.8.8.8:587") {
				t.Fatal("wrong probe target", calls)
			}
		})
	}
}
