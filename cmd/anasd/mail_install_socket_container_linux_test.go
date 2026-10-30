//go:build linux

// TEST_CASES: MAILMOD-T-081
package main

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit opt-in only: disposable, disconnected container on the default
// daemon; never mounts a workspace or modifies the existing mail stack.
func TestMailInstallSocketReadOnlyContainerLive(t *testing.T) {
	if os.Getenv("MAIL_SOCKET_DOCKER_LIVE") != "1" {
		t.Skip("explicit isolated Docker fixture required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("root required for test group ownership")
	}
	const image = "ghcr.io/docker-mailserver/docker-mailserver:16.0.1"
	const imageID = "sha256:d0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86"
	cmd := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		b, e := exec.CommandContext(ctx, "/usr/bin/docker", args...).CombinedOutput()
		return strings.TrimSpace(string(b)), e
	}
	got, err := cmd("image", "inspect", image, "--format", "{{.Id}}")
	if err != nil || got != imageID {
		t.Fatal("pinned image unavailable")
	}
	dir, err := os.MkdirTemp("/run", "anas-mail-socket-live-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	if os.Chown(dir, 0, 65532) != nil || os.Chmod(dir, 0750) != nil {
		t.Fatal("test directory permissions")
	}
	l, err := listenMailInstallSocket(dir, 65532)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := l.Close(); e != nil {
			t.Error(e)
		}
	})
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsServer.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw})
	if err = os.WriteFile(filepath.Join(dir, "ca.pem"), ca, 0644); err != nil {
		t.Fatal(err)
	}
	// The remote launcher uses umask 077; the public test CA must still be
	// readable by the non-root container for certificate verification.
	if err = os.Chmod(filepath.Join(dir, "ca.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveMailInstallSocket(ctx, l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), tlsServer.TLS.Clone())
	}()
	t.Cleanup(func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	script := `import socket,ssl
s=socket.socket(socket.AF_UNIX);s.settimeout(10);s.connect('/socket/install.sock')
c=ssl.create_default_context(cafile='/socket/ca.pem').wrap_socket(s,server_hostname='127.0.0.1')
c.sendall(b'POST /api/v1/service/workspaces/main/applications/mail/mail-ingress-install HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 0\r\nConnection: close\r\n\r\n')
r=c.recv(256);assert r.startswith(b'HTTP/1.1 204 '),r
c.close();print('read-only socket TLS accepted')`
	label := filepath.Base(dir)
	id, err := cmd("create", "--pull", "never", "--label", "anas.mail.socket-test="+label, "--network", "none", "--memory", "128m", "--pids-limit", "64", "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--mount", "type=bind,src="+dir+",dst=/socket,readonly", "--entrypoint", "python3", image, "-c", script)
	if err != nil || len(id) != 64 {
		t.Fatal("fixture container creation failed")
	}
	t.Cleanup(func() {
		observed, e := cmd("inspect", "--format", `{{index .Config.Labels "anas.mail.socket-test"}}`, id)
		if e != nil || observed != label {
			t.Error("fixture ownership unknown; not removed")
			return
		}
		if _, e = cmd("rm", "-f", "-v", id); e != nil {
			t.Error("fixture removal failed")
		}
	})
	mount, err := cmd("inspect", "--format", `{{(index .Mounts 0).RW}}`, id)
	if err != nil || mount != "false" {
		t.Fatal("socket mount is not read-only")
	}
	output, err := cmd("start", "-a", id)
	if err != nil || !strings.Contains(output, "read-only socket TLS accepted") {
		t.Fatal("read-only container could not connect", fmt.Sprintf("%v %s", err, output))
	}
}
