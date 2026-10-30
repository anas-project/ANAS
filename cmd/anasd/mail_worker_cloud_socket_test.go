// TEST_CASES: MAILMOD-T-081
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func workerCloudWorkspace(t *testing.T) string {
	t.Helper()
	root, workspace := t.TempDir(), t.TempDir()
	for _, module := range []string{"mail", "samba_dc"} {
		dir := filepath.Join(root, module)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		config := "config:\n  types: {mail_management_policy: string}\n  defaults: {mail_management_policy: ''}\n"
		if module == "mail" {
			config = "config:\n  types: {ingress_sources: string, ingress_credentials: string, ingress_capacity_bytes: int}\n  defaults: {ingress_sources: '', ingress_credentials: '', ingress_capacity_bytes: 0}\n  changes:\n    ingress_sources: {effect: container_recreate}\n    ingress_capacity_bytes: {effect: container_recreate}\n    ingress_credentials: {effect: container_recreate, sensitive: true, store_input: true}\n"
		}
		manifest := "api_version: anas.module/v1\nkind: Module\nname: " + module + "\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi:\n  supports: [anas.module-hook/v1]\nruntime:\n  type: builtin\n" + config
		if err := os.WriteFile(filepath.Join(dir, "module.yml"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := `{"domains":["mail-poc.bengbu.cc.cd"],"protected_addresses":[],"max_additional":2,"max_aliases":10,"capacity_bytes":2147483648,"send_per_minute":30}`
	body := []byte(fmt.Sprintf("module_source: official\nmodules:\n  mail: {}\n  samba_dc:\n    config:\n      mail_management_policy: %q\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n", policy))
	state := filepath.Join(workspace, ".anas")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "config.yml"), body, 0600); err != nil {
		t.Fatal(err)
	}
	managed := fmt.Sprintf("api_version: anas.config/v1\ndigest: sha256:%x\nvalidator: cfgv-%s\nupdated_by: test\n", sha256.Sum256(body), strings.Repeat("1", 64))
	if err := os.WriteFile(filepath.Join(state, "config-managed.yml"), []byte(managed), 0600); err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(map[string]string{"api_version": "anas.module-view/v1", "module_root": root})
	if err := os.WriteFile(filepath.Join(state, "module-view.json"), view, 0600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

// The management credentials never leave this local machine. The host API,
// service token, TLS Unix socket, configuration transaction and cloud are real;
// the administrator session and directory in the child are synthetic.
func TestWorkerAdminRealHostCloudSocket(t *testing.T) {
	if os.Getenv("MAIL_WORKER_ADMIN_HOST_CLOUD_TEST") != "1" {
		t.Skip("explicit isolated cloud host fixture required")
	}
	base, child, cleanup := os.Getenv("MAIL_WORKER_HOST_FIXTURE_DIR"), os.Getenv("MAIL_WORKER_SERVICE_TEST_BINARY"), os.Getenv("MAIL_WORKER_INSTALLER_TEST_BINARY")
	if !filepath.IsAbs(base) || !filepath.IsAbs(child) || !filepath.IsAbs(cleanup) {
		t.Fatal("explicit private fixture paths required")
	}
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("private fixture directory required")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "anas-reserve-poc-" + hex.EncodeToString(random[:12])
	operation := "cfg-" + hex.EncodeToString(random[:])
	workspace, socketDir := workerCloudWorkspace(t), socketTestDir(t)
	listener, err := listenMailInstallSocket(socketDir, uint32(os.Getegid()))
	if err != nil {
		t.Fatal("restricted socket unavailable", err)
	}
	defer listener.Close()
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer certServer.Close()
	caPath := filepath.Join(t.TempDir(), "test-ca.pem")
	if err = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certServer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	identityDir, err := os.MkdirTemp(base, "identity-")
	if err != nil {
		t.Fatal("private identity fixture unavailable", err)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(identityDir); e != nil {
			t.Error("identity fixture cleanup failed", e)
		}
	})
	serviceCfg := serviceauth.Config{Directory: filepath.Join(identityDir, "clients"), Clients: []serviceauth.Client{{ID: "mail-test", Enabled: true, UID: os.Geteuid(), GID: os.Getegid(), Grants: []serviceauth.Grant{{Permission: "mail.ingress.install", Workspace: "main", Resources: []string{"mail"}}}}}}
	manager, err := serviceauth.New(serviceauth.Options{LoadConfig: func() (serviceauth.Config, error) { return serviceCfg, nil }})
	if err != nil {
		t.Fatal("isolated service credential manager unavailable", err)
	}
	if err = manager.Reconcile(t.Context()); err != nil {
		t.Fatal("isolated service credential unavailable", err)
	}
	registry, err := httpapi.NewRegistry([]httpapi.Workspace{{ID: "main", Path: workspace}})
	if err != nil {
		t.Fatal(err)
	}
	writer := &recordingConfigAuditAppender{}
	api := &httpapi.ServiceAPIOptions{Authenticate: manager.Authenticate, Factory: func(string) application.ApplicationCredentialService { return nil }, Audit: func(context.Context, httpapi.ServiceCredentialAuditEvent) error { return nil }, MailInstall: newMailInstallHandler(filepath.Join(base, "operations"), writer)}
	handler, err := httpapi.New(httpapi.Options{Registry: registry, Security: httpapi.SecurityOptions{InitialState: httpapi.StateFull, HostAllowed: func(r *http.Request) bool { return r.Host == "127.0.0.1" }}, ServiceAPI: api})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveMailInstallSocket(ctx, listener, handler, certServer.TLS.Clone()) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error("fixture socket shutdown failed", e)
		}
	}()
	opDir := filepath.Join(base, "operations", "main", operation)
	defer func() {
		if _, e := os.Lstat(filepath.Join(opDir, "journal.jsonl")); os.IsNotExist(e) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, cleanup, "-test.v", "-test.run", "^TestCloudflareInstallCleanupRetryReal$", "-test.timeout", "3m")
		cmd.Env = append(os.Environ(), "MAIL_CLOUDFLARE_INSTALL_CLEANUP=1", "MAIL_CLOUDFLARE_RESERVATION_TEST_DIR="+opDir, "MAIL_CLOUDFLARE_JOURNAL_NAME=journal.jsonl")
		out, e := cmd.CombinedOutput()
		if e != nil || !strings.Contains(string(out), "--- PASS: TestCloudflareInstallCleanupRetryReal") {
			t.Error("isolated cloud cleanup not confirmed; journal and private credentials retained")
		} else {
			t.Log("isolated cloud resources and source credentials removed")
		}
	}()
	ctxChild, stop := context.WithTimeout(t.Context(), 7*time.Minute)
	defer stop()
	cmd := exec.CommandContext(ctxChild, child, "-test.v", "-test.run", "^TestWorkerAdminThroughRealHostCloud$", "-test.timeout", "6m")
	cmd.Env = append(os.Environ(), "MAIL_WORKER_ADMIN_CLOUD_TEST=1", "MAIL_WORKER_TEST_NAME="+name, "MAIL_WORKER_TEST_OPERATION="+operation, "MAIL_WORKER_TEST_CA="+caPath, "MAIL_WORKER_TEST_TOKEN_FILE="+filepath.Join(serviceCfg.Directory, "mail-test", "token"), "MAIL_WORKER_TEST_SOCKET="+filepath.Join(socketDir, mailInstallSocketName))
	out, err := cmd.CombinedOutput()
	if e := os.WriteFile(filepath.Join(base, "child.log"), out, 0600); e != nil {
		t.Fatal("private child test log unavailable")
	}
	if err != nil || !strings.Contains(string(out), "--- PASS: TestWorkerAdminThroughRealHostCloud") {
		t.Fatal("administrator/cloud fixture failed; inspect isolated journal")
	}
	if len(writer.events) < 3 {
		t.Fatal("host audit events missing")
	}
	public, e := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if e != nil || !strings.Contains(string(public), name) {
		t.Fatal("real host did not commit source configuration")
	}
	if _, e = os.Stat(filepath.Join(workspace, ".anas", "secrets.yml")); e != nil {
		t.Fatal("real host did not commit private source credentials")
	}
	t.Log("administrator API, service grant, TLS Unix socket, actual host and isolated cloud configuration passed")
}
