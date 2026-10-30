//go:build linux

// TEST_CASES: MAILMOD-T-081
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/serviceauth"
	"github.com/anas-project/ANAS/internal/tempconsolecert"
)

// This opt-in test starts the real daemon with isolated state and fake provider
// inputs. Root is needed to create the same cross-UID service credentials as the
// deployed process; no provider or production workspace is contacted.
func TestMailInstallSocketDaemonLive(t *testing.T) {
	if os.Getenv("MAIL_SOCKET_ANASD_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("requires explicit opt-in and root on an isolated Linux host")
	}
	root, err := os.MkdirTemp("/run", "anas-mail-daemon-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	workspace := filepath.Join(root, "workspace")
	socketDir := filepath.Join(root, "socket")
	for _, dir := range []string{filepath.Join(workspace, ".anas"), socketDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(socketDir, 0, 65532); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socketDir, 0750); err != nil {
		t.Fatal(err)
	}
	cert, err := tempconsolecert.Generate(tempconsolecert.Options{Directory: filepath.Join(root, "cert"), IPAddresses: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(cert.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("invalid test certificate")
	}
	store := filepath.Join(root, "console")
	writer, err := audit.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	state, err := consolestate.Open(context.Background(), store, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []consolestate.State{consolestate.StateEnrollment, consolestate.StateFull} {
		if _, err := state.Transition(context.Background(), next, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := consoleconfig.Config{Mode: consoleconfig.ModeLoopback, Port: 18445, ConsoleStore: store,
		Workspaces:        []consoleconfig.Workspace{{ID: "main", Path: workspace}},
		TLS:               consoleconfig.TLSConfig{Temporary: &consoleconfig.TemporaryTLSPaths{Certificate: cert.CertificatePath, PrivateKey: cert.PrivateKeyPath, IPAddresses: []string{"127.0.0.1"}}},
		ServiceAPI:        &serviceauth.Config{Directory: filepath.Join(root, "clients"), Clients: []serviceauth.Client{{ID: "mail-test", Enabled: true, UID: 65532, GID: 65532, Grants: []serviceauth.Grant{{Permission: "mail.ingress.install", Workspace: "main", Resources: []string{"mail"}}}}}},
		MailInstallSocket: &consoleconfig.MailInstallSocketConfig{Directory: socketDir, GroupID: 65532},
	}
	var mu sync.RWMutex
	enabled := true
	reload := func() (consoleconfig.Config, error) {
		mu.RLock()
		defer mu.RUnlock()
		c := cfg
		s := *cfg.ServiceAPI
		s.Clients = append([]serviceauth.Client(nil), s.Clients...)
		s.Clients[0].Enabled = enabled
		c.ServiceAPI = &s
		return c, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runConfiguredWithServiceReload(ctx, cfg, nil, func(_ context.Context, network, _ string) (net.Listener, error) {
			if network == "tcp6" {
				return nil, syscall.EAFNOSUPPORT
			}
			return net.Listen("tcp4", "127.0.0.1:0")
		}, reload)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon failed to stop")
		}
	})
	socket := filepath.Join(socketDir, "install.sock")
	tokenPath := filepath.Join(cfg.ServiceAPI.Directory, "mail-test", "token")
	var token []byte
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		token, err = os.ReadFile(tokenPath)
		if err == nil {
			if _, err = os.Stat(socket); err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		select {
		case e := <-done:
			done <- e
			t.Fatalf("daemon startup: %v", e)
		default:
			t.Fatal(err)
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	body := `{"actor":"anchor","action":"prepare","operation_id":"cfg-0123456789abcdef0123456789abcdef","account_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","worker":"mail-worker","bucket":"mail-bucket","zone_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","domains":["example.test"],"allowed_domains":["example.test"],"drain_ids":[],"capacity_bytes":10,"total_capacity_bytes":10,"delivered_days":7,"deployment_token":"fake-deploy-token-value","routing_token":"fake-routing-token-value"}`
	call := func(path, bearer, host string) int {
		t.Helper()
		req, err := http.NewRequest("POST", "https://127.0.0.1:18445"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		t.Logf("response %d: %s", resp.StatusCode, data)
		return resp.StatusCode
	}
	endpoint := "/api/v1/service/workspaces/main/applications/mail/mail-ingress-install"
	// The workspace intentionally has no configuration: the actual installer
	// must fail closed before any provider operation, after successful auth.
	if got := call(endpoint, strings.TrimSpace(string(token)), ""); got != 503 {
		t.Fatalf("actual installer status %d, want 503", got)
	}
	if got := call(endpoint, "bad-token", ""); got != 401 {
		t.Fatalf("bad token status %d", got)
	}
	if got := call("/api/v1/service/workspaces/main/applications/mail/credentials", strings.TrimSpace(string(token)), ""); got != 404 {
		t.Fatalf("credentials exposed: %d", got)
	}
	if got := call(endpoint, strings.TrimSpace(string(token)), "attacker.invalid"); got != 400 {
		t.Fatalf("invalid host status %d", got)
	}
	if os.Getenv("MAIL_SOCKET_DOCKER_LIVE") == "1" {
		checkDaemonSocketContainer(t, root, socketDir, filepath.Dir(tokenPath), certPEM, body)
	}
	mu.Lock()
	enabled = false
	mu.Unlock()
	if got := call(endpoint, strings.TrimSpace(string(token)), ""); got != 401 && got != 403 {
		t.Fatalf("revoked token status %d", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("daemon failed to stop")
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after shutdown: %v", err)
	}
}

func checkDaemonSocketContainer(t *testing.T, root, socketDir, tokenDir string, certPEM []byte, body string) {
	t.Helper()
	const image = "ghcr.io/docker-mailserver/docker-mailserver:16.0.1"
	const imageID = "sha256:d0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86"
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		b, err := exec.CommandContext(ctx, "/usr/bin/docker", args...).CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	if id, err := run("image", "inspect", image, "--format", "{{.Id}}"); err != nil || id != imageID {
		t.Fatal("pinned image unavailable")
	}
	caPath := filepath.Join(root, "public-ca.pem")
	if err := os.WriteFile(caPath, certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(caPath, 0644); err != nil {
		t.Fatal(err)
	}
	script := `import socket,ssl,sys
body=sys.argv[1].encode();token=open('/token/token').read().strip()
def call(path,host):
 s=socket.socket(socket.AF_UNIX);s.settimeout(10);s.connect('/socket/install.sock')
 c=ssl.create_default_context(cafile='/ca.pem').wrap_socket(s,server_hostname='127.0.0.1')
 h=('POST '+path+' HTTP/1.1\r\nHost: '+host+'\r\nAuthorization: Bearer '+token+'\r\nContent-Type: application/json\r\nContent-Length: '+str(len(body))+'\r\nConnection: close\r\n\r\n').encode()
 c.sendall(h+body); data=b''
 while True:
  chunk=c.recv(4096)
  if not chunk: break
  data+=chunk
 c.close();return data
path='/api/v1/service/workspaces/main/applications/mail/mail-ingress-install'
r=call(path,'127.0.0.1:18445');assert r.startswith(b'HTTP/1.1 503 ') and b'mail_install_unknown' in r
assert call(path,'attacker.invalid').startswith(b'HTTP/1.1 400 ')
assert call('/api/v1/service/workspaces/main/applications/mail/credentials','127.0.0.1:18445').startswith(b'HTTP/1.1 404 ')
print('real daemon socket and service grant accepted from container')`
	label := filepath.Base(root)
	id, err := run("create", "--pull", "never", "--label", "anas.mail.daemon-test="+label, "--network", "none", "--memory", "128m", "--pids-limit", "64", "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,src="+socketDir+",dst=/socket,readonly", "--mount", "type=bind,src="+tokenDir+",dst=/token,readonly", "--mount", "type=bind,src="+caPath+",dst=/ca.pem,readonly", "--entrypoint", "python3", image, "-c", script, body)
	if err != nil || len(id) != 64 {
		t.Fatal("fixture container creation failed")
	}
	t.Cleanup(func() {
		observed, err := run("inspect", "--format", `{{index .Config.Labels "anas.mail.daemon-test"}}`, id)
		if err != nil || observed != label {
			t.Error("fixture ownership unknown; not removed")
			return
		}
		if _, err := run("rm", "-f", "-v", id); err != nil {
			t.Error("fixture removal failed")
		}
	})
	output, err := run("start", "-a", id)
	if err != nil || !strings.Contains(output, "real daemon socket and service grant accepted from container") {
		t.Fatalf("fixture failed: %v %s", err, output)
	}
	clientBinary := os.Getenv("MAIL_SOCKET_CLIENT_BINARY")
	if clientBinary == "" {
		return
	}
	program, err := os.ReadFile(clientBinary)
	if err != nil {
		t.Fatal(err)
	}
	clientPath := filepath.Join(root, "mail-client-test")
	if err := os.WriteFile(clientPath, program, 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(clientPath, 0555); err != nil {
		t.Fatal(err)
	}
	operation := filepath.Join(root, "mail-installations", "main", "cfg-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	if _, err := os.Lstat(operation); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("client operation already exists")
	}
	clientID, err := run("create", "--pull", "never", "--label", "anas.mail.daemon-test="+label, "--network", "none", "--memory", "128m", "--pids-limit", "64", "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--env", "MAIL_SOCKET_CLIENT_LIVE=1", "--mount", "type=bind,src="+socketDir+",dst=/socket,readonly", "--mount", "type=bind,src="+tokenDir+",dst=/token,readonly", "--mount", "type=bind,src="+caPath+",dst=/ca.pem,readonly", "--mount", "type=bind,src="+clientPath+",dst=/client,readonly", "--entrypoint", "/client", image, "-test.v", "-test.run", "^TestMailInstallerRealDaemonLive$")
	if err != nil || len(clientID) != 64 {
		t.Fatal("mail client container creation failed")
	}
	t.Cleanup(func() {
		observed, err := run("inspect", "--format", `{{index .Config.Labels "anas.mail.daemon-test"}}`, clientID)
		if err != nil || observed != label {
			t.Error("mail client fixture ownership unknown; not removed")
			return
		}
		if _, err := run("rm", "-f", "-v", clientID); err != nil {
			t.Error("mail client fixture removal failed")
		}
	})
	output, err = run("start", "-a", clientID)
	if err != nil || !strings.Contains(output, "--- PASS: TestMailInstallerRealDaemonLive") {
		t.Fatalf("mail client fixture failed: %v %s", err, output)
	}
	if info, err := os.Stat(operation); err != nil || !info.IsDir() {
		t.Fatal("actual mail client did not reach real installation handler")
	}
	adminOperation := filepath.Join(root, "mail-installations", "main", "cfg-ffffffffffffffffffffffffffffffff")
	if info, err := os.Stat(adminOperation); err != nil || !info.IsDir() {
		t.Fatal("administrator handler did not reach real installation handler")
	}
}
