// TEST_CASES: APPTHEME-T-003
package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/apptheme"
)

func themePNG(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if e := png.Encode(&b, img); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func testTheme(t *testing.T) apptheme.Theme {
	return apptheme.Theme{Schema: apptheme.Schema, ID: "example", Name: map[string]string{"en": "Example"}, Icons: map[string][]byte{"tool": themePNG(t, color.NRGBA{B: 170, A: 255}), "hidden-application": themePNG(t, color.NRGBA{R: 170, A: 255})}}
}
func jsonTheme(t *testing.T, v apptheme.Theme) string {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func TestThemeLibraryAtomicIdempotencyRestartCorruptionAndLimits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "themes")
	l := openThemes(dir)
	theme := testTheme(t)
	if e := l.put(theme); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "example.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = l.put(theme); e != nil {
		t.Fatal("idempotent import rejected", e)
	}
	theme.Name = map[string]string{"en": "Changed"}
	if e = l.put(theme); e != errThemeConflict {
		t.Fatal("conflict overwritten", e)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "example.json"))
	if !bytes.Equal(raw, again) {
		t.Fatal("conflict changed storage")
	}
	info, _ := os.Stat(filepath.Join(dir, "example.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe theme mode")
	}
	if e = os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"broken":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	restarted := openThemes(dir)
	if _, ok := restarted.get("example"); !ok || restarted.invalid != 1 {
		t.Fatal("restart/corrupt isolation failed")
	}
	if x, _ := os.ReadFile(filepath.Join(dir, "broken.json")); string(x) != `{"broken":true}` {
		t.Fatal("corrupt file changed")
	}
	for i := 0; i < 7; i++ {
		v := apptheme.Theme{Schema: apptheme.Schema, ID: "copy-" + string(rune('a'+i)), Name: map[string]string{"en": "Copy"}, Icons: map[string][]byte{}}
		if e = restarted.put(v); e != nil {
			t.Fatal(e)
		}
	}
	if e = restarted.put(apptheme.Theme{Schema: apptheme.Schema, ID: "overflow", Name: map[string]string{"en": "Overflow"}, Icons: map[string][]byte{}}); e == nil {
		t.Fatal("library count limit ignored")
	}
	link := filepath.Join(t.TempDir(), "link")
	if e = os.Symlink(dir, link); e != nil {
		t.Fatal(e)
	}
	if !openThemes(link).broken {
		t.Fatal("symlink library accepted")
	}
}

func TestThemeImportUsesCurrentAdministratorCSRFAndRecentAuthentication(t *testing.T) {
	f, _ := adminFixture(t)
	f.server.themes = openThemes(filepath.Join(t.TempDir(), "themes"))
	body := jsonTheme(t, testTheme(t))
	r := f.request("POST", "/api/themes", body)
	if r.Code != 200 {
		t.Fatal("admin import failed", r.Code, r.Body.String())
	}
	if p := preferencesOf(t, f); p.IconTheme != "" {
		t.Fatal("import changed preference")
	}
	if f.directory.writes != 0 {
		t.Fatal("import changed directory")
	}
	r = f.request("POST", "/api/themes", body)
	if r.Code != 200 {
		t.Fatal("same import failed")
	}
	request := httptest.NewRequest("POST", "https://portal.example/api/themes", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:1000"
	w := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(w, request)
	if w.Code != 401 && w.Code != 403 {
		t.Fatal("anonymous import accepted", w.Code)
	}
	old := f.session.CSRF
	f.session.CSRF = "wrong"
	if r = f.request("POST", "/api/themes", body); r.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	f.session.CSRF = old
	session := f.session
	session.AuthTime = time.Now().Add(-10 * time.Minute)
	f.server.store.add(session)
	if r = f.request("POST", "/api/themes", body); r.Code != 403 || !strings.Contains(r.Body.String(), "recent_authentication_required") {
		t.Fatal("stale auth accepted", r.Body.String())
	}
	f.server.store.add(f.session)
	// A delegated helper and a revoked administrator both fail the live role check.
	f.server.config.AdminGroupGUID = "other-guid"
	f.server.config.Rules = []Rule{{ID: "helpdesk", OperatorGroups: []string{"operator-group"}, Actions: []string{"users.read"}, TargetUserGroups: []string{"scope-group"}}}
	if r = f.request("GET", "/api/users", ""); r.Code != 200 {
		t.Fatal("delegated helper fixture cannot read its scope", r.Code)
	}
	if r = f.request("POST", "/api/themes", body); r.Code != 403 {
		t.Fatal("non-admin import accepted")
	}
	if r = f.request("GET", "/api/themes/example/export", ""); r.Code != 403 {
		t.Fatal("non-admin export accepted")
	}
}

func TestThemeListAndImagesRemainAuthorizedWithOriginalFallback(t *testing.T) {
	f, _ := adminFixture(t)
	dir := t.TempDir()
	f.server.themes = openThemes(filepath.Join(dir, "themes"))
	theme := testTheme(t)
	if e := f.server.themes.put(theme); e != nil {
		t.Fatal(e)
	}
	original := themePNG(t, color.NRGBA{R: 180, A: 255})
	if e := os.WriteFile(filepath.Join(dir, "original.png"), original, 0600); e != nil {
		t.Fatal(e)
	}
	app := f.server.catalog.Applications[0]
	c, a, files, e := appcatalog.BuildAssets(f.server.catalog, []appcatalog.Input{{Application: app, Root: dir, Icons: appcatalog.IconInput{Source: "original.png"}}})
	if e != nil {
		t.Fatal(e)
	}
	global := testTheme(t)
	global.ID = "global"
	global.Name = map[string]string{"en": "Frozen global"}
	global.Icons["tool"] = themePNG(t, color.NRGBA{G: 180, A: 255})
	global.Colors = map[string]apptheme.Palette{"light": {"primary": "#383AC0"}}
	raw, _ := apptheme.Canonical(global)
	if e = os.WriteFile(filepath.Join(dir, "global.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	c, a, files, e = appcatalog.ApplyIconTheme(appcatalog.Config{IconTheme: "custom", ThemeFile: "global.json"}, dir, c, a, files)
	if e != nil {
		t.Fatal(e)
	}
	bundle := filepath.Join(dir, "bundle")
	if _, e = appcatalog.WriteBundle(bundle, c, a, files); e != nil {
		t.Fatal(e)
	}
	f.server.catalog = c
	f.server.config.Catalog = filepath.Join(bundle, "catalog.json")
	r := f.request("GET", "/api/themes", "")
	if r.Code != 200 || strings.Contains(r.Body.String(), "hidden-application") || strings.Contains(r.Body.String(), "icons") {
		t.Fatal("theme list leaks artwork IDs", r.Body.String())
	}
	if r = f.request("POST", "/api/themes", string(raw)); r.Code != 409 {
		t.Fatal("frozen global ID may be shadowed")
	}
	for _, name := range []string{"example", "global", "original"} {
		r = f.request("GET", "/api/assets/tool/64?theme="+name, "")
		if r.Code != 200 {
			t.Fatal("authorized icon failed", name, r.Code, r.Body.String())
		}
	}
	r = f.request("GET", "/api/assets/hidden-application/64?theme=example", "")
	if r.Code != 404 {
		t.Fatal("theme exposed unauthorized icon")
	}
	partial := testTheme(t)
	partial.ID = "partial"
	delete(partial.Icons, "tool")
	if e = f.server.themes.put(partial); e != nil {
		t.Fatal(e)
	}
	want, _ := apptheme.Render(original, 64)
	r = f.request("GET", "/api/assets/tool/64?theme=partial", "")
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), want) {
		t.Fatal("partial theme used global instead of original")
	}
	r = f.request("GET", "/api/assets/tool/64?theme=missing", "")
	if r.Code != 200 || bytes.Equal(r.Body.Bytes(), want) {
		t.Fatal("missing theme did not fall back to global")
	}
	if r = f.request("POST", "/api/me/preferences", `{"language":"en","theme":"dark","icon_theme":"example","show_categories":true,"pinned":[]}`); r.Code != 200 {
		t.Fatal("icon preference rejected", r.Body.String())
	}
	f.server.preferences = openPreferences(filepath.Dir(f.server.preferences.path))
	if p := preferencesOf(t, f); p.IconTheme != "example" || p.Theme != "dark" {
		t.Fatal("choice not persisted per account")
	}
	if p := f.server.preferences.get("other-anchor"); p.IconTheme != "" {
		t.Fatal("choice leaked across accounts")
	}
	stale := preferencesOf(t, f)
	stale.IconTheme = "removed-theme"
	if e = f.server.preferences.set(f.person.Anchor, stale); e != nil {
		t.Fatal(e)
	}
	if r = f.request("POST", "/api/me/preferences", `{"language":"ja","theme":"light","icon_theme":"removed-theme","show_categories":true,"pinned":[]}`); r.Code != 200 {
		t.Fatal("stale icon reference blocked other preferences", r.Body.String())
	}
	if p := preferencesOf(t, f); p.IconTheme != "" || p.Language != "ja" || p.Theme != "light" {
		t.Fatal("stale theme was not repaired")
	}
	if r = f.request("POST", "/api/me/preferences", `{"language":"ja","theme":"light","icon_theme":"invented","show_categories":true,"pinned":[]}`); r.Code != 400 {
		t.Fatal("new unknown theme accepted")
	}
	p := f.directory.people[f.person.Anchor]
	p.Groups = nil
	f.directory.people[p.Anchor] = p
	r = f.request("GET", "/api/assets/tool/64?theme=example", "")
	if r.Code != 404 {
		t.Fatal("icon remained available after revocation", r.Code)
	}
}
