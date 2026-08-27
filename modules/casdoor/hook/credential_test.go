package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCasdoorCredentialLifecycleStreamsDesiredValueAndReportsStatus(t *testing.T) {
	original := runCasdoorCredentialCommand
	defer func() { runCasdoorCredentialCommand = original }()
	var gotContainer, gotAction, gotKind, gotDesired string
	runCasdoorCredentialCommand = func(container, action, kind, desired string) ([]byte, error) {
		gotContainer, gotAction, gotKind, gotDesired = container, action, kind, desired
		return []byte("reconciled\n"), nil
	}
	req := hookRequest{
		Module: "casdoor", Phase: "credential_reconcile",
		Env:     map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets: map[string]string{"candidate": "high-entropy-candidate"},
		Credential: &credentialOperation{
			Handler: "reconcile-casdoor-signing-key", CredentialID: "casdoor.signing_key",
			SecretKey: "CASDOOR_SIGNING_MATERIAL", DesiredSecretKey: "candidate", Authority: "anas",
		},
	}
	response, err := handle(req)
	if err != nil {
		t.Fatal(err)
	}
	if gotContainer != "anas_casdoor" || gotAction != "reconcile" || gotKind != "signing-key" ||
		gotDesired != "high-entropy-candidate" {
		t.Fatalf("credential command = %q %q %q desired=%q", gotContainer, gotAction, gotKind, gotDesired)
	}
	if response.Credential == nil || response.Credential.Status != "reconciled" || !response.Credential.Changed {
		t.Fatalf("credential response = %#v", response.Credential)
	}
}

func TestCasdoorCredentialFailureDoesNotExposeDesiredValue(t *testing.T) {
	original := runCasdoorCredentialCommand
	defer func() { runCasdoorCredentialCommand = original }()
	runCasdoorCredentialCommand = func(_, _, _, _ string) ([]byte, error) {
		return nil, errors.New("command failed with sensitive stderr")
	}
	const desired = "never-print-this-candidate"
	_, err := handle(hookRequest{
		Module: "casdoor", Phase: "credential_verify",
		Env:     map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets: map[string]string{"candidate": desired},
		Credential: &credentialOperation{
			Handler: "verify-casdoor-portal-client-secret", CredentialID: "casdoor.portal_client_secret",
			SecretKey: "CASDOOR_PORTAL_CLIENT_SECRET", DesiredSecretKey: "candidate", Authority: "anas",
		},
	})
	if err == nil || strings.Contains(err.Error(), desired) || strings.Contains(err.Error(), "sensitive stderr") {
		t.Fatalf("credential error was absent or exposed command details: %v", err)
	}
}
