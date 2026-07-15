package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
