// TEST_CASES: MAILMOD-T-080
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The controller owns the remote relay and both restricted SSH tunnels. This
// file contains only ephemeral fixture values, never production credentials.
type nativeVPSCrossHostInput struct {
	Source, Generation, Reader, Control, Notification, RelayCA string
}

func nativeVPSCrossHostConfig(t *testing.T) *nativeVPSCrossHostInput {
	t.Helper()
	path := os.Getenv("MAIL_NATIVE_VPS_CROSSHOST_CONFIG")
	if path == "" {
		return nil
	}
	if path != "/input/vps-crosshost.json" {
		t.Fatal("unexpected isolated cross-host input")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		t.Fatal("private cross-host input required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cross-host input unavailable")
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input nativeVPSCrossHostInput
	if decoder.Decode(&input) != nil || !webmailKeyPattern.MatchString(input.Reader) || !webmailKeyPattern.MatchString(input.Control) || !webmailKeyPattern.MatchString(input.Notification) || input.Reader == input.Control || input.Reader == input.Notification || input.Control == input.Notification || input.RelayCA == "" {
		t.Fatal("invalid cross-host fixture input")
	}
	for _, name := range []string{"notify.sock", "notify-cert.pem", "relay-ready", "abort"} {
		if _, err = os.Lstat(filepath.Join("/bridge", name)); !os.IsNotExist(err) {
			t.Fatal("stale cross-host fixture state")
		}
	}
	return &input
}

func nativeVPSWaitSignal(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Second)
	defer cancel()
	for {
		if _, err := os.Stat("/bridge/abort"); err == nil {
			t.Fatal("cross-host controller aborted")
		}
		if _, err := os.Stat(filepath.Join("/bridge", name)); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("cross-host fixture readiness timed out")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func nativeVPSBridge(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("/bridge", name)
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("restricted cross-host tunnel missing")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("fixture loopback listener unavailable")
	}
	t.Cleanup(testWorkerTunnel(listener, path))
	return listener.Addr().String()
}

func nativeVPSCrossHostNotify(t *testing.T, path string, handler http.Handler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/api/ingress/notify", handler)
	mux.HandleFunc("/__.health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewUnstartedServer(mux)
	_ = server.Listener.Close()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal("cross-host notification socket unavailable")
	}
	server.Listener = listener
	if os.Chmod(path, 0666) != nil {
		_ = listener.Close()
		t.Fatal("cross-host notification socket permissions unavailable")
	}
	server.StartTLS()
	return server
}

func TestVPSCrossHostNotificationSocketUsesVerifiedTLS(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "anas-vps-notify-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "notice.sock")
	server := nativeVPSCrossHostNotify(t, path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/__.health", 204}, {"POST", "/api/ingress/notify", 202}, {"POST", "/__.health", 404}} {
		r, err := http.NewRequest(tc.method, "https://127.0.0.1"+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("verified socket connection failed", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatal("unexpected fixture response", response.StatusCode)
		}
	}
}
