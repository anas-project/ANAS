// Package identity verifies personnel identities. It neither owns addresses nor
// writes AD. Protocol and mailbox authorization remain separate decisions.
package identity

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
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	ErrAuthentication = errors.New("mail authentication denied")
	anchorPattern     = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	flowPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

type OIDCConfig struct{ Issuer, ClientID, Callback string }

// Flow must be persisted and consumed once with a separate browser binding by
// the HTTP caller. Possessing a Flow alone does not authorize a local session.
type Flow struct {
	State, Nonce, Verifier string
	Expires                time.Time
}

func NewFlow() (Flow, error) {
	f := Flow{Expires: time.Now().Add(5 * time.Minute)}
	for _, v := range []*string{&f.State, &f.Nonce, &f.Verifier} {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return Flow{}, err
		}
		*v = base64.RawURLEncoding.EncodeToString(b)
	}
	return f, nil
}

func (f Flow) valid() bool {
	return flowPattern.MatchString(f.State) && flowPattern.MatchString(f.Nonce) &&
		flowPattern.MatchString(f.Verifier) && f.Expires.After(time.Now()) &&
		!f.Expires.After(time.Now().Add(5*time.Minute+time.Second))
}

type Authentication struct {
	Anchor, Issuer, SID      string
	AuthenticatedAt, Expires time.Time
}

type OIDC struct {
	config           OIDCConfig
	client           *http.Client
	authorize, token string
	endSession       string
	verifier         *oidc.IDTokenVerifier
	logoutVerifier   *oidc.IDTokenVerifier
}

func httpsURL(value string) (*url.URL, bool) {
	u, err := url.Parse(value)
	return u, err == nil && u.Scheme == "https" && u.Hostname() != "" &&
		u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" &&
		len(value) <= 2048 && !strings.ContainsAny(value, "\x00\r\n")
}

func NewOIDC(ctx context.Context, cfg OIDCConfig, client *http.Client) (*OIDC, error) {
	issuer, ok := httpsURL(cfg.Issuer)
	if !ok {
		return nil, ErrAuthentication
	}
	callback, ok := httpsURL(cfg.Callback)
	if !ok || (callback.Path != "/oidc/callback" && callback.Path != "/oidc/reauth-callback") || cfg.ClientID == "" || len(cfg.ClientID) > 256 || strings.ContainsAny(cfg.ClientID, "\x00\r\n") || client == nil {
		return nil, ErrAuthentication
	}
	// Discovery, token exchange and JWKS retrieval never follow redirects. In
	// particular, a token-endpoint 307 must not disclose a code/verifier elsewhere.
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.Timeout <= 0 || c.Timeout > 10*time.Second {
		c.Timeout = 10 * time.Second
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, &c), cfg.Issuer)
	if err != nil {
		return nil, ErrAuthentication
	}
	var d struct {
		Authorization string `json:"authorization_endpoint"`
		Token         string `json:"token_endpoint"`
		JWKS          string `json:"jwks_uri"`
		EndSession    string `json:"end_session_endpoint"`
	}
	if p.Claims(&d) != nil {
		return nil, ErrAuthentication
	}
	for _, value := range []string{d.Authorization, d.Token, d.JWKS} {
		u, valid := httpsURL(value)
		if !valid || u.Host != issuer.Host {
			return nil, ErrAuthentication
		}
	}
	if d.EndSession != "" {
		u, valid := httpsURL(d.EndSession)
		if !valid || u.Host != issuer.Host {
			return nil, ErrAuthentication
		}
	}
	return &OIDC{config: cfg, client: &c, authorize: d.Authorization, token: d.Token, endSession: d.EndSession,
		verifier:       p.Verifier(&oidc.Config{ClientID: cfg.ClientID, SupportedSigningAlgs: []string{"RS256"}}),
		logoutVerifier: p.Verifier(&oidc.Config{ClientID: cfg.ClientID, SupportedSigningAlgs: []string{"RS256"}, SkipExpiryCheck: true})}, nil
}

func (o *OIDC) AuthorizationURL(f Flow) (string, error) {
	if !f.valid() {
		return "", ErrAuthentication
	}
	h := sha256.Sum256([]byte(f.Verifier))
	u, _ := url.Parse(o.authorize)
	q := url.Values{"client_id": {o.config.ClientID}, "redirect_uri": {o.config.Callback},
		"response_type": {"code"}, "scope": {"openid profile email"}, "state": {f.State},
		"nonce": {f.Nonce}, "code_challenge_method": {"S256"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(h[:])}}
	if strings.HasSuffix(o.config.Callback, "/oidc/reauth-callback") {
		q.Set("prompt", "login")
		q.Set("max_age", "0")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exchange only verifies the IAM identity. The caller must separately check a
// fresh AD person/group admission, then persist a local session. Email, username
// and editable profile claims never select the person or mailbox.
func (o *OIDC) Exchange(ctx context.Context, f Flow, code string) (Authentication, error) {
	if !f.valid() || code == "" || len(code) > 4096 || strings.ContainsAny(code, "\x00\r\n") {
		return Authentication{}, ErrAuthentication
	}
	form := url.Values{"client_id": {o.config.ClientID}, "grant_type": {"authorization_code"},
		"code": {code}, "redirect_uri": {o.config.Callback}, "code_verifier": {f.Verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.token, strings.NewReader(form.Encode()))
	if err != nil {
		return Authentication{}, ErrAuthentication
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client.Do(req)
	if err != nil {
		return Authentication{}, ErrAuthentication
	}
	defer resp.Body.Close()
	var tokens struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		Type        string `json:"token_type"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || decoder.Decode(&tokens) != nil || tokens.IDToken == "" || tokens.AccessToken == "" || !strings.EqualFold(tokens.Type, "Bearer") {
		return Authentication{}, ErrAuthentication
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Authentication{}, ErrAuthentication
	}
	t, err := o.verifier.Verify(oidc.ClientContext(ctx, o.client), tokens.IDToken)
	if err != nil || t.Nonce != f.Nonce || !anchorPattern.MatchString(t.Subject) {
		return Authentication{}, ErrAuthentication
	}
	if t.AccessTokenHash != "" && t.VerifyAccessToken(tokens.AccessToken) != nil {
		return Authentication{}, ErrAuthentication
	}
	var claims struct {
		AuthTime int64  `json:"auth_time"`
		SID      string `json:"sid"`
		AZP      string `json:"azp"`
	}
	if t.Claims(&claims) != nil || (len(t.Audience) > 1 && claims.AZP != o.config.ClientID) || (claims.AZP != "" && claims.AZP != o.config.ClientID) || len(claims.SID) > 1024 || strings.ContainsAny(claims.SID, "\x00\r\n") {
		return Authentication{}, ErrAuthentication
	}
	authTime := time.Unix(claims.AuthTime, 0)
	if claims.AuthTime <= 0 || t.IssuedAt.IsZero() || t.IssuedAt.After(time.Now().Add(30*time.Second)) || authTime.After(t.IssuedAt.Add(30*time.Second)) || !t.Expiry.After(t.IssuedAt) {
		return Authentication{}, ErrAuthentication
	}
	return Authentication{Anchor: t.Subject, Issuer: o.config.Issuer, SID: claims.SID, AuthenticatedAt: authTime, Expires: t.Expiry}, nil
}
