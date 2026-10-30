package config

import (
	"strings"
	"testing"
)

func TestExternalCatalogConfigurationIsStrict(t *testing.T) {
	source := `modules:
  casdoor: {}
application_catalog:
  entries:
    - name: external
      display_name: {en: External}
      url: https://external.example/
      summary: {en: Service}
      description: {en: External service}
      documentation_url: {en: https://docs.example/}
      kind: user
      categories: [other]
      access: {mode: external}
      visibility: {mode: authenticated}
`
	cfg, err := Parse([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ApplicationCatalog.Entries) != 1 {
		t.Fatal(cfg)
	}
	// APPCAT-R-040 added French to the accepted catalog languages. Keep the
	// negative case outside the supported set instead of rejecting a valid key.
	if _, err := Parse([]byte(strings.Replace(source, "{en: External}", "{fr: External}", 1))); err != nil {
		t.Fatalf("supported French translation rejected: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(source, "{en: External}", "{it: External}", 1),
		strings.Replace(source, "{en: External}", "{en: ''}", 1),
		strings.Replace(source, "url: https://external.example/", "url: javascript:alert(1)", 1),
		strings.Replace(source, "name: external", "name: external\n      client_secret: exposed", 1),
		"modules: {casdoor: {}}\napplication_catalog:\n  overrides:\n    external:\n      url: https://override.example/\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
}
