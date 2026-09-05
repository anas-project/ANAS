package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// incusResponse is the envelope every Incus REST call returns. Errors arrive
// with HTTP 200 and type "error" as often as they arrive with a 4xx, so the
// envelope is authoritative and the status line is not.
type incusResponse struct {
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	ErrorText string          `json:"error"`
	ErrorCode int             `json:"error_code"`
	Metadata  json.RawMessage `json:"metadata"`
}

type notFoundError struct{ path string }

func (e notFoundError) Error() string { return "incus: " + e.path + " does not exist" }

type client struct {
	endpoint string
	http     *http.Client
}

// newClient pins the daemon's certificate by exact DER comparison.
//
// InsecureSkipVerify disables chain and hostname checking, which is the point:
// an Incus daemon presents a self-signed certificate whose SAN rarely matches
// the address an operator configures, so chain verification would either fail
// on a correct deployment or have to be relaxed into accepting any certificate.
// VerifyPeerCertificate replaces it with a stricter rule than a CA check --
// the leaf must be byte-identical to the certificate pinned at apply time.
// There is deliberately no path here that proceeds on a mismatch.
func newClient(endpoint string, serverCert, clientCert, clientKey []byte) (*client, error) {
	pinned, err := decodeCertificate(serverCert)
	if err != nil {
		return nil, fmt.Errorf("pinned server certificate: %w", err)
	}
	keypair, err := tls.X509KeyPair(clientCert, clientKey)
	if err != nil {
		return nil, fmt.Errorf("client keypair is not a usable certificate and key")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{keypair},
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
				if len(rawCerts) == 0 {
					return fmt.Errorf("incus daemon presented no certificate")
				}
				if !bytes.Equal(rawCerts[0], pinned.Raw) {
					return fmt.Errorf("incus daemon certificate does not match the pinned certificate")
				}
				return nil
			},
		},
	}
	return &client{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		http:     &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}, nil
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		// net/http wraps the endpoint but never the credentials; the request
		// carries the client certificate at the TLS layer, not in a header.
		return fmt.Errorf("incus request %s failed: %w", method+" "+path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope incusResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("incus response to %s is not JSON", method+" "+path)
	}
	if envelope.Type == "error" || res.StatusCode >= 400 {
		if envelope.ErrorCode == http.StatusNotFound || res.StatusCode == http.StatusNotFound {
			return notFoundError{path: path}
		}
		return fmt.Errorf("incus %s: %s", method+" "+path, envelope.ErrorText)
	}
	if out != nil && len(envelope.Metadata) > 0 {
		return json.Unmarshal(envelope.Metadata, out)
	}
	return nil
}

func decodeCertificate(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("value is not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// certificateFingerprint is the SHA-256 over the DER body, which is the
// identifier Incus uses for a trust store entry.
func certificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
