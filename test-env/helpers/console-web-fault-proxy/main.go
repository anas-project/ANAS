// Command console-web-fault-proxy is a narrow browser E2E fixture that makes
// only the embedded main SPA JavaScript unavailable while forwarding the
// independently built emergency package to a real anasd instance.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
)

const blockedMainScript = "/assets/main.js"

func main() {
	listen := flag.String("listen", "127.0.0.1:7792", "HTTP listen address")
	upstreamValue := flag.String("upstream", "http://127.0.0.1:7793", "real anasd HTTP origin")
	flag.Parse()
	upstream, err := validateUpstream(*upstreamValue)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:     *listen,
		Handler:  newFaultHandler(upstream),
		ErrorLog: log.New(io.Discard, "", 0),
	}
	fmt.Printf("ready=web_fault_proxy listen=%s upstream=%s\n", *listen, upstream.String())
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func validateUpstream(value string) (*url.URL, error) {
	upstream, err := url.Parse(value)
	if err != nil || upstream.Scheme != "http" || upstream.Host == "" || upstream.User != nil ||
		upstream.Path != "" || upstream.RawQuery != "" || upstream.Fragment != "" {
		return nil, errors.New("upstream must be an absolute HTTP origin without credentials, path, query, or fragment")
	}
	return upstream, nil
}

func newFaultHandler(upstream *url.URL) http.Handler {
	return newFaultHandlerWithTransport(upstream, nil)
}

func newFaultHandlerWithTransport(upstream *url.URL, transport http.RoundTripper) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	if transport != nil {
		proxy.Transport = transport
	}
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Host = upstream.Host
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || !allowedFaultProxyPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == blockedMainScript {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-ANAS-Test-Fault", "main-js-blocked")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("main SPA JavaScript deliberately unavailable\n"))
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func allowedFaultProxyPath(path string) bool {
	switch path {
	case "/", "/emergency", blockedMainScript, "/assets/main.css", "/assets/emergency.css", "/assets/emergency.js", "/healthz":
		return true
	default:
		return false
	}
}

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
}
