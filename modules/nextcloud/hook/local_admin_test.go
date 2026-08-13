package main

import (
	"errors"
	"strings"
	"testing"
)

func nextcloudRotateRequest(current, candidate string) hookRequest {
	return hookRequest{Module: "nextcloud", Phase: "local_account_rotate", Env: map[string]string{"CONTAINER_PREFIX": "anas_"}, Secrets: map[string]string{"current": current, "candidate": candidate}, LocalAccount: &localAccountOperation{Handler: "rotate-nextcloud-break-glass", AccountID: "break_glass", Username: "admin_nc", SecretKey: "current", CandidateSecretKey: "candidate"}}
}

func TestNextcloudRotationUsesOccAndKeepsPasswordOutOfArgv(t *testing.T) {
	original := runSecretCommand
	defer func() { runSecretCommand = original }()
	calls := 0
	runSecretCommand = func(secret, name string, args ...string) ([]byte, error) {
		calls++
		joined := strings.Join(append([]string{name}, args...), " ")
		if strings.Contains(joined, "candidate-password") {
			t.Fatal("candidate password leaked into argv")
		}
		if calls == 1 && !strings.Contains(joined, "user:resetpassword --password-from-env") {
			t.Fatalf("not the real occ reset path: %s", joined)
		}
		return nil, nil
	}
	if err := handleLocalAccount(nextcloudRotateRequest("old-password", "candidate-password")); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want reset + verification", calls)
	}
}

func TestNextcloudRotationVerificationFailureRestoresOldPassword(t *testing.T) {
	original := runSecretCommand
	defer func() { runSecretCommand = original }()
	secrets := []string{}
	runSecretCommand = func(secret, _ string, _ ...string) ([]byte, error) {
		secrets = append(secrets, secret)
		if len(secrets) == 2 {
			return []byte("rejected"), errors.New("exit 1")
		}
		return nil, nil
	}
	err := handleLocalAccount(nextcloudRotateRequest("old-password", "candidate-password"))
	if err == nil || !strings.Contains(err.Error(), "old password restored") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"candidate-password", "candidate-password", "old-password", "old-password"}
	if strings.Join(secrets, ",") != strings.Join(want, ",") {
		t.Fatalf("secret sequence = %v", secrets)
	}
}
