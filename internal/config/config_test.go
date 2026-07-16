package config

import (
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
  domain: nas.example.com
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
  domain: nas.example.com
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
	if env["IPv4"] != "false" {
		t.Fatalf("IPv4 = %q", env["IPv4"])
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
  domain: nas.example.com
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
  domain: nas.example.com
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
