package main

import (
	"fmt"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func catalogApplicationName(name string) string { return "app-anas-catalog-" + name }

// Only native Application fields are projected. Full translations and assets
// remain in the immutable catalog for the second-stage portal.
func projectCatalogApplications(e map[string]string, c appcatalog.Catalog, applications *[]any) error {
	used := map[string]bool{}
	for _, entry := range c.Applications {
		if !entry.Enabled || entry.Hidden {
			continue
		}
		title := entry.DisplayName.Resolve(c.DefaultLanguage)
		summary := entry.Summary.Resolve(c.DefaultLanguage)
		if utf8.RuneCountInString(title) > 100 || utf8.RuneCountInString(summary) > 100 || utf8.RuneCountInString(entry.URL) > 100 {
			return fmt.Errorf("application %s exceeds Casdoor native 100-character name/summary/URL fields", entry.Name)
		}
		var target map[string]any
		if entry.Access.Mode == "iam" && !used[entry.Access.Client] {
			for _, raw := range *applications {
				candidate := raw.(map[string]any)
				if candidate["name"] == appName(entry.Access.Client) {
					target = candidate
					break
				}
			}
			used[entry.Access.Client] = true
		}
		if target == nil {
			target = map[string]any{
				"owner": "admin", "name": catalogApplicationName(entry.Name), "organization": "anas",
				"enableSignUp": false, "enablePassword": false, "disableSignin": true,
			}
			*applications = append(*applications, target)
		}
		target["displayName"] = title
		target["description"] = summary
		target["homepageUrl"] = entry.URL
		target["logo"] = "/files/anas-catalog/" + entry.Icons["128"].Path
		target["order"] = appcatalog.Weight(entry.Weight)
	}
	return nil
}
