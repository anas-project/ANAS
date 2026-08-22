package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileStateStorePersistsNoTokenWithPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller", "state.json")
	store := FileStateStore{Path: path}
	state := ControllerState{
		Version: controllerStateVersion,
		Workloads: map[string]Workload{"handle": {
			Handle: "handle", Scope: "team/repo", RunnerID: 7, RunnerUUID: "uuid",
			InstanceID: "anas-fj-0123456789abcdef0123", CreatedAt: time.Now(),
		}},
		RetryAfter: map[string]time.Time{},
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(body)), "token") {
		t.Fatalf("controller state contains a token field: %s", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("controller state mode = %o", info.Mode().Perm())
	}
}
