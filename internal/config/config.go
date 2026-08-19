package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/anas-project/ANAS/internal/localization"
	"github.com/anas-project/ANAS/internal/modulesource"
	"gopkg.in/yaml.v3"
)

var localConfigUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var moduleReleasePattern = regexp.MustCompile(`^(.+)-r([1-9][0-9]*)$`)

type File struct {
	// ModuleSource selects a distribution profile. It is retrieval policy, not
	// a container environment value; the exact artifact identity is frozen in
	// the resolution lock after resolution.
	ModuleSource   string          `yaml:"module_source"`
	Modules        ModuleSelection `yaml:"modules"`
	Global         Global          `yaml:"global"`
	Administration Administration  `yaml:"administration"`
	Identity       Identity        `yaml:"identity"`
	// IAM is the normalized runtime view of Identity.IAM. It is not a second
	// accepted YAML spelling.
	IAM        IAM            `yaml:"-"`
	DynamicDNS DynamicDNS     `yaml:"dynamic_dns"`
	Rollback   Rollback       `yaml:"rollback"`
	Secrets    map[string]any `yaml:"secrets"`
	Env        map[string]any `yaml:"env"`
}

type ModuleSelection struct {
	Order  []string
	Values map[string]ModuleConfig
}

func NewModuleSelection(names ...string) ModuleSelection {
	selection := ModuleSelection{Values: map[string]ModuleConfig{}}
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			selection.Order = append(selection.Order, name)
			selection.Values[name] = ModuleConfig{}
		}
	}
	return selection
}

func (m *ModuleSelection) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("modules must be a mapping of module names to configuration")
	}
	m.Order = nil
	m.Values = map[string]ModuleConfig{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := strings.ToLower(strings.TrimSpace(node.Content[i].Value))
		if name == "" {
			return fmt.Errorf("modules contains an empty name")
		}
		if _, exists := m.Values[name]; exists {
			return fmt.Errorf("module %q is configured more than once", name)
		}
		var module ModuleConfig
		if err := node.Content[i+1].Decode(&module); err != nil {
			return fmt.Errorf("modules.%s: %w", name, err)
		}
		m.Order = append(m.Order, name)
		m.Values[name] = module
	}
	return nil
}

// Administration contains deployment-wide defaults for human administrator
// accounts. Modules still declare whether they have a local account at all; this
// block only makes those independently managed accounts predictable to find.
type Administration struct {
	Bootstrap     BootstrapAdministrator `yaml:"bootstrap"`
	LocalAccounts LocalAccountDefaults   `yaml:"local_accounts"`
}

type BootstrapAdministrator struct {
	Username string `yaml:"username"`
}

type LocalAccountDefaults struct {
	PasswordLength int `yaml:"password_length"`
}

// Identity is the provider-oriented spelling of the deployment identity
// topology. IAM remains readable as a legacy top-level alias until existing
// configurations have migrated.
type Identity struct {
	Directory DirectoryProvider `yaml:"directory"`
	IAM       IAM               `yaml:"iam"`
}

type DirectoryProvider struct {
	Provider string `yaml:"provider"`
}

// DynamicDNS asks for the deployment's own A/AAAA records to be kept current
// without naming the program that does it.
//
// It is deliberately not a list. Several DDNS implementations may run at once
// -- listing them in `modules` still starts them, and nothing here stops that
// -- but exactly one of them holds the records ANAS declares. Two programs
// updating one record is not redundancy, it is a race whose loser silently
// reverts the winner.
type DynamicDNS struct {
	// Provider is the module that maintains the declared records, or "auto" to
	// let the runner pick from the implementations that can address the chosen
	// vendor. Empty means ANAS declares no records at all, and any DDNS module
	// listed in `modules` is left entirely to its own configuration.
	Provider string `yaml:"provider"`
	// DNSProvider is the vendor those records live at. It seeds the selected
	// module's own dns_provider, which may still be set per service to override.
	DNSProvider string `yaml:"dns_provider"`
}

// IAM selects the single identity provider for the deployment. Provider has no
// default: when an IAM consumer is enabled the runner fails rather than picking
// one, even if only one candidate exists. These values are deliberately absent
// from BaseEnv so a host environment variable cannot make the same config file
// produce a different deployment.
type IAM struct {
	// Provider is the IAM module name. Required whenever an enabled module
	// consumes the iam capability.
	Provider string `yaml:"provider"`
	// DefaultProtocol is the deployment-wide fallback for modules that leave
	// their protocol on "auto". It only applies to modules that actually
	// support it; others fall back to their manifest preference order.
	DefaultProtocol string `yaml:"default_protocol"`
}

// Rollback configures an optional persistent-data snapshot backend. Runtime
// artifacts are always immutable deployments; this section only controls data
// that lives outside those artifacts.
type Rollback struct {
	Snapshot Snapshot `yaml:"snapshot"`
}

type Snapshot struct {
	Backend string `yaml:"backend"`
	Source  string `yaml:"source"`
	Root    string `yaml:"root"`
	// KeepAuto is how many automatic snapshots survive a prune; manual and
	// pinned ones are neither counted nor collected. Int rather than int so an
	// explicit 0 ("keep none") stays distinguishable from an absent setting,
	// which takes the default of 5 -- and so `keep_auto: five` is rejected at
	// load time instead of silently becoming 0.
	KeepAuto Int `yaml:"keep_auto"`
}

type Global struct {
	// BaseDomain is the application and Web-entry namespace: services publish
	// hostnames beneath it, while directory providers own their identity domain
	// independently.
	BaseDomain string `yaml:"base_domain"`
	Email      string `yaml:"email"`
	// There is deliberately no data_path. Service data lives at
	// <workspace>/data so that copying one directory is a complete backup, and
	// a deployment-wide switch would make that untrue for everything at once.
	// Users who need the data on a larger disk put the whole workspace there.
	//
	// A single module may still be pointed elsewhere -- samba_fs.userdata_path is
	// the one that is -- but only through that module's own parameter, which
	// carries a change policy saying what moving it costs. The difference is
	// that the exception is named and policed rather than global and silent.
	Timezone        string `yaml:"timezone"`
	ContainerPrefix string `yaml:"container_prefix"`
	NetworkPrefix   string `yaml:"network_prefix"`
	HostIP          string `yaml:"host_ip"`
	// The two addresses a host-LAN module puts on the local segment: the
	// container's own address and the host-side macvlan bridge. Both are
	// optional, and both exist because the alternative is Docker's IPAM
	// choosing from a locally carved pool -- an allocation no DHCP server ever
	// agreed to. Pinning them is what lets an operator exclude the addresses
	// from the router's pool, and what lets the runner check for a conflict
	// before taking them.
	HostLANIP       string `yaml:"host_lan_ip"`
	HostLANBridgeIP string `yaml:"host_lan_bridge_ip"`
	// Unset means the check runs. It is a Bool rather than a plain string so
	// that turning it off is a deliberate `false` and not a typo that silently
	// disables a safety check.
	HostLANARPCheck Bool `yaml:"host_lan_arp_check"`
	// There is deliberately no dns_provider. A DNS vendor is chosen per engine
	// -- modules.lego.config.dns_provider, modules.ddns_go.config.dns_provider --
	// because certificates and dynamic DNS routinely live at different
	// vendors, and because the same vendor often needs different credentials
	// for each. See internal/dns.
	DNSServer       string `yaml:"dns_server"`
	DefaultLanguage string `yaml:"default_language"`
	DefaultLocale   string `yaml:"default_locale"`
	// Bool rather than bool: the schema defaults these to true, and a default
	// fills whatever the config left empty, so a type that could not express
	// "absent" would let the default overwrite a deliberate false. See
	// internal/config/scalar.go for why it is a checked string and not a
	// pointer.
	ChineseSpeedup      Bool `yaml:"chinese_speedup"`
	ChineseBuildSpeedup Bool `yaml:"chinese_build_speedup"`
	IPv4                Bool `yaml:"ipv4"`
	IPv6                Bool `yaml:"ipv6"`
	// VirtualDomain marks a domain that cannot obtain a publicly trusted
	// certificate — a reserved TLD such as .test or .lan, or any name without
	// public DNS. It means exactly one thing: do not attempt ACME. The
	// internal CA exists either way, because ACME issuance is never instant
	// and services need a certificate while it is in flight.
	VirtualDomain Bool `yaml:"virtual_domain"`
}

// globalBinding ties one Global field to the environment key it becomes.
type globalBinding struct {
	Parameter string
	Key       string
	value     func(Global) string
}

// globalBindings is the correspondence between the config's `global:` block and
// the environment. It is a table rather than a sequence of assignments because
// the mapping has a second reader: `anas config list` prints the env key beside
// every parameter, and deriving that from the parameter name would be a guess.
// The one case it would guess wrong is timezone, which becomes TZ.
//
// TestGlobalBindingsCoverEveryField fails when a field is added without an
// entry here, which is the failure this table exists to make loud: a global
// parameter that silently reaches no container is indistinguishable, from the
// outside, from one that is simply ignored.
var globalBindings = []globalBinding{
	{"base_domain", "BASE_DOMAIN", func(g Global) string { return g.BaseDomain }},
	{"email", "EMAIL", func(g Global) string { return g.Email }},
	{"timezone", "TZ", func(g Global) string { return g.Timezone }},
	{"container_prefix", "CONTAINER_PREFIX", func(g Global) string { return g.ContainerPrefix }},
	{"network_prefix", "NETWORK_PREFIX", func(g Global) string { return g.NetworkPrefix }},
	{"host_ip", "HOST_IP", func(g Global) string { return g.HostIP }},
	{"host_lan_ip", "HOST_LAN_IP", func(g Global) string { return g.HostLANIP }},
	{"host_lan_bridge_ip", "HOST_LAN_BRIDGE_IP", func(g Global) string { return g.HostLANBridgeIP }},
	{"host_lan_arp_check", "HOST_LAN_ARP_CHECK", func(g Global) string { return g.HostLANARPCheck.String() }},
	{"dns_server", "DNS_SERVER", func(g Global) string { return g.DNSServer }},
	{"default_language", "DEFAULT_LANGUAGE", func(g Global) string { return g.DefaultLanguage }},
	{"default_locale", "DEFAULT_LOCALE", func(g Global) string { return g.DefaultLocale }},
	{"chinese_speedup", "CHINESE_SPEEDUP", func(g Global) string { return g.ChineseSpeedup.String() }},
	{"chinese_build_speedup", "CHINESE_BUILD_SPEEDUP", func(g Global) string { return g.ChineseBuildSpeedup.String() }},
	{"ipv4", "IPV4", func(g Global) string { return g.IPv4.String() }},
	{"ipv6", "IPV6", func(g Global) string { return g.IPv6.String() }},
	// A false virtual_domain publishes nothing rather than "false": the key's
	// presence is the signal, and every reader tests for it that way.
	//
	// Unlike the three above it is a plain bool, because it has no schema
	// default: absent and false already mean the same thing, so there is
	// nothing for a pointer to distinguish.
	//
	// It is unprefixed like every other user setting. The ANAS_ prefix marks
	// keys the runner derives as a cross-module contract -- ANAS_IAM_*, ANAS_TLS_*
	// -- and this was the one user-facing parameter wearing it, which made the
	// prefix mean nothing in particular.
	{"virtual_domain", "VIRTUAL_DOMAIN", func(g Global) string {
		if g.VirtualDomain.True() {
			return "true"
		}
		return ""
	}},
}

// GlobalEnvKey returns the environment key a global parameter becomes, or ""
// when the parameter reaches no environment.
func GlobalEnvKey(parameter string) string {
	parameter = strings.ToLower(strings.TrimSpace(parameter))
	for _, binding := range globalBindings {
		if binding.Parameter == parameter {
			return binding.Key
		}
	}
	return ""
}

// GlobalParameters names every field of Global, which is exactly the set of
// parameters that may be written into the config's `global:` block.
//
// It is derived rather than written down because the alternative -- a second
// list maintained by hand -- had already drifted: virtual_domain is a Global
// field that the list did not mention, so addressing it through a module path
// wrote it to the raw `env:` block instead, where the typed field never saw
// it. Decoding uses KnownFields, so the reverse mistake is worse: a parameter
// wrongly believed to be global lands in `global:` and every later command
// fails to load the config at all.
func GlobalParameters() []string {
	t := reflect.TypeOf(Global{})
	out := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

type ModuleConfig struct {
	// Version selects an exact independently published Module release such as
	// 34.0.2-r2. Resolution writes the immutable OCI digest to config.lock.yml;
	// ordinary sync/apply never follows a moved tag.
	Version        string               `yaml:"version"`
	Enabled        *bool                `yaml:"enabled"`
	DependsOn      []string             `yaml:"depends_on"`
	Identity       ModuleIdentity       `yaml:"identity"`
	Administration ModuleAdministration `yaml:"administration"`
	Config         map[string]any       `yaml:"config"`
}

type ModuleIdentity struct {
	// LoginProtocol is the human-facing spelling of the IAM protocol selector.
	// It maps to the module's existing <PREFIX>_IAM_PROTOCOL contract so old env
	// configuration remains compatible while provisioning and login are no
	// longer presented as one setting.
	LoginProtocol string `yaml:"login_protocol"`
}

type ModuleAdministration struct {
	LocalAccounts map[string]LocalAccountOverride `yaml:"local_accounts"`
}

// LocalAccountOverride is intentionally fieldless. The mapping exists only so
// controlled import can validate a Manifest account ID while extracting its
// one-time password. Physical usernames are never user configuration.
type LocalAccountOverride struct{}

func (*LocalAccountOverride) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("local account configuration must be a mapping")
	}
	if len(node.Content) != 0 {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "username" {
				return fmt.Errorf("local administrator username is not configurable; the Module fixed_username or ANAS default determines it")
			}
		}
		return fmt.Errorf("local administrator credentials are accepted only as one-time `anas config import` input")
	}
	return nil
}

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg File
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	if cfg.Modules.Values == nil {
		cfg.Modules.Values = map[string]ModuleConfig{}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]any{}
	}
	if cfg.Env == nil {
		cfg.Env = map[string]any{}
	}
	cfg.ModuleSource = modulesource.DefaultName(cfg.ModuleSource)
	if _, ok := modulesource.LookupBuiltin(cfg.ModuleSource); !ok {
		return nil, fmt.Errorf("module_source must be official, official-cn, or cn")
	}
	// The CN source is a complete distribution profile: Module artifacts come
	// from CNB and the deployed services should use the matching published image
	// and download mirrors. An explicit false remains an escape hatch.
	if modulesource.UsesChineseDefaults(cfg.ModuleSource) && !cfg.Global.ChineseSpeedup.Set() {
		cfg.Global.ChineseSpeedup = BoolTrue
	}
	if len(cfg.Modules.Order) == 0 {
		return nil, fmt.Errorf("missing modules")
	}
	for name, module := range cfg.Modules.Values {
		if release := strings.TrimSpace(module.Version); release != "" {
			match := moduleReleasePattern.FindStringSubmatch(release)
			if match == nil {
				return nil, fmt.Errorf("modules.%s.version must be <semver>-r<N>", name)
			}
			if _, err := semver.NewVersion(match[1]); err != nil {
				return nil, fmt.Errorf("modules.%s.version %q is invalid: %w", name, release, err)
			}
			module.Version = release
			cfg.Modules.Values[name] = module
		}
	}
	cfg.Identity.Directory.Provider = strings.ToLower(strings.TrimSpace(cfg.Identity.Directory.Provider))
	cfg.Identity.IAM.Provider = strings.ToLower(strings.TrimSpace(cfg.Identity.IAM.Provider))
	if err := normalizeTopologyParameters(&cfg); err != nil {
		return nil, err
	}
	if cfg.Identity.Directory.Provider != "" && cfg.Identity.Directory.Provider != "samba_dc" {
		return nil, fmt.Errorf("identity.directory.provider must currently be samba_dc")
	}
	cfg.IAM = cfg.Identity.IAM
	cfg.Administration.Bootstrap.Username = strings.TrimSpace(cfg.Administration.Bootstrap.Username)
	if cfg.Administration.Bootstrap.Username != "" && !localConfigUsernamePattern.MatchString(cfg.Administration.Bootstrap.Username) {
		return nil, fmt.Errorf("administration.bootstrap.username is invalid")
	}
	cfg.DynamicDNS.Provider = strings.ToLower(strings.TrimSpace(cfg.DynamicDNS.Provider))
	cfg.DynamicDNS.DNSProvider = strings.TrimSpace(cfg.DynamicDNS.DNSProvider)
	if cfg.Administration.LocalAccounts.PasswordLength == 0 {
		cfg.Administration.LocalAccounts.PasswordLength = 24
	}
	if cfg.Administration.LocalAccounts.PasswordLength < 16 {
		return nil, fmt.Errorf("administration.local_accounts.password_length must be at least 16")
	}
	if cfg.DynamicDNS.Provider != "" && cfg.DynamicDNS.DNSProvider == "" {
		return nil, fmt.Errorf("dynamic_dns.provider is set but dynamic_dns.dns_provider is not; name the DNS vendor the records live at")
	}
	if cfg.Global.Timezone, err = localization.ValidateTimezone(cfg.Global.Timezone); err != nil {
		return nil, fmt.Errorf("global.timezone: %w", err)
	}
	if cfg.Global.DefaultLanguage, err = localization.NormalizeLanguage(cfg.Global.DefaultLanguage); err != nil {
		return nil, fmt.Errorf("global.default_language: %w", err)
	}
	if cfg.Global.DefaultLocale, err = localization.NormalizeLocale(cfg.Global.DefaultLocale); err != nil {
		return nil, fmt.Errorf("global.default_locale: %w", err)
	}
	if cfg.Global.DefaultLocale == "" {
		if inferred, ok, err := localization.LocaleFromExplicitLanguage(cfg.Global.DefaultLanguage); err != nil {
			return nil, fmt.Errorf("global.default_language: %w", err)
		} else if ok {
			cfg.Global.DefaultLocale = inferred
		}
	}
	return &cfg, nil
}

// Owner markers used in the ownership map returned by BaseEnvWithOwners.
// The empty string marks a globally scoped key; OwnerUserSecret marks a
// user-provided secret that is only distributed to modules that claim it.
const OwnerUserSecret = "!user-secret"

// chineseSpeedupDefaults are needed after release: published images come from
// CNB, while Nextcloud still downloads application metadata and pinned runtime
// assets during first start and upgrades.
var chineseSpeedupDefaults = map[string]string{
	"GITHUB_DOWNLOAD_PROXY_PREFIX": "https://files.m.daocloud.io/",
	"NEXTCLOUD_APPSTORE_URL":       "https://files.m.daocloud.io/apps.nextcloud.com/api/v1",
	"ANAS_IMAGE_REGISTRY":          "docker.cnb.cool/anas.dev/anas",
}

// chineseBuildSpeedupDefaults are consumed while materialising hooks or
// executing Dockerfiles. They deliberately live behind a separate switch:
// changing them requires rebuilding images, whereas CHINESE_SPEEDUP only
// selects published runtime artifacts and runtime download mirrors.
var chineseBuildSpeedupDefaults = map[string]string{
	"APT_MIRROR_URL":                     "https://mirrors.aliyun.com",
	"APK_MIRROR_URL":                     "https://mirrors.aliyun.com",
	"NPM_REGISTRY_URL":                   "https://registry.npmmirror.com",
	"GOPROXY_URL":                        "https://goproxy.cn,direct",
	"BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX": "https://files.m.daocloud.io/",
	"DOCKER_HUB_REGISTRY":                "m.daocloud.io/docker.io",
	"LLNG_DOCKER_HUB_REGISTRY":           "docker.1ms.run",
	"GHCR_REGISTRY":                      "ghcr.nju.edu.cn",
}

func (f *File) BaseEnv() map[string]string {
	env, _ := f.BaseEnvWithOwners()
	return env
}

// BaseEnvWithOwners flattens the config into environment values and reports
// which config section introduced each key: "" for global sections,
// OwnerUserSecret for user secrets, and the module name for module config.
func (f *File) BaseEnvWithOwners() (map[string]string, map[string]string) {
	return f.baseEnvWithOwners(nil)
}

// BaseEnvWithOwnersUsing preserves the config package's established source
// precedence while allowing the manifest-aware runner to choose the runtime
// key for Module parameters. Returning an empty key uses the module-name
// fallback, which keeps callers that have no registry fully compatible.
func (f *File) BaseEnvWithOwnersUsing(moduleKey func(module, parameter string) string) (map[string]string, map[string]string) {
	return f.baseEnvWithOwners(moduleKey)
}

func (f *File) baseEnvWithOwners(moduleKey func(module, parameter string) string) (map[string]string, map[string]string) {
	env := map[string]string{}
	owners := map[string]string{}
	set := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			env[key] = value
			owners[key] = ""
		}
	}
	// DATA_PATH is not sourced from the config; the runner injects it from the
	// workspace it resolved.
	for _, binding := range globalBindings {
		set(binding.Key, binding.value(f.Global))
	}
	for k, v := range f.Secrets {
		key := EnvKey(k)
		env[key] = Scalar(v)
		owners[key] = OwnerUserSecret
	}
	for k, v := range f.Env {
		key := EnvKey(k)
		env[key] = Scalar(v)
		owners[key] = ""
	}
	for name, module := range f.Modules.Values {
		prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		if protocol := strings.ToLower(strings.TrimSpace(module.Identity.LoginProtocol)); protocol != "" {
			key := prefix + "_IAM_PROTOCOL"
			if moduleKey != nil {
				if mapped := moduleKey(name, "iam_protocol"); mapped != "" {
					key = mapped
				}
			}
			env[key] = protocol
			owners[key] = name
		}
		for k, v := range module.Config {
			key := EnvKey(k)
			if moduleKey != nil {
				if mapped := moduleKey(name, k); mapped != "" {
					key = mapped
				} else if !strings.HasPrefix(key, prefix+"_") {
					key = prefix + "_" + key
				}
			} else if !strings.HasPrefix(key, prefix+"_") {
				key = prefix + "_" + key
			}
			env[key] = Scalar(v)
			owners[key] = name
		}
	}
	if username := strings.TrimSpace(f.Administration.Bootstrap.Username); username != "" {
		env["ANAS_BOOTSTRAP_ADMIN_USERNAME"] = username
		owners["ANAS_BOOTSTRAP_ADMIN_USERNAME"] = ""
		if f.Identity.Directory.Provider == "samba_dc" || f.Identity.Directory.Provider == "" {
			env["SAMBA_DC_ADMIN_NAME"] = username
			owners["SAMBA_DC_ADMIN_NAME"] = "samba_dc"
		}
	}
	if strings.EqualFold(strings.TrimSpace(env["CHINESE_SPEEDUP"]), "true") {
		for key, value := range chineseSpeedupDefaults {
			if strings.TrimSpace(env[key]) == "" {
				env[key] = value
				owners[key] = ""
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(env["CHINESE_BUILD_SPEEDUP"]), "true") {
		for key, value := range chineseBuildSpeedupDefaults {
			if strings.TrimSpace(env[key]) == "" {
				env[key] = value
				owners[key] = ""
			}
		}
	}
	return env, owners
}

// EnvKey turns a config parameter name into the environment key it becomes.
//
// There is one table, globalBindings, and this is the only place that consults
// it. There used to be three functions answering this question and they
// disagreed: this one mapped `domain` to DOMAIN while the runner's mapped it to
// BASE_DOMAIN, and none of them knew that virtual_domain becomes
// VIRTUAL_DOMAIN. The wrong answer was reachable, merely hidden behind the
// path that happened to be taken.
//
// Names that differ from their key do so for a reason worth stating: TZ is what
// every container image expects, and BASE_DOMAIN says "the zone" where a bare
// DOMAIN would be confused with the <CASK>_DOMAIN family, which are hostnames
// inside it.
func EnvKey(key string) string {
	key = strings.TrimSpace(key)
	if bound := GlobalEnvKey(key); bound != "" {
		return bound
	}
	return strings.ToUpper(key)
}

func Scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
