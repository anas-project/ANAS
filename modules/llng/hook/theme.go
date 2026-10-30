package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/apptheme"
)

// The catalog already freezes the deployment's icon theme. Reuse its palette
// and portal mark without creating another theme store or fetching an asset.
func renderPortalTheme(c appcatalog.Catalog, env map[string]string) (string, error) {
	theme := apptheme.Theme{Schema: apptheme.Schema, ID: "llng", Name: map[string]string{"en": "ANAS"}, Colors: c.ThemeColors, Icons: map[string][]byte{}}
	if err := theme.Validate(); err != nil {
		return "", fmt.Errorf("LLNG portal theme: %w", err)
	}
	env["LLNG_BRAND_LOGO"] = "anas/logo.svg"
	for _, name := range []string{"user-portal", "llng-admin"} {
		for _, app := range c.Applications {
			if app.Name == name && app.Enabled {
				path := app.Icons["128"].Path
				if strings.HasPrefix(path, "assets/") && !strings.Contains(path, "..") && !strings.ContainsAny(path, "?\"'<>\\") {
					env["LLNG_BRAND_LOGO"] = "common/apps/anas-catalog/" + strings.TrimPrefix(path, "assets/")
					break
				}
			}
		}
		if env["LLNG_BRAND_LOGO"] != "anas/logo.svg" {
			break
		}
	}
	var css strings.Builder
	for _, mode := range []string{"light", "dark"} {
		palette := apptheme.Palette{}
		for k, v := range apptheme.Defaults[mode] {
			palette[k] = v
		}
		for k, v := range c.ThemeColors[mode] {
			palette[k] = v
		}
		selector := ":root"
		if mode == "dark" {
			selector = ":root[data-anas-theme=dark]"
		}
		fmt.Fprintf(&css, "%s {\n  color-scheme: %s;\n", selector, mode)
		keys := make([]string, 0, len(palette))
		for k := range palette {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&css, "  --anas-%s: %s;\n", k, palette[k])
		}
		css.WriteString("}\n")
	}
	return css.String(), nil
}
