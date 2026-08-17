package runner

import (
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

// The schema is embedded, so a malformed edit is a build that ships a panic.
func TestGlobalSchemaParses(t *testing.T) {
	schema, err := loadGlobalSchema(globalsYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Defaults) == 0 || len(schema.Changes) == 0 || len(schema.Required) == 0 {
		t.Fatal("global schema is empty")
	}
	// Parameters are declared in lower snake_case and become env keys, the same
	// rule module manifests follow. There are no exceptions: IPv4 and IPv6 used
	// to keep a mixed-case spelling, which forced every mapper to carry a
	// special case for them and made the mapping non-uniform for no benefit.
	for _, key := range append(append([]string{}, schema.Required...), keysOf(schema.Defaults)...) {
		if !isEnvKey(key) {
			t.Errorf("global parameter produced %q, which is not an env key", key)
		}
	}
	for _, want := range []string{"BASE_DOMAIN", "EMAIL"} {
		if !contains(schema.Required, want) {
			t.Errorf("global schema no longer requires %s", want)
		}
	}
	if got := schema.Changes["chinese_build_speedup"].Effect; got != "image_rebuild" {
		t.Fatalf("chinese_build_speedup effect = %q, want image_rebuild", got)
	}
}

// Ownership is the whole scoping mechanism: a deployment-wide key recorded
// under a module's name silently disappears from every other module's .env, with
// no error anywhere. This pins the keys that must stay global.
func TestDeploymentWideKeysAreGloballyOwned(t *testing.T) {
	t.Setenv("TZ", "Asia/Tokyo")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LC_TIME", "")
	t.Setenv("LANG", "pt_BR.UTF-8")
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{
		reg: reg, env: map[string]string{}, envOwner: map[string]string{},
		order: []string{"samba_fs", "traefik", "lego"},
	}
	a.env["HOST_IP"] = "10.254.0.2"
	a.env["INTERFACE"] = "anas-test-peer"
	a.env["HOST_SUBNET_MASK"] = "24"
	a.applyModuleDefaults()
	if a.env["TZ"] != "Asia/Tokyo" || a.env["DEFAULT_LANGUAGE"] != "pt-BR" || a.env["DEFAULT_LOCALE"] != "pt-BR" {
		t.Fatalf("host localization defaults were not rendered: TZ=%q language=%q locale=%q", a.env["TZ"], a.env["DEFAULT_LANGUAGE"], a.env["DEFAULT_LOCALE"])
	}
	if err := a.applyHostNetwork(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"TZ", "CONTAINER_PREFIX", "NETWORK_PREFIX",
		"DEFAULT_LANGUAGE", "DEFAULT_LOCALE", "CHINESE_SPEEDUP", "CHINESE_BUILD_SPEEDUP", "IPV4", "IPV6", "DNS_SERVER",
		"HOST_IP", "INTERFACE", "HOST_SUBNET_MASK", "HOST_DNS_SERVER", "SERVER_NAME",
		"LOCAL_DNS_SERVER", "HOST_HAS_IPV6", "HOST_SEGMENT", "VLAN_SEGMENT",
		"VLAN_GATEWAY_IP", "VLAN_BRIDGE_IP", "VLAN_INTERFACE", "VLAN_BRIDGE_INTERFACE",
		"VLAN_SUBNET_MASK",
	} {
		owner, tracked := a.envOwner[key]
		if !tracked {
			t.Errorf("%s has no owner; nothing publishes it any more", key)
			continue
		}
		if owner != globalScope {
			t.Errorf("%s is owned by %q; it must stay global or modules outside that owner lose it", key, owner)
		}
	}
	// The file-sharing parameters went the other way: they belong to samba_fs
	// and keep their bare names through config.exports.
	//
	// This is the defaulted path. A value the user writes into the config's top
	// level `env:` block is globally owned before any module default is applied,
	// which is true of every key set there and is what that block means; such a
	// value is therefore visible deployment-wide, exactly as it was when these
	// parameters lived in the core module.
	for _, key := range []string{"SHARE_DIR_NAME", "SHARE_ACCESS_MODE", "SHARE_GUEST_READ_ONLY", "USE_DEFAULT_DOMAIN"} {
		if owner := a.envOwner[key]; owner != "samba_fs" {
			t.Errorf("%s is owned by %q, want samba_fs", key, owner)
		}
	}
}

// Binding a typed global field into .env proves transport, not usefulness. A
// dead global used to survive indefinitely because that transport test passed
// even though no module, Compose file, or container asset read the value.
func TestGlobalParametersHaveRuntimeConsumers(t *testing.T) {
	modules := filepath.Join("..", "..", "modules")
	for _, parameter := range config.GlobalParameters() {
		key := config.GlobalEnvKey(parameter)
		if runnerConsumedGlobals[parameter] {
			if !treeContainsRuntimeKey(".", key) {
				t.Errorf("global.%s is declared as runner-consumed but %s appears nowhere in the runner", parameter, key)
			}
			continue
		}
		if !treeContainsRuntimeKey(modules, key) {
			t.Errorf("global.%s produces %s but no shipped module reads it", parameter, key)
		}
	}
}

// runnerConsumedGlobals are the parameters whose reader is the runner rather
// than a container. They are named here rather than exempted, because the
// failure this test exists to catch -- a parameter nothing reads -- is just as
// possible for them; the check simply has to look at the runner's own source
// instead of the module tree.
//
// Both describe how the runner attaches a deployment to the host LAN, and both
// are consumed before any container exists: the bridge address seeds the
// macvlan plan, and the ARP check governs whether the runner probes an address
// before taking it. Neither reaches a container, and giving them one to keep
// this test quiet would be inventing a reader.
var runnerConsumedGlobals = map[string]bool{
	"host_lan_bridge_ip": true,
	"host_lan_arp_check": true,
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Artifact start, stop and rollback recover the deployment-wide environment
// from a file beside the rendered modules rather than from the config, so they
// use the values the release was actually built with.
func TestAdoptReleaseEnvReadsGlobalFile(t *testing.T) {
	release := t.TempDir()
	if err := writeEnv(filepath.Join(release, globalEnvFile), map[string]string{"HOST_IP": "10.0.0.2"}); err != nil {
		t.Fatal(err)
	}
	a := &app{env: map[string]string{}}
	a.adoptReleaseEnv(release)
	if a.env["HOST_IP"] != "10.0.0.2" {
		t.Fatalf("HOST_IP = %q", a.env["HOST_IP"])
	}

	// A release without the file leaves the environment alone rather than
	// half-populating it: the caller's next step reports the release as
	// unreadable, which is more useful than starting with missing values.
	empty := &app{env: map[string]string{"HOST_IP": "10.0.0.9"}}
	empty.adoptReleaseEnv(t.TempDir())
	if empty.env["HOST_IP"] != "10.0.0.9" {
		t.Fatalf("a missing global env file disturbed the environment: %q", empty.env["HOST_IP"])
	}
}
