// TEST_CASES: MAILMOD-T-006 MAILMOD-T-007 MAILMOD-T-080
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type countedNativeIntake struct {
	*dms.Client
	posts atomic.Int64
}

func (n *countedNativeIntake) Intake(ctx context.Context, m dms.IntakeMetadata, raw io.Reader) (dms.IntakeReceipt, error) {
	n.posts.Add(1)
	return n.Client.Intake(ctx, m, raw)
}

// The source is controlled TLS; PostgreSQL, Consumer, DMS SMTP/filter/storage,
// final receipts and the caller's Roundcube session are real. This bridges the
// native import path without claiming actual cloud SMTP acceptance.
func nativeIngressRoundTrip(t *testing.T, data store.Store, backend *dms.Client, resources []identity.MailboxResource) string {
	t.Helper()
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		t.Fatal(e)
	}
	random[6], random[8] = random[6]&15|64, random[8]&63|128
	source := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	generation := resources[0].ID
	subject := "ANAS native ingress " + source[:8]
	raw := []byte("From: probe@mail.mailtest.invalid\r\nTo: forged@foreign.example\r\nReceived: from forged.invalid for <other@foreign.example>; Mon, 26 Oct 2026 12:00:00 +0000\r\nMessage-ID: <same-message-id@sender.example.test>\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=anas-native-ingress\r\n\r\n--anas-native-ingress\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nNative ingress body\r\n--anas-native-ingress\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=proof.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/w==\r\n--anas-native-ingress--\r\n")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	readerToken := strings.Repeat("e", 64)
	messages := []ingress.SourceMessage{}
	_, domain, ok := strings.Cut(resources[0].DefaultAddress, "@")
	if !ok || domain == "" {
		t.Fatal("native source domain unavailable")
	}
	for _, r := range resources {
		a := r.Addresses[len(r.Addresses)-1]
		if !strings.HasSuffix(a.Address, "@"+domain) {
			t.Fatal("native fixture addresses must use the same source domain")
		}
		b := store.IngressBinding{SourceID: source, Generation: generation, Address: a.Address, ClaimVersion: a.Version, MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, FirstSequence: 1}
		if e := data.ConfirmIngressBinding(t.Context(), b); e != nil {
			t.Fatal("native source binding", e)
		}
		messages = append(messages, ingress.SourceMessage{Metadata: dms.IntakeMetadata{SourceID: source, MessageID: "native-" + source, MailboxID: r.ID, OwnerAnchor: r.OwnerAnchor, AcceptedAddress: a.Address, ClaimVersion: a.Version, EnvelopeFrom: "probe@mail.mailtest.invalid", SHA256: digest}, Sequence: 1, Bytes: int64(len(raw)), ETag: "native-" + r.ID, ReceivedAt: time.Now().UTC().Add(-time.Second)})
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, query := range []string{`DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(ctx, query, source); e != nil {
				t.Error(e)
			}
		}
	})
	var mu sync.Mutex
	loseReceipt := true
	acks := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Header.Get("Authorization") != "Bearer "+readerToken {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "GET" && req.URL.Path == "/v1/messages" {
			json.NewEncoder(w).Encode(ingress.SourcePage{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, RouteRevision: 1, Messages: messages})
			return
		}
		for n := range messages {
			m := &messages[n]
			if req.URL.Query().Get("target") != m.Metadata.MailboxID {
				continue
			}
			base := "/v1/messages/" + m.Metadata.MessageID
			if req.Method == "GET" && req.URL.Path == base+"/raw" {
				w.Header().Set("Content-Type", "message/rfc822")
				w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
				w.Header().Set("ETag", strconv.Quote(m.ETag))
				w.Write(raw)
				return
			}
			if req.Method != "POST" || req.URL.Path != base+"/ack" {
				continue
			}
			// The response is deliberately lost only after an independently
			// verified native receipt and durable source-side delivery state.
			var wire struct {
				SourceID   string            `json:"source_id"`
				Generation string            `json:"generation"`
				Receipt    dms.IntakeReceipt `json:"receipt"`
			}
			if json.NewDecoder(io.LimitReader(req.Body, 4097)).Decode(&wire) != nil || wire.SourceID != source || wire.Generation != generation || wire.Receipt.ID != m.Metadata.Key() || wire.Receipt.MailboxID != m.Metadata.MailboxID || wire.Receipt.SHA256 != digest {
				t.Error("native source acknowledgement identity changed")
				w.WriteHeader(400)
				return
			}
			receipt, e := backend.IntakeStatus(req.Context(), wire.Receipt.ID)
			if e != nil || receipt.ID != wire.Receipt.ID || receipt.MailboxID != wire.Receipt.MailboxID || receipt.SHA256 != digest || receipt.State != "stored" && receipt.State != "processed" {
				t.Error("source acknowledgement preceded actual native storage", e)
				w.WriteHeader(409)
				return
			}
			acks++
			m.Delivery = &ingress.SourceDelivery{ReceiptID: receipt.ID, SHA256: digest, State: receipt.State, AcknowledgedAt: time.Now().UTC()}
			if loseReceipt {
				loseReceipt = false
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"schema": "anas.mail-source/v1", "source_id": source, "generation": generation, "message_id": m.Metadata.MessageID, "mailbox_id": m.Metadata.MailboxID, "delivery": m.Delivery})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, e := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: readerToken, Domains: []string{domain}, AdditionalCA: ca})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	native := &countedNativeIntake{Client: backend}
	dir := t.TempDir()
	// testing.TempDir creates a numbered child using 0777 and the process
	// umask. Explicitly prepare the 0700 staging leaf required in production.
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	consumer := func() *ingress.Consumer {
		return &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: dir, SourceID: source, Generation: generation}
	}
	if e = consumer().Poll(t.Context()); e != nil {
		t.Fatal("native source poll", e)
	}
	deadline := time.Now().Add(60 * time.Second)
	var lastWork error
	for {
		lastWork = consumer().Work(t.Context())
		var complete int
		if e = data.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, source).Scan(&complete); e != nil {
			t.Fatal(e)
		}
		if complete == len(messages) {
			break
		}
		if time.Now().After(deadline) {
			rows, e := data.DB.QueryContext(t.Context(), `SELECT phase,native_state,last_error,attempts FROM mail.ingress_message WHERE source_id=$1 ORDER BY id`, source)
			if e == nil {
				defer rows.Close()
				for rows.Next() {
					var phase, native, check string
					var attempts int
					if e := rows.Scan(&phase, &native, &check, &attempts); e == nil {
						t.Log("native ingress phase", phase, "native", native, "check", check, "attempts", attempts)
					}
				}
			}
			t.Fatal("native ingress did not converge", "work", lastWork, "native submissions", native.posts.Load())
		}
		time.Sleep(time.Second)
	}
	if native.posts.Load() != int64(len(messages)) {
		t.Fatal("lost source receipt repeated native submission", native.posts.Load())
	}
	mu.Lock()
	count := acks
	mu.Unlock()
	if count != len(messages)+1 {
		t.Fatal("lost receipt did not use exact acknowledgement retry", count)
	}
	if e = consumer().Poll(t.Context()); e != nil {
		t.Fatal("repeat native source poll", e)
	}
	if e = consumer().Work(t.Context()); e != nil || native.posts.Load() != int64(len(messages)) {
		t.Fatal("delivered source replay repeated native import", e)
	}
	files, e := os.ReadDir(dir)
	if e != nil || len(files) != 0 {
		t.Fatal("confirmed import retained redundant local original", e)
	}
	return subject
}

func nativeIngressContents(t *testing.T, conn net.Conn, subject string) string {
	t.Helper()
	return nativeIngressContentsExpected(t, conn, subject, "Native ingress body", nil)
}

func nativeIngressContentsExpected(t *testing.T, conn net.Conn, subject, expectedBody string, checkHeaders func(mail.Header) bool) string {
	t.Helper()
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	reader := bufio.NewReader(conn)
	command := func(tag, text string) []string {
		t.Helper()
		if _, e := fmt.Fprintf(conn, "%s %s\r\n", tag, text); e != nil {
			t.Fatal(e)
		}
		lines := []string{}
		for range 100 {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					t.Fatal("native inbox command rejected")
				}
				return lines
			}
			lines = append(lines, line)
		}
		t.Fatal("native inbox command did not finish")
		return nil
	}
	uid := ""
	folder := ""
	for _, candidate := range []string{"INBOX", "Junk"} {
		command("is", "SELECT "+candidate)
		for _, line := range command("iq", "UID SEARCH HEADER Subject "+strconv.Quote(subject)) {
			if strings.HasPrefix(line, "* SEARCH") {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					continue
				}
				if len(fields) != 3 {
					t.Fatal("native import duplicated in target mailbox")
				}
				if _, e := strconv.ParseUint(fields[2], 10, 32); e != nil {
					t.Fatal(e)
				}
				uid, folder = fields[2], candidate
			}
		}
		if uid != "" {
			break
		}
	}
	if uid == "" {
		t.Fatal("native imported UID absent")
	}
	if _, e := fmt.Fprintf(conn, "if UID FETCH %s (BODY.PEEK[])\r\n", uid); e != nil {
		t.Fatal(e)
	}
	var raw []byte
	finished := false
	for range 100 {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasPrefix(line, "if ") {
			finished = strings.HasPrefix(line, "if OK")
			break
		}
		if match := regexp.MustCompile(`\{([0-9]+)\}\r\n$`).FindStringSubmatch(line); len(match) == 2 {
			n, e := strconv.Atoi(match[1])
			if e != nil || n < 1 || n > 2<<20 || raw != nil {
				t.Fatal("native raw literal invalid")
			}
			raw = make([]byte, n)
			if _, e = io.ReadFull(reader, raw); e != nil {
				t.Fatal(e)
			}
		}
	}
	message, e := mail.ReadMessage(bytes.NewReader(raw))
	if !finished || e != nil || !strings.Contains(message.Header.Get("Subject"), subject) {
		t.Fatal("native raw readback failed", e)
	}
	if checkHeaders != nil && !checkHeaders(message.Header) {
		t.Fatal("native message headers did not match")
	}
	media, params, e := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if e != nil || media != "multipart/mixed" || params["boundary"] == "" {
		t.Fatal("native MIME structure changed", e)
	}
	parts := multipart.NewReader(message.Body, params["boundary"])
	bodyOK, attachmentOK := false, false
	for {
		part, e := parts.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		var content io.Reader = part
		if strings.EqualFold(part.Header.Get("Content-Transfer-Encoding"), "base64") {
			content = base64.NewDecoder(base64.StdEncoding, part)
		}
		value, e := io.ReadAll(io.LimitReader(content, 1<<20))
		if e != nil {
			t.Fatal(e)
		}
		if part.FileName() == "proof.bin" {
			attachmentOK = bytes.Equal(value, []byte{0, 1, 2, 255})
		} else {
			bodyOK = strings.Contains(string(value), expectedBody)
		}
		part.Close()
	}
	if !bodyOK || !attachmentOK {
		t.Fatal("native mailbox lost original body or attachment")
	}
	return folder
}
