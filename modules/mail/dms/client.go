package dms

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ClientConfig struct{ URL, ServiceToken string }
type Client struct {
	base, token string
	http        *http.Client
}

func NewClient(cfg ClientConfig, trust *tls.Config) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "9443" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(cfg.URL, "\x00\r\n") || !serviceKey.MatchString(cfg.ServiceToken) || trust == nil || trust.InsecureSkipVerify || trust.RootCAs == nil {
		return nil, ErrConflict
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = trust.Clone()
	if transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	return &Client{base: u.String(), token: cfg.ServiceToken, http: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, in any) (Receipt, error) {
	var body []byte
	var err error
	if in != nil {
		body, err = json.Marshal(in)
		if err != nil {
			return Receipt{}, ErrConflict
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, ErrConflict
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(r)
	if err != nil {
		return Receipt{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 409 {
		return Receipt{}, ErrConflict
	}
	if response.StatusCode == 204 && path == "/v1/sessions/kick" {
		return Receipt{}, nil
	}
	if response.StatusCode == 503 {
		var state struct {
			Receipt
			Error string `json:"error"`
			Check string `json:"check"`
		}
		d := json.NewDecoder(io.LimitReader(response.Body, 65537))
		d.DisallowUnknownFields()
		if d.Decode(&state) == nil && state.Error == "dms-not-confirmed" && !state.Applied && uuid.MatchString(state.MailboxID) && uuid.MatchString(state.Revision) && len(state.Fingerprint) == 64 {
			var extra any
			if d.Decode(&extra) == io.EOF {
				return state.Receipt, ErrUnavailable
			}
		}
		return Receipt{}, ErrUnavailable
	}
	if response.StatusCode != 200 {
		return Receipt{}, ErrUnavailable
	}
	var receipt Receipt
	d := json.NewDecoder(io.LimitReader(response.Body, 65537))
	d.DisallowUnknownFields()
	if d.Decode(&receipt) != nil {
		return Receipt{}, ErrUnavailable
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !receipt.Applied || !uuid.MatchString(receipt.MailboxID) || !uuid.MatchString(receipt.Revision) || len(receipt.Fingerprint) != 64 {
		return Receipt{}, ErrUnavailable
	}
	return receipt, nil
}
func (c *Client) Apply(ctx context.Context, in ApplyRequest) (Receipt, error) {
	if !canonicalProjection(in.Projection) {
		return Receipt{}, ErrConflict
	}
	out, err := c.request(ctx, "POST", "/v1/config/apply", in)
	if err == nil && (out.MailboxID != in.Projection.Mailbox.ID || out.Revision != in.Projection.Revision || out.Fingerprint != in.Projection.Fingerprint) {
		return Receipt{}, ErrUnavailable
	}
	return out, err
}
func (c *Client) Read(ctx context.Context, id string) (Receipt, error) {
	if !uuid.MatchString(id) {
		return Receipt{}, ErrConflict
	}
	out, err := c.request(ctx, "GET", "/v1/config/"+id, nil)
	if out.MailboxID != "" && out.MailboxID != id {
		return Receipt{}, ErrUnavailable
	}
	return out, err
}
func (c *Client) Kick(ctx context.Context, session, box string) error {
	if !uuid.MatchString(session) || box != "" && !uuid.MatchString(box) {
		return ErrConflict
	}
	_, err := c.request(ctx, "POST", "/v1/sessions/kick", map[string]string{"session_id": session, "mailbox_id": box})
	return err
}

// OutboundStatus never returns an endpoint, configuration or credentials.
func (c *Client) OutboundStatus(ctx context.Context) (string, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/outbound/status", nil)
	if err != nil {
		return "unknown", ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(r)
	if err != nil {
		return "unknown", ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "unknown", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(data) > 4096 {
		return "unknown", ErrUnavailable
	}
	var result struct {
		Status string `json:"status"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil {
		return "unknown", ErrUnavailable
	}
	var extra any
	if d.Decode(&extra) != io.EOF || result.Status != "not-configured" && result.Status != "unverified" && result.Status != "unknown" {
		return "unknown", ErrUnavailable
	}
	return result.Status, nil
}

// OutboundQueue is a coarse, read-only Postfix observation. It does not expose
// queue identifiers or establish that a subsequent route change is safe.
func (c *Client) OutboundQueue(ctx context.Context) (string, error) {
	r, err := http.NewRequestWithContext(ctx, "GET", c.base+"/v1/outbound/queue", nil)
	if err != nil {
		return "unknown", ErrUnavailable
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(r)
	if err != nil {
		return "unknown", ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "unknown", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(data) > 4096 {
		return "unknown", ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(data))
	open, err := d.Token()
	if err != nil || open != json.Delim('{') || !d.More() {
		return "unknown", ErrUnavailable
	}
	key, err := d.Token()
	if err != nil || key != "status" {
		return "unknown", ErrUnavailable
	}
	var status string
	if d.Decode(&status) != nil || d.More() {
		return "unknown", ErrUnavailable
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return "unknown", ErrUnavailable
	}
	var extra any
	if d.Decode(&extra) != io.EOF || status != "queued-observed" && status != "none-observed" && status != "unknown" {
		return "unknown", ErrUnavailable
	}
	return status, nil
}
