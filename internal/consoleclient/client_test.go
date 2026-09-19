package consoleclient

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientUsesHTTPSSessionEnvelopeWithoutProxyOrRedirect(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	cert, ca := testCertificate(t)
	envelope := Envelope{
		Schema: EnvelopeSchema, Origin: "https://anas.test:8443", CAPEM: string(ca),
		Session: SessionEnvelope{Source: SessionLocalOwner, SessionToken: "local-session-token", CSRFToken: "csrf-token"},
	}
	client, err := New(envelope)
	if err != nil {
		t.Fatal(err)
	}
	transport := client.httpClient.Transport.(*http.Transport).Clone()
	if transport.Proxy != nil {
		t.Fatal("console client must not inherit proxy configuration")
	}
	transport.DialContext = pipeDialer(t, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.Method != http.MethodPost || r.URL.Path != "/api/v1/workspaces/main/host/actions/incus.status" {
			t.Fatalf("request = tls:%t %s %s", r.TLS != nil, r.Method, r.URL.Path)
		}
		if r.Header.Get("Origin") != "https://anas.test:8443" || r.Header.Get("X-CSRF-Token") != "csrf-token" || r.Header.Get("Idempotency-Key") != "key-1" {
			t.Fatalf("headers = %#v", r.Header)
		}
		cookie, err := r.Cookie("__Host-anas_local_session")
		if err != nil || cookie.Value != "local-session-token" {
			t.Fatalf("cookie = %#v, %v", cookie, err)
		}
		w.Header().Set("Location", "/api/v1/jobs/job-host")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"api_version":"anas.dev/api/v1","job":{"id":"job-host","status":"queued"}}`))
	}))
	client.httpClient.Transport = transport

	body, location, err := client.InvokeIncusPreflight(context.Background(), "main", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if location != "/api/v1/jobs/job-host" || body["job"] == nil {
		t.Fatalf("response = %#v location=%q", body, location)
	}

	redirectClient, err := New(envelope)
	if err != nil {
		t.Fatal(err)
	}
	redirectTransport := redirectClient.httpClient.Transport.(*http.Transport).Clone()
	redirectTransport.DialContext = pipeDialer(t, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://other.example.test/api/v1/jobs/job-host", http.StatusTemporaryRedirect)
	}))
	redirectClient.httpClient.Transport = redirectTransport
	if _, err := redirectClient.GetJob(context.Background(), "job-host"); err == nil || !strings.Contains(err.Error(), "redirect_refused") {
		t.Fatalf("redirect was not refused: %v", err)
	}
}

func pipeDialer(t *testing.T, cert tls.Certificate, handler http.Handler) func(context.Context, string, string) (net.Conn, error) {
	t.Helper()
	return func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close() })
		go func() {
			conn := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{cert}})
			defer conn.Close()
			request, err := http.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return
			}
			request.TLS = &tls.ConnectionState{}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			_ = recorder.Result().Write(conn)
		}()
		return client, nil
	}
}

func testCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "anas.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              []string{"anas.test"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return cert, certPEM
}
