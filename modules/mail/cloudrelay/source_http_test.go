// TEST_CASES: MAILMOD-T-048
package cloudrelay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

func sourceHTTPFixture(t *testing.T, store *OriginalStore) (*ingress.SourceClient, http.Handler, *httptest.Server) {
	t.Helper()
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	handler, err := NewSourceHandler(store, reader, control, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: routeSource, Generation: routeGeneration, Origin: server.URL, Token: reader, ControlToken: control, Domains: []string{"example.test"}, AdditionalCA: ca})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, handler, server
}

func TestSourceHTTPBriefLockContention(t *testing.T) {
	ledger, store, _ := originalFixture(t, routeMax+1000)
	client, _, _ := sourceHTTPFixture(t, store)
	dir, err := ledger.openDir()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := unix.Openat(dir, "routes.lock", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	unix.Close(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(lock)
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := client.Status(context.Background()); done <- e }()
	time.Sleep(75 * time.Millisecond)
	if err = unix.Flock(lock, unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("brief lock contention should recover: %v", err)
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_, err = client.Status(context.Background())
	unix.Flock(lock, unix.LOCK_UN)
	if !errors.Is(err, ingress.ErrUnavailable) {
		t.Fatalf("persistent contention should fail safely: %v", err)
	}
}

func TestSourceHTTPMultipleTargetsAndReceipt(t *testing.T) {
	ledger, store, dir := originalFixture(t, routeMax+2000)
	primary, alias := routeFixture("alice@example.test"), routeFixture("alias@example.test")
	extra := routeFixture("extra@example.test")
	extra.MailboxID = routeOp3
	extra.ClaimVersion = routeOp2
	if _, err := ledger.Replace(1, routeOp2, []ingress.Route{primary, alias, extra}); err != nil {
		t.Fatal(err)
	}
	body := []byte("From: sender@example.net\r\nTo: forged@foreign.test\r\n\r\ntest original\r\n")
	stored, err := store.Store(routeOp3, "sender@example.net", []string{primary.Address, alias.Address, extra.Address}, body)
	if err != nil {
		t.Fatal(err)
	}
	transferred := alias
	transferred.OwnerAnchor = routeOp2
	transferred.MailboxID = routeOp1
	transferred.ClaimVersion = routeOp3
	if _, err = ledger.Replace(2, routeOp3, []ingress.Route{primary, transferred, extra}); err != nil {
		t.Fatal(err)
	}
	client, handler, _ := sourceHTTPFixture(t, store)
	ctx := context.Background()
	page, err := client.List(ctx, "")
	if err != nil || len(page.Messages) != 3 || page.Cursor != nil {
		t.Fatalf("list: %+v %v", page, err)
	}
	for i, m := range page.Messages {
		if m.Metadata.AcceptedAddress != stored.Recipients[i].Route.Address || m.Metadata.OwnerAnchor != stored.Recipients[i].Route.OwnerAnchor || m.Metadata.ClaimVersion != stored.Recipients[i].Route.ClaimVersion || m.Delivery != nil {
			t.Fatalf("wrong target: %+v", m)
		}
		direct, err := client.Message(ctx, m.Metadata.MessageID, m.Metadata.MailboxID)
		if err != nil || !reflect.DeepEqual(direct, m) {
			t.Fatalf("message: %+v %v", direct, err)
		}
		var output bytes.Buffer
		digest, err := client.Download(ctx, m, &output)
		if err != nil || digest != m.Metadata.SHA256 || !bytes.Equal(output.Bytes(), body) {
			t.Fatalf("download: %v", err)
		}
	}
	first := page.Messages[0]
	receipt := dms.IntakeReceipt{ID: first.Metadata.Key(), MailboxID: first.Metadata.MailboxID, SHA256: first.Metadata.SHA256, State: "stored"}
	if err = client.Acknowledge(ctx, first, receipt); err != nil {
		t.Fatal(err)
	}
	if err = client.Acknowledge(ctx, first, receipt); err != nil {
		t.Fatal("replay", err)
	}
	if _, err = client.Message(ctx, first.Metadata.MessageID, extra.MailboxID); !errors.Is(err, ingress.ErrGone) {
		t.Fatalf("cross-target: %v", err)
	}
	updated, err := client.List(ctx, "")
	if err != nil || updated.Messages[0].Delivery == nil || updated.Messages[1].Delivery != nil || updated.Messages[2].Delivery != nil {
		t.Fatalf("target receipt isolation: %+v %v", updated, err)
	}
	// A stale or altered receipt cannot mark another recipient delivered.
	bad := fmt.Sprintf(`{"source_id":%q,"generation":%q,"receipt":{"id":%q,"mailbox_id":%q,"sha256":%q,"state":"stored"}}`, routeSource, routeGeneration, receipt.ID, updated.Messages[1].Metadata.MailboxID, receipt.SHA256)
	r := httptest.NewRequest("POST", "https://source.example/v1/messages/"+updated.Messages[1].Metadata.MessageID+"/ack?target="+updated.Messages[1].Metadata.MailboxID, strings.NewReader(bad))
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("cross receipt: %d", w.Code)
	}
	reopenedLedger, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOriginalStore(reopenedLedger, routeMax+2000, 7)
	if err != nil {
		t.Fatal(err)
	}
	client2, _, _ := sourceHTTPFixture(t, reopened)
	saved, err := client2.List(ctx, "")
	if err != nil || saved.Messages[0].Delivery == nil || saved.Messages[1].Delivery != nil || saved.Messages[2].Delivery != nil {
		t.Fatalf("receipt restart: %+v %v", saved, err)
	}
	var output bytes.Buffer
	if _, err = client2.Download(ctx, saved.Messages[0], &output); err != nil || !bytes.Equal(output.Bytes(), body) {
		t.Fatalf("ack removed original: %v", err)
	}
}
func TestSourceHTTPPendingBarrierAndPagination(t *testing.T) {
	ledger, store, _ := originalFixture(t, routeMax+5000)
	body := []byte("first")
	if _, err := store.Store(routeOp1, "", []string{"alice@example.test"}, body); err != nil {
		t.Fatal(err)
	}
	pendingBody := []byte("pending")
	hash := sha256.Sum256(pendingBody)
	if _, err := ledger.ReserveEnvelope(routeOp2, "", hex.EncodeToString(hash[:]), int64(len(pendingBody)), []string{"alias@example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Store(routeOp3, "", []string{"alice@example.test"}, []byte("later")); err != nil {
		t.Fatal(err)
	}
	client, handler, _ := sourceHTTPFixture(t, store)
	ctx := context.Background()
	page, err := client.List(ctx, "")
	if err != nil || len(page.Messages) != 1 || page.Cursor != nil {
		t.Fatalf("barrier: %+v %v", page, err)
	}
	if _, err = store.Store(routeOp2, "", []string{"alias@example.test"}, pendingBody); err != nil {
		t.Fatal(err)
	}
	page, err = client.List(ctx, "")
	if err != nil || len(page.Messages) != 3 {
		t.Fatalf("recovered: %+v %v", page, err)
	}
	getPage := func(cursor string) ingress.SourcePage {
		path := "https://source.example/v1/messages?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("page status %d", w.Code)
		}
		var out ingress.SourcePage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	one := getPage("")
	if len(one.Messages) != 1 || one.Cursor == nil {
		t.Fatal("first page missing cursor")
	}
	two := getPage(*one.Cursor)
	if len(two.Messages) != 1 || two.Cursor == nil || two.Messages[0].Sequence <= one.Messages[0].Sequence {
		t.Fatal("second page")
	}
	three := getPage(*two.Cursor)
	if len(three.Messages) != 1 || three.Cursor != nil || three.Messages[0].Sequence <= two.Messages[0].Sequence {
		t.Fatal("last page")
	}
}

func TestSourceHTTPClientTraversesBacklog(t *testing.T) {
	_, store, _ := originalFixture(t, routeMax+5000)
	for i := 0; i < 51; i++ {
		id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012x", i)
		if _, err := store.Store(id, "", []string{"alice@example.test", "alias@example.test"}, []byte("original")); err != nil {
			t.Fatal(err)
		}
	}
	client, _, _ := sourceHTTPFixture(t, store)
	ctx := context.Background()
	first, err := client.List(ctx, "")
	if err != nil || len(first.Messages) != 100 || first.Cursor == nil {
		t.Fatalf("first page: %d %v", len(first.Messages), err)
	}
	second, err := client.List(ctx, *first.Cursor)
	if err != nil || len(second.Messages) != 2 || second.Cursor != nil {
		t.Fatalf("second page: %d %v", len(second.Messages), err)
	}
	seen := map[string]bool{}
	for _, message := range append(first.Messages, second.Messages...) {
		key := message.Metadata.Key()
		if seen[key] {
			t.Fatal("duplicate target key")
		}
		seen[key] = true
	}
	if len(seen) != 102 {
		t.Fatalf("targets: %d", len(seen))
	}
}
func TestSourceHTTPRejectsUnsafeRequestsAndCorruption(t *testing.T) {
	_, store, dir := originalFixture(t, routeMax+1000)
	if _, err := store.Store(routeOp3, "", []string{"alice@example.test"}, []byte("raw")); err != nil {
		t.Fatal(err)
	}
	client, handler, _ := sourceHTTPFixture(t, store)
	page, err := client.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	m := page.Messages[0]
	for _, tc := range []struct {
		name, path, auth, origin string
		status                   int
	}{
		{"wrong token", "/v1/messages?limit=1", strings.Repeat("b", 64), "", 401},
		{"browser origin", "/v1/messages?limit=1", strings.Repeat("a", 64), "https://attacker.test", 403},
		{"duplicate query", "/v1/messages?limit=1&limit=2", strings.Repeat("a", 64), "", 400},
		{"invalid cursor", "/v1/messages?cursor=1", strings.Repeat("a", 64), "", 400},
		{"unknown query", "/v1/messages?extra=1", strings.Repeat("a", 64), "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://source.example"+tc.path, nil)
			r.Header.Set("Authorization", "Bearer "+tc.auth)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
	if err = os.WriteFile(dir+"/originals/raw-"+routeOp3, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	var destination bytes.Buffer
	if _, err = client.Download(context.Background(), m, &destination); !errors.Is(err, ingress.ErrUnavailable) {
		t.Fatalf("corrupt raw download: %v", err)
	}
	receipt := dms.IntakeReceipt{ID: m.Metadata.Key(), MailboxID: m.Metadata.MailboxID, SHA256: m.Metadata.SHA256, State: "stored"}
	if err = client.Acknowledge(context.Background(), m, receipt); !errors.Is(err, ingress.ErrUnavailable) {
		t.Fatalf("corrupt raw ack: %v", err)
	}
	page, err = client.List(context.Background(), "")
	if err != nil || page.Messages[0].Delivery != nil {
		t.Fatalf("corruption marked delivered: %+v %v", page, err)
	}
}
