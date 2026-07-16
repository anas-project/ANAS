package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whlsxl/anas/internal/config"
)

func TestResolveCoreAndServiceConfigTargets(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	guest, err := resolveConfigTarget("core.share_guest_read_only", reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(guest.YAMLPath, "."); got != "env.SHARE_GUEST_READ_ONLY" {
		t.Fatalf("guest YAML path = %q", got)
	}
	policy := policyForTarget(guest, reg)
	if policy.Effect != "reconcile" {
		t.Fatalf("guest effect = %q", policy.Effect)
	}
	password, err := resolveConfigTarget("samba_dc.ldap_bind_password", reg)
	if err != nil {
		t.Fatal(err)
	}
	if policyForTarget(password, reg).Effect != "credential_rotate" {
		t.Fatal("LDAP bind password must require credential rotation")
	}
}

func TestOrdinaryStartRejectsImmutableChange(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	writeTestConfig := func(domain string) {
		t.Helper()
		content := "modules: [traefik]\nglobal:\n  domain: " + domain + "\n  email: admin@example.com\n  default_service_root_password: change-me\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeTestConfig("nas.example.com")
	if err := saveAppliedConfig(dir, cfgPath); err != nil {
		t.Fatal(err)
	}
	writeTestConfig("new.example.com")
	err = validateOrdinaryStartChanges(dir, cfgPath, reg)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("expected immutable change error, got %v", err)
	}
	settings, err := config.Settings(cfgPath)
	if err != nil || settings["global.domain"] != "new.example.com" {
		t.Fatalf("desired config was not retained: %v, %v", settings, err)
	}
}
