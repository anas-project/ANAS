package runtimeissues

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, *[]string, *time.Time) {
	t.Helper()
	var logs []string
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	s := Open(filepath.Join(t.TempDir(), "state", "runtime-issues.json"), func(format string, args ...any) {
		logs = append(logs, format)
	})
	s.now = func() time.Time { return now }
	return s, &logs, &now
}

// ISSUE-R-001, R-002, R-005: one record per key, refreshed not duplicated,
// resolved when the condition clears, one log line per open and resolve.
func TestRecordDeduplicatesAndReconcileResolves(t *testing.T) {
	s, logs, now := testStore(t)
	key := "incus.port-binding/forgejo.runners/tcp/30022"
	for range 3 {
		if err := s.Record(SourceAnasd, Finding{Key: key, Message: "port is published by a Docker container"}); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Minute)
	}
	issues, err := Load(s.path)
	if err != nil || len(issues) != 1 || issues[0].Count != 3 || issues[0].Level != LevelError || !issues[0].Open() ||
		!issues[0].LastSeen.After(issues[0].FirstSeen) || len(*logs) != 1 {
		t.Fatalf("issues = %+v, logs = %v, %v", issues, *logs, err)
	}
	// Another source's record under the same prefix is not this check's.
	if err := s.Record(SourceHostd, Finding{Key: "incus.port-binding/other/udp/30053", Level: LevelWarning, Message: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reconcile(SourceAnasd, "incus.port-binding/", nil); err != nil {
		t.Fatal(err)
	}
	issues, _ = Load(s.path)
	if issues[0].Open() || !issues[1].Open() || len(*logs) != 3 {
		t.Fatalf("issues = %+v, logs = %v", issues, *logs)
	}
	// Found again: reopened as a fresh occurrence.
	if err := s.Record(SourceAnasd, Finding{Key: key, Message: "again"}); err != nil {
		t.Fatal(err)
	}
	issues, _ = Load(s.path)
	if !issues[0].Open() || issues[0].Count != 1 || issues[0].Message != "again" {
		t.Fatalf("reopened = %+v", issues[0])
	}
	// A resolved record is kept for the retention, then dropped.
	_ = s.Reconcile(SourceAnasd, "incus.port-binding/", nil)
	*now = now.Add(Retention + time.Hour)
	_ = s.Reconcile(SourceAnasd, "none/", nil)
	issues, _ = Load(s.path)
	if len(issues) != 1 || issues[0].Source != SourceHostd {
		t.Fatalf("after retention = %+v", issues)
	}
}

// ISSUE-R-003: key material never reaches a record.
func TestMessagesCarryNoSensitiveValue(t *testing.T) {
	for _, message := range []string{"-----BEGIN EC PRIVATE KEY-----\nMHcC", "token=abc", "PASSWORD=x"} {
		if got := Sanitize(message); strings.Contains(got, "MHcC") || strings.Contains(got, "abc") || !strings.Contains(got, "withheld") {
			t.Fatalf("Sanitize(%q) = %q", message, got)
		}
	}
	if got := Sanitize("line\none\x1b[31m" + strings.Repeat("é", 400)); strings.ContainsAny(got, "\n\x1b") || len(got) > maxMessage+len("…") {
		t.Fatalf("Sanitize = %q", got)
	}
}

// ISSUE-R-008: a store that cannot be read is an error, not an empty list.
func TestUnreadableStoreIsAnError(t *testing.T) {
	dir := t.TempDir()
	if issues, err := Load(filepath.Join(dir, "missing.json")); err != nil || len(issues) != 0 {
		t.Fatalf("missing = %v, %v", issues, err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad); err == nil {
		t.Fatal("a corrupt store read as no issues")
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, "dir.json")); err == nil {
		t.Fatal("a directory read as no issues")
	}
}
