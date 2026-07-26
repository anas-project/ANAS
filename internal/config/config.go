package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type File struct {
	Modules  []string           `yaml:"modules"`
	Global   Global             `yaml:"global"`
	IAM      IAM                `yaml:"iam"`
	Rollback Rollback           `yaml:"rollback"`
	Secrets  map[string]any     `yaml:"secrets"`
	Services map[string]Service `yaml:"services"`
	Env      map[string]any     `yaml:"env"`
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
}

type Global struct {
	Domain                     string `yaml:"domain"`
	Email                      string `yaml:"email"`
	DataPath                   string `yaml:"data_path"`
	Timezone                   string `yaml:"timezone"`
	ContainerPrefix            string `yaml:"container_prefix"`
	ImagePrefix                string `yaml:"image_prefix"`
	NetworkPrefix              string `yaml:"network_prefix"`
	HostIP                     string `yaml:"host_ip"`
	DNSProvider                string `yaml:"dns_provider"`
	DNSServer                  string `yaml:"dns_server"`
	DefaultServiceRootPassword string `yaml:"default_service_root_password"`
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
	set("BASE_DOMAIN", f.Global.Domain)
	set("EMAIL", f.Global.Email)
	set("DATA_PATH", f.Global.DataPath)
	set("TZ", f.Global.Timezone)
	set("CONTAINER_PREFIX", f.Global.ContainerPrefix)
	set("IMAGE_PREFIX", f.Global.ImagePrefix)
	set("NETWORK_PREFIX", f.Global.NetworkPrefix)
	set("HOST_IP", f.Global.HostIP)
	set("DNS_PROVIDER", f.Global.DNSProvider)
	set("DNS_SERVER", f.Global.DNSServer)
	set("DEFAULT_SERVICE_ROOT_PASSWORD", f.Global.DefaultServiceRootPassword)
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

func EnvKey(key string) string {
	key = strings.TrimSpace(key)
	switch strings.ToLower(key) {
	case "timezone":
		return "TZ"
	case "ipv4":
		return "IPv4"
	case "ipv6":
		return "IPv6"
	default:
		return strings.ToUpper(key)
	}
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
