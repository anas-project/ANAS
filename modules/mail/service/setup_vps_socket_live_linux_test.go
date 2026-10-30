//go:build linux

// TEST_CASES: MAILMOD-T-073, MAILMOD-T-081
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

// Executed only by the isolated Docker/real anasd fixture. The session and
// directory are synthetic; the host daemon and remote SSH helper are real.
func TestMailVPSAdminSocketClientLive(t *testing.T) {
	if os.Getenv("MAIL_VPS_ADMIN_CLIENT_LIVE") != "1" {
		t.Skip("isolated admin fixture required")
	}
	cert, err := os.ReadFile("/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid test CA")
	}
	t.Setenv("MAIL_SERVICE_API_URL", "https://127.0.0.1:18445")
	t.Setenv("MAIL_SERVICE_API_WORKSPACE", "main")
	t.Setenv("MAIL_SERVICE_API_TOKEN_FILE", "/token/token")
	t.Setenv("MAIL_SERVICE_API_SOCKET_FILE", "/socket/install.sock")
	client, err := configuredMailInstaller([]string{"example.test"}, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.ReadFile("/ssh/client")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(private)
	hostKey, err := os.ReadFile("/ssh/hostkey.pub")
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(os.Getenv("ANAS_TEST_VPS_SSH_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.example.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"relay.example.test"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(keyDER)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	full := os.Getenv("MAIL_VPS_ADMIN_FULL") == "1"
	if full {
		certPEM, err = os.ReadFile("/ssh/relay.crt")
		if err != nil {
			t.Fatal(err)
		}
		keyPEM, err = os.ReadFile("/ssh/relay.key")
		if err != nil {
			t.Fatal(err)
		}
	}
	defer clear(keyPEM)
	h, login, _, dir := loginFixture(t)
	draft := store.SetupDraft{Inbound: "vps-direct", Domain: "example.test", SourceURL: "https://relay.example.test:18448", LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 8 << 20, DeliveredDays: 7}, NotificationURL: "https://nas.example.test"}
	if full {
		if _, err := strconv.Atoi(os.Getenv("ANAS_TEST_VPS_SOURCE_PORT")); err != nil {
			t.Fatal("missing source port")
		}
		draft.SourceURL = "https://127.0.0.1:" + os.Getenv("ANAS_TEST_VPS_SOURCE_PORT")
		if _, err := strconv.Atoi(os.Getenv("ANAS_TEST_VPS_NOTIFY_PORT")); err != nil {
			t.Fatal("missing notification port")
		}
		draft.NotificationURL = "https://127.0.0.1:" + os.Getenv("ANAS_TEST_VPS_NOTIFY_PORT")
	}
	h.store = &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: draft}}
	h.directory, h.install = &setupHTTPDirectory{httpDirectory: dir, admin: true}, client
	input := vpsInstallInput{Revision: 3, Action: "publish", OperationID: "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CapacityBytes: 8 << 20, DrainIDs: []string{}, SSHHost: "127.0.0.1", SSHPort: port, SSHUser: "anasmaildeploy", SSHHostKey: string(hostKey), SSHPrivateKey: string(private), TLSCertificate: string(certPEM), TLSPrivateKey: string(keyPEM), SMTPHostname: "relay.example.test"}
	if os.Getenv("MAIL_VPS_ADMIN_FORWARD") == "1" {
		draft.Inbound = "cloud-relay"
		h.store = &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: draft}}
		input.AccountID, input.ZoneID, input.ForwardDomain = strings.Repeat("1", 32), strings.Repeat("2", 32), "delivery.example.test"
		input.RoutingToken, input.DestinationToken = strings.Repeat("r", 40), strings.Repeat("d", 40)
	}
	var source string
	actions := []string{"publish", "publish"}
	if full {
		actions = append(actions, "activate", "prepare", "commit")
	}
	for n, action := range actions {
		input.Action = action
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "https://mail.example.test/api/setup/vps-install", strings.NewReader(string(raw)))
		clear(raw)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
		req.Header.Set("Origin", "https://mail.example.test")
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		h.ServeHTTP(resp, req)
		if resp.Code != 200 || strings.Contains(resp.Body.String(), string(private)) || strings.Contains(resp.Body.String(), string(keyPEM)) || input.RoutingToken != "" && strings.Contains(resp.Body.String(), input.RoutingToken) || input.DestinationToken != "" && strings.Contains(resp.Body.String(), input.DestinationToken) {
			t.Fatalf("administrator publish response %d", resp.Code)
		}
		var result struct {
			State    string `json:"state"`
			SourceID string `json:"source_id"`
			Enabled  bool   `json:"enabled"`
		}
		wantState := map[string]string{"publish": "published", "activate": "activated", "prepare": "prepared", "commit": "configured"}[action]
		if json.Unmarshal(resp.Body.Bytes(), &result) != nil || result.State != wantState || result.SourceID == "" || result.Enabled {
			t.Fatalf("invalid %s response", action)
		}
		if n > 0 && result.SourceID != source {
			t.Fatal("retry changed source")
		}
		source = result.SourceID
	}
}
