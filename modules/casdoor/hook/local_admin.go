package main

import (
	"bytes"
	"fmt"
	"os/exec"
)

var runCasdoorSecretCommand = func(secret, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewBufferString(secret + "\n")
	return cmd.CombinedOutput()
}

func handleLocalAccount(req hookRequest) error {
	op := req.LocalAccount
	if op == nil || op.AccountID != "break_glass" ||
		(op.Handler != "apply-casdoor-break-glass" && op.Handler != "rotate-casdoor-break-glass") {
		return fmt.Errorf("casdoor: unsupported local account handler")
	}
	if op.Username == "" {
		return fmt.Errorf("casdoor: break-glass username is empty")
	}
	current, candidate := req.Secrets[op.SecretKey], req.Secrets[op.CandidateSecretKey]
	if current == "" || candidate == "" {
		return fmt.Errorf("casdoor: current or candidate local administrator secret is missing")
	}
	if req.Phase == "local_account_apply" || req.Phase == "local_account_rollback" {
		return setAndVerifyCasdoorPassword(req.Env, op.Username, candidate)
	}
	if err := setAndVerifyCasdoorPassword(req.Env, op.Username, candidate); err != nil {
		if rollbackErr := setAndVerifyCasdoorPassword(req.Env, op.Username, current); rollbackErr != nil {
			return fmt.Errorf("Casdoor password verification failed (%v) and rollback failed (%v)", err, rollbackErr)
		}
		return fmt.Errorf("Casdoor password verification failed; old password restored: %w", err)
	}
	return nil
}

func setAndVerifyCasdoorPassword(env map[string]string, username, password string) error {
	container := env["CONTAINER_PREFIX"] + "casdoor"
	out, err := runCasdoorSecretCommand(password, "docker", "exec", "-i", container,
		"/opt/anas/bin/casdoor-helper", "set-password", "built-in", username)
	if err != nil {
		return fmt.Errorf("Casdoor administrator password update: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
