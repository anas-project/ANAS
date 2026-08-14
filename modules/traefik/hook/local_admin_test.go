package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraefikLocalAdminRotationWritesVerifiedBcrypt(t *testing.T) {
	originalVerify := verifyTraefikDashboard
	verifyTraefikDashboard = func(url, hostIP, username, password string) error { return nil }
	defer func() { verifyTraefikDashboard = originalVerify }()
	dir := t.TempDir()
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "dynamic"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runtimeDir, "dynamic", "dashboard-auth.yml")
	old, err := traefikAuthConfig("admin_traefik", "old-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	req := hookRequest{Module: "traefik", Phase: "local_account_rotate", Workdir: dir,
		Env:     map[string]string{"ANAS_MODULE_RUNTIME_STATE_PATH": runtimeDir},
		Secrets: map[string]string{"old": "old-password", "candidate-key": "new-password"}, LocalAccount: &localAccountOperation{
			Handler: "rotate-traefik-local-admin", AccountID: "primary", Username: "admin_traefik", SecretKey: "old", CandidateSecretKey: "candidate-key"}}
	if err := handleLocalAccount(req); err != nil {
		t.Fatal(err)
	}
	if err := verifyTraefikAuth(path, "admin_traefik", "new-password"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "new-password") {
		t.Fatal("plaintext password entered Traefik state")
	}
}

func TestDashboardRouterUsesFileProviderAuthentication(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "traefik.http.routers.api.middlewares=auth@file") {
		t.Fatal("dashboard router does not reference the generated file-provider middleware")
	}
}
