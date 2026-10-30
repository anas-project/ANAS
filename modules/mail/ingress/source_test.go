// TEST_CASES: MAILMOD-T-008
package ingress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
)

func sourceFixture(t *testing.T, mode string) (*SourceClient, SourceMessage, string, *atomic.Int32) {
	t.Helper()
	raw := "From: sender@example.net\r\nTo: forged@example.net\r\nSubject: cloud 原文\r\n\r\nMessage body.\r\n"
	sum := sha256.Sum256([]byte(raw))
	digest := hex.EncodeToString(sum[:])
	const source = "11111111-2222-4333-8444-555555555555"
	const generation = "22222222-2222-4222-8222-222222222222"
	m := SourceMessage{Metadata: dms.IntakeMetadata{SourceID: source, MessageID: "message-1", MailboxID: "33333333-3333-4333-8333-333333333333", OwnerAnchor: "44444444-4444-4444-8444-444444444444", AcceptedAddress: "alice@example.test", ClaimVersion: "55555555-5555-4555-8555-555555555555", EnvelopeFrom: "sender@example.net"}, Sequence: 1, Bytes: int64(len(raw)), ETag: digest, ReceivedAt: time.Now().UTC().Add(-time.Minute)}
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.ProtoMajor != 1 {
			t.Error("source negotiated unsupported/warmed HTTP2")
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("b", 64) || strings.Contains(r.URL.String(), strings.Repeat("b", 64)) {
			t.Error("source authentication leaked or missing")
		}
		if mode == "authentication" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/messages/message-1":
			if r.URL.Query().Get("target") != m.Metadata.MailboxID {
				w.WriteHeader(404)
				return
			}
			out := m
			if mode == "wrong-message-target" {
				out.Metadata.MailboxID = generation
			}
			json.NewEncoder(w).Encode(map[string]any{"schema": sourceSchema, "source_id": source, "generation": generation, "route_revision": 1, "message": out})
		case "/v1/messages":
			if mode == "redirect" {
				w.Header().Set("Location", "https://foreign.example.test/collect")
				w.WriteHeader(302)
				return
			}
			p := SourcePage{Schema: sourceSchema, SourceID: source, Generation: generation, RouteRevision: 1, Messages: []SourceMessage{m}}
			switch mode {
			case "wrong-source":
				p.SourceID = generation
			case "wrong-generation":
				p.Generation = source
			case "foreign-domain":
				p.Messages[0].Metadata.AcceptedAddress = "alice@foreign.example"
			case "duplicate":
				p.Messages = append(p.Messages, m)
			case "noncanonical-owner":
				p.Messages[0].Metadata.OwnerAnchor = "alice"
			case "stalled-cursor":
				cursor := "same"
				p.Cursor = &cursor
			case "unknown-field":
				w.Write([]byte(`{"schema":"anas.mail-source/v1","private_token":"never-echo"}`))
				return
			}
			json.NewEncoder(w).Encode(p)
		case "/v1/messages/message-1/raw":
			if r.URL.Query().Get("target") != m.Metadata.MailboxID {
				t.Error("raw request lost target binding")
			}
			if mode == "gone" {
				w.WriteHeader(410)
				return
			}
			etag := m.ETag
			if mode == "changed-etag" {
				etag = "changed"
			}
			w.Header().Set("ETag", strconv.Quote(etag))
			body := raw
			if mode == "truncated" {
				body = raw[:len(raw)-1]
			}
			if mode == "oversized" {
				body = raw + "extra"
			}
			if mode == "truncated" || mode == "oversized" {
				w.(http.Flusher).Flush()
			}
			io.WriteString(w, body)
		case "/v1/messages/message-1/ack":
			var input struct {
				SourceID   string            `json:"source_id"`
				Generation string            `json:"generation"`
				Receipt    dms.IntakeReceipt `json:"receipt"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.SourceID != source || input.Generation != generation || input.Receipt.ID != m.Metadata.Key() {
				t.Error("wrong native receipt")
			}
			result := map[string]any{"schema": sourceSchema, "source_id": source, "generation": generation, "message_id": m.Metadata.MessageID, "mailbox_id": m.Metadata.MailboxID, "delivery": SourceDelivery{ReceiptID: m.Metadata.Key(), SHA256: digest, State: "stored", AcknowledgedAt: time.Now().UTC()}}
			if mode == "wrong-ack" {
				result["mailbox_id"] = generation
			}
			json.NewEncoder(w).Encode(result)
		default:
			w.WriteHeader(404)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	c, err := NewSourceClient(SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: strings.Repeat("b", 64), Domains: []string{"example.test"}, AdditionalCA: cert, IPv4Only: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, m, raw, &requests
}
func TestSourceClientActualTLSDownloadAndBoundFinalAcknowledgement(t *testing.T) {
	c, m, raw, _ := sourceFixture(t, "")
	page, err := c.List(t.Context(), "")
	if err != nil || len(page.Messages) != 1 {
		t.Fatal("source list", err)
	}
	var staged bytes.Buffer
	digest, err := c.Download(t.Context(), page.Messages[0], &staged)
	if err != nil || staged.String() != raw {
		t.Fatal("raw stream changed", err)
	}
	receipt := dms.IntakeReceipt{ID: m.Metadata.Key(), MailboxID: m.Metadata.MailboxID, SHA256: digest, State: "stored"}
	if err = c.Acknowledge(t.Context(), m, receipt); err != nil {
		t.Fatal("native acknowledgement", err)
	}
}

func TestSourceNotificationLookupRechecksTrustedTarget(t *testing.T) {
	c, m, _, _ := sourceFixture(t, "")
	if got, e := c.Message(t.Context(), m.Metadata.MessageID, m.Metadata.MailboxID); e != nil || got.Metadata != m.Metadata {
		t.Fatal("message lookup", e)
	}
	c, m, _, _ = sourceFixture(t, "wrong-message-target")
	if _, e := c.Message(t.Context(), m.Metadata.MessageID, m.Metadata.MailboxID); !errors.Is(e, ErrIdentity) {
		t.Fatal("foreign notification target accepted", e)
	}
	if _, e := c.Message(t.Context(), "../foreign", m.Metadata.MailboxID); !errors.Is(e, ErrConfiguration) {
		t.Fatal("notification path accepted", e)
	}
}
func TestSourceClientRejectsForeignNamespaceProtocolAndCursor(t *testing.T) {
	for _, mode := range []string{"wrong-source", "wrong-generation", "foreign-domain", "duplicate", "noncanonical-owner", "stalled-cursor", "unknown-field", "authentication", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			c, _, _, _ := sourceFixture(t, mode)
			cursor := ""
			if mode == "stalled-cursor" {
				cursor = "same"
			}
			_, err := c.List(t.Context(), cursor)
			expected := ErrIdentity
			if mode == "authentication" {
				expected = ErrAuthentication
			}
			if mode == "redirect" {
				expected = ErrUnavailable
			}
			if !errors.Is(err, expected) || strings.Contains(err.Error(), "never-echo") {
				t.Fatal("unsafe source page accepted", err)
			}
		})
	}
}
func TestSourceDownloadNeverPublishesChangedTruncatedOrExpiredContent(t *testing.T) {
	for _, mode := range []string{"changed-etag", "truncated", "oversized", "gone", "changed-sha"} {
		t.Run(mode, func(t *testing.T) {
			c, m, _, _ := sourceFixture(t, mode)
			if mode == "changed-sha" {
				m.Metadata.SHA256 = strings.Repeat("f", 64)
			}
			var staged bytes.Buffer
			_, err := c.Download(t.Context(), m, &staged)
			expected := ErrIntegrity
			if mode == "gone" {
				expected = ErrGone
			}
			if !errors.Is(err, expected) {
				t.Fatal("bad raw response accepted", err)
			}
		})
	}
}
func TestSourceAcknowledgementCannotUseQueueAcceptanceOrAnotherBox(t *testing.T) {
	c, m, raw, count := sourceFixture(t, "")
	sum := sha256.Sum256([]byte(raw))
	receipt := dms.IntakeReceipt{ID: m.Metadata.Key(), MailboxID: m.Metadata.MailboxID, SHA256: hex.EncodeToString(sum[:]), State: "queued"}
	if err := c.Acknowledge(t.Context(), m, receipt); !errors.Is(err, ErrConfiguration) || count.Load() != 0 {
		t.Fatal("queued message acknowledged upstream", err)
	}
	receipt.State = "stored"
	receipt.MailboxID = c.cfg.Generation
	if err := c.Acknowledge(t.Context(), m, receipt); !errors.Is(err, ErrConfiguration) || count.Load() != 0 {
		t.Fatal("foreign mailbox acknowledged")
	}
	c, m, _, _ = sourceFixture(t, "wrong-ack")
	receipt.MailboxID = m.Metadata.MailboxID
	if err := c.Acknowledge(t.Context(), m, receipt); !errors.Is(err, ErrIdentity) {
		t.Fatal("foreign source receipt accepted", err)
	}
}
func TestSourceRejectsUnsafeEndpointsAndNeverFollowsRedirectsOrDisablesTrust(t *testing.T) {
	c, _, _, _ := sourceFixture(t, "")
	cfg := c.cfg
	for _, origin := range []string{"http://source.example.test", "https://user:password@source.example.test", "https://source.example.test/path", "https://source.example.test/?token=secret"} {
		bad := cfg
		bad.Origin = origin
		if _, err := NewSourceClient(bad); !errors.Is(err, ErrConfiguration) {
			t.Fatal("unsafe origin accepted")
		}
	}
	bad := cfg
	bad.Token = "cloudflare-management-token"
	if _, err := NewSourceClient(bad); !errors.Is(err, ErrConfiguration) {
		t.Fatal("management token accepted as a runtime reader")
	}
	bad = cfg
	bad.AdditionalCA = nil
	untrusted, err := NewSourceClient(bad)
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.Close()
	if _, err = untrusted.List(context.Background(), ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal("self-signed origin accepted without explicit trust")
	}
	transport := c.http.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("source transport relaxed trust or adopted an ambient proxy")
	}
}
