package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/coreos/go-oidc/v3/oidc"
)

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("system randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

type OIDC struct {
	config                   Config
	client                   *http.Client
	provider                 *oidc.Provider
	authorize, token, logout string
	verifier, reauthVerifier *oidc.IDTokenVerifier
}

func newOIDC(ctx context.Context, c Config, client *http.Client) (*OIDC, error) {
	ctx = oidc.ClientContext(ctx, client)
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, errors.New("IAM discovery unavailable")
	}
	var d struct {
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
		Logout        string `json:"end_session_endpoint"`
		JWKS          string `json:"jwks_uri"`
	}
	if err := p.Claims(&d); err != nil {
		return nil, err
	}
	for _, s := range []string{d.Authorization, d.Token, d.Logout, d.JWKS} {
		u, err := url.Parse(s)
		issuer, _ := url.Parse(c.Issuer)
		if err != nil || u.Scheme != issuer.Scheme || u.Host != issuer.Host || u.User != nil || u.Fragment != "" {
			return nil, errors.New("IAM endpoint origin mismatch")
		}
	}
	return &OIDC{config: c, client: client, provider: p, authorize: d.Authorization, token: d.Token, logout: d.Logout, verifier: p.Verifier(&oidc.Config{ClientID: c.ClientID, SupportedSigningAlgs: []string{"RS256"}}), reauthVerifier: p.Verifier(&oidc.Config{ClientID: c.ReauthClientID, SupportedSigningAlgs: []string{"RS256"}})}, nil
}

type Flow struct {
	State, Nonce, Verifier, Cookie, Session, Subject string
	Theme                                            string
	Reauth                                           bool
	Silent                                           bool
	Logout                                           bool
	Expires                                          time.Time
}

func (o *OIDC) authorizeURL(f Flow) string {
	client, callback := o.config.ClientID, o.config.URL+"/oidc/callback"
	if f.Reauth {
		client, callback = o.config.ReauthClientID, o.config.URL+"/oidc/reauth-callback"
	}
	h := sha256.Sum256([]byte(f.Verifier))
	u, _ := url.Parse(o.authorize)
	q := u.Query()
	for k, v := range map[string]string{"client_id": client, "redirect_uri": callback, "response_type": "code", "scope": "openid profile email", "state": f.State, "nonce": f.Nonce, "code_challenge_method": "S256", "code_challenge": base64.RawURLEncoding.EncodeToString(h[:])} {
		q.Set(k, v)
	}
	if f.Silent && !f.Reauth {
		q.Set("prompt", "none")
	}
	if f.Reauth {
		q.Set("max_age", "0")
		q.Set("prompt", "login")
	}
	if f.Theme == "dark" || f.Theme == "default" {
		q.Set("theme", f.Theme)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type Authentication struct {
	Subject, SID, AccessToken, IDToken string
	AuthTime, Expires                  time.Time
	// Passkey comes only from the verified LLNG ID token's AMR claim.
	Passkey bool
}

func (o *OIDC) exchange(ctx context.Context, f Flow, code string) (Authentication, error) {
	var a Authentication
	client, callback, verifier := o.config.ClientID, o.config.URL+"/oidc/callback", o.verifier
	if f.Reauth {
		client, callback, verifier = o.config.ReauthClientID, o.config.URL+"/oidc/reauth-callback", o.reauthVerifier
	}
	form := url.Values{"client_id": {client}, "grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback}, "code_verifier": {f.Verifier}}
	req, err := http.NewRequestWithContext(ctx, "POST", o.token, strings.NewReader(form.Encode()))
	if err != nil {
		return a, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client.Do(req)
	if err != nil {
		return a, errors.New("token exchange failed")
	}
	defer resp.Body.Close()
	var tokens struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		Type        string `json:"token_type"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tokens) != nil || tokens.AccessToken == "" || tokens.IDToken == "" || !strings.EqualFold(tokens.Type, "Bearer") {
		return a, errDenied
	}
	t, err := verifier.Verify(oidc.ClientContext(ctx, o.client), tokens.IDToken)
	if err != nil || t.Nonce != f.Nonce || !anchorPattern.MatchString(t.Subject) {
		return a, errDenied
	}
	var claims struct {
		AuthTime int64    `json:"auth_time"`
		SID      string   `json:"sid"`
		AZP      string   `json:"azp"`
		AMR      []string `json:"amr"`
	}
	if t.Claims(&claims) != nil || (len(t.Audience) > 1 && claims.AZP != client) || (claims.AZP != "" && claims.AZP != client) {
		return a, errDenied
	}
	a = Authentication{Subject: t.Subject, SID: claims.SID, AccessToken: tokens.AccessToken, IDToken: tokens.IDToken, AuthTime: time.Unix(claims.AuthTime, 0), Expires: t.Expiry}
	a.Passkey = o.config.AccountSchema == "llng-account/v1" && has(claims.AMR, "webauthn") && !has(claims.AMR, "pwd")
	if claims.AuthTime <= 0 || t.IssuedAt.IsZero() || t.IssuedAt.After(time.Now().Add(30*time.Second)) || a.AuthTime.After(t.IssuedAt.Add(30*time.Second)) || !t.Expiry.After(t.IssuedAt) {
		return Authentication{}, errDenied
	}
	if f.Reauth && (a.Subject != f.Subject || !recent(a.AuthTime, o.config.RecentWindow, time.Now())) {
		return Authentication{}, errDenied
	}
	return a, nil
}

func recent(authTime time.Time, window time.Duration, now time.Time) bool {
	return authTime.Unix() > 0 && !authTime.After(now.Add(30*time.Second)) && !authTime.Before(now.Add(-window))
}

type Permission struct {
	Owner, Name, Model, Adapter, State, Effect, ResourceType, ExpireTime string
	Groups, Resources, Actions, Roles, Users, Domains                    []string
	IsEnabled                                                            bool
}
type IAMAccount struct {
	Schema                  string
	Applications            []IAMApplicationPolicy
	Owner, Name, ExternalId string
	IsForbidden, IsDeleted  *bool
	Groups                  []string
	Permissions             []Permission
}

type IAMApplicationPolicy struct {
	Name    string   `json:"name"`
	Groups  []string `json:"groups"`
	Allowed *bool    `json:"allowed"`
}

func (o *OIDC) get(ctx context.Context, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(o.config.Issuer, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := o.client.Do(req)
	if err != nil {
		return errors.New("IAM unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errDenied
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out) != nil {
		return errDenied
	}
	return nil
}

func (o *OIDC) account(ctx context.Context, a Authentication) (IAMAccount, error) {
	if o.config.AccountSchema == "llng-account/v1" {
		return o.llngAccount(ctx, a)
	}
	var info struct {
		Sub    string
		Groups []string
	}
	if err := o.get(ctx, "/api/userinfo", a.AccessToken, &info); err != nil {
		return IAMAccount{}, err
	}
	var response struct {
		Status string
		Data   *IAMAccount
	}
	if err := o.get(ctx, "/api/get-account", a.AccessToken, &response); err != nil {
		return IAMAccount{}, err
	}
	if info.Sub != a.Subject || response.Status != "ok" || response.Data == nil {
		return IAMAccount{}, errDenied
	}
	u := *response.Data
	if u.ExternalId != a.Subject || u.Owner != "anas" || u.IsDeleted == nil || u.IsForbidden == nil || *u.IsDeleted || *u.IsForbidden || !sameSet(info.Groups, u.Groups) {
		return IAMAccount{}, errDenied
	}
	// Ignore any extra accessToken minted by get-account; it never leaves this backend.
	return u, nil
}

func (o *OIDC) llngAccount(ctx context.Context, a Authentication) (IAMAccount, error) {
	var info struct{ Sub string }
	if err := o.get(ctx, "/oauth2/userinfo", a.AccessToken, &info); err != nil {
		return IAMAccount{}, err
	}
	var response struct {
		Schema, Subject, Name string
		Enabled               *bool
		Groups                []string
		Applications          []IAMApplicationPolicy
	}
	if err := o.get(ctx, "/anas-account", a.AccessToken, &response); err != nil {
		return IAMAccount{}, err
	}
	if response.Schema != "llng-account/v1" || info.Sub != a.Subject || response.Subject != a.Subject || response.Name == "" || response.Enabled == nil || !*response.Enabled || !sameSet(response.Groups, response.Groups) {
		return IAMAccount{}, errDenied
	}
	// LLNG checks its current native rules against a fresh AD read. The portal
	// additionally intersects these facts with its own directory and catalog.
	no := false
	u := IAMAccount{Schema: response.Schema, Owner: "anas", Name: response.Name, ExternalId: a.Subject, IsForbidden: &no, IsDeleted: &no, Applications: response.Applications}
	for _, name := range response.Groups {
		if name == "" {
			return IAMAccount{}, errDenied
		}
		u.Groups = append(u.Groups, "anas/"+name)
	}
	return u, nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range a {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
		delete(seen, v)
	}
	return len(seen) == 0
}

func applicationAllowed(c appcatalog.Catalog, app appcatalog.Application, u IAMAccount, person Person) bool {
	if !app.Enabled || app.Hidden || len(app.Groups) == 0 {
		return false
	}
	if u.Schema == "llng-account/v1" {
		var policy *IAMApplicationPolicy
		for i := range u.Applications {
			if u.Applications[i].Name == app.Name {
				if policy != nil {
					return false
				}
				policy = &u.Applications[i]
			}
		}
		return policy != nil && policy.Allowed != nil && *policy.Allowed && sameSet(policy.Groups, app.Groups) && currentApplicationGroup(app, u, person)
	}
	resource := "app-anas-catalog-" + app.Name
	if primary, ok := c.IAMApplication(app.Access.Client); app.Access.Mode == "iam" && ok && primary.Name == app.Name {
		resource = "app-anas-" + strings.ToLower(strings.ReplaceAll(app.Access.Client, "_", "-"))
	}
	expected := []string{}
	for _, g := range app.Groups {
		expected = append(expected, "anas/"+g)
	}
	var policy *Permission
	for i := range u.Permissions {
		p := &u.Permissions[i]
		if p.Owner == "anas" && p.Name == "permission-"+resource {
			if policy != nil {
				return false
			}
			policy = p
		}
	}
	if policy == nil {
		return false
	}
	p := policy
	if !p.IsEnabled || p.State != "Approved" || p.Effect != "Allow" || p.ResourceType != "Application" || p.Model != "built-in/user-model-built-in" || p.Adapter != "" || p.ExpireTime != "" || len(p.Roles) > 0 || len(p.Users) > 0 || len(p.Domains) > 0 || !sameSet(p.Resources, []string{resource}) || !sameSet(p.Actions, []string{"Read"}) || !sameSet(p.Groups, expected) {
		return false
	}
	return currentApplicationGroup(app, u, person)
}

func currentApplicationGroup(app appcatalog.Application, u IAMAccount, person Person) bool {
	for _, name := range app.Groups {
		if person.KnownGroups[name] == "" {
			return false
		}
	}
	for _, g := range person.Groups {
		if has(app.Groups, g.Name) && has(u.Groups, "anas/"+g.Name) && person.KnownGroups[g.Name] == g.Anchor {
			return true
		}
	}
	return false
}
