package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type File struct {
	Modules    []string           `yaml:"modules"`
	Global     Global             `yaml:"global"`
	IAM        IAM                `yaml:"iam"`
	DynamicDNS DynamicDNS         `yaml:"dynamic_dns"`
	Rollback   Rollback           `yaml:"rollback"`
	Secrets    map[string]any     `yaml:"secrets"`
	Services   map[string]Service `yaml:"services"`
	Env        map[string]any     `yaml:"env"`
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
	// Provider is the cask that maintains the declared records, or "auto" to
	// let the runner pick from the implementations that can address the chosen
	// vendor. Empty means ANAS declares no records at all, and any DDNS cask
	// listed in `modules` is left entirely to its own configuration.
	Provider string `yaml:"provider"`
	// DNSProvider is the vendor those records live at. It seeds the selected
	// cask's own dns_provider, which may still be set per service to override.
	DNSProvider string `yaml:"dns_provider"`
}

// IAM selects the single identity provider for the deployment. Provider has no
// default: when an IAM consumer is enabled the runner fails rather than picking
// one, even if only one candidate exists. These values are deliberately absent
// from BaseEnv so a host environment variable cannot make the same config file
// produce a different deployment.
type IAM struct {
	// Provider is the IAM cask name. Required whenever an enabled cask
	// consumes the iam capability.
	Provider string `yaml:"provider"`
	// DefaultProtocol is the deployment-wide fallback for casks that leave
	// their protocol on "auto". It only applies to casks that actually
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
	// BaseDomain is the root every hostname and the AD realm derive from:
	// traefik.<base_domain> for services, upper-cased for the Samba realm. It is
	// not "the domain" of any one thing, which is what the old name suggested.
	BaseDomain string `yaml:"base_domain"`
	Email      string `yaml:"email"`
	// There is deliberately no data_path. Service data lives at
	// <workspace>/data so that copying one directory is a complete backup, and
	// a deployment-wide switch would make that untrue for everything at once.
	// Users who need the data on a larger disk put the whole workspace there.
	//
	// A single cask may still be pointed elsewhere -- samba_fs.userdata_path is
	// the one that is -- but only through that cask's own parameter, which
	// carries a change policy saying what moving it costs. The difference is
	// that the exception is named and policed rather than global and silent.
	Timezone        string `yaml:"timezone"`
	ContainerPrefix string `yaml:"container_prefix"`
	ImagePrefix     string `yaml:"image_prefix"`
	NetworkPrefix   string `yaml:"network_prefix"`
	HostIP          string `yaml:"host_ip"`
	// There is deliberately no dns_provider. A DNS vendor is chosen per engine
	// -- services.lego.env.dns_provider, services.ddns_go.env.dns_provider --
	// because certificates and dynamic DNS routinely live at different
	// vendors, and because the same vendor often needs different credentials
	// for each. See internal/dns.
	DNSServer                  string `yaml:"dns_server"`
	DefaultServiceRootPassword string `yaml:"default_service_root_password"`
	BasicAuthUser              string `yaml:"basicauth_user"`
	DefaultLanguage            string `yaml:"default_language"`
	// Bool rather than bool: the schema defaults these to true, and a default
	// fills whatever the config left empty, so a type that could not express
	// "absent" would let the default overwrite a deliberate false. See
	// internal/config/scalar.go for why it is a checked string and not a
	// pointer.
	ChineseSpeedup Bool `yaml:"chinese_speedup"`
	IPv4           Bool `yaml:"ipv4"`
	IPv6           Bool `yaml:"ipv6"`
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
	{"image_prefix", "IMAGE_PREFIX", func(g Global) string { return g.ImagePrefix }},
	{"network_prefix", "NETWORK_PREFIX", func(g Global) string { return g.NetworkPrefix }},
	{"host_ip", "HOST_IP", func(g Global) string { return g.HostIP }},
	{"dns_server", "DNS_SERVER", func(g Global) string { return g.DNSServer }},
	{"default_service_root_password", "DEFAULT_SERVICE_ROOT_PASSWORD", func(g Global) string {
		return g.DefaultServiceRootPassword
	}},
	{"basicauth_user", "BASICAUTH_USER", func(g Global) string { return g.BasicAuthUser }},
	{"default_language", "DEFAULT_LANGUAGE", func(g Global) string { return g.DefaultLanguage }},
	{"chinese_speedup", "CHINESE_SPEEDUP", func(g Global) string { return g.ChineseSpeedup.String() }},
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
	// keys the runner derives as a cross-cask contract -- ANAS_IAM_*, ANAS_TLS_*
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

type Service struct {
	Enabled   *bool          `yaml:"enabled"`
	DependsOn []string       `yaml:"depends_on"`
	Env       map[string]any `yaml:"env"`
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
	if cfg.Services == nil {
		cfg.Services = map[string]Service{}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = map[string]any{}
	}
	if cfg.Env == nil {
		cfg.Env = map[string]any{}
	}
	if len(cfg.Modules) == 0 {
		return nil, fmt.Errorf("missing modules")
	}
	cfg.IAM.Provider = strings.ToLower(strings.TrimSpace(cfg.IAM.Provider))
	cfg.IAM.DefaultProtocol = strings.ToLower(strings.TrimSpace(cfg.IAM.DefaultProtocol))
	cfg.DynamicDNS.Provider = strings.ToLower(strings.TrimSpace(cfg.DynamicDNS.Provider))
	cfg.DynamicDNS.DNSProvider = strings.TrimSpace(cfg.DynamicDNS.DNSProvider)
	if cfg.DynamicDNS.Provider != "" && cfg.DynamicDNS.DNSProvider == "" {
		return nil, fmt.Errorf("dynamic_dns.provider is set but dynamic_dns.dns_provider is not; name the DNS vendor the records live at")
	}
	// The shared administrator password is optional: when it is absent every
	// cask receives its own generated root password instead. When set it must
	// still meet the minimum length.
	if pw := cfg.Global.DefaultServiceRootPassword; pw != "" && utf8.RuneCountInString(pw) < 8 {
		return nil, fmt.Errorf("global.default_service_root_password must be at least 8 characters")
	}
	return &cfg, nil
}

// Owner markers used in the ownership map returned by BaseEnvWithOwners.
// The empty string marks a globally scoped key; OwnerUserSecret marks a
// user-provided secret that is only distributed to casks that claim it.
const OwnerUserSecret = "!user-secret"

func (f *File) BaseEnv() map[string]string {
	env, _ := f.BaseEnvWithOwners()
	return env
}

// BaseEnvWithOwners flattens the config into environment values and reports
// which config section introduced each key: "" for global sections,
// OwnerUserSecret for user secrets, and the service name for service env.
func (f *File) BaseEnvWithOwners() (map[string]string, map[string]string) {
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
	for name, service := range f.Services {
		prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		for k, v := range service.Env {
			key := EnvKey(k)
			if !strings.HasPrefix(key, prefix+"_") {
				key = prefix + "_" + key
			}
			env[key] = Scalar(v)
			owners[key] = name
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
