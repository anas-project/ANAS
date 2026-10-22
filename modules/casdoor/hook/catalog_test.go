package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func TestNativeCatalogKeepsClientAndAddsProviderlessAdmin(t *testing.T) {
	e := casdoorTestEnv()
	catalog := appcatalog.Catalog{Schema: appcatalog.Schema, DefaultLanguage: "zh-CN", Applications: []appcatalog.Application{
		{Metadata: appcatalog.Metadata{Name: "nextcloud", DisplayName: appcatalog.Text{"en": "Cloud", "zh-CN": "云盘"}, Summary: appcatalog.Text{"en": "Files", "zh-CN": "文件"}, URL: "https://cloud.example/?a=b#c", Kind: "user", Categories: []string{"files"}}, Enabled: true, Access: appcatalog.Access{Mode: "iam", Client: "nextcloud"}, Groups: []string{"APP_nextcloud"}},
		{Metadata: appcatalog.Metadata{Name: "db-adminer", DisplayName: appcatalog.Text{"en": "Database", "zh-CN": "数据库"}, Summary: appcatalog.Text{"en": "Admin", "zh-CN": "管理"}, URL: "https://db.example/", Kind: "admin", Categories: []string{"database"}}, Enabled: true, Access: appcatalog.Access{Mode: "forward_auth", Role: "platform_admin"}, Groups: []string{"Admins"}},
	}}
	inputs := []appcatalog.Input{{Application: catalog.Applications[0]}, {Application: catalog.Applications[1]}}
	c, _, _, err := appcatalog.BuildAssets(catalog, inputs)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	e["ANAS_APP_CATALOG_FILE"] = path
	text, err := renderInitData(e)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Applications []map[string]any
		Permissions  []map[string]any
		Groups       []map[string]any
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	found := map[string]map[string]any{}
	for _, app := range doc.Applications {
		found[app["name"].(string)] = app
	}
	if found[appName("nextcloud")]["displayName"] != "云盘" || found[appName("nextcloud")]["clientId"] == "" {
		t.Fatal("IAM client identity lost")
	}
	admin := found[catalogApplicationName("db-adminer")]
	if admin["clientId"] != nil || admin["clientSecret"] != nil || admin["disableSignin"] != true || admin["homepageUrl"] != "https://db.example/" {
		t.Fatal(admin)
	}
	if admin["logo"] != "/files/anas-catalog/"+c.Applications[1].Icons["512"].Path {
		t.Fatal("bad native icon path")
	}
	permissionFound := false
	for _, p := range doc.Permissions {
		if p["name"] == "permission-"+catalogApplicationName("db-adminer") {
			permissionFound = true
			groups := p["groups"].([]any)
			if len(groups) != 1 || groups[0] != "anas/Admins" {
				t.Fatal(p)
			}
		}
	}
	if !permissionFound {
		t.Fatal("admin permission not generated")
	}
	if len(managedIAMGroups(e)) < 1 {
		t.Fatal("catalog-only groups absent")
	}
}
