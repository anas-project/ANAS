package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

// Preferences are account-level display choices. They hold no identity facts,
// credentials or group data; the anchor is the only key.
type Preferences struct {
	Language       string   `json:"language"`
	Theme          string   `json:"theme"`
	ShowCategories *bool    `json:"show_categories"`
	Pinned         []string `json:"pinned"`
}

const (
	maxPinned          = 24
	maxPreferenceUsers = 20000
)

var (
	errPreferencesUnavailable = errors.New("preferences unavailable")
	errPreferencesFull        = errors.New("preferences capacity reached")
	applicationNamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

func defaultPreferences() Preferences {
	show := true
	return Preferences{Theme: "system", ShowCategories: &show, Pinned: []string{}}
}

func (p Preferences) valid() bool {
	if p.Language != "" && !appcatalog.SupportedKey(p.Language) || p.Theme != "system" && p.Theme != "light" && p.Theme != "dark" || p.ShowCategories == nil || p.Pinned == nil || len(p.Pinned) > maxPinned {
		return false
	}
	seen := map[string]bool{}
	for _, name := range p.Pinned {
		if !applicationNamePattern.MatchString(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

// One process owns the file. A write replaces it atomically; an unreadable
// or corrupt file is never overwritten, so its content stays available for
// repair while the portal keeps serving defaults.
type preferenceStore struct {
	mu     sync.Mutex
	path   string
	data   map[string]Preferences
	broken bool
}

func openPreferences(dir string) *preferenceStore {
	s := &preferenceStore{path: filepath.Join(dir, "preferences.json"), data: map[string]Preferences{}}
	// The root bootstrap creates this directory in the container; local and
	// test runs create it as the current user.
	_ = os.MkdirAll(dir, 0700)
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil || json.Unmarshal(raw, &s.data) != nil || s.data == nil {
		s.data, s.broken = map[string]Preferences{}, true
		return s
	}
	for anchor, p := range s.data {
		if !anchorPattern.MatchString(anchor) || !p.valid() {
			s.data, s.broken = map[string]Preferences{}, true
			return s
		}
	}
	return s
}

func (s *preferenceStore) get(anchor string) Preferences {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.data[anchor]; ok {
		return p
	}
	return defaultPreferences()
}

func (s *preferenceStore) set(anchor string, p Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broken {
		return errPreferencesUnavailable
	}
	if _, ok := s.data[anchor]; !ok && len(s.data) >= maxPreferenceUsers {
		return errPreferencesFull
	}
	next := make(map[string]Preferences, len(s.data)+1)
	for k, v := range s.data {
		next[k] = v
	}
	next[anchor] = p
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".preferences-*")
	if err != nil {
		return errPreferencesUnavailable
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil || tmp.Chmod(0600) != nil || tmp.Sync() != nil || tmp.Close() != nil {
		tmp.Close()
		return errPreferencesUnavailable
	}
	if os.Rename(tmp.Name(), s.path) != nil {
		return errPreferencesUnavailable
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	s.data = next
	return nil
}

// visiblePreferences removes pinned applications the person can no longer
// open, so a response never names a filtered catalog entry.
func (s *Server) visiblePreferences(p Preferences, account IAMAccount, person Person) Preferences {
	pinned := []string{}
	for _, name := range p.Pinned {
		for _, app := range s.catalog.Applications {
			if app.Name == name && applicationAllowed(s.catalog, app, account, person) {
				pinned = append(pinned, name)
				break
			}
		}
	}
	p.Pinned = pinned
	return p
}

func (s *Server) updatePreferences(w http.ResponseWriter, r *http.Request) {
	v, person, account, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		failure(w, 403, "csrf_rejected")
		return
	}
	var p Preferences
	if !decode(r, &p) || !p.valid() {
		failure(w, 400, "invalid_request")
		return
	}
	if visible := s.visiblePreferences(p, account, person); len(visible.Pinned) != len(p.Pinned) {
		failure(w, 400, "invalid_request")
		return
	}
	switch err := s.preferences.set(person.Anchor, p); {
	case errors.Is(err, errPreferencesFull):
		failure(w, 503, "capacity_reached")
		return
	case err != nil:
		failure(w, 503, "preferences_unavailable")
		return
	}
	respond(w, 200, map[string]any{"preferences": p})
}
