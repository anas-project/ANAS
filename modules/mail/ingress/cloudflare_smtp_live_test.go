// TEST_CASES: MAILMOD-T-010
package ingress

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/mail"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The outer driver deploys the unmodified production Worker, sends one real
// SMTP transaction and independently removes its isolated resources. This test
// never synthesizes an EmailEvent or a DMS receipt. It stops before delivery.
func TestCloudflareSMTPSourceLive(t *testing.T) {
	path := os.Getenv("MAIL_CLOUDFLARE_SMTP_CONFIG")
	if path == "" {
		t.Skip("explicit real SMTP/source fixture required; no cloud acceptance pass")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		t.Fatal("private SMTP/source fixture required")
	}
	var cfg struct {
		Source                                                    SourceConfig
		Zone, Worker, RoutingToken, Operation, Subject, VisibleTo string
		Routes                                                    []Route
	}
	raw, e := os.ReadFile(path)
	domains := map[string]string{"0d84033826bc3cd0c283efdb57a90f7a": "mail-poc.bengbu.cc.cd", "cb14d4de75d3286765c7864476f9f4fb": "hlong.wang"}
	if e != nil || json.Unmarshal(raw, &cfg) != nil || domains[cfg.Zone] == "" || !strings.HasPrefix(cfg.Worker, "anas-mail-smtp-live-") || len(cfg.Routes) != 3 || len(cfg.Source.Domains) != 1 || cfg.Source.Domains[0] != domains[cfg.Zone] || !idPattern.MatchString(cfg.Operation) || cfg.Subject != "ANAS cloud SMTP "+cfg.Source.ID {
		t.Fatal("authorized SMTP/source fixture required")
	}
	for _, r := range cfg.Routes {
		if !strings.HasPrefix(r.Address, "smtp-") || !strings.HasSuffix(r.Address, "@"+domains[cfg.Zone]) {
			t.Fatal("isolated recipient required")
		}
	}
	if cfg.VisibleTo == "" {
		cfg.VisibleTo = "forged@foreign.example"
	}
	if cfg.VisibleTo != "forged@foreign.example" && cfg.VisibleTo != cfg.Routes[0].Address {
		t.Fatal("controlled visible recipient required")
	}
	client, e := NewSourceClient(cfg.Source)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	client.http.Transport = &cloudSMTPTransport{testing: t, base: client.http.Transport}
	ctx, cancel := context.WithTimeout(t.Context(), 360*time.Second)
	defer cancel()
	var state RouteState
	readyBy := time.Now().Add(90 * time.Second)
	readyContext, stopReadiness := context.WithDeadline(ctx, readyBy)
	defer stopReadiness()
	for {
		state, e = client.ReadRoutes(readyContext)
		if e == nil || !errors.Is(e, ErrUnavailable) || time.Now().After(readyBy) {
			break
		}
		t.Log("NAS source readiness transient; retrying bounded read")
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
	if e != nil || state.Revision != 0 || len(state.Bindings) != 0 {
		t.Fatal("fresh source required", e)
	}
	if _, e = client.ReplaceRoutes(ctx, 0, cfg.Operation, cfg.Routes); e != nil {
		t.Fatal("source route write", e)
	}
	state, e = client.ReadRoutes(ctx)
	fp, _ := RouteFingerprint(cfg.Routes)
	if e != nil || state.Revision != 1 || state.OperationID != cfg.Operation || state.Fingerprint != fp || len(state.Bindings) != 3 {
		t.Fatal("source route readback", e)
	}
	rules, e := NewCloudflareRules(client, cfg.Zone, cfg.Worker, cfg.RoutingToken)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range cfg.Routes {
		created, err := rules.Create(ctx, r.Address)
		if err != nil {
			t.Fatal("isolated public recipient create", err)
		}
		read, err := rules.Read(ctx, created.ID)
		if err != nil || !rules.owns(read) || read.Matchers[0].Value != r.Address {
			t.Fatal("public recipient readback", err)
		}
	}
	// The line is a non-secret synchronization signal, not a test verdict.
	fmt.Println("ANAS_CLOUD_SMTP_ROUTES_READY")
	// Leave the real source without a consumer for over one minute. The
	// orchestrator remains responsive and sends the SMTP message meanwhile.
	quiet := time.NewTimer(70 * time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer quiet.Stop()
	defer heartbeat.Stop()
waiting:
	for {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-heartbeat.C:
			t.Log("cloud retention wait; consumer remains stopped")
		case <-quiet.C:
			break waiting
		}
	}
	var messages []SourceMessage
	previousCount := -1
	for {
		page, err := client.List(ctx, "")
		if errors.Is(err, ErrUnavailable) && ctx.Err() == nil {
			t.Log("transient source listing failure; bounded read retry")
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(3 * time.Second):
			}
			continue
		}
		if err != nil {
			t.Fatal("real source listing", err)
		}
		if page.Cursor != nil || len(page.Messages) > 3 {
			t.Fatal("unexpected source recipients/pagination")
		}
		messages = page.Messages
		if len(messages) != previousCount {
			previousCount = len(messages)
			t.Log("real source received target count", previousCount)
		}
		if len(messages) == 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real SMTP did not reach all three targets", "received", len(messages))
		case <-time.After(3 * time.Second):
		}
	}
	wanted := map[string]Route{}
	for _, r := range cfg.Routes {
		wanted[r.Address] = r
	}
	seen := map[string]bool{}
	for _, message := range messages {
		m := message.Metadata
		r, ok := wanted[m.AcceptedAddress]
		if !ok || seen[m.AcceptedAddress] || m.MailboxID != r.MailboxID || m.OwnerAnchor != r.OwnerAnchor || m.ClaimVersion != r.ClaimVersion || message.Delivery != nil || message.Notification == nil || message.Notification.State != "unconfigured" {
			t.Fatal("real envelope target or undelivered state changed")
		}
		seen[m.AcceptedAddress] = true
		var first, repeat bytes.Buffer
		digest, err := client.Download(ctx, message, &first)
		if err != nil {
			t.Fatal("real SMTP original download", err)
		}
		again, err := client.Download(ctx, message, &repeat)
		if err != nil || again != digest || !bytes.Equal(first.Bytes(), repeat.Bytes()) {
			t.Fatal("undelivered original changed", err)
		}
		verifyCloudSMTPMIME(t, first.Bytes(), cfg.Subject, cfg.VisibleTo)
		t.Log("trusted SMTP target", r.Address, "mailbox", r.MailboxID, "bytes", first.Len(), "sha256", digest)
	}
	// Read-only re-listing and repeated downloads must not invent delivery.
	page, e := client.List(ctx, "")
	if e != nil || len(page.Messages) != 3 {
		t.Fatal("retained source originals missing", e)
	}
	for _, m := range page.Messages {
		if m.Delivery != nil {
			t.Fatal("read implied final delivery")
		}
	}
	t.Log("real SMTP/R2/Go source subset passed; no PG/native receipt, notification or deployment verdict")
}

// Private test diagnostics expose status and a bounded protocol error code,
// never request headers, credentials or provider response bodies.
type cloudSMTPTransport struct {
	testing *testing.T
	base    http.RoundTripper
}

func (p *cloudSMTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := p.base.RoundTrip(r)
	if err != nil {
		p.testing.Log("source transport failed before HTTP", fmt.Sprintf("%T", err))
	} else if response.StatusCode != 200 {
		p.testing.Log("source HTTP status", response.StatusCode, "path", r.URL.Path)
		body, e := io.ReadAll(io.LimitReader(response.Body, 4097))
		response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		var failure struct {
			Error string `json:"error"`
		}
		if e == nil && len(body) <= 4096 && json.Unmarshal(body, &failure) == nil && regexp.MustCompile(`^[a-z-]{1,64}$`).MatchString(failure.Error) {
			p.testing.Log("source protocol error", failure.Error)
		}
	}
	return response, err
}

func (p *cloudSMTPTransport) CloseIdleConnections() {
	if closer, ok := p.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func verifyCloudSMTPMIME(t *testing.T, raw []byte, subject, visibleTo string) {
	t.Helper()
	message, e := mail.ReadMessage(bytes.NewReader(raw))
	if e != nil || message.Header.Get("Subject") != subject || message.Header.Get("To") != visibleTo {
		t.Fatal("original MIME headers changed", e)
	}
	kind, params, e := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if e != nil || kind != "multipart/mixed" {
		t.Fatal("original MIME structure", e)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	body, attachment := false, false
	for {
		part, err := parts.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var reader io.Reader = part
		if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "base64") {
			reader = base64.NewDecoder(base64.StdEncoding, reader)
		}
		content, err := io.ReadAll(io.LimitReader(reader, 4097))
		if err != nil || len(content) > 4096 {
			t.Fatal("bounded MIME part", err)
		}
		body = body || strings.TrimSpace(string(content)) == "ANAS real Cloudflare SMTP body"
		attachment = attachment || part.FileName() == "proof.bin" && bytes.Equal(content, []byte{0, 1, 2, 255})
	}
	if !body || !attachment {
		t.Fatal("original body/attachment missing")
	}
}
