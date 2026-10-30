// TEST_CASES: MAILMOD-T-042
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/cloudflare/installer"
)

func TestMailInstallDirectoryRejectsUnsafePaths(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "installations")
	op := "cfg-0123456789abcdef0123456789abcdef"
	path, err := mailInstallationDirectory(root, "main", op)
	if err != nil || path != filepath.Join(root, "main", op) {
		t.Fatal("private operation directory unavailable", err)
	}
	if path2, err := mailInstallationDirectory(root, "main", op); err != nil || path2 != path {
		t.Fatal("private operation directory not reusable", err)
	}
	unlock, err := lockMailInstallWorkspace(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockMailInstallWorkspace(filepath.Dir(path)); !errors.Is(err, installer.ErrBusy) {
		t.Fatal("concurrent workspace install was not blocked", err)
	}
	unlock()
	unlock, err = lockMailInstallWorkspace(filepath.Dir(path))
	if err != nil {
		t.Fatal("workspace lock did not release", err)
	}
	unlock()
	for _, bad := range []string{"../main", ".", "..", ""} {
		if _, err := mailInstallationDirectory(root, bad, op); err == nil {
			t.Fatal("invalid workspace accepted", bad)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	if _, err := mailInstallationDirectory(root, "other", op); err == nil {
		t.Fatal("workspace symlink followed")
	}
	if err := os.Mkdir(filepath.Join(root, "unsafe"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "unsafe"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := mailInstallationDirectory(root, "unsafe", op); err == nil {
		t.Fatal("unsafe directory accepted")
	}
}

func TestMailInstallPolicyUsesOnlyManagedSambaPolicy(t *testing.T) {
	policy := `{"domains":["example.test"],"protected_addresses":[],"max_additional":2,"max_aliases":10,"capacity_bytes":2147483648,"send_per_minute":30}`
	snapshot := application.ConfigSnapshot{Managed: true, Validator: "cfgv-one", Config: application.ConfigDocument{"modules": map[string]any{"samba_dc": map[string]any{"config": map[string]any{"mail_management_policy": policy}}}}}
	domains, err := mailInstallPolicy(snapshot)
	if err != nil || len(domains) != 1 || domains[0] != "example.test" {
		t.Fatal("explicit policy rejected", err)
	}
	snapshot.Config["env"] = map[string]any{"SAMBA_DC_MAIL_MANAGEMENT_POLICY": policy}
	if _, err := mailInstallPolicy(snapshot); !errors.Is(err, installer.ErrConflict) {
		t.Fatal("environment override accepted", err)
	}
	delete(snapshot.Config, "env")
	snapshot.Managed = false
	if _, err := mailInstallPolicy(snapshot); !errors.Is(err, installer.ErrConflict) {
		t.Fatal("unmanaged config accepted", err)
	}
}

func TestMailInstallAuditRechecksGrantAndRedactsTokens(t *testing.T) {
	allowed := true
	writer := &recordingConfigAuditAppender{}
	call := httpapi.ServiceMailInstallCall{WorkspaceID: "main", ClientID: "mail-service", Request: httpapi.ServiceMailInstallRequest{Actor: "anchor", DeploymentToken: "private-deploy", RoutingToken: "private-routing"}, Reauthorize: func(context.Context) error {
		if !allowed {
			return errors.New("revoked")
		}
		return nil
	}}
	a := mailInstallAudit{call: call, writer: writer}
	if err := a.BeforeMailProvision(t.Context(), installer.ProvisionIntent{OperationID: "cfg-0123456789abcdef0123456789abcdef"}); err != nil || len(writer.events) != 1 {
		t.Fatal("audit failed", err)
	}
	encoded, _ := json.Marshal(writer.events[0])
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("audit leaked a token")
	}
	allowed = false
	if err := a.BeforeConfigCommit(t.Context(), application.ConfigCommitIntent{OperationID: "cfg-0123456789abcdef0123456789abcdef"}); err == nil || len(writer.events) != 1 {
		t.Fatal("revoked service grant permitted config commit", err)
	}
}

func TestMailInstallHostRejectsUnpreparedCommitAndWrongPolicy(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	for _, module := range []string{"mail", "samba_dc"} {
		if err := os.Mkdir(filepath.Join(root, module), 0700); err != nil {
			t.Fatal(err)
		}
		config := ""
		if module == "mail" {
			config = "config:\n  types: {ingress_sources: string, ingress_credentials: string, ingress_capacity_bytes: int}\n  defaults: {ingress_sources: '', ingress_credentials: '', ingress_capacity_bytes: 0}\n  changes:\n    ingress_sources: {effect: container_recreate}\n    ingress_capacity_bytes: {effect: container_recreate}\n    ingress_credentials: {effect: container_recreate, sensitive: true, store_input: true}\n"
		} else {
			config = "config:\n  types: {mail_management_policy: string}\n  defaults: {mail_management_policy: ''}\n"
		}
		manifest := "api_version: anas.module/v1\nkind: Module\nname: " + module + "\nversion: 1.0.0\nrevision: 1\nstatus: release\nabi:\n  supports: [anas.module-hook/v1]\nruntime:\n  type: builtin\n" + config
		if err := os.WriteFile(filepath.Join(root, module, "module.yml"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := `{"domains":["example.test"],"protected_addresses":[],"max_additional":2,"max_aliases":10,"capacity_bytes":2147483648,"send_per_minute":30}`
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
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	operations := filepath.Join(parent, "mail-installations")
	writer := &recordingConfigAuditAppender{}
	call := httpapi.ServiceMailInstallCall{WorkspaceID: "main", WorkspacePath: workspace, ClientID: "mail-service", Reauthorize: func(context.Context) error { return nil }, Request: httpapi.ServiceMailInstallRequest{
		Actor: "anchor", Action: "commit", OperationID: "cfg-0123456789abcdef0123456789abcdef", AccountID: strings.Repeat("a", 32), ZoneID: strings.Repeat("b", 32), Worker: "anas-worker", Bucket: "anas-bucket", Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 2 << 30, TotalCapacityBytes: 2 << 30, DeliveredDays: 7, RoutingToken: strings.Repeat("R", 32),
	}}
	handler := newMailInstallHandler(operations, writer)
	call.Request.AllowedDomains = []string{"other.test"}
	if _, err := handler(t.Context(), call); !errors.Is(err, httpapi.ErrMailInstallConflict) {
		t.Fatal("foreign domain policy accepted", err)
	}
	if _, err := os.Stat(filepath.Join(operations, "main", call.Request.OperationID, "journal.jsonl")); !errors.Is(err, os.ErrNotExist) || len(writer.events) != 0 {
		t.Fatal("invalid policy created an operation journal or audit", err)
	}
	call.Request.AllowedDomains = []string{"example.test"}
	if _, err := handler(t.Context(), call); !errors.Is(err, httpapi.ErrMailInstallUnknown) {
		t.Fatal("unprepared install was committed", err)
	}
	after, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || string(after) != string(body) || len(writer.events) != 1 || writer.events[0].Type != "service.mail.ingress.read" {
		t.Fatal("unprepared commit modified workspace or audit", err)
	}
}
