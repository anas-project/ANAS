package main

import (
	"embed"
	"net/http"
	"net/url"
	"strings"

	"github.com/anas-project/ANAS/modules/user_portal/browserlogout"
)

//go:embed ui/*
var ui embed.FS

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	data, _ := ui.ReadFile("ui/index.html")
	if r.URL.Path == "/signed-out" && r.URL.Query().Get("native") == "1" {
		// Cross-site navigations and browsers without Fetch Metadata require an
		// explicit button. Merely embedding/linking this URL must not log out.
		automatic := r.Header.Get("Sec-Fetch-Site") == "same-site" && r.Header.Get("Sec-Fetch-Dest") == "document"
		if ref := r.Referer(); ref != "" {
			u, err := url.Parse(ref)
			allowed := err == nil && u.User == nil && equivalentOrigin(u.Scheme+"://"+u.Host, s.config.Issuer)
			if targets, err := browserlogout.Targets(s.catalog, s.config.URL); err == nil {
				for _, target := range targets {
					allowed = allowed || u != nil && u.User == nil && equivalentOrigin(u.Scheme+"://"+u.Host, target.Origin)
				}
			}
			automatic = automatic && allowed
		}
		value := "manual"
		if automatic {
			value = "automatic"
		}
		data = []byte(strings.Replace(string(data), "<head>", "<head><meta name=\"anas-native-logout\" content=\""+value+"\">", 1))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name != "app.js" && name != "i18n.js" && name != "theme.js" && name != "style.css" && name != "chevron-down.svg" {
		failure(w, 404, "not_found")
		return
	}
	data, err := ui.ReadFile("ui/" + name)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	if strings.HasSuffix(name, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else if name == "chevron-down.svg" {
		w.Header().Set("Content-Type", "image/svg+xml")
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(data)
}
