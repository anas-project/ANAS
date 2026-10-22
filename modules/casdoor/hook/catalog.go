package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

func catalogApplicationName(name string) string { return "app-anas-catalog-" + name }

// Only native Application fields are projected. Full translations and assets
// remain in the immutable catalog for the second-stage portal.
func projectCatalogApplications(e map[string]string, c appcatalog.Catalog, applications *[]any) error {
	used := map[string]bool{}
	for _, entry := range c.Applications {
		if !entry.Enabled {
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
			if entry.Hidden {
				continue
			}
			target = map[string]any{
				"owner": "admin", "name": catalogApplicationName(entry.Name), "organization": "anas",
				"enableSignUp": false, "enablePassword": false, "disableSignin": true,
			}
			*applications = append(*applications, target)
		}
		target["displayName"] = title
		target["description"] = summary
		target["homepageUrl"] = entry.URL
		target["logo"] = "/files/anas-catalog/" + entry.Icons["512"].Path
		// Native Casdoor assumes a wide wordmark. Our square app marks have a
		// bounded display size while retaining enough pixels for retina screens.
		target["formCss"] = squareLogoCSS
		target["formCssMobile"] = squareLogoCSS
		target["order"] = appcatalog.Weight(entry.Weight)
		for _, raw := range *applications {
			candidate := raw.(map[string]any)
			if candidate["name"] == appName(entry.Access.Client)+"-reauth" {
				candidate["logo"], candidate["displayName"] = target["logo"], title
				candidate["formCss"], candidate["formCssMobile"] = squareLogoCSS, squareLogoCSS
			}
		}
	}
	// Provider-owned directory and recovery applications also need a local logo.
	for _, entry := range c.Applications {
		if !entry.Enabled || entry.Name != "casdoor-admin" {
			continue
		}
		for _, raw := range *applications {
			candidate := raw.(map[string]any)
			if candidate["name"] == "app-built-in" || candidate["name"] == "app-anas-directory" {
				candidate["logo"] = "/files/anas-catalog/" + entry.Icons["512"].Path
				candidate["formCss"], candidate["formCssMobile"] = squareLogoCSS, squareLogoCSS
			}
		}
	}
	for _, entry := range c.Applications {
		if !entry.Enabled || entry.Access.Mode != "iam" || entry.Access.Client != "user_portal" {
			continue
		}
		for _, raw := range *applications {
			candidate := raw.(map[string]any)
			if candidate["organization"] != "anas" || candidate["disableSignin"] == true {
				continue
			}
			candidate["forgetUrl"] = strings.TrimRight(entry.URL, "/") + "/#/recover"
			candidate["signinItems"] = directorySigninItems()
		}
	}
	return nil
}

const squareLogoCSS = "<style>.panel-logo{width:80px;height:80px;object-fit:contain;border-radius:14px}</style>"

func directorySigninItems() []any {
	// Native item names, in the upstream default order. Only the recovery
	// label/layout differs; no custom HTML or executable frontend is injected.
	items := []any{}
	for _, name := range []string{"Back button", "Languages", "Logo", "Signin methods", "Username", "Password", "Verification code", "Agreement", "Forgot password?", "Login button", "Signup link", "Providers"} {
		item := map[string]any{"name": name, "visible": true, "rule": "None"}
		switch name {
		case "Back button":
			item["customCss"] = ".back-button{top:65px;left:15px;position:absolute}"
		case "Languages":
			item["customCss"] = ".login-languages{top:55px;right:5px;position:absolute}"
		case "Login button":
			item["customCss"] = ".login-button-box{margin-bottom:5px}.login-button{width:100%}"
		case "Signup link":
			item["customCss"] = ".login-signup-link{margin-bottom:24px;display:flex;justify-content:end}"
		case "Providers":
			item["customCss"] = ".provider-img{width:30px;margin:5px}.provider-big-img{margin-bottom:10px}"
		case "Forgot password?":
			item["label"] = "首次／到期改密 · Set new password"
			item["customCss"] = ".login-forget-password{display:flex;flex-wrap:wrap;gap:8px;width:100%;margin-bottom:20px}"
		}
		items = append(items, item)
	}
	return items
}
