// TEST_CASES: MAILMOD-T-017 MAILMOD-T-019
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOutboundQueueRequiresAdministratorAndReportsOnlyObservation(t *testing.T) {
	for _, mode := range []string{"queued", "none", "unavailable", "invalid-result", "not-admin", "origin", "body", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			h.directory = &setupHTTPDirectory{httpDirectory: dir, admin: mode != "not-admin"}
			calls := 0
			h.queueProbe = func(ctx context.Context) (string, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded queue check")
				}
				switch mode {
				case "queued":
					return "queued-observed", nil
				case "none":
					return "none-observed", nil
				case "invalid-result":
					return "empty", nil
				case "revoked":
					login.sessionErr = errors.New("revoked")
				}
				return "unknown", errors.New("private queue detail")
			}
			body := ""
			if mode == "body" {
				body = "unexpected"
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/outbound-queue", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", "https://mail.example.test")
			if mode == "origin" {
				r.Header.Set("Origin", "https://evil.test")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if mode == "not-admin" || mode == "origin" || mode == "body" {
				if w.Code < 400 || calls != 0 {
					t.Fatal("denied request invoked queue probe", w.Code, calls)
				}
				return
			}
			if mode == "revoked" {
				if w.Code < 400 || strings.Contains(w.Body.String(), "status") {
					t.Fatal("revoked admin received queue state")
				}
				return
			}
			want := map[string]string{"queued": "queued-observed", "none": "none-observed", "unavailable": "unknown", "invalid-result": "unknown"}[mode]
			if w.Code != 200 || calls != 1 || !strings.Contains(w.Body.String(), `"status":"`+want+`"`) || !strings.Contains(w.Body.String(), `"checked_at":`) || strings.Contains(w.Body.String(), "private queue detail") {
				t.Fatal("invalid queue result", w.Code, w.Body.String())
			}
		})
	}
}
