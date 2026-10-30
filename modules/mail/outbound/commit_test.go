// TEST_CASES: MAILMOD-T-017
package outbound

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
	"gopkg.in/yaml.v3"
)

const operation = "cfg-0123456789abcdef0123456789abcdef"

type observer struct {
	fail  bool
	calls int
}

func (o *observer) BeforeConfigCommit(context.Context, application.ConfigCommitIntent) error {
	o.calls++
	if o.fail {
		return errors.New("private audit error")
	}
	return nil
}

type lostReply struct{ application.ConfigService }

func (s lostReply) PutConfig(ctx context.Context, request application.ConfigPutRequest, o application.ConfigCommitObserver) (application.ConfigPutResult, error) {
	result, err := s.ConfigService.PutConfig(ctx, request, o)
	if err != nil {
		return result, err
	}
	return application.ConfigPutResult{}, errors.New("private lost reply")
}

func workspaceFixture(t *testing.T) (application.ConfigService, string) {
	t.Helper()
	root, workspace := t.TempDir(), t.TempDir()
	module := filepath.Join(root, "mail")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: mail
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  types:
    outbound_routing: string
    outbound_passwords: string
    ingress_sources: string
  defaults:
    outbound_routing: ""
    outbound_passwords: ""
    ingress_sources: ""
  changes:
    ingress_sources:
      effect: container_recreate
    outbound_routing:
      effect: container_recreate
    outbound_passwords:
      effect: container_recreate
      sensitive: true
      store_input: true
`
	if err := os.WriteFile(filepath.Join(module, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(workspace, ".anas")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	config := []byte("module_source: official\nmodules:\n  mail:\n    config:\n      ingress_sources: keep-existing-source\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n")
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
	return runner.NewWorkspaceConfigService(workspace), workspace
}

func previous(t *testing.T, workspace string) Previous {
	t.Helper()
	s, err := runner.ReadMailOutboundSnapshot(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	return Previous{Routing: s.Routing, Passwords: s.Passwords, Validator: s.Validator}
}

func directConfig() smtpconfig.Config {
	return smtpconfig.Config{Default: "direct", Profiles: []smtpconfig.Profile{{ID: "direct", Provider: "direct"}}}
}

func mixedConfig() smtpconfig.Config {
	return smtpconfig.Config{Default: "direct", Profiles: []smtpconfig.Profile{
		{ID: "direct", Provider: "direct"},
		{ID: "relay", Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", Password: "private-mail-password"},
	}, Domains: map[string]string{"example.test": "relay"}}
}

func TestOutboundCommitWorkspaceTransactionAndRecovery(t *testing.T) {
	service, workspace := workspaceFixture(t)
	ctx := context.Background()
	initial := previous(t, workspace)
	audit := &observer{fail: true}
	if err := Commit(ctx, service, audit, operation, initial, directConfig()); !errors.Is(err, ErrUnknown) {
		t.Fatal("audit failure accepted", err)
	}
	if s := previous(t, workspace); s.Routing != initial.Routing || s.Passwords != initial.Passwords {
		t.Fatal("audit failure changed configuration")
	}
	audit.fail = false
	if err := Commit(ctx, service, audit, operation, initial, directConfig()); err != nil {
		t.Fatal(err)
	}
	direct := previous(t, workspace)
	if direct.Passwords != "{}" {
		t.Fatal("direct route contains credentials")
	}
	if _, err := smtpconfig.Parse(direct.Routing, direct.Passwords); err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, lostReply{service}, audit, operation, direct, mixedConfig()); !errors.Is(err, ErrUnknown) {
		t.Fatal("lost response was reported successful", err)
	}
	after := previous(t, workspace)
	parsed, err := smtpconfig.Parse(after.Routing, after.Passwords)
	if err != nil || parsed.Domains["example.test"] != "relay" || parsed.Profiles[1].Password != "private-mail-password" {
		t.Fatal("committed configuration invalid", err)
	}
	public, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || bytes.Contains(public, []byte("private-mail-password")) || !bytes.Contains(public, []byte("keep-existing-source")) {
		t.Fatal("password exposed or unrelated configuration lost", err)
	}
	before := after.Validator
	if err := Commit(ctx, service, audit, operation, after, mixedConfig()); err != nil {
		t.Fatal("fresh identical retry failed", err)
	}
	if retry := previous(t, workspace); retry.Validator != before || audit.calls != 4 {
		t.Fatal("retry changed config or skipped audit")
	}
	if err := Commit(ctx, service, audit, operation, direct, directConfig()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale snapshot restored old route", err)
	}
	encoded, _ := json.Marshal(after)
	for _, text := range []string{string(encoded), fmt.Sprintf("%+v", after), fmt.Sprintf("%#v", after)} {
		if strings.Contains(text, "private-mail-password") {
			t.Fatal("snapshot disclosed password")
		}
	}
}

func TestOutboundCommitRejectsChangedPrivateValueUnderSameValidator(t *testing.T) {
	service, workspace := workspaceFixture(t)
	ctx := context.Background()
	if err := Commit(ctx, service, &observer{}, operation, previous(t, workspace), mixedConfig()); err != nil {
		t.Fatal(err)
	}
	prior := previous(t, workspace)
	secretPath := filepath.Join(workspace, ".anas", "secrets.yml")
	raw, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	item := doc["secrets"].(map[string]any)["MAIL_OUTBOUND_PASSWORDS"].(map[string]any)
	item["value"] = `{"relay":"other-private-password"}`
	raw, err = yaml.Marshal(doc)
	if err != nil || os.WriteFile(secretPath, raw, 0600) != nil {
		t.Fatal("cannot inject changed private input", err)
	}
	configBefore, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err := Commit(ctx, service, &observer{}, operation, prior, directConfig()); !errors.Is(err, ErrConflict) {
		t.Fatal("stale private value accepted", err)
	}
	configAfter, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	secretAfter, _ := os.ReadFile(secretPath)
	if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(raw, secretAfter) {
		t.Fatal("conflicting transaction changed files")
	}
}

func TestOutboundCommitDefaultChangePreservesExistingOverrides(t *testing.T) {
	service, workspace := workspaceFixture(t)
	ctx := context.Background()
	initial := mixedConfig()
	initial.Domains["direct.example.test"] = "direct"
	if err := Commit(ctx, service, &observer{}, operation, previous(t, workspace), initial); err != nil {
		t.Fatal(err)
	}
	prior := previous(t, workspace)
	updated, err := DefaultConfig(prior, smtpconfig.Profile{Provider: "resend", Host: "smtp.resend.com", Port: 587, TLS: "starttls", Username: "resend", Password: "replacement-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(ctx, service, &observer{}, operation, prior, updated); err != nil {
		t.Fatal(err)
	}
	after := previous(t, workspace)
	actual, err := smtpconfig.Parse(after.Routing, after.Passwords)
	if err != nil || actual.Domains["direct.example.test"] != "direct" || actual.Domains["example.test"] != "relay" || actual.Profiles[1].Password != "private-mail-password" || actual.Profiles[2].Password != "replacement-key" {
		t.Fatal("default update lost an existing override or credential", err)
	}
}
