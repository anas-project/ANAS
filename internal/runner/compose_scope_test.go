package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeProjectNameFollowsContainerPrefix(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "default", env: map[string]string{}, want: "anas_nextcloud"},
		{name: "production default remains compatible", env: map[string]string{"CONTAINER_PREFIX": "anas_"}, want: "anas_nextcloud"},
		{name: "isolated test scope", env: map[string]string{"CONTAINER_PREFIX": "anas_e2e_r7_"}, want: "anas_e2e_r7_nextcloud"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := composeProjectName("nextcloud", tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("project = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComposeProjectNameRejectsUnsafePrefix(t *testing.T) {
	if _, err := composeProjectName("nextcloud", map[string]string{"CONTAINER_PREFIX": "ANAS E2E/"}); err == nil {
		t.Fatal("unsafe Compose project prefix was accepted")
	}
}

func TestRenderedModuleEnvPreservesCustomComposeProjectScope(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CONTAINER_PREFIX=anas_site_a_\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env, err := parseEnvFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := composeProjectName("nextcloud", env)
	if err != nil {
		t.Fatal(err)
	}
	if got != "anas_site_a_nextcloud" {
		t.Fatalf("project = %q, want %q", got, "anas_site_a_nextcloud")
	}
}

func TestComposeWorkingDirWorkspace(t *testing.T) {
	for _, dir := range []string{
		"/data/anas/.anas/deployments/20260819T000000Z-abcd/modules/nextcloud",
		"/data/anas/.anas/releases/current/nextcloud",
	} {
		got, ok := composeWorkingDirWorkspace(dir)
		if !ok || got != "/data/anas" {
			t.Fatalf("working dir %q resolved to %q, %v", dir, got, ok)
		}
	}
	if _, ok := composeWorkingDirWorkspace("/private/tmp/e2e/nextcloud"); ok {
		t.Fatal("unowned working directory was accepted")
	}
}

func TestComposeProjectOwnerAllowsSameWorkspaceAndRejectsAnother(t *testing.T) {
	original := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = original })
	a := &app{workspace: "/data/anas"}

	inspectComposeProjectOwners = func(string) ([]string, error) {
		return []string{"/data/anas/.anas/deployments/old/modules/nextcloud"}, nil
	}
	if err := a.ensureComposeProjectOwner("anas_nextcloud"); err != nil {
		t.Fatalf("same workspace rejected: %v", err)
	}

	inspectComposeProjectOwners = func(string) ([]string, error) {
		return []string{"/private/tmp/auth-r6-e2e/.anas/deployments/test/modules/nextcloud"}, nil
	}
	err := a.ensureComposeProjectOwner("anas_nextcloud")
	if err == nil || !strings.Contains(err.Error(), "owned by workspace") || !strings.Contains(err.Error(), "unique container_prefix") {
		t.Fatalf("cross-workspace collision error = %v", err)
	}
}

func TestComposeProjectOwnerRejectsMissingOwnershipLabel(t *testing.T) {
	original := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = original })
	inspectComposeProjectOwners = func(string) ([]string, error) {
		return []string{"/tmp/legacy-compose"}, nil
	}
	a := &app{workspace: "/data/anas"}
	if err := a.ensureComposeProjectOwner("anas_nextcloud"); err == nil || !strings.Contains(err.Error(), "do not expose an ANAS workspace owner") {
		t.Fatalf("missing ownership label error = %v", err)
	}
}
