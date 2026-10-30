package main

import (
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/apptheme"
)

func TestPortalThemeUsesCatalogPaletteAndFrozenHiddenBrand(t *testing.T) {
	c := appcatalog.Catalog{ThemeColors: map[string]apptheme.Palette{"light": {"primary": "#2525AA"}}, Applications: []appcatalog.Application{
		{Metadata: appcatalog.Metadata{Name: "user-portal", Hidden: true}, Enabled: true, Icons: map[string]appcatalog.Icon{"128": {Path: "assets/portal.png"}}},
	}}
	e := map[string]string{}
	css, err := renderPortalTheme(c, e)
	if err != nil || !strings.Contains(css, "--anas-primary: #2525AA;") || !strings.Contains(css, "data-anas-theme=dark") || e["LLNG_BRAND_LOGO"] != "common/apps/anas-catalog/portal.png" {
		t.Fatalf("catalog theme not projected to native portal: %v", err)
	}
	c.ThemeColors["light"]["primary"] = "url(https://untrusted.test/)"
	if _, err := renderPortalTheme(c, e); err == nil {
		t.Fatal("palette must not inject CSS or fetch external resources")
	}
}
