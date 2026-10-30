//go:build linux

// TEST_CASES: MAILMOD-T-072, MAILMOD-T-074
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
)

// The fixture first lacks a unit directory, then explicitly creates one inside
// its private mount namespace. It exercises actual conflict, publication and
// retry without reloading or activating the host's systemd manager.
func TestMailVPSRealHostPublish(t *testing.T) {
	portValue := os.Getenv("ANAS_TEST_VPS_SSH_PORT")
	if portValue == "" {
		t.Skip("explicit isolated SSH fixture required")
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := os.ReadFile(os.Getenv("ANAS_TEST_VPS_SSH_HOSTKEY"))
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile(os.Getenv("ANAS_TEST_VPS_SSH_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(private)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.example.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"relay.example.test"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	_, workspace := relayHostWorkspace(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	audit := &recordingConfigAuditAppender{}
	call := httpapi.ServiceMailInstallCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Reauthorize: func(context.Context) error { return nil }, Request: httpapi.ServiceMailInstallRequest{Actor: "anchor", Action: "publish", Provider: "vps-ssh", OperationID: "cfg-0123456789abcdef0123456789abcdef", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 8 << 20, TotalCapacityBytes: 8 << 20, DeliveredDays: 7, SourceOrigin: "https://relay.example.test:18448", NotificationOrigin: "https://nas.example.test", SSHHost: "127.0.0.1", SSHPort: port, SSHUser: "anasmaildeploy", SSHHostKey: string(hostKey), SSHPrivateKey: string(private), TLSCertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), TLSPrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), SMTPHostname: "relay.example.test"}}
	handle := newMailInstallHandler(filepath.Join(parent, "installations"), audit)
	success := os.Getenv("ANAS_TEST_VPS_PUBLISH_SUCCESS") == "1"
	result, err := handle(t.Context(), call)
	if success {
		if err != nil || result.State != "published" || result.SourceID == "" {
			t.Fatalf("real publish failed: %v %q", err, result.State)
		}
		second, err := handle(t.Context(), call)
		if err != nil || second.State != "published" || second.SourceID != result.SourceID {
			t.Fatalf("real publish retry failed: %v %q", err, second.State)
		}
	} else if !errors.Is(err, httpapi.ErrMailInstallConflict) || result.State != "" {
		t.Fatalf("publish conflict was not preserved: %v %q", err, result.State)
	}
	provision := filepath.Join(parent, "installations", "main", call.Request.OperationID, "vps-ssh", "provision")
	if _, err := os.Stat(filepath.Join(provision, "remote-publish.issued")); err != nil {
		t.Fatal("remote publish intent missing", err)
	}
	_, doneErr := os.Stat(filepath.Join(provision, "remote-publish.done"))
	if success && doneErr != nil || !success && !errors.Is(doneErr, os.ErrNotExist) {
		t.Fatal("remote publish completion marker incorrect", doneErr)
	}
	if !success {
		call.Request.Action = "activate"
		if result, err := handle(t.Context(), call); err == nil || result.State != "" {
			t.Fatalf("unpublished source activated: %v %q", err, result.State)
		}
	}
	for _, event := range audit.events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), string(private)) || strings.Contains(string(raw), call.Request.TLSPrivateKey) {
			t.Fatal("private key in audit")
		}
	}
}
