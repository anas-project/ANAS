// TEST_CASES: MAILMOD-T-049
package cloudrelay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
	"golang.org/x/sys/unix"
)

type recordedNotices struct {
	mu      sync.Mutex
	notices map[string]store.IngressNotice
	wakes   int
}

func (r *recordedNotices) RecordIngressNotice(_ context.Context, n store.IngressNotice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.notices[n.ID]; ok && old != n {
		return store.ErrIngressChanged
	}
	r.notices[n.ID] = n
	return nil
}
func (r *recordedNotices) wake(_ string) { r.mu.Lock(); r.wakes++; r.mu.Unlock() }

func notifierForServer(t *testing.T, server *httptest.Server) *Notifier {
	t.Helper()
	n, err := newNotifier(NotificationConfig{Origin: server.URL, SourceID: routeSource, Generation: routeGeneration, Secret: strings.Repeat("c", 64), Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestSourceNotificationRetryAndSeparateDelivery(t *testing.T) {
	_, source, dir := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test", "alias@example.test"}, []byte("original")); err != nil {
		t.Fatal(err)
	}
	registrar := &recordedNotices{notices: map[string]store.IngressNotice{}}
	now := time.Now().UTC().Add(time.Second)
	handler := ingress.NotificationHandler{Store: registrar, Sources: map[string]ingress.NotificationSource{routeSource: {Generation: routeGeneration, Secret: strings.Repeat("c", 64)}}, Wake: registrar.wake, Now: func() time.Time { return now }}
	var mu sync.Mutex
	var ids []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var payload struct {
			ID        string `json:"notification_id"`
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(body, &payload) != nil {
			t.Error("bad body")
			return
		}
		if bytes.Contains(body, []byte("original")) || bytes.Contains(body, []byte(strings.Repeat("c", 64))) {
			t.Error("notification leaked content or key")
		}
		mu.Lock()
		ids = append(ids, payload.ID)
		first := len(ids) == 1
		mu.Unlock()
		if first {
			w.WriteHeader(503)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	n := notifierForServer(t, server)
	client, _, _ := sourceHTTPFixture(t, source)
	if worked, err := source.ProcessNextNotification(t.Context(), n, now); !worked || err != nil {
		t.Fatalf("first: %v %v", worked, err)
	}
	page, err := client.List(t.Context(), "")
	if err != nil || page.Messages[0].Notification == nil || page.Messages[0].Notification.State != "failed" || page.Messages[0].Delivery != nil {
		t.Fatalf("failure: %+v %v", page, err)
	}
	changed := *n
	changed.secret = []byte(strings.Repeat("d", 32))
	if _, err = source.ProcessNextNotification(t.Context(), &changed, now); !errors.Is(err, ErrNotificationState) {
		t.Fatalf("unfinished notice silently rebound: %v", err)
	}
	ledger, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	source, err = OpenOriginalStore(ledger, routeMax+1000, 7)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := source.ProcessNextNotification(t.Context(), n, now); !worked || err != nil {
		t.Fatalf("other recipient: %v %v", worked, err)
	}
	if worked, err := source.ProcessNextNotification(t.Context(), n, now.Add(59*time.Second)); worked || err != nil {
		t.Fatalf("early retry: %v %v", worked, err)
	}
	now = now.Add(time.Minute)
	if worked, err := source.ProcessNextNotification(t.Context(), n, now); !worked || err != nil {
		t.Fatalf("retry: %v %v", worked, err)
	}
	mu.Lock()
	if len(ids) != 3 || ids[0] != ids[2] || ids[0] == ids[1] {
		t.Fatalf("notification IDs: %v", ids)
	}
	mu.Unlock()
	registrar.mu.Lock()
	if len(registrar.notices) != 2 || registrar.wakes != 2 {
		t.Fatal("NAS record/wake counts")
	}
	registrar.mu.Unlock()
	page, err = client.List(t.Context(), "")
	if err != nil || len(page.Messages) != 2 {
		t.Fatal(err)
	}
	for _, m := range page.Messages {
		if m.Notification == nil || m.Notification.State != "notified" || m.Delivery != nil {
			t.Fatalf("notice changed delivery: %+v", m)
		}
		var out bytes.Buffer
		if _, err = client.Download(t.Context(), m, &out); err != nil || out.String() != "original" {
			t.Fatalf("raw: %v", err)
		}
	}
}
func TestSourceNotificationLateResultPreservesDelivery(t *testing.T) {
	_, source, _ := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("message")); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; validNoticeReply(w, r) }))
	defer server.Close()
	n := notifierForServer(t, server)
	client, _, _ := sourceHTTPFixture(t, source)
	now := time.Now().UTC().Add(time.Second)
	done := make(chan error, 1)
	go func() { _, err := source.ProcessNextNotification(context.Background(), n, now); done <- err }()
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("send did not start")
	}
	page, err := client.List(t.Context(), "")
	if err != nil || len(page.Messages) != 1 {
		t.Fatal(err)
	}
	m := page.Messages[0]
	receipt := dms.IntakeReceipt{ID: m.Metadata.Key(), MailboxID: m.Metadata.MailboxID, SHA256: m.Metadata.SHA256, State: "stored"}
	if err = client.Acknowledge(t.Context(), m, receipt); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	page, err = client.List(t.Context(), "")
	if err != nil || page.Messages[0].Delivery == nil || page.Messages[0].Notification == nil || page.Messages[0].Notification.State != "notified" {
		t.Fatalf("lost delivery or notice: %+v %v", page, err)
	}
}
func TestSourceNotificationRecoversUnknownAndRejectsPending(t *testing.T) {
	ledger, source, _ := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("message")); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(validNoticeReply))
	defer server.Close()
	n := notifierForServer(t, server)
	now := time.Now().UTC().Add(time.Second)
	// Simulate a process dying after the last attempt's durable send claim.
	err := source.sourceLocked(func(dir, _ int, disk routeDisk) (routeDisk, error) {
		e := disk.Reservations[0]
		r := e.Recipients[0]
		r.Notice = &sourceNotice{ID: ledger.sourceNoticeID(e, r), Binding: notificationBinding(n), Started: now.Unix(), NotificationProgress: NotificationProgress{State: "sending", Attempts: 3}}
		disk.Reservations[0].Recipients[0] = r
		return disk, ledger.write(dir, disk)
	})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := source.ProcessNextNotification(t.Context(), n, now.Add(5*time.Minute)); !worked || err != nil {
		t.Fatalf("unknown recovery: %v %v", worked, err)
	}
	client, _, _ := sourceHTTPFixture(t, source)
	page, err := client.List(t.Context(), "")
	if err != nil || page.Messages[0].Notification == nil || page.Messages[0].Notification.State != "unknown" || page.Messages[0].Delivery != nil {
		t.Fatalf("unknown state: %+v %v", page, err)
	}
	if worked, err := source.ProcessNextNotification(t.Context(), n, now.Add(10*time.Minute)); worked || err != nil {
		t.Fatalf("unknown retried: %v %v", worked, err)
	}
}

func TestSourceNotificationPendingAndCorruptOriginalAreNotSent(t *testing.T) {
	ledger, source, dir := originalFixture(t, routeMax+1000)
	body := []byte("message")
	sum := sha256.Sum256(body)
	if _, err := ledger.ReserveEnvelope(routeOp3, "", hex.EncodeToString(sum[:]), int64(len(body)), []string{"alice@example.test"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); validNoticeReply(w, r) }))
	defer server.Close()
	n := notifierForServer(t, server)
	now := time.Now().UTC().Add(time.Second)
	if worked, err := source.ProcessNextNotification(t.Context(), n, now); worked || err != nil || calls.Load() != 0 {
		t.Fatalf("pending sent: %v %v", worked, err)
	}
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, body); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "originals", "raw-"+routeOp3), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if worked, err := source.ProcessNextNotification(t.Context(), n, now); worked || !errors.Is(err, ErrOriginalState) || calls.Load() != 0 {
		t.Fatalf("corrupt original notified: %v %v", worked, err)
	}
}

func TestSourceNotificationSchedulerStartsAndStops(t *testing.T) {
	_, source, dir := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("message")); err != nil {
		t.Fatal(err)
	}
	received := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { validNoticeReply(w, r); received <- struct{}{} }))
	defer server.Close()
	n := notifierForServer(t, server)
	fd, err := unix.Open(filepath.Join(dir, "routes.lock"), unix.O_RDWR|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var reported atomic.Int32
	go func() { done <- source.RunNotificationScheduler(ctx, n, func(error) { reported.Add(1) }) }()
	time.Sleep(150 * time.Millisecond)
	if err = unix.Flock(fd, unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("scheduler did not recover promptly from ordinary lock contention")
	}
	if reported.Load() != 0 {
		t.Fatal("ordinary lock contention was reported as a service error")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestSourceNotificationExhaustsWithoutDeletingOriginal(t *testing.T) {
	_, source, dir := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("message")); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	n := notifierForServer(t, server)
	now := time.Now().UTC().Add(time.Second)
	for _, delta := range []time.Duration{0, time.Minute, 5 * time.Minute} {
		if worked, err := source.ProcessNextNotification(t.Context(), n, now.Add(delta)); !worked || err != nil {
			t.Fatalf("attempt: %v %v", worked, err)
		}
	}
	if worked, err := source.ProcessNextNotification(t.Context(), n, now.Add(time.Hour)); worked || err != nil || calls.Load() != 3 {
		t.Fatalf("exhausted retry: %v %v", worked, err)
	}
	client, _, _ := sourceHTTPFixture(t, source)
	page, err := client.List(t.Context(), "")
	if err != nil || page.Messages[0].Notification == nil || page.Messages[0].Notification.State != "exhausted" || page.Messages[0].Delivery != nil {
		t.Fatalf("exhausted state: %+v %v", page, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "originals", "raw-"+routeOp3))
	if err != nil || string(data) != "message" {
		t.Fatalf("lost original: %v", err)
	}
}

func TestSourceNotificationRetriesResultLockWithoutResending(t *testing.T) {
	_, source, dir := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("message")); err != nil {
		t.Fatal(err)
	}
	entered, respond := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-respond
		validNoticeReply(w, r)
	}))
	defer server.Close()
	n := notifierForServer(t, server)
	done := make(chan error, 1)
	go func() {
		_, err := source.ProcessNextNotification(context.Background(), n, time.Now().Add(time.Second))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("notification did not start")
	}
	fd, err := unix.Open(filepath.Join(dir, "routes.lock"), unix.O_RDWR|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	close(respond)
	select {
	case err := <-done:
		unix.Flock(fd, unix.LOCK_UN)
		t.Fatalf("result did not wait for lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err = unix.Flock(fd, unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("commit after contention: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("result commit stalled")
	}
	client, _, _ := sourceHTTPFixture(t, source)
	page, err := client.List(t.Context(), "")
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Notification == nil || page.Messages[0].Notification.State != "notified" || calls.Load() != 1 {
		t.Fatalf("result: %+v %v", page, err)
	}
}
