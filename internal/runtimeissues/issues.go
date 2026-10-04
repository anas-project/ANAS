// Package runtimeissues records what is wrong right now (ISSUE-R-001): a
// conflict or failure found while ANAS runs, not in answer to a command. A
// recurring problem is one record; when its condition clears the record is
// marked resolved and kept for a while.
//
// Two stores exist: the workspace store, written by Core's apply and by
// anasd, and the host store, written by hostd. Each is one JSON file updated
// under a file lock by an atomic rename, so any writer works without the
// others (ISSUE-R-004). Modules never write here (ISSUE-R-009): only ANAS
// code links this package.
package runtimeissues

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const (
	Schema = "anas.runtime-issues/v1"
	// HostPath is hostd's store, in its own state directory.
	HostPath = "/var/lib/anas-hostd/runtime-issues.json"

	LevelError   = "error"
	LevelWarning = "warning"

	SourceApply = "apply"
	SourceAnasd = "anasd"
	SourceHostd = "hostd"
	SourceBoot  = "boot"

	// Retention keeps a resolved record visible for review. Provisional, the
	// same 30 days as hostd's invocation records, until the operator settles
	// it with the log retention (plans/runtime-issues.md §2).
	Retention = 30 * 24 * time.Hour

	maxMessage = 512
	maxIssues  = 1024
	maxFile    = 4 << 20
)

// WorkspacePath is the workspace store.
func WorkspacePath(workspace string) string {
	return filepath.Join(workspace, ".anas", "state", "runtime-issues.json")
}

type Issue struct {
	Key        string     `json:"key"`
	Source     string     `json:"source"`
	Level      string     `json:"level"`
	Message    string     `json:"message"`
	FirstSeen  time.Time  `json:"first_seen"`
	LastSeen   time.Time  `json:"last_seen"`
	Count      int        `json:"count"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

func (i Issue) Open() bool { return i.ResolvedAt == nil }

// Finding is one problem a writer currently sees.
type Finding struct {
	Key     string
	Level   string
	Message string
}

type document struct {
	Schema string  `json:"schema"`
	Issues []Issue `json:"issues"`
}

type Store struct {
	path string
	logf func(string, ...any)
	now  func() time.Time
}

// Open names a store; nothing is read or created until a write. logf
// receives one line, carrying the key, for every opened and resolved record
// (ISSUE-R-005).
func Open(path string, logf func(string, ...any)) *Store {
	return &Store{path: path, logf: logf, now: func() time.Time { return time.Now().UTC() }}
}

// Record adds or refreshes one record. A record already open only moves its
// last-seen time and count (ISSUE-R-002); a resolved one is reopened.
func (s *Store) Record(source string, finding Finding) error {
	return s.update(func(issues []Issue, now time.Time) []Issue {
		return s.upsert(issues, source, finding, now)
	})
}

// Reconcile is one complete check by one source over one key prefix: what it
// found is recorded, and every open record of that source under the prefix it
// no longer finds is resolved.
func (s *Store) Reconcile(source, prefix string, found []Finding) error {
	return s.update(func(issues []Issue, now time.Time) []Issue {
		seen := map[string]bool{}
		for _, finding := range found {
			issues = s.upsert(issues, source, finding, now)
			seen[finding.Key] = true
		}
		for i := range issues {
			issue := &issues[i]
			if issue.Open() && issue.Source == source && strings.HasPrefix(issue.Key, prefix) && !seen[issue.Key] {
				resolved := now
				issue.ResolvedAt = &resolved
				s.log("runtime issue resolved: %s", issue.Key)
			}
		}
		return issues
	})
}

func (s *Store) upsert(issues []Issue, source string, finding Finding, now time.Time) []Issue {
	finding.Message = Sanitize(finding.Message)
	if finding.Level != LevelWarning {
		finding.Level = LevelError
	}
	for i := range issues {
		issue := &issues[i]
		if issue.Key != finding.Key {
			continue
		}
		reopened := !issue.Open()
		if reopened {
			issue.FirstSeen, issue.Count, issue.ResolvedAt = now, 0, nil
		}
		issue.Source, issue.Level, issue.Message, issue.LastSeen = source, finding.Level, finding.Message, now
		issue.Count++
		if reopened {
			s.log("runtime issue opened: %s (%s): %s", issue.Key, issue.Level, issue.Message)
		}
		return issues
	}
	s.log("runtime issue opened: %s (%s): %s", finding.Key, finding.Level, finding.Message)
	return append(issues, Issue{Key: finding.Key, Source: source, Level: finding.Level, Message: finding.Message, FirstSeen: now, LastSeen: now, Count: 1})
}

func (s *Store) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

func (s *Store) update(change func([]Issue, time.Time) []Issue) (result error) {
	if s == nil || s.path == "" {
		return errors.New("runtime issue store is not configured")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	issues, err := Load(s.path)
	if err != nil {
		return err
	}
	now := s.now()
	issues = change(issues, now)
	// Resolved records past the retention go; the store stays bounded.
	issues = slices.DeleteFunc(issues, func(issue Issue) bool {
		return !issue.Open() && now.Sub(*issue.ResolvedAt) > Retention
	})
	if len(issues) > maxIssues {
		return fmt.Errorf("runtime issue store holds more than %d records", maxIssues)
	}
	body, err := json.MarshalIndent(document{Schema: Schema, Issues: issues}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Load reads a store. A store that was never written holds no issues; one
// that cannot be read is an error, never an empty list (ISSUE-R-008).
func Load(path string) ([]Issue, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Issue{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFile {
		return nil, fmt.Errorf("runtime issue store %s is not a bounded regular file", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil || doc.Schema != Schema {
		return nil, fmt.Errorf("runtime issue store %s has an invalid schema", path)
	}
	if doc.Issues == nil {
		doc.Issues = []Issue{}
	}
	return doc.Issues, nil
}

// Sanitize keeps a message to one bounded line of text and drops one that
// carries key material (ISSUE-R-003). Writers build messages from names,
// ports and addresses; this is the guard behind that rule.
func Sanitize(message string) string {
	upper := strings.ToUpper(message)
	for _, marker := range []string{"-----BEGIN", "PRIVATE KEY", "PASSWORD=", "SECRET=", "TOKEN="} {
		if strings.Contains(upper, marker) {
			return "[message withheld: it carried a sensitive value]"
		}
	}
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	message = strings.TrimSpace(message)
	if len(message) > maxMessage {
		cut := maxMessage
		for cut > 0 && !utf8Start(message[cut]) {
			cut--
		}
		message = message[:cut] + "…"
	}
	return message
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
