//go:build unix

package consoleauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteJSONFileRollsBackRenameWhenDirectorySyncFails(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	if err := ensureStoreDirectory(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "sync-rollback.json")
	if err := writeJSONFile(path, map[string]string{"value": "before"}); err != nil {
		t.Fatal(err)
	}
	failed := false
	err := writeJSONFileWithSync(path, map[string]string{"value": "after"}, func(path string) error {
		if !failed {
			failed = true
			return errors.New("injected directory sync failure")
		}
		return syncDirectory(path)
	})
	if err == nil || !strings.Contains(err.Error(), "injected directory sync failure") {
		t.Fatalf("write error = %v", err)
	}
	var record map[string]string
	found, err := readJSONFile(path, &record)
	if err != nil || !found {
		t.Fatalf("read rolled-back file: found=%t err=%v", found, err)
	}
	if record["value"] != "before" {
		t.Fatalf("rolled-back value = %q", record["value"])
	}

	newPath := filepath.Join(directory, "sync-rollback-new.json")
	failed = false
	err = writeJSONFileWithSync(newPath, map[string]string{"value": "candidate"}, func(path string) error {
		if !failed {
			failed = true
			return errors.New("injected new-file sync failure")
		}
		return syncDirectory(path)
	})
	if err == nil {
		t.Fatal("new-file sync failure unexpectedly succeeded")
	}
	if _, err := os.Lstat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncommitted new file still exists: %v", err)
	}
}

func TestRemovePrivateFileRestoresJournalWhenDirectorySyncFails(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	if err := ensureStoreDirectory(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "cleanup-rollback.json")
	want := map[string]string{"value": "pending"}
	if err := writeJSONFile(path, want); err != nil {
		t.Fatal(err)
	}
	failed := false
	err := removePrivateFileWithSync(path, func(path string) error {
		if !failed {
			failed = true
			return errors.New("injected removal sync failure")
		}
		return syncDirectory(path)
	})
	if err == nil || !strings.Contains(err.Error(), "injected removal sync failure") {
		t.Fatalf("remove error = %v", err)
	}
	var record map[string]string
	found, err := readJSONFile(path, &record)
	if err != nil || !found {
		t.Fatalf("read restored journal: found=%t err=%v", found, err)
	}
	if !reflect.DeepEqual(record, want) {
		t.Fatalf("restored journal = %#v", record)
	}
}

func TestWriteJSONFileRollsBackEveryPostRenameValidationFailure(t *testing.T) {
	failures := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{
			name: "lstat",
			mutate: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
			want: "inspect committed authentication state",
		},
		{
			name: "validation",
			mutate: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "mode 0600",
		},
	}
	for _, failure := range failures {
		for _, existed := range []bool{false, true} {
			name := "new"
			if existed {
				name = "existing"
			}
			t.Run(failure.name+"/"+name, func(t *testing.T) {
				directory := filepath.Join(t.TempDir(), "auth")
				if err := ensureStoreDirectory(directory); err != nil {
					t.Fatal(err)
				}
				statePath := filepath.Join(directory, "post-rename-rollback.json")
				if existed {
					if err := writeJSONFile(statePath, map[string]string{"value": "before"}); err != nil {
						t.Fatal(err)
					}
				}
				err := writeJSONFileWithHooks(statePath, map[string]string{"value": "candidate"}, writeJSONHooks{
					afterRename: func(path string) error {
						failure.mutate(t, path)
						return nil
					},
				})
				if err == nil || !strings.Contains(err.Error(), failure.want) {
					t.Fatalf("write error = %v", err)
				}
				if !existed {
					if _, err := os.Lstat(statePath); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("uncommitted new file still exists: %v", err)
					}
					return
				}
				var record map[string]string
				found, err := readJSONFile(statePath, &record)
				if err != nil || !found {
					t.Fatalf("read restored file: found=%t err=%v", found, err)
				}
				if record["value"] != "before" {
					t.Fatalf("restored value = %q", record["value"])
				}
			})
		}
	}
}

func TestAuthenticationSnapshotCarriesExistenceFromStrictRead(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	snapshot, err := store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.BootstrapExisted || snapshot.LocalExisted {
		t.Fatalf("fresh snapshot existence = bootstrap:%t local:%t", snapshot.BootstrapExisted, snapshot.LocalExisted)
	}
	issueTestBootstrap(t, store, "txn-snapshot-existence")
	snapshot, err = store.loadAuthenticationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.BootstrapExisted || snapshot.LocalExisted {
		t.Fatalf("persisted snapshot existence = bootstrap:%t local:%t", snapshot.BootstrapExisted, snapshot.LocalExisted)
	}
	if err := os.Chmod(filepath.Join(directory, bootstrapFileName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.loadAuthenticationSnapshot(); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("unsafe state was treated as absent: %v", err)
	}
}

func TestStoreRejectsUnsafeDirectory(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, path string)
		want  string
	}{
		{name: "wide mode", setup: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "mode 0700"},
		{name: "symlink", setup: func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}, want: "symbolic link"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth")
			test.setup(t, path)
			_, err := Open(path, &memoryAudit{}, StoreOptions{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Open error = %v", err)
			}
		})
	}
}

func TestStateCorruptionAndUnsafeFilesFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, directory, statePath string)
		want   string
	}{
		{name: "corrupt JSON", mutate: func(t *testing.T, _, statePath string) {
			if err := os.WriteFile(statePath, []byte("{not-json\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "decode authentication state"},
		{name: "unknown field", mutate: func(t *testing.T, _, statePath string) {
			body := `{"api_version":"anas.console-auth/v1","sessions":{},"unknown":true}`
			if err := os.WriteFile(statePath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "unknown field"},
		{name: "missing required sessions", mutate: func(t *testing.T, _, statePath string) {
			body := `{"api_version":"anas.console-auth/v1"}`
			if err := os.WriteFile(statePath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "sessions must be an object"},
		{name: "wide mode", mutate: func(t *testing.T, _, statePath string) {
			if err := os.Chmod(statePath, 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "mode 0600"},
		{name: "symlink", mutate: func(t *testing.T, directory, statePath string) {
			target := filepath.Join(directory, "target.json")
			if err := os.Rename(statePath, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, statePath); err != nil {
				t.Fatal(err)
			}
		}, want: "symbolic link"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "auth")
			audit := &memoryAudit{}
			store := openTestStore(t, directory, audit, newTestClock())
			issued := issueTestBootstrap(t, store, "txn-corrupt")
			statePath := filepath.Join(directory, bootstrapFileName)
			test.mutate(t, directory, statePath)
			_, err := store.ExchangeBootstrapToken(context.Background(), ExchangeBootstrapTokenRequest{Token: issued.Token, Origin: "http://nas.example"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("exchange error = %v", err)
			}
			// No credential is returned and no replacement state is committed.
			if errors.Is(err, ErrAuditUnavailable) {
				t.Fatalf("unexpected audit failure masked unsafe state: %v", err)
			}
		})
	}
}

func TestStoreRejectsSymlinkLockFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "auth")
	store := openTestStore(t, directory, &memoryAudit{}, newTestClock())
	target := filepath.Join(directory, "target.lock")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, lockFileName)); err != nil {
		t.Fatal(err)
	}
	_, err := store.IssueBootstrapToken(context.Background(), IssueBootstrapTokenRequest{
		TransactionID: "txn-lock", State: StateBootstrap, AllowedRoutes: []string{"/status"},
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink lock error = %v", err)
	}
}
