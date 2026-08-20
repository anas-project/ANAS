package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCalcEturnalExportsDomainForDNSRegistration(t *testing.T) {
	env := map[string]string{
		"BASE_DOMAIN":        "nas.test",
		"CONTAINER_PREFIX":   "anas_",
		"TURN_DOMAIN_PREFIX": "turn",
		"TURN_PORT":          "3478",
	}
	secrets := &secretStore{values: map[string]string{}}

	if err := calcEturnal(env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["TURN_DOMAIN"] != "turn.nas.test" {
		t.Fatalf("TURN_DOMAIN = %q", env["TURN_DOMAIN"])
	}
	if env["ETURNAL_DOMAIN"] != env["TURN_DOMAIN"] {
		t.Fatalf("ETURNAL_DOMAIN = %q", env["ETURNAL_DOMAIN"])
	}
}

type credentialTestExit int

func (e credentialTestExit) Error() string { return fmt.Sprintf("exit %d", e) }
func (e credentialTestExit) ExitCode() int { return int(e) }

func TestProbeEturnalCredentialUsesStdinAndDistinguishesState(t *testing.T) {
	original := credentialDockerCommand
	t.Cleanup(func() { credentialDockerCommand = original })
	const desired = "private-turn-value"
	for _, test := range []struct {
		name   string
		err    error
		status string
	}{
		{name: "match", status: "match"},
		{name: "mismatch", err: credentialTestExit(3), status: "mismatch"},
		{name: "missing", err: credentialTestExit(4), status: "missing"},
		{name: "unavailable", err: credentialTestExit(1), status: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			credentialDockerCommand = func(stdin []byte, args ...string) error {
				if strings.Contains(strings.Join(args, " "), desired) {
					t.Fatal("desired credential leaked into docker argv")
				}
				if string(stdin) != desired+"\n" {
					t.Fatalf("stdin = %q", stdin)
				}
				return test.err
			}
			if got := probeEturnalCredential("anas_eturnal", desired); got != test.status {
				t.Fatalf("status = %q, want %q", got, test.status)
			}
		})
	}
}

func TestHandleCredentialReconcileReloadsAndVerifies(t *testing.T) {
	originalCommand := credentialDockerCommand
	originalPause := credentialRetryPause
	t.Cleanup(func() {
		credentialDockerCommand = originalCommand
		credentialRetryPause = originalPause
	})
	credentialRetryPause = func() {}
	const desired = "private-turn-value"
	probes := 0
	reloads := 0
	credentialDockerCommand = func(stdin []byte, args ...string) error {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "restart") {
			t.Fatal("credential reconcile must not own container lifecycle")
		}
		if strings.Contains(joined, "eturnalctl reload") {
			reloads++
			if string(stdin) != desired+"\n" || strings.Contains(joined, desired) {
				t.Fatal("desired credential was not confined to stdin")
			}
			return nil
		}
		probes++
		if probes == 1 {
			return credentialTestExit(4)
		}
		if probes == 2 {
			return credentialTestExit(1)
		}
		return nil
	}
	result, err := handleCredential(hookRequest{
		Module: "eturnal", Phase: "credential_reconcile",
		Env:     map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets: map[string]string{"ANAS_CREDENTIAL_DESIRED": desired},
		Credential: &credentialOperation{
			Handler: "reconcile-eturnal-secret", CredentialID: eturnalCredentialID,
			SecretKey: "TURN_SECRET", DesiredSecretKey: "ANAS_CREDENTIAL_DESIRED", Authority: "anas",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "reconciled" || !result.Changed || reloads != 1 || probes != 3 {
		t.Fatalf("result = %#v, reloads = %d, probes = %d", result, reloads, probes)
	}
}

func TestEturnalCredentialReloadRestoresConfigOnFailure(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "eturnal.yml")
	original := "eturnal:\n  secret: 'old-value'\n  strict_expiry: false\n"
	if err := os.WriteFile(config, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "eturnalctl"), []byte("#!/bin/sh\nexit \"${ETURNAL_TEST_RELOAD_EXIT:-0}\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(value, exit string) error {
		cmd := exec.Command("sh", "-c", eturnalCredentialReconcileScript)
		cmd.Stdin = strings.NewReader(value + "\n")
		cmd.Env = append(os.Environ(),
			"ANAS_CONFIG_DIR="+dir,
			"PATH="+bin+":"+os.Getenv("PATH"),
			"ETURNAL_TEST_RELOAD_EXIT="+exit,
		)
		return cmd.Run()
	}
	if err := run("new'value", "0"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "  secret: 'new''value'") {
		t.Fatalf("successful reload config = %q", body)
	}
	beforeFailure := string(body)
	if err := run("must-not-stick", "6"); err == nil {
		t.Fatal("reload failure was accepted")
	}
	body, err = os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != beforeFailure {
		t.Fatalf("failed reload did not restore config: %q", body)
	}
}
