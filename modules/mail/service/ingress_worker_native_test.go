// TEST_CASES: MAILMOD-T-010 MAILMOD-T-080
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type nativeWorkerInput struct {
	Source         ingress.SourceConfig
	Routes         []ingress.Route
	Subject        string
	VisibleTo      string
	NotifyToken    string
	NotifyCert     string
	NotifyKey      string
	RuleToken      string
	Offline        bool
	ScheduledRetry bool
}

// Verify each delivered original, not merely its subject. The two addresses
// of the first mailbox intentionally produce two distinct deliveries.
func nativeWorkerContents(t *testing.T, conn net.Conn, subject, visibleTo string, expected int) []string {
	t.Helper()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	reader := bufio.NewReader(conn)
	command := func(tag, text string) []string {
		t.Helper()
		if _, err := fmt.Fprintf(conn, "%s %s\r\n", tag, text); err != nil {
			t.Fatal(err)
		}
		var lines []string
		for range 100 {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, tag+" ") {
				if !strings.HasPrefix(line, tag+" OK") {
					t.Fatal("Worker inbox command rejected")
				}
				return lines
			}
			lines = append(lines, line)
		}
		t.Fatal("Worker inbox command did not finish")
		return nil
	}
	count := 0
	folders := []string{}
	for _, folder := range []string{"INBOX", "Junk"} {
		command("ws", "SELECT "+folder)
		for _, line := range command("wq", "UID SEARCH HEADER Subject "+strconv.Quote(subject)) {
			if !strings.HasPrefix(line, "* SEARCH") {
				continue
			}
			ids := strings.Fields(line)[2:]
			if len(ids) != 0 {
				folders = append(folders, folder)
			}
			for _, uid := range ids {
				if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
					t.Fatal("invalid Worker inbox UID")
				}
				count++
				if _, err := fmt.Fprintf(conn, "wf UID FETCH %s (BODY.PEEK[])\r\n", uid); err != nil {
					t.Fatal(err)
				}
				var raw []byte
				finished := false
				for range 100 {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Fatal(err)
					}
					if strings.HasPrefix(line, "wf ") {
						finished = strings.HasPrefix(line, "wf OK")
						break
					}
					if match := regexp.MustCompile(`\{([0-9]+)\}\r\n$`).FindStringSubmatch(line); len(match) == 2 {
						n, e := strconv.Atoi(match[1])
						if e != nil || n < 1 || n > 2<<20 || raw != nil {
							t.Fatal("invalid Worker raw message")
						}
						raw = make([]byte, n)
						if _, e = io.ReadFull(reader, raw); e != nil {
							t.Fatal(e)
						}
					}
				}
				message, err := mail.ReadMessage(bytes.NewReader(raw))
				if !finished || err != nil || message.Header.Get("Subject") != subject || message.Header.Get("To") != visibleTo {
					t.Fatal("Worker original headers changed")
				}
				media, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
				if err != nil || media != "multipart/mixed" || params["boundary"] == "" {
					t.Fatal("Worker original MIME missing")
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
						bodyOK = strings.Contains(string(value), "ANAS real Cloudflare SMTP body")
					}
					part.Close()
				}
				if !bodyOK || !attachmentOK {
					t.Fatal("Worker original body or attachment missing")
				}
			}
		}
	}
	if count != expected {
		t.Fatal("Worker inbox count changed", count, "expected", expected)
	}
	return folders
}

func nativeWorkerConfig(t *testing.T) *nativeWorkerInput {
	t.Helper()
	path := os.Getenv("MAIL_NATIVE_WORKER_CONFIG")
	if path == "" {
		return nil
	}
	if path != "/input/worker-native.json" {
		t.Fatal("fixed isolated Worker input required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		t.Fatal("private isolated Worker input required")
	}
	raw, err := os.ReadFile(path)
	var cfg nativeWorkerInput
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("invalid isolated Worker input")
	}
	clear(raw)
	origin, err := url.Parse(cfg.Source.Origin)
	if err != nil || origin == nil {
		t.Fatal("invalid Worker origin")
	}
	pattern := regexp.MustCompile(`^anas-mail-smtp-live-([a-f0-9]{12})\.[a-z0-9-]+\.workers\.dev$`)
	match := pattern.FindStringSubmatch(origin.Hostname())
	if origin.Scheme != "https" || origin.User != nil || origin.Host != origin.Hostname() || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || len(match) != 2 || len(cfg.Source.Domains) != 1 || cfg.Source.Domains[0] != "mail-poc.bengbu.cc.cd" || len(cfg.Routes) != 3 || cfg.Subject != "ANAS cloud SMTP "+cfg.Source.ID || cfg.Source.Proxy != "" || len(cfg.Source.AdditionalCA) != 0 {
		t.Fatal("fixed disposable Worker and domain required")
	}
	for i, kind := range []string{"main", "alias", "extra"} {
		route := cfg.Routes[i]
		if route.Address != "smtp-"+match[1]+"-"+kind+"@mail-poc.bengbu.cc.cd" || route.OwnerAnchor != cfg.Routes[0].OwnerAnchor || i == 1 && route.MailboxID != cfg.Routes[0].MailboxID || i == 2 && route.MailboxID == cfg.Routes[0].MailboxID {
			t.Fatal("unexpected isolated Worker recipient")
		}
	}
	if cfg.VisibleTo != "forged@foreign.example" && cfg.VisibleTo != cfg.Routes[0].Address && !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}@126\.com$`).MatchString(cfg.VisibleTo) {
		t.Fatal("controlled visible recipient required")
	}
	if cfg.NotifyToken != "" && (!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(cfg.NotifyToken) || cfg.NotifyToken == cfg.Source.Token || cfg.NotifyToken == cfg.Source.ControlToken || cfg.NotifyCert == "" || cfg.NotifyKey == "") {
		t.Fatal("invalid isolated notification credentials")
	}
	if cfg.NotifyToken == "" && (cfg.NotifyCert != "" || cfg.NotifyKey != "") {
		t.Fatal("unused notification TLS credentials")
	}
	if cfg.Offline && cfg.NotifyToken == "" {
		t.Fatal("offline test requires the real notification binding")
	}
	if cfg.RuleToken != "" && (!regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`).MatchString(cfg.RuleToken) || cfg.RuleToken == cfg.Source.Token || cfg.RuleToken == cfg.Source.ControlToken || cfg.RuleToken == cfg.NotifyToken) {
		t.Fatal("invalid isolated zone routing credential")
	}
	if _, err = ingress.RouteFingerprint(cfg.Routes); err != nil {
		t.Fatal("invalid isolated Worker routes")
	}
	return &cfg
}

// Connects a disposable real Worker to actual isolated PostgreSQL and DMS.
// The controller creates only the three explicit public rules after the local
// route operation is persisted and observed; SMTP is sent after that signal.
func nativeWorkerIngressRoundTrip(t *testing.T, cfg *nativeWorkerInput, data store.Store, backend *dms.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := testWorkerTunnel(listener, "/bridge/cloud.sock")
	t.Cleanup(stop)
	source := cfg.Source
	source.Proxy = "http://" + listener.Addr().String()
	client, err := ingress.NewSourceClient(source)
	if err != nil {
		t.Fatal("isolated Worker source invalid", err)
	}
	t.Cleanup(client.Close)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, query := range []string{`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`, `DELETE FROM mail.cloudflare_rule WHERE source_id=$1`, `DELETE FROM mail.ingress_notice WHERE source_id=$1`, `DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(cleanup, query, source.ID); e != nil {
				t.Error("isolated Worker ledger cleanup", e)
			}
		}
	})
	limit := 390 * time.Second
	if cfg.ScheduledRetry {
		if !cfg.Offline {
			t.Fatal("scheduled retry requires offline fixture")
		}
		limit = 1800 * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()
	sync := &ingress.RouteSync{Ledger: data, Source: client, ID: source.ID, Generation: source.Generation}
	if err = sync.Synchronize(ctx, cfg.Routes, nil); err != nil {
		t.Fatal("real Worker route coordination failed", err)
	}
	if cfg.RuleToken != "" {
		origin, _ := url.Parse(source.Origin)
		worker := strings.Split(origin.Hostname(), ".")[0]
		rules, e := ingress.NewCloudflareRules(client, "0d84033826bc3cd0c283efdb57a90f7a", worker, cfg.RuleToken)
		if e != nil {
			t.Fatal("isolated Cloudflare rules invalid", e)
		}
		public := &ingress.CloudflareRuleSync{Ledger: data, Client: rules}
		if e = public.Synchronize(ctx, cfg.Routes); e != nil {
			t.Fatal("real Cloudflare rule coordination failed", e)
		}
		bindings, e := data.CloudflareRuleBindings(ctx, source.ID, source.Generation)
		if e != nil || len(bindings) != 3 {
			t.Fatal("public rule ledger incomplete", e)
		}
		for _, b := range bindings {
			if b.Fact.ID == "" || b.Fact.Fingerprint == "" || b.ObservedAt == nil {
				t.Fatal("public rule not read back")
			}
		}
		t.Log("real Cloudflare rule synchronization confirmed for all three addresses")
	}
	if cfg.Offline {
		nativeWorkerOfflineRoundTrip(t, ctx, cfg, data, backend, client)
		return
	}
	if cfg.NotifyToken != "" {
		nativeWorkerNotificationRoundTrip(t, ctx, cfg, data, backend, client)
		return
	}
	if err = os.WriteFile("/bridge/routes-ready", []byte("ready\n"), 0644); err != nil {
		t.Fatal("isolated route synchronization signal", err)
	}
	for {
		if _, abortErr := os.Stat("/bridge/abort"); abortErr == nil {
			t.Fatal("isolated controller stopped before SMTP submission")
		}
		if _, err = os.Stat("/bridge/smtp-submitted"); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("controlled real SMTP submission was not observed")
		}
		time.Sleep(time.Second)
	}
	stage := t.TempDir()
	if err = os.Chmod(stage, 0700); err != nil {
		t.Fatal(err)
	}
	native := &countedNativeIntake{Client: backend}
	consumer := &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: stage, SourceID: source.ID, Generation: source.Generation}
	deadline := time.Now().Add(150 * time.Second)
	var completed int
	var pollErr, workErr error
	for {
		pollErr = consumer.Poll(ctx)
		workErr = consumer.Work(ctx)
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, source.ID).Scan(&completed); err != nil {
			t.Fatal("isolated ingress ledger", err)
		}
		if completed == 3 {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			t.Fatal("real Worker native import incomplete", completed, "poll", pollErr, "work", workErr)
		}
		time.Sleep(2 * time.Second)
	}
	if native.posts.Load() != 3 {
		t.Fatal("real Worker import repeated native submission", native.posts.Load())
	}
	page, err := client.List(ctx, "")
	if err != nil || len(page.Messages) != 3 || page.Cursor != nil {
		t.Fatal("Worker delivered originals unavailable", err)
	}
	seen := map[string]bool{}
	for _, m := range page.Messages {
		address := m.Metadata.AcceptedAddress
		if seen[address] || m.Delivery == nil || m.Metadata.OwnerAnchor != cfg.Routes[0].OwnerAnchor {
			t.Fatal("Worker receipt or target mismatch")
		}
		seen[address] = true
		matched := false
		for _, route := range cfg.Routes {
			matched = matched || address == route.Address && m.Metadata.MailboxID == route.MailboxID && m.Metadata.ClaimVersion == route.ClaimVersion
		}
		if !matched {
			t.Fatal("Worker target not bound to expected mailbox")
		}
	}
	if err = consumer.Poll(ctx); err != nil {
		t.Fatal("real Worker replay poll", err)
	}
	if err = consumer.Work(ctx); err != nil || native.posts.Load() != 3 {
		t.Fatal("real Worker replay duplicated native delivery", err)
	}
	files, err := os.ReadDir(stage)
	if err != nil || len(files) != 0 {
		t.Fatal("confirmed Worker import retained local staging original", err)
	}
	t.Log(fmt.Sprintf("real Worker -> isolated PostgreSQL/DMS: %d completed targets; notification not configured", completed))
}

// A real Worker sends signed notices over a short-lived HTTPS tunnel to this
// isolated receiver. Its acknowledgement records a notice, not delivery.
func nativeWorkerNotificationRoundTrip(t *testing.T, ctx context.Context, cfg *nativeWorkerInput, data store.Store, backend *dms.Client, client *ingress.SourceClient) {
	t.Helper()
	cert, err := tls.X509KeyPair([]byte(cfg.NotifyCert), []byte(cfg.NotifyKey))
	if err != nil {
		t.Fatal("invalid isolated TLS certificate")
	}
	listener, err := net.Listen("unix", "/bridge/notify.sock")
	if err != nil {
		t.Fatal("isolated notification socket", err)
	}
	if err = os.Chmod("/bridge/notify.sock", 0666); err != nil {
		t.Fatal(err)
	} // Parent is private to the fixture owner.
	wake := make(chan struct{}, 1)
	var wakes atomic.Int32
	handler := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{cfg.Source.ID: {Generation: cfg.Source.Generation, Secret: cfg.NotifyToken}}, Wake: func(string) {
		wakes.Add(1)
		select {
		case wake <- struct{}{}:
		default:
		}
	}}
	mux := http.NewServeMux()
	mux.Handle("/api/ingress/notify", handler)
	mux.HandleFunc("/__.health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = server.Close(); _ = os.Remove("/bridge/notify.sock") })
	if err = os.WriteFile("/bridge/routes-ready", []byte("ready\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForSignal := func(name string) {
		t.Helper()
		for {
			if _, e := os.Stat("/bridge/abort"); e == nil {
				t.Fatal("isolated controller aborted")
			}
			if _, e := os.Stat("/bridge/" + name); e == nil {
				return
			}
			if ctx.Err() != nil {
				t.Fatal("isolated signal timeout", name)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	waitForSignal("consumer-start")
	stage := t.TempDir()
	if err = os.Chmod(stage, 0700); err != nil {
		t.Fatal("private notification staging directory", err)
	}
	native := &countedNativeIntake{Client: backend}
	consumer := &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: stage, SourceID: cfg.Source.ID, Generation: cfg.Source.Generation}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = consumer.Run(runCtx, wake) }()
	t.Cleanup(func() { cancel(); <-done })
	for {
		var ready bool
		err = data.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM mail.ingress_poll WHERE source_id=$1 AND last_success IS NOT NULL)`, cfg.Source.ID).Scan(&ready)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("initial poll not completed")
		}
		time.Sleep(250 * time.Millisecond)
	}
	started := time.Now()
	if err = os.WriteFile("/bridge/consumer-ready", []byte("ready\n"), 0644); err != nil {
		t.Fatal(err)
	}
	waitForSignal("smtp-submitted")
	var completed, notices, staged int
	stagedBeforePeriodic := false
	diagnostics := func() {
		t.Helper()
		for _, query := range []string{`SELECT state,last_error,count(*) FROM mail.ingress_notice WHERE source_id=$1 GROUP BY state,last_error`, `SELECT phase,last_error,count(*) FROM mail.ingress_message WHERE source_id=$1 GROUP BY phase,last_error`} {
			rows, e := data.DB.QueryContext(ctx, query, cfg.Source.ID)
			if e != nil {
				t.Log("ledger diagnostic unavailable")
				continue
			}
			for rows.Next() {
				var state, code string
				var count int
				if rows.Scan(&state, &code, &count) == nil {
					t.Logf("isolated ledger state=%s code=%s count=%d", state, code, count)
				}
			}
			_ = rows.Close()
		}
	}
	for {
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, cfg.Source.ID).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_notice WHERE source_id=$1`, cfg.Source.ID).Scan(&notices); err != nil {
			t.Fatal(err)
		}
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1`, cfg.Source.ID).Scan(&staged); err != nil {
			t.Fatal(err)
		}
		if staged == 3 && time.Since(started) < 55*time.Second {
			stagedBeforePeriodic = true
		}
		if completed == 3 && notices == 3 && stagedBeforePeriodic {
			break
		}
		if ctx.Err() != nil || time.Since(started) > 55*time.Second && !stagedBeforePeriodic || time.Since(started) > 150*time.Second {
			diagnostics()
			t.Fatal("notification-driven import incomplete", completed, notices, staged, wakes.Load(), "staged before periodic poll", stagedBeforePeriodic)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !stagedBeforePeriodic || wakes.Load() < 3 || native.posts.Load() != 3 {
		t.Fatal("notification wake or delivery count mismatch", wakes.Load(), native.posts.Load())
	}
	cancel()
	<-done
	page, err := client.List(ctx, "")
	if err != nil || len(page.Messages) != 3 || page.Cursor != nil {
		t.Fatal("Worker notified messages unavailable", err)
	}
	seen := map[string]bool{}
	for _, m := range page.Messages {
		if seen[m.Metadata.AcceptedAddress] || m.Delivery == nil || m.Notification == nil || m.Notification.State != "notified" {
			t.Fatal("notification or delivery state mismatch")
		}
		seen[m.Metadata.AcceptedAddress] = true
		matched := false
		for _, route := range cfg.Routes {
			matched = matched || m.Metadata.AcceptedAddress == route.Address && m.Metadata.MailboxID == route.MailboxID && m.Metadata.OwnerAnchor == route.OwnerAnchor && m.Metadata.ClaimVersion == route.ClaimVersion
		}
		if !matched {
			t.Fatal("Worker target mismatch")
		}
	}
	if err = consumer.Poll(ctx); err != nil {
		t.Fatal("replay poll", err)
	}
	if err = consumer.Work(ctx); err != nil || native.posts.Load() != 3 {
		t.Fatal("replay duplicated native delivery", err)
	}
	files, err := os.ReadDir(stage)
	if err != nil || len(files) != 0 {
		t.Fatal("staged originals retained", err)
	}
	t.Logf("real Worker signed notices: %d notices, %d staged before periodic poll, %d completed targets, %d wakes", notices, staged, completed, wakes.Load())
}

// The controller keeps the public notification tunnel disconnected while the
// consumer is stopped. Only after the real Worker reports failed notification
// and its originals are read back does it wait 70 seconds and restore the
// tunnel. This simulates a disconnected endpoint, not a whole NAS power loss.
func nativeWorkerOfflineRoundTrip(t *testing.T, ctx context.Context, cfg *nativeWorkerInput, data store.Store, backend *dms.Client, client *ingress.SourceClient) {
	t.Helper()
	cert, err := tls.X509KeyPair([]byte(cfg.NotifyCert), []byte(cfg.NotifyKey))
	if err != nil {
		t.Fatal("invalid isolated TLS certificate")
	}
	listener, err := net.Listen("unix", "/bridge/notify.sock")
	if err != nil {
		t.Fatal("isolated notification socket", err)
	}
	if err = os.Chmod("/bridge/notify.sock", 0666); err != nil {
		t.Fatal("isolated socket permissions", err)
	}
	wake := make(chan struct{}, 1)
	handler := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{cfg.Source.ID: {Generation: cfg.Source.Generation, Secret: cfg.NotifyToken}}, Wake: func(string) {
		select {
		case wake <- struct{}{}:
		default:
		}
	}}
	mux := http.NewServeMux()
	mux.Handle("/api/ingress/notify", handler)
	mux.HandleFunc("/__.health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = server.Close(); _ = os.Remove("/bridge/notify.sock") })
	signal := func(name string) {
		t.Helper()
		if err := os.WriteFile("/bridge/"+name, []byte("ready\n"), 0644); err != nil {
			t.Fatal("isolated signal unavailable", name)
		}
	}
	wait := func(name string) {
		t.Helper()
		for {
			if _, err := os.Stat("/bridge/abort"); err == nil {
				t.Fatal("isolated controller aborted")
			}
			if _, err := os.Stat("/bridge/" + name); err == nil {
				return
			}
			if ctx.Err() != nil {
				t.Fatal("isolated signal timeout", name)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	signal("routes-ready")
	wait("consumer-start")
	// The controller starts no public tunnel and no consumer for this phase.
	signal("consumer-ready")
	wait("smtp-submitted")
	deadline := time.Now().Add(100 * time.Second)
	var page ingress.SourcePage
	for {
		page, err = client.List(ctx, "")
		ready := err == nil && page.Cursor == nil && len(page.Messages) == 3
		if ready {
			for _, m := range page.Messages {
				ready = ready && m.Delivery == nil && m.Notification != nil && m.Notification.State == "failed" && m.Notification.Attempts >= 1
			}
		}
		if ready {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			t.Fatal("Worker did not expose three retained messages with failed notification")
		}
		time.Sleep(2 * time.Second)
	}
	seen := map[string]bool{}
	for _, message := range page.Messages {
		matched := false
		for _, route := range cfg.Routes {
			matched = matched || message.Metadata.AcceptedAddress == route.Address && message.Metadata.MailboxID == route.MailboxID && message.Metadata.OwnerAnchor == route.OwnerAnchor && message.Metadata.ClaimVersion == route.ClaimVersion
		}
		if !matched || seen[message.Metadata.AcceptedAddress] {
			t.Fatal("offline target binding changed")
		}
		seen[message.Metadata.AcceptedAddress] = true
		var raw bytes.Buffer
		if _, err = client.Download(ctx, message, &raw); err != nil || raw.Len() == 0 {
			t.Fatal("offline original unavailable")
		}
	}
	var count int
	if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_notice WHERE source_id=$1`, cfg.Source.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("offline endpoint recorded a notice")
	}
	signal("offline-observed")
	if cfg.ScheduledRetry {
		wait("retry-start")
		// Cloudflare documents up to 15 minutes of schedule propagation;
		// leave time for propagation plus multiple one-minute invocations.
		deadline = time.Now().Add(1200 * time.Second)
		for {
			if _, abortErr := os.Stat("/bridge/abort"); abortErr == nil {
				t.Fatal("isolated controller aborted during retry")
			}
			page, err = client.List(ctx, "")
			ready := err == nil && page.Cursor == nil && len(page.Messages) == 3
			if ready {
				for _, m := range page.Messages {
					ready = ready && m.Delivery == nil && m.Notification != nil && m.Notification.State == "notified" && m.Notification.Attempts >= 2
				}
			}
			if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_notice WHERE source_id=$1`, cfg.Source.ID).Scan(&count); err != nil {
				t.Fatal("scheduled retry notice ledger unavailable")
			}
			if ready && count == 3 {
				break
			}
			if ctx.Err() != nil || time.Now().After(deadline) {
				t.Fatal("scheduled notification retries not confirmed")
			}
			time.Sleep(2 * time.Second)
		}
		signal("retry-observed")
	}
	wait("offline-resume")
	page, err = client.List(ctx, "")
	if err != nil || page.Cursor != nil || len(page.Messages) != 3 {
		t.Fatal("offline originals did not remain available")
	}
	for _, message := range page.Messages {
		if message.Delivery != nil {
			t.Fatal("offline source marked delivery without DMS")
		}
	}
	stage := t.TempDir()
	if err = os.Chmod(stage, 0700); err != nil {
		t.Fatal("private staging unavailable")
	}
	native := &countedNativeIntake{Client: backend}
	consumer := &ingress.Consumer{Ledger: data, Source: client, Native: native, Directory: stage, SourceID: cfg.Source.ID, Generation: cfg.Source.Generation}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = consumer.Run(runCtx, wake) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline = time.Now().Add(150 * time.Second)
	for {
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, cfg.Source.ID).Scan(&count); err != nil {
			t.Fatal("offline recovery ledger unavailable")
		}
		if count == 3 {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			t.Fatal("offline recovery incomplete", count)
		}
		time.Sleep(time.Second)
	}
	cancel()
	<-done
	if native.posts.Load() != 3 {
		t.Fatal("offline recovery duplicated native submission")
	}
	page, err = client.List(ctx, "")
	if err != nil || page.Cursor != nil || len(page.Messages) != 3 {
		t.Fatal("recovered originals unavailable")
	}
	for _, message := range page.Messages {
		if message.Delivery == nil {
			t.Fatal("recovered delivery receipt missing")
		}
	}
	if err = consumer.Poll(ctx); err != nil {
		t.Fatal("recovery replay poll failed")
	}
	if err = consumer.Work(ctx); err != nil || native.posts.Load() != 3 {
		t.Fatal("recovery replay duplicated delivery")
	}
	files, err := os.ReadDir(stage)
	if err != nil || len(files) != 0 {
		t.Fatal("recovered local staging not cleaned")
	}
	t.Log("real Worker: three failed notifications and retained originals, disconnected interval, recovery and replay verified")
}

func workerSignalDirectory(t *testing.T) {
	t.Helper()
	info, err := os.Lstat("/bridge")
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("isolated worker bridge missing")
	}
	socket, err := os.Lstat(filepath.Join("/bridge", "cloud.sock"))
	if err != nil || socket.Mode()&os.ModeSocket == 0 {
		t.Fatal("isolated worker tunnel missing")
	}
	for _, name := range []string{"routes-ready", "smtp-submitted"} {
		if _, err = os.Lstat(filepath.Join("/bridge", name)); !os.IsNotExist(err) {
			t.Fatal("stale worker fixture signal")
		}
	}
}
