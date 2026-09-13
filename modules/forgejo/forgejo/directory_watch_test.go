package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type countingSyncer struct {
	calls int
	err   error
}

func (s *countingSyncer) sync() error {
	s.calls++
	return s.err
}

func watchSettings(t *testing.T) directoryWatchSettings {
	t.Helper()
	dir := t.TempDir()
	return directoryWatchSettings{
		enabled:         true,
		eventFile:       filepath.Join(dir, "events.jsonl"),
		cursorFile:      filepath.Join(dir, "state", "cursor.json"),
		healthFile:      filepath.Join(dir, "state", "health.json"),
		operations:      csvSet("Add,Modify,Delete", false),
		attributes:      csvSet("member,userAccountControl,sAMAccountName,mail", true),
		debounce:        5 * time.Second,
		minimumInterval: 60 * time.Second,
	}
}

func appendJournal(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, line := range lines {
		if _, err := file.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// Only changes that can alter who may use Forgejo wake the watcher.
func TestInterestingEventsAreThoseThatChangeAccess(t *testing.T) {
	settings := watchSettings(t)
	for _, tc := range []struct {
		event directoryEvent
		want  bool
	}{
		{directoryEvent{Operation: "Add", DN: "CN=u"}, true},
		{directoryEvent{Operation: "Delete", DN: "CN=u"}, true},
		{directoryEvent{Operation: "Modify", Attributes: []string{"member"}}, true},
		{directoryEvent{Operation: "Modify", Attributes: []string{"userAccountControl"}}, true},
		{directoryEvent{Operation: "Modify", Attributes: []string{"description"}}, false},
		{directoryEvent{Operation: "Rename", Attributes: []string{"member"}}, false},
	} {
		if got := interestingDirectoryEvent(tc.event, settings); got != tc.want {
			t.Errorf("%+v: got %v want %v", tc.event, got, tc.want)
		}
	}
}

// A burst of changes becomes one refresh after the debounce, and a later burst
// waits for the minimum interval: a bulk import must not become a sync storm.
func TestWatcherDebouncesAndSpacesRefreshes(t *testing.T) {
	settings := watchSettings(t)
	appendJournal(t, settings.eventFile,
		`{"seq":1,"op":"Modify","dn":"CN=APP_forgejo","attributes":["member"]}`,
		`{"seq":2,"op":"Modify","dn":"CN=alice","attributes":["userAccountControl"]}`,
	)
	syncer := &countingSyncer{}
	watcher := newDirectoryWatcher(settings, syncer)
	defer watcher.reader.close()

	start := time.Unix(1_000, 0)
	if triggered, err := watcher.poll(start); err != nil || triggered {
		t.Fatalf("triggered before the debounce: %v %v", triggered, err)
	}
	if triggered, err := watcher.poll(start.Add(6 * time.Second)); err != nil || !triggered {
		t.Fatalf("did not trigger after the debounce: %v %v", triggered, err)
	}
	if syncer.calls != 1 {
		t.Fatalf("two changes produced %d syncs, want one", syncer.calls)
	}
	if cursor := readDirectoryCursor(settings.cursorFile); cursor != 2 {
		t.Fatalf("cursor = %d, want 2", cursor)
	}

	appendJournal(t, settings.eventFile, `{"seq":3,"op":"Delete","dn":"CN=bob"}`)
	if triggered, _ := watcher.poll(start.Add(20 * time.Second)); triggered {
		t.Fatal("a second refresh ran inside the minimum interval")
	}
	if triggered, err := watcher.poll(start.Add(70 * time.Second)); err != nil || !triggered {
		t.Fatalf("the pending change was lost: %v %v", triggered, err)
	}
}

// A failed sync must not advance the cursor, or the change is silently dropped.
func TestFailedSyncKeepsTheChangePending(t *testing.T) {
	settings := watchSettings(t)
	appendJournal(t, settings.eventFile, `{"seq":7,"op":"Modify","dn":"CN=alice","attributes":["mail"]}`)
	syncer := &countingSyncer{err: errors.New("forgejo is restarting")}
	watcher := newDirectoryWatcher(settings, syncer)
	defer watcher.reader.close()

	start := time.Unix(1_000, 0)
	_, _ = watcher.poll(start)
	if _, err := watcher.poll(start.Add(6 * time.Second)); err == nil {
		t.Fatal("a failed sync was reported as success")
	}
	if cursor := readDirectoryCursor(settings.cursorFile); cursor != 0 {
		t.Fatalf("cursor advanced past an unsynced change: %d", cursor)
	}
	syncer.err = nil
	if triggered, err := watcher.poll(start.Add(7 * time.Second)); err != nil || !triggered {
		t.Fatalf("the retried sync did not run: %v %v", triggered, err)
	}
}

// Irrelevant records still move the cursor, so a restart does not re-scan the
// whole journal.
func TestIrrelevantRecordsAdvanceTheCursorWithoutSyncing(t *testing.T) {
	settings := watchSettings(t)
	appendJournal(t, settings.eventFile, `{"seq":4,"op":"Modify","dn":"CN=x","attributes":["description"]}`)
	syncer := &countingSyncer{}
	watcher := newDirectoryWatcher(settings, syncer)
	defer watcher.reader.close()
	if _, err := watcher.poll(time.Unix(1_000, 0)); err != nil {
		t.Fatal(err)
	}
	if syncer.calls != 0 || readDirectoryCursor(settings.cursorFile) != 4 {
		t.Fatalf("calls=%d cursor=%d", syncer.calls, readDirectoryCursor(settings.cursorFile))
	}
}

func TestCronSyncerPostsTheExternalUserTask(t *testing.T) {
	var gotPath, gotUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotUser, _, _ = r.BasicAuth()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	syncer := &cronSyncer{settings: directoryWatchSettings{endpoint: server.URL, username: "anas_dirwatch", password: "p"}, client: server.Client()}
	if err := syncer.sync(); err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /api/v1/admin/cron/sync_external_users" || gotUser != "anas_dirwatch" {
		t.Fatalf("path=%q user=%q", gotPath, gotUser)
	}
}

func TestCronSyncerTreatsRefusalAsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	syncer := &cronSyncer{settings: directoryWatchSettings{endpoint: server.URL, username: "u", password: "p"}, client: server.Client()}
	if err := syncer.sync(); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v", err)
	}
}

// With synchronisation off the service must be inert rather than crash-loop.
func TestDisabledWatcherNeedsNoCredentials(t *testing.T) {
	settings, err := directoryWatchSettingsFromEnv(func(string) string { return "" })
	if err != nil || settings.enabled {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	_, err = directoryWatchSettingsFromEnv(func(key string) string {
		if key == "FORGEJO_DIRWATCH_ENABLED" {
			return "true"
		}
		return ""
	})
	if err == nil {
		t.Fatal("an enabled watcher without credentials was accepted")
	}
}
