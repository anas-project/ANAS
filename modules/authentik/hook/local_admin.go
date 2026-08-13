package main

import (
	"bytes"
	"fmt"
	"os/exec"
)

var runAuthentikSecretCommand = func(secret string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewBufferString(secret + "\n")
	return cmd.CombinedOutput()
}

func handleLocalAccount(req hookRequest) error {
	op := req.LocalAccount
	if req.Module != "authentik" || op == nil || op.AccountID != "break_glass" ||
		(op.Handler != "apply-authentik-break-glass" && op.Handler != "rotate-authentik-break-glass") {
		return fmt.Errorf("authentik: unsupported local account handler")
	}
	if op.Username != "akadmin" {
		return fmt.Errorf("authentik: break-glass username must be akadmin")
	}
	current, candidate := req.Secrets[op.SecretKey], req.Secrets[op.CandidateSecretKey]
	if current == "" || candidate == "" {
		return fmt.Errorf("authentik: current or candidate local administrator secret is missing")
	}
	if req.Phase == "local_account_apply" || req.Phase == "local_account_rollback" {
		return setAndVerifyAuthentikPassword(req.Env, op.Username, candidate)
	}
	if err := setAndVerifyAuthentikPassword(req.Env, op.Username, candidate); err != nil {
		if rollbackErr := setAndVerifyAuthentikPassword(req.Env, op.Username, current); rollbackErr != nil {
			return fmt.Errorf("authentik password verification failed (%v) and rollback failed (%v)", err, rollbackErr)
		}
		return fmt.Errorf("authentik password verification failed; old password restored: %w", err)
	}
	return nil
}

func setAndVerifyAuthentikPassword(env map[string]string, username, password string) error {
	container := env["CONTAINER_PREFIX"] + "authentik"
	script := `import sys; from authentik.core.models import User; p=sys.stdin.readline().rstrip("\r\n"); u=User.objects.get(username="akadmin"); u.set_password(p); u.save(update_fields=["password"]); assert u.check_password(p), "password verification failed"`
	out, err := runAuthentikSecretCommand(password, "docker", "exec", "-i", container, "ak", "shell", "-c", script)
	if err != nil {
		return fmt.Errorf("akadmin password update: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
