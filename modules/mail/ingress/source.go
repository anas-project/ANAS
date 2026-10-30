// Package ingress consumes the two managed cloud sources through their fixed
// mail-source protocol. It does not accept raw download URLs, use Docker or
// infer recipients from message headers.
package ingress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/dms"
)

var (
	ErrConfiguration  = errors.New("mail source configuration invalid")
	ErrUnavailable    = errors.New("mail source unavailable")
	ErrAuthentication = errors.New("mail source credentials rejected")
	ErrIdentity       = errors.New("mail source identity or protocol changed")
	ErrIntegrity      = errors.New("mail source content changed")
	ErrGone           = errors.New("mail source copy is no longer retained")
	idPattern         = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
	secretPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
	etagPattern       = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	messagePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

const sourceSchema = "anas.mail-source/v1"

type SourceConfig struct {
	ID, Generation, Origin, Token string
	// ControlToken can edit only this source's receiving bindings. It is not a
	// Cloudflare management credential and is distinct from the reader token.
	ControlToken string
	Domains      []string
	IPv4Only     bool
	// Proxy is an optional private runtime value, never a browser draft field.
	Proxy        string
	AdditionalCA []byte
}
type SourceClient struct {
	cfg  SourceConfig
	http *http.Client
}
type SourceDelivery struct {
	ReceiptID      string    `json:"receipt_id"`
	SHA256         string    `json:"sha256"`
	State          string    `json:"state"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}
type SourceMessage struct {
	Metadata     dms.IntakeMetadata  `json:"metadata"`
	Sequence     int64               `json:"sequence"`
	Bytes        int64               `json:"bytes"`
	ETag         string              `json:"etag"`
	ReceivedAt   time.Time           `json:"received_at"`
	Delivery     *SourceDelivery     `json:"delivery,omitempty"`
	Notification *SourceNotification `json:"notification,omitempty"`
}
type SourceNotification struct {
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	Check         string     `json:"check,omitempty"`
	HTTPStatus    int        `json:"http_status,omitempty"`
	NotifiedAt    *time.Time `json:"notified_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}
type SourcePage struct {
	Schema        string          `json:"schema"`
	SourceID      string          `json:"source_id"`
	Generation    string          `json:"generation"`
	RouteRevision int64           `json:"route_revision"`
	Messages      []SourceMessage `json:"messages"`
	Cursor        *string         `json:"cursor"`
}

func NewSourceClient(cfg SourceConfig) (*SourceClient, error) {
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(cfg.Origin, "\x00\r\n") || !idPattern.MatchString(cfg.ID) || !idPattern.MatchString(cfg.Generation) || !secretPattern.MatchString(cfg.Token) || len(cfg.Domains) == 0 || len(cfg.Domains) > 100 {
		return nil, ErrConfiguration
	}
	if cfg.ControlToken != "" && (!secretPattern.MatchString(cfg.ControlToken) || cfg.ControlToken == cfg.Token) {
		return nil, ErrConfiguration
	}
	seen := map[string]bool{}
	for _, domain := range cfg.Domains {
		canonical, err := directorymail.Domain(domain, "")
		if err != nil || canonical != domain || seen[domain] {
			return nil, ErrConfiguration
		}
		seen[domain] = true
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if len(cfg.AdditionalCA) > 4<<20 || len(cfg.AdditionalCA) > 0 && !roots.AppendCertsFromPEM(cfg.AdditionalCA) {
		return nil, ErrConfiguration
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if cfg.Proxy != "" {
		proxy, err := url.Parse(cfg.Proxy)
		if err != nil || proxy.Hostname() == "" || proxy.Scheme != "http" && proxy.Scheme != "https" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h" || proxy.Path != "" || proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" || strings.ContainsAny(cfg.Proxy, "\x00\r\n") {
			return nil, ErrConfiguration
		}
		transport.Proxy = http.ProxyURL(proxy)
	}
	if cfg.IPv4Only {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", address)
		}
	}
	// This workload uses one bounded stream per source. Explicit HTTP/1 also
	// avoids inheriting a warmed default transport's HTTP/2 ALPN state.
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, NextProtos: []string{"http/1.1"}}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.ResponseHeaderTimeout = 10 * time.Second
	// Polling is normally idle for a minute. Do not carry an unused TCP/proxy
	// connection across that interval: NAS routers may silently discard it.
	// Active requests retain their existing deadlines and verification rules.
	transport.IdleConnTimeout = 30 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	cfg.Domains = append([]string(nil), cfg.Domains...)
	cfg.AdditionalCA = append([]byte(nil), cfg.AdditionalCA...)
	return &SourceClient{cfg: cfg, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *SourceClient) Close() { c.http.CloseIdleConnections() }
func (c *SourceClient) validMessage(message SourceMessage) bool {
	m := message.Metadata
	if m.SourceID != c.cfg.ID || message.Sequence < 1 || message.Sequence > 9007199254740991 || message.Bytes < 1 || message.Bytes > dms.IntakeMaxBytes || !etagPattern.MatchString(message.ETag) || message.ReceivedAt.IsZero() || message.ReceivedAt.After(time.Now().Add(5*time.Minute)) {
		return false
	}
	if m.SHA256 != "" && !secretPattern.MatchString(m.SHA256) {
		return false
	}
	// SHA is computed during the bounded download if not yet supplied by the
	// source; all remaining native metadata is validated before any I/O.
	m.SHA256 = strings.Repeat("0", 64)
	if m.Validate() != nil {
		return false
	}
	domain := strings.Split(m.AcceptedAddress, "@")[1]
	allowed := false
	for _, candidate := range c.cfg.Domains {
		allowed = allowed || candidate == domain
	}
	if !allowed {
		return false
	}
	if delivery := message.Delivery; delivery != nil {
		if delivery.ReceiptID != m.Key() || !secretPattern.MatchString(delivery.SHA256) || delivery.State != "stored" && delivery.State != "processed" || delivery.AcknowledgedAt.IsZero() || delivery.AcknowledgedAt.Before(message.ReceivedAt) || delivery.AcknowledgedAt.After(time.Now().Add(5*time.Minute)) || message.Metadata.SHA256 != "" && message.Metadata.SHA256 != delivery.SHA256 {
			return false
		}
	}
	if n := message.Notification; n != nil {
		switch n.State {
		case "unconfigured", "configuration-error", "pending", "sending", "failed", "notified", "exhausted", "unknown":
		default:
			return false
		}
		switch n.Check {
		case "", "timeout", "unreachable", "http-response", "invalid-receipt":
		default:
			return false
		}
		if n.Attempts < 0 || n.Attempts > 3 || n.HTTPStatus != 0 && (n.HTTPStatus < 100 || n.HTTPStatus > 599) {
			return false
		}
		if n.State == "notified" && (n.NotifiedAt == nil || n.NotifiedAt.IsZero() || n.NotifiedAt.Before(message.ReceivedAt) || n.NotifiedAt.After(time.Now().Add(5*time.Minute))) {
			return false
		}
		if n.State != "notified" && n.NotifiedAt != nil {
			return false
		}
		if n.NextAttemptAt != nil && n.NextAttemptAt.IsZero() {
			return false
		}
	}
	return true
}

// Message reads one authenticated notification target. It never downloads a
// caller-supplied URL or derives ownership from the notification itself.
func (c *SourceClient) Message(ctx context.Context, id, target string) (SourceMessage, error) {
	if !messagePattern.MatchString(id) || !idPattern.MatchString(target) {
		return SourceMessage{}, ErrConfiguration
	}
	response, err := c.request(ctx, "GET", "/v1/messages/"+url.PathEscape(id)+"?target="+url.QueryEscape(target), nil)
	if err != nil {
		return SourceMessage{}, err
	}
	defer response.Body.Close()
	var result struct {
		Schema        string        `json:"schema"`
		SourceID      string        `json:"source_id"`
		Generation    string        `json:"generation"`
		RouteRevision int64         `json:"route_revision"`
		Message       SourceMessage `json:"message"`
	}
	if err = sourceBoundedJSON(response.Body, &result, 4096); err != nil {
		return SourceMessage{}, err
	}
	if result.Schema != sourceSchema || result.SourceID != c.cfg.ID || result.Generation != c.cfg.Generation || result.RouteRevision < 0 || result.RouteRevision > 9007199254740991 || !c.validMessage(result.Message) || result.Message.Metadata.MessageID != id || result.Message.Metadata.MailboxID != target {
		return SourceMessage{}, ErrIdentity
	}
	return result.Message, nil
}
func sourceStatus(status int) error {
	switch status {
	case 200:
		return nil
	case 401, 403:
		return ErrAuthentication
	case 404, 410:
		return ErrGone
	case 409:
		return ErrIdentity
	default:
		return ErrUnavailable
	}
}
func (c *SourceClient) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	return c.authenticatedRequest(ctx, method, path, body, c.cfg.Token)
}
func (c *SourceClient) authenticatedRequest(ctx context.Context, method, path string, body []byte, token string) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.cfg.Origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, ErrConfiguration
	}
	r.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = sourceStatus(response.StatusCode); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}
func sourceJSON(body io.Reader, target any) error {
	return sourceBoundedJSON(body, target, 1<<20)
}
func sourceBoundedJSON(body io.Reader, target any, limit int64) error {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return ErrUnavailable
	}
	if int64(len(raw)) > limit {
		return ErrIdentity
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(target) != nil || d.Decode(&extra) != io.EOF {
		return ErrIdentity
	}
	return nil
}
func (c *SourceClient) List(ctx context.Context, cursor string) (SourcePage, error) {
	if len(cursor) > 2048 || strings.ContainsAny(cursor, "\x00\r\n") {
		return SourcePage{}, ErrConfiguration
	}
	query := url.Values{"limit": []string{"100"}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	response, err := c.request(ctx, "GET", "/v1/messages?"+query.Encode(), nil)
	if err != nil {
		return SourcePage{}, err
	}
	defer response.Body.Close()
	var page SourcePage
	if err = sourceJSON(response.Body, &page); err != nil {
		return SourcePage{}, err
	}
	if page.Schema != sourceSchema || page.SourceID != c.cfg.ID || page.Generation != c.cfg.Generation || page.RouteRevision < 0 || page.RouteRevision > 9007199254740991 || len(page.Messages) > 100 || page.Messages == nil {
		return SourcePage{}, ErrIdentity
	}
	if page.Cursor != nil && (len(*page.Cursor) == 0 || len(*page.Cursor) > 2048 || *page.Cursor == cursor || strings.ContainsAny(*page.Cursor, "\x00\r\n")) {
		return SourcePage{}, ErrIdentity
	}
	seen := map[string]bool{}
	for _, m := range page.Messages {
		key := m.Metadata.Key()
		if !c.validMessage(m) || seen[key] {
			return SourcePage{}, ErrIdentity
		}
		seen[key] = true
	}
	return page, nil
}
func messagePath(message SourceMessage, action string) string {
	return "/v1/messages/" + url.PathEscape(message.Metadata.MessageID) + "/" + action + "?target=" + url.QueryEscape(message.Metadata.MailboxID)
}

// Download writes only to the caller's unpublished staging file. A partial or
// changed response must never be published or sent to DMS by the caller.
func (c *SourceClient) Download(ctx context.Context, message SourceMessage, destination io.Writer) (string, error) {
	if !c.validMessage(message) || destination == nil {
		return "", ErrConfiguration
	}
	response, err := c.request(ctx, "GET", messagePath(message, "raw"), nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.Header.Get("ETag") != strconv.Quote(message.ETag) || response.ContentLength >= 0 && response.ContentLength != message.Bytes {
		return "", ErrIntegrity
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(destination, h), io.LimitReader(response.Body, message.Bytes+1))
	if err != nil {
		return "", ErrUnavailable
	}
	if size != message.Bytes {
		return "", ErrIntegrity
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if message.Metadata.SHA256 != "" && digest != message.Metadata.SHA256 || message.Delivery != nil && digest != message.Delivery.SHA256 {
		return "", ErrIntegrity
	}
	return digest, nil
}
func (c *SourceClient) Acknowledge(ctx context.Context, message SourceMessage, receipt dms.IntakeReceipt) error {
	if !c.validMessage(message) || receipt.ID != message.Metadata.Key() || receipt.MailboxID != message.Metadata.MailboxID || !secretPattern.MatchString(receipt.SHA256) || receipt.State != "stored" && receipt.State != "processed" || message.Metadata.SHA256 != "" && receipt.SHA256 != message.Metadata.SHA256 || message.Delivery != nil && receipt.SHA256 != message.Delivery.SHA256 {
		return ErrConfiguration
	}
	input := struct {
		SourceID   string `json:"source_id"`
		Generation string `json:"generation"`
		Receipt    struct {
			ID        string `json:"id"`
			MailboxID string `json:"mailbox_id"`
			SHA256    string `json:"sha256"`
			State     string `json:"state"`
		} `json:"receipt"`
	}{SourceID: c.cfg.ID, Generation: c.cfg.Generation}
	input.Receipt.ID, input.Receipt.MailboxID, input.Receipt.SHA256, input.Receipt.State = receipt.ID, receipt.MailboxID, receipt.SHA256, receipt.State
	raw, _ := json.Marshal(input)
	response, err := c.request(ctx, "POST", messagePath(message, "ack"), raw)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		Schema     string         `json:"schema"`
		SourceID   string         `json:"source_id"`
		Generation string         `json:"generation"`
		MessageID  string         `json:"message_id"`
		MailboxID  string         `json:"mailbox_id"`
		Delivery   SourceDelivery `json:"delivery"`
	}
	if err = sourceJSON(response.Body, &result); err != nil {
		return err
	}
	if result.Schema != sourceSchema || result.SourceID != c.cfg.ID || result.Generation != c.cfg.Generation || result.MessageID != message.Metadata.MessageID || result.MailboxID != receipt.MailboxID || result.Delivery.ReceiptID != receipt.ID || result.Delivery.SHA256 != receipt.SHA256 || result.Delivery.State != "stored" && result.Delivery.State != "processed" || result.Delivery.AcknowledgedAt.IsZero() || result.Delivery.AcknowledgedAt.Before(message.ReceivedAt) || result.Delivery.AcknowledgedAt.After(time.Now().Add(5*time.Minute)) {
		return ErrIdentity
	}
	return nil
}
