// TEST_CASES: MAILMOD-T-008
package store_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func fixtureUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type nativeConsumerFixture struct {
	mu                    sync.Mutex
	receipt               dms.IntakeReceipt
	submissions, releases int
	stored                bool
	failRelease           bool
	raw                   string
}

func (n *nativeConsumerFixture) Intake(_ context.Context, m dms.IntakeMetadata, body io.Reader) (dms.IntakeReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.receipt.ID == "" {
		raw, e := io.ReadAll(body)
		if e != nil {
			return dms.IntakeReceipt{}, e
		}
		n.raw = string(raw)
		n.submissions++
		n.receipt = dms.IntakeReceipt{ID: m.Key(), MailboxID: m.MailboxID, SHA256: m.SHA256, State: "queued"}
	}
	return n.receipt, dms.ErrUnavailable
}
func (n *nativeConsumerFixture) IntakeStatus(_ context.Context, id string) (dms.IntakeReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.receipt.ID == "" {
		return dms.IntakeReceipt{}, dms.ErrUnavailable
	}
	if n.stored {
		n.receipt.State = "stored"
		return n.receipt, nil
	}
	return n.receipt, dms.ErrUnavailable
}
func (n *nativeConsumerFixture) ReleaseIntake(_ context.Context, r dms.IntakeReceipt) (dms.IntakeReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.releases++
	if n.failRelease {
		n.failRelease = false
		return r, dms.ErrUnavailable
	}
	n.receipt.RawReleased = true
	return n.receipt, nil
}

func consumerDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	file := os.Getenv("MAIL_STORE_TEST_CONFIG")
	if file == "" {
		t.Skip("requires isolated PostgreSQL; not a real-host pass")
	}
	i, e := os.Stat(file)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 {
		t.Fatal("private fixture config required")
	}
	rawConfig, e := os.ReadFile(file)
	var cfg struct {
		DSN string `json:"dsn"`
	}
	if e != nil || json.Unmarshal(rawConfig, &cfg) != nil || cfg.DSN == "" {
		t.Fatal("fixture config invalid")
	}
	db, e := sql.Open("pgx", cfg.DSN)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx, done := context.WithTimeout(t.Context(), 120*time.Second)
	t.Cleanup(done)
	var database string
	if db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database) != nil || database != "anas_mail_model_1025" {
		t.Fatal("refusing non-fixture database")
	}
	return db, ctx
}

// This is the real PostgreSQL/source-HTTPS consumer contract. Native delivery
// is controlled here; separate T-007 native tests prove actual DMS operations.
func TestPostgreSQLIngressConsumerResumesUnknownAndLostCloudReceipt(t *testing.T) {
	db, ctx := consumerDatabase(t)
	var e error
	data := store.Store{DB: db}
	if e = data.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	source, generation, box, owner, version, op := fixtureUUID(t), fixtureUUID(t), fixtureUUID(t), fixtureUUID(t), fixtureUUID(t), fixtureUUID(t)
	address := "consumer-" + strings.ReplaceAll(box, "-", "") + "@example.test"
	resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: version, OperationID: op, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: "primary", State: "active", Version: version, OperationID: op, OperationState: "complete"}}}
	t.Cleanup(func() {
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, q := range []string{`DELETE FROM mail.ingress_notice WHERE source_id=$1`, `DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`} {
			if _, e := db.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
		for _, q := range []string{`DELETE FROM mail.mailbox_projection WHERE mailbox_id=$1`, `DELETE FROM mail.address WHERE mailbox_id=$1`, `DELETE FROM mail.mailbox WHERE id=$1`} {
			if _, e := db.ExecContext(clean, q, box); e != nil {
				t.Error(e)
			}
		}
		if _, e := db.ExecContext(clean, `DELETE FROM mail.person WHERE anchor=$1`, owner); e != nil {
			t.Error(e)
		}
	})
	p, e := data.StageMailbox(ctx, resource, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = data.ConfirmMailbox(ctx, box, p.Revision, p.Fingerprint); e != nil {
		t.Fatal(e)
	}
	if e = data.ConfirmIngressBinding(ctx, store.IngressBinding{SourceID: source, Generation: generation, Address: address, ClaimVersion: version, MailboxID: box, OwnerAnchor: owner, FirstSequence: 1}); e != nil {
		t.Fatal(e)
	}
	raw := "From: sender@example.net\r\nTo: forged@foreign.example\r\nSubject: source consumer 附件\r\nContent-Type: application/octet-stream\r\n\r\nattachment-original\x00\xff\r\n"
	sum := sha256.Sum256([]byte(raw))
	digest := hex.EncodeToString(sum[:])
	message := ingress.SourceMessage{Metadata: dms.IntakeMetadata{SourceID: source, MessageID: "consumer-1", MailboxID: box, OwnerAnchor: owner, AcceptedAddress: address, ClaimVersion: version, EnvelopeFrom: "sender@example.net", SHA256: digest}, Sequence: 1, Bytes: int64(len(raw)), ETag: digest, ReceivedAt: time.Now().UTC().Add(-time.Minute)}
	var mu sync.Mutex
	downloads, acks, lookups := 0, 0, 0
	var delivery *ingress.SourceDelivery
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("b", 64) {
			w.WriteHeader(401)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/messages/consumer-1":
			if r.URL.Query().Get("target") != box {
				w.WriteHeader(404)
				return
			}
			lookups++
			json.NewEncoder(w).Encode(map[string]any{"schema": "anas.mail-source/v1", "source_id": source, "generation": generation, "route_revision": 1, "message": message})
		case "/v1/messages":
			m := message
			m.Delivery = delivery
			json.NewEncoder(w).Encode(ingress.SourcePage{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, RouteRevision: 1, Messages: []ingress.SourceMessage{m}})
		case "/v1/messages/consumer-1/raw":
			downloads++
			if r.URL.Query().Get("target") != box {
				t.Error("target missing")
			}
			w.Header().Set("ETag", strconv.Quote(digest))
			io.WriteString(w, raw)
		case "/v1/messages/consumer-1/ack":
			acks++
			var input struct {
				SourceID   string            `json:"source_id"`
				Generation string            `json:"generation"`
				Receipt    dms.IntakeReceipt `json:"receipt"`
			}
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if d.Decode(&input) != nil || input.SourceID != source || input.Generation != generation || input.Receipt.ID != message.Metadata.Key() || input.Receipt.State != "stored" {
				t.Error("unsafe acknowledgement")
			}
			delivery = &ingress.SourceDelivery{ReceiptID: message.Metadata.Key(), SHA256: digest, State: "stored", AcknowledgedAt: time.Now().UTC()}
			if acks == 1 {
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"schema": "anas.mail-source/v1", "source_id": source, "generation": generation, "message_id": message.Metadata.MessageID, "mailbox_id": box, "delivery": delivery})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, e := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: strings.Repeat("b", 64), Domains: []string{"example.test"}, AdditionalCA: cert, IPv4Only: true})
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	native := &nativeConsumerFixture{failRelease: true}
	consumer := &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: filepath.Join(t.TempDir(), "spool"), SourceID: source, Generation: generation}
	notice := fixtureUUID(t)
	notifyKey := strings.Repeat("d", 64)
	push := func(id, messageID string) int {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"schema": "anas.mail-notification/v1", "source_id": source, "generation": generation, "notification_id": id, "message_id": messageID, "mailbox_id": box})
		r := httptest.NewRequest("POST", "https://mail.example.test/api/ingress/notify", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-ANAS-Mail-Time", strconv.FormatInt(time.Now().Unix(), 10))
		key, _ := hex.DecodeString(notifyKey)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(r.Header.Get("X-ANAS-Mail-Time") + "."))
		mac.Write(raw)
		r.Header.Set("X-ANAS-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
		woken := false
		h := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{source: {Generation: generation, Secret: notifyKey}}, Wake: func(sourceID string) {
			var exists bool
			if sourceID != source || db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.ingress_notice WHERE source_id=$1 AND notification_id=$2)`, source, id).Scan(&exists) != nil || !exists {
				t.Error("wake preceded durable PostgreSQL notice")
			}
			woken = true
		}}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == 202 && !woken {
			t.Fatal("durable notice not woken")
		}
		return w.Code
	}
	if push(notice, "consumer-1") != 202 || push(notice, "consumer-1") != 202 {
		t.Fatal("real PG notice receipt")
	}
	if e = consumer.Notices(ctx); e != nil {
		t.Fatal("notice metadata resolution", e)
	}
	if e = consumer.Poll(ctx); e != nil {
		t.Fatal("source list", e)
	}
	// Concurrent notification/periodic workers still share one PG task lease.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _ = consumer.Work(ctx) }()
	}
	wg.Wait()
	check := func(phase string) {
		t.Helper()
		row, e := data.IngressTask(ctx, message.Metadata.Key())
		if e != nil || row.Phase != phase {
			t.Fatal("phase", phase, row.Phase, e)
		}
	}
	check("native")
	native.mu.Lock()
	if native.submissions != 1 || native.raw != raw {
		t.Fatal("native duplicate or payload changed")
	}
	native.mu.Unlock()
	mu.Lock()
	if acks != 0 {
		t.Fatal("queued acceptance acknowledged")
	}
	mu.Unlock()
	retry := func() {
		t.Helper()
		if _, e = db.ExecContext(ctx, `UPDATE mail.ingress_message SET next_attempt=clock_timestamp() WHERE source_id=$1`, source); e != nil {
			t.Fatal(e)
		}
	}
	retry()
	_ = consumer.Work(ctx)
	native.mu.Lock()
	if native.submissions != 1 {
		t.Fatal("queued result blindly resubmitted")
	}
	native.stored = true
	native.mu.Unlock()
	retry()
	if e = consumer.Work(ctx); e == nil {
		t.Fatal("lost cloud acknowledgement reported success")
	}
	check("ack")
	// Reconstruct the consumer/store: resume from durable phase, without
	// consulting a person session or AD and without another native submission.
	consumer.Ledger = store.Store{DB: db}
	retry()
	if e = consumer.Work(ctx); e == nil {
		t.Fatal("native cleanup failure reported complete")
	}
	check("cleanup")
	mu.Lock()
	if acks != 2 {
		t.Fatal("cloud receipt retry", acks)
	}
	mu.Unlock()
	retry()
	if e = consumer.Work(ctx); e != nil {
		t.Fatal("cleanup resume", e)
	}
	check("complete")
	if _, e = os.Lstat(filepath.Join(consumer.Directory, message.Metadata.Key()+".eml")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("NAS redundant raw not released", e)
	}
	native.mu.Lock()
	if native.submissions != 1 || !native.receipt.RawReleased || native.releases != 2 {
		t.Fatal("native delivery or cleanup repeated incorrectly")
	}
	native.mu.Unlock()
	if e = consumer.Poll(ctx); e != nil {
		t.Fatal("retained delivered copy not resumable", e)
	}
	if e = consumer.Work(ctx); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	if downloads != 1 || acks != 2 || delivery == nil {
		t.Fatal("retained source changed or already completed mail reimported", downloads, acks)
	}
	mu.Unlock()
	if push(fixtureUUID(t), "consumer-1") != 202 {
		t.Fatal("late notice")
	}
	if e = consumer.Notices(ctx); e != nil {
		t.Fatal("completed target notice", e)
	}
	mu.Lock()
	if lookups != 1 {
		t.Fatal("completed mailbox task unnecessarily looked up again", lookups)
	}
	mu.Unlock()
	if push(fixtureUUID(t), "retention-expired") != 202 {
		t.Fatal("expired source hint")
	}
	if e = consumer.Notices(ctx); !errors.Is(e, ingress.ErrGone) {
		t.Fatal("missing source not surfaced", e)
	}
}
