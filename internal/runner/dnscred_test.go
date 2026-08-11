package runner

import (
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// dnsCredApp builds a deployment running lego alongside both DDNS engines,
// which is the arrangement where credential separation actually matters.
func dnsCredApp(env map[string]string) *app {
	owners := map[string]string{}
	for key := range env {
		owners[key] = config.OwnerUserSecret
	}
	return &app{
		order: []string{"core", "lego", "ddns_go", "ddns_updater"},
		reg: map[string]Module{
			"core":         {Name: "core", EnvPrefix: "CORE"},
			"lego":         {Name: "lego", EnvPrefix: "LEGO"},
			"ddns_go":      {Name: "ddns_go", EnvPrefix: "DDNS_GO"},
			"ddns_updater": {Name: "ddns_updater", EnvPrefix: "DDNS_UPDATER"},
		},
		deps: map[string][]string{
			"lego": {"core"}, "ddns_go": {"core"}, "ddns_updater": {"core"},
		},
		env:      env,
		envOwner: owners,
	}
}

// One credential written once drives both engines, because Tencent Cloud uses
// the same API key for its ACME and its record APIs.
func TestSharedCredentialReachesEveryEngine(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER":       "tencentcloud",
		"DDNS_GO_DNS_PROVIDER":    "tencentcloud",
		"TENCENTCLOUD_SECRET_ID":  "id-value",
		"TENCENTCLOUD_SECRET_KEY": "key-value",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, want string }{
		{"LEGO_TENCENTCLOUD_SECRET_ID", "id-value"},
		{"LEGO_TENCENTCLOUD_SECRET_KEY", "key-value"},
		{"DDNS_GO_TENCENTCLOUD_SECRET_ID", "id-value"},
		{"DDNS_GO_TENCENTCLOUD_SECRET_KEY", "key-value"},
	} {
		if got := a.env[tc.key]; got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
	// The scope rules must attribute each copy to its own cask, or the prefix
	// rule alone would be doing the work and a rename could break isolation.
	if a.envOwner["DDNS_GO_TENCENTCLOUD_SECRET_KEY"] != "ddns_go" {
		t.Errorf("owner = %q, want ddns_go", a.envOwner["DDNS_GO_TENCENTCLOUD_SECRET_KEY"])
	}
	if a.env["LEGO_DNS_PLATFORM"] != "tencentcloud" {
		t.Errorf("resolved platform = %q", a.env["LEGO_DNS_PLATFORM"])
	}
	// ddns_updater never asked for a platform, so nothing is published for it.
	if _, ok := a.env["DDNS_UPDATER_TENCENTCLOUD_SECRET_ID"]; ok {
		t.Error("an engine with no dns_provider received credentials")
	}
}

// The canonical spelling is an input only. Leaving it addressable would let a
// cask reach a credential without the prefix that scopes it.
func TestCanonicalSecretIsNeverDelivered(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER":       "tencentcloud",
		"TENCENTCLOUD_SECRET_ID":  "id-value",
		"TENCENTCLOUD_SECRET_KEY": "key-value",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	for _, key := range a.userSecretKeys() {
		if strings.HasPrefix(key, "TENCENTCLOUD_") {
			if _, ok := a.scopedEnv("lego")[key]; ok {
				t.Errorf("lego received the canonical secret %s", key)
			}
		}
	}
	if _, ok := a.scopedEnv("lego")["LEGO_TENCENTCLOUD_SECRET_ID"]; !ok {
		t.Error("lego did not receive its materialised credential")
	}
}

// Traefik depends on lego, so the dependency-closure rule would otherwise hand
// it lego's DNS API token -- a credential it has no use for. Materialised
// credentials come from user config rather than the generated secret store, so
// nothing marks them sensitive unless the runner does.
func TestMaterialisedCredentialsDoNotFollowTheDependencyClosure(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER":       "tencentcloud",
		"TENCENTCLOUD_SECRET_ID":  "id-value",
		"TENCENTCLOUD_SECRET_KEY": "key-value",
	})
	a.order = append(a.order, "traefik")
	a.reg["traefik"] = Module{Name: "traefik", EnvPrefix: "TRAEFIK"}
	a.deps["traefik"] = []string{"core", "lego"}

	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	traefik := a.scopedEnv("traefik")
	for _, key := range []string{"LEGO_TENCENTCLOUD_SECRET_ID", "LEGO_TENCENTCLOUD_SECRET_KEY"} {
		if _, ok := traefik[key]; ok {
			t.Errorf("traefik received %s through lego's dependency closure", key)
		}
	}
	// Nor does anything else of lego's, credential or not: a rendered .env now
	// carries what the cask declares rather than what its dependencies own.
	// The credential gate still matters, because it also governs the
	// calculate-phase view that declarations do not.
	if _, ok := traefik["LEGO_DNS_PLATFORM"]; ok {
		t.Error("traefik received an undeclared value from lego")
	}
}

// Namecheap is the case the whole mechanism exists for: the same vendor, two
// engines, two credential objects that cannot be derived from each other.
func TestSeparateCredentialsStayWithTheirEngine(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER":               "namecheap",
		"DDNS_GO_DNS_PROVIDER":            "namecheap",
		"NAMECHEAP_API_USER":              "api-user",
		"NAMECHEAP_API_KEY":               "api-key",
		"DDNS_GO_NAMECHEAP_DDNS_PASSWORD": "ddns-password",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	legoEnv := a.scopedEnv("lego")
	goEnv := a.scopedEnv("ddns_go")

	if legoEnv["LEGO_NAMECHEAP_API_KEY"] != "api-key" {
		t.Error("lego did not receive the DNS API credential")
	}
	if goEnv["DDNS_GO_NAMECHEAP_DDNS_PASSWORD"] != "ddns-password" {
		t.Error("ddns_go did not receive the dynamic DNS password")
	}
	if _, ok := goEnv["LEGO_NAMECHEAP_API_KEY"]; ok {
		t.Error("ddns_go can read lego's DNS API credential")
	}
	if _, ok := legoEnv["DDNS_GO_NAMECHEAP_DDNS_PASSWORD"]; ok {
		t.Error("lego can read ddns_go's dynamic DNS password")
	}
}

// Two engines on the same vendor with different accounts: an explicit
// per-engine value must win over the shared one rather than be overwritten.
func TestExplicitPerEngineValueWinsOverShared(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER":               "tencentcloud",
		"DDNS_GO_DNS_PROVIDER":            "tencentcloud",
		"TENCENTCLOUD_SECRET_ID":          "shared-id",
		"TENCENTCLOUD_SECRET_KEY":         "shared-key",
		"DDNS_GO_TENCENTCLOUD_SECRET_ID":  "own-id",
		"DDNS_GO_TENCENTCLOUD_SECRET_KEY": "own-key",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	if a.env["DDNS_GO_TENCENTCLOUD_SECRET_ID"] != "own-id" {
		t.Errorf("explicit value was overwritten: %q", a.env["DDNS_GO_TENCENTCLOUD_SECRET_ID"])
	}
	if a.env["LEGO_TENCENTCLOUD_SECRET_ID"] != "shared-id" {
		t.Errorf("lego did not fall back to the shared value: %q", a.env["LEGO_TENCENTCLOUD_SECRET_ID"])
	}
}

func TestMaterializeRejectsUnusablePlatforms(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"unknown platform": {
			env:  map[string]string{"LEGO_DNS_PROVIDER": "not-a-vendor"},
			want: "not a known DNS platform",
		},
		// lego v5 removed dnspod, so a legacy token cannot issue certificates
		// even though it still updates records.
		"platform the engine cannot address": {
			env:  map[string]string{"LEGO_DNS_PROVIDER": "dnspod"},
			want: "cannot address it",
		},
		"missing credential": {
			env: map[string]string{
				"LEGO_DNS_PROVIDER":      "tencentcloud",
				"TENCENTCLOUD_SECRET_ID": "id-only",
			},
			want: "TENCENTCLOUD_SECRET_KEY",
		},
	} {
		a := dnsCredApp(tc.env)
		err := a.materializeDNSCredentials()
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", name, err.Error(), tc.want)
		}
	}
}

// An optional credential must not be demanded, and must still be carried when
// the user supplies it.
func TestOptionalCredentialIsNotRequired(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER": "vercel",
		"VERCEL_API_TOKEN":  "token",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatalf("optional credential was treated as required: %v", err)
	}
	if _, ok := a.env["LEGO_VERCEL_TEAM_ID"]; ok {
		t.Error("an absent optional credential was materialised as empty")
	}

	a = dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER": "vercel",
		"VERCEL_API_TOKEN":  "token",
		"VERCEL_TEAM_ID":    "team",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	if a.env["LEGO_VERCEL_TEAM_ID"] != "team" {
		t.Error("a supplied optional credential was not materialised")
	}
}

// Aliases resolve to one canonical platform, so `aliyun` and `alidns` are the
// same deployment rather than two engines silently pointing at different code.
func TestAliasResolvesToCanonicalPlatform(t *testing.T) {
	a := dnsCredApp(map[string]string{
		"LEGO_DNS_PROVIDER":    "aliyun",
		"DDNS_GO_DNS_PROVIDER": "alidns",
		"ALICLOUD_ACCESS_KEY":  "ak",
		"ALICLOUD_SECRET_KEY":  "sk",
	})
	if err := a.materializeDNSCredentials(); err != nil {
		t.Fatal(err)
	}
	if a.env["LEGO_DNS_PLATFORM"] != "alidns" || a.env["DDNS_GO_DNS_PLATFORM"] != "alidns" {
		t.Fatalf("alias did not resolve: lego=%q ddns_go=%q",
			a.env["LEGO_DNS_PLATFORM"], a.env["DDNS_GO_DNS_PLATFORM"])
	}
	summary := a.dnsPlanSummary()
	if !strings.Contains(summary, "ddns_go/lego credentials: shared") &&
		!strings.Contains(summary, "lego/ddns_go credentials: shared") {
		t.Errorf("plan summary does not report shared credentials:\n%s", summary)
	}
}
