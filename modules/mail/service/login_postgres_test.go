package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
	"github.com/go-jose/go-jose/v4"
	"github.com/jackc/pgx/v5"
)

type protocolDirectory struct {
	anchor string
	err    error
}

func (d *protocolDirectory) Person(_ context.Context, anchor string) (identity.Person, error) {
	if anchor != d.anchor {
		return identity.Person{}, identity.ErrAdmission
	}
	return identity.Person{Anchor: d.anchor, Username: "fixture", DisplayName: "Fixture", Mail: "fixture@example.test"}, d.err
}

func loginTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	path := os.Getenv("MAIL_STORE_TEST_CONFIG")
	if path == "" {
		t.Skip("isolated PostgreSQL fixture required; not a live-host pass")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private database fixture required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("database fixture unreadable")
	}
	var private struct {
		DSN string `json:"dsn"`
	}
	if json.Unmarshal(b, &private) != nil {
		t.Fatal("database fixture invalid")
	}
	cfg, err := pgx.ParseConfig(private.DSN)
	if err != nil || (cfg.Database != "anas_mail_model_1025" && cfg.Database != "anas_mail_lifecycle_1025" && cfg.Database != "anas_mail_cold_1025") {
		t.Fatal("refusing non-fixture database")
	}
	db, err := sql.Open("pgx", private.DSN)
	if err != nil {
		t.Fatal("database open failed")
	}
	t.Cleanup(func() { db.Close() })
	if err := (store.Store{DB: db}).Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

// Real PostgreSQL + real TLS/OIDC protocol + actual HTTP handlers, with a
// simulated issuer and directory. This is not Casdoor/LLNG or AD acceptance.
func TestPostgreSQLLoginHTTPProtocol(t *testing.T) {
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	anchor := handoffPerson(t, db)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "one"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(claims map[string]any) string {
		b, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		obj, err := signer.Sign(b)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := obj.CompactSerialize()
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var mu sync.Mutex
	var nonce, challenge string
	var issuer *httptest.Server
	iamSID := "http-" + anchor
	issuer = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.URL, "authorization_endpoint": issuer.URL + "/authorize", "token_endpoint": issuer.URL + "/token", "jwks_uri": issuer.URL + "/jwks"})
		case "/jwks":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "one", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			r.ParseForm()
			mu.Lock()
			n, c := nonce, challenge
			mu.Unlock()
			hash := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if r.PostForm.Get("code") != "controlled-code" || r.PostForm.Get("client_id") != "mail" || base64.RawURLEncoding.EncodeToString(hash[:]) != c {
				w.WriteHeader(400)
				return
			}
			now := time.Now().Unix()
			raw := sign(map[string]any{"iss": issuer.URL, "aud": "mail", "sub": anchor, "sid": iamSID, "iat": now, "exp": now + 3600, "auth_time": now - 5, "nonce": n})
			json.NewEncoder(w).Encode(map[string]any{"id_token": raw, "access_token": "fixture-access", "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	o, err := identity.NewOIDC(t.Context(), identity.OIDCConfig{Issuer: issuer.URL, ClientID: "mail", Callback: "https://mail.example.test/oidc/callback"}, issuer.Client())
	if err != nil {
		t.Fatal(err)
	}
	directory := &protocolDirectory{anchor: anchor}
	h, err := newLoginHandler("https://mail.example.test", issuer.URL, data, o, directory)
	if err != nil {
		t.Fatal(err)
	}
	start := request(t, h, "GET", "/login", "", "", "")
	if start.Code != 303 {
		t.Fatal("start", start.Code)
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	nonce = location.Query().Get("nonce")
	challenge = location.Query().Get("code_challenge")
	mu.Unlock()
	flow := start.Result().Cookies()[0]
	// Recreate the handler and Store before consuming the persistent flow.
	h, err = newLoginHandler("https://mail.example.test", issuer.URL, store.Store{DB: db}, o, directory)
	if err != nil {
		t.Fatal(err)
	}
	callback := "/oidc/callback?state=" + url.QueryEscape(location.Query().Get("state")) + "&code=controlled-code"
	responses := make(chan *httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); responses <- request(t, h, "GET", callback, flow.Name+"="+flow.Value, "", "") }()
	}
	wg.Wait()
	close(responses)
	winners := 0
	var secret string
	for w := range responses {
		if w.Code == 303 {
			winners++
			for _, c := range w.Result().Cookies() {
				if c.Name == sessionCookie {
					secret = c.Value
				}
			}
		} else if w.Code != 400 {
			t.Fatal("concurrent callback", w.Code, w.Body.String())
		}
	}
	if winners != 1 || secret == "" {
		t.Fatal("callback consumption", winners)
	}
	cookie := sessionCookie + "=" + secret
	if w := request(t, h, "GET", "/api/me", cookie, "", ""); w.Code != 200 {
		t.Fatal("fresh profile", w.Code, w.Body.String())
	}
	directory.err = identity.ErrDirectoryUnavailable
	if w := request(t, h, "GET", "/api/me", cookie, "", ""); w.Code != 503 {
		t.Fatal("directory outage admitted")
	}
	if _, err := data.Session(t.Context(), secret); err != nil {
		t.Fatal("outage persisted false deletion", err)
	}
	logout := sign(map[string]any{"iss": issuer.URL, "aud": "mail", "sub": anchor, "sid": iamSID, "jti": "http-logout-" + anchor, "iat": time.Now().Unix(), "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}})
	for i := 0; i < 2; i++ {
		w := request(t, h, "POST", "/oidc/backchannel-logout", "", "logout_token="+url.QueryEscape(logout), "")
		if w.Code != 200 {
			t.Fatal("logout with LDAP down", w.Code, w.Body.String())
		}
	}
	directory.err = nil
	if w := request(t, h, "GET", "/api/me", cookie, "", ""); w.Code != 401 {
		t.Fatal("old cookie after notification", w.Code)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM mail.web_session WHERE owner_anchor=$1`, anchor).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate persisted sessions", count, err)
	}
	var raw bool
	if err := db.QueryRowContext(t.Context(), `SELECT token_hash=$2 FROM mail.web_session WHERE owner_anchor=$1`, anchor, []byte(secret)).Scan(&raw); err != nil || raw {
		t.Fatal("raw cookie stored")
	}
	if strings.Contains(logout, secret) {
		t.Fatal("fixture secret confusion")
	}
}
