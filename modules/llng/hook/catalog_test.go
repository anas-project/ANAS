package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func TestNativeMenuUsesRenderableCategoriesAndExactGroupRule(t *testing.T) {
	c := appcatalog.Catalog{DefaultLanguage: "en", Categories: []appcatalog.Category{{ID: "files", DisplayName: appcatalog.Text{"en": "Files"}}}, Applications: []appcatalog.Application{
		{Metadata: appcatalog.Metadata{Name: "cloud", DisplayName: appcatalog.Text{"en": "Cloud"}, Summary: appcatalog.Text{"en": "Files"}, URL: "https://cloud.example/?a=b#c", Kind: "user", Categories: []string{"files"}}, Enabled: true, Groups: []string{"APP_cloud"}, Visibility: appcatalog.Visibility{Mode: "access"}, Icons: map[string]appcatalog.Icon{"64": {Path: "assets/hash.png"}}},
		{Metadata: appcatalog.Metadata{Name: "cloud-admin", DisplayName: appcatalog.Text{"en": "Admin"}, URL: "https://admin.example/", Kind: "admin", Categories: []string{"files"}}, Enabled: true, Groups: []string{"Admins"}, Visibility: appcatalog.Visibility{Mode: "subjects"}, Icons: map[string]appcatalog.Icon{"64": {Path: "assets/hash.png"}}},
	}}
	document, err := renderApplicationList(c)
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal([]byte(document), &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree) != 2 || tree["10anas-user-0000_files"] == nil || tree["11anas-admin-0000_files"] == nil {
		t.Fatal(tree)
	}
	for key, label := range map[string]string{"10anas-user-0000_files": "User applications / Files", "11anas-admin-0000_files": "Administration / Files"} {
		category := tree[key].(map[string]any)
		if category["catname"] != label {
			t.Fatal(category)
		}
		cards := 0
		for _, value := range category {
			if child, ok := value.(map[string]any); ok {
				if child["type"] != "application" {
					t.Fatal("default portal cannot render nested categories", child)
				}
				cards++
			}
		}
		if cards != 1 {
			t.Fatal("missing top-level card", category)
		}
	}
	if got := nativeGroupRule(c.Applications[0]); got != "inGroup('APP_cloud')" {
		t.Fatal(got)
	}
	if strings.Contains(nativeGroupRule(c.Applications[0]), "Admins") {
		t.Fatal("implicit administrator added")
	}
	if !strings.Contains(document, "https://cloud.example/?a=b#c") || !strings.Contains(document, "anas-catalog/hash.png") {
		t.Fatal(document)
	}
	c.Applications[0].Groups = []string{"Team' OR 'x"}
	if got := nativeGroupRule(c.Applications[0]); !strings.Contains(got, "\\'") {
		t.Fatal("group literal unescaped")
	}
}
