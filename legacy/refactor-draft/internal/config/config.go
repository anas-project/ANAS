package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	Modules    []string           `yaml:"modules"`
	LegacyMods []string           `yaml:"mods"`
	Global     Global             `yaml:"global"`
	Secrets    map[string]any     `yaml:"secrets"`
	Services   map[string]Service `yaml:"services"`
	Env        map[string]any     `yaml:"env"`
	LegacyEnv  map[string]any     `yaml:"envs"`
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
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if len(cfg.Modules) == 0 {
		cfg.Modules = cfg.LegacyMods
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
	for k, v := range cfg.LegacyEnv {
		cfg.Env[k] = v
	}
	if len(cfg.Modules) == 0 {
		return nil, fmt.Errorf("missing modules")
	}
	return &cfg, nil
}

func (f *File) BaseEnv() map[string]string {
	env := map[string]string{
		"DATA_PATH":          "~/data",
		"TZ":                 "Asia/Hong_Kong",
		"CONTAINER_PREFIX":   "anas_",
		"IMAGE_PREFIX":       "anas_",
		"NETWORK_PREFIX":     "anas_",
		"PUID":               "1000",
		"PGID":               "1000",
		"BASICAUTH_USER":     "admin",
		"DEFAULT_LANGUAGE":   "zh-cn",
		"CHINESE_SPEEDUP":    "false",
		"USE_DEFAULT_DOMAIN": "yes",
		"IPv4":               "true",
		"IPv6":               "true",
		"DNS_SERVER":         "223.5.5.5",
		"SHARE_DIR_NAME":     "Share",
		"USERDATA_NAME":      "userdata",
		"SHARE_GUEST_OK":     "Yes",
	}
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
		env[strings.ToUpper(k)] = Scalar(v)
	}
	for k, v := range f.Env {
		env[k] = Scalar(v)
	}
	for name, service := range f.Services {
		prefix := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		for k, v := range service.Env {
			key := strings.ToUpper(k)
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
