package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestResolveGlobalAndServiceConfigTargets(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A parameter a module publishes under a bare env name is reachable through
	// the module that owns it, and keeps that module's change policy rather than
	// falling back to the default one.
	guest, err := resolveConfigTarget("samba_fs.share_guest_read_only", reg)
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

func TestBuildMirrorEnvRequiresImageRebuild(t *testing.T) {
	target, err := resolveConfigTarget("env.APT_MIRROR_URL", map[string]Module{})
	if err != nil {
		t.Fatal(err)
	}
	policy := policyForTarget(target, map[string]Module{})
	if policy.Effect != "image_rebuild" || policy.Apply != "apply-with-build" {
		t.Fatalf("APT mirror policy = %+v", policy)
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
		content := "modules:\n  traefik: {}\nglobal:\n  base_domain: " + domain + "\n  email: admin@example.com\n  default_service_root_password: change-me\n"
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
	if err != nil || settings["global.base_domain"] != "new.example.com" {
		t.Fatalf("desired config was not retained: %v, %v", settings, err)
	}
}
