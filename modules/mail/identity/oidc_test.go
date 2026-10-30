package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAnchor = "11111111-2222-3333-4444-555555555555"

func signedToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"one","typ":"JWT"}`))
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := h + "." + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

type oidcFixture struct {
	mu                                                       sync.Mutex
	server                                                   *httptest.Server
	key                                                      *rsa.PrivateKey
	flow                                                     Flow
	claims                                                   map[string]any
	endpointOverride, tokenRedirect, trailing, tokenOverride string
	logoutEndpoint                                           string
	seen                                                     url.Values
}

func newOIDCFixture(t *testing.T) (*oidcFixture, *OIDC) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := NewFlow()
	if err != nil {
		t.Fatal(err)
	}
	f := &oidcFixture{key: key, flow: flow}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			endpoint := f.server.URL + "/token"
			if f.endpointOverride != "" {
				endpoint = f.endpointOverride
			}
			json.NewEncoder(w).Encode(map[string]any{"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": endpoint, "jwks_uri": f.server.URL + "/jwks", "end_session_endpoint": f.logoutEndpoint, "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "one", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			r.ParseForm()
			f.seen = r.PostForm
			if f.tokenRedirect != "" {
				http.Redirect(w, r, f.tokenRedirect, 307)
				return
			}
			if r.PostForm.Get("client_id") != "mail" || r.PostForm.Get("code") != "one-use-code" || r.PostForm.Get("code_verifier") != flow.Verifier || r.PostForm.Get("redirect_uri") != "https://mail.example.test/oidc/callback" || r.PostForm.Get("client_secret") != "" {
				w.WriteHeader(400)
				return
			}
			token := f.tokenOverride
			if token == "" {
				token = signedToken(t, key, f.claims)
			}
			json.NewEncoder(w).Encode(map[string]any{"id_token": token, "access_token": "synthetic-access-token", "token_type": "Bearer"})
			w.Write([]byte(f.trailing))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	now := time.Now().Unix()
	f.claims = map[string]any{"iss": f.server.URL, "sub": testAnchor, "aud": "mail", "iat": now, "exp": now + 3600, "auth_time": now - 5, "nonce": flow.Nonce, "sid": "one-iam-session", "email": "editable@example.test", "preferred_username": "editable"}
	o, err := NewOIDC(t.Context(), OIDCConfig{Issuer: f.server.URL, ClientID: "mail", Callback: "https://mail.example.test/oidc/callback"}, f.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return f, o
}

func TestOIDCIdentityAndPKCE(t *testing.T) {
	f, o := newOIDCFixture(t)
	location, err := o.AuthorizationURL(f.flow)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(location)
	q := u.Query()
	h := sha256.Sum256([]byte(f.flow.Verifier))
	if q.Get("state") != f.flow.State || q.Get("nonce") != f.flow.Nonce || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(h[:]) || q.Get("redirect_uri") != "https://mail.example.test/oidc/callback" || strings.Contains(location, f.flow.Verifier) {
		t.Fatal("authorization lost its PKCE/browser flow binding")
	}
	a, err := o.Exchange(t.Context(), f.flow, "one-use-code")
	if err != nil || a.Anchor != testAnchor || a.Issuer != f.server.URL || a.SID != "one-iam-session" || a.AuthenticatedAt.IsZero() || !a.Expires.After(time.Now()) {
		t.Fatal("verified identity failed", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen.Get("code_verifier") != f.flow.Verifier || f.seen.Get("client_secret") != "" {
		t.Fatal("public-client exchange contract changed")
	}
}

func TestOIDCRejectsUntrustedClaims(t *testing.T) {
	f, o := newOIDCFixture(t)
	mutations := []struct {
		name   string
		change func(map[string]any)
	}{
		{"issuer", func(c map[string]any) { c["iss"] = "https://other.example.test" }},
		{"audience", func(c map[string]any) { c["aud"] = "other-client" }},
		{"multiple audience without azp", func(c map[string]any) { c["aud"] = []string{"mail", "other"} }},
		{"azp", func(c map[string]any) { c["azp"] = "other-client" }},
		{"nonce", func(c map[string]any) { c["nonce"] = "wrong-nonce" }},
		{"email subject", func(c map[string]any) { c["sub"] = "alice@example.test" }},
		{"username subject", func(c map[string]any) { c["sub"] = "alice" }},
		{"uppercase anchor", func(c map[string]any) { c["sub"] = "AAAAAAAA-2222-3333-4444-555555555555" }},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{"future iat", func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() }},
		{"missing authentication time", func(c map[string]any) { delete(c, "auth_time") }},
		{"future authentication", func(c map[string]any) { c["auth_time"] = time.Now().Add(time.Hour).Unix() }},
		{"wrong access token hash", func(c map[string]any) { c["at_hash"] = "invalid" }},
		{"control in sid", func(c map[string]any) { c["sid"] = "private\r\nsecret" }},
	}
	base := f.claims
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			claims := map[string]any{}
			for k, v := range base {
				claims[k] = v
			}
			tt.change(claims)
			f.mu.Lock()
			f.claims = claims
			f.mu.Unlock()
			if a, err := o.Exchange(t.Context(), f.flow, "one-use-code"); err != ErrAuthentication || a.Anchor != "" || strings.Contains(err.Error(), "private") {
				t.Fatal("untrusted claims authenticated", err)
			}
		})
	}
	f.mu.Lock()
	f.claims = base
	f.claims["aud"] = []string{"mail", "other"}
	f.claims["azp"] = "mail"
	f.mu.Unlock()
	if _, err := o.Exchange(t.Context(), f.flow, "one-use-code"); err != nil {
		t.Fatal("authorized party rejected", err)
	}
}

func TestOIDCRejectsRedirectsMalformedAndExpiredFlows(t *testing.T) {
	f, o := newOIDCFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.tokenOverride = signedToken(t, other, f.claims)
	f.mu.Unlock()
	if _, err := o.Exchange(t.Context(), f.flow, "one-use-code"); err != ErrAuthentication {
		t.Fatal("invalid ID token signature accepted")
	}
	f.mu.Lock()
	f.tokenOverride = ""
	f.mu.Unlock()
	for _, endpoint := range []string{"http://iam.example.test/token", "https://other.example.test/token", f.server.URL + "/token?secret=x", f.server.URL + "/token#x"} {
		f.mu.Lock()
		f.endpointOverride = endpoint
		f.mu.Unlock()
		if _, err := NewOIDC(t.Context(), o.config, f.server.Client()); err == nil {
			t.Fatal("untrusted discovery endpoint accepted")
		}
	}
	f.mu.Lock()
	f.endpointOverride = ""
	f.trailing = "{}"
	f.mu.Unlock()
	if _, err := o.Exchange(t.Context(), f.flow, "one-use-code"); err != ErrAuthentication {
		t.Fatal("trailing token response accepted")
	}
	f.mu.Lock()
	f.trailing = ""
	f.tokenRedirect = f.server.URL + "/should-not-follow"
	f.mu.Unlock()
	if _, err := o.Exchange(t.Context(), f.flow, "one-use-code"); err != ErrAuthentication {
		t.Fatal("token redirect accepted")
	}
	expired := f.flow
	expired.Expires = time.Now().Add(-time.Second)
	if _, err := o.AuthorizationURL(expired); err == nil {
		t.Fatal("expired flow authorized")
	}
	if _, err := o.Exchange(t.Context(), expired, "one-use-code"); err == nil {
		t.Fatal("expired flow exchanged")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := o.Exchange(ctx, f.flow, "one-use-code"); err == nil {
		t.Fatal("canceled exchange succeeded")
	}
	if _, err := NewOIDC(t.Context(), o.config, http.DefaultClient); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
	for _, issuer := range []string{"http://iam.example.test", "https://user:pass@iam.example.test", "https://iam.example.test/?", "https://iam.example.test/#x"} {
		cfg := o.config
		cfg.Issuer = issuer
		if _, err := NewOIDC(t.Context(), cfg, f.server.Client()); err == nil {
			t.Fatal("unsafe issuer accepted")
		}
	}
}
