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
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// incusResponse is the envelope every Incus REST call returns. Errors arrive
// with HTTP 200 and type "error" as often as they arrive with a 4xx, so the
// envelope is authoritative and the status line is not.
type incusResponse struct {
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	Operation string          `json:"operation"`
	ErrorText string          `json:"error"`
	ErrorCode int             `json:"error_code"`
	Metadata  json.RawMessage `json:"metadata"`
}

type operationRecord struct {
	ID         string `json:"id"`
	StatusCode int    `json:"status_code"`
	Err        string `json:"err"`
	Metadata   map[string]any `json:"metadata"`
	Resources  map[string][]string `json:"resources"`
}

type notFoundError struct{ path string }

func (e notFoundError) Error() string { return "incus: " + e.path + " does not exist" }

type client struct {
	endpoint string
	http     *http.Client
}

var errPinnedCertificate = errors.New("incus daemon certificate does not match the pinned certificate")

const maxResponseBytes = 4 << 20

var operationPathPattern = regexp.MustCompile(`^/1\.0/operations/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var operationIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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
	address, err := url.Parse(endpoint)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" || address.Opaque != "" || (address.Path != "" && address.Path != "/") || address.RawPath != "" {
		return nil, fmt.Errorf("INCUS_ENDPOINT must be an HTTPS origin without credentials, path, query or fragment")
	}
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
					return errPinnedCertificate
				}
				return nil
			},
		},
	}
	return &client{
		endpoint: strings.TrimSuffix(endpoint, "/"),
		http: &http.Client{
			Transport: transport, Timeout: 30 * time.Second,
			// An HTTP redirect must never move administrative mTLS to another
			// origin, including one presenting the same server certificate.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("incus %s: invalid request body", method)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, payload)
	if err != nil {
		return fmt.Errorf("incus %s: invalid request", method)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		// The endpoint is sensitive too. Never unwrap a url.Error, peer error
		// or daemon response into the Runner's output. Preserve only trusted
		// categories and cancellation identity for callers.
		if ctx.Err() != nil {
			return fmt.Errorf("incus %s: %w", method+" "+path, ctx.Err())
		}
		if errors.Is(err, errPinnedCertificate) {
			return errPinnedCertificate
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return fmt.Errorf("incus %s: request timed out", method+" "+path)
		}
		return fmt.Errorf("incus %s: transport failed", method+" "+path)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return fmt.Errorf("incus %s: response unavailable or exceeds limit", method+" "+path)
	}
	var envelope incusResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("incus response to %s is not JSON", method+" "+path)
	}
	if envelope.Type == "error" || res.StatusCode >= 400 {
		if envelope.ErrorCode == http.StatusNotFound || res.StatusCode == http.StatusNotFound {
			return notFoundError{path: path}
		}
		// A daemon can echo a submitted certificate or another sensitive
		// value in error text. Only its numeric status is safe to publish.
		return fmt.Errorf("incus %s: operation rejected (HTTP %d, code %d)", method+" "+path, res.StatusCode, envelope.ErrorCode)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || envelope.Type != "sync" {
		return fmt.Errorf("incus %s: expected a synchronous successful response", method+" "+path)
	}
	if out != nil && len(envelope.Metadata) > 0 {
		if err := json.Unmarshal(envelope.Metadata, out); err != nil {
			return fmt.Errorf("incus %s: invalid response metadata", method+" "+path)
		}
	}
	return nil
}

func (c *client) doMultipartOperation(ctx context.Context, path, project string, write func(*multipart.Writer) error) (string, error) {
	if ctx == nil || write == nil || strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("incus image import: invalid request")
	}
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	errs := make(chan error, 1)
	go func() {
		err := write(writer)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = pipeWriter.CloseWithError(err)
		} else {
			_ = pipeWriter.Close()
		}
		errs <- err
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, pipeReader)
	if err != nil {
		return "", fmt.Errorf("incus image import: invalid request")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := c.http.Do(req)
	if err != nil {
		_ = pipeReader.Close()
		<-errs
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, errPinnedCertificate) {
			return "", errPinnedCertificate
		}
		return "", fmt.Errorf("incus image import: transport failed")
	}
	writeErr := <-errs
	defer res.Body.Close()
	if writeErr != nil {
		return "", fmt.Errorf("incus image import: artifact stream failed")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return "", fmt.Errorf("incus image import: response unavailable or exceeds limit")
	}
	var envelope incusResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("incus image import response is not JSON")
	}
	if envelope.Type == "error" || res.StatusCode >= 400 {
		return "", fmt.Errorf("incus image import: operation rejected (HTTP %d, code %d)", res.StatusCode, envelope.ErrorCode)
	}
	if res.StatusCode != http.StatusAccepted || envelope.Type != "async" || !operationPathPattern.MatchString(envelope.Operation) {
		return "", fmt.Errorf("incus image import: expected a bounded asynchronous operation")
	}
	return c.waitOperation(ctx, envelope.Operation, project)
}

func (c *client) waitOperation(ctx context.Context, operation, project string) (string, error) {
	if !operationPathPattern.MatchString(operation) {
		return "", fmt.Errorf("incus operation identifier is invalid")
	}
	id := strings.TrimPrefix(operation, "/1.0/operations/")
	if !operationIDPattern.MatchString(id) {
		return "", fmt.Errorf("incus operation identifier is invalid")
	}
	deadline, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for attempt := 0; attempt < 60; attempt++ {
		var record operationRecord
		if err := c.do(deadline, http.MethodGet, operation+"/wait?timeout=5", nil, &record); err != nil {
			return "", err
		}
		if record.ID != "" && !strings.EqualFold(record.ID, id) {
			return "", fmt.Errorf("incus operation identity changed while waiting")
		}
		switch record.StatusCode {
		case 200:
			fingerprint, _ := record.Metadata["fingerprint"].(string)
			if !fingerprintPattern.MatchString(fingerprint) {
				return "", fmt.Errorf("incus image import did not return a fingerprint")
			}
			if resources := record.Resources["images"]; len(resources) > 0 && !operationResourcesMatchProject(resources, fingerprint, project) {
				return "", fmt.Errorf("incus image import operation resources do not match the requested project")
			}
			return fingerprint, nil
		case 400, 401, 403, 404, 500:
			return "", fmt.Errorf("incus operation failed")
		}
		if err := deadline.Err(); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("incus operation wait exceeded retry bound")
}

func operationResourcesMatchProject(resources []string, fingerprint, project string) bool {
	for _, resource := range resources {
		parsed, err := url.Parse(resource)
		if err != nil {
			continue
		}
		if parsed.Path == "/1.0/images/"+fingerprint && (parsed.RawQuery == "" || parsed.Query().Get("project") == project) {
			return true
		}
	}
	return false
}

func decodeCertificate(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("value is not a PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		// ParseCertificate may include a rejected SAN URI or name in its
		// error. The same decoder serves admin pins and consumer trust input.
		return nil, fmt.Errorf("value is not a valid X.509 certificate")
	}
	return certificate, nil
}

// certificateFingerprint is the SHA-256 over the DER body, which is the
// identifier Incus uses for a trust store entry.
func certificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
