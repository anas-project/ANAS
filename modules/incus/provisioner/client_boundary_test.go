package main

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func boundaryClient(t *testing.T, server *httptest.Server) *client {
	t.Helper()
	pin := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	cert, key := selfSigned(t, "boundary-admin")
	c, err := newClient(server.URL, pin, cert, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.http.CloseIdleConnections)
	return c
}

// INCUS-R-004/R-015/R-016: even a correctly pinned daemon must not be able
// to reflect a certificate, sensitive endpoint or arbitrary error into logs.
func TestClientDoesNotReflectUntrustedErrors(t *testing.T) {
	const secret = "private-certificate-test-marker"
	for name, handler := range map[string]http.HandlerFunc{
		"daemon error": func(w http.ResponseWriter, _ *http.Request) { writeError(w, 400, secret) },
		"invalid metadata": func(w http.ResponseWriter, _ *http.Request) {
			writeSync(w, map[string]any{"count": secret})
		},
		"invalid envelope": func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, secret) },
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(handler)
			defer server.Close()
			c := boundaryClient(t, server)
			var value struct {
				Count int `json:"count"`
			}
			err := c.do(context.Background(), "GET", "/1.0/projects", nil, &value)
			if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("missing or unsafe failure: %v", err)
			}
		})
	}
	server := httptest.NewTLSServer(http.NotFoundHandler())
	c := boundaryClient(t, server)
	server.Close()
	err := c.do(context.Background(), "GET", "/1.0/projects", nil, nil)
	if err == nil || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), strings.TrimPrefix(server.URL, "https://")) {
		t.Fatalf("transport failure exposed endpoint: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.do(ctx, "GET", "/1.0/projects", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", err)
	}
}

func TestClientRequiresSynchronousBoundedResponse(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"async":        `{"type":"async","metadata":{"id":"unobserved-operation"}}`,
		"oversized":    `{"type":"sync","metadata":null}` + strings.Repeat(" ", maxResponseBytes),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			if err := boundaryClient(t, server).do(context.Background(), "GET", "/1.0/projects", nil, nil); err == nil {
				t.Fatal("accepted a response that cannot prove completion")
			}
		})
	}
}

func TestClientNeverFollowsAdministrativeRedirect(t *testing.T) {
	var calls atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeSync(w, nil)
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/1.0/projects", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	// httptest's servers share their test certificate. Pinning alone would
	// therefore not stop this redirect to a different origin.
	if err := boundaryClient(t, source).do(context.Background(), "POST", "/1.0/certificates", map[string]string{"certificate": "not-forwarded"}, nil); err == nil {
		t.Fatal("redirect reported success")
	}
	if calls.Load() != 0 {
		t.Fatal("administrative request followed a redirect")
	}
}

func TestClientRejectsNonOriginEndpointsWithoutEcho(t *testing.T) {
	const marker = "sensitive-marker"
	for _, endpoint := range []string{
		"http://" + marker, "https://user:" + marker + "@example.test",
		"https://example.test/" + marker, "https://example.test?token=" + marker,
		"https://example.test#" + marker, "https://example.test?", "https:///" + marker,
	} {
		_, err := newClient(endpoint, nil, nil, nil)
		if err == nil || strings.Contains(err.Error(), marker) || !strings.Contains(err.Error(), "INCUS_ENDPOINT") {
			t.Fatalf("unsafe endpoint accepted or reflected: %v", err)
		}
	}
}

func TestCertificateDecodeNeverEchoesMalformedSubjectAlternativeName(t *testing.T) {
	certPEM, keyPEM := selfSigned(t, "certificate-boundary")
	certificate, err := decodeCertificate(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(keyPEM)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	const marker = "private-certificate-uri-marker"
	certificate.URIs = []*url.URL{{Scheme: "https", Host: marker + "\\invalid.test"}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	// Go's X.509 parser includes this rejected URI in its raw error. Neither
	// Provider control credentials nor consumer trust requests may echo it.
	if _, err := x509.ParseCertificate(der); err == nil || !strings.Contains(err.Error(), marker) {
		t.Fatalf("fixture did not exercise an input-reflecting parser error: %v", err)
	}
	if _, err := decodeCertificate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("certificate parser error exposed certificate content: %v", err)
	}
}
