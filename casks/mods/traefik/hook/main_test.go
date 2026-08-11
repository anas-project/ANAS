package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard credential is derived here rather than deployment-wide, so
// the hash format is maintained next to the proxy that parses it.
func TestCalcBasicAuthDerivesDashboardCredential(t *testing.T) {
	e := map[string]string{
		"BASICAUTH_USER":                "admin",
		"DEFAULT_SERVICE_ROOT_PASSWORD": "s3cret",
	}
	if err := calcBasicAuth(e); err != nil {
		t.Fatal(err)
	}
	// {SHA} of "s3cret"; asserted literally so a change of hash format is a
	// deliberate edit rather than a silent one.
	const want = "admin:{SHA}/vNB+F2HQ559kaLUZbmHHvZrXpg="
	if e["TRAEFIK_BASICAUTH_HTPASSWD"] != want {
		t.Fatalf("TRAEFIK_BASICAUTH_HTPASSWD = %q, want %q", e["TRAEFIK_BASICAUTH_HTPASSWD"], want)
	}

	// An explicit password wins over the shared administrator password.
	e = map[string]string{
		"BASICAUTH_USER":                "ops",
		"BASICAUTH_PASSWD":              "s3cret",
		"DEFAULT_SERVICE_ROOT_PASSWORD": "ignored",
	}
	if err := calcBasicAuth(e); err != nil {
		t.Fatal(err)
	}
	if e["TRAEFIK_BASICAUTH_HTPASSWD"] != "ops:{SHA}/vNB+F2HQ559kaLUZbmHHvZrXpg=" {
		t.Fatalf("explicit password was not used: %q", e["TRAEFIK_BASICAUTH_HTPASSWD"])
	}

	// A deployment that pasted a ready-made htpasswd line into the config keeps
	// it verbatim rather than hashing a password over the top of it.
	e = map[string]string{"TRAEFIK_BASICAUTH_HTPASSWD": "admin:{SHA}already-hashed"}
	if err := calcBasicAuth(e); err != nil {
		t.Fatal(err)
	}
	if e["TRAEFIK_BASICAUTH_HTPASSWD"] != "admin:{SHA}already-hashed" {
		t.Fatalf("a supplied htpasswd line was overwritten: %q", e["TRAEFIK_BASICAUTH_HTPASSWD"])
	}

	// Silently publishing an empty credential would leave the dashboard behind
	// a middleware that rejects everyone, with nothing to explain why.
	if err := calcBasicAuth(map[string]string{"BASICAUTH_USER": "admin"}); err == nil {
		t.Fatal("expected an error when no password is available")
	}
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
