package main

// TEST_CASES: TEMP-T-021

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the production bootstrap block with directory/native commands stubbed.
// Group metadata is now supplied by the catalog, rather than a second CSV loop.
func runStructureCapabilityGroups(t *testing.T, environment []string) ([]byte, error) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start := strings.Index(text, "# Group names, directory locations")
	end := strings.Index(text, "# deal with admin")
	if start < 0 || end <= start {
		t.Fatal("production catalog bootstrap not found")
	}
	command := `set -euo pipefail
python3(){ [ "$1" = /usr/local/bin/identity_groups.py ] && printf 'catalog\n'; }
create_ou(){ :; }
add_to_group(){ :; }
remove_from_group(){ :; }
net(){ :; }
` + text[start:end] + "\nprintf 'continued\\n'\n"
	cmd := exec.Command("bash", "-c", command)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}, environment...)
	return cmd.CombinedOutput()
}
func TestStructureOptionalCapabilityGroups(t *testing.T) {
	for _, enabled := range []string{"true", "false"} {
		output, err := runStructureCapabilityGroups(t, []string{"SAMBA_DC_CREATE_STRUCTURE=" + enabled, "SAMBA_DC_APP_FILTER=false", "SAMBA_DC_BASE_DN=DC=temp-edit,DC=test", "SAMBA_DC_ADMIN_GROUP_NAME=Admins", "SAMBA_DC_WORKGROUP=TEMP", "SAMBA_DC_ADMINISTRATOR_NAME=Administrator", "SAMBA_DC_ADMINISTRATOR_PASSWORD=fixture"})
		if err != nil || string(output) != "catalog\ncontinued\n" {
			t.Fatalf("catalog bootstrap failed: %s %v", output, err)
		}
	}
}
func TestStructureCapabilityGroupsRejectMissingRequiredEnvironment(t *testing.T) {
	for _, missing := range []string{"SAMBA_DC_CREATE_STRUCTURE", "SAMBA_DC_BASE_DN"} {
		env := []string{"SAMBA_DC_APP_FILTER=false"}
		if missing != "SAMBA_DC_CREATE_STRUCTURE" {
			env = append(env, "SAMBA_DC_CREATE_STRUCTURE=true")
		}
		output, err := runStructureCapabilityGroups(t, env)
		if err == nil || !strings.Contains(string(output), missing+": unbound variable") || strings.Contains(string(output), "continued\n") {
			t.Fatalf("missing %s did not stop bootstrap: %s %v", missing, output, err)
		}
	}
}

// TEST_CASES: MAILMOD-T-004
func TestStructureWaitsForLDAPAndStopsWhenUnavailable(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start, end := strings.Index(text, "# Wait for the local LDAP listener"), strings.Index(text, "# A service account is always required")
	if start < 0 || end <= start {
		t.Fatal("startup wait block unavailable")
	}
	for _, unavailable := range []bool{false, true} {
		command := `set -euo pipefail
attempts=0
nc(){ attempts=$((attempts+1)); [ "$attempts" -ge 3 ] && [ "$UNAVAILABLE" = false ]; }
sleep(){ :; }
` + text[start:end] + "\nprintf 'continued:%s\\n' \"$attempts\"\n"
		cmd := exec.Command("bash", "-c", command)
		value := "false"
		if unavailable {
			value = "true"
		}
		cmd.Env = append(os.Environ(), "UNAVAILABLE="+value)
		out, err := cmd.CombinedOutput()
		if unavailable {
			if err == nil || strings.Contains(string(out), "continued:") || !strings.Contains(string(out), "did not become ready") {
				t.Fatalf("unavailable listener did not stop startup: %s %v", out, err)
			}
		} else if err != nil || string(out) != "continued:3\n" {
			t.Fatalf("listener retry failed: %s %v", out, err)
		}
	}
}

func TestStructureAppliesCompleteDomainPolicyInOneNativeCall(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start, end := strings.Index(text, "# samba password rule"), strings.Index(text, "# Separate policy object")
	if start < 0 || end <= start {
		t.Fatal("domain policy block unavailable")
	}
	for _, complexity := range []string{"true", "false"} {
		for _, failure := range []string{"0", "1"} {
			cmd := exec.Command("bash", "-c", `set -euo pipefail
samba-tool(){ printf 'native'; printf '\t%s' "$@"; printf '\n'; return "$FAILURE"; }
`+text[start:end]+"\nprintf 'continued\\n'\n")
			cmd.Env = append(os.Environ(), "SAMBA_DC_USER_COMPLEX_PASS="+complexity, "SAMBA_DC_USER_MIN_PASS_AGE=2", "SAMBA_DC_USER_MAX_PASS_AGE=45", "SAMBA_DC_USER_MIN_PASS_LENGTH=12", "SAMBA_DC_USER_PASSWORD_HISTORY=6", "SAMBA_DC_USER_LOCKOUT_THRESHOLD=4", "SAMBA_DC_USER_LOCKOUT_DURATION=20", "SAMBA_DC_USER_LOCKOUT_RESET_AFTER=15", "FAILURE="+failure)
			out, err := cmd.CombinedOutput()
			wantComplexity := "off"
			if complexity == "true" {
				wantComplexity = "on"
			}
			want := "native\tdomain\tpasswordsettings\tset\t--min-pwd-age=2\t--max-pwd-age=45\t--min-pwd-length=12\t--history-length=6\t--account-lockout-threshold=4\t--account-lockout-duration=20\t--reset-account-lockout-after=15\t--complexity=" + wantComplexity + "\n"
			if strings.Count(string(out), "native\t") != 1 || !strings.Contains(string(out), want) {
				t.Fatalf("incomplete domain policy: %s", out)
			}
			if failure == "1" {
				if err == nil || strings.Contains(string(out), "continued") {
					t.Fatalf("policy failure did not stop startup: %s %v", out, err)
				}
			} else if err != nil || !strings.HasSuffix(string(out), "continued\n") {
				t.Fatalf("policy application failed: %s %v", out, err)
			}
		}
	}
}
