package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ErrRoutingDenied means the provider explicitly refused read access. It does
// not distinguish a wrong token from a missing grant or disabled account.
var ErrRoutingDenied = errors.New("cloud routing read access denied")

var routingTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)

// preflightRouting observes one bounded page using only the routing token.
// Read access does not establish write access, zone/domain ownership, or mail
// delivery. No provider error body or credential is returned to the caller.
func (c *Cloudflare) preflightRouting(ctx context.Context, zone, token, proxy string) error {
	if c == nil || c.http == nil || !accountID.MatchString(zone) || !routingTokenPattern.MatchString(token) || token == c.token {
		return ErrJournal
	}
	client := c.http
	if proxy != "" {
		p, err := url.Parse(proxy)
		if err != nil || p.Hostname() == "" || p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5" && p.Scheme != "socks5h" || p.Path != "" || p.RawQuery != "" || p.ForceQuery || p.Fragment != "" || strings.ContainsAny(proxy, "\x00\r\n") {
			return ErrJournal
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyURL(p)
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.cloudflare.com/client/v4/zones/"+zone+"/email/routing/rules?per_page=5&page=1", nil)
	if err != nil {
		return ErrJournal
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return ErrUnknown
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return ErrRoutingDenied
	}
	if res.StatusCode != http.StatusOK {
		return ErrUnknown
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return ErrUnknown
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || !envelope.Success || json.Unmarshal(envelope.Result, &rows) != nil || rows == nil || len(rows) > 5 {
		return ErrUnknown
	}
	return nil
}
