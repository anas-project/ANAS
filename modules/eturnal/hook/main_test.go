package main

import (
	"fmt"
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

func TestHandleCredentialReconcileRestartsAndVerifies(t *testing.T) {
	originalCommand := credentialDockerCommand
	originalPause := credentialRetryPause
	t.Cleanup(func() {
		credentialDockerCommand = originalCommand
		credentialRetryPause = originalPause
	})
	credentialRetryPause = func() {}
	probes := 0
	credentialDockerCommand = func(_ []byte, args ...string) error {
		if args[0] == "restart" {
			return nil
		}
		probes++
		if probes == 1 {
			return credentialTestExit(1)
		}
		return nil
	}
	result, err := handleCredential(hookRequest{
		Module: "eturnal", Phase: "credential_reconcile",
		Env:     map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets: map[string]string{"ANAS_CREDENTIAL_DESIRED": "secret"},
		Credential: &credentialOperation{
			Handler: "reconcile-eturnal-secret", CredentialID: eturnalCredentialID,
			SecretKey: "TURN_SECRET", DesiredSecretKey: "ANAS_CREDENTIAL_DESIRED", Authority: "anas",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "reconciled" || !result.Changed || probes != 2 {
		t.Fatalf("result = %#v, probes = %d", result, probes)
	}
}
