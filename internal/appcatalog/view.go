package appcatalog

import "sort"

// View contains only already-authorized presentation data, never policy or provenance.
type View struct {
	Language string  `json:"language"`
	Groups   []Group `json:"groups"`
}

type Group struct {
	Kind        string     `json:"kind"`
	DisplayName string     `json:"display_name"`
	Categories  []Subgroup `json:"categories"`
}

type Subgroup struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description,omitempty"`
	Applications []Card `json:"applications"`
}

type Card struct {
	Name             string          `json:"name"`
	DisplayName      string          `json:"display_name"`
	URL              string          `json:"url"`
	Summary          string          `json:"summary"`
	Description      string          `json:"description"`
	DocumentationURL string          `json:"documentation_url"`
	Weight           int             `json:"weight"`
	Categories       []string        `json:"categories"`
	Icons            map[string]Icon `json:"icons"`
}

// BuildView must receive the provider's already-authorized applications.
// It does not authenticate a session or substitute for the IAM permission engine.
func BuildView(categories []Category, authorized []Application, lang string, filter []string) View {
	lang = supportedLanguage(lang)
	result := View{Language: lang, Groups: []Group{}}
	selected := map[string]bool{}
	for _, id := range filter {
		selected[id] = true
	}
	ordered := append([]Category{}, categories...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if Weight(a.Weight) != Weight(b.Weight) {
			return Weight(a.Weight) < Weight(b.Weight)
		}
		return a.ID < b.ID
	})
	for _, kind := range []string{"user", "admin"} {
		labels := Text{"zh-CN": "用户应用", "en": "User applications"}
		if kind == "admin" {
			labels = Text{"zh-CN": "后台管理", "en": "Administration"}
		}
		group := Group{Kind: kind, DisplayName: labels.Resolve(lang), Categories: []Subgroup{}}
		for _, category := range ordered {
			subgroup := Subgroup{ID: category.ID, DisplayName: category.DisplayName.Resolve(lang), Description: category.Description.Resolve(lang), Applications: []Card{}}
			seen := map[string]bool{}
			for _, app := range authorized {
				if !app.Enabled || app.Hidden || app.Kind != kind || len(app.Categories) == 0 || app.Categories[0] != category.ID || seen[app.Name] {
					continue
				}
				matches := len(selected) == 0
				for _, id := range app.Categories {
					if selected[id] {
						matches = true
					}
				}
				if !matches {
					continue
				}
				seen[app.Name] = true
				subgroup.Applications = append(subgroup.Applications, Card{
					Name: app.Name, DisplayName: app.DisplayName.Resolve(lang), URL: app.URL, Summary: app.Summary.Resolve(lang),
					Description: app.Description.Resolve(lang), DocumentationURL: app.DocumentationURL.Resolve(lang),
					Weight: Weight(app.Weight), Categories: append([]string{}, app.Categories...), Icons: app.Icons,
				})
			}
			sort.Slice(subgroup.Applications, func(i, j int) bool {
				a, b := subgroup.Applications[i], subgroup.Applications[j]
				if a.Weight != b.Weight {
					return a.Weight < b.Weight
				}
				return a.Name < b.Name
			})
			if len(subgroup.Applications) > 0 {
				group.Categories = append(group.Categories, subgroup)
			}
		}
		if len(group.Categories) > 0 {
			result.Groups = append(result.Groups, group)
		}
	}
	return result
}
