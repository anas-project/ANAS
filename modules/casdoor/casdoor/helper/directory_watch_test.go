package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fakeDirectorySyncer struct {
	calls int
	err   error
}

func (syncer *fakeDirectorySyncer) sync(_ []directoryEvent) error {
	syncer.calls++
	return syncer.err
}

func testDirectoryWatchSettings(t *testing.T) directoryWatchSettings {
	t.Helper()
	root := t.TempDir()
	return directoryWatchSettings{
		eventFile:       filepath.Join(root, "events.jsonl"),
		cursorFile:      filepath.Join(root, "cursor.json"),
		healthFile:      filepath.Join(root, "health.json"),
		operations:      csvSet("Add,Modify,Delete", false),
		attributes:      csvSet("member,userAccountControl,sAMAccountName,anasIdentityAnchor", true),
		debounce:        5 * time.Second,
		minimumInterval: time.Minute,
		pollInterval:    time.Second,
	}
}

func appendDirectoryEvents(t *testing.T, path string, events ...directoryEvent) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
}

func membershipEvent(seq int64) directoryEvent {
	return directoryEvent{
		Seq: seq, Operation: "Modify",
		DN:         "CN=APP_nextcloud,OU=Apps,OU=Groups,DC=example,DC=test",
		Attributes: []string{"member"},
	}
}

func TestDirectoryEventInterestUsesSubscriberFilter(t *testing.T) {
	settings := testDirectoryWatchSettings(t)
	if !interestingDirectoryEvent(membershipEvent(1), settings) {
		t.Fatal("membership change must trigger a sync")
	}
	cosmetic := membershipEvent(2)
	cosmetic.Attributes = []string{"description"}
	if interestingDirectoryEvent(cosmetic, settings) {
		t.Fatal("unwatched attribute unexpectedly triggered a sync")
	}
	for _, operation := range []string{"Add", "Delete"} {
		event := cosmetic
		event.Operation = operation
		if !interestingDirectoryEvent(event, settings) {
			t.Fatalf("%s must bypass the attribute filter", operation)
		}
	}
}

func TestDirectoryWatcherDebouncesAndCommitsAfterSync(t *testing.T) {
	settings := testDirectoryWatchSettings(t)
	appendDirectoryEvents(t, settings.eventFile,
		membershipEvent(1), membershipEvent(2), membershipEvent(3))
	syncer := &fakeDirectorySyncer{}
	watcher := newDirectoryWatcher(settings, syncer)
	defer watcher.reader.close()
	start := time.Unix(100, 0)
	if fired, err := watcher.poll(start); err != nil || fired {
		t.Fatalf("initial poll fired=%v err=%v", fired, err)
	}
	if fired, err := watcher.poll(start.Add(6 * time.Second)); err != nil || !fired {
		t.Fatalf("debounced poll fired=%v err=%v", fired, err)
	}
	if syncer.calls != 1 || readDirectoryCursor(settings.cursorFile) != 3 {
		t.Fatalf("sync calls=%d cursor=%d", syncer.calls, readDirectoryCursor(settings.cursorFile))
	}
	if fired, err := watcher.poll(start.Add(10 * time.Second)); err != nil || fired || syncer.calls != 1 {
		t.Fatalf("burst replay fired=%v calls=%d err=%v", fired, syncer.calls, err)
	}
}

func TestDirectoryWatcherRetriesWithoutAdvancingCursor(t *testing.T) {
	settings := testDirectoryWatchSettings(t)
	appendDirectoryEvents(t, settings.eventFile, membershipEvent(1))
	syncer := &fakeDirectorySyncer{err: errors.New("temporary API failure")}
	watcher := newDirectoryWatcher(settings, syncer)
	defer watcher.reader.close()
	start := time.Unix(100, 0)
	_, _ = watcher.poll(start)
	if _, err := watcher.poll(start.Add(6 * time.Second)); err == nil {
		t.Fatal("expected sync failure")
	}
	if got := readDirectoryCursor(settings.cursorFile); got != 0 {
		t.Fatalf("failed sync committed cursor %d", got)
	}
	syncer.err = nil
	if fired, err := watcher.poll(start.Add(7 * time.Second)); err != nil || !fired {
		t.Fatalf("retry fired=%v err=%v", fired, err)
	}
	if got := readDirectoryCursor(settings.cursorFile); got != 1 {
		t.Fatalf("successful retry cursor=%d", got)
	}
}

func TestDirectoryJournalCursorSurvivesRestartAndRotation(t *testing.T) {
	settings := testDirectoryWatchSettings(t)
	appendDirectoryEvents(t, settings.eventFile, membershipEvent(1))
	first := newDirectoryWatcher(settings, &fakeDirectorySyncer{})
	start := time.Unix(100, 0)
	_, _ = first.poll(start)
	_, _ = first.poll(start.Add(6 * time.Second))
	first.reader.close()

	rotated := settings.eventFile + ".1"
	if err := os.Rename(settings.eventFile, rotated); err != nil {
		t.Fatal(err)
	}
	appendDirectoryEvents(t, settings.eventFile, membershipEvent(2))
	secondSyncer := &fakeDirectorySyncer{}
	second := newDirectoryWatcher(settings, secondSyncer)
	defer second.reader.close()
	if fired, err := second.poll(start.Add(time.Minute)); err != nil || fired {
		t.Fatalf("restart initial poll fired=%v err=%v", fired, err)
	}
	if fired, err := second.poll(start.Add(time.Minute + 6*time.Second)); err != nil || !fired {
		t.Fatalf("post-rotation poll fired=%v err=%v", fired, err)
	}
	if secondSyncer.calls != 1 || readDirectoryCursor(settings.cursorFile) != 2 {
		t.Fatalf("sync calls=%d cursor=%d", secondSyncer.calls, readDirectoryCursor(settings.cursorFile))
	}
}

func TestCasdoorLDAPSyncerUsesBasicAuthAndPostsFetchedUsers(t *testing.T) {
	var calls []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "client-id" || password != "client-secret" {
			t.Errorf("basic auth = %q/%q ok=%v", username, password, ok)
		}
		calls = append(calls, request.Method+" "+request.URL.Path)
		body := ""
		switch request.URL.Path {
		case "/api/get-ldap-users":
			if got := request.URL.Query().Get("id"); got != "anas/anas-samba-ad" {
				t.Errorf("LDAP id = %q", got)
			}
			body = `{"status":"ok","data":{"users":[{"uid":"alice","cn":"Alice","displayName":"Alice Example","email":"alice@example.test"}]}}`
		case "/api/sync-ldap-users":
			if got := request.URL.Query().Get("id"); got != "anas/anas-samba-ad" {
				t.Errorf("LDAP id = %q", got)
			}
			var users []map[string]any
			if err := json.NewDecoder(request.Body).Decode(&users); err != nil {
				t.Errorf("decode posted users: %v", err)
			}
			if len(users) != 1 || users[0]["uid"] != "alice" {
				t.Errorf("posted users = %#v", users)
			}
			body = `{"status":"ok","data":{"exist":[],"failed":[]}}`
		case "/api/update-user":
			if got := request.URL.Query().Get("id"); got != "anas/alice" {
				t.Errorf("user id = %q", got)
			}
			if got := request.URL.Query().Get("columns"); got != "displayName,email" {
				t.Errorf("updated columns = %q", got)
			}
			var profile map[string]string
			if err := json.NewDecoder(request.Body).Decode(&profile); err != nil {
				t.Errorf("decode profile: %v", err)
			}
			if profile["displayName"] != "Alice Example" || profile["email"] != "alice@example.test" {
				t.Errorf("profile = %#v", profile)
			}
			body = `{"status":"ok","data":"Affected"}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	settings := testDirectoryWatchSettings(t)
	settings.endpoint = "http://anas_casdoor:8000"
	settings.ldapID = "anas/anas-samba-ad"
	settings.clientID = "client-id"
	settings.clientSecret = "client-secret"
	syncer := &casdoorLDAPSyncer{settings: settings, client: client}
	if err := syncer.sync([]directoryEvent{{DN: "CN=alice,OU=People,DC=example,DC=test", Operation: "Modify", Attributes: []string{"displayName"}}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "GET /api/get-ldap-users,POST /api/sync-ldap-users,POST /api/update-user" {
		t.Fatalf("API calls = %s", got)
	}
}
