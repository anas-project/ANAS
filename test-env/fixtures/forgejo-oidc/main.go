// Test-only OIDC provider for a disposable Forgejo fixture. It is not an IAM
// implementation: authorization always uses the single controlled test user.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

func main() {
	claimsPath := flag.String("claims", "", "private fixture claims JSON")
	flag.Parse()
	if *claimsPath != "/home/whl/anas-forgejo-1030/fixture/claims.json" {
		log.Fatal("dedicated fixture claims required")
	}
	const issuer = "http://10.253.30.2:13001"
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	encode := func(v any) string { b, _ := json.Marshal(v); return b64(b) }
	send := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	type authorization struct{ nonce, verifier string }
	codes := map[string]authorization{}
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "userinfo_endpoint": issuer + "/userinfo", "jwks_uri": issuer + "/keys",
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}, "scopes_supported": []string{"openid", "profile", "email", "groups"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		send(w, map[string]any{"keys": []any{map[string]any{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "fixture", "n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil || redirect.String() != "http://127.0.0.1:13000/user/oauth2/anas/callback" || q.Get("client_id") != "fixture-client" {
			http.Error(w, "invalid fixture client", 400)
			return
		}
		random := make([]byte, 24)
		_, _ = rand.Read(random)
		code := b64(random)
		mu.Lock()
		codes[code] = authorization{q.Get("nonce"), q.Get("code_challenge")}
		mu.Unlock()
		values := redirect.Query()
		values.Set("code", code)
		values.Set("state", q.Get("state"))
		redirect.RawQuery = values.Encode()
		http.Redirect(w, r, redirect.String(), 302)
	})
	claims := func() (map[string]any, error) {
		b, err := os.ReadFile(*claimsPath)
		if err != nil {
			return nil, err
		}
		var v map[string]any
		err = json.Unmarshal(b, &v)
		return v, err
	}
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		_ = r.ParseForm()
		user, password, ok := r.BasicAuth()
		if !ok {
			user, password = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if user != "fixture-client" || password != "fixture-secret" {
			http.Error(w, "client", 401)
			return
		}
		mu.Lock()
		a, ok := codes[r.Form.Get("code")]
		delete(codes, r.Form.Get("code"))
		mu.Unlock()
		if !ok {
			http.Error(w, "code", 400)
			return
		}
		if a.verifier != "" {
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if b64(sum[:]) != a.verifier {
				http.Error(w, "PKCE", 400)
				return
			}
		}
		v, err := claims()
		if err != nil {
			http.Error(w, "claims", 500)
			return
		}
		v["iss"], v["aud"], v["iat"], v["exp"], v["nonce"] = issuer, "fixture-client", time.Now().Unix(), time.Now().Add(5*time.Minute).Unix(), a.nonce
		message := encode(map[string]string{"alg": "RS256", "kid": "fixture", "typ": "JWT"}) + "." + encode(v)
		sum := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			http.Error(w, "signature", 500)
			return
		}
		send(w, map[string]any{"access_token": "fixture-userinfo", "token_type": "Bearer", "expires_in": 300, "id_token": message + "." + b64(signature)})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Authorization"), "Bearer fixture-userinfo") {
			http.Error(w, "token", 401)
			return
		}
		v, err := claims()
		if err != nil {
			http.Error(w, "claims", 500)
			return
		}
		send(w, v)
	})
	fmt.Println("fixture OIDC ready")
	log.Fatal((&http.Server{Addr: "10.253.30.2:13001", Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
