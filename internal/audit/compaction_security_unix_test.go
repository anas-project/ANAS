//go:build unix

package audit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditOpenRejectsUnsafeStaleCompactionPaths(t *testing.T) {
	marker := []byte("external-audit-compaction-target\n")
	tests := []struct {
		name      string
		wantError string
		prepare   func(*testing.T, string, string) func(*testing.T)
	}{
		{
			name:      "symlink",
			wantError: "non-symlink regular file",
			prepare: func(t *testing.T, root, compactionPath string) func(*testing.T) {
				t.Helper()
				target := filepath.Join(root, "symlink-target")
				if err := os.WriteFile(target, marker, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, compactionPath); err != nil {
					t.Fatal(err)
				}
				return func(t *testing.T) {
					t.Helper()
					body, err := os.ReadFile(target)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(body, marker) {
						t.Fatalf("symlink target body = %q, want %q", body, marker)
					}
					info, err := os.Lstat(compactionPath)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode()&os.ModeSymlink == 0 {
						t.Fatalf("unsafe compaction path mode = %v, want symlink", info.Mode())
					}
				}
			},
		},
		{
			name:      "hard link",
			wantError: "link count",
			prepare: func(t *testing.T, root, compactionPath string) func(*testing.T) {
				t.Helper()
				target := filepath.Join(root, "hard-link-target")
				if err := os.WriteFile(target, marker, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, compactionPath); err != nil {
					t.Fatal(err)
				}
				return func(t *testing.T) {
					t.Helper()
					body, err := os.ReadFile(target)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(body, marker) {
						t.Fatalf("hard-link target body = %q, want %q", body, marker)
					}
					targetInfo, err := os.Lstat(target)
					if err != nil {
						t.Fatal(err)
					}
					compactionInfo, err := os.Lstat(compactionPath)
					if err != nil {
						t.Fatal(err)
					}
					if !os.SameFile(targetInfo, compactionInfo) {
						t.Fatal("unsafe compaction hard link was replaced")
					}
				}
			},
		},
		{
			name:      "wide mode",
			wantError: "0600",
			prepare: func(t *testing.T, _, compactionPath string) func(*testing.T) {
				t.Helper()
				if err := os.WriteFile(compactionPath, marker, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(compactionPath, 0o640); err != nil {
					t.Fatal(err)
				}
				return func(t *testing.T) {
					t.Helper()
					body, err := os.ReadFile(compactionPath)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(body, marker) {
						t.Fatalf("wide compaction body = %q, want %q", body, marker)
					}
					info, err := os.Lstat(compactionPath)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode().Perm() != 0o640 {
						t.Fatalf("wide compaction mode = %04o, want 0640", info.Mode().Perm())
					}
				}
			},
		},
		{
			name:      "non-regular",
			wantError: "non-symlink regular file",
			prepare: func(t *testing.T, _, compactionPath string) func(*testing.T) {
				t.Helper()
				if err := os.Mkdir(compactionPath, 0o700); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(compactionPath, "sentinel")
				if err := os.WriteFile(sentinel, marker, 0o600); err != nil {
					t.Fatal(err)
				}
				return func(t *testing.T) {
					t.Helper()
					info, err := os.Lstat(compactionPath)
					if err != nil {
						t.Fatal(err)
					}
					if !info.IsDir() {
						t.Fatalf("unsafe compaction path mode = %v, want directory", info.Mode())
					}
					body, err := os.ReadFile(sentinel)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(body, marker) {
						t.Fatalf("non-regular sentinel body = %q, want %q", body, marker)
					}
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "audit")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			compactionPath := filepath.Join(directory, CompactionFilename)
			verify := test.prepare(t, root, compactionPath)

			writer, err := OpenWithOptions(directory, Options{})
			if err == nil {
				_ = writer.Close()
				t.Fatal("OpenWithOptions accepted an unsafe stale compaction path")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("OpenWithOptions error = %v, want %q", err, test.wantError)
			}
			verify(t)
		})
	}
}

func TestAuditCompactionPreservesCanonicalSecurityAndLockIdentity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{MaxEvents: 1, CompactionThreshold: disabledAutomaticAuditCompaction}
	writer, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			t.Errorf("close audit writer: %v", err)
		}
	}()

	for _, eventType := range []string{"security-pruned", "security-retained"} {
		if _, err := writer.Append(Event{Type: eventType}); err != nil {
			t.Fatal(err)
		}
	}
	canonicalPath := filepath.Join(directory, Filename)
	lockPath := filepath.Join(directory, lockFilename)
	canonicalBefore, err := os.Lstat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	canonicalAfter, err := os.Lstat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSecureFileInfo(canonicalAfter, Filename); err != nil {
		t.Fatalf("compacted canonical security: %v", err)
	}
	if os.SameFile(canonicalBefore, canonicalAfter) {
		t.Fatal("successful compaction did not replace the canonical audit inode")
	}
	lockAfter, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSecureFileInfo(lockAfter, lockFilename); err != nil {
		t.Fatalf("audit lock security after compaction: %v", err)
	}
	if !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("audit.lock inode changed during compaction")
	}
	if _, err := os.Lstat(filepath.Join(directory, CompactionFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compaction temporary path remains after commit: %v", err)
	}
}

func TestAuditCompactionFailsClosedWhenStablePathChangesBeforeRename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{name: "canonical journal", filename: Filename},
		{name: "fixed lock", filename: lockFilename},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer := openAuditCompactionWriter(t, directory, Options{
				MaxEvents:           8,
				CompactionThreshold: disabledAutomaticAuditCompaction,
			})
			if _, err := writer.Append(Event{Type: "stable-path-marker"}); err != nil {
				t.Fatal(err)
			}

			target := filepath.Join(directory, test.filename)
			displaced := target + ".displaced"
			replacement := []byte("replacement-must-not-be-renamed\n")
			syncDirectory := writer.compaction.syncDirectory
			syncCalls := 0
			writer.compaction.syncDirectory = func(openedDirectory *os.File) error {
				syncCalls++
				if syncCalls == 1 {
					if err := os.Rename(target, displaced); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(target, replacement, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				return syncDirectory(openedDirectory)
			}

			if err := writer.Compact(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Compact after %s replacement = %v, want ErrUnavailable", test.filename, err)
			}
			if syncCalls < 2 {
				t.Fatalf("directory sync calls = %d, want pre-rename sync plus cleanup sync", syncCalls)
			}
			body, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(body, replacement) {
				t.Fatalf("replacement %s body = %q, want %q", test.filename, body, replacement)
			}
			if _, err := os.Lstat(displaced); err != nil {
				t.Fatalf("displaced %s was lost: %v", test.filename, err)
			}
			if _, err := os.Lstat(filepath.Join(directory, CompactionFilename)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("compaction temporary path remains after stable path replacement: %v", err)
			}
		})
	}
}

func TestAuditWriterTransparentlyRebindsAfterPeerCompaction(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{MaxEvents: 8, CompactionThreshold: disabledAutomaticAuditCompaction}
	first, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first audit writer: %v", err)
		}
	}()
	second, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second audit writer: %v", err)
		}
	}()

	staleInfo, err := second.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{"peer-one", "peer-two", "peer-three"} {
		if _, err := first.Append(Event{Type: eventType}); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	canonicalAfterCompaction, err := os.Lstat(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(staleInfo, canonicalAfterCompaction) {
		t.Fatal("peer compaction did not replace the stale writer's inode")
	}

	persisted, err := second.Append(Event{Type: "peer-after-compaction"})
	if err != nil {
		t.Fatalf("stale Writer append after peer compaction: %v", err)
	}
	if persisted.Sequence != 4 {
		t.Fatalf("sequence after peer compaction = %d, want 4", persisted.Sequence)
	}
	secondInfo, err := second.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	canonicalAfterAppend, err := os.Lstat(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(secondInfo, canonicalAfterAppend) {
		t.Fatal("stale Writer did not rebind to the compacted canonical inode")
	}

	events := readEvents(t, filepath.Join(directory, Filename))
	if len(events) != 4 {
		t.Fatalf("events after peer compaction append = %d, want 4", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d, want %d", index, event.Sequence, index+1)
		}
	}
	if events[len(events)-1].Type != "peer-after-compaction" {
		t.Fatalf("last event type = %q, want peer-after-compaction", events[len(events)-1].Type)
	}
}
