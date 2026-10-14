package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func nativeGroupRule(app appcatalog.Application) string {
	if app.Visibility.Mode == "authenticated" {
		return "on"
	}
	parts := []string{}
	for _, group := range app.Groups {
		escaped := strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(group)
		parts = append(parts, "inGroup('"+escaped+"')")
	}
	if len(parts) == 0 {
		return "off"
	}
	return strings.Join(parts, " or ")
}

func renderApplicationList(c appcatalog.Catalog) (string, error) {
	result := map[string]any{}
	byName := map[string]appcatalog.Application{}
	for _, app := range c.Applications {
		byName[app.Name] = app
	}
	view := appcatalog.BuildView(c.Categories, c.Applications, c.DefaultLanguage, nil)
	for gi, group := range view.Groups {
		for ci, category := range group.Categories {
			// LLNG's default portal template renders only top-level categories.
			// Keep the primary category visible without hiding cards in a nested tree.
			sub := map[string]any{"type": "category", "catname": group.DisplayName + " / " + category.DisplayName}
			for ai, card := range category.Applications {
				app := byName[card.Name]
				sub[fmt.Sprintf("%04d_%s", ai, card.Name)] = map[string]any{
					"type": "application", "options": map[string]any{
						"name": card.DisplayName, "uri": card.URL, "description": card.Summary,
						"tooltip": card.Description, "display": nativeGroupRule(app),
						"logo": "anas-catalog/" + strings.TrimPrefix(app.Icons["64"].Path, "assets/"),
					},
				}
			}
			result[fmt.Sprintf("%02danas-%s-%04d_%s", gi+10, group.Kind, ci, category.ID)] = sub
		}
	}
	b, err := json.MarshalIndent(result, "", "  ")
	return string(b) + "\n", err
}
