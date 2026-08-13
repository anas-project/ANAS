package main

import (
	"bytes"
	"fmt"
	"os/exec"
)

var runSecretCommand = func(secret string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewBufferString(secret + "\n")
	return cmd.CombinedOutput()
}

func handleLocalAccount(req hookRequest) error {
	op := req.LocalAccount
	if req.Module != "nextcloud" || op == nil || op.AccountID != "break_glass" || (op.Handler != "apply-nextcloud-break-glass" && op.Handler != "rotate-nextcloud-break-glass") {
		return fmt.Errorf("nextcloud: unsupported local account handler")
	}
	current := req.Secrets[op.SecretKey]
	candidate := req.Secrets[op.CandidateSecretKey]
	if current == "" || candidate == "" {
		return fmt.Errorf("nextcloud: current or candidate local administrator secret is missing")
	}
	if req.Phase == "local_account_apply" || req.Phase == "local_account_rollback" {
		return resetAndVerifyNextcloudPassword(req.Env, op.Username, candidate)
	}
	if err := resetAndVerifyNextcloudPassword(req.Env, op.Username, candidate); err != nil {
		if rollbackErr := resetAndVerifyNextcloudPassword(req.Env, op.Username, current); rollbackErr != nil {
			return fmt.Errorf("nextcloud password verification failed (%v) and rollback failed (%v)", err, rollbackErr)
		}
		return fmt.Errorf("nextcloud password verification failed; old password restored: %w", err)
	}
	return nil
}

func resetAndVerifyNextcloudPassword(env map[string]string, username, password string) error {
	container := env["CONTAINER_PREFIX"] + "nextcloud"
	resetScript := `IFS= read -r OC_PASS; export OC_PASS; exec runuser -u www-data -- php /var/www/html/occ user:resetpassword --password-from-env "$1"`
	if out, err := runSecretCommand(password, "docker", "exec", "-i", container, "sh", "-c", resetScript, "anas-reset", username); err != nil {
		return fmt.Errorf("occ user:resetpassword: %w: %s", err, bytes.TrimSpace(out))
	}
	verifyPHP := `require_once "/var/www/html/lib/base.php"; $p=rtrim(stream_get_contents(STDIN), "\r\n"); if (!\OC::$server->getUserManager()->checkPassword($argv[1], $p)) { fwrite(STDERR, "password rejected\n"); exit(1); }`
	if out, err := runSecretCommand(password, "docker", "exec", "-i", "--user", "www-data", container, "php", "-r", verifyPHP, username); err != nil {
		return fmt.Errorf("Nextcloud password verification: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
