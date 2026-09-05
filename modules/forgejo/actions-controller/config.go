package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
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

	// Lease is the compute-contract sandbox the runner provisioned for this
	// module. LeaseError records why it could not be read, which is not fatal
	// while Actions is switched off.
	Lease      computeclient.Lease
	LeaseError error
	ConfigDir  string
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
		ConfigDir:     defaultString(os.Getenv("INCUS_CONF"), "/run/anas-compute"),
	}
	cfg.Lease, cfg.LeaseError = computeclient.LeaseFromEnv("forgejo", "runners")
	if cfg.Scopes, err = ParseScopes(os.Getenv("FORGEJO_ALLOWED_SCOPES")); err != nil {
		return Config{}, err
	}
	if !enabled {
		return cfg, nil
	}
	for key, value := range map[string]string{
		"FORGEJO_URL": cfg.ForgejoURL, "FORGEJO_RUNNER_URL": cfg.RunnerURL,
		"FORGEJO_USERNAME": cfg.Username, "FORGEJO_PASSWORD": cfg.Password,
		"FORGEJO_RUNNER_IMAGE": cfg.RunnerImage,
	} {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s is required when Actions is enabled", key)
		}
	}
	if cfg.LeaseError != nil {
		return Config{}, fmt.Errorf("compute lease is required when Actions is enabled: %w", cfg.LeaseError)
	}
	if len(cfg.Scopes) == 0 {
		return Config{}, fmt.Errorf("at least one repo/org scope is required when Actions is enabled")
	}
	// The lease's allowlist is the authority on what this module may boot. A
	// runner image outside it would be refused by the client anyway; failing
	// here says so while the operator can still act on it.
	if !cfg.Lease.AllowsImage(cfg.RunnerImage) {
		return Config{}, fmt.Errorf("FORGEJO_RUNNER_IMAGE is not in the compute lease image allowlist")
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
