// TEST_CASES: MAILMOD-T-005
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

type fakePublicRuleReader struct {
	err   error
	calls int
}

func (f *fakePublicRuleReader) List(context.Context) ([]ingress.CloudflareRule, error) {
	f.calls++
	return nil, f.err
}

func TestPublicRuleReadReportsOnlyReadAccess(t *testing.T) {
	if observePublicRuleRead(t.Context(), nil) != "not-configured" {
		t.Fatal("missing reader reported available")
	}
	for _, test := range []struct {
		err  error
		want string
	}{{nil, "observed"}, {ingress.ErrAuthentication, "authentication-failed"}, {ingress.ErrRateLimited, "unavailable"}, {ingress.ErrIdentity, "unavailable"}} {
		reader := &fakePublicRuleReader{err: test.err}
		if got := observePublicRuleRead(t.Context(), reader); got != test.want || reader.calls != 1 {
			t.Fatal("unexpected read observation", got)
		}
	}
}

type fakeIngressInspector struct {
	calls int
	after func()
}

func (f *fakeIngressInspector) Inspect(context.Context) []ingressReport {
	f.calls++
	if f.after != nil {
		f.after()
	}
	return []ingressReport{{ID: "11111111-1111-4111-8111-111111111111", Provider: "worker-r2", Mode: "active", CloudCheck: "authentication-failed"}}
}
func TestIngressStatusRechecksAdministratorAfterObservation(t *testing.T) {
	for _, mode := range []string{"normal", "not-admin", "role-removed", "directory-down", "query"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			h.store = &setupHTTPStore{httpStore: login}
			d := &setupHTTPDirectory{httpDirectory: dir, admin: true}
			h.directory = d
			inspector := &fakeIngressInspector{}
			h.sources = inspector
			path := "https://mail.example.test/api/ingress"
			switch mode {
			case "not-admin":
				d.admin = false
			case "role-removed":
				inspector.after = func() { d.admin = false }
			case "directory-down":
				dir.err = identity.ErrDirectoryUnavailable
			case "query":
				path += "?token=private"
			}
			r := httptest.NewRequest("GET", path, nil)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if mode == "normal" {
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"public_ready":false`) || !strings.Contains(w.Body.String(), `"cloud_check":"authentication-failed"`) {
					t.Fatal(w.Code, w.Body.String())
				}
			} else if w.Code == 200 || strings.Contains(w.Body.String(), "11111111") {
				t.Fatal("status leaked after loss of administrator authorization", w.Code, w.Body.String())
			}
			if mode != "normal" && mode != "role-removed" && inspector.calls != 0 {
				t.Fatal("unauthorized request reached cloud")
			}
		})
	}
}
