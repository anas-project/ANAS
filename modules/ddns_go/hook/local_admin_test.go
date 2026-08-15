package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDDNSRotationFailureRestoresApplicationState(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "ddns_go", ".ddns_go_config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte("user:\n  username: old\n  password: old-hash\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	original := restartDDNSGo
	originalOwnership := takeDDNSStateOwnership
	defer func() {
		restartDDNSGo = original
		takeDDNSStateOwnership = originalOwnership
	}()
	restartDDNSGo = func(string) error { return errors.New("restart failed") }
	takeDDNSStateOwnership = func(string) error { return nil }
	req := hookRequest{Module: "ddns_go", Phase: "local_account_rotate", Env: map[string]string{"DATA_PATH": data, "CONTAINER_PREFIX": "anas_"}, Secrets: map[string]string{"old": "old-password", "new": "new-password"}, LocalAccount: &localAccountOperation{Handler: "rotate-ddns-go-local-admin", AccountID: "primary", Username: "old", SecretKey: "old", CandidateSecretKey: "new"}}
	if err := handleLocalAccount(req); err == nil {
		t.Fatal("rotation unexpectedly succeeded")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("application state was not restored:\n%s", after)
	}
}
