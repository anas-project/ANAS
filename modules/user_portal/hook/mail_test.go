package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/directorymail"
)

func mailBindingEnvironment(t *testing.T) map[string]string {
	t.Helper()
	policy, err := json.Marshal(directorymail.Policy{Domains: []string{"example.test"}, Protected: []string{}, MaxAdditional: 2, MaxAliases: 10, CapacityBytes: 2 << 30, SendPerMinute: 30})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"SAMBA_DC_MAIL_DIRECTORY_ENABLED": "true", "SAMBA_DC_MAIL_MANAGEMENT_POLICY": string(policy), "SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN": "OU=Service Accounts,DC=example,DC=test", "SAMBA_DC_MAIL_CREATOR_BIND_DN": "CN=creator,OU=Service Accounts,DC=example,DC=test", "SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD": "synthetic-creator", "SAMBA_DC_MAIL_ADDRESS_BIND_DN": "CN=address,OU=Service Accounts,DC=example,DC=test", "SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD": "synthetic-address", "SAMBA_DC_LDAP_BIND_DN": "CN=reader,OU=Service Accounts,DC=example,DC=test"}
}
func TestMailManagementBindingUsesPrivateRuntimeAndFixedCreator(t *testing.T) {
	manifest, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "SAMBA_DC_MAIL_ADDRESS_BIND_") {
		t.Fatal("portal still consumes address writer")
	}

	e := mailBindingEnvironment(t)
	files, env, err := mailManagementBinding(e)
	if err != nil || strings.Contains(files["mail-management/config.json"], "synthetic-address") || env["USER_PORTAL_CREATE_BIND_DN"] != e["SAMBA_DC_MAIL_CREATOR_BIND_DN"] {
		t.Fatal("mail binding", err)
	}
	for _, value := range env {
		if strings.Contains(value, "synthetic-address") {
			t.Fatal("address writer leaked to ordinary env projection")
		}
	}
	for _, phase := range []string{"validate", "runtime_restore"} {
		out, err := handle(request{Phase: phase, Env: e})
		if err != nil {
			t.Fatal(phase, err)
		}
		if phase == "validate" && (len(out.Env) > 0 || len(out.RuntimeFiles) > 0) {
			t.Fatal("validate mutated configuration")
		}
		if phase == "runtime_restore" && out.RuntimeFiles["mail-management/config.json"] != files["mail-management/config.json"] {
			t.Fatal("restore omitted private binding")
		}
	}
	e["SAMBA_DC_MAIL_DIRECTORY_ENABLED"] = "false"
	files, _, err = mailManagementBinding(e)
	if err != nil || files["mail-management/config.json"] != "" {
		t.Fatal("disabled deployment retained secret")
	}
}
func TestMailManagementBindingRejectsPartialPolicyAndWrongWriter(t *testing.T) {
	for _, kind := range []string{"missing-policy", "missing-limit", "wrong-creator", "missing-password"} {
		e := mailBindingEnvironment(t)
		switch kind {
		case "missing-policy":
			delete(e, "SAMBA_DC_MAIL_MANAGEMENT_POLICY")
		case "missing-limit":
			var p map[string]any
			json.Unmarshal([]byte(e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"]), &p)
			delete(p, "max_additional")
			b, _ := json.Marshal(p)
			e["SAMBA_DC_MAIL_MANAGEMENT_POLICY"] = string(b)
		case "wrong-creator":
			e["USER_PORTAL_CREATE_BIND_DN"] = e["SAMBA_DC_LDAP_BIND_DN"]
		case "shared-writer":
			e["SAMBA_DC_MAIL_ADDRESS_BIND_DN"] = e["SAMBA_DC_MAIL_CREATOR_BIND_DN"]
		case "missing-password":
			delete(e, "SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD")
		}
		if files, env, err := mailManagementBinding(e); err == nil || len(files) > 0 || len(env) > 0 {
			t.Fatal(kind, "invalid binding published")
		}
	}
}

func TestMailManagementValidationPrecedesDirectoryProjection(t *testing.T) {
	e := mailBindingEnvironment(t)
	for key := range e {
		if strings.HasSuffix(key, "_DN") || strings.HasSuffix(key, "_PASSWORD") {
			delete(e, key)
		}
	}
	if out, err := handle(request{Phase: "validate", Env: e}); err != nil || len(out.Env)+len(out.RuntimeFiles) != 0 {
		t.Fatal("validate required uncalculated service scope", err)
	}
	if _, _, err := mailManagementBinding(e); err == nil {
		t.Fatal("final binding accepted missing directory projection")
	}
}
