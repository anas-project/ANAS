// TEST_CASES: PORTALUX-T-002
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preferencesOf(t *testing.T, f *fixture) Preferences {
	t.Helper()
	r := f.request("GET", "/api/me", "")
	var data struct {
		Preferences Preferences `json:"preferences"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &data) != nil {
		t.Fatal("account unavailable", r.Code)
	}
	return data.Preferences
}

func TestPreferencesPersistPerAccountAndHoldOnlyDisplayChoices(t *testing.T) {
	f := newFixture(t)
	if p := preferencesOf(t, f); p.Theme != "system" || p.Language != "" || p.ShowCategories == nil || !*p.ShowCategories || len(p.Pinned) != 0 {
		t.Fatal("unexpected defaults", p)
	}
	r := f.request("POST", "/api/me/preferences", `{"language":"ja","theme":"dark","show_categories":false,"pinned":["tool"]}`)
	if r.Code != 200 {
		t.Fatal("valid preferences rejected", r.Code, r.Body.String())
	}
	if p := preferencesOf(t, f); p.Language != "ja" || p.Theme != "dark" || *p.ShowCategories || len(p.Pinned) != 1 {
		t.Fatal("preferences not applied", p)
	}
	dir := filepath.Join(filepath.Dir(f.server.config.AuditDir), "preferences")
	raw, err := os.ReadFile(filepath.Join(dir, "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]map[string]any
	if json.Unmarshal(raw, &stored) != nil || len(stored) != 1 || stored[f.person.Anchor] == nil {
		t.Fatal("preferences must be keyed by the stable anchor only", string(raw))
	}
	for key := range stored[f.person.Anchor] {
		if !map[string]bool{"language": true, "theme": true, "show_categories": true, "pinned": true}[key] {
			t.Fatal("unexpected stored field", key)
		}
	}
	for _, forbidden := range []string{f.person.Username, f.person.DN, "backend-access-token", "csrf"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("identity or secret data stored", forbidden)
		}
	}
	// A new process (another browser after restart) reads the same account choice.
	if again := openPreferences(dir).get(f.person.Anchor); again.Language != "ja" || again.Theme != "dark" {
		t.Fatal("preferences did not persist", again)
	}
}

func TestPreferenceWritesUseWhitelistSessionAndCSRF(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{
		`{"language":"ru","theme":"dark","show_categories":true,"pinned":[]}`,
		`{"language":"","theme":"blue","show_categories":true,"pinned":[]}`,
		`{"language":"","theme":"dark","pinned":[]}`,
		`{"language":"","theme":"dark","show_categories":true,"pinned":["tool","tool"]}`,
		`{"language":"","theme":"dark","show_categories":true,"pinned":["missing"]}`,
		`{"language":"","theme":"dark","show_categories":true,"pinned":[],"email":"x@example.net"}`,
		`{"language":"","theme":"dark","show_categories":true,"pinned":["` + strings.Repeat(`a","`, 24) + `a"]}`,
	} {
		if r := f.request("POST", "/api/me/preferences", body); r.Code != 400 {
			t.Fatal("invalid preferences accepted", body, r.Code)
		}
	}
	f.session.CSRF = "forged"
	if r := f.request("POST", "/api/me/preferences", `{"language":"","theme":"dark","show_categories":true,"pinned":[]}`); r.Code != 403 {
		t.Fatal("forged CSRF accepted", r.Code)
	}
	f.session.ID = "missing-session"
	if r := f.request("POST", "/api/me/preferences", `{"language":"","theme":"dark","show_categories":true,"pinned":[]}`); r.Code != 401 {
		t.Fatal("anonymous preference write accepted", r.Code)
	}
}

func TestPinnedApplicationsFollowCurrentAuthorization(t *testing.T) {
	f := newFixture(t)
	if r := f.request("POST", "/api/me/preferences", `{"language":"","theme":"system","show_categories":true,"pinned":["tool"]}`); r.Code != 200 {
		t.Fatal(r.Code)
	}
	f.server.catalog.Applications[0].Hidden = true
	if p := preferencesOf(t, f); len(p.Pinned) != 0 {
		t.Fatal("pinned list named an application that is no longer authorized")
	}
	if r := f.request("POST", "/api/me/preferences", `{"language":"","theme":"system","show_categories":true,"pinned":["tool"]}`); r.Code != 400 {
		t.Fatal("unauthorized pinned application saved", r.Code)
	}
}

func TestCorruptPreferencesServeDefaultsAndAreNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preferences.json")
	if err := os.WriteFile(path, []byte(`{"broken"`), 0600); err != nil {
		t.Fatal(err)
	}
	s := openPreferences(dir)
	if p := s.get("anchor"); p.Theme != "system" {
		t.Fatal("corrupt store did not serve defaults")
	}
	show := true
	if err := s.set("anchor", Preferences{Theme: "dark", ShowCategories: &show, Pinned: []string{}}); err == nil {
		t.Fatal("write accepted over a corrupt preference file")
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{"broken"` {
		t.Fatal("corrupt preference file was overwritten")
	}
	invalid := filepath.Join(t.TempDir(), "preferences.json")
	os.WriteFile(invalid, []byte(`{"anchor":{"language":"","theme":"neon","show_categories":true,"pinned":[]}}`), 0600)
	if !openPreferences(filepath.Dir(invalid)).broken {
		t.Fatal("invalid stored value accepted")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".preferences-") {
			t.Fatal("temporary file left behind", e.Name())
		}
	}
}
