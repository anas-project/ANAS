package runner

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHostActionsReportsOnlyCompiledInventory(t *testing.T) {
	out, _, code := capture(t, "host", "actions", "--json")
	if code != 0 {
		t.Fatal(code, out)
	}
	var result struct {
		OK                   bool   `json:"ok"`
		Source               string `json:"source"`
		InstallationVerified bool   `json:"installation_verified"`
		Actions              []struct {
			Name, Scope, Implementation string
			ReadOnly                    bool `json:"read_only"`
			RequiresRoot                bool `json:"requires_root"`
			RequiresConfirmation        bool `json:"requires_confirmation"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err, out)
	}
	if !result.OK || result.Source != "compiled-client" || result.InstallationVerified || len(result.Actions) < 9 {
		t.Fatal(out)
	}
	seen := map[string]struct {
		ReadOnly, RequiresRoot, RequiresConfirmation bool
	}{}
	for _, a := range result.Actions {
		seen[a.Name] = struct {
			ReadOnly, RequiresRoot, RequiresConfirmation bool
		}{a.ReadOnly, a.RequiresRoot, a.RequiresConfirmation}
	}
	if a := seen["incus.status"]; !a.ReadOnly || a.RequiresRoot || a.RequiresConfirmation {
		t.Fatal(out)
	}
	if a := seen["incus.install"]; a.ReadOnly || !a.RequiresRoot || !a.RequiresConfirmation {
		t.Fatal(out)
	}
	if a := seen["incus.install.plan"]; !a.ReadOnly || !a.RequiresRoot || a.RequiresConfirmation {
		t.Fatal(out)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["installation_verified"]) != "false" {
		t.Fatal("missing explicit unknown installation state")
	}
	plain, _, code := capture(t, "host", "actions")
	if code != 0 || !strings.Contains(plain, "installation NOT verified") {
		t.Fatal(code, plain)
	}
	help, _, code := capture(t, "help", "--json")
	if code != 0 || !strings.Contains(help, `"host"`) {
		t.Fatal("host missing from discovery", help)
	}
}

func TestHostActionsRejectsWritesAndArbitraryPaths(t *testing.T) {
	for _, args := range [][]string{{"host", "--json"}, {"host", "install", "--json"}, {"host", "plan", "incus.uninstall", "--json"}, {"host", "actions", "--socket", "/private-marker", "--json"}, {"host", "actions", "--root-password", "private-marker", "--json"}, {"host", "actions", "incus.status", "--json"}} {
		out, stderr, code := capture(t, args...)
		if code != 2 || strings.Contains(out+stderr, "private-marker") {
			t.Fatal(args, code, out, stderr)
		}
	}
}
