package main

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed ui/*
var ui embed.FS

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	data, _ := ui.ReadFile("ui/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name != "app.js" && name != "style.css" {
		failure(w, 404, "not_found")
		return
	}
	data, err := ui.ReadFile("ui/" + name)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	if name == "app.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(data)
}
