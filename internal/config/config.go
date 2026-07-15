package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Modules  []string           `yaml:"modules"`
	Global   Global             `yaml:"global"`
	Secrets  map[string]any     `yaml:"secrets"`
	Services map[string]Service `yaml:"services"`
	Env      map[string]any     `yaml:"env"`
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
	DefaultRootPassword        string `yaml:"default_root_password"`
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
	return &cfg, nil
}

func (f *File) BaseEnv() map[string]string {
	env := map[string]string{}
	set := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			env[key] = value
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
	set("DEFAULT_ROOT_PASSWORD", f.Global.DefaultRootPassword)
	set("DEFAULT_SERVICE_ROOT_PASSWORD", f.Global.DefaultServiceRootPassword)
	for k, v := range f.Secrets {
		env[EnvKey(k)] = Scalar(v)
	}
	for k, v := range f.Env {
		env[EnvKey(k)] = Scalar(v)
	}
	for name, service := range f.Services {
		prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		for k, v := range service.Env {
			key := EnvKey(k)
			if !strings.HasPrefix(key, prefix+"_") {
				key = prefix + "_" + key
			}
			env[key] = Scalar(v)
		}
	}
	if env["DEFAULT_SERVICE_ROOT_PASSWORD"] == "" {
		env["DEFAULT_SERVICE_ROOT_PASSWORD"] = env["DEFAULT_ROOT_PASSWORD"]
	}
	return env
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
