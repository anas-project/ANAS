package moduledocs

import (
	"strings"
	"testing"
)

func TestRenderCatalogSortsWithoutMutatingInputAndEscapesPipes(t *testing.T) {
	entries := []CatalogEntry{
		{
			Name: "zeta", Title: "Zeta", Version: "2.0.0", Revision: 3,
			Status: "release", Category: "media", Description: "Stream | archive",
		},
		{
			Name: "alpha", Title: "Alpha | Home", Version: "1.0.0", Revision: 1,
			Status: "developing|preview", Category: "core|tools", Description: "Manage files",
		},
	}

	got := string(RenderCatalog(entries, false))
	alpha := "| [Alpha \\| Home](/reference/modules/alpha/) | `1.0.0-r1` | `developing\\|preview` | `core\\|tools` | Manage files |"
	zeta := "| [Zeta](/reference/modules/zeta/) | `2.0.0-r3` | `release` | `media` | Stream \\| archive |"
	if !strings.Contains(got, "# Module 目录") {
		t.Fatalf("Chinese catalog heading missing:\n%s", got)
	}
	if strings.Index(got, alpha) < 0 || strings.Index(got, zeta) < 0 {
		t.Fatalf("catalog rows missing or not escaped:\n%s", got)
	}
	if strings.Index(got, alpha) > strings.Index(got, zeta) {
		t.Fatalf("catalog is not sorted by Module name:\n%s", got)
	}
	if entries[0].Name != "zeta" || entries[1].Name != "alpha" {
		t.Fatalf("RenderCatalog mutated its input: %#v", entries)
	}
}

func TestRenderCatalogUsesEnglishHeadingAndLinks(t *testing.T) {
	got := string(RenderCatalog([]CatalogEntry{{
		Name: "demo", Title: "Demo", Version: "1.2.3", Revision: 4,
		Status: "release", Category: "tools", Description: "Example",
	}}, true))
	for _, want := range []string{
		"# Module catalog",
		"The checked-in page and documentation build use the same renderer",
		"[Demo](/en/reference/modules/demo/)",
		"`1.2.3-r4`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("English catalog lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("catalog must end with a newline: %q", got)
	}
}

func TestRenderCatalogKeepsInputOrderForEqualNames(t *testing.T) {
	got := string(RenderCatalog([]CatalogEntry{
		{Name: "demo", Title: "First", Version: "1.0.0", Revision: 1},
		{Name: "demo", Title: "Second", Version: "2.0.0", Revision: 1},
	}, true))
	first, second := strings.Index(got, "[First]"), strings.Index(got, "[Second]")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("stable sort changed the order of equal Module names:\n%s", got)
	}
}
