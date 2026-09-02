package runner

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRestrictedCommandEnvironmentDropsAmbientVariables(t *testing.T) {
	t.Setenv("ANAS_DAEMON_AMBIENT_SECRET", "must-not-escape")
	t.Setenv("ANAS_MODULE_ROOT", "/attacker-controlled")

	a := &app{restrictedProcessEnvironment: true}
	environment := a.commandEnvironment(map[string]string{
		"MODULE_SETTING": "rendered-value",
		"PATH":           "/deployment/bin",
	})

	for _, assignment := range environment {
		if strings.HasPrefix(assignment, "ANAS_DAEMON_AMBIENT_SECRET=") || strings.HasPrefix(assignment, "ANAS_MODULE_ROOT=") {
			t.Fatalf("restricted environment leaked ambient variable: %q", assignment)
		}
	}
	if !slices.Contains(environment, "MODULE_SETTING=rendered-value") {
		t.Fatalf("restricted environment = %#v, want rendered deployment setting", environment)
	}
	if slices.Contains(environment, "PATH=/deployment/bin") {
		t.Fatalf("restricted environment accepted a deployment PATH override: %#v", environment)
	}
}

func TestRestrictedSnapshotCopyHonorsCanceledJobContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := copyDeploymentTreeContext(ctx, true, t.TempDir(), t.TempDir()+"/copy")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("copy error = %v, want context canceled", err)
	}
}

func TestParseDefaultRoute(t *testing.T) {
	gateway, iface := parseDefaultRoute([]byte("default via 192.0.2.1 dev eno1 proto dhcp src 192.0.2.20\n"))
	if gateway != "192.0.2.1" || iface != "eno1" {
		t.Fatalf("route = (%q, %q), want gateway and interface", gateway, iface)
	}
}
