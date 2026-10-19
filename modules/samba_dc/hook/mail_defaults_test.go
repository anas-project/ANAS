// TEST_CASES: MAIL-T-001
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMailDomainDoesNotRenameDirectory(t *testing.T) {
	for _, mailDomain := range []string{"", "hlong.wang"} {
		env := map[string]string{"BASE_DOMAIN": "finance.hlong.wang", "SAMBA_DC_DOMAIN": "finance.hlong.wang", "SAMBA_DC_ADMIN_NAME": "admin", "SAMBA_DC_MAIL_DOMAIN": mailDomain}
		if err := calcSambaDC(env, "", &secretStore{values: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		want := mailDomain
		if want == "" {
			want = "finance.hlong.wang"
		}
		if env["SAMBA_DC_ADMIN_MAIL"] != "admin@"+want || env["SAMBA_DC_MAIL_DOMAIN"] != want || env["SAMBA_DC_DOMAIN"] != "finance.hlong.wang" || env["SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN"] != "finance.hlong.wang" || env["SAMBA_DC_REALM"] != "FINANCE.HLONG.WANG" {
			t.Fatal("email domain changed directory identity or default address")
		}
	}
}

func TestInvalidMailPolicyFailsValidationAndCalculation(t *testing.T) {
	for _, phase := range []string{"validate", "calculate"} {
		if _, err := handle(hookRequest{Phase: phase, Module: "samba_dc", Env: map[string]string{"BASE_DOMAIN": "finance.hlong.wang", "SAMBA_DC_MAIL_DOMAIN": "https://hlong.wang", "SAMBA_DC_ADMIN_NAME": "admin"}}); err == nil {
			t.Fatal("invalid email policy accepted", phase)
		}
	}
}

func TestProductionBootstrapSuppliesEmailOnlyWhenCreatingAdmin(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(script)
	start := strings.Index(source, "# deal with admin\n")
	if start < 0 {
		t.Fatal("admin provisioning block unavailable")
	}
	end := strings.Index(source[start+1:], "\n# ")
	if end < 0 {
		t.Fatal("admin provisioning block end unavailable")
	}
	block := source[start : start+1+end]
	for _, existing := range []string{"", "CN=admin,OU=People,DC=finance,DC=hlong,DC=wang"} {
		command := exec.Command("bash", "-c", `set -euo pipefail
get_attribute_dn() { printf '%s' "$EXISTING_DN"; }
add_to_group() { :; }
remove_from_group() { :; }
samba-tool() { printf 'samba-tool'; printf '\t%s' "$@"; printf '\n'; }
`+block)
		command.Env = append(os.Environ(), "EXISTING_DN="+existing, "SAMBA_DC_ADMIN_NAME=admin", "SAMBA_DC_ADMIN_PASSWORD=fixture-only", "SAMBA_DC_ADMIN_DN=CN=admin,OU=People,DC=finance,DC=hlong,DC=wang", "SAMBA_DC_BASE_USERS_DN_PREFIX=OU=People", "SAMBA_DC_ADMIN_MAIL=admin@finance.hlong.wang", "SAMBA_DC_ADMIN_DISPLAY_NAME=Administrator", "SAMBA_DC_CREATE_STRUCTURE=false")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("production bootstrap failed: %v: %s", err, out)
		}
		if existing == "" && !strings.Contains(string(out), "--mail-address=admin@finance.hlong.wang") {
			t.Fatal("bootstrap creation omitted mail", string(out))
		}
		if existing != "" && strings.Contains(string(out), "samba-tool\tuser\t") {
			t.Fatal("bootstrap overwrote an existing account", string(out))
		}
	}
}
