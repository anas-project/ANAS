// TEST_CASES: APPTHEME-T-004
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/apptheme"
)

func TestGlobalThemeUsesFrozenNativeCasdoorLogos(t *testing.T) {
	e := casdoorTestEnv()
	catalog := appcatalog.Catalog{Schema: appcatalog.Schema, DefaultLanguage: "en", Applications: []appcatalog.Application{
		{Metadata: appcatalog.Metadata{Name: "nextcloud", DisplayName: appcatalog.Text{"en": "Nextcloud"}, URL: "https://cloud.example/", Kind: "user"}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: "nextcloud"}, Groups: []string{"APP_nextcloud"}},
		{Metadata: appcatalog.Metadata{Name: "casdoor-admin", DisplayName: appcatalog.Text{"en": "Casdoor"}, URL: "https://auth.example/", Kind: "admin"}, Enabled: true, Access: appcatalog.Access{Mode: "local"}, Groups: []string{"Admins"}}}}
	inputs := []appcatalog.Input{{Application: catalog.Applications[0]}, {Application: catalog.Applications[1]}}
	c, a, files, err := appcatalog.BuildAssets(catalog, inputs)
	if err != nil {
		t.Fatal(err)
	}
	c, a, files, err = appcatalog.ApplyIconTheme(appcatalog.Config{IconTheme: "soft"}, t.TempDir(), c, a, files)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "catalog")
	if _, err = appcatalog.WriteBundle(bundle, c, a, files); err != nil {
		t.Fatal(err)
	}
	e["ANAS_APP_CATALOG_FILE"] = filepath.Join(bundle, "catalog.json")
	text, err := renderInitData(e)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Applications []map[string]any }
	if json.Unmarshal([]byte(text), &doc) != nil {
		t.Fatal("invalid native data")
	}
	theme, _ := apptheme.Builtin("soft")
	for _, app := range c.Applications {
		nativeName := catalogApplicationName(app.Name)
		if app.Access.Mode == "iam" {
			nativeName = appName(app.Access.Client)
		}
		found := false
		for _, native := range doc.Applications {
			if native["name"] != nativeName {
				continue
			}
			found = true
			if native["logo"] != "/files/anas-catalog/"+app.Icons["512"].Path {
				t.Fatal("native logo diverged", app.Name)
			}
			want, _ := apptheme.Render(theme.Icons[app.Name], 512)
			got, er := os.ReadFile(filepath.Join(bundle, app.Icons["512"].Path))
			if er != nil || !bytes.Equal(want, got) {
				t.Fatal("native frozen icon differs from theme", er)
			}
		}
		if !found {
			t.Fatal("native app absent", nativeName)
		}
	}
}

func TestThemeNativeBrandingIncludesHiddenPortalAndRecovery(t *testing.T) {
	c := appcatalog.Catalog{DefaultLanguage: "en", Applications: []appcatalog.Application{
		{Metadata: appcatalog.Metadata{Name: "user-portal", Hidden: true, DisplayName: appcatalog.Text{"en": "ANAS Portal"}, URL: "https://portal.example/", Kind: "user"}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: "user_portal"}, Icons: map[string]appcatalog.Icon{"512": {Path: "portal512.png"}}},
		{Metadata: appcatalog.Metadata{Name: "casdoor-admin", DisplayName: appcatalog.Text{"en": "Casdoor"}, URL: "https://iam.example/", Kind: "admin"}, Enabled: true, Icons: map[string]appcatalog.Icon{"512": {Path: "iam512.png"}}},
	}}
	apps := []any{
		map[string]any{"name": appName("user_portal"), "organization": "anas", "clientId": "stable-id"},
		map[string]any{"name": appName("user_portal") + "-reauth", "organization": "anas", "enableAutoSignin": false},
		map[string]any{"name": "app-anas-directory", "organization": "anas"},
		map[string]any{"name": "app-built-in", "organization": "built-in", "forgetUrl": "/forget"},
	}
	if err := projectCatalogApplications(nil, c, &apps); err != nil {
		t.Fatal(err)
	}
	for _, raw := range apps {
		a := raw.(map[string]any)
		if a["logo"] == nil || a["formCss"] != squareLogoCSS {
			t.Fatal("missing native logo", a["name"])
		}
		if a["name"] == "app-built-in" {
			if a["forgetUrl"] != "/forget" {
				t.Fatal("local recovery overwritten")
			}
			continue
		}
		if a["disableSignin"] == true {
			continue
		}
		if a["forgetUrl"] != "https://portal.example/#/recover" {
			t.Fatal("bad recovery URL", a["name"])
		}
		items := a["signinItems"].([]any)
		if len(items) != 12 {
			t.Fatal("native fields lost")
		}
		found := false
		for _, raw := range items {
			item := raw.(map[string]any)
			if item["name"] == "Forgot password?" {
				found = item["visible"] == true && item["label"] != "" && item["customCss"] != ""
			}
		}
		if !found {
			t.Fatal("recovery entry invisible")
		}
	}
	if len(apps) != 5 || apps[0].(map[string]any)["clientId"] != "stable-id" || apps[1].(map[string]any)["enableAutoSignin"] != false {
		t.Fatal("hidden brand created a card or changed auth")
	}
}
