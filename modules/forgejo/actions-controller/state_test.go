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

func TestStateLoadCannotTreatLinkedOrAmbiguousDataAsNoCleanup(t *testing.T) {
	for _, mode := range []string{"dangling link", "link to empty state", "hardlink", "public mode", "directory link", "oversized",
		"duplicate workloads", "workloads alias", "duplicate workload entry", "unknown field", "trailing document"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.json")
			valid := `{"version":1,"workloads":{}}`
			body := valid
			switch mode {
			case "duplicate workloads":
				body = `{"version":1,"workloads":{"work":{"handle":"work"}},"workloads":{}}`
			case "workloads alias":
				body = `{"version":1,"Workloads":{}}`
			case "duplicate workload entry":
				body = `{"version":1,"workloads":{"work":{"runner_id":7},"work":{}}}`
			case "unknown field":
				body = `{"version":1,"workloads":{},"unknown":true}`
			case "trailing document":
				body += `{}`
			case "oversized":
				body += strings.Repeat(" ", 4<<20)
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "dangling link", "link to empty state":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(dir, "other.json")
				if mode == "link to empty state" {
					if err := os.WriteFile(target, []byte(valid), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(dir, "alias")); err != nil {
					t.Fatal(err)
				}
			case "public mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "directory link":
				alias := filepath.Join(t.TempDir(), "linked")
				if err := os.Symlink(dir, alias); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(alias, "state.json")
			}
			if _, err := (FileStateStore{Path: path}).Load(); err == nil {
				t.Fatal("unsafe state established cleanup completion")
			}
		})
	}
}

func TestStateLoadIsReadOnlyAndRequiresExplicitWorkloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "state.json")
	store := FileStateStore{Path: path}
	if s, err := store.Load(); err != nil || s.Version != controllerStateVersion || len(s.Workloads) != 0 {
		t.Fatal("fresh absent state rejected", err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("state observation created a directory")
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"version":1}`, `{"version":null,"workloads":{}}`, `{"version":1,"workloads":null}`, `{"version":1,"workloads":{},"version":1}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Load(); err == nil {
			t.Fatal("incomplete state treated as completed cleanup")
		}
	}
}
