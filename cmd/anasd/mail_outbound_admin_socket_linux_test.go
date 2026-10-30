//go:build linux

// TEST_CASES: MAILMOD-T-017 MAILMOD-T-081
package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/internal/serviceauth"
	"github.com/anas-project/ANAS/internal/tempconsolecert"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

// Requires the explicitly leased, disposable Docker daemon. It does not
// access a production workspace, mailbox, upstream SMTP server or queue.
func TestMailOutboundAdminSocketLive(t *testing.T) {
	if os.Getenv("MAIL_OUTBOUND_ADMIN_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("isolated root and Docker fixture required")
	}
	binary := os.Getenv("MAIL_OUTBOUND_SERVICE_TEST")
	if binary == "" {
		t.Fatal("missing service test binary")
	}
	root, err := os.MkdirTemp("/run", "anas-mail-outbound-admin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	workspace := mailOutboundWorkspace(t)
	socketDir := filepath.Join(root, "socket")
	if os.Mkdir(socketDir, 0700) != nil || os.Chown(socketDir, 0, 65532) != nil || os.Chmod(socketDir, 0750) != nil {
		t.Fatal("socket directory setup failed")
	}
	cert, err := tempconsolecert.Generate(tempconsolecert.Options{Directory: filepath.Join(root, "cert"), IPAddresses: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(cert.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(root, "ca.pem")
	if os.WriteFile(caPath, ca, 0644) != nil || os.Chmod(caPath, 0644) != nil {
		t.Fatal("fixture CA setup failed")
	}
	console := filepath.Join(root, "console")
	writer, err := audit.Open(console)
	if err != nil {
		t.Fatal(err)
	}
	state, err := consolestate.Open(context.Background(), console, consoleaudit.StateSink{Writer: writer})
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
	cfg := consoleconfig.Config{Mode: consoleconfig.ModeLoopback, Port: 18445, ConsoleStore: console, Workspaces: []consoleconfig.Workspace{{ID: "main", Path: workspace}}, TLS: consoleconfig.TLSConfig{Temporary: &consoleconfig.TemporaryTLSPaths{Certificate: cert.CertificatePath, PrivateKey: cert.PrivateKeyPath, IPAddresses: []string{"127.0.0.1"}}}, ServiceAPI: &serviceauth.Config{Directory: filepath.Join(root, "clients"), Clients: []serviceauth.Client{{ID: "mail-test", Enabled: true, UID: 65532, GID: 65532, Grants: []serviceauth.Grant{{Permission: "mail.outbound.configure", Workspace: "main", Resources: []string{"mail"}}}}}}, MailInstallSocket: &consoleconfig.MailInstallSocketConfig{Directory: socketDir, GroupID: 65532}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runConfiguredWithServiceReload(ctx, cfg, nil, func(_ context.Context, network, _ string) (net.Listener, error) {
			if network == "tcp6" {
				return nil, syscall.EAFNOSUPPORT
			}
			return net.Listen("tcp4", "127.0.0.1:0")
		}, nil)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon shutdown timed out")
		}
	})
	socket := filepath.Join(socketDir, "install.sock")
	tokenDir := filepath.Join(cfg.ServiceAPI.Directory, "mail-test")
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, socketErr := os.Stat(socket)
		_, tokenErr := os.Stat(filepath.Join(tokenDir, "token"))
		if socketErr == nil && tokenErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture daemon not ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	service, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(root, "service-test")
	if os.WriteFile(servicePath, service, 0555) != nil || os.Chmod(servicePath, 0555) != nil {
		t.Fatal("service binary setup failed")
	}
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/docker", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	const img = "ghcr.io/docker-mailserver/docker-mailserver:16.0.1"
	const imageID = "sha256:d0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86"
	if id, err := run("image", "inspect", img, "--format", "{{.Id}}"); err != nil || id != imageID {
		t.Fatal("pinned test image unavailable")
	}
	label := filepath.Base(root)
	id, err := run("create", "--pull", "never", "--label", "anas.mail.outbound-admin-test="+label, "--network", "none", "--memory", "192m", "--pids-limit", "64", "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--env", "MAIL_OUTBOUND_ADMIN_CLIENT_LIVE=1", "--mount", "type=bind,src="+socketDir+",dst=/socket,readonly", "--mount", "type=bind,src="+tokenDir+",dst=/token,readonly", "--mount", "type=bind,src="+caPath+",dst=/ca.pem,readonly", "--mount", "type=bind,src="+servicePath+",dst=/client,readonly", "--entrypoint", "/client", img, "-test.v", "-test.run", "^TestMailOutboundAdminSocketClientLive$")
	if err != nil || len(id) != 64 {
		t.Fatal("fixture container creation failed")
	}
	t.Cleanup(func() {
		observed, err := run("inspect", "--format", `{{index .Config.Labels "anas.mail.outbound-admin-test"}}`, id)
		if err != nil || observed != label {
			t.Error("fixture container ownership unknown")
			return
		}
		if _, err = run("rm", "-f", "-v", id); err != nil {
			t.Error("fixture container cleanup failed")
		}
	})
	output, err := run("start", "-a", id)
	if err != nil || !strings.Contains(output, "--- PASS: TestMailOutboundAdminSocketClientLive") || strings.Contains(output, "synthetic-smtp-password") {
		t.Fatal("non-root administrator client failed")
	}
	snapshot, err := runner.ReadMailOutboundSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal("managed snapshot unavailable")
	}
	config, err := smtpconfig.Parse(snapshot.Routing, snapshot.Passwords)
	if err != nil || len(config.Profiles) != 1 || config.Profiles[0].Provider != "smtp" || config.Profiles[0].Password != "synthetic-smtp-password" {
		t.Fatal("host configuration not verified")
	}
	public, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || bytes.Contains(public, []byte("synthetic-smtp-password")) {
		t.Fatal("public configuration leaked password")
	}
}
