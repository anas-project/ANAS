package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestRenderRuntimeEnvUsesOnlyManagedDashboardCredential(t *testing.T) {
	files, err := renderRuntimeEnv("traefik", map[string]string{
		"TRAEFIK_LOCAL_ADMIN_USERNAME": "admin_traefik",
		"TRAEFIK_LOCAL_ADMIN_PASSWORD": "s3cret",
		"BASICAUTH_PASSWD":             "must-not-win",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files["dynamic/dashboard-auth.yml"], "s3cret") {
		t.Fatal("plaintext password entered rendered application state")
	}
	if err := verifyTraefikAuthFile([]byte(files["dynamic/dashboard-auth.yml"]), "admin_traefik", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if _, err := renderRuntimeEnv("traefik", map[string]string{}); err == nil {
		t.Fatal("missing managed credential was accepted")
	}
}

func TestRuntimeRestoreReturnsNoArtifactFiles(t *testing.T) {
	resp, err := handle(hookRequest{Module: "traefik", Phase: "runtime_restore", Env: map[string]string{
		"TRAEFIK_LOCAL_ADMIN_USERNAME": "admin_traefik",
		"TRAEFIK_LOCAL_ADMIN_PASSWORD": "s3cret",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 0 {
		t.Fatalf("runtime restore returned artifact files: %v", resp.Files)
	}
	if _, ok := resp.RuntimeFiles["dynamic/dashboard-auth.yml"]; !ok {
		t.Fatal("runtime restore did not reconstruct dashboard authentication")
	}
}

func verifyTraefikAuthFile(content []byte, username, password string) error {
	prefix := username + ":"
	i := strings.Index(string(content), prefix)
	if i < 0 {
		return fmt.Errorf("username missing")
	}
	hash := string(content)[i+len(prefix):]
	if j := strings.IndexAny(hash, "\"\r\n"); j >= 0 {
		hash = hash[:j]
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func TestComposeUsesRenderedTraefikNetworkName(t *testing.T) {
	b, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "--providers.docker.network=${NETWORK_PREFIX}traefik") {
		t.Fatal("Docker provider must select the rendered Traefik network name")
	}
	if !strings.Contains(string(b), "--providers.docker.constraints=Label(`anas.traefik.instance`,`${NETWORK_PREFIX}traefik`)") {
		t.Fatal("Docker provider must only discover containers assigned to this Traefik instance")
	}
	if !strings.Contains(string(b), "${ANAS_MODULE_RUNTIME_STATE_PATH}/dynamic:/run/anas") {
		t.Fatal("Traefik file-provider state is not mounted outside the sealed artifact")
	}

	composeFiles, err := filepath.Glob("../../*/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, composeFile := range composeFiles {
		compose, err := os.ReadFile(composeFile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(compose), "traefik.docker.network=traefik") {
			t.Fatalf("%s contains an unrendered Traefik network label", composeFile)
		}
		lines := strings.Split(string(compose), "\n")
		for i, line := range lines {
			if !strings.Contains(line, "traefik.enable=true") {
				continue
			}
			serviceEnd := len(lines)
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "  ") && !strings.HasPrefix(lines[j], "    ") {
					serviceEnd = j
					break
				}
			}
			if !strings.Contains(strings.Join(lines[i:serviceEnd], "\n"), "anas.traefik.instance=${NETWORK_PREFIX}traefik") {
				t.Fatalf("%s has a Traefik-enabled service without an instance isolation label near line %d", composeFile, i+1)
			}
		}
	}
}
