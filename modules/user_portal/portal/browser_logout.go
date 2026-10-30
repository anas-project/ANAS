package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/user_portal/browserlogout"
)

type logoutTicket struct {
	Origin  string `json:"origin"`
	Nonce   string `json:"nonce"`
	Expires int64  `json:"expires"`
}

func (s *Store) signLogout(purpose string, ticket logoutTicket) string {
	data, _ := json.Marshal(ticket)
	payload := base64.RawURLEncoding.EncodeToString(data)
	m := hmac.New(sha256.New, []byte(s.logoutKey))
	m.Write([]byte(purpose + ":" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Store) verifyLogout(purpose, value string, now time.Time) (logoutTicket, bool) {
	var t logoutTicket
	if len(value) > 2048 {
		return t, false
	}
	payload, signature, ok := strings.Cut(value, ".")
	if !ok {
		return t, false
	}
	mac, err := base64.RawURLEncoding.DecodeString(signature)
	m := hmac.New(sha256.New, []byte(s.logoutKey))
	m.Write([]byte(purpose + ":" + payload))
	if err != nil || !hmac.Equal(mac, m.Sum(nil)) {
		return t, false
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(data, &t) != nil || len(t.Nonce) != 43 || t.Expires <= now.Unix() || t.Expires > now.Add(10*time.Minute).Unix() {
		return t, false
	}
	return t, true
}

type browserLogoutTarget struct {
	Origin string `json:"origin"`
	URL    string `json:"url"`
	Nonce  string `json:"nonce"`
}

// These tickets permit only native sign-out. They carry no identity or IAM token
// and cannot read a directory, authenticate, or restore a deleted portal session.
func (s *Server) logoutResponse(w http.ResponseWriter, session Session, publish bool) {
	targets := []browserLogoutTarget{}
	if publish {
		native, err := browserlogout.Targets(s.catalog, s.config.URL)
		if err != nil {
			failure(w, 503, "logout_configuration_invalid")
			return
		}
		for _, target := range native {
			ticket := logoutTicket{target.Origin, randomID(), time.Now().Add(2 * time.Minute).Unix()}
			targets = append(targets, browserLogoutTarget{target.Origin, target.Origin + browserlogout.Path + "?ticket=" + url.QueryEscape(s.store.signLogout("bridge", ticket)), ticket.Nonce})
		}
	}
	response := map[string]any{"iam_endpoint": s.oidc.logout, "local": "logged_out", "targets": targets}
	if s.config.AccountSchema == "llng-account/v1" {
		// Local logout has already happened. Discovery is cached at startup, so
		// an unavailable provider cannot prevent local session destruction.
		f := Flow{State: randomID(), Cookie: randomID(), Logout: true, Expires: time.Now().Add(5 * time.Minute)}
		if !s.store.addFlow(f) {
			failure(w, 503, "capacity_reached")
			return
		}
		cookie(w, logoutCookie, f.Cookie, 300)
		u, _ := url.Parse(s.oidc.logout)
		q := u.Query()
		q.Set("client_id", s.config.ClientID)
		q.Set("post_logout_redirect_uri", s.config.URL+"/oidc/logout-callback")
		q.Set("state", f.State)
		if session.IDToken != "" {
			q.Set("id_token_hint", session.IDToken)
		}
		u.RawQuery = q.Encode()
		response["iam_redirect"] = u.String()
	}
	respond(w, 200, response)
}

func (s *Server) oidcLogoutCallback(w http.ResponseWriter, r *http.Request) {
	f, ok := s.store.consumeFlow(r.URL.Query().Get("state"))
	c, err := r.Cookie(logoutCookie)
	confirmed := ok && f.Logout && err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(f.Cookie)) == 1 && r.URL.Query().Get("error") == ""
	cookie(w, logoutCookie, "", -1)
	value := "failed"
	if confirmed {
		value = "confirmed"
	}
	// Only this verified response can confirm the central logout in the UI.
	// No session is created, and neither the state nor an ID token enters HTML.
	data, _ := ui.ReadFile("ui/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !confirmed {
		w.WriteHeader(http.StatusBadRequest)
	}
	_, _ = w.Write([]byte(strings.Replace(string(data), "<head>", "<head><meta name=\"anas-oidc-logout\" content=\""+value+"\">", 1)))
}

func (s *Server) browserLogout(w http.ResponseWriter, r *http.Request) {
	ticket, ok := s.store.verifyLogout("bridge", r.URL.Query().Get("ticket"), time.Now())
	if !ok || !equivalentOrigin("https://"+r.Host, ticket.Origin) {
		failure(w, 404, "not_found")
		return
	}
	targets, err := browserlogout.Targets(s.catalog, s.config.URL)
	if err != nil {
		failure(w, 404, "not_found")
		return
	}
	adapter := ""
	for _, target := range targets {
		if target.Origin == ticket.Origin {
			adapter = target.Adapter
		}
	}
	if adapter == "" {
		failure(w, 404, "not_found")
		return
	}
	nonce := randomID()
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; frame-ancestors "+s.config.URL+"; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	config, _ := json.Marshal(map[string]string{"parent": s.config.URL, "nonce": ticket.Nonce, "adapter": adapter})
	script, _ := ui.ReadFile("ui/logout-bridge.js")
	// json.Marshal escapes HTML metacharacters; script and nonce are server-owned.
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>Sign out</title><script nonce=\"%s\">const config=%s;\n%s</script>", nonce, config, script)
}
