package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

const companyTestGroup = "77777777-7777-4777-8777-777777777777"

type companyHTTPStore struct {
	*httpStore
	directory store.CompanyDirectory
	reads     int
	err       error
	afterRead func()
}

func (s *companyHTTPStore) CompanyDirectory(context.Context) (store.CompanyDirectory, error) {
	s.reads++
	if s.afterRead != nil {
		s.afterRead()
	}
	return s.directory, s.err
}
func companyHTTPFixture(t *testing.T) (*loginHandler, *companyHTTPStore, *protocolHTTPStore, *protocolHTTPDirectory) {
	t.Helper()
	h, p, d, login := protocolFixture(t)
	h.companyEnabled = true
	snapshot := identity.CompanySnapshot{Schema: identity.CompanySchema, People: []identity.CompanyContact{{ID: fixtureAnchor, Name: "Alice", Email: "alice@example.test", Groups: []string{companyTestGroup}, DirectGroups: []string{companyTestGroup}}}, Groups: []identity.CompanyGroup{{ID: companyTestGroup, Name: "Finance", Members: []string{fixtureAnchor}, DirectMembers: []string{fixtureAnchor}, Children: []string{}}}}
	data := &companyHTTPStore{httpStore: login, directory: store.CompanyDirectory{Data: snapshot, Fingerprint: strings.Repeat("a", 64), RefreshedAt: time.Now()}}
	h.store = data
	return h, data, p, d
}
func companyRequest(h *loginHandler, path string, form url.Values, mutate func(*http.Request)) *httptest.ResponseRecorder {
	return protocolRequest(h.internalHandler(), "/internal/directory/"+path, form, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+webmailTestKey)
		if mutate != nil {
			mutate(r)
		}
	})
}
func TestCompanyServiceOnlyCurrentMailboxAndCompanySource(t *testing.T) {
	for _, mode := range []string{"success", "source-denied", "forged-owner", "no-service-key", "cookie", "directory-down", "stale-snapshot", "logout-during-read"} {
		t.Run(mode, func(t *testing.T) {
			h, data, p, d := companyHTTPFixture(t)
			form := url.Values{"refresh_token": {"synthetic-refresh"}, "source": {"company"}, "query": {"Alice"}, "group": {"all"}, "offset": {"0"}, "limit": {"20"}}
			var mutate func(*http.Request)
			want := 200
			switch mode {
			case "source-denied":
				form.Set("source", "shared-group")
				want = 403
			case "forged-owner":
				form.Set("owner", fixtureAnchor)
				want = 400
			case "no-service-key":
				mutate = func(r *http.Request) { r.Header.Del("Authorization") }
				want = 401
			case "cookie":
				mutate = func(r *http.Request) { r.Header.Set("Cookie", "person=forged") }
				want = 401
			case "directory-down":
				d.err = identity.ErrDirectoryUnavailable
				want = 503
			case "stale-snapshot":
				data.directory.RefreshedAt = time.Now().Add(-time.Minute - time.Second)
				want = 503
			case "logout-during-read":
				data.afterRead = func() { p.err = store.ErrCredentialDenied }
				want = 401
			}
			w := companyRequest(h, "search", form, mutate)
			if w.Code != want {
				t.Fatal("source authorization", mode, w.Code, w.Body.String())
			}
			if want != 200 {
				if strings.Contains(w.Body.String(), "alice@example.test") {
					t.Fatal("denied source leaked cached contacts")
				}
				return
			}
			var result struct {
				Records []identity.CompanyContact `json:"records"`
				Total   int                       `json:"total"`
			}
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Total != 1 || len(result.Records) != 1 {
				t.Fatal("company query")
			}
			for _, private := range []string{"proxyAddresses", "protocol_login", "storage_ref", "owner_anchor"} {
				if strings.Contains(w.Body.String(), private) {
					t.Fatal("private fact in source response")
				}
			}
		})
	}
}
func TestCompanySourcesGroupsLookupAndDisabledWrites(t *testing.T) {
	h, _, _, _ := companyHTTPFixture(t)
	w := companyRequest(h, "sources", url.Values{"refresh_token": {"synthetic-refresh"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"readonly":true`) || !strings.Contains(w.Body.String(), `"write":false`) {
		t.Fatal("source capabilities", w.Code)
	}
	w = companyRequest(h, "groups", url.Values{"refresh_token": {"synthetic-refresh"}, "source": {"company"}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Finance") {
		t.Fatal("public group list", w.Code)
	}
	w = companyRequest(h, "contact", url.Values{"refresh_token": {"synthetic-refresh"}, "source": {"company"}, "id": {fixtureAnchor}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alice@example.test") {
		t.Fatal("contact ID lookup", w.Code)
	}
	w = companyRequest(h, "write", url.Values{"refresh_token": {"synthetic-refresh"}, "source": {"company"}}, nil)
	if w.Code != 404 {
		t.Fatal("company source accepted a write", w.Code)
	}
}
