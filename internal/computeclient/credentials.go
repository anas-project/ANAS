package computeclient

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
	"time"
)

var errClientCredentialState = errors.New("compute credentials require unchanged private owned directories and matching single-link files; use a separate directory for a new identity")

type credentialItem struct {
	name   string
	server bool
	body   []byte
}

func (c *Client) writeCredentials(configDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.writeCredentialsContext(ctx, configDir)
}

// Verify the whole TLS tuple before filesystem changes. Existing files are
// immutable inputs: reuse matching bytes, never truncate a pathname to rotate
// identities or repair uncertain writes.
func (c *Client) writeCredentialsContext(ctx context.Context, configDir string) error {
	if ctx == nil {
		return fmt.Errorf("compute credential preparation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	server, err := decodeB64(c.lease.ServerCertB64)
	if err != nil {
		return err
	}
	defer clear(server)
	pin, err := certFingerprint(server)
	if err != nil {
		return err
	}
	if pin != c.lease.ServerCertFingerprint {
		return fmt.Errorf("compute lease server certificate does not match its published fingerprint")
	}
	certificate, err := decodeB64(c.lease.ClientCertB64)
	if err != nil {
		return err
	}
	defer clear(certificate)
	if _, err := singleComputeCertificate(certificate); err != nil {
		return err
	}
	key, err := decodeB64(c.lease.ClientKeyB64)
	if err != nil {
		return err
	}
	defer clear(key)
	block, rest := pem.Decode(bytes.TrimSpace(key))
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 ||
		(block.Type != "PRIVATE KEY" && block.Type != "RSA PRIVATE KEY" && block.Type != "EC PRIVATE KEY") ||
		!bytes.HasPrefix(bytes.TrimSpace(key), []byte("-----BEGIN "+block.Type+"-----")) {
		return fmt.Errorf("compute client private key must contain exactly one private-key PEM block")
	}
	defer clear(block.Bytes)
	if _, err := tls.X509KeyPair(certificate, key); err != nil {
		return fmt.Errorf("compute client certificate and private key do not form a usable TLS identity")
	}
	// JSON is a YAML-compatible representation of the Incus CLI config. Use
	// the standard encoder so endpoint characters cannot inject YAML fields.
	// This file is checked with the same no-overwrite protocol as the keypair.
	configuration, err := json.Marshal(map[string]any{
		"default-remote": remoteName,
		"remotes": map[string]any{remoteName: map[string]any{
			"addr": c.lease.Endpoint, "auth_type": "tls", "project": c.lease.Sandbox,
			"protocol": "incus", "public": false,
		}},
	})
	if err != nil {
		return fmt.Errorf("encode private compute connection")
	}
	return publishClientCredentials(ctx, configDir, []credentialItem{
		{name: "client.crt", body: certificate}, {name: "client.key", body: key},
		{name: remoteName + ".crt", server: true, body: server},
		{name: "config.yml", body: append(configuration, '\n')},
	})
}

func singleComputeCertificate(body []byte) (*x509.Certificate, error) {
	trimmed := bytes.TrimSpace(body)
	block, rest := pem.Decode(trimmed)
	if len(body) > 64<<10 || block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.HasPrefix(trimmed, []byte("-----BEGIN CERTIFICATE-----")) {
		return nil, fmt.Errorf("compute TLS certificate must contain exactly one bounded certificate PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("compute TLS certificate is invalid")
	}
	return certificate, nil
}

func certFingerprint(body []byte) (string, error) {
	certificate, err := singleComputeCertificate(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(sum[:]), nil
}
