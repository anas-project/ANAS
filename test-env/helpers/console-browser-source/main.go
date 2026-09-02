// REQUIREMENTS: CONSOLE-R-093
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const exchangePath = "/api/v1/auth/enrollment/exchange"

var handoffPage = template.Must(template.New("handoff").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>ANAS R-093 browser handoff</title>
</head>
<body>
  <form method="post" action="{{.}}" autocomplete="off">
    <label>One-time handoff <input name="handoff" type="text" autocomplete="off" spellcheck="false"></label>
    <button type="submit">Exchange handoff</button>
  </form>
</body>
</html>
`))

func main() {
	listen := flag.String("listen", "127.0.0.1:7796", "HTTPS listen address")
	certificate := flag.String("certificate", "", "TLS certificate chain")
	privateKey := flag.String("private-key", "", "TLS private key")
	action := flag.String("action", "", "absolute enrollment exchange form action")
	flag.Parse()

	if *certificate == "" || *privateKey == "" {
		log.Fatal("certificate and private-key are required")
	}
	formAction, err := validateFormAction(*action)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		Addr:     *listen,
		Handler:  newHandler(formAction),
		ErrorLog: log.New(io.Discard, "", 0),
		TLSConfig: &tls.Config{
			MinVersion:             tls.VersionTLS12,
			SessionTicketsDisabled: true,
		},
	}
	fmt.Printf("ready=browser_source listen=%s\n", *listen)
	if err := server.ListenAndServeTLS(*certificate, *privateKey); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func validateFormAction(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != exchangePath ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", fmt.Errorf("action must be an absolute HTTPS URL ending in %s without credentials, query, or fragment", exchangePath)
	}
	return parsed.String(), nil
}

func newHandler(action string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "strict-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action "+formActionOrigin(action))
		if r.URL.Path != "/" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := handoffPage.Execute(w, action); err != nil {
			http.Error(w, "render handoff form", http.StatusInternalServerError)
		}
	})
}

func formActionOrigin(action string) string {
	parsed, err := url.Parse(action)
	if err != nil {
		return "'none'"
	}
	return strings.ToLower(parsed.Scheme) + "://" + parsed.Host
}

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
}
