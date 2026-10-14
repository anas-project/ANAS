// TEST_CASES: PORTAL-T-001
package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

type fakeDirectory struct {
	people      map[string]Person
	groups      map[string]Group
	writes      int
	result      string
	beforeWrite func()
}

func (d *fakeDirectory) Person(a string) (Person, error) {
	p, ok := d.people[a]
	if !ok {
		return p, errDenied
	}
	return p, nil
}
func (d *fakeDirectory) Login(a string) (Person, error) {
	for _, p := range d.people {
		if p.Username == a {
			return p, nil
		}
	}
	return Person{}, errDenied
}
func (d *fakeDirectory) People() ([]Person, error) {
	p := []Person{}
	for _, v := range d.people {
		p = append(p, v)
	}
	return p, nil
}
func (d *fakeDirectory) Group(a string) (Group, error) {
	g, ok := d.groups[a]
	if !ok {
		return g, errDenied
	}
	return g, nil
}
func (d *fakeDirectory) do() string {
	d.writes++
	if d.beforeWrite != nil {
		d.beforeWrite()
	}
	if d.result == "" {
		return "saved"
	}
	return d.result
}
func (d *fakeDirectory) ChangePassword(Person, string, string) string { return d.do() }
func (d *fakeDirectory) ResetPassword(Person, string, bool) string    { return d.do() }
func (d *fakeDirectory) Unlock(Person) string                         { return d.do() }
func (d *fakeDirectory) Member(Group, Person, bool) string            { return d.do() }

type fixture struct {
	server     *Server
	directory  *fakeDirectory
	iam        *httptest.Server
	session    Session
	person     Person
	permission Permission
	key        *rsa.PrivateKey
	claims     map[string]any
	seenForm   map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.key = key
	f.person = Person{PasswordStateKnown: true, Anchor: "actor-anchor", Username: "alice", DN: "CN=Alice,OU=People,DC=example", GUID: "actor-guid", Groups: []Group{{Anchor: "operator-group", Name: "Operators", Direct: true}, {Anchor: "app-group", Name: "Apps", Direct: true}}, KnownGroups: map[string]string{"Operators": "operator-group", "Apps": "app-group"}}
	f.directory = &fakeDirectory{people: map[string]Person{f.person.Anchor: f.person, "target-anchor": {Anchor: "target-anchor", Username: "bob", DN: "CN=Bob,OU=People,DC=example", GUID: "target-guid", Groups: []Group{{Anchor: "scope-group", Name: "Scope", Direct: true}}}}, groups: map[string]Group{"editable-group": {Anchor: "editable-group", Name: "Editable", GUID: "editable-guid"}}}
	f.permission = Permission{Owner: "anas", Name: "permission-app-anas-catalog-tool", Model: "built-in/user-model-built-in", State: "Approved", Effect: "Allow", ResourceType: "Application", Groups: []string{"anas/Apps"}, Resources: []string{"app-anas-catalog-tool"}, Actions: []string{"Read"}, IsEnabled: true}
	f.iam = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": f.iam.URL, "authorization_endpoint": f.iam.URL + "/authorize", "token_endpoint": f.iam.URL + "/token", "userinfo_endpoint": f.iam.URL + "/api/userinfo", "jwks_uri": f.iam.URL + "/jwks", "end_session_endpoint": f.iam.URL + "/api/logout", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
		case "/api/userinfo":
			json.NewEncoder(w).Encode(map[string]any{"sub": "actor-anchor", "groups": []string{"anas/Operators", "anas/Apps"}})
		case "/api/get-account":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": map[string]any{"owner": "anas", "externalId": "actor-anchor", "isDeleted": false, "isForbidden": false, "groups": []string{"anas/Operators", "anas/Apps"}, "permissions": []Permission{f.permission}, "accessToken": "discard-this-extra-token"}})
		case "/token":
			r.ParseForm()
			f.seenForm = map[string]string{}
			for k := range r.Form {
				f.seenForm[k] = r.Form.Get(k)
			}
			json.NewEncoder(w).Encode(map[string]string{"access_token": "backend-access-token", "id_token": signToken(t, key, f.claims), "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.iam.Close)
	c := Config{URL: "https://portal.example", Issuer: f.iam.URL, ClientID: "portal", ReauthClientID: "portal-reauth", RecentWindow: 5 * time.Minute, SessionTTL: time.Hour, AuditDir: filepath.Join(t.TempDir(), "audit"), AuditMaxBytes: 1 << 20, Rules: []Rule{{ID: "helpdesk", OperatorGroups: []string{"operator-group"}, Actions: []string{"users.read", "password.reset", "account.unlock", "audit.read", "groups.member.add", "groups.member.remove"}, TargetUserGroups: []string{"scope-group"}, EditableGroups: []string{"editable-group"}}}}
	o, err := newOIDC(context.Background(), c, f.iam.Client())
	if err != nil {
		t.Fatal(err)
	}
	j, err := openJournal(c.AuditDir, c.AuditMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(j.close)
	f.server = &Server{config: c, oidc: o, directory: f.directory, audit: j, store: newStore(), limiter: newLimiter(), peers: []netip.Addr{netip.MustParseAddr("127.0.0.1")}, catalog: appcatalog.Catalog{Schema: appcatalog.Schema, Categories: []appcatalog.Category{{ID: "other", DisplayName: appcatalog.Text{"en": "Other", "zh-CN": "其他"}}}, Applications: []appcatalog.Application{{Metadata: appcatalog.Metadata{Name: "tool", DisplayName: appcatalog.Text{"en": "Tool", "zh-CN": "工具"}, Summary: appcatalog.Text{"en": "Useful", "zh-CN": "好用"}, Kind: "user", Categories: []string{"other"}}, Enabled: true, Groups: []string{"Apps"}, Icons: map[string]appcatalog.Icon{}}}}}
	f.session = Session{ID: "session", CSRF: "csrf", DirectoryGUID: f.person.GUID, Authentication: Authentication{Subject: f.person.Anchor, SID: "sid-a", AccessToken: "backend-access-token", AuthTime: time.Now()}, Expires: time.Now().Add(time.Hour)}
	f.server.store.add(f.session)
	return f
}

func signToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test"})
	payload, _ := json.Marshal(claims)
	s := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	h := sha256.Sum256([]byte(s))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return s + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (f *fixture) request(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1000"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session.ID})
	r.Header.Set("Origin", "https://portal.example")
	r.Header.Set("X-CSRF-Token", f.session.CSRF)
	r.Header.Set("X-Forwarded-For", "198.51.100.1")
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(w, r)
	return w
}

func TestPermissionUsesCurrentDirectoryAndExactPolicy(t *testing.T) {
	f := newFixture(t)
	u := IAMAccount{Permissions: []Permission{f.permission}, Groups: []string{"anas/Apps"}}
	app := f.server.catalog.Applications[0]
	if !applicationAllowed(f.server.catalog, app, u, f.person) {
		t.Fatal("valid binding denied")
	}
	for _, change := range []func(*Permission){func(p *Permission) { p.IsEnabled = false }, func(p *Permission) { p.State = "Pending" }, func(p *Permission) { p.Model = "" }, func(p *Permission) { p.Users = []string{"anas/alice"} }, func(p *Permission) { p.Actions = []string{"Write"} }} {
		p := f.permission
		change(&p)
		u.Permissions = []Permission{p}
		if applicationAllowed(f.server.catalog, app, u, f.person) {
			t.Fatal("invalid policy accepted")
		}
	}
	u.Permissions = []Permission{f.permission}
	p := f.person
	p.Groups = nil
	if applicationAllowed(f.server.catalog, app, u, p) {
		t.Fatal("old IAM group bypassed current AD revocation")
	}
	p = f.person
	p.KnownGroups = map[string]string{}
	if applicationAllowed(f.server.catalog, app, u, p) {
		t.Fatal("unknown directory group accepted")
	}
	app.Hidden = true
	if applicationAllowed(f.server.catalog, app, u, f.person) {
		t.Fatal("hidden app accepted")
	}
}

func TestSearchLanguagePrivacyAndLiveRevocation(t *testing.T) {
	f := newFixture(t)
	r := f.request("GET", "/api/apps?lang=fr&q=Useful", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"language":"en"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	if strings.Contains(r.Body.String(), "backend-access-token") || strings.Contains(r.Body.String(), "permission-") {
		t.Fatal("private IAM fields leaked")
	}
	p := f.person
	p.Groups = []Group{{Anchor: "operator-group", Name: "Operators"}}
	f.directory.people[p.Anchor] = p
	r = f.request("GET", "/api/apps?lang=zh-CN", "")
	if r.Code != 200 || strings.Contains(r.Body.String(), "工具") {
		t.Fatal("revoked app remained visible")
	}
	r = f.request("GET", "/api/assets/tool/48", "")
	if r.Code != 404 {
		t.Fatal("revoked icon exposed", r.Code)
	}
}

func TestDelegationDoesNotCombineRulesOrProtectTargetScopePeople(t *testing.T) {
	f := newFixture(t)
	target := f.directory.people["target-anchor"]
	rules := []Rule{{ID: "read", OperatorGroups: []string{"operator-group"}, Actions: []string{"users.read"}, TargetUsers: []string{target.Anchor}}, {ID: "write", OperatorGroups: []string{"other-group"}, Actions: []string{"password.reset"}, TargetUsers: []string{target.Anchor}}}
	if authorize(rules, f.person, target, "password.reset", "") != "" {
		t.Fatal("rules combined")
	}
	d := LDAPDirectory{config: f.server.config}
	g := Group{Anchor: "scope-group"}
	if !d.protectedGroup(g) || d.privilegedGroup(g) {
		t.Fatal("target scope group must be immutable without making every target an administrator")
	}
}

func TestAuditIntentFailurePreventsWriteAndResultFailureClosesWrites(t *testing.T) {
	f := newFixture(t)
	f.server.audit.maxBytes = 1
	r := f.request("POST", "/api/users/target-anchor/reset", `{"reason":"support","force_change":false}`)
	if r.Code != 503 || f.directory.writes != 0 {
		t.Fatal("write issued without audit intent")
	}
	f.server.audit.broken = false
	f.server.audit.maxBytes = 1 << 20
	f.directory.beforeWrite = func() { f.server.audit.mu.Lock(); f.server.audit.broken = true; f.server.audit.mu.Unlock() }
	r = f.request("POST", "/api/users/target-anchor/reset", `{"reason":"support","force_change":false}`)
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"directory":"saved"`) || !strings.Contains(r.Body.String(), `"audit":"unavailable"`) {
		t.Fatal(r.Code, r.Body.String())
	}
	r = f.request("POST", "/api/users/target-anchor/reset", `{"reason":"support","force_change":false}`)
	if r.Code != 503 || f.directory.writes != 1 {
		t.Fatal("writes continued after result failure")
	}
}

func TestProtectedRecentCSRFAndUnknownFieldsRejectWrites(t *testing.T) {
	f := newFixture(t)
	p := f.directory.people["target-anchor"]
	p.Protected = true
	f.directory.people[p.Anchor] = p
	if f.request("POST", "/api/users/target-anchor/reset", `{"reason":"support"}`).Code != 403 {
		t.Fatal("protected user allowed")
	}
	p.Protected = false
	f.directory.people[p.Anchor] = p
	v := f.session
	v.AuthTime = time.Now().Add(-time.Hour)
	f.server.store.sessions[v.ID] = v
	if f.request("POST", "/api/users/target-anchor/reset", `{"reason":"support"}`).Code != 403 {
		t.Fatal("expired authentication allowed")
	}
	f.server.store.sessions[f.session.ID] = f.session
	if f.request("POST", "/api/users/target-anchor/reset", `{"reason":"support","dn":"CN=Someone"}`).Code != 400 {
		t.Fatal("arbitrary DN accepted")
	}
	r := httptest.NewRequest("POST", "https://portal.example/api/users/target-anchor/reset", strings.NewReader(`{"reason":"support"}`))
	r.RemoteAddr = "127.0.0.1:999"
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session.ID})
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(w, r)
	if w.Code != 403 || f.directory.writes != 0 {
		t.Fatal("unsafe request wrote LDAP")
	}
}

func TestUnknownWritesDoNotReplayOrPersistPasswords(t *testing.T) {
	f := newFixture(t)
	f.directory.result = "unknown"
	r := f.request("POST", "/api/password/change", `{"old_password":"OldSecret123!","new_password":"NewSecret123!","confirm_password":"NewSecret123!"}`)
	if r.Code != 409 || f.directory.writes != 1 {
		t.Fatal(r.Code, f.directory.writes)
	}
	data, err := os.ReadFile(f.server.audit.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"OldSecret123!", "NewSecret123!", "backend-access-token"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("secret persisted")
		}
	}
	f.server.audit.close()
	j, err := openJournal(f.server.config.AuditDir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer j.close()
	if f.directory.writes != 1 {
		t.Fatal("restart replayed LDAP")
	}
}

func TestLogoutInvalidatesLocalSessionWithoutGivingBrowserToken(t *testing.T) {
	f := newFixture(t)
	r := f.request("POST", "/api/logout", `{}`)
	if r.Code != 200 || strings.Contains(r.Body.String(), "backend-access-token") || strings.Contains(r.Body.String(), "id_token_hint") {
		t.Fatal("logout token leaked")
	}
	if f.request("GET", "/api/me", "").Code != 401 {
		t.Fatal("old portal cookie still authenticated")
	}
}

func TestOIDCExchangeBindingAndRecentIdentity(t *testing.T) {
	f := newFixture(t)
	flow := Flow{Nonce: "nonce", Verifier: "verifier", Subject: f.person.Anchor, Reauth: true}
	base := map[string]any{"iss": f.iam.URL, "aud": "portal-reauth", "sub": f.person.Anchor, "nonce": "nonce", "auth_time": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	for _, change := range []map[string]any{{}, {"nonce": "wrong"}, {"sub": "another-anchor"}, {"auth_time": time.Now().Add(-time.Hour).Unix()}, {"auth_time": nil}, {"iss": "https://other.example"}, {"aud": "other-client"}, {"exp": time.Now().Add(-time.Hour).Unix()}} {
		claims := map[string]any{}
		for k, v := range base {
			claims[k] = v
		}
		for k, v := range change {
			claims[k] = v
		}
		f.claims = claims
		_, err := f.server.oidc.exchange(context.Background(), flow, "code")
		if (err == nil) != (len(change) == 0) {
			t.Fatal("wrong OIDC acceptance", change, err)
		}
		if f.seenForm["code_verifier"] != "verifier" || f.seenForm["client_secret"] != "" {
			t.Fatal("PKCE/secret contract broken")
		}
	}
}

func TestJournalIncompleteAndCorruptRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "audit")
	j, err := openJournal(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.append(Record{ID: "intent", Phase: "intent", Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	j.close()
	j, err = openJournal(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if j.list()[0].Directory != "unknown" {
		t.Fatal("incomplete intent lost")
	}
	j.close()
	f, err := os.OpenFile(filepath.Join(root, "audit.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"fragment"`)
	f.Close()
	j, err = openJournal(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer j.close()
	if j.append(Record{ID: "later", Phase: "intent"}) == nil {
		t.Fatal("corrupt journal allowed a write")
	}
}

func TestBackchannelSignatureSessionScopeAndReplay(t *testing.T) {
	f := newFixture(t)
	other := f.session
	other.ID = "other-session"
	other.SID = "sid-b"
	f.server.store.add(other)
	claims := map[string]any{"iss": f.iam.URL, "aud": "portal", "sub": f.person.Anchor, "sid": "sid-a", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "jti": "logout-1", "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
	token := signToken(t, f.key, claims)
	send := func(token string) int {
		r := httptest.NewRequest("POST", "https://portal.example/oidc/backchannel-logout", strings.NewReader("logout_token="+token))
		r.RemoteAddr = "127.0.0.1:1000"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if send(token) != 200 {
		t.Fatal("valid native back-channel token rejected")
	}
	if _, ok := f.server.store.get(f.session.ID); ok {
		t.Fatal("matching session retained")
	}
	if _, ok := f.server.store.get(other.ID); !ok {
		t.Fatal("another session revoked")
	}
	if send(token) != 409 {
		t.Fatal("logout token replay accepted")
	}
	claims["jti"] = "logout-2"
	claims["nonce"] = "not-permitted"
	if send(signToken(t, f.key, claims)) != 401 {
		t.Fatal("nonce-bearing logout token accepted")
	}
}

func TestSameSiteOriginsAndTrustedClientIP(t *testing.T) {
	if !sameSite("portal.example.co.uk", "auth.example.co.uk") || sameSite("portal.example.co.uk", "auth.other.co.uk") || sameSite("10.0.0.1", "10.0.0.2") {
		t.Fatal("incorrect same-site classification")
	}
	if !equivalentOrigin("https://portal.example:443", "https://portal.example") || equivalentOrigin("https://portal.example.evil", "https://portal.example") {
		t.Fatal("incorrect origin comparison")
	}
	f := newFixture(t)
	f.server.config.TrustedForwarders = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	r := httptest.NewRequest("POST", "https://portal.example/api/password/change", nil)
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.7, 10.0.0.2")
	if f.server.clientIP(r) != "198.51.100.7" {
		t.Fatal("forged leftmost client address trusted")
	}
}

func TestPasswordLimitsCoverAliasesAndNetwork(t *testing.T) {
	limiter := newLimiter()
	for i := 0; i < 5; i++ {
		if !limiter.allow("submitted-alias-"+string(rune('a'+i)), "198.51.100.1") || !limiter.allowIdentity("same-anchor") {
			t.Fatal("legitimate first attempts refused")
		}
	}
	if limiter.allowIdentity("same-anchor") {
		t.Fatal("username/UPN aliases bypassed stable account limit")
	}
	if !limiter.allowIdentity("another-anchor") {
		t.Fatal("separate account unexpectedly limited")
	}
	for i := 0; i < 15; i++ {
		if !limiter.allow("different-"+string(rune('a'+i)), "198.51.100.1") {
			t.Fatal("network limit reached early")
		}
	}
	if limiter.allow("new-account", "198.51.100.1") {
		t.Fatal("IP limit bypassed")
	}
	if !limiter.allow("new-account", "198.51.100.2") {
		t.Fatal("independent IP refused")
	}
}

func TestPublicRecoveryIdentityLimitDoesNotDiscloseAccount(t *testing.T) {
	f := newFixture(t)
	f.session.Anonymous = true
	f.session.Authentication = Authentication{}
	f.server.store.add(f.session)
	p := f.person
	p.MustChangePassword = true
	f.directory.people[p.Anchor] = p
	for i := 0; i < 5; i++ {
		if !f.server.limiter.allowIdentity(p.Anchor) {
			t.Fatal("stable account limit reached early")
		}
	}
	response := func(username string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"username": username, "old_password": "wrong", "new_password": "Aa1!new-password", "confirm_password": "Aa1!new-password"})
		return f.request("POST", "/api/password/recover", string(body))
	}
	known, missing := response(p.Username), response("nonexistent")
	if known.Code != missing.Code || known.Body.String() != missing.Body.String() || known.Code != 409 || f.directory.writes != 0 {
		t.Fatal("stable identity limit disclosed account existence or attempted a write")
	}
	// Submitted labels are limited before lookup for every account, including
	// nonexistent ones; neither path reaches password verification.
	for _, username := range []string{p.Username, "nonexistent"} {
		for i := 0; i < 4; i++ {
			if response(username).Code != 409 {
				t.Fatal("submitted-label limit reached early")
			}
		}
		if response(username).Code != 429 {
			t.Fatal("submitted-label rate limit was not enforced")
		}
	}
}

func TestInvalidMemberRequestAuditsAuthenticatedActor(t *testing.T) {
	f := newFixture(t)
	if f.request("POST", "/api/groups/editable-group/member", `{"unexpected":true}`).Code != 400 || f.directory.writes != 0 {
		t.Fatal("invalid member request was not rejected")
	}
	records := f.server.audit.list()
	if len(records) != 1 || records[0].Actor != f.person.Anchor || records[0].Directory != "denied" {
		t.Fatal("invalid member request omitted the authenticated actor")
	}
}

func TestRecreatedIdentityCannotReplaceSessionAndEmptyDelegation(t *testing.T) {
	f := newFixture(t)
	f.server.config.Rules = nil
	r := f.request("GET", "/api/me", "")
	if r.Code != 200 || !strings.Contains(r.Body.String(), `"actions":[]`) {
		t.Fatal("missing rules granted actions")
	}
	if f.request("POST", "/api/users/target-anchor/reset", `{"reason":"test"}`).Code != 403 || f.directory.writes != 0 {
		t.Fatal("missing delegation allowed reset")
	}
	p := f.person
	p.GUID = "recreated-guid"
	f.directory.people[p.Anchor] = p
	if f.request("GET", "/api/me", "").Code != 401 {
		t.Fatal("reused anchor replaced original directory object")
	}
	if _, ok := f.server.store.get(f.session.ID); ok {
		t.Fatal("identity reuse retained session")
	}
}

func TestFormOriginAndMediaTypeRemainStrict(t *testing.T) {
	f := newFixture(t)
	send := func(origin, body, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://portal.example"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:999"
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: f.session.ID})
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		w := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(w, r)
		return w
	}
	if send("null", "csrf=csrf", "/api/reauth").Code != 403 {
		t.Fatal("opaque form origin accepted")
	}
	if send("https://portal.example", "", "/api/reauth?csrf=csrf").Code != 403 {
		t.Fatal("CSRF accepted from URL")
	}
	response := send("https://portal.example", "csrf=csrf", "/api/reauth")
	if response.Code != 303 || response.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("native same-origin form blocked")
	}
}

func TestUnconfirmedAndLockedDirectoryStateCannotUseOldSession(t *testing.T) {
	for _, alter := range []func(*Person){func(p *Person) { p.Locked = true }, func(p *Person) { p.PasswordStateKnown = false }} {
		f := newFixture(t)
		p := f.person
		alter(&p)
		f.directory.people[p.Anchor] = p
		if f.request("GET", "/api/me", "").Code != 401 {
			t.Fatal("inactive or unknown directory state retained access")
		}
	}
}

func TestDirectoryTLSRejectsWrongTrustAndName(t *testing.T) {
	f := newFixture(t)
	url := strings.Replace(f.iam.URL, "https://", "ldaps://", 1)
	for _, cfg := range []*tls.Config{{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}, {RootCAs: f.iam.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, ServerName: "wrong.example.invalid", MinVersion: tls.VersionTLS12}} {
		d := &LDAPDirectory{config: Config{LDAPURL: url}, tls: cfg}
		conn, err := d.dial("CN=fixture", "only-test")
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatal("unverified directory TLS accepted")
		}
	}
	d := &LDAPDirectory{config: Config{LDAPURL: "ldap://127.0.0.1:1"}}
	if _, err := d.dial("fixture", "only-test"); err == nil {
		t.Fatal("plaintext directory fallback")
	}
}
