package runner

import (
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func dynamicDNSApp(cfg *config.File, lock *caskLock) *app {
	return &app{
		cfg:  cfg,
		lock: lock,
		reg: map[string]Module{
			"core":         {Name: "core", EnvPrefix: "CORE"},
			"lego":         {Name: "lego", EnvPrefix: "LEGO"},
			"ddns_go":      {Name: "ddns_go", EnvPrefix: "DDNS_GO"},
			"ddns_updater": {Name: "ddns_updater", EnvPrefix: "DDNS_UPDATER"},
		},
		env:              map[string]string{},
		envOwner:         map[string]string{},
		resolvedBindings: map[string]map[string]string{},
	}
}

func dynamicDNSConfig(provider, vendor string, modules ...string) *config.File {
	return &config.File{
		Modules:    modules,
		DynamicDNS: config.DynamicDNS{Provider: provider, DNSProvider: vendor},
	}
}

// Asking for dynamic DNS is enough: naming a cask as well would make the
// capability pointless.
func TestDynamicDNSAutoPicksAnImplementation(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("auto", "tencentcloud"), nil)
	provider, err := a.resolveDynamicDNS()
	if err != nil {
		t.Fatal(err)
	}
	// ddns_updater cannot address Tencent Cloud at all, so there is only one
	// possible answer and no preference is involved.
	if provider != "ddns_go" {
		t.Fatalf("provider = %q, want ddns_go", provider)
	}
}

// When both can address the vendor, the answer must come from a fixed order
// rather than from whatever the registry happens to enumerate first.
func TestDynamicDNSAutoFollowsTheDeclaredPreference(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("auto", "cloudflare"), nil)
	provider, err := a.resolveDynamicDNS()
	if err != nil {
		t.Fatal(err)
	}
	if provider != "ddns_go" {
		t.Fatalf("provider = %q, want the first supported preference", provider)
	}
}

// An implementation the user already runs is preferred over adding another
// container to the deployment.
func TestDynamicDNSAutoPrefersAnAlreadyListedModule(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("auto", "cloudflare", "ddns_updater"), nil)
	provider, err := a.resolveDynamicDNS()
	if err != nil {
		t.Fatal(err)
	}
	if provider != "ddns_updater" {
		t.Fatalf("provider = %q, want the module already listed", provider)
	}
}

// A recorded binding outranks the preference list, so a later release that
// reorders it cannot move an existing deployment onto a different updater.
func TestDynamicDNSAutoHonoursTheLockedBinding(t *testing.T) {
	lock := &caskLock{Bindings: map[string]map[string]string{
		deploymentBindingKey: {capabilityDynamicDNS: "ddns_updater"},
	}}
	a := dynamicDNSApp(dynamicDNSConfig("auto", "cloudflare"), lock)
	provider, err := a.resolveDynamicDNS()
	if err != nil {
		t.Fatal(err)
	}
	if provider != "ddns_updater" {
		t.Fatalf("provider = %q, want the locked binding to win", provider)
	}

	// Unless it stopped being valid: a locked implementation that cannot
	// address the configured vendor is re-resolved rather than kept.
	a = dynamicDNSApp(dynamicDNSConfig("auto", "tencentcloud"), lock)
	provider, err = a.resolveDynamicDNS()
	if err != nil {
		t.Fatal(err)
	}
	if provider != "ddns_go" {
		t.Fatalf("provider = %q, want re-resolution when the lock no longer fits", provider)
	}
}

func TestDynamicDNSRejectsUnusableSelections(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *config.File
		want string
	}{
		"unknown vendor": {
			cfg:  dynamicDNSConfig("auto", "not-a-vendor"),
			want: "not a known DNS platform",
		},
		"not an implementation": {
			cfg:  dynamicDNSConfig("lego", "cloudflare"),
			want: "not a dynamic DNS implementation",
		},
		// The legacy DNSPod token is registered for ddns_go alone.
		"implementation cannot address the vendor": {
			cfg:  dynamicDNSConfig("ddns_updater", "dnspod"),
			want: "cannot update records at",
		},
	} {
		a := dynamicDNSApp(tc.cfg, nil)
		_, err := a.resolveDynamicDNS()
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", name, err.Error(), tc.want)
		}
	}
}

// No request means no declared records, and a DDNS cask listed by hand is left
// entirely to its own configuration.
func TestDynamicDNSIsOptional(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("", "", "ddns_go"), nil)
	provider, err := a.resolveDynamicDNS()
	if err != nil {
		t.Fatal(err)
	}
	if provider != "" {
		t.Fatalf("provider = %q, want none", provider)
	}
}

// The selection seeds the chosen cask's vendor and tells every implementation
// whether it holds the declared records.
func TestDynamicDNSBindingSeedsTheSelectedCask(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("auto", "tencentcloud"), nil)
	a.applyDynamicDNSBinding("ddns_go")

	if a.env["DDNS_GO_DNS_PROVIDER"] != "tencentcloud" {
		t.Errorf("selected cask vendor = %q", a.env["DDNS_GO_DNS_PROVIDER"])
	}
	if a.env["DDNS_GO_DYNAMIC_DNS_MANAGED"] != "true" {
		t.Error("the selected cask was not told it holds the declared records")
	}
	if a.env["DDNS_UPDATER_DYNAMIC_DNS_MANAGED"] != "false" {
		t.Error("an unselected implementation was not told to leave the records alone")
	}
	if a.resolvedBindings[deploymentBindingKey][capabilityDynamicDNS] != "ddns_go" {
		t.Error("the binding was not recorded for the lock")
	}
}

// A vendor configured per service is the user being specific, and must not be
// overwritten by the deployment-wide default.
func TestDynamicDNSBindingDoesNotOverrideAnExplicitVendor(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("ddns_go", "tencentcloud"), nil)
	a.env["DDNS_GO_DNS_PROVIDER"] = "cloudflare"
	a.applyDynamicDNSBinding("ddns_go")
	if a.env["DDNS_GO_DNS_PROVIDER"] != "cloudflare" {
		t.Errorf("explicit vendor was overwritten with %q", a.env["DDNS_GO_DNS_PROVIDER"])
	}
}

// An overlap is reported but not refused: both updaters usually agree on the
// address and leave the record alone, so blocking the deployment would be
// stricter than the hazard warrants.
func TestDynamicDNSOverlapIsReported(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("auto", "cloudflare"), nil)
	a.order = []string{"core", "ddns_go", "ddns_updater"}
	a.env["BASE_DOMAIN"] = "nas.example.com"
	a.env["DDNS_GO_DNS_PLATFORM"] = "cloudflare"
	a.env["DDNS_UPDATER_DNS_PLATFORM"] = "cloudflare"

	overlaps := a.dynamicDNSOverlaps()
	if len(overlaps) == 0 {
		t.Fatal("expected an overlap when two updaters maintain the same records")
	}
	joined := strings.Join(overlaps, "\n")
	for _, want := range []string{"ddns_go", "ddns_updater", "nas.example.com"} {
		if !strings.Contains(joined, want) {
			t.Errorf("report %q does not mention %q", joined, want)
		}
	}
	// Each record is reported once, not once per claiming engine.
	if len(overlaps) != 2 {
		t.Errorf("reported %d overlaps, want one per record: %v", len(overlaps), overlaps)
	}
}

// Different vendors are not a conflict: holding one zone at two providers
// during a migration is a legitimate arrangement.
func TestDynamicDNSAllowsTwoUpdatersAtDifferentVendors(t *testing.T) {
	a := dynamicDNSApp(dynamicDNSConfig("auto", "cloudflare"), nil)
	a.order = []string{"core", "ddns_go", "ddns_updater"}
	a.env["BASE_DOMAIN"] = "nas.example.com"
	a.env["DDNS_GO_DNS_PLATFORM"] = "tencentcloud"
	a.env["DDNS_UPDATER_DNS_PLATFORM"] = "cloudflare"

	if overlaps := a.dynamicDNSOverlaps(); len(overlaps) != 0 {
		t.Fatalf("two vendors were treated as an overlap: %v", overlaps)
	}
}
