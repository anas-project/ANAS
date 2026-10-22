package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/apptheme"
	"github.com/anas-project/ANAS/modules/user_portal/browserlogout"
)

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if probe, ok := s.directory.(interface{ Probe() error }); ok {
		if probe.Probe() != nil {
			failure(w, 503, "identity_unavailable")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", s.config.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		failure(w, 503, "identity_unavailable")
		return
	}
	response, err := s.oidc.client.Do(req)
	if err != nil {
		failure(w, 503, "identity_unavailable")
		return
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		failure(w, 503, "identity_unavailable")
		return
	}
	s.audit.mu.Lock()
	broken := s.audit.broken
	s.audit.mu.Unlock()
	if broken {
		failure(w, 503, "audit_unavailable")
		return
	}
	respond(w, 200, map[string]string{"status": "ok"})
}

const sessionCookie = "__Host-anas_portal"
const flowCookie = "__Host-anas_portal_flow"

type Server struct {
	config    Config
	oidc      *OIDC
	directory Directory
	audit     *Journal
	catalog   appcatalog.Catalog
	store     *Store
	limiter   *Limiter
	// preferences holds account-level display choices only.
	preferences *preferenceStore
	themes      *themeLibrary
	themesOnce  sync.Once
	peers       []netip.Addr
	digest      string
	writes      sync.Mutex
}

func cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) session(r *http.Request) (Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Session{}, false
	}
	return s.store.get(c.Value)
}

func equivalentOrigin(a, b string) bool {
	x, err := url.Parse(a)
	if err != nil || x.User != nil || x.Path != "" || x.RawQuery != "" || x.Fragment != "" || x.Scheme != "https" {
		return false
	}
	y, err := url.Parse(b)
	if err != nil || y.Scheme != "https" {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		return u.Port()
	}
	return strings.EqualFold(x.Hostname(), y.Hostname()) && port(x) == port(y)
}

func (s *Server) origin(r *http.Request) bool {
	return equivalentOrigin(r.Header.Get("Origin"), s.config.URL)
}

func (s *Server) csrf(r *http.Request, session Session) bool {
	if !s.origin(r) {
		return false
	}
	token := r.Header.Get("X-CSRF-Token")
	if mediaType(r) == "application/x-www-form-urlencoded" {
		if r.ParseForm() != nil {
			return false
		}
		token = r.PostForm.Get("csrf")
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRF)) == 1
}

func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, code string) {
	respond(w, status, map[string]string{"error": code})
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", s.health)
	m.HandleFunc("GET /{$}", s.page)
	m.HandleFunc("GET /signed-out", s.page)
	m.HandleFunc("GET /static/", s.static)
	m.HandleFunc("GET /login", s.login)
	m.HandleFunc("GET /oidc/callback", s.callback)
	m.HandleFunc("GET /oidc/reauth-callback", s.callback)
	m.HandleFunc("POST /oidc/backchannel-logout", s.backchannel)
	m.HandleFunc("GET /api/bootstrap", s.bootstrap)
	m.HandleFunc("GET /api/me", s.me)
	m.HandleFunc("POST /api/me/preferences", s.updatePreferences)
	m.HandleFunc("GET /api/themes", s.themesList)
	m.HandleFunc("POST /api/themes", s.importTheme)
	m.HandleFunc("GET /api/themes/{id}/export", s.exportTheme)
	m.HandleFunc("POST /api/me/profile", s.updateProfile)
	m.HandleFunc("POST /api/administration/prepare", s.prepareOperation)
	m.HandleFunc("POST /api/administration/execute", s.executeOperation)
	m.HandleFunc("GET /api/apps", s.apps)
	m.HandleFunc("GET /api/assets/{name}/{size}", s.asset)
	m.HandleFunc("GET /api/users", s.users)
	m.HandleFunc("POST /api/users", s.createUser)
	m.HandleFunc("GET /api/users/{anchor}", s.user)
	m.HandleFunc("GET /api/groups", s.groups)
	m.HandleFunc("GET /api/audit", s.auditQuery)
	m.HandleFunc("POST /api/logout", s.logout)
	m.HandleFunc("POST /api/logout/complete", s.logout)
	m.HandleFunc("GET "+browserlogout.Path, s.browserLogout)
	m.HandleFunc("POST /api/reauth", s.reauth)
	m.HandleFunc("POST /api/password/change", s.changePassword)
	m.HandleFunc("POST /api/password/recover", s.recoverPassword)
	m.HandleFunc("POST /api/users/{anchor}/reset", s.resetPassword)
	m.HandleFunc("POST /api/users/{anchor}/unlock", s.unlock)
	m.HandleFunc("POST /api/groups/{anchor}/member", s.member)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Same-origin form navigations need their true Origin for CSRF checks.
		// Cross-origin IAM and application links still receive no Referer.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		frameSources := ""
		session, hasSession := s.session(r)
		if hasSession && !session.Anonymous || r.URL.Path == "/signed-out" {
			if targets, err := browserlogout.Targets(s.catalog, s.config.URL); err == nil {
				for _, target := range targets {
					frameSources += " " + target.Origin
				}
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self' "+s.config.Issuer+"; frame-src 'self'"+frameSources+"; frame-ancestors 'none'; base-uri 'none'; form-action 'self' "+s.config.Issuer)
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip, parseErr := netip.ParseAddr(host)
		trusted := false
		for _, peer := range s.peers {
			trusted = trusted || ip.Unmap() == peer.Unmap()
		}
		if err != nil || parseErr != nil || !trusted && !(ip.IsLoopback() && r.URL.Path == "/health") {
			failure(w, 403, "untrusted_proxy")
			return
		}
		if r.URL.Path != "/health" && r.URL.Path != browserlogout.Path {
			if !equivalentOrigin("https://"+r.Host, s.config.URL) {
				failure(w, 403, "invalid_host")
				return
			}
		}
		bodyLimit := int64(16 << 10)
		if r.Method == "POST" && r.URL.Path == "/api/themes" {
			bodyLimit = apptheme.MaxBytes
		}
		r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
		m.ServeHTTP(w, r)
	})
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	v, ok := s.session(r)
	if !ok {
		v = Session{ID: randomID(), CSRF: randomID(), Anonymous: true, Expires: time.Now().Add(10 * time.Minute)}
		if !s.store.add(v) {
			failure(w, 503, "capacity_reached")
			return
		}
		cookie(w, sessionCookie, v.ID, 600)
	}
	respond(w, 200, map[string]any{"csrf": v.CSRF, "language": appcatalog.SelectLanguage(r.URL.Query().Get("lang"), r.Header.Get("Accept-Language"), s.config.Language), "authenticated": !v.Anonymous})
}

func (s *Server) begin(w http.ResponseWriter, r *http.Request, v Session, reauth bool) {
	f := Flow{State: randomID(), Nonce: randomID(), Verifier: randomID(), Cookie: randomID(), Reauth: reauth, Expires: time.Now().Add(5 * time.Minute)}
	if theme := r.URL.Query().Get("theme"); theme == "dark" || theme == "default" {
		f.Theme = theme
	}
	if reauth {
		f.Session = v.ID
		f.Subject = v.Subject
	}
	if !s.store.addFlow(f) {
		failure(w, 503, "capacity_reached")
		return
	}
	cookie(w, flowCookie, f.Cookie, 300)
	http.Redirect(w, r, s.oidc.authorizeURL(f), http.StatusSeeOther)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) { s.begin(w, r, Session{}, false) }
func (s *Server) reauth(w http.ResponseWriter, r *http.Request) {
	v, _, _, ok := s.current(w, r)
	if !ok {
		return
	}
	if !s.csrf(r, v) {
		s.rejected(v.Subject, "", "reauth", "")
		failure(w, 403, "csrf_rejected")
		return
	}
	s.begin(w, r, v, true)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	f, ok := s.store.consumeFlow(r.URL.Query().Get("state"))
	c, err := r.Cookie(flowCookie)
	if !ok || err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(f.Cookie)) != 1 || f.Reauth != (r.URL.Path == "/oidc/reauth-callback") {
		failure(w, 401, "authentication_rejected")
		return
	}
	cookie(w, flowCookie, "", -1)
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		failure(w, 401, "authentication_rejected")
		return
	}
	a, err := s.oidc.exchange(r.Context(), f, r.URL.Query().Get("code"))
	if err != nil {
		failure(w, 401, "authentication_rejected")
		return
	}
	if _, err := s.oidc.account(r.Context(), a); err != nil {
		failure(w, 401, "authentication_rejected")
		return
	}
	p, err := s.directory.Person(a.Subject)
	if err != nil || p.Anchor != a.Subject || !p.canLogin() {
		failure(w, 401, "authentication_rejected")
		return
	}
	if f.Reauth {
		v, valid := s.session(r)
		if !valid || v.ID != f.Session || v.Subject != f.Subject || v.DirectoryGUID != p.GUID || !s.store.reauthenticate(v.ID, a) {
			failure(w, 401, "authentication_rejected")
			return
		}
	} else {
		if old, valid := s.session(r); valid {
			s.store.delete(old.ID)
		}
		expires := time.Now().Add(s.config.SessionTTL)
		if a.Expires.Before(expires) {
			expires = a.Expires
		}
		v := Session{ID: randomID(), CSRF: randomID(), DirectoryGUID: p.GUID, Authentication: a, Expires: expires}
		if !s.store.add(v) {
			failure(w, 503, "capacity_reached")
			return
		}
		cookie(w, sessionCookie, v.ID, int(time.Until(expires).Seconds()))
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) current(w http.ResponseWriter, r *http.Request) (Session, Person, IAMAccount, bool) {
	v, ok := s.session(r)
	if !ok || v.Anonymous {
		failure(w, 401, "authentication_required")
		return v, Person{}, IAMAccount{}, false
	}
	u, err := s.oidc.account(r.Context(), v.Authentication)
	if err != nil {
		if errors.Is(err, errDenied) {
			s.store.delete(v.ID)
			cookie(w, sessionCookie, "", -1)
			failure(w, 401, "authentication_required")
		} else {
			failure(w, 503, "identity_unavailable")
		}
		return v, Person{}, u, false
	}
	p, err := s.directory.Person(v.Subject)
	if err != nil || p.Anchor != v.Subject || p.GUID != v.DirectoryGUID || !p.canLogin() {
		if errors.Is(err, errDenied) || err == nil {
			s.store.delete(v.ID)
			cookie(w, sessionCookie, "", -1)
			failure(w, 401, "authentication_required")
		} else {
			failure(w, 503, "identity_unavailable")
		}
		return v, p, u, false
	}
	return v, p, u, true
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	v, ok := s.session(r)
	if !ok || !s.csrf(r, v) {
		failure(w, 403, "csrf_rejected")
		return
	}
	s.store.delete(v.ID)
	cookie(w, sessionCookie, "", -1)
	s.logoutResponse(w, !v.Anonymous || r.URL.Path == "/api/logout/complete")
}

func (s *Server) backchannel(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		failure(w, 400, "invalid_request")
		return
	}
	t, err := s.oidc.verifier.VerifyLogout(r.Context(), r.PostForm.Get("logout_token"))
	if err != nil || t.IssuedAt.After(time.Now().Add(30*time.Second)) || t.IssuedAt.Before(time.Now().Add(-2*time.Minute)) {
		failure(w, 401, "logout_rejected")
		return
	}
	if !s.store.revoke(t.Subject, t.SessionID, t.TokenID, time.Now().Add(3*time.Minute)) {
		failure(w, 409, "logout_rejected")
		return
	}
	respond(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	v, p, account, ok := s.current(w, r)
	if !ok {
		return
	}
	actions := []string{}
	for _, rule := range s.config.Rules {
		operator := false
		for _, g := range rule.OperatorGroups {
			operator = operator || p.inGroup(g)
		}
		if operator {
			for _, a := range rule.Actions {
				if !has(actions, a) {
					actions = append(actions, a)
				}
			}
		}
	}
	if s.administrator(p) {
		for _, a := range []string{"users.read", "profile.update", "password.reset", "account.unlock", "groups.member.add", "groups.member.remove", "audit.read"} {
			if !has(actions, a) {
				actions = append(actions, a)
			}
		}
		if s.creationEnabled() {
			actions = append(actions, "user.create")
		}
	}
	respond(w, 200, map[string]any{"person": localizePerson(p, r.URL.Query().Get("lang")), "preferences": s.visiblePreferences(s.preferences.get(p.Anchor), account, p), "csrf": v.CSRF, "actions": actions, "administration": s.administrator(p), "domain_administration": s.administrator(p) && s.config.DomainAdministration, "profile_editable": s.managedPerson(p) && s.config.ProfileBindDN != "" && s.config.ProfileBindPassword != "", "profile_version": profileVersion(p), "mail_domain": s.config.MailDomain, "recent_authentication": recent(v.AuthTime, s.config.RecentWindow, time.Now())})
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	_, person, account, ok := s.current(w, r)
	if !ok {
		return
	}
	lang := appcatalog.SelectLanguage(r.URL.Query().Get("lang"), r.Header.Get("Accept-Language"), s.config.Language)
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len(query) > 512 {
		failure(w, 400, "invalid_request")
		return
	}
	allowed := []appcatalog.Application{}
	for _, app := range s.catalog.Applications {
		if !applicationAllowed(s.catalog, app, account, person) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(app.Name+" "+app.DisplayName.Resolve(lang)+" "+app.Summary.Resolve(lang)), query) {
			continue
		}
		// Public icon addresses are authenticated portal routes, not the bundle path.
		icons := map[string]appcatalog.Icon{}
		for size, icon := range app.Icons {
			icon.Path = "/api/assets/" + app.Name + "/" + size + "?v=" + icon.SHA256
			icons[size] = icon
		}
		app.Icons = icons
		allowed = append(allowed, app)
	}
	respond(w, 200, appcatalog.BuildView(s.catalog.Categories, allowed, lang, r.URL.Query()["category"]))
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	_, p, u, ok := s.current(w, r)
	if !ok {
		return
	}
	for _, app := range s.catalog.Applications {
		if app.Name != r.PathValue("name") || !applicationAllowed(s.catalog, app, u, p) {
			continue
		}
		_, found := app.Icons[r.PathValue("size")]
		if !found {
			break
		}
		data, digest, err := s.themedIcon(app, s.chosenTheme(p, r.URL.Query().Get("theme")), r.PathValue("size"))
		if err != nil {
			failure(w, 503, "asset_unavailable")
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
		w.Header().Set("ETag", `"`+digest+`"`)
		w.WriteHeader(200)
		_, _ = w.Write(data)
		return
	}
	failure(w, 404, "not_found")
}

func pageRange(r *http.Request, length int) (int, int) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	if offset > length {
		offset = length
	}
	end := offset + limit
	if end > length {
		end = length
	}
	return offset, end
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	_, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	people, err := s.directory.People()
	if err != nil {
		failure(w, 503, "identity_unavailable")
		return
	}
	allowed := []Person{}
	q := strings.ToLower(r.URL.Query().Get("q"))
	if len(q) > 512 {
		failure(w, 400, "invalid_request")
		return
	}
	for _, p := range people {
		if !s.managedPerson(p) {
			continue
		}
		if !s.administrator(actor) && authorize(s.config.Rules, actor, p, "users.read", "") == "" {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Username+" "+p.DisplayName+" "+p.Email), q) {
			continue
		}
		allowed = append(allowed, p)
	}
	offset, end := pageRange(r, len(allowed))
	respond(w, 200, map[string]any{"count": len(allowed), "offset": offset, "users": allowed[offset:end]})
}

func (s *Server) targetActions(actor, target Person) []string {
	result := []string{}
	if s.administrator(actor) && !target.System && !has(s.config.ProtectedUsers, target.Anchor) {
		if target.DomainRisk && !s.config.DomainAdministration && target.Anchor != actor.Anchor {
			return result
		}
		result = append(result, "profile.update")
		if target.Anchor != actor.Anchor {
			result = append(result, "password.reset", "account.unlock")
		}
		return result
	}
	if target.Protected {
		return result
	}
	for _, a := range []string{"password.reset", "account.unlock"} {
		if authorize(s.config.Rules, actor, target, a, "") != "" {
			result = append(result, a)
		}
	}
	return result
}

func (s *Server) user(w http.ResponseWriter, r *http.Request) {
	_, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	target, err := s.directory.Person(r.PathValue("anchor"))
	if err != nil || !s.managedPerson(target) || !s.administrator(actor) && authorize(s.config.Rules, actor, target, "users.read", "") == "" {
		failure(w, 403, "access_denied")
		return
	}
	respond(w, 200, map[string]any{"person": localizePerson(target, r.URL.Query().Get("lang")), "actions": s.targetActions(actor, target), "profile_version": profileVersion(target), "requires_domain": target.DomainRisk, "administration": s.administrator(actor), "domain_administration": s.administrator(actor) && s.config.DomainAdministration})
}

func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	_, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	target, err := s.directory.Person(r.URL.Query().Get("target"))
	if err != nil || !s.managedPerson(target) || !s.administrator(actor) && authorize(s.config.Rules, actor, target, "users.read", "") == "" {
		failure(w, 403, "access_denied")
		return
	}
	if s.administrator(actor) {
		s.adminGroups(w, r, actor, target)
		return
	}
	result := []map[string]any{}
	seen := map[string]bool{}
	for _, rule := range s.config.Rules {
		for _, anchor := range rule.EditableGroups {
			if seen[anchor] {
				continue
			}
			seen[anchor] = true
			g, err := s.directory.Group(anchor)
			if err != nil {
				failure(w, 503, "identity_unavailable")
				return
			}
			add := !target.Protected && !g.Protected && authorize(s.config.Rules, actor, target, "groups.member.add", anchor) != ""
			remove := !target.Protected && !g.Protected && authorize(s.config.Rules, actor, target, "groups.member.remove", anchor) != ""
			if !add && !remove {
				continue
			}
			direct := false
			for _, m := range g.Members {
				direct = direct || equalDN(m, target.DN)
			}
			result = append(result, map[string]any{"group": g, "direct": direct, "inherited": target.inGroup(anchor) && !direct, "can_add": add && !direct, "can_remove": remove && direct})
		}
	}
	respond(w, 200, map[string]any{"groups": result})
}

func (s *Server) auditQuery(w http.ResponseWriter, r *http.Request) {
	_, actor, _, ok := s.current(w, r)
	if !ok {
		return
	}
	allowed := []Record{}
	permittedTargets := map[string]bool{}
	if s.administrator(actor) {
		people, err := s.directory.People()
		if err != nil {
			failure(w, 503, "identity_unavailable")
			return
		}
		for _, p := range people {
			permittedTargets[p.Anchor] = s.managedPerson(p)
		}
	}
	for _, rule := range s.config.Rules {
		if s.administrator(actor) {
			break
		}
		if !has(rule.Actions, "audit.read") {
			continue
		}
		operator := false
		for _, group := range rule.OperatorGroups {
			operator = operator || actor.inGroup(group)
		}
		if !operator {
			continue
		}
		people, err := s.directory.People()
		if err != nil {
			failure(w, 503, "identity_unavailable")
			return
		}
		for _, person := range people {
			permittedTargets[person.Anchor] = s.managedPerson(person) && authorize(s.config.Rules, actor, person, "audit.read", "") != ""
		}
		break
	}
	for _, record := range s.audit.list() {
		if record.Target == actor.Anchor || record.Target == "" && record.Actor == actor.Anchor || record.Action == "user.create" && record.Actor == actor.Anchor {
			allowed = append(allowed, record)
			continue
		}
		if record.Target == "" {
			continue
		}
		if permittedTargets[record.Target] {
			allowed = append(allowed, record)
		}
	}
	// Most recent records first; scope filtering precedes count and pagination.
	for i, j := 0, len(allowed)-1; i < j; i, j = i+1, j-1 {
		allowed[i], allowed[j] = allowed[j], allowed[i]
	}
	offset, end := pageRange(r, len(allowed))
	respond(w, 200, map[string]any{"count": len(allowed), "records": allowed[offset:end]})
}

func decode(r *http.Request, out any) bool {
	if mediaType(r) != "application/json" {
		return false
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return false
	}
	var extra any
	return d.Decode(&extra) == io.EOF
}

func mediaType(r *http.Request) string {
	value, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return value
}

func (s *Server) rejected(actor, target, action, rule string) {
	if !anchorPattern.MatchString(target) {
		target = ""
	}
	_ = s.audit.append(Record{ID: randomID(), Time: time.Now().UTC(), Phase: "result", Actor: actor, Target: target, Action: action, Rule: rule, Directory: "denied", ConfigDigest: s.digest})
}

// OperationResult preserves each step of a domain membership change.
type OperationResult struct {
	Directory  string
	Ownership  string
	Membership string
}

func (s *Server) write(w http.ResponseWriter, r *http.Request, record Record, operation func() string, temporaryPassword string, invalidate bool) {
	s.writeResult(w, r, record, func() OperationResult { return OperationResult{Directory: operation()} }, temporaryPassword, invalidate)
}

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, record Record, operation func() OperationResult, temporaryPassword string, invalidate bool) {
	record.ID = randomID()
	record.Time = time.Now().UTC()
	record.Phase = "intent"
	record.ConfigDigest = s.digest
	if err := s.audit.append(record); err != nil {
		respond(w, 503, map[string]string{"request_id": record.ID, "directory": "not_attempted", "audit": "unavailable"})
		return
	}
	details := operation()
	if record.Ownership != "" && details.Ownership == "" {
		details.Ownership, details.Membership = "not_attempted", "not_attempted"
	}
	outcome := details.Directory
	record.Ownership, record.Membership = details.Ownership, details.Membership
	record.Time = time.Now().UTC()
	record.Phase = "result"
	record.Directory = outcome
	record.IAM = "pending"
	auditStatus := "saved"
	if s.audit.append(record) != nil {
		auditStatus = "unavailable"
	}
	result := map[string]any{"request_id": record.ID, "directory": outcome, "audit": auditStatus, "iam": "pending", "external_sessions": "unverified"}
	if details.Ownership != "" {
		result["ownership"], result["membership"] = details.Ownership, details.Membership
	}
	if outcome == "saved" && temporaryPassword != "" {
		result["temporary_password"] = temporaryPassword
	}
	if record.Action == "user.create" {
		result["target"] = record.Target
	}
	if outcome == "saved" && invalidate {
		if v, ok := s.session(r); ok {
			s.store.delete(v.ID)
		}
		cookie(w, sessionCookie, "", -1)
		result["reauthenticate"] = true
	}
	status := 200
	if outcome != "saved" {
		status = 409
	}
	respond(w, status, result)
}

func (s *Server) clientIP(r *http.Request) string {
	// The outer handler admits only the exact Traefik peer. Traefik applies
	// the deployment's trusted-forwarder policy before emitting this chain.
	chain := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(chain) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return "unknown"
		}
		ip = ip.Unmap()
		trusted := false
		for _, prefix := range s.config.TrustedForwarders {
			trusted = trusted || prefix.Contains(ip)
		}
		if !trusted {
			return ip.String()
		}
	}
	return "unknown"
}

func (s *Server) passwordAttempt(r *http.Request, account string) bool {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(account))))
	return s.limiter.allow(hex.EncodeToString(h[:]), s.clientIP(r))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, person, _, ok := s.current(w, r)
	if !ok {
		s.rejected(v.Subject, v.Subject, "password.change", "")
		return
	}
	if !s.csrf(r, v) {
		s.rejected(v.Subject, v.Subject, "password.change", "")
		failure(w, 403, "csrf_rejected")
		return
	}
	var b struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
		Confirm     string `json:"confirm_password"`
	}
	if !decode(r, &b) || b.OldPassword == "" || b.NewPassword == "" || b.NewPassword != b.Confirm || len(b.NewPassword) > 1024 {
		s.rejected(v.Subject, v.Subject, "password.change", "")
		failure(w, 400, "invalid_request")
		return
	}
	if !s.passwordAttempt(r, person.Anchor) || !s.limiter.allowIdentity(person.Anchor) {
		s.rejected(v.Subject, v.Subject, "password.change", "")
		failure(w, 429, "rate_limited")
		return
	}
	s.write(w, r, Record{Actor: v.Subject, Target: v.Subject, Action: "password.change"}, func() string {
		if !s.validWriteSession(r.Context(), v.ID, v.Subject, false) {
			return "denied"
		}
		p, err := s.directory.Person(v.Subject)
		if err != nil || p.GUID != person.GUID || !p.canLogin() {
			return "denied"
		}
		return s.directory.ChangePassword(p, b.OldPassword, b.NewPassword)
	}, "", true)
}

func (s *Server) recoverPassword(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, ok := s.session(r)
	if !ok || !s.csrf(r, v) {
		s.rejected("", "", "password.recover", "")
		failure(w, 403, "csrf_rejected")
		return
	}
	var b struct {
		Username    string `json:"username"`
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
		Confirm     string `json:"confirm_password"`
	}
	if !decode(r, &b) || b.OldPassword == "" || b.NewPassword == "" || b.NewPassword != b.Confirm || len(b.NewPassword) > 1024 || len(b.Username) > 256 {
		s.rejected("", "", "password.recover", "")
		failure(w, 400, "password_change_rejected")
		return
	}
	if !s.passwordAttempt(r, b.Username) {
		s.rejected("", "", "password.recover", "")
		failure(w, 429, "rate_limited")
		return
	}
	p, err := s.directory.Login(b.Username)
	if err == nil && !s.limiter.allowIdentity(p.Anchor) {
		s.rejected("", "", "password.recover", "")
		// An alias that has not exhausted its submitted-label bucket must not
		// reveal whether lookup found an already limited directory identity.
		failure(w, 409, "password_change_rejected")
		return
	}
	if err != nil || p.Disabled || p.AccountExpired || !p.MustChangePassword && !p.PasswordExpired {
		s.rejected("", "", "password.recover", "")
		failure(w, 409, "password_change_rejected")
		return
	}
	if !v.Anonymous && v.Subject != p.Anchor {
		s.rejected(v.Subject, "", "password.recover", "")
		failure(w, 409, "password_change_rejected")
		return
	}
	record := Record{Actor: p.Anchor, Target: p.Anchor, Action: "password.recover", ID: randomID(), Time: time.Now().UTC(), Phase: "intent", ConfigDigest: s.digest}
	if s.audit.append(record) != nil {
		failure(w, 409, "password_change_rejected")
		return
	}
	fresh, err := s.directory.Person(p.Anchor)
	outcome := "denied"
	if err == nil && fresh.GUID == p.GUID && !fresh.Disabled && !fresh.AccountExpired && (fresh.MustChangePassword || fresh.PasswordExpired) {
		outcome = s.directory.ChangePassword(fresh, b.OldPassword, b.NewPassword)
	}
	record.Phase = "result"
	record.Directory = outcome
	record.Time = time.Now().UTC()
	auditOK := s.audit.append(record) == nil
	if outcome == "saved" {
		s.store.delete(v.ID)
		cookie(w, sessionCookie, "", -1)
		respond(w, 200, map[string]any{"directory": "saved", "audit": auditOK, "reauthenticate": true})
		return
	}
	// Public failures share one unconfirmed response, including network-unknown
	// outcomes. Neither the HTTP status nor its shape reveals account existence.
	failure(w, 409, "password_change_rejected")
}

func (s *Server) adminGuard(w http.ResponseWriter, r *http.Request, targetAnchor, action, group string) (Session, Person, Person, string, bool) {
	v, actor, _, ok := s.current(w, r)
	if !ok {
		s.rejected("", targetAnchor, action, "")
		return v, actor, Person{}, "", false
	}
	if !s.csrf(r, v) {
		s.rejected(v.Subject, targetAnchor, action, "")
		failure(w, 403, "csrf_rejected")
		return v, actor, Person{}, "", false
	}
	target, err := s.directory.Person(targetAnchor)
	rule := authorize(s.config.Rules, actor, target, action, group)
	if err != nil || !s.managedPerson(target) || target.Protected || rule == "" {
		s.rejected(v.Subject, targetAnchor, action, rule)
		failure(w, 403, "access_denied")
		return v, actor, target, rule, false
	}
	if !recent(v.AuthTime, s.config.RecentWindow, time.Now()) {
		s.rejected(v.Subject, targetAnchor, action, rule)
		failure(w, 403, "recent_authentication_required")
		return v, actor, target, rule, false
	}
	return v, actor, target, rule, true
}

func (s *Server) validWriteSession(ctx context.Context, id, subject string, needRecent bool) bool {
	v, ok := s.store.get(id)
	if !ok || v.Anonymous || v.Subject != subject || needRecent && !recent(v.AuthTime, s.config.RecentWindow, time.Now()) {
		return false
	}
	_, err := s.oidc.account(ctx, v.Authentication)
	return err == nil
}

func (s *Server) freshWrite(ctx context.Context, id string, actor, target Person, action, group, rule string) (Person, bool) {
	if !s.validWriteSession(ctx, id, actor.Anchor, true) {
		return Person{}, false
	}
	freshActor, err := s.directory.Person(actor.Anchor)
	if err != nil || freshActor.GUID != actor.GUID || !freshActor.canLogin() {
		return Person{}, false
	}
	fresh, err := s.directory.Person(target.Anchor)
	return fresh, err == nil && fresh.GUID == target.GUID && !fresh.Protected && authorize(s.config.Rules, freshActor, fresh, action, group) == rule
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, actor, target, rule, ok := s.adminGuard(w, r, r.PathValue("anchor"), "password.reset", "")
	if !ok {
		return
	}
	var b struct {
		Force bool `json:"force_change"`
	}
	if !decode(r, &b) {
		s.rejected(v.Subject, target.Anchor, "password.reset", rule)
		failure(w, 400, "invalid_request")
		return
	}
	if !s.passwordAttempt(r, target.Anchor) || !s.limiter.allowIdentity(target.Anchor) {
		s.rejected(v.Subject, target.Anchor, "password.reset", rule)
		failure(w, 429, "rate_limited")
		return
	}
	password := "Aa1!" + randomID()[:28]
	s.write(w, r, Record{Actor: v.Subject, Target: target.Anchor, Action: "password.reset", Rule: rule, ForceChange: &b.Force}, func() string {
		p, ok := s.freshWrite(r.Context(), v.ID, actor, target, "password.reset", "", rule)
		if !ok {
			return "denied"
		}
		return s.directory.ResetPassword(p, password, b.Force)
	}, password, false)
}

func (s *Server) unlock(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	v, actor, target, rule, ok := s.adminGuard(w, r, r.PathValue("anchor"), "account.unlock", "")
	if !ok {
		return
	}
	var b struct {
	}
	if !decode(r, &b) {
		s.rejected(v.Subject, target.Anchor, "account.unlock", rule)
		failure(w, 400, "invalid_request")
		return
	}
	s.write(w, r, Record{Actor: v.Subject, Target: target.Anchor, Action: "account.unlock", Rule: rule}, func() string {
		p, ok := s.freshWrite(r.Context(), v.ID, actor, target, "account.unlock", "", rule)
		if !ok {
			return "denied"
		}
		return s.directory.Unlock(p)
	}, "", false)
}

func (s *Server) member(w http.ResponseWriter, r *http.Request) {
	s.writes.Lock()
	defer s.writes.Unlock()
	var b struct {
		Target         string `json:"target"`
		Add            bool   `json:"add"`
		ExpectedDirect bool   `json:"expected_direct"`
	}
	if !decode(r, &b) {
		v, _ := s.session(r)
		s.rejected(v.Subject, "", "groups.member", "")
		failure(w, 400, "invalid_request")
		return
	}
	action := "groups.member.remove"
	if b.Add {
		action = "groups.member.add"
	}
	anchor := r.PathValue("anchor")
	v, actor, target, rule, ok := s.adminGuard(w, r, b.Target, action, anchor)
	if !ok {
		return
	}
	g, err := s.directory.Group(anchor)
	if err != nil || g.Protected {
		s.rejected(v.Subject, target.Anchor, action, rule)
		failure(w, 403, "access_denied")
		return
	}
	s.write(w, r, Record{Actor: v.Subject, Target: target.Anchor, Action: action, Rule: rule, Group: anchor, ExpectedDirect: &b.ExpectedDirect}, func() string {
		p, ok := s.freshWrite(r.Context(), v.ID, actor, target, action, anchor, rule)
		if !ok {
			return "denied"
		}
		fresh, err := s.directory.Group(anchor)
		if err != nil || fresh.GUID != g.GUID || fresh.Protected {
			return "denied"
		}
		direct := false
		for _, m := range fresh.Members {
			direct = direct || equalDN(m, p.DN)
		}
		if direct != b.ExpectedDirect || direct == b.Add {
			return "conflict"
		}
		return s.directory.Member(fresh, p, b.Add)
	}, "", false)
}
