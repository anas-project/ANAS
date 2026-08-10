package config

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStructuredConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`modules:
  - traefik
global:
  base_domain: nas.example.com
  email: admin@example.com
  default_service_root_password: change-me
env:
  basicauth_user: admin
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Modules[0]; got != "traefik" {
		t.Fatalf("module = %q, want traefik", got)
	}
	env := cfg.BaseEnv()
	if env["BASE_DOMAIN"] != "nas.example.com" {
		t.Fatalf("BASE_DOMAIN = %q", env["BASE_DOMAIN"])
	}
	if env["BASICAUTH_USER"] != "admin" {
		t.Fatalf("BASICAUTH_USER = %q", env["BASICAUTH_USER"])
	}
}

func TestLowercaseServiceEnvIsNormalized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`modules:
  - nextcloud
global:
  base_domain: nas.example.com
  email: admin@example.com
  default_service_root_password: change-me
services:
  nextcloud:
    env:
      domain_prefix: cloud
      upload_max_size: 32G
env:
  ipv4: false
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	env := cfg.BaseEnv()
	if env["NEXTCLOUD_DOMAIN_PREFIX"] != "cloud" {
		t.Fatalf("NEXTCLOUD_DOMAIN_PREFIX = %q", env["NEXTCLOUD_DOMAIN_PREFIX"])
	}
	if env["NEXTCLOUD_UPLOAD_MAX_SIZE"] != "32G" {
		t.Fatalf("NEXTCLOUD_UPLOAD_MAX_SIZE = %q", env["NEXTCLOUD_UPLOAD_MAX_SIZE"])
	}
	if env["IPV4"] != "false" {
		t.Fatalf("IPV4 = %q", env["IPV4"])
	}
}

func TestLoadRejectsLegacyKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`mods:
  - traefik
envs:
  BASE_DOMAIN: nas.example.com
`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected legacy config keys to be rejected")
	}
}

func TestLoadRejectsShortDefaultServicePassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`modules: [traefik]
global:
  base_domain: nas.example.com
  email: admin@example.com
  default_service_root_password: short
`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a password length validation error")
	}
}

func TestSetScalarPreservesCommentsAndAddsServiceOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`modules:
  - samba_dc
global:
  base_domain: nas.example.com
  email: admin@example.com
  default_service_root_password: change-me
# keep this operator note
env:
  SHARE_GUEST_READ_ONLY: "No"
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SetScalar(path, []string{"services", "samba_dc", "env", "user_min_pass_length"}, "10"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# keep this operator note") {
		t.Fatal("existing comment was lost")
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.BaseEnv()["SAMBA_DC_USER_MIN_PASS_LENGTH"]; got != "10" {
		t.Fatalf("service override = %q", got)
	}
}

// A Global field with no binding reaches no container. That failure is silent
// from the outside -- the parameter accepts a value, the config loads, and
// nothing happens -- so it is caught here instead.
func TestGlobalBindingsCoverEveryField(t *testing.T) {
	bound := map[string]bool{}
	for _, binding := range globalBindings {
		if binding.Key == "" {
			t.Fatalf("global parameter %q is bound to an empty env key", binding.Parameter)
		}
		if bound[binding.Parameter] {
			t.Fatalf("global parameter %q is bound twice", binding.Parameter)
		}
		bound[binding.Parameter] = true
	}
	for _, parameter := range GlobalParameters() {
		if !bound[parameter] {
			t.Errorf("global parameter %q has no entry in globalBindings, so setting it changes nothing", parameter)
		}
		if GlobalEnvKey(parameter) == "" {
			t.Errorf("GlobalEnvKey(%q) is empty", parameter)
		}
	}
	for parameter := range bound {
		if !contains(GlobalParameters(), parameter) {
			t.Errorf("globalBindings names %q, which is not a field of Global", parameter)
		}
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// The env keys the bindings promise are the keys the environment actually gets.
func TestGlobalBindingsProduceDeclaredKeys(t *testing.T) {
	f := &File{Global: Global{
		BaseDomain: "nas.example.com", Email: "a@example.com", Timezone: "Asia/Tokyo",
		ContainerPrefix: "c_", ImagePrefix: "i_", NetworkPrefix: "n_",
		HostIP: "10.0.0.2", DNSServer: "1.1.1.1",
		DefaultServiceRootPassword: "ChangeMe1!", VirtualDomain: BoolTrue,
		BasicAuthUser: "admin", DefaultLanguage: "en",
		ChineseSpeedup: BoolFalse, IPv4: BoolTrue, IPv6: BoolFalse,
	}}
	env := f.BaseEnv()
	for _, parameter := range GlobalParameters() {
		key := GlobalEnvKey(parameter)
		if env[key] == "" {
			t.Errorf("global.%s should have produced %s, which is unset", parameter, key)
		}
	}
	if env["TZ"] != "Asia/Tokyo" || env["VIRTUAL_DOMAIN"] != "true" || env["BASE_DOMAIN"] != "nas.example.com" {
		t.Fatalf("renamed keys are wrong: TZ=%q VIRTUAL_DOMAIN=%q BASE_DOMAIN=%q",
			env["TZ"], env["VIRTUAL_DOMAIN"], env["BASE_DOMAIN"])
	}

	// A false virtual_domain publishes nothing at all, which is what every
	// reader tests for.
	off := &File{Global: Global{}}
	if _, ok := off.BaseEnv()["VIRTUAL_DOMAIN"]; ok {
		t.Fatal("a false virtual_domain must not publish VIRTUAL_DOMAIN")
	}
}

// The mapping from parameter to environment key must be injective. Two
// parameters sharing a key is the ambiguity this consolidation exists to
// remove: whichever is applied last wins, and which that is depends on map
// iteration order rather than on anything a user could reason about.
func TestGlobalEnvKeysAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, parameter := range GlobalParameters() {
		key := GlobalEnvKey(parameter)
		if other, clash := seen[key]; clash {
			t.Errorf("global.%s and global.%s both become %s", other, parameter, key)
		}
		seen[key] = parameter
	}
}

// One function answers "what env key does this parameter become". EnvKey used
// to disagree with the runner's copy on the two parameters whose names differ
// from their keys, and the disagreement was reachable.
func TestEnvKeyAgreesWithTheBindingTable(t *testing.T) {
	for _, parameter := range GlobalParameters() {
		if got, want := EnvKey(parameter), GlobalEnvKey(parameter); got != want {
			t.Errorf("EnvKey(%q) = %q, but the binding table says %q", parameter, got, want)
		}
	}
	// A parameter with no binding falls through to the uniform rule.
	if got := EnvKey("basicauth_user"); got != "BASICAUTH_USER" {
		t.Errorf("EnvKey(basicauth_user) = %q", got)
	}
	// The mixed-case spellings are gone: every key is upper case now.
	if got := EnvKey("ipv4"); got != "IPV4" {
		t.Errorf("EnvKey(ipv4) = %q, want IPV4", got)
	}
}

// The reason these three are pointers. The schema defaults them to true, and a
// default fills whatever the config left empty, so a plain bool would make
// "ipv6: false" indistinguishable from silence: the user writes the setting,
// the command accepts it, and the default turns it back on.
func TestOptionalBooleansDistinguishFalseFromAbsent(t *testing.T) {
	off := (&File{Global: Global{IPv6: BoolFalse}}).BaseEnv()
	if got, ok := off["IPV6"]; !ok || got != "false" {
		t.Fatalf("an explicit false published %q (present=%v), want \"false\"", got, ok)
	}

	// Absent publishes nothing, which is what leaves the schema default free to
	// apply. Publishing "false" here would be the same bug in the other
	// direction: it would pin the value and the default could never take.
	absent := (&File{Global: Global{}}).BaseEnv()
	if _, ok := absent["IPV6"]; ok {
		t.Fatal("an unset optional boolean published a value, so the default can never apply")
	}

	on := (&File{Global: Global{IPv6: BoolTrue}}).BaseEnv()
	if on["IPV6"] != "true" {
		t.Fatalf("IPV6 = %q, want \"true\"", on["IPV6"])
	}
}

// A typo must not become the opposite instruction. With a plain string field
// "flase" would be stored verbatim and a cask testing `!= "false"` would read
// it as true: the setting written, the command silent, the behaviour reversed.
func TestBoolRejectsAnythingThatIsNotABoolean(t *testing.T) {
	for _, bad := range []string{"flase", "yes", "1", "on"} {
		var g struct {
			IPv6 Bool `yaml:"ipv6"`
		}
		if err := yaml.Unmarshal([]byte("ipv6: "+bad+"\n"), &g); err == nil {
			t.Errorf("%q was accepted as a boolean (got %q)", bad, g.IPv6)
		}
	}
	// Both spellings of the real thing are accepted, quoted or not.
	for raw, want := range map[string]Bool{"false": BoolFalse, "true": BoolTrue, `"false"`: BoolFalse} {
		var g struct {
			IPv6 Bool `yaml:"ipv6"`
		}
		if err := yaml.Unmarshal([]byte("ipv6: "+raw+"\n"), &g); err != nil {
			t.Errorf("%s was rejected: %v", raw, err)
		} else if g.IPv6 != want {
			t.Errorf("%s -> %q, want %q", raw, g.IPv6, want)
		}
	}
	// Absent stays absent, which is what lets a schema default apply.
	var g struct {
		IPv6 Bool `yaml:"ipv6"`
	}
	if err := yaml.Unmarshal([]byte("other: 1\n"), &g); err != nil || g.IPv6.Set() {
		t.Fatalf("an absent setting was not unset: %q %v", g.IPv6, err)
	}
}
