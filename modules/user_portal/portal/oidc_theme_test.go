// TEST_CASES: APPTHEME-T-007
package main

import (
	"net/url"
	"testing"
)

func TestNativeAuthorizationThemeAllowListPreservesOIDC(t *testing.T) {
	o := &OIDC{config: Config{URL: "https://portal.example", ClientID: "portal", ReauthClientID: "reauth"}, authorize: "https://iam.example/login/oauth/authorize"}
	for _, mode := range []string{"dark", "default", "", "system", "https://bad.example/", "dark&redirect_uri=bad"} {
		for _, reauth := range []bool{false, true} {
			u, err := url.Parse(o.authorizeURL(Flow{Theme: mode, Reauth: reauth, State: "state", Nonce: "nonce", Verifier: "verifier"}))
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			want := ""
			if mode == "dark" || mode == "default" {
				want = mode
			}
			if q.Get("theme") != want || q.Get("state") != "state" || q.Get("nonce") != "nonce" || q.Get("code_challenge_method") != "S256" {
				t.Fatal("invalid theme or OIDC data")
			}
			if reauth && (q.Get("client_id") != "reauth" || q.Get("prompt") != "login" || q.Get("max_age") != "0") {
				t.Fatal("recent-auth guarantees lost")
			}
		}
	}
}
