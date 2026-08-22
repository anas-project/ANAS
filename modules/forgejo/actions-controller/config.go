package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled       bool
	ForgejoURL    string
	RunnerURL     string
	Username      string
	Password      string
	Scopes        []Scope
	RunnerImage   string
	RunnerLabel   string
	StatePath     string
	PollInterval  time.Duration
	OperationTTL  time.Duration
	WaitingTTL    time.Duration
	JobTimeout    time.Duration
	MaxConcurrent int
	MaxPerScope   int
	CPU           int
	MemoryMiB     int
	DiskGiB       int
	Incus         IncusConfig
}

func LoadConfig() (Config, error) {
	enabled, err := strconv.ParseBool(defaultString(os.Getenv("FORGEJO_ACTIONS_ENABLED"), "false"))
	if err != nil {
		return Config{}, fmt.Errorf("FORGEJO_ACTIONS_ENABLED must be true or false")
	}
	cfg := Config{
		Enabled:       enabled,
		ForgejoURL:    strings.TrimRight(os.Getenv("FORGEJO_URL"), "/"),
		RunnerURL:     normalizedServerURL(os.Getenv("FORGEJO_RUNNER_URL")),
		Username:      os.Getenv("FORGEJO_USERNAME"),
		Password:      os.Getenv("FORGEJO_PASSWORD"),
		RunnerImage:   os.Getenv("FORGEJO_RUNNER_IMAGE"),
		RunnerLabel:   defaultString(os.Getenv("FORGEJO_RUNNER_LABEL"), "docker:docker://data.forgejo.org/oci/node:24-bookworm"),
		StatePath:     defaultString(os.Getenv("FORGEJO_ACTIONS_STATE_PATH"), "/var/lib/anas-actions/state.json"),
		PollInterval:  15 * time.Second,
		OperationTTL:  2 * time.Minute,
		WaitingTTL:    10 * time.Minute,
		JobTimeout:    60 * time.Minute,
		MaxConcurrent: 4,
		MaxPerScope:   2,
		CPU:           2,
		MemoryMiB:     4096,
		DiskGiB:       20,
		Incus: IncusConfig{
			Endpoint:      os.Getenv("INCUS_ENDPOINT"),
			ClientCertB64: os.Getenv("INCUS_CLIENT_CERT_B64"),
			ClientKeyB64:  os.Getenv("INCUS_CLIENT_KEY_B64"),
			ServerCertB64: os.Getenv("INCUS_SERVER_CERT_B64"),
			Profile:       defaultString(os.Getenv("INCUS_RUNNER_PROFILE"), "anas-forgejo-runner"),
			ConfigDir:     defaultString(os.Getenv("INCUS_CONF"), "/run/anas-incus"),
		},
	}
	if cfg.Scopes, err = ParseScopes(os.Getenv("FORGEJO_ALLOWED_SCOPES")); err != nil {
		return Config{}, err
	}
	if !enabled {
		return cfg, nil
	}
	for key, value := range map[string]string{
		"FORGEJO_URL": cfg.ForgejoURL, "FORGEJO_RUNNER_URL": cfg.RunnerURL,
		"FORGEJO_USERNAME": cfg.Username, "FORGEJO_PASSWORD": cfg.Password,
		"FORGEJO_RUNNER_IMAGE": cfg.RunnerImage, "INCUS_ENDPOINT": cfg.Incus.Endpoint,
		"INCUS_CLIENT_CERT_B64": cfg.Incus.ClientCertB64, "INCUS_CLIENT_KEY_B64": cfg.Incus.ClientKeyB64,
		"INCUS_SERVER_CERT_B64": cfg.Incus.ServerCertB64,
	} {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s is required when Actions is enabled", key)
		}
	}
	if len(cfg.Scopes) == 0 {
		return Config{}, fmt.Errorf("at least one repo/org scope is required when Actions is enabled")
	}
	if !imageFingerprintPat.MatchString(cfg.RunnerImage) {
		return Config{}, fmt.Errorf("FORGEJO_RUNNER_IMAGE must be a pinned SHA-256 fingerprint")
	}
	if !strings.HasPrefix(cfg.ForgejoURL, "http://") && !strings.HasPrefix(cfg.ForgejoURL, "https://") {
		return Config{}, fmt.Errorf("FORGEJO_URL must be an HTTP(S) URL")
	}
	if !strings.HasPrefix(cfg.RunnerURL, "https://") {
		return Config{}, fmt.Errorf("FORGEJO_RUNNER_URL must be an HTTPS URL")
	}
	return cfg, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func normalizedServerURL(value string) string {
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	return value + "/"
}
