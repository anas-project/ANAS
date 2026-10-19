package groupcatalog

// TEST_CASES: GROUPCAT-T-001

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func declaration() Declaration {
	return Declaration{ID: "features", NamesFrom: "MODULE_GROUPS", SelectPrefix: "cloud_", NamePrefix: "CAP_", ParentDNFrom: "DIRECTORY_GROUPS_DN", When: "DIRECTORY_ENABLED", Managed: true, Category: "capabilities", Risk: "normal", DisplayName: Text{"en": "{name}"}, Description: Text{"en": "Cloud capability {name}", "zh-CN": "云能力 {name}"}}
}
func TestExpansionAndLanguageFallback(t *testing.T) {
	d := declaration()
	env := map[string]string{"MODULE_GROUPS": "cloud_files,foreign_admin,cloud_search", "DIRECTORY_GROUPS_DN": "OU=Cap,OU=Groups,DC=example", "DIRECTORY_ENABLED": "true"}
	groups, err := Expand("cloud", d, env)
	if err != nil || len(groups) != 2 {
		t.Fatal(groups, err)
	}
	c := Catalog{Schema: Schema, Groups: groups}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(groups[0].Description.Resolve("fr"), "Cloud capability CAP_cloud_files") {
		t.Fatal("English fallback missing")
	}
	env["DIRECTORY_ENABLED"] = "false"
	if g, err := Expand("cloud", d, env); err != nil || len(g) != 0 {
		t.Fatal("disabled directory created groups", err)
	}
	env["DIRECTORY_ENABLED"] = ""
	if _, err := Expand("cloud", d, env); err == nil {
		t.Fatal("unresolved condition accepted")
	}
}
func TestRejectInvalidAndDuplicateCatalog(t *testing.T) {
	env := map[string]string{"MODULE_GROUPS": "cloud_files", "DIRECTORY_GROUPS_DN": "OU=Cap,OU=Groups,DC=example", "DIRECTORY_ENABLED": "true"}
	groups, err := Expand("cloud", declaration(), env)
	if err != nil {
		t.Fatal(err)
	}
	c := Catalog{Schema: Schema, Groups: append(groups, groups...)}
	if c.Validate() == nil {
		t.Fatal("duplicate accepted")
	}
	d := declaration()
	d.Description = Text{"zh-CN": "只有中文"}
	if ValidateDeclaration(d) == nil {
		t.Fatal("missing English accepted")
	}
	d = declaration()
	d.Managed = false
	if ValidateDeclaration(d) == nil {
		t.Fatal("system entry with managed locator accepted")
	}
	d = declaration()
	d.Category = "unknown"
	if ValidateDeclaration(d) == nil {
		t.Fatal("unknown category accepted")
	}
	c.Groups = groups
	path := filepath.Join(t.TempDir(), "catalog.json")
	data, _ := json.Marshal(c)
	os.WriteFile(path, data, 0600)
	if _, err := Read(path); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, append(data, []byte(" {}")...), 0600)
	if _, err := Read(path); err == nil {
		t.Fatal("trailing data accepted")
	}
}
