package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectoryRevocationScope(t *testing.T) {
	s := testDirectoryWatchSettings(t)
	s.applications = []directoryApplication{{Application: "app-a", Groups: []string{"A"}}, {Application: "app-b", Groups: []string{"B"}}}
	u := casdoorManagedUser{ID: "internal", Name: "alice", ExternalID: "anchor", Properties: map[string]string{s.identityAnchor: "anchor"}}
	d := casdoorDirectoryUser{UID: "alice", Attributes: map[string]string{s.identityAnchor: "anchor"}}
	for _, tc := range []struct {
		name      string
		directory []casdoorDirectoryUser
		groups    []string
		all       bool
		apps      int
	}{
		{"unchanged", []casdoorDirectoryUser{d}, []string{"anas/A", "anas/B"}, false, 0},
		{"group-only", []casdoorDirectoryUser{d}, []string{"anas/B"}, false, 1},
		{"disabled-or-deleted", nil, nil, true, 2},
		{"rename", []casdoorDirectoryUser{{UID: "alice-new", Attributes: d.Attributes}}, []string{"anas/A", "anas/B"}, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, err := planDirectoryRevocations(tc.directory, []casdoorManagedUser{u}, map[string][]string{"anchor": tc.groups}, s)
			if err != nil {
				t.Fatal(err)
			}
			if tc.apps == 0 {
				if len(requests) != 0 {
					t.Fatal(requests)
				}
				return
			}
			if len(requests) != 1 || requests[0].All != tc.all || len(requests[0].Applications) != tc.apps {
				t.Fatalf("scope=%#v", requests)
			}
			if !tc.all && requests[0].Applications[0] != "app-a" {
				t.Fatal(requests)
			}
		})
	}
}

func TestDirectoryIdentityReuseIsQuarantined(t *testing.T) {
	s := testDirectoryWatchSettings(t)
	old := casdoorManagedUser{Name: "alice", ExternalID: "old-anchor", Properties: map[string]string{s.identityAnchor: "old-anchor"}}
	directory := []casdoorDirectoryUser{{UID: "alice", Attributes: map[string]string{s.identityAnchor: "new-anchor"}}, {UID: "bob", Attributes: map[string]string{s.identityAnchor: "bob-anchor"}}}
	allowed, problem := filterDirectoryIdentityConflicts(directory, []casdoorManagedUser{old}, s.identityAnchor)
	if len(allowed) != 1 || allowed[0].UID != "bob" || problem == "" {
		t.Fatalf("allowed=%#v problem=%s", allowed, problem)
	}
	patches, err := planPreSyncUserPatches(nil, allowed, []casdoorManagedUser{old}, s)
	if err != nil || len(patches) != 1 || patches[0].body["isForbidden"] != true {
		t.Fatalf("patches=%#v err=%v", patches, err)
	}
	if _, grants := patches[0].body["externalId"]; grants {
		t.Fatal("label reuse overwrote identity")
	}
}

func TestPendingLogoutSurvivesRestartAndRetriesCapturedSID(t *testing.T) {
	s := testDirectoryWatchSettings(t)
	s.pendingFile = filepath.Join(t.TempDir(), "pending.json")
	fail := true
	revocations, deliveries := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action   string                   `json:"action"`
			Snapshot directorySessionSnapshot `json:"snapshot"`
			Target   directoryLogoutTarget    `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		data := any(true)
		status := "ok"
		switch body.Action {
		case "prepare":
			body.Snapshot.Subject = "old-anchor"
			body.Snapshot.Tokens = []string{"old-token"}
			body.Snapshot.Targets = []directoryLogoutTarget{{Application: "app-a", SID: "old-issued-sid"}}
			data = body.Snapshot
		case "revoke":
			revocations++
		case "deliver":
			deliveries++
			if body.Target.SID != "old-issued-sid" || body.Snapshot.Subject != "old-anchor" {
				t.Fatal("retry changed identity/session")
			}
			if fail {
				status = "error"
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "msg": "receiver unavailable", "data": data})
	}))
	defer server.Close()
	s.endpoint = server.URL
	syncer := &casdoorLDAPSyncer{settings: s, client: server.Client()}
	if err := syncer.prepareLogouts([]directorySessionSnapshot{{Owner: "anas", UserID: "internal", Name: "old-name", Applications: []string{"app-a"}}}); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(s.pendingFile)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v err=%v", stat, err)
	}
	if err := syncer.revokePreparedLogouts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := syncer.deliverPendingLogouts(now); err != nil {
		t.Fatal(err)
	}
	if len(syncer.pendingLogouts) != 1 || syncer.pendingLogouts[0].LastError == "" {
		t.Fatal("failure was discarded")
	}
	restarted := &casdoorLDAPSyncer{settings: s, client: server.Client()}
	if err := restarted.loadPendingLogouts(); err != nil {
		t.Fatal(err)
	}
	if !restarted.pendingLogouts[0].Revoked {
		t.Fatal("lost provider acknowledgment")
	}
	if err := restarted.revokePreparedLogouts(); err != nil {
		t.Fatal(err)
	}
	if revocations != 1 {
		t.Fatal("delivery retry repeated provider deletion")
	}
	fail = false
	if err := restarted.deliverPendingLogouts(now.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if deliveries != 2 || len(restarted.pendingLogouts) != 0 {
		t.Fatal("retry not acknowledged")
	}
	count, _, problem := restarted.logoutHealth(now)
	if count != 0 || problem != "" {
		t.Fatal("health did not recover")
	}
}

func TestPendingLogoutCorruptionIsReported(t *testing.T) {
	s := testDirectoryWatchSettings(t)
	s.pendingFile = filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(s.pendingFile, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	syncer := &casdoorLDAPSyncer{settings: s}
	if err := syncer.loadPendingLogouts(); err == nil {
		t.Fatal("corruption was ignored")
	}
}

func TestWatcherReconcilesWithoutRetainedEvents(t *testing.T) {
	s := testDirectoryWatchSettings(t)
	s.reconcileInterval = 5 * time.Minute
	syncer := &fakeDirectorySyncer{}
	watcher := newDirectoryWatcher(s, syncer)
	defer watcher.reader.close()
	now := time.Now()
	if _, err := watcher.poll(now); err != nil {
		t.Fatal(err)
	}
	if syncer.calls != 1 {
		t.Fatal("startup reconciliation missing")
	}
	if _, err := watcher.poll(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if syncer.calls != 1 {
		t.Fatal("reconciliation repeated before interval")
	}
	if _, err := watcher.poll(now.Add(5 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if syncer.calls != 2 {
		t.Fatal("periodic reconciliation missing")
	}
}

// A grant arriving before the shadow update must be captured durably afterward.
func TestDirectoryRevocationCapturesAfterAdmissionUpdate(t *testing.T) {
	settings := testDirectoryWatchSettings(t)
	settings.pendingFile = filepath.Join(t.TempDir(), "pending.json")
	settings.applications = []directoryApplication{{Application: "app-a", Groups: []string{"A"}}}
	updated := false
	prepares, revoked := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := any(true)
		switch r.URL.Path {
		case "/api/get-ldap-users":
			data = map[string]any{"users": []casdoorDirectoryUser{{UID: "alice", Attributes: map[string]string{settings.identityAnchor: "anchor"}}}}
		case "/api/get-users":
			data = []casdoorManagedUser{{ID: "internal", Name: "alice", ExternalID: "anchor", LDAP: "alice", Properties: map[string]string{settings.identityAnchor: "anchor"}, Groups: []string{"anas/A"}}}
		case "/api/sync-ldap-users":
			data = map[string]any{"failed": []any{}}
		case "/api/update-user":
			updated = true
		case "/api/anas-directory-revocation":
			var body struct {
				Action   string                   `json:"action"`
				Snapshot directorySessionSnapshot `json:"snapshot"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Action == "prepare" {
				prepares++
				if updated {
					body.Snapshot.Tokens = []string{"late-issued-token"}
					body.Snapshot.Subject = "anchor"
					body.Snapshot.Targets = []directoryLogoutTarget{{Application: "app-a", SID: "late-sid", Subject: "anchor"}}
				}
				data = body.Snapshot
			} else if body.Action == "revoke" {
				persisted, err := os.ReadFile(settings.pendingFile)
				var records []pendingDirectoryLogout
				if err != nil || json.Unmarshal(persisted, &records) != nil || len(records) != 1 || len(records[0].Snapshot.Tokens) != 1 {
					t.Fatal("provider deletion preceded durable late-grant capture")
				}
				revoked++
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": data})
	}))
	defer server.Close()
	settings.endpoint = server.URL
	syncer := &casdoorLDAPSyncer{settings: settings, client: server.Client(), memberships: fakeMembershipResolver{"anchor": {}}}
	if err := syncer.sync(nil); err != nil {
		t.Fatal(err)
	}
	if prepares != 2 || revoked != 1 {
		t.Fatalf("prepares=%d revoked=%d", prepares, revoked)
	}
}

func TestRenameCaptureSendsEachIssuedLogoutOnce(t *testing.T) {
	s := testDirectoryWatchSettings(t)
	s.pendingFile = filepath.Join(t.TempDir(), "pending.json")
	prepares, deliveries := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action   string                   `json:"action"`
			Snapshot directorySessionSnapshot `json:"snapshot"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		data := any(true)
		switch body.Action {
		case "prepare":
			prepares++
			body.Snapshot.Name = "old"
			if prepares == 2 {
				body.Snapshot.Name = "renamed"
			}
			body.Snapshot.Tokens = []string{"same-grant"}
			body.Snapshot.Targets = []directoryLogoutTarget{{Application: "app-a", SID: "same-sid", Subject: "anchor"}}
			data = body.Snapshot
		case "deliver":
			deliveries++
			if deliveries > 1 {
				t.Error("already closed receiver session received duplicate intent")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": data})
	}))
	defer server.Close()
	s.endpoint = server.URL
	syncer := &casdoorLDAPSyncer{settings: s, client: server.Client()}
	request := []directorySessionSnapshot{{Owner: "anas", UserID: "internal", Applications: []string{"app-a"}}}
	for i := 0; i < 2; i++ {
		if err := syncer.prepareLogouts(request); err != nil {
			t.Fatal(err)
		}
	}
	if err := syncer.revokePreparedLogouts(); err != nil {
		t.Fatal(err)
	}
	if err := syncer.deliverPendingLogouts(time.Now()); err != nil {
		t.Fatal(err)
	}
	if deliveries != 1 || len(syncer.pendingLogouts) != 0 {
		t.Fatalf("deliveries=%d pending=%d", deliveries, len(syncer.pendingLogouts))
	}
}
