// TEST_CASES: MAILMOD-T-037
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
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

func TestSMTPProbeSTARTTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.example.test"}, DNSNames: []string{"relay.example.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	for _, mode := range []string{"verified", "wrong-host", "untrusted", "not-advertised", "bad-starttls", "malformed-ehlo"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			serverDone := make(chan error, 1)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				server, err := listener.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer server.Close()
				read := bufio.NewReader(server)
				_, _ = io.WriteString(server, "220 test.example ESMTP\r\n")
				line, err := read.ReadString('\n')
				if err != nil || line != "EHLO anas.invalid\r\n" {
					serverDone <- errors.New("unexpected EHLO")
					return
				}
				if mode == "malformed-ehlo" {
					_, _ = io.WriteString(server, "451 temporary failure\r\n")
					serverDone <- nil
					return
				}
				if mode == "not-advertised" {
					_, _ = io.WriteString(server, "250-test.example\r\n250 SIZE 1024\r\n")
					serverDone <- nil
					return
				}
				_, _ = io.WriteString(server, "250-test.example\r\n250 STARTTLS\r\n")
				line, err = read.ReadString('\n')
				if err != nil || line != "STARTTLS\r\n" {
					serverDone <- errors.New("unexpected SMTP command")
					return
				}
				if mode == "bad-starttls" {
					_, _ = io.WriteString(server, "454 unavailable\r\n")
					serverDone <- nil
					return
				}
				_, _ = io.WriteString(server, "220 ready for TLS\r\n")
				secure := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
				_ = secure.HandshakeContext(ctx)
				serverDone <- nil
			}()
			dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
			}
			host, trust := "relay.example.test", roots
			if mode == "wrong-host" {
				host = "wrong.example.test"
			}
			if mode == "untrusted" {
				trust = x509.NewCertPool()
			}
			greeting, status := smtpProbe(ctx, dial, netip.MustParseAddr("8.8.8.8"), host, trust)
			want := map[string]string{"verified": "verified", "wrong-host": "invalid-certificate", "untrusted": "invalid-certificate", "not-advertised": "not-advertised", "bad-starttls": "unknown", "malformed-ehlo": "unknown"}[mode]
			if greeting != "smtp-greeting" || status != want {
				t.Fatal("incorrect SMTP/TLS observation", greeting, status, want)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVPSSMTPProbePublicAddressGuard(t *testing.T) {
	for _, tc := range []struct {
		address string
		public  bool
	}{
		{"8.8.8.8", true}, {"2001:4860:4860::8888", true},
		{"127.0.0.1", false}, {"10.1.2.3", false}, {"100.64.0.1", false},
		{"169.254.169.254", false}, {"192.0.2.1", false}, {"198.18.0.1", false},
		{"203.0.113.1", false}, {"::1", false}, {"fd00::1", false},
		{"fe80::1", false}, {"2001:db8::1", false}, {"2002::1", false},
		{"::ffff:127.0.0.1", false}, {"3fff::1", false},
	} {
		_, ok := publicProbeIP(net.ParseIP(tc.address))
		if ok != tc.public {
			t.Fatalf("incorrect classification of %s", tc.address)
		}
	}
}

func TestVPSSMTPProbeObservationsAndAuthorization(t *testing.T) {
	for _, mode := range []string{"mixed", "lookup-failure", "too-many", "invalid-host", "duplicate-json", "bad-origin", "not-admin", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, directory := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			admin := &setupHTTPDirectory{httpDirectory: directory, admin: mode != "not-admin"}
			h.directory = admin
			var mu sync.Mutex
			calls := []string{}
			h.lookupIP = func(ctx context.Context, family, host string) ([]net.IP, error) {
				if _, ok := ctx.Deadline(); !ok || host != "relay.example.test" {
					t.Error("invalid lookup")
				}
				if mode == "lookup-failure" {
					return nil, errors.New("private resolver error")
				}
				if mode == "too-many" {
					return make([]net.IP, 9), nil
				}
				if family == "ip4" {
					return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("1.1.1.1"), net.ParseIP("9.9.9.9"), net.ParseIP("10.0.0.1"), net.ParseIP("8.8.8.8")}, nil
				}
				return []net.IP{net.ParseIP("2001:4860:4860::8888")}, nil
			}
			h.probeDial = func(ctx context.Context, network, address string) (net.Conn, error) {
				if _, ok := ctx.Deadline(); !ok || network != "tcp" {
					t.Error("invalid dial")
				}
				mu.Lock()
				calls = append(calls, address)
				mu.Unlock()
				if strings.HasPrefix(address, "1.1.1.1:") {
					return nil, syscall.ECONNREFUSED
				}
				if strings.HasPrefix(address, "9.9.9.9:") {
					return nil, context.DeadlineExceeded
				}
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					if strings.HasPrefix(address, "[2001:") {
						_, _ = io.WriteString(server, "421 private-banner\r\n")
					} else {
						_, _ = io.WriteString(server, "220 private-banner\r\n")
					}
				}()
				return client, nil
			}
			body, origin := `{"host":"relay.example.test"}`, "https://mail.example.test"
			switch mode {
			case "invalid-host":
				body = `{"host":"127.0.0.1"}`
			case "duplicate-json":
				body = `{"host":"relay.example.test","host":"other.example.test"}`
			case "bad-origin":
				origin = "https://evil.test"
			case "revoked":
				directory.afterRead = func() {
					if directory.reads >= 2 {
						admin.admin = false
					}
				}
			}
			req := httptest.NewRequest("POST", "https://mail.example.test/api/setup/vps-smtp-probe", strings.NewReader(body))
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if mode == "invalid-host" || mode == "duplicate-json" || mode == "bad-origin" || mode == "not-admin" {
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
			if w.Code != 200 || strings.Contains(w.Body.String(), "private-") || !strings.Contains(w.Body.String(), `"checked_at"`) {
				t.Fatal("invalid result", w.Code, w.Body.String())
			}
			if mode == "mixed" {
				if len(calls) != 4 {
					t.Fatal("unexpected dials", calls)
				}
				for _, want := range []string{`"address":"8.8.8.8","status":"smtp-greeting"`, `"address":"1.1.1.1","status":"refused"`, `"address":"9.9.9.9","status":"unknown"`, `"address":"10.0.0.1","status":"non-public"`, `"address":"2001:4860:4860::8888","status":"unexpected-banner"`} {
					if !strings.Contains(w.Body.String(), want) {
						t.Fatal("missing observation", want, w.Body.String())
					}
				}
			} else if len(calls) != 0 || !strings.Contains(w.Body.String(), `"status":"unknown"`) {
				t.Fatal("incomplete lookup was probed", w.Body.String())
			}
		})
	}
}

func TestSMTPGreetingHandlesTimeoutAndBounds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	status, _ := smtpProbe(ctx, func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() { defer server.Close(); _, _ = io.WriteString(server, strings.Repeat("x", 16384)) }()
		return client, nil
	}, netip.MustParseAddr("8.8.8.8"), "relay.example.test", nil)
	if status != "unknown" {
		t.Fatal("invalid oversized greeting accepted", status)
	}
}

func TestSMTPGreetingClosesConnectionOnCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connected := make(chan struct{})
	done := make(chan string, 1)
	go func() {
		status, _ := smtpProbe(ctx, func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() { defer server.Close(); _, _ = io.Copy(io.Discard, server) }()
			close(connected)
			return client, nil
		}, netip.MustParseAddr("8.8.8.8"), "relay.example.test", nil)
		done <- status
	}()
	<-connected
	cancel()
	select {
	case status := <-done:
		if status != "unknown" {
			t.Fatal("canceled check returned a definite result", status)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled check left its connection open")
	}
}

func TestVPSSMTPProbeLimitsConcurrentChecks(t *testing.T) {
	vpsSMTPProbeSlot <- struct{}{}
	defer func() { <-vpsSMTPProbeSlot }()
	h, login, _, directory := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login}
	h.directory = &setupHTTPDirectory{httpDirectory: directory, admin: true}
	called := false
	h.lookupIP = func(context.Context, string, string) ([]net.IP, error) {
		called = true
		return nil, errors.New("unexpected lookup")
	}
	r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/vps-smtp-probe", strings.NewReader(`{"host":"relay.example.test"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Origin", "https://mail.example.test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 429 || called {
		t.Fatal("concurrent probe was not limited", w.Code, called)
	}
}

// Explicitly runs from the authorized finance host without sending MAIL or
// modifying either server. A partial observation is not public readiness.
func TestVPSSMTPProbeFinanceLive(t *testing.T) {
	if os.Getenv("MAIL_VPS_SMTP_PROBE_FINANCE_LIVE") != "1" {
		t.Skip("explicit finance network fixture required")
	}
	h, login, _, directory := loginFixture(t)
	h.store = &setupHTTPStore{httpStore: login}
	h.directory = &setupHTTPDirectory{httpDirectory: directory, admin: true}
	r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/vps-smtp-probe", strings.NewReader(`{"host":"osa.letuh.com"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
	r.Header.Set("Origin", "https://mail.example.test")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got struct {
		Host      string          `json:"host"`
		IPv4      smtpProbeFamily `json:"ipv4"`
		IPv6      smtpProbeFamily `json:"ipv6"`
		CheckedAt string          `json:"checked_at"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Host != "osa.letuh.com" || got.IPv4.Status != "observed" || len(got.IPv4.Addresses) == 0 || got.CheckedAt == "" {
		t.Fatal("real finance observation incomplete", w.Code, w.Body.String())
	}
	for _, address := range got.IPv4.Addresses {
		if address.Status != "smtp-greeting" && address.Status != "refused" && address.Status != "unexpected-banner" && address.Status != "unknown" {
			t.Fatal("finance DNS did not resolve a probeable public target", address.Status)
		}
	}
	if got.IPv6.Status != "observed" && got.IPv6.Status != "not-found" && got.IPv6.Status != "unknown" {
		t.Fatal("invalid IPv6 observation")
	}
	t.Log("finance-to-OSA probe result:", w.Body.String())
}
