package dms

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDMSClientRejectsUntrustedBindingAndRedirects(t *testing.T) {
	cfg := ClientConfig{URL: "https://mail.example.test:9443", ServiceToken: strings.Repeat("a", 64)}
	trust := &tls.Config{RootCAs: x509.NewCertPool()}
	for _, target := range []string{"http://mail.example.test:9443", "https://mail.example.test", "https://user:password@mail.example.test:9443", "https://mail.example.test:9443/path", "https://mail.example.test:9443?key=private"} {
		bad := cfg
		bad.URL = target
		if _, err := NewClient(bad, trust); err == nil {
			t.Fatal("untrusted DMS URL accepted", target)
		}
	}
	for _, badTrust := range []*tls.Config{nil, {InsecureSkipVerify: true, RootCAs: x509.NewCertPool()}, {}} {
		if _, err := NewClient(cfg, badTrust); err == nil {
			t.Fatal("unverified TLS accepted")
		}
	}
	c, err := NewClient(cfg, trust)
	if err != nil {
		t.Fatal(err)
	}
	stronger := trust.Clone()
	stronger.MinVersion = tls.VersionTLS13
	strict, err := NewClient(cfg, stronger)
	if err != nil || strict.http.Transport.(*http.Transport).TLSClientConfig.MinVersion != tls.VersionTLS13 {
		t.Fatal("configured TLS policy weakened", err)
	}
	called := 0
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		called++
		if r.Header.Get("Authorization") != "Bearer "+cfg.ServiceToken || r.URL.Host != "mail.example.test:9443" {
			t.Fatal("wrong server authentication")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://foreign.example.test/secret"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if _, err = c.Read(context.Background(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"); err == nil || called != 1 {
		t.Fatal("DMS redirect followed or treated as success", err, called)
	}
	if err = c.Kick(context.Background(), "../command", ""); err == nil || called != 1 {
		t.Fatal("untrusted session reached network")
	}
}
