package main

// TEST_CASES: TEMP-T-021

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the production capability block without touching a Samba database or
// /run. Only its directory writes are replaced with records of their arguments.
func runStructureCapabilityGroups(t *testing.T, environment []string) ([]byte, error) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "structure.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start := strings.Index(text, "# Application capability groups.")
	end := strings.Index(text, "\n# auto create ldap structure")
	if start < 0 || end <= start {
		t.Fatal("production capability block was not found")
	}
	optionsStart := strings.Index(text, "\nset ")
	if optionsStart < 0 {
		t.Fatal("production shell options were not found")
	}
	options := strings.SplitN(text[optionsStart+1:], "\n", 2)[0]
	command := options + `
create_ou() { printf 'ou\t%s\t%s\n' "$1" "$2"; }
create_group() { printf 'group\t%s\t%s\n' "$1" "$2"; }
` + text[start:end] + "\nprintf 'continued\\n'\n"
	cmd := exec.Command("bash", "-c", command)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}, environment...)
	return cmd.CombinedOutput()
}

func TestStructureOptionalCapabilityGroups(t *testing.T) {
	for _, test := range []struct {
		name        string
		structure   string
		groups      []string
		wantRecords string
	}{
		{name: "absent", structure: "true"},
		{name: "empty", structure: "true", groups: []string{"ANAS_IDENTITY_CAPABILITY_GROUPS="}},
		{
			name: "nonempty", structure: "true",
			groups: []string{"ANAS_IDENTITY_CAPABILITY_GROUPS=ai_agent_execute,,nextcloud_admin"},
			wantRecords: "Create capability ou & groups\n" +
				"ou\tOU=Groups\tDC=temp-edit,DC=test\n" +
				"ou\tOU=Cap\tOU=Groups,DC=temp-edit,DC=test\n" +
				"group\tCAP_ai_agent_execute\tOU=Cap,OU=Groups\n" +
				"group\tCAP_nextcloud_admin\tOU=Cap,OU=Groups\n",
		},
		{name: "disabled_absent", structure: "false"},
		{name: "disabled_nonempty", structure: "false", groups: []string{"ANAS_IDENTITY_CAPABILITY_GROUPS=ai_agent_execute"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			environment := append([]string{
				"SAMBA_DC_CREATE_STRUCTURE=" + test.structure,
				"SAMBA_DC_BASE_DN=DC=temp-edit,DC=test",
			}, test.groups...)
			output, err := runStructureCapabilityGroups(t, environment)
			if err != nil {
				t.Fatalf("production capability block failed: %v\n%s", err, output)
			}
			if got, want := string(output), test.wantRecords+"continued\n"; got != want {
				t.Fatalf("production capability operations = %q, want %q", got, want)
			}
		})
	}
}

func TestStructureCapabilityGroupsRejectMissingRequiredEnvironment(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment []string
		missing     string
	}{
		{name: "structure_switch", environment: []string{"SAMBA_DC_BASE_DN=DC=temp-edit,DC=test"}, missing: "SAMBA_DC_CREATE_STRUCTURE"},
		{
			name: "base_dn",
			environment: []string{
				"SAMBA_DC_CREATE_STRUCTURE=true",
				"ANAS_IDENTITY_CAPABILITY_GROUPS=ai_agent_execute",
			},
			missing: "SAMBA_DC_BASE_DN",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := runStructureCapabilityGroups(t, test.environment)
			if err == nil {
				t.Fatalf("missing required %s was accepted: %s", test.missing, output)
			}
			text := string(output)
			if !strings.Contains(text, test.missing+": unbound variable") || strings.Contains(text, "continued\n") || strings.Contains(text, "group\t") {
				t.Fatalf("missing required %s did not stop the production block: %s", test.missing, output)
			}
		})
	}
}
