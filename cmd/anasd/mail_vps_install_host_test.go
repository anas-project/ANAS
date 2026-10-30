// TEST_CASES: MAILMOD-T-072
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"golang.org/x/crypto/ssh"
)

func TestMailVPSHostPublishesAndActivatesBoundSource(t *testing.T) {
	_, hostKey, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostKey)
	_, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	clientSigner, _ := ssh.NewSignerFromKey(clientKey)
	private, _ := ssh.MarshalPrivateKey(clientKey, "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	calls := make(chan cloudrelay.RuntimeConfig, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
				if !bytes.Equal(k.Marshal(), clientSigner.PublicKey().Marshal()) {
					return nil, errors.New("denied")
				}
				return nil, nil
			}}
			config.AddHostKey(hostSigner)
			server, channels, requests, e := ssh.NewServerConn(conn, config)
			if e != nil {
				conn.Close()
				return
			}
			go ssh.DiscardRequests(requests)
			ch, ok := <-channels
			if !ok {
				server.Close()
				return
			}
			channel, reqs, e := ch.Accept()
			if e != nil {
				server.Close()
				return
			}
			req, ok := <-reqs
			if !ok {
				channel.Close()
				server.Close()
				return
			}
			var cmd struct{ Command string }
			if req.Type != "exec" || ssh.Unmarshal(req.Payload, &cmd) != nil || cmd.Command != "sudo -n /usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap" {
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
			calls <- message.Config
			state := map[string]string{"publish": "published", "activate": "active-local-verified"}[message.Action]
			_, _ = channel.Write([]byte(`{"state":"` + state + `"}`))
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
			channel.Close()
			server.Close()
		}
	}()
	defer func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("SSH fixture failed to stop")
		}
	}()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "relay.example.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), DNSNames: []string{"relay.example.test"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	_, workspace := relayHostWorkspace(t)
	parent := t.TempDir()
	_ = os.Chmod(parent, 0700)
	writer := &recordingConfigAuditAppender{}
	allowed := true
	call := httpapi.ServiceMailInstallCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Reauthorize: func(context.Context) error {
		if !allowed {
			return errors.New("revoked")
		}
		return nil
	}, Request: httpapi.ServiceMailInstallRequest{Actor: "anchor", Action: "publish", Provider: "vps-ssh", OperationID: "cfg-0123456789abcdef0123456789abcdef", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 8 << 20, TotalCapacityBytes: 8 << 20, DeliveredDays: 7, SourceOrigin: "https://relay.example.test:18448", NotificationOrigin: "https://nas.example.test", SSHHost: "127.0.0.1", SSHPort: port, SSHUser: "relaydeploy", SSHHostKey: string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())), SSHPrivateKey: string(pem.EncodeToMemory(private)), TLSCertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), TLSPrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), SMTPHostname: "relay.example.test"}}
	handle := newMailInstallHandler(filepath.Join(parent, "installations"), writer)
	first, err := handle(t.Context(), call)
	if err != nil || first.State != "published" {
		t.Fatalf("publish: %+v %v", first, err)
	}
	call.Request.Action = "activate"
	allowed = false
	if _, err = handle(t.Context(), call); err == nil {
		t.Fatal("revoked activation succeeded")
	}
	allowed = true
	second, err := handle(t.Context(), call)
	if err != nil || second.State != "activated" || second.SourceID != first.SourceID {
		t.Fatalf("activate: %+v %v", second, err)
	}
	a, b := <-calls, <-calls
	if a.SourceID != first.SourceID || a.SourceID != b.SourceID || a.Reader != b.Reader || a.Control != b.Control || a.NotificationSecret != b.NotificationSecret {
		t.Fatal("source credentials changed across steps")
	}
	for _, event := range writer.events {
		raw, _ := json.Marshal(event)
		for _, secret := range []string{a.Reader, a.Control, a.NotificationSecret, call.Request.SSHPrivateKey, call.Request.TLSPrivateKey} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("credential leaked into audit")
			}
		}
	}
	guard := filepath.Join(parent, "installations", "main", call.Request.OperationID, "vps-ssh", "relay", "commit-guard.json")
	if err = os.WriteFile(guard, []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	call.Request.Action = "publish"
	if _, err = handle(t.Context(), call); err == nil {
		t.Fatal("old remote operation revived after commit intent")
	}
}
