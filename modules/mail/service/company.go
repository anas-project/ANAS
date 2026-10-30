package main

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type companyDirectoryReader interface {
	CompanySnapshot(context.Context) (identity.CompanySnapshot, error)
}
type companyDirectoryWriter interface {
	SaveCompanyDirectory(context.Context, identity.CompanySnapshot) (string, error)
}
type companyDirectoryStore interface {
	CompanyDirectory(context.Context) (store.CompanyDirectory, error)
}

func (s *loginHandler) companyAccess(w http.ResponseWriter, r *http.Request, fields ...string) (url.Values, store.MailboxGrant, bool) {
	if !s.companyEnabled {
		loginError(w, 404, "source-not-enabled")
		return nil, store.MailboxGrant{}, false
	}
	if !serviceRequest(r, "roundcube", s.webmail.token) {
		loginError(w, 401, "service-authentication-required")
		return nil, store.MailboxGrant{}, false
	}
	form, ok := exactWebmailForm(w, r, append([]string{"refresh_token"}, fields...)...)
	if !ok {
		return nil, store.MailboxGrant{}, false
	}
	if len(fields) > 0 && form.Get("source") != "company" {
		loginError(w, 403, "source-denied")
		return nil, store.MailboxGrant{}, false
	}
	i, err := s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
	if err == nil {
		i.Grant, err = s.freshGrant(r.Context(), i.Grant)
	}
	if err != nil {
		protocolError(w, err, false)
		return nil, store.MailboxGrant{}, false
	}
	return form, i.Grant, true
}
func (s *loginHandler) companyCurrent(w http.ResponseWriter, r *http.Request, form url.Values, grant store.MailboxGrant) bool {
	current, err := s.protocol.store.RefreshGrant(r.Context(), form.Get("refresh_token"))
	if err != nil {
		protocolError(w, err, false)
		return false
	}
	if current.Grant != grant {
		loginError(w, 401, "credential-denied")
		return false
	}
	return true
}
func (s *loginHandler) companySnapshot(w http.ResponseWriter, r *http.Request) (store.CompanyDirectory, bool) {
	data, ok := s.store.(companyDirectoryStore)
	if !ok {
		loginError(w, 503, "company-directory-unavailable")
		return store.CompanyDirectory{}, false
	}
	snapshot, err := data.CompanyDirectory(r.Context())
	if err != nil || time.Since(snapshot.RefreshedAt) > 60*time.Second || snapshot.RefreshedAt.After(time.Now().Add(5*time.Second)) {
		loginError(w, 503, "company-directory-unavailable")
		return store.CompanyDirectory{}, false
	}
	return snapshot, true
}
func (s *loginHandler) companySources(w http.ResponseWriter, r *http.Request) {
	form, grant, ok := s.companyAccess(w, r)
	if !ok {
		return
	}
	if !s.companyCurrent(w, r, form, grant) {
		return
	}
	reply(w, 200, map[string]any{"sources": []map[string]any{{"id": "company", "readonly": true, "groups": true, "write": false}}})
}
func (s *loginHandler) companySearch(w http.ResponseWriter, r *http.Request) {
	form, grant, ok := s.companyAccess(w, r, "source", "query", "group", "offset", "limit")
	if !ok {
		return
	}
	query := strings.TrimSpace(form.Get("query"))
	offset, offsetErr := strconv.Atoi(form.Get("offset"))
	limit, limitErr := strconv.Atoi(form.Get("limit"))
	group := form.Get("group")
	if len(query) > 256 || offsetErr != nil || limitErr != nil || offset < 0 || offset > 5000 || limit < 1 || limit > 100 || group != "all" && !webmailIDPattern.MatchString(group) {
		loginError(w, 400, "invalid-directory-query")
		return
	}
	snapshot, ok := s.companySnapshot(w, r)
	if !ok {
		return
	}
	if group != "all" {
		found := false
		for _, candidate := range snapshot.Data.Groups {
			found = found || candidate.ID == group
		}
		if !found {
			loginError(w, 404, "group-not-found")
			return
		}
	}
	query = strings.ToLower(query)
	contacts := []identity.CompanyContact{}
	for _, person := range snapshot.Data.People {
		inGroup := group == "all"
		for _, id := range person.Groups {
			inGroup = inGroup || id == group
		}
		if !inGroup {
			continue
		}
		if query != "*" && !strings.Contains(strings.ToLower(person.Name+"\n"+person.Email+"\n"+person.Department+"\n"+person.Title+"\n"+person.Phone+"\n"+person.Mobile), query) {
			continue
		}
		contacts = append(contacts, person)
	}
	sort.Slice(contacts, func(i, j int) bool {
		a, b := strings.ToLower(contacts[i].Name), strings.ToLower(contacts[j].Name)
		if a != b {
			return a < b
		}
		return contacts[i].ID < contacts[j].ID
	})
	total := len(contacts)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	if !s.companyCurrent(w, r, form, grant) {
		return
	}
	reply(w, 200, map[string]any{"source": "company", "records": contacts[offset:end], "total": total, "offset": offset, "revision": snapshot.Fingerprint})
}
func (s *loginHandler) companyGroups(w http.ResponseWriter, r *http.Request) {
	form, grant, ok := s.companyAccess(w, r, "source")
	if !ok {
		return
	}
	snapshot, ok := s.companySnapshot(w, r)
	if !ok {
		return
	}
	groups := []map[string]any{}
	for _, group := range snapshot.Data.Groups {
		groups = append(groups, map[string]any{"id": group.ID, "name": group.Name, "count": len(group.Members)})
	}
	if !s.companyCurrent(w, r, form, grant) {
		return
	}
	reply(w, 200, map[string]any{"source": "company", "groups": groups, "revision": snapshot.Fingerprint})
}
func (s *loginHandler) companyContact(w http.ResponseWriter, r *http.Request) {
	form, grant, ok := s.companyAccess(w, r, "source", "id")
	if !ok {
		return
	}
	if !webmailIDPattern.MatchString(form.Get("id")) {
		loginError(w, 400, "invalid-contact-id")
		return
	}
	snapshot, ok := s.companySnapshot(w, r)
	if !ok {
		return
	}
	for _, person := range snapshot.Data.People {
		if person.ID == form.Get("id") {
			if !s.companyCurrent(w, r, form, grant) {
				return
			}
			reply(w, 200, map[string]any{"source": "company", "record": person, "revision": snapshot.Fingerprint})
			return
		}
	}
	if !s.companyCurrent(w, r, form, grant) {
		return
	}
	reply(w, 200, map[string]any{"source": "company", "record": nil, "revision": snapshot.Fingerprint})
}
