package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *consumer {
	t.Helper()
	s := httptest.NewTLSServer(handler)
	t.Cleanup(s.Close)
	client := cloudClient(config{})
	client.Transport = s.Client().Transport
	return &consumer{cfg: config{URL: s.URL, Token: "synthetic-local-token"}, http: client}
}

func TestRedirectNeverForwardsCredential(t *testing.T) {
	var calls atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte("unexpected"))
	}))
	defer destination.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-local-token" {
			t.Error("source did not receive authentication")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	if _, err := c.request(context.Background(), "/messages", "GET", nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("redirect target contacted")
	}
}

func TestIncompleteAndOversizeBodiesAreNotAccepted(t *testing.T) {
	for _, mode := range []string{"truncated", "oversize", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "truncated":
					w.Header().Set("Content-Length", "100")
					w.Write([]byte("short"))
				case "oversize":
					io.Copy(w, strings.NewReader(strings.Repeat("x", 25*1024*1024+1)))
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte("credential response must not enter error log"))
				}
			})
			b, err := c.request(context.Background(), "/raw", "GET", nil)
			if err == nil || b != nil {
				t.Fatal("failed/oversize response accepted")
			}
			if strings.Contains(err.Error(), "credential response") {
				t.Fatal("server response exposed in error")
			}
		})
	}
}

func TestDurableSpoolPreservesBinaryBytesAndPrivateMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mail.eml")
	raw := []byte("Header: value\r\n\r\n\x00\x01\xff\r\n")
	if err := persist(path, raw); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != string(raw) {
		t.Fatal("spool changed raw bytes")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("spool is not private")
	}
	if err := persist(filepath.Join(dir, "missing", "mail.eml"), raw); err == nil {
		t.Fatal("failed persistence reported success")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary files leaked")
	}
}

func TestTransientFailureRetriesAreBounded(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if _, err := c.request(context.Background(), "/messages", "GET", nil); err == nil {
		t.Fatal("service failure reported success")
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestHTTP1ConfigurationNegotiatesHTTP1(t *testing.T) {
	// Clone warms DefaultTransport; the real network exposed stale h2 ALPN here.
	http.DefaultTransport.(*http.Transport).Clone()
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	}))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	client := cloudClient(config{IPv4Only: true, HTTP1Only: true})
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig.RootCAs = s.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	defer transport.CloseIdleConnections()
	r, err := client.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil || string(b) != "HTTP/1.1" {
		t.Fatalf("unexpected negotiated protocol: %q, %v", b, err)
	}
}
