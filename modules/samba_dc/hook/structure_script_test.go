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
