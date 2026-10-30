// Package cloudrelay contains candidate components for the VPS mail source.
// It does not yet implement a production relay, persistent retries or receipt.
package cloudrelay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const notificationSchema = "anas.mail-notification/v1"

var identifier = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var secretValue = regexp.MustCompile(`^[a-f0-9]{64}$`)
var messageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var ErrConfiguration = errors.New("notification configuration invalid")

// NotificationConfig is private configuration. Reader and Control are checked
// only to ensure that the notification credential is independently scoped.
type NotificationConfig struct {
	Origin, SourceID, Generation, Secret, Reader, Control string
}

type Notification struct {
	ID, MessageID, MailboxID string
}

// NotificationResult is one attempt, not evidence that mail was delivered.
// Durable attempt claims, retry scheduling and delivery receipts must be owned
// by the VPS source; this component never starts or schedules attempts itself.
type NotificationResult struct {
	State, Check string
	HTTPStatus   int
}

type Notifier struct {
	origin, source, generation string
	secret                     []byte
	client                     *http.Client
}

func NewNotifier(cfg NotificationConfig) (*Notifier, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newNotifier(cfg, transport)
}

func newNotifier(cfg NotificationConfig, transport http.RoundTripper) (*Notifier, error) {
	u, err := url.Parse(cfg.Origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.HasSuffix(u.Host, ":") || len(cfg.Origin) > 300 || strings.ContainsAny(cfg.Origin, "\x00\r\n") || !identifier.MatchString(cfg.SourceID) || !identifier.MatchString(cfg.Generation) || !secretValue.MatchString(cfg.Secret) || !secretValue.MatchString(cfg.Reader) || !secretValue.MatchString(cfg.Control) || cfg.Secret == cfg.Reader || cfg.Secret == cfg.Control || cfg.Reader == cfg.Control || transport == nil {
		return nil, ErrConfiguration
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, ErrConfiguration
		}
	}
	secret, _ := hex.DecodeString(cfg.Secret)
	return &Notifier{origin: cfg.Origin, source: cfg.SourceID, generation: cfg.Generation, secret: secret,
		client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// A notification acknowledgement must have exactly one of each protocol key.
// encoding/json otherwise accepts duplicate and case-folded object keys.
func notificationReceiptFields(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	open, err := d.Token()
	if err != nil || open != json.Delim('{') {
		return false
	}
	keys := map[string]bool{"schema": false, "source_id": false, "generation": false, "notification_id": false, "accepted": false}
	for d.More() {
		item, err := d.Token()
		name, ok := item.(string)
		seen, allowed := keys[name]
		if err != nil || !ok || !allowed || seen {
			return false
		}
		keys[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return false
	}
	if _, err = d.Token(); err != io.EOF {
		return false
	}
	for _, seen := range keys {
		if !seen {
			return false
		}
	}
	return true
}

func (n *Notifier) Send(ctx context.Context, notice Notification, now time.Time) NotificationResult {
	if n == nil || !identifier.MatchString(notice.ID) || !identifier.MatchString(notice.MailboxID) || !messageID.MatchString(notice.MessageID) || now.IsZero() {
		return NotificationResult{State: "configuration-error", Check: "invalid-notice"}
	}
	body, err := json.Marshal(struct {
		Schema     string `json:"schema"`
		SourceID   string `json:"source_id"`
		Generation string `json:"generation"`
		ID         string `json:"notification_id"`
		MessageID  string `json:"message_id"`
		MailboxID  string `json:"mailbox_id"`
	}{notificationSchema, n.source, n.generation, notice.ID, notice.MessageID, notice.MailboxID})
	if err != nil {
		return NotificationResult{State: "configuration-error", Check: "invalid-notice"}
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, n.secret)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, n.origin+"/api/ingress/notify", bytes.NewReader(body))
	if err != nil {
		return NotificationResult{State: "configuration-error", Check: "invalid-notice"}
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-ANAS-Mail-Time", timestamp)
	r.Header.Set("X-ANAS-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := n.client.Do(r)
	if err != nil {
		check := "unreachable"
		if errors.Is(err, context.DeadlineExceeded) {
			check = "timeout"
		}
		return NotificationResult{State: "failed", Check: check}
	}
	defer response.Body.Close()
	code := response.StatusCode
	if code == 200 || code == 202 {
		media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1025))
		var receipt struct {
			Schema     string `json:"schema"`
			SourceID   string `json:"source_id"`
			Generation string `json:"generation"`
			ID         string `json:"notification_id"`
			Accepted   bool   `json:"accepted"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var extra any
		if e != nil || media != "application/json" || len(response.Header.Values("Content-Type")) != 1 || readErr != nil || len(raw) > 1024 || !notificationReceiptFields(raw) || decoder.Decode(&receipt) != nil || decoder.Decode(&extra) != io.EOF || receipt.Schema != notificationSchema || receipt.SourceID != n.source || receipt.Generation != n.generation || receipt.ID != notice.ID || !receipt.Accepted {
			return NotificationResult{State: "configuration-error", Check: "invalid-receipt", HTTPStatus: code}
		}
		return NotificationResult{State: "notified", HTTPStatus: code}
	}
	state := "failed"
	if code >= 300 && code < 500 && code != 408 && code != 429 {
		state = "configuration-error"
	}
	return NotificationResult{State: state, Check: "http-response", HTTPStatus: code}
}
