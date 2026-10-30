// TEST_CASES: MAILMOD-T-072
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/crypto/ssh"
)

// Linux child loads its isolated CA before Go initializes the system root pool.
func TestMailVPSFullHostHandoff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated system trust fixture requires Linux")
	}
	caDir := os.Getenv("ANAS_TEST_VPS_CA_DIR")
	if caDir == "" {
		caDir = t.TempDir()
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		now := time.Now()
		cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated VPS test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"relay.example.test"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
		der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		private, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(caDir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(caDir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestMailVPSFullHostHandoff$", "-test.v")
		cmd.Env = append(os.Environ(), "ANAS_TEST_VPS_CA_DIR="+caDir, "SSL_CERT_FILE="+filepath.Join(caDir, "cert.pem"))
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("child failed: %v\n%s", e, out)
		}
		t.Log(string(out))
		return
	}
	certPEM, e := os.ReadFile(filepath.Join(caDir, "cert.pem"))
	if e != nil {
		t.Fatal(e)
	}
	keyPEM, e := os.ReadFile(filepath.Join(caDir, "key.pem"))
	if e != nil {
		t.Fatal(e)
	}
	certificate, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		t.Fatal(e)
	}
	sourceListener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer sourceListener.Close()
	_, sourcePort, _ := net.SplitHostPort(sourceListener.Addr().String())
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostKey)
	_, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	clientSigner, _ := ssh.NewSignerFromKey(clientKey)
	clientBlock, _ := ssh.MarshalPrivateKey(clientKey, "")
	sshListener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer sshListener.Close()
	_, sshPortText, _ := net.SplitHostPort(sshListener.Addr().String())
	sshPort, _ := strconv.Atoi(sshPortText)
	configs := make(chan cloudrelay.RuntimeConfig, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			conn, err := sshListener.Accept()
			if err != nil {
				return
			}
			cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
				if !bytes.Equal(k.Marshal(), clientSigner.PublicKey().Marshal()) {
					return nil, errors.New("denied")
				}
				return nil, nil
			}}
			cfg.AddHostKey(hostSigner)
			server, channels, requests, err := ssh.NewServerConn(conn, cfg)
			if err != nil {
				conn.Close()
				return
			}
			go ssh.DiscardRequests(requests)
			ch, ok := <-channels
			if !ok {
				server.Close()
				return
			}
			channel, reqs, err := ch.Accept()
			if err != nil {
				server.Close()
				return
			}
			req, ok := <-reqs
			if !ok {
				channel.Close()
				server.Close()
				return
			}
			var command struct{ Command string }
			if req.Type != "exec" || ssh.Unmarshal(req.Payload, &command) != nil || command.Command != "sudo -n /usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap" {
				req.Reply(false, nil)
				channel.Close()
				server.Close()
				return
			}
			req.Reply(true, nil)
			raw, _ := io.ReadAll(io.LimitReader(channel, 96<<10))
			var message struct {
				Action string                   `json:"action"`
				Config cloudrelay.RuntimeConfig `json:"config"`
			}
			if json.Unmarshal(raw, &message) != nil {
				channel.Close()
				server.Close()
				return
			}
			configs <- message.Config
			state := map[string]string{"publish": "published", "activate": "active-local-verified"}[message.Action]
			_, _ = channel.Write([]byte(`{"state":"` + state + `"}`))
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			channel.Close()
			server.Close()
		}
	}()
	defer func() {
		sshListener.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("SSH fixture did not stop")
		}
	}()
	_, workspace := relayHostWorkspace(t)
	parent := t.TempDir()
	_ = os.Chmod(parent, 0700)
	audit := &recordingConfigAuditAppender{}
	call := httpapi.ServiceMailInstallCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Reauthorize: func(context.Context) error { return nil }, Request: httpapi.ServiceMailInstallRequest{Actor: "anchor", Action: "publish", Provider: "vps-ssh", OperationID: "cfg-0123456789abcdef0123456789abcdef", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 8 << 20, TotalCapacityBytes: 8 << 20, DeliveredDays: 7, SourceOrigin: "https://127.0.0.1:" + sourcePort, NotificationOrigin: "https://nas.example.test", SSHHost: "127.0.0.1", SSHPort: sshPort, SSHUser: "relaydeploy", SSHHostKey: string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())), SSHPrivateKey: string(pem.EncodeToMemory(clientBlock)), TLSCertificate: string(certPEM), TLSPrivateKey: string(keyPEM), SMTPHostname: "relay.example.test"}}
	forward := os.Getenv("ANAS_TEST_VPS_FORWARD") == "1"
	if forward {
		call.Request.AccountID, call.Request.ZoneID, call.Request.ForwardDomain = strings.Repeat("1", 32), strings.Repeat("2", 32), "delivery.example.test"
		call.Request.RoutingToken, call.Request.DestinationToken = strings.Repeat("r", 40), strings.Repeat("d", 40)
	}
	handle := newMailInstallHandler(filepath.Join(parent, "installations"), audit)
	first, e := handle(t.Context(), call)
	if e != nil {
		t.Fatal("publish", e)
	}
	firstConfig := <-configs
	call.Request.Action = "activate"
	second, e := handle(t.Context(), call)
	if e != nil || first.SourceID != second.SourceID {
		t.Fatal("activate", e)
	}
	cfg := <-configs
	if cfg.Reader != firstConfig.Reader || cfg.Control != firstConfig.Control {
		t.Fatal("credentials changed")
	}
	if forward {
		raw, _ := json.Marshal(cfg)
		if cfg.ForwardDomain != call.Request.ForwardDomain || bytes.Contains(raw, []byte(call.Request.RoutingToken)) || bytes.Contains(raw, []byte(call.Request.DestinationToken)) {
			t.Fatal("provider credentials leaked or forward domain missing in SSH configuration")
		}
	}
	data := t.TempDir()
	_ = os.Chmod(data, 0700)
	ledger, e := cloudrelay.InitializeRouteLedger(data, cfg.SourceID, cfg.Generation, cfg.Domains)
	if e != nil {
		t.Fatal(e)
	}
	store, e := cloudrelay.InitializeOriginalStore(ledger, cfg.CapacityBytes, cfg.DeliveredDays)
	if e != nil {
		t.Fatal(e)
	}
	notifier, e := cloudrelay.NewNotifier(cloudrelay.NotificationConfig{Origin: cfg.NotificationOrigin, SourceID: cfg.SourceID, Generation: cfg.Generation, Secret: cfg.NotificationSecret, Reader: cfg.Reader, Control: cfg.Control})
	if e != nil {
		t.Fatal(e)
	}
	handler, e := cloudrelay.NewSourceHandler(store, cfg.Reader, cfg.Control, notifier)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forward && r.URL.Path == "/v1/status" {
			if r.Header.Get("Authorization") != "Bearer "+cfg.Reader {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: cfg.SourceID, Generation: cfg.Generation, CapacityBytes: cfg.CapacityBytes, DeliveredDays: cfg.DeliveredDays, AccountQuota: "unknown", Notifications: "configured", ForwardDomain: cfg.ForwardDomain})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	server.Listener.Close()
	server.Listener = sourceListener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	for _, action := range []string{"prepare", "commit"} {
		call.Request.Action = action
		result, err := handle(t.Context(), call)
		if err != nil || result.SourceID != cfg.SourceID || result.State != map[string]string{"prepare": "prepared", "commit": "configured"}[action] {
			t.Fatalf("%s: %+v %v", action, result, err)
		}
	}
	snapshot, e := runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if e != nil {
		t.Fatal(e)
	}
	sources, e := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, []string{"example.test"})
	if e != nil || len(sources) != 1 || sources[0].ID != cfg.SourceID || sources[0].Credentials.Reader != cfg.Reader || sources[0].Credentials.Control != cfg.Control || sources[0].Credentials.Notification != cfg.NotificationSecret {
		t.Fatalf("secret transaction mismatch: %v", e)
	}
	if forward && (sources[0].CloudflareForward == nil || sources[0].CloudflareForward.Domain != call.Request.ForwardDomain || sources[0].CloudflareForward.AccountID != call.Request.AccountID || sources[0].CloudflareForward.ZoneID != call.Request.ZoneID || sources[0].Credentials.Routing != call.Request.RoutingToken || sources[0].Credentials.Destination != call.Request.DestinationToken || strings.Contains(snapshot.Sources, call.Request.RoutingToken) || strings.Contains(snapshot.Sources, call.Request.DestinationToken)) {
		t.Fatal("forward mode or private token handoff lost")
	}
	call.Request.Action = "publish"
	if _, e = handle(t.Context(), call); e == nil {
		t.Fatal("committed operation replayed remote action")
	}
	for _, event := range audit.events {
		raw, _ := json.Marshal(event)
		for _, secret := range []string{cfg.Reader, cfg.Control, cfg.NotificationSecret, string(keyPEM), call.Request.RoutingToken, call.Request.DestinationToken} {
			if secret == "" {
				continue
			}
			if strings.Contains(string(raw), secret) {
				t.Fatal("audit leaked secret")
			}
		}
	}
}
