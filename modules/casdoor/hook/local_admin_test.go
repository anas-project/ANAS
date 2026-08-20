package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCasdoorRotationKeepsSecretOutOfArgvAndRollsBack(t *testing.T) {
	original := runCasdoorSecretCommand
	defer func() { runCasdoorSecretCommand = original }()
	var passwords []string
	runCasdoorSecretCommand = func(secret, name string, args ...string) ([]byte, error) {
		passwords = append(passwords, secret)
		if strings.Contains(strings.Join(args, " "), secret) {
			t.Fatal("password entered argv")
		}
		if secret == "candidate" {
			return []byte("rejected"), errors.New("exit 1")
		}
		return nil, nil
	}
	req := hookRequest{Module: "casdoor", Phase: "local_account_rotate", Env: map[string]string{"CONTAINER_PREFIX": "anas_"},
		Secrets: map[string]string{"old": "current", "candidate-key": "candidate"}, LocalAccount: &localAccountOperation{
			Handler: "rotate-casdoor-break-glass", AccountID: "break_glass", Username: "admin_casdoor", SecretKey: "old", CandidateSecretKey: "candidate-key"}}
	if err := handleLocalAccount(req); err == nil || !strings.Contains(err.Error(), "old password restored") {
		t.Fatalf("rotation error = %v", err)
	}
	if strings.Join(passwords, ",") != "candidate,current" {
		t.Fatalf("password transaction = %v", passwords)
	}
}
