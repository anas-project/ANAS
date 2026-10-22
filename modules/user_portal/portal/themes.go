package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/apptheme"
)

var errThemeConflict = errors.New("theme conflict")
var errThemeStorage = errors.New("theme storage unavailable")

type storedTheme struct {
	theme   apptheme.Theme
	raw     []byte
	builtin bool
}
type themeLibrary struct {
	mu      sync.Mutex
	dir     string
	items   map[string]storedTheme
	broken  bool
	invalid int
}

func openThemes(dir string) *themeLibrary {
	l := &themeLibrary{dir: dir, items: map[string]storedTheme{}}
	for _, id := range []string{"original", "soft"} {
		t, err := apptheme.Builtin(id)
		if err != nil {
			l.broken = true
			continue
		}
		raw, err := apptheme.BuiltinPackage(id)
		if err != nil {
			l.broken = true
			continue
		}
		l.items[id] = storedTheme{theme: t, raw: raw, builtin: true}
	}
	if dir == "" {
		return l
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		l.broken = true
		return l
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		l.broken = true
		return l
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		l.broken = true
		return l
	}
	total, count := 0, 0
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !entry.Type().IsRegular() || info.Size() > apptheme.MaxBytes {
			l.invalid++
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			l.invalid++
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(f, apptheme.MaxBytes+1))
		f.Close()
		t, parseErr := apptheme.Read(raw)
		if err != nil || parseErr != nil || entry.Name() != t.ID+".json" || t.ID == "original" || t.ID == "soft" || count >= 8 || total+len(raw) > apptheme.MaxBytes {
			l.invalid++
			continue
		}
		l.items[t.ID] = storedTheme{theme: t, raw: raw}
		total += len(raw)
		count++
	}
	return l
}

func (l *themeLibrary) get(id string) (storedTheme, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.items[id]
	return t, ok
}
func (l *themeLibrary) put(t apptheme.Theme) error {
	raw, err := apptheme.Canonical(t)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if prior, ok := l.items[t.ID]; ok {
		if bytes.Equal(prior.raw, raw) {
			return nil
		}
		return errThemeConflict
	}
	if l.broken || l.dir == "" {
		return errThemeStorage
	}
	total, count := len(raw), 1
	for _, v := range l.items {
		if !v.builtin {
			total += len(v.raw)
			count++
		}
	}
	if total > apptheme.MaxBytes || count > 8 {
		return fmt.Errorf("theme capacity reached")
	}
	path := filepath.Join(l.dir, t.ID+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errThemeConflict
	}
	f, err := os.CreateTemp(l.dir, ".theme-")
	if err != nil {
		return errThemeStorage
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil || f.Chmod(0600) != nil || f.Sync() != nil || f.Close() != nil {
		f.Close()
		return errThemeStorage
	}
	if os.Rename(f.Name(), path) != nil {
		return errThemeStorage
	}
	if d, err := os.Open(l.dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	normalized, err := apptheme.Read(raw)
	if err != nil {
		return errThemeStorage
	}
	l.items[t.ID] = storedTheme{theme: normalized, raw: raw}
	return nil
}

func (s *Server) themeLibrary() *themeLibrary {
	s.themesOnce.Do(func() {
		if s.themes == nil {
			s.themes = openThemes("")
		}
	})
	return s.themes
}
func (s *Server) hasTheme(id string) bool {
	if id == "" || id == s.catalog.IconTheme {
		return true
	}
	_, ok := s.themeLibrary().get(id)
	return ok
}

type themeChoice struct {
	ID         string                      `json:"id"`
	Name       map[string]string           `json:"name"`
	Colors     map[string]apptheme.Palette `json:"colors,omitempty"`
	Builtin    bool                        `json:"builtin"`
	Exportable bool                        `json:"exportable"`
}

func (s *Server) themesList(w http.ResponseWriter, r *http.Request) {
	_, person, _, ok := s.current(w, r)
	if !ok {
		return
	}
	l := s.themeLibrary()
	l.mu.Lock()
	defer l.mu.Unlock()
	choices := []themeChoice{}
	for _, v := range l.items {
		if v.theme.ID != s.catalog.IconTheme || s.catalog.IconTheme == "original" || s.catalog.IconTheme == "soft" {
			choices = append(choices, themeChoice{ID: v.theme.ID, Name: v.theme.Name, Colors: v.theme.Colors, Builtin: v.builtin, Exportable: true})
		}
	}
	global := s.catalog.IconTheme
	if global == "" {
		global = "original"
	}
	if global != "original" && global != "soft" {
		choices = append(choices, themeChoice{ID: global, Name: s.catalog.ThemeName, Colors: s.catalog.ThemeColors})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].ID < choices[j].ID })
	respond(w, 200, map[string]any{"themes": choices, "default": global, "can_import": s.administrator(person), "invalid_themes": l.invalid, "storage_available": !l.broken && l.dir != ""})
}

func (s *Server) importTheme(w http.ResponseWriter, r *http.Request) {
	v, person, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		failure(w, 403, "csrf_rejected")
		return
	}
	if !s.administrator(person) {
		failure(w, 403, "outside_scope")
		return
	}
	if !s.recentSession(v) {
		failure(w, 403, "recent_authentication_required")
		return
	}
	if mediaType(r) != "application/json" {
		failure(w, 400, "invalid_theme")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, apptheme.MaxBytes+1))
	if err != nil || len(raw) > apptheme.MaxBytes {
		failure(w, 413, "theme_too_large")
		return
	}
	theme, err := apptheme.Read(raw)
	if err != nil {
		failure(w, 400, "invalid_theme")
		return
	}
	// A configured global theme is frozen with the deployment, not owned by
	// this writable library. Never allow an import to shadow its ID or palette.
	if theme.ID == s.catalog.IconTheme && theme.ID != "original" && theme.ID != "soft" {
		failure(w, 409, "theme_conflict")
		return
	}
	switch err = s.themeLibrary().put(theme); {
	case errors.Is(err, errThemeConflict):
		failure(w, 409, "theme_conflict")
	case err != nil:
		failure(w, 503, "theme_storage_unavailable")
	default:
		respond(w, 200, map[string]any{"id": theme.ID, "status": "imported"})
	}
}

func (s *Server) exportTheme(w http.ResponseWriter, r *http.Request) {
	_, person, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.administrator(person) {
		failure(w, 403, "outside_scope")
		return
	}
	id := r.PathValue("id")
	item, ok := s.themeLibrary().get(id)
	if !ok {
		failure(w, 404, "not_found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.anas-theme.json"`)
	w.WriteHeader(200)
	_, _ = w.Write(item.raw)
}

func (s *Server) chosenTheme(person Person, requested string) string {
	if requested != "" {
		return requested
	}
	if s.preferences != nil {
		if selected := s.preferences.get(person.Anchor).IconTheme; selected != "" {
			return selected
		}
	}
	return s.catalog.IconTheme
}

func (s *Server) themedIcon(app appcatalog.Application, selected, size string) ([]byte, string, error) {
	icons := app.Icons
	if selected == "original" && len(app.OriginalIcons) > 0 {
		icons = app.OriginalIcons
	}
	if selected != "" && selected != s.catalog.IconTheme && selected != "original" {
		item, ok := s.themeLibrary().get(selected)
		if ok {
			if raw, found := item.theme.Icons[app.Name]; found {
				pixels, err := strconv.Atoi(size)
				if err != nil {
					return nil, "", err
				}
				data, err := apptheme.Render(raw, pixels)
				return data, apptheme.Digest(data), err
			}
			if len(app.OriginalIcons) > 0 {
				icons = app.OriginalIcons
			}
		}
	}
	icon, ok := icons[size]
	if !ok {
		return nil, "", fmt.Errorf("unknown size")
	}
	data, err := appcatalog.ReadIcon(filepath.Dir(s.config.Catalog), icon)
	return data, icon.SHA256, err
}
