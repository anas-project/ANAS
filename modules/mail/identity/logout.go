package identity

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func (o *OIDC) LogoutURL(state, callback string) (string, error) {
	u, ok := httpsURL(o.endSession)
	to, valid := httpsURL(callback)
	original, _ := url.Parse(o.config.Callback)
	if !ok || !valid || !flowPattern.MatchString(state) || to.Host != original.Host || to.Path != "/oidc/logout-callback" {
		return "", ErrAuthentication
	}
	q := u.Query()
	q.Set("client_id", o.config.ClientID)
	q.Set("post_logout_redirect_uri", callback)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Logout is a verified back-channel notification, never a browser identity.
type Logout struct {
	Issuer, ID, Anchor, SID string
	IssuedAt                time.Time
}

func (o *OIDC) VerifyLogout(ctx context.Context, raw string) (Logout, error) {
	if raw == "" || len(raw) > 16384 {
		return Logout{}, ErrAuthentication
	}
	token, err := o.logoutVerifier.Verify(oidc.ClientContext(ctx, o.client), raw)
	if err != nil {
		return Logout{}, ErrAuthentication
	}
	var claims struct {
		ID     string                     `json:"jti"`
		SID    string                     `json:"sid"`
		AZP    string                     `json:"azp"`
		Issued int64                      `json:"iat"`
		Expiry *int64                     `json:"exp"`
		Nonce  json.RawMessage            `json:"nonce"`
		Events map[string]json.RawMessage `json:"events"`
	}
	if token.Claims(&claims) != nil || claims.ID == "" || len(claims.ID) > 256 || len(claims.SID) > 1024 || strings.ContainsAny(claims.ID+claims.SID, "\x00\r\n") ||
		(token.Subject == "" && claims.SID == "") || (token.Subject != "" && !anchorPattern.MatchString(token.Subject)) ||
		len(claims.Nonce) != 0 || (len(token.Audience) > 1 && claims.AZP != o.config.ClientID) || (claims.AZP != "" && claims.AZP != o.config.ClientID) {
		return Logout{}, ErrAuthentication
	}
	var event map[string]json.RawMessage
	if json.Unmarshal(claims.Events["http://schemas.openid.net/event/backchannel-logout"], &event) != nil || event == nil || len(event) != 0 {
		return Logout{}, ErrAuthentication
	}
	now, issued := time.Now(), time.Unix(claims.Issued, 0)
	if claims.Issued <= 0 || issued.After(now.Add(30*time.Second)) || issued.Before(now.Add(-5*time.Minute)) ||
		(claims.Expiry != nil && (*claims.Expiry <= now.Unix() || *claims.Expiry <= claims.Issued)) {
		return Logout{}, ErrAuthentication
	}
	return Logout{Issuer: o.config.Issuer, ID: claims.ID, Anchor: token.Subject, SID: claims.SID, IssuedAt: issued}, nil
}
