package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type casdoorCredentialDefinition struct {
	secretKey string
	kind      string
	handlers  map[string]string
}

var casdoorCredentialDefinitions = map[string]casdoorCredentialDefinition{
	"casdoor.signing_key": {
		secretKey: "CASDOOR_SIGNING_MATERIAL", kind: "signing-key",
		handlers: map[string]string{
			"credential_probe":     "probe-casdoor-signing-key",
			"credential_reconcile": "reconcile-casdoor-signing-key",
			"credential_verify":    "verify-casdoor-signing-key",
		},
	},
	"casdoor.portal_client_secret": {
		secretKey: "CASDOOR_PORTAL_CLIENT_SECRET", kind: "portal-client-secret",
		handlers: map[string]string{
			"credential_probe":     "probe-casdoor-portal-client-secret",
			"credential_reconcile": "reconcile-casdoor-portal-client-secret",
			"credential_verify":    "verify-casdoor-portal-client-secret",
		},
	},
}

var runCasdoorCredentialCommand = func(container, action, kind, desired string) ([]byte, error) {
	cmd := exec.Command("docker", "exec", "-i", container,
		"/opt/anas/bin/casdoor-helper", "credential", action, kind)
	cmd.Stdin = bytes.NewBufferString(desired)
	return cmd.Output()
}

func handleCredential(req hookRequest) (credentialResult, error) {
	operation := req.Credential
	if operation == nil {
		return credentialResult{}, fmt.Errorf("invalid Casdoor credential operation")
	}
	definition, ok := casdoorCredentialDefinitions[operation.CredentialID]
	if !ok || operation.SecretKey != definition.secretKey ||
		operation.Handler != definition.handlers[req.Phase] {
		return credentialResult{}, fmt.Errorf("invalid Casdoor credential operation")
	}
	desired := req.Secrets[operation.DesiredSecretKey]
	if desired == "" {
		return credentialResult{}, fmt.Errorf("missing Casdoor desired credential")
	}
	container := req.Env["CONTAINER_PREFIX"] + "casdoor"
	if container == "casdoor" {
		return credentialResult{}, fmt.Errorf("missing Casdoor container prefix")
	}
	action := "probe"
	if req.Phase == "credential_reconcile" {
		if operation.Authority != "anas" {
			return credentialResult{}, fmt.Errorf("Casdoor credential authority is external")
		}
		action = "reconcile"
	}
	body, err := runCasdoorCredentialCommand(container, action, definition.kind, desired)
	if err != nil {
		return credentialResult{}, fmt.Errorf("Casdoor credential %s %s failed", operation.CredentialID, action)
	}
	status := strings.TrimSpace(string(body))
	if status != "match" && status != "missing" && status != "mismatch" &&
		status != "unavailable" && status != "reconciled" {
		return credentialResult{}, fmt.Errorf("Casdoor credential %s returned invalid status", operation.CredentialID)
	}
	return credentialResult{
		CredentialID: operation.CredentialID,
		Status:       status,
		Changed:      status == "reconciled",
	}, nil
}
