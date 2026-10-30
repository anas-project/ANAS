// TEST_CASES: MAILMOD-T-080
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// Only the poll methods are used; any unexpected consumer call fails the test.
type guardPollLedger struct {
	ingress.Ledger
	staged []store.IngressObservation
}

func (l *guardPollLedger) ClaimIngressPoll(_ context.Context, source, generation string) (store.IngressPoll, error) {
	return store.IngressPoll{SourceID: source, Generation: generation}, nil
}
func (l *guardPollLedger) StageIngress(_ context.Context, o store.IngressObservation) (store.IngressTask, error) {
	l.staged = append(l.staged, o)
	return store.IngressTask{}, nil
}
func (l *guardPollLedger) AdvanceIngressPoll(_ context.Context, p store.IngressPoll, cursor string, revision int64) (store.IngressPoll, error) {
	p.Cursor, p.RouteRevision = cursor, revision
	return p, nil
}
func (l *guardPollLedger) ReleaseIngressPoll(context.Context, store.IngressPoll, string) error {
	return nil
}

type guardUnusedNative struct{ ingress.Native }

type guardNoticeLedger struct {
	ingress.Ledger
	notices  []store.IngressNoticeTask
	staged   []store.IngressObservation
	finished []string
}

func (l *guardNoticeLedger) ClaimIngressNotice(context.Context, string) (store.IngressNoticeTask, error) {
	if len(l.notices) == 0 {
		return store.IngressNoticeTask{}, store.ErrIngressIdle
	}
	n := l.notices[0]
	l.notices = l.notices[1:]
	return n, nil
}
func (l *guardNoticeLedger) IngressTask(context.Context, string) (store.IngressTask, error) {
	return store.IngressTask{}, store.ErrIngressIdle
}
func (l *guardNoticeLedger) StageIngress(_ context.Context, o store.IngressObservation) (store.IngressTask, error) {
	l.staged = append(l.staged, o)
	return store.IngressTask{}, nil
}
func (l *guardNoticeLedger) FinishIngressNotice(_ context.Context, n store.IngressNoticeTask, state, code string) error {
	l.finished = append(l.finished, n.MessageID+":"+state+":"+code)
	return nil
}

type guardSource struct {
	page                                 ingress.SourcePage
	message                              ingress.SourceMessage
	downloads, acknowledgements, lookups int
}

// Exercise the real HTTPS source client around the test-only filter. The
// upstream service contains both messages, but the verification body is never
// requested and the synthetic body still passes the source integrity check.
func TestCloudflareVPSFixtureGuardWithSourceClient(t *testing.T) {
	const sourceID = "10000000-0000-4000-8000-000000000001"
	const generation = "10000000-0000-4000-8000-000000000002"
	route := ingress.Route{Address: "test@example.test", MailboxID: "10000000-0000-4000-8000-000000000003", OwnerAnchor: "10000000-0000-4000-8000-000000000004", ClaimVersion: "10000000-0000-4000-8000-000000000005"}
	raw := []byte("controlled synthetic message")
	digest := sha256.Sum256(raw)
	message := func(id string, sequence int64) ingress.SourceMessage {
		return ingress.SourceMessage{Metadata: dms.IntakeMetadata{SourceID: sourceID, MessageID: id, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor, AcceptedAddress: route.Address, ClaimVersion: route.ClaimVersion, EnvelopeFrom: "sender@example.test", SHA256: hex.EncodeToString(digest[:])}, Sequence: sequence, Bytes: int64(len(raw)), ETag: "fixture-tag", ReceivedAt: time.Now().UTC()}
	}
	page := ingress.SourcePage{Schema: "anas.mail-source/v1", SourceID: sourceID, Generation: generation, RouteRevision: 1, Messages: []ingress.SourceMessage{message("verification", 1), message("synthetic", 2)}}
	var verificationRequests, syntheticRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/messages":
			_ = json.NewEncoder(w).Encode(page)
		case "/v1/messages/verification/raw":
			verificationRequests.Add(1)
			http.Error(w, "unexpected verification access", http.StatusInternalServerError)
		case "/v1/messages/synthetic/raw":
			syntheticRequests.Add(1)
			w.Header().Set("ETag", `"fixture-tag"`)
			_, _ = w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: sourceID, Generation: generation, Origin: server.URL, Token: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64), Domains: []string{"example.test"}, AdditionalCA: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	guard := cloudflareVPSFixtureSource{source: client, sourceID: sourceID, generation: generation, verification: "verification", route: route}
	filtered, err := guard.List(context.Background(), "")
	if err != nil || len(filtered.Messages) != 1 {
		t.Fatal("validated source page unavailable", err)
	}
	var received bytes.Buffer
	if _, err = guard.Download(context.Background(), filtered.Messages[0], &received); err != nil || !bytes.Equal(received.Bytes(), raw) {
		t.Fatal("synthetic source content not verified", err)
	}
	if _, err = guard.Download(context.Background(), page.Messages[0], io.Discard); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("verification body was accessible", err)
	}
	if verificationRequests.Load() != 0 || syntheticRequests.Load() != 1 {
		t.Fatal("unexpected upstream requests")
	}
	ledger := &guardPollLedger{}
	consumer := ingress.Consumer{Ledger: ledger, Source: guard, Native: guardUnusedNative{}, Directory: t.TempDir(), SourceID: sourceID, Generation: generation}
	if err = consumer.Poll(context.Background()); err != nil || len(ledger.staged) != 1 || ledger.staged[0].MessageID != "synthetic" {
		t.Fatal("consumer staged unexpected source messages", err)
	}
	if verificationRequests.Load() != 0 {
		t.Fatal("consumer accessed verification body")
	}
}

func (s *guardSource) List(context.Context, string) (ingress.SourcePage, error) { return s.page, nil }
func (s *guardSource) Download(_ context.Context, _ ingress.SourceMessage, _ io.Writer) (string, error) {
	s.downloads++
	return "ok", nil
}
func (s *guardSource) Acknowledge(_ context.Context, _ ingress.SourceMessage, _ dms.IntakeReceipt) error {
	s.acknowledgements++
	return nil
}
func (s *guardSource) Message(_ context.Context, _, _ string) (ingress.SourceMessage, error) {
	s.lookups++
	return s.message, nil
}

func TestCloudflareVPSFixtureKeepsVerificationMessagePrivate(t *testing.T) {
	ctx := context.Background()
	route := ingress.Route{Address: "test@example.test", MailboxID: "mailbox", OwnerAnchor: "owner", ClaimVersion: "version"}
	makeMessage := func(id string) ingress.SourceMessage {
		return ingress.SourceMessage{Metadata: dms.IntakeMetadata{SourceID: "source", MessageID: id, AcceptedAddress: route.Address, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor, ClaimVersion: route.ClaimVersion}}
	}
	verification, synthetic := makeMessage("verification"), makeMessage("synthetic")
	base := &guardSource{page: ingress.SourcePage{SourceID: "source", Generation: "generation", Messages: []ingress.SourceMessage{verification, synthetic}}, message: synthetic}
	guard := cloudflareVPSFixtureSource{source: base, sourceID: "source", generation: "generation", verification: "verification", route: route}
	page, err := guard.List(ctx, "")
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Metadata.MessageID != "synthetic" {
		t.Fatal("test message not isolated", err)
	}
	if len(base.page.Messages) != 2 {
		t.Fatal("source page was modified")
	}
	if _, err = guard.Download(ctx, verification, io.Discard); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("verification download allowed")
	}
	if err = guard.Acknowledge(ctx, verification, dms.IntakeReceipt{}); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("verification acknowledgment allowed")
	}
	if _, err = guard.Message(ctx, "verification", route.MailboxID); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("verification lookup allowed")
	}
	if base.downloads != 0 || base.acknowledgements != 0 || base.lookups != 0 {
		t.Fatal("private message reached source operations")
	}
	if _, err = guard.Download(ctx, synthetic, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err = guard.Acknowledge(ctx, synthetic, dms.IntakeReceipt{}); err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Message(ctx, "synthetic", route.MailboxID); err != nil {
		t.Fatal(err)
	}
	if base.downloads != 1 || base.acknowledgements != 1 || base.lookups != 1 {
		t.Fatal("test message did not reach source")
	}
	guard.expected = "synthetic"
	other := makeMessage("other")
	for _, action := range []func() error{
		func() error { _, e := guard.Download(ctx, other, io.Discard); return e },
		func() error { return guard.Acknowledge(ctx, other, dms.IntakeReceipt{}) },
		func() error { _, e := guard.Message(ctx, "other", route.MailboxID); return e },
	} {
		if err := action(); !errors.Is(err, ingress.ErrIdentity) {
			t.Fatal("unexpected public message accepted", err)
		}
	}
	wrongMessage := &guardSource{page: ingress.SourcePage{SourceID: "source", Generation: "generation", Messages: []ingress.SourceMessage{verification, other}}}
	strict := guard
	strict.source = wrongMessage
	if _, err = strict.List(ctx, ""); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("unexpected public message listed", err)
	}

	for name, mutate := range map[string]func(*guardSource){
		"missing verification":   func(s *guardSource) { s.page.Messages = []ingress.SourceMessage{synthetic} },
		"duplicate verification": func(s *guardSource) { s.page.Messages = []ingress.SourceMessage{verification, verification} },
		"unknown extra": func(s *guardSource) {
			s.page.Messages = []ingress.SourceMessage{verification, synthetic, makeMessage("third")}
		},
		"different target": func(s *guardSource) {
			m := synthetic
			m.Metadata.MailboxID = "other"
			s.page.Messages = []ingress.SourceMessage{verification, m}
		},
		"unexpected cursor": func(s *guardSource) { c := "next"; s.page.Cursor = &c },
		"wrong source":      func(s *guardSource) { s.page.SourceID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			fake := &guardSource{page: base.page}
			mutate(fake)
			g := guard
			g.source = fake
			if _, err := g.List(ctx, ""); !errors.Is(err, ingress.ErrIdentity) {
				t.Fatal("ambiguous page accepted", err)
			}
		})
	}
	if _, err = guard.List(ctx, "next"); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("unexpected cursor accepted")
	}
	guard.verification = ""
	if _, err = guard.Download(ctx, synthetic, io.Discard); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("missing verification identity accepted")
	}
}

func TestCloudflareVPSFixtureNoticeDoesNotImportVerification(t *testing.T) {
	const source = "10000000-0000-4000-8000-000000000001"
	const generation = "10000000-0000-4000-8000-000000000002"
	route := ingress.Route{Address: "test@example.test", MailboxID: "10000000-0000-4000-8000-000000000003", OwnerAnchor: "10000000-0000-4000-8000-000000000004", ClaimVersion: "10000000-0000-4000-8000-000000000005"}
	synthetic := ingress.SourceMessage{Metadata: dms.IntakeMetadata{SourceID: source, MessageID: "synthetic", AcceptedAddress: route.Address, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor, ClaimVersion: route.ClaimVersion}}
	upstream := &guardSource{message: synthetic}
	guard := cloudflareVPSFixtureSource{source: upstream, sourceID: source, generation: generation, verification: "verification", route: route}
	makeNotice := func(id string) store.IngressNoticeTask {
		return store.IngressNoticeTask{IngressNotice: store.IngressNotice{SourceID: source, Generation: generation, MessageID: id, MailboxID: route.MailboxID}}
	}
	ledger := &guardNoticeLedger{notices: []store.IngressNoticeTask{makeNotice("verification"), makeNotice("synthetic")}}
	consumer := ingress.Consumer{Ledger: ledger, Source: guard, Native: guardUnusedNative{}, Directory: t.TempDir(), SourceID: source, Generation: generation}
	if err := consumer.Notices(context.Background()); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("verification notice did not remain pending", err)
	}
	if len(ledger.staged) != 1 || ledger.staged[0].MessageID != "synthetic" || upstream.lookups != 1 {
		t.Fatal("notice staged unexpected message")
	}
	if len(ledger.finished) != 2 || ledger.finished[0] != "verification:pending:source-identity-changed" || ledger.finished[1] != "synthetic:accepted:" {
		t.Fatal("notice results did not preserve verification isolation")
	}
}
