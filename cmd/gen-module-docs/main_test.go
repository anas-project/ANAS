package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsStaleModuleVersion(t *testing.T) {
	m := manifest{Name: "demo", Version: "2.0", Revision: 1, Title: "Demo"}
	inv := validInventory()
	inv.ModuleVersion = "1.0"
	if err := validate(m, inv); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("validate returned %v, want stale version error", err)
	}
}

func TestValidateRejectsStaleModuleRevision(t *testing.T) {
	m := manifest{Name: "demo", Version: "1.0", Revision: 2, Title: "Demo"}
	inv := validInventory()
	if err := validate(m, inv); err == nil || !strings.Contains(err.Error(), "module_revision") {
		t.Fatalf("validate returned %v, want stale revision error", err)
	}
}

func TestValidateRejectsNonCanonicalLanguage(t *testing.T) {
	m := manifest{Name: "demo", Version: "1.0", Revision: 1, Title: "Demo"}
	inv := validInventory()
	inv.Language.Supported = []string{"zh_cn"}
	if err := validate(m, inv); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("validate returned %v, want canonical spelling error", err)
	}
}

func TestReplaceGeneratedBlockPreservesManualContent(t *testing.T) {
	base := "# Demo\n\nmanual\n\n" + blockStart + "\nold\n" + blockEnd + "\n\nafter\n"
	got, err := replaceGeneratedBlock(base, blockStart+"\nnew\n"+blockEnd+"\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"manual", "new", "after"} {
		if !strings.Contains(got, want) {
			t.Fatalf("result lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old") {
		t.Fatalf("old generated block remains:\n%s", got)
	}
}

func TestRunGeneratesAndChecksBothLanguageReferences(t *testing.T) {
	root := t.TempDir()
	moduleDir := filepath.Join(root, "modules", "demo")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestYAML := "name: demo\nversion: \"1.0\"\nrevision: 1\ntitle: Demo\ndescription: Demo module.\n"
	inventoryYAML := `api_version: anas.module-localization/v1
module: demo
module_version: "1.0"
module_revision: 1
reviewed_at: 2026-08-13
timezone: {status: container, mechanism: TZ}
language:
  status: supported
  scope: Demo UI
  selection: browser
  global_default: not_consumed
  global_locale: not_consumed
  fallback: English
  supported: [en]
  evidence: [{version: "1.0", url: "https://example.com", path: locales}]
`
	for name, body := range map[string]string{"module.yml": manifestYAML, "localization.yml": inventoryYAML} {
		if err := os.WriteFile(filepath.Join(moduleDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "docs", "reference", "module-localization.md"),
		filepath.Join(root, "docs", "en", "reference", "module-localization.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("paired generated reference missing %s: %v", path, err)
		}
	}
	if err := run(root, true); err != nil {
		t.Fatalf("paired generated references failed check: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "docs", "en", "reference", "module-localization.md")); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err == nil {
		t.Fatal("check accepted a missing English generated reference")
	}
}

func validInventory() inventory {
	return inventory{
		APIVersion:     localizationAPI,
		Module:         "demo",
		ModuleVersion:  "1.0",
		ModuleRevision: 1,
		ReviewedAt:     "2026-08-13",
		Timezone:       timezoneInfo{Status: "container", Mechanism: "TZ"},
		Language: languageInfo{
			Status:        "supported",
			Scope:         "Demo UI",
			Selection:     "browser",
			GlobalDefault: "not_consumed",
			GlobalLocale:  "not_consumed",
			Fallback:      "English",
			Supported:     []string{"en"},
			Evidence:      []evidenceInfo{{Version: "1.0", URL: "https://example.com", Path: "locales"}},
		},
	}
}
