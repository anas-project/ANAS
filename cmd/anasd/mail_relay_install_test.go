// TEST_CASES: MAILMOD-T-059
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/cloudflare/installer"
	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func TestMailInstallOperationCannotMixWorkerAndRelay(t *testing.T) {
	t.Run("worker first", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "journal.jsonl"), []byte("worker"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := relayOperationDirectory(dir); !errors.Is(err, installer.ErrConflict) {
			t.Fatalf("worker operation reused: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "relay")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("conflict modified worker directory: %v", err)
		}
		if !workerOperationAllowed(dir) {
			t.Fatal("failed relay attempt blocked worker")
		}
	})
	t.Run("relay first", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		path, err := relayOperationDirectory(dir)
		if err != nil || path != filepath.Join(dir, "relay") {
			t.Fatalf("relay directory: %v", err)
		}
		if _, err = relayOperationDirectory(dir); err != nil {
			t.Fatalf("relay retry: %v", err)
		}
		if workerOperationAllowed(dir) {
			t.Fatal("worker reused relay operation")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(dir, "relay")); err != nil {
			t.Fatal(err)
		}
		if _, err := relayOperationDirectory(dir); err == nil {
			t.Fatal("followed relay symlink")
		}
		if workerOperationAllowed(dir) {
			t.Fatal("worker ignored relay symlink")
		}
	})
}

func relayHostWorkspace(t *testing.T) (string, string) {
	t.Helper()
	root, workspace := t.TempDir(), t.TempDir()
	for _, module := range []string{"mail", "samba_dc"} {
		if err := os.Mkdir(filepath.Join(root, module), 0700); err != nil {
			t.Fatal(err)
		}
		config := "config:\n  types: {mail_management_policy: string}\n  defaults: {mail_management_policy: ''}\n"
		if module == "mail" {
			config = "config:\n  types: {ingress_sources: string, ingress_credentials: string, ingress_capacity_bytes: int}\n  defaults: {ingress_sources: '', ingress_credentials: '', ingress_capacity_bytes: 0}\n  changes:\n    ingress_sources: {effect: container_recreate}\n    ingress_capacity_bytes: {effect: container_recreate}\n    ingress_credentials: {effect: container_recreate, sensitive: true, store_input: true}\n"
		}
		manifest := "api_version: anas.module/v1\nkind: Module\nname: " + module + "\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi:\n  supports: [anas.module-hook/v1]\nruntime:\n  type: builtin\n" + config
		if err := os.WriteFile(filepath.Join(root, module, "module.yml"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := `{"domains":["example.test"],"protected_addresses":[],"max_additional":2,"max_aliases":10,"capacity_bytes":2147483648,"send_per_minute":30}`
	config := []byte(fmt.Sprintf("module_source: official\nmodules:\n  mail: {}\n  samba_dc:\n    config:\n      mail_management_policy: %q\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n", policy))
	state := filepath.Join(workspace, ".anas")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "config.yml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	managed := fmt.Sprintf("api_version: anas.config/v1\ndigest: sha256:%x\nvalidator: cfgv-%s\nupdated_by: test\n", sha256.Sum256(config), strings.Repeat("1", 64))
	if err := os.WriteFile(filepath.Join(state, "config-managed.yml"), []byte(managed), 0600); err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(map[string]string{"api_version": "anas.module-view/v1", "module_root": root})
	if err := os.WriteFile(filepath.Join(state, "module-view.json"), view, 0600); err != nil {
		t.Fatal(err)
	}
	return root, workspace
}

func TestMailRelayHostRealTransaction(t *testing.T) {
	const source = "11111111-1111-4111-8111-111111111111"
	const generation = "22222222-2222-4222-8222-222222222222"
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if runtime.GOOS != "linux" {
		t.Skip("SSL_CERT_FILE trust injection is verified on the Linux deployment target")
	}
	if origin := os.Getenv("ANAS_TEST_RELAY_HOST_ORIGIN"); origin != "" {
		_, workspace := relayHostWorkspace(t)
		parent := t.TempDir()
		if err := os.Chmod(parent, 0700); err != nil {
			t.Fatal(err)
		}
		writer := &recordingConfigAuditAppender{}
		allowed := true
		call := httpapi.ServiceMailInstallCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Reauthorize: func(context.Context) error {
			if !allowed {
				return errors.New("revoked")
			}
			return nil
		}, Request: httpapi.ServiceMailInstallRequest{Actor: "anchor", Action: "prepare", Provider: "cloud-relay", OperationID: "cfg-0123456789abcdef0123456789abcdef", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 8 << 20, TotalCapacityBytes: 8 << 20, DeliveredDays: 7, SourceID: source, Generation: generation, SourceOrigin: origin, ReaderToken: reader, ControlToken: control}}
		handle := newMailInstallHandler(filepath.Join(parent, "installations"), writer)
		call.Request.Action = "commit"
		if _, err := handle(t.Context(), call); !errors.Is(err, httpapi.ErrMailInstallUnknown) {
			t.Fatalf("commit before prepare: %v", err)
		}
		call.Request.Action = "prepare"
		result, err := handle(t.Context(), call)
		if err != nil || result.State != "prepared" || result.SourceID != source {
			t.Fatalf("prepare: %+v %v", result, err)
		}
		call.Request.Action = "commit"
		result, err = handle(t.Context(), call)
		if err != nil || result.State != "configured" {
			t.Fatalf("commit: %+v %v", result, err)
		}
		snapshot, err := runner.ReadMailIngressSnapshot(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		sources, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, []string{"example.test"})
		if err != nil || len(sources) != 1 || sources[0].ID != source || sources[0].Credentials.Reader != reader || sources[0].Credentials.Control != control {
			t.Fatalf("workspace handoff: %v", err)
		}
		for _, event := range writer.events {
			raw, _ := json.Marshal(event)
			if strings.Contains(string(raw), reader) || strings.Contains(string(raw), control) {
				t.Fatal("audit exposed credentials")
			}
		}
		allowed = false
		if _, err = handle(t.Context(), call); !errors.Is(err, httpapi.ErrMailInstallUnknown) {
			t.Fatalf("revoked retry: %v", err)
		}
		allowed = true
		call.Request.Provider = "worker-r2"
		if _, err = handle(t.Context(), call); !errors.Is(err, httpapi.ErrMailInstallConflict) {
			t.Fatalf("worker reused relay operation: %v", err)
		}
		return
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := cloudrelay.InitializeRouteLedger(dir, source, generation, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cloudrelay.InitializeOriginalStore(ledger, 8<<20, 7)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := cloudrelay.NewSourceHandler(store, reader, control, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestMailRelayHostRealTransaction$")
	child.Env = append(os.Environ(), "ANAS_TEST_RELAY_HOST_ORIGIN="+server.URL, "SSL_CERT_FILE="+certPath)
	if output, e := child.CombinedOutput(); e != nil {
		t.Fatalf("isolated host transaction: %v: %s", e, output)
	}
}
