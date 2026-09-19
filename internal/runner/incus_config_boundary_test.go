package runner

import (
	"strings"
	"testing"
)

// INCUS-R-004: load the real manifest rather than constructing a fake Module
// whose sensitivity policies can drift independently of the shipped provider.
func TestIncusControlInputsUseCanonicalKeysAndSensitiveListing(t *testing.T) {
	reg, err := loadRegistry("../..")
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, parameter := range []string{"endpoint", "server_certificate_b64", "admin_certificate_b64", "admin_key_b64"} {
		settings["modules.incus.config."+parameter] = "private-test-marker-" + parameter
	}
	entries, err := collectConfigParameters(reg, settings)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Path, "incus.") {
			continue
		}
		if _, exists := settings["modules.incus.config."+strings.TrimPrefix(entry.Path, "incus.")]; !exists {
			continue
		}
		seen[entry.Path] = true
		wantKey := "INCUS_" + strings.ToUpper(strings.TrimPrefix(entry.Path, "incus."))
		if !entry.Policy.Sensitive || entry.EnvKey != wantKey || !entry.Set || configListValue(entry) != "<set>" {
			t.Errorf("%s does not enforce the canonical sensitive config boundary", entry.Path)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("found %d control inputs, want four", len(seen))
	}
}
