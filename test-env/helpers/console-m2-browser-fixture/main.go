// Command console-m2-browser-fixture serves the production console assets with
// a narrow fake API for the R-124 browser E2E. It verifies that the SPA renders
// the server-expanded lifecycle chain and submits that exact ordered chain.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const apiVersion = "anas.dev/api/v1"

type fixture struct {
	assets string
	result string

	mu        sync.Mutex
	confirmed bool
	request   lifecycleRequest
}

type lifecycleRequest struct {
	Modules              []string `json:"modules"`
	ExpectedDeploymentID string   `json:"expected_deployment_id"`
	ExpectedDigest       string   `json:"expected_digest"`
	ConfirmedModules     []string `json:"confirmed_modules"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:7794", "HTTP listen address")
	assets := flag.String("assets", "internal/webui/dist/main", "built main console assets")
	result := flag.String("result", "", "optional JSON result path")
	flag.Parse()

	info, err := os.Stat(*assets)
	if err != nil || !info.IsDir() {
		log.Fatalf("assets must name a built console directory: %v", err)
	}
	f := &fixture{assets: *assets, result: *result}
	server := &http.Server{
		Addr:     *listen,
		Handler:  f.handler(),
		ErrorLog: log.New(io.Discard, "", 0),
	}
	fmt.Printf("ready=console_m2_browser_fixture listen=%s\n", *listen)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (f *fixture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/system":
			writeJSON(w, http.StatusOK, map[string]any{
				"api_version":   apiVersion,
				"build":         map[string]any{"version": "m2-browser-fixture", "commit": "fixture", "date": "2026-09-03"},
				"capabilities":  map[string]any{"read_only": false},
				"workspace_ids": []string{"main"}, "certificate_issuer": "none", "console_state": "full",
				"listener": "direct", "direct_recovery_urls": []string{}, "proxy_url": nil,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/auth/session":
			writeJSON(w, http.StatusOK, map[string]any{
				"api_version": apiVersion, "csrf_token": "fixture-csrf", "state": "full",
				"expires_at": "2030-01-01T00:00:00Z", "idle_expires_at": "2030-01-01T00:00:00Z",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workspaces/main/config":
			w.Header().Set("ETag", `"fixture-validator"`)
			writeJSON(w, http.StatusOK, map[string]any{
				"api_version": apiVersion, "workspace_id": "main", "managed": true,
				"config":            map[string]any{"modules": map[string]any{"db": map[string]any{}, "app": map[string]any{}}},
				"available_modules": []string{"db", "app"}, "fields": []any{},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/workspaces/main/status":
			writeJSON(w, http.StatusOK, map[string]any{
				"api_version": apiVersion, "workspace_id": "main", "active_deployment": "dep-active",
				"runtime_status": "running", "runtime_healthy": true, "runtime_probe_error": nil,
				"module_runtime": []map[string]any{
					{"module": "db", "runtime": "running", "health": "healthy", "containers": 1},
					{"module": "app", "runtime": "running", "health": "healthy", "containers": 1},
				},
				"activated_at": "2026-09-03T00:00:00Z", "verified_at": "2026-09-03T00:00:00Z",
				"previous_deployments": []string{"dep-previous"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/workspaces/main/modules/actions/restart":
			f.lifecycle(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs":
			f.jobs(w)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/jobs/job-r124-browser":
			writeJSON(w, http.StatusOK, map[string]any{"api_version": apiVersion, "job": jobDetail()})
		case r.Method == http.MethodGet && r.URL.Path == "/__fixture/status":
			f.status(w)
		case r.Method == http.MethodGet && (r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/assets/")):
			path := filepath.Join(f.assets, filepath.Clean("/"+r.URL.Path))
			if r.URL.Path == "/" {
				path = filepath.Join(f.assets, "index.html")
			}
			http.ServeFile(w, r, path)
		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fixture) lifecycle(w http.ResponseWriter, r *http.Request) {
	var request lifecycleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeProblem(w, "fixture_request_invalid", http.StatusBadRequest)
		return
	}
	if request.ExpectedDigest == "" && request.ExpectedDeploymentID == "" && request.ConfirmedModules == nil {
		if !reflect.DeepEqual(request.Modules, []string{"db"}) {
			writeProblem(w, "fixture_preview_expected_db", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"api_version": apiVersion, "workspace_id": "main",
			"preview": map[string]any{
				"deployment_id": "dep-active", "action": "restart", "requested_modules": []string{"db"},
				"affected_modules": []string{"db", "app"}, "digest": strings.Repeat("a", 64),
			},
		})
		return
	}
	want := lifecycleRequest{
		Modules: []string{"db"}, ExpectedDeploymentID: "dep-active", ExpectedDigest: strings.Repeat("a", 64),
		ConfirmedModules: []string{"db", "app"},
	}
	if !reflect.DeepEqual(request, want) || r.Header.Get("Idempotency-Key") == "" {
		writeProblem(w, "fixture_confirmation_mismatch", http.StatusConflict)
		return
	}
	f.mu.Lock()
	f.confirmed = true
	f.request = request
	f.mu.Unlock()
	if err := f.writeResult(); err != nil {
		writeProblem(w, "fixture_result_write_failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"api_version": apiVersion, "job": jobSummary("queued"), "existing": false,
	})
}

func (f *fixture) jobs(w http.ResponseWriter) {
	f.mu.Lock()
	confirmed := f.confirmed
	f.mu.Unlock()
	items := []any{}
	if confirmed {
		items = append(items, jobSummary("succeeded"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_version": apiVersion, "items": items, "next_cursor": nil})
}

func (f *fixture) status(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": f.confirmed, "request": f.request})
}

func (f *fixture) writeResult() error {
	if f.result == "" {
		return nil
	}
	f.mu.Lock()
	result := map[string]any{"confirmed": f.confirmed, "request": f.request}
	f.mu.Unlock()
	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(f.result, content, 0o600)
}

func jobSummary(status string) map[string]any {
	now := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	return map[string]any{
		"id": "job-r124-browser", "kind": "deployment.restart", "workspace_id": "main", "mutating": true,
		"status": status, "progress": map[bool]int{true: 100, false: 0}[status == "succeeded"],
		"created_at": now, "finished_at": now, "revision": 2,
	}
}

func jobDetail() map[string]any {
	job := jobSummary("succeeded")
	job["events"] = []any{}
	job["warnings"] = []any{}
	job["result"] = map[string]any{"affected_modules": []string{"db", "app"}}
	return job
}

func writeProblem(w http.ResponseWriter, code string, status int) {
	w.Header().Set("Content-Type", "application/problem+json")
	writeJSON(w, status, map[string]any{
		"api_version": apiVersion, "type": "about:blank", "title": http.StatusText(status),
		"status": status, "detail": code, "code": code,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
}
