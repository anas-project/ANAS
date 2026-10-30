// TEST_CASES: MAILPOC-T-004, MAILPOC-T-005
// Isolated feasibility fixture. Not a deployable mail Module.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-ldap/ldap/v3"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

const origin = "https://manage.mailtest.invalid:18884"
const webmail = "https://webmail.mailtest.invalid:18884/"
const issuer = "https://auth.mailtest.invalid:18884/"

var ctx = context.Background()
var db *pgxpool.Pool
var secret map[string]string
var tlsConfig *tls.Config
var oauth oauth2.Config
var verifier *oidc.IDTokenVerifier
var mutation sync.Mutex

func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	must(json.NewEncoder(w).Encode(v))
}
func deny(w http.ResponseWriter) { http.Error(w, "Access denied", 403) }
func cookie(w http.ResponseWriter, n, v string, max int) {
	http.SetCookie(w, &http.Cookie{Name: n, Value: v, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: max})
}
func value(r *http.Request, n string) string {
	c, e := r.Cookie(n)
	if e != nil {
		return ""
	}
	return c.Value
}
func exec(q string, a ...any) { _, e := db.Exec(ctx, q, a...); must(e) }
func service(r *http.Request, kind string) bool {
	u, p, ok := r.BasicAuth()
	return ok && u == kind && hash(p) == hash(secret[kind])
}
func session(r *http.Request) (sid, owner string) {
	sid = hash(value(r, "mailgo"))
	e := db.QueryRow(ctx, "SELECT s.owner FROM sessions s JOIN people p ON p.anchor=s.owner WHERE s.id=$1 AND s.expires>now() AND p.allowed", sid).Scan(&owner)
	if e != nil {
		return "", ""
	}
	return
}
func csrf(r *http.Request) bool {
	return r.Method == "POST" && r.Header.Get("Origin") == origin && r.FormValue("csrf") == hash(value(r, "mailgo")+"csrf")
}
func owned(owner, box string) bool {
	var ok bool
	_ = db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mailboxes m JOIN people p ON p.anchor=m.owner WHERE m.id=$1 AND m.owner=$2 AND m.enabled AND p.allowed)", box, owner).Scan(&ok)
	return ok
}

func directory() *ldap.Conn {
	c, e := ldap.DialURL("ldaps://dc.mailtest.invalid:636", ldap.DialWithTLSConfig(tlsConfig))
	must(e)
	// Fixture-only administrator on a newly provisioned synthetic AD. Production
	// must use the existing directory provider and a narrowly delegated account.
	must(c.Bind("CN=Administrator,CN=Users,DC=mailtest,DC=invalid", secret["admin"]))
	return c
}
func person(uid string) (anchor, dn string, addresses []string) {
	c := directory()
	defer c.Close()
	rows, e := c.Search(ldap.NewSearchRequest("DC=mailtest,DC=invalid", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 5, false, "(&(objectClass=user)(sAMAccountName="+ldap.EscapeFilter(uid)+"))", []string{"anasIdentityAnchor", "mail", "proxyAddresses"}, nil))
	must(e)
	if len(rows.Entries) != 1 {
		panic("fixture user absent")
	}
	p := rows.Entries[0]
	anchor = p.GetAttributeValue("anasIdentityAnchor")
	dn = p.DN
	addresses = append(addresses, p.GetAttributeValue("mail"))
	for _, a := range p.GetAttributeValues("proxyAddresses") {
		if strings.HasPrefix(strings.ToLower(a), "smtp:") {
			addresses = append(addresses, strings.ToLower(a[5:]))
		}
	}
	return
}
func routes() {
	rows, e := db.Query(ctx, "SELECT a.address,m.login FROM addresses a JOIN mailboxes m ON m.id=a.box WHERE a.active AND m.enabled ORDER BY a.address")
	must(e)
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var a, l string
		must(rows.Scan(&a, &l))
		fmt.Fprintf(&b, "%s %s\n", a, l)
	}
	must(rows.Err())
	if current, err := os.ReadFile("/dms/postfix-virtual.cf"); err == nil && string(current) == b.String() {
		return
	}
	must(os.WriteFile("/dms/postfix-virtual.cf.new", []byte(b.String()), 0600))
	must(os.Rename("/dms/postfix-virtual.cf.new", "/dms/postfix-virtual.cf"))
}
func credential(sid, box string) map[string]any {
	var login, address string
	must(db.QueryRow(ctx, "SELECT m.login,m.address FROM mailboxes m JOIN sessions s ON s.owner=m.owner WHERE s.id=$1 AND s.expires>now() AND m.id=$2 AND m.enabled", sid, box).Scan(&login, &address))
	access, refresh, smtpToken := random(), random(), random()
	smtpLogin := "smtp-" + hash(smtpToken)[:32] + "@boxes.mailtest.invalid"
	exec("INSERT INTO tokens(hash,sid,box,kind,expires) VALUES($1,$2,$3,'access',now()+interval '90 seconds'),($4,$2,$3,'refresh',now()+interval '15 minutes')", hash(access), sid, box, hash(refresh))
	exec("INSERT INTO tokens(hash,sid,box,kind,binding,expires) VALUES($1,$2,$3,'smtp',$4,now()+interval '90 seconds')", hash(smtpToken), sid, box, smtpLogin)
	rows, e := db.Query(ctx, "SELECT address FROM addresses WHERE box=$1 AND active ORDER BY address", box)
	must(e)
	defer rows.Close()
	aliases := []string{}
	for rows.Next() {
		var a string
		must(rows.Scan(&a))
		aliases = append(aliases, a)
	}
	return map[string]any{"access_token": access, "smtp_token": smtpToken, "smtp_username": smtpLogin, "session_ref": sid, "refresh_token": refresh, "expires_at": time.Now().Add(90 * time.Second).Unix(), "username": login, "address": address, "box": box, "addresses": aliases}
}
func internal(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/internal/session" || strings.HasPrefix(r.URL.Path, "/internal/addressbooks") {
		directoryAPI(w, r)
		return
	}
	switch r.URL.Path {
	case "/internal/introspect":
		if !service(r, "dovecot") {
			deny(w)
			return
		}
		r.ParseForm()
		var login, sid, kind, smtpLogin string
		e := db.QueryRow(ctx, `SELECT m.login,t.sid,t.kind,t.binding FROM tokens t JOIN mailboxes m ON m.id=t.box JOIN sessions s ON s.id=t.sid JOIN people p ON p.anchor=s.owner WHERE t.hash=$1 AND t.kind IN ('access','smtp') AND t.expires>now() AND s.expires>now() AND m.enabled AND p.allowed AND s.owner=m.owner`, hash(r.FormValue("token"))).Scan(&login, &sid, &kind, &smtpLogin)
		protocol := r.Header.Get("X-Dovecot-Auth-Protocol")
		if e != nil || (kind == "access" && protocol != "imap") || (kind == "smtp" && protocol != "smtp") {
			reply(w, map[string]any{"active": false})
			return
		}
		if kind == "smtp" {
			login = smtpLogin
		}
		reply(w, map[string]any{"active": true, "username": login, "mailgo_sid": sid, "scope": "mail", "exp": time.Now().Add(60 * time.Second).Unix()})

	case "/internal/redeem", "/internal/refresh":
		if !service(r, "roundcube") || r.Method != "POST" {
			deny(w)
			return
		}
		var box, sid string
		var e error
		if r.URL.Path == "/internal/redeem" {
			e = db.QueryRow(ctx, "DELETE FROM tokens WHERE hash=$1 AND kind='handoff' AND binding=$2 AND expires>now() RETURNING sid,box", hash(r.FormValue("code")), hash(r.FormValue("state"))).Scan(&sid, &box)
		} else {
			e = db.QueryRow(ctx, "DELETE FROM tokens WHERE hash=$1 AND kind='refresh' AND expires>now() RETURNING sid,box", hash(r.FormValue("refresh_token"))).Scan(&sid, &box)
		}
		if e != nil {
			deny(w)
			return
		}
		var owner string
		_ = db.QueryRow(ctx, "SELECT s.owner FROM sessions s JOIN people p ON p.anchor=s.owner WHERE s.id=$1 AND s.expires>now() AND p.allowed", sid).Scan(&owner)
		if !owned(owner, box) {
			deny(w)
			return
		}
		reply(w, credential(sid, box))
	case "/internal/sender":
		if !service(r, "rspamd") || r.Method != "POST" {
			deny(w)
			return
		}
		var in struct {
			Login     string   `json:"login"`
			Envelope  []string `json:"envelope"`
			From      []string `json:"from"`
			FromCount int      `json:"from_count"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil {
			deny(w)
			return
		}
		allow := in.Login != "" && len(in.Envelope) == 1 && len(in.From) == 1 && in.FromCount == 1
		for _, a := range append(in.Envelope, in.From...) {
			var ok bool
			e := db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tokens t JOIN sessions s ON s.id=t.sid JOIN people p ON p.anchor=s.owner JOIN mailboxes m ON m.id=t.box JOIN addresses a ON a.box=m.id WHERE t.binding=$1 AND t.kind='smtp' AND t.expires>now() AND s.expires>now() AND p.allowed AND m.enabled AND s.owner=m.owner AND a.address=$2 AND a.active)", in.Login, strings.ToLower(a)).Scan(&ok)
			allow = allow && e == nil && ok
		}
		if !allow {
			deny(w)
			return
		}
		w.WriteHeader(204)
	default:
		http.NotFound(w, r)
	}
}
func handler(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if failure := recover(); failure != nil {
			log.Printf("fixture request failed (%T): %.300v", failure, failure)
			http.Error(w, "Fixture dependency failed", 503)
		}
	}()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "strict-origin")
	if strings.HasPrefix(r.URL.Path, "/internal/") {
		internal(w, r)
		return
	}
	if r.URL.Path == "/backchannel" {
		backchannel(w, r)
		return
	}
	if r.URL.Path == "/health" {
		w.Write([]byte("ready"))
		return
	}
	if r.URL.Path == "/login" {
		state, nonce, v, binding := random(), random(), random(), random()
		exec("INSERT INTO flows(id,nonce,verifier,binding,expires) VALUES($1,$2,$3,$4,now()+interval '2 minutes')", hash(state), nonce, v, hash(binding))
		cookie(w, "mailgo_flow", binding, 120)
		h := sha256.Sum256([]byte(v))
		http.Redirect(w, r, oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", base64.RawURLEncoding.EncodeToString(h[:])), oauth2.SetAuthURLParam("code_challenge_method", "S256")), 302)
		return
	}
	if r.URL.Path == "/callback" {
		var nonce, v string
		e := db.QueryRow(ctx, "DELETE FROM flows WHERE id=$1 AND binding=$2 AND expires>now() RETURNING nonce,verifier", hash(r.URL.Query().Get("state")), hash(value(r, "mailgo_flow"))).Scan(&nonce, &v)
		if e != nil {
			deny(w)
			return
		}
		tok, e := oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", v))
		if e != nil {
			deny(w)
			return
		}
		raw, _ := tok.Extra("id_token").(string)
		id, e := verifier.Verify(ctx, raw)
		if e != nil || id.Nonce != nonce {
			deny(w)
			return
		}
		var exists bool
		_ = db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM people WHERE anchor=$1 AND allowed)", id.Subject).Scan(&exists)
		if !exists {
			deny(w)
			return
		}
		sid := random()
		var claims struct {
			SID string `json:"sid"`
		}
		must(id.Claims(&claims))
		exec("INSERT INTO sessions(id,owner,oidc_sid,expires) VALUES($1,$2,$3,now()+interval '30 minutes')", hash(sid), id.Subject, claims.SID)
		cookie(w, "mailgo", sid, 1800)
		cookie(w, "mailgo_flow", "", -1)
		http.Redirect(w, r, "/", 303)
		return
	}
	sid, owner := session(r)
	if owner == "" {
		http.Redirect(w, r, "/login", 302)
		return
	}
	if r.URL.Path == "/handoff" {
		box, state := r.URL.Query().Get("box"), r.URL.Query().Get("state")
		if !owned(owner, box) || len(state) < 32 {
			deny(w)
			return
		}
		code := random()
		exec("INSERT INTO tokens(hash,sid,box,kind,binding,expires) VALUES($1,$2,$3,'handoff',$4,now()+interval '30 seconds')", hash(code), sid, box, hash(state))
		http.Redirect(w, r, webmail+"?_task=login&_action=login&mailgo_code="+url.QueryEscape(code)+"&mailgo_state="+url.QueryEscape(state), 303)
		return
	}
	if r.URL.Path == "/test/credential" { // Test driver only; not a proposed public API.
		if !csrf(r) || !owned(owner, r.FormValue("box")) {
			deny(w)
			return
		}
		reply(w, credential(sid, r.FormValue("box")))
		return
	}
	if r.URL.Path == "/logout" {
		if !csrf(r) {
			deny(w)
			return
		}
		revokeSessions("id=$1", sid)
		cookie(w, "mailgo", "", -1)
		w.Write([]byte("Mail session revoked"))
		return
	}
	if r.URL.Path == "/alias" {
		if !csrf(r) {
			deny(w)
			return
		}
		box, address, op := r.FormValue("box"), strings.ToLower(r.FormValue("address")), r.FormValue("op")
		// Only fixture addresses; no arbitrary domains, new principal, or primary rename.
		if !owned(owner, box) || !strings.HasSuffix(address, "@mailtest.invalid") || strings.ContainsAny(address, "\r\n <>\t") || strings.Count(address, "@") != 1 || (op != "add" && op != "remove") {
			deny(w)
			return
		}
		mutation.Lock()
		defer mutation.Unlock()
		var uid, main string
		must(db.QueryRow(ctx, "SELECT p.uid,m.address FROM people p JOIN mailboxes m ON m.owner=p.anchor WHERE m.id=$1", box).Scan(&uid, &main))
		if address == main {
			deny(w)
			return
		}
		var existingBox string
		_ = db.QueryRow(ctx, "SELECT box FROM addresses WHERE address=$1", address).Scan(&existingBox)
		if existingBox != "" && existingBox != box {
			deny(w)
			return
		}
		_, dn, adAddresses := person(uid)
		has := false
		for _, a := range adAddresses {
			has = has || a == address
		}
		if (op == "add" && !has) || (op == "remove" && has) {
			c := directory()
			defer c.Close()
			m := ldap.NewModifyRequest(dn, nil)
			if op == "add" {
				m.Add("proxyAddresses", []string{"smtp:" + address})
			} else {
				m.Delete("proxyAddresses", []string{"smtp:" + address})
			}
			must(c.Modify(m))
		}
		exec("INSERT INTO addresses(address,box,active) VALUES($1,$2,$3) ON CONFLICT(address) DO UPDATE SET active=excluded.active", address, box, op == "add")
		routes()
		http.Redirect(w, r, "/", 303)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	type Box struct {
		ID, Address string
		Aliases     []string
	}
	boxes := []Box{}
	rows, e := db.Query(ctx, "SELECT id,address FROM mailboxes WHERE owner=$1 AND enabled ORDER BY id", owner)
	must(e)
	for rows.Next() {
		var b Box
		must(rows.Scan(&b.ID, &b.Address))
		boxes = append(boxes, b)
	}
	rows.Close()
	for i := range boxes {
		rs, e := db.Query(ctx, "SELECT address FROM addresses WHERE box=$1 AND active ORDER BY address", boxes[i].ID)
		must(e)
		for rs.Next() {
			var a string
			must(rs.Scan(&a))
			boxes[i].Aliases = append(boxes[i].Aliases, a)
		}
		rs.Close()
	}
	t := template.Must(template.New("ui").Parse(`<!doctype html><meta charset="utf-8"><title>Mail Go PoC</title><h1>邮件管理验证</h1>{{range .Boxes}}<section><h2>{{.Address}}</h2><p>{{range .Aliases}}{{.}} {{end}}</p><a id="open-{{.ID}}" href="https://webmail.mailtest.invalid:18884/?_task=login&_action=plugin.mailgo&box={{.ID}}">查看邮件 / 发邮件</a><form method="post" action="/alias"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="box" value="{{.ID}}"><input name="address" required><button name="op" value="add">添加别名</button><button name="op" value="remove">移除别名</button></form></section>{{end}}<form action="/logout" method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>退出邮件会话</button></form>`))
	must(t.Execute(w, map[string]any{"Boxes": boxes, "CSRF": hash(value(r, "mailgo") + "csrf")}))
}
func main() {
	b, e := os.ReadFile("/input/secrets.json")
	must(e)
	must(json.Unmarshal(b, &secret))
	ca, e := os.ReadFile("/certs/cert.pem")
	must(e)
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		panic("CA")
	}
	tlsConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}, Timeout: 10 * time.Second}
	ctx = oidc.ClientContext(ctx, client)
	db, e = pgxpool.New(ctx, "postgres://mailgo:"+url.QueryEscape(secret["postgres"])+"@postgres:5432/mailgo?sslmode=disable")
	must(e)
	defer db.Close()
	for i := 0; i < 60; i++ {
		if db.Ping(ctx) == nil {
			break
		}
		time.Sleep(time.Second)
	}
	must(db.Ping(ctx))
	exec(`CREATE TABLE IF NOT EXISTS people(anchor text PRIMARY KEY,uid text UNIQUE NOT NULL);
 CREATE TABLE IF NOT EXISTS mailboxes(id text PRIMARY KEY,owner text REFERENCES people(anchor),login text UNIQUE NOT NULL,address text UNIQUE NOT NULL,enabled boolean NOT NULL DEFAULT true);
 CREATE TABLE IF NOT EXISTS addresses(address text PRIMARY KEY,box text REFERENCES mailboxes(id),active boolean NOT NULL DEFAULT true);
 CREATE TABLE IF NOT EXISTS sessions(id text PRIMARY KEY,owner text REFERENCES people(anchor),expires timestamptz NOT NULL);
 CREATE TABLE IF NOT EXISTS tokens(hash text PRIMARY KEY,sid text REFERENCES sessions(id) ON DELETE CASCADE,box text REFERENCES mailboxes(id),kind text NOT NULL,binding text NOT NULL DEFAULT '',expires timestamptz NOT NULL);
 CREATE TABLE IF NOT EXISTS flows(id text PRIMARY KEY,nonce text,verifier text,binding text,expires timestamptz);`)
	initDirectorySchema()
	// Synthetic resources are seeded once. Later address changes go through AD.
	for _, uid := range []string{"alice", "bob"} {
		anchor, _, addresses := person(uid)
		exec("INSERT INTO people(anchor,uid) VALUES($1,$2) ON CONFLICT DO NOTHING", anchor, uid)
		box := "box-01"
		if uid == "bob" {
			box = "box-03"
		}
		exec("INSERT INTO mailboxes(id,owner,login,address) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", box, anchor, box+"@boxes.mailtest.invalid", uid+"@mailtest.invalid")
		if uid == "alice" {
			exec("INSERT INTO mailboxes(id,owner,login,address) VALUES('box-02',$1,'box-02@boxes.mailtest.invalid','projects@mailtest.invalid') ON CONFLICT DO NOTHING", anchor)
		}
		sort.Strings(addresses)
		for _, a := range addresses {
			target := box
			if a == "projects@mailtest.invalid" {
				target = "box-02"
			}
			exec("INSERT INTO addresses VALUES($1,$2,true) ON CONFLICT DO NOTHING", a, target)
		}
	}
	routes()
	var p *oidc.Provider
	for i := 0; i < 60; i++ {
		p, e = oidc.NewProvider(ctx, issuer)
		if e == nil {
			break
		}
		time.Sleep(time.Second)
	}
	must(e)
	verifier = p.Verifier(&oidc.Config{ClientID: "mail-poc-go", SupportedSigningAlgs: []string{"RS256"}})
	oauth = oauth2.Config{ClientID: "mail-poc-go", ClientSecret: secret["client"], Endpoint: p.Endpoint(), RedirectURL: origin + "/callback", Scopes: []string{"openid", "email", "profile"}}
	syncDirectory()
	go directoryLoop()
	log.Println("mail Go fixture ready")
	must((&http.Server{Addr: ":8080", Handler: http.HandlerFunc(handler), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second}).ListenAndServe())
}
