// TEST_CASES: MAILMOD-T-039
package cloudrelay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func notificationTaskFixture(t *testing.T, handler http.Handler) (*Notifier, Notification, string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	config := NotificationConfig{Origin: server.URL, SourceID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", Secret: strings.Repeat("c", 64), Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}
	n, err := newNotifier(config, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return n, Notification{ID: "33333333-3333-4333-8333-333333333333", MessageID: "message-1", MailboxID: "44444444-4444-4444-8444-444444444444"}, dir
}

func validNoticeReply(w http.ResponseWriter, r *http.Request) {
	var value struct {
		Source     string `json:"source_id"`
		Generation string `json:"generation"`
		ID         string `json:"notification_id"`
	}
	if json.NewDecoder(r.Body).Decode(&value) != nil {
		w.WriteHeader(400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(map[string]any{"schema": notificationSchema, "source_id": value.Source, "generation": value.Generation, "notification_id": value.ID, "accepted": true})
}

func TestNotificationTaskPersistsRetryAndAcknowledgement(t *testing.T) {
	var calls atomic.Int32
	var directory string
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := os.ReadFile(filepath.Join(directory, "notification.json"))
		var state notificationState
		if err != nil || json.Unmarshal(body, &state) != nil || state.State != "sending" || state.Attempts != int(calls.Load())+1 {
			t.Error("send started before durable claim")
			w.WriteHeader(500)
			return
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		validNoticeReply(w, r)
	}))
	directory = dir
	start := time.Unix(1800000000, 0)
	first, err := ProcessNotification(t.Context(), dir, n, notice, start)
	if err != nil || first.State != "failed" || first.Attempts != 1 || first.NextAttempt != start.Unix()+60 {
		t.Fatal("failure was not scheduled", first, err)
	}
	waiting, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(59*time.Second))
	if err != nil || waiting.State != "failed" || calls.Load() != 1 {
		t.Fatal("early retry", waiting, err)
	}
	second, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(60*time.Second))
	if err != nil || second.State != "notified" || second.Attempts != 2 || second.NextAttempt != 0 || calls.Load() != 2 {
		t.Fatal("notice acknowledgement missing", second, err)
	}
	again, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(10*time.Minute))
	if err != nil || again.State != "notified" || calls.Load() != 2 {
		t.Fatal("acknowledged notice was resent", again, err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "notification.json"))
	if err != nil || strings.Contains(string(content), strings.Repeat("c", 64)) || strings.Contains(string(content), "raw.eml") {
		t.Fatal("private notification material leaked", err)
	}
}

func TestNotificationTaskExhaustsAndRetainsMessage(t *testing.T) {
	var calls atomic.Int32
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	mail := filepath.Join(dir, "raw.eml")
	if err := os.WriteFile(mail, []byte("message still retained"), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1800000000, 0)
	for i, delta := range []time.Duration{0, time.Minute, 5 * time.Minute} {
		progress, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(delta))
		want := "failed"
		if i == 2 {
			want = "exhausted"
		}
		if err != nil || progress.State != want || progress.Attempts != i+1 {
			t.Fatal("incorrect retry outcome", progress, err)
		}
	}
	if _, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(time.Hour)); err != nil || calls.Load() != 3 {
		t.Fatal("exhausted notice was resent", err)
	}
	if raw, err := os.ReadFile(mail); err != nil || string(raw) != "message still retained" {
		t.Fatal("notification affected message", err)
	}
}

func TestNotificationTaskStopsOnAuthenticationFailureAndBindingChange(t *testing.T) {
	var calls atomic.Int32
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(401) }))
	start := time.Unix(1800000000, 0)
	progress, err := ProcessNotification(t.Context(), dir, n, notice, start)
	if err != nil || progress.State != "configuration-error" || progress.HTTPStatus != 401 {
		t.Fatal("authentication failure not recorded", progress, err)
	}
	if _, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(time.Hour)); err != nil || calls.Load() != 1 {
		t.Fatal("configuration failure was retried", err)
	}
	changed := *n
	changed.secret = []byte(strings.Repeat("d", 32))
	if _, err := ProcessNotification(t.Context(), dir, &changed, notice, start.Add(time.Hour)); !errors.Is(err, ErrNotificationState) {
		t.Fatal("changed binding was silently adopted", err)
	}
}

func TestNotificationTaskLostResultPersistsUnknownAndRetriesSameID(t *testing.T) {
	var calls atomic.Int32
	var directory string
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			if err := os.Chmod(directory, 0500); err != nil {
				t.Error(err)
			}
		}
		validNoticeReply(w, r)
	}))
	directory = dir
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	start := time.Unix(1800000000, 0)
	if _, err := ProcessNotification(t.Context(), dir, n, notice, start); !errors.Is(err, ErrNotificationUnknown) {
		t.Fatal("lost local result not reported unknown", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "notification.json"))
	var persisted notificationState
	if err != nil || json.Unmarshal(body, &persisted) != nil || persisted.State != "sending" || persisted.ID != notice.ID || persisted.Attempts != 1 {
		t.Fatal("missing issued intent", err)
	}
	progress, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(time.Minute))
	if err != nil || progress.State != "notified" || progress.Attempts != 2 || calls.Load() != 2 {
		t.Fatal("unknown result retry failed", progress, err)
	}
}

func TestNotificationTaskRecoversInterruptedClaims(t *testing.T) {
	var calls atomic.Int32
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); validNoticeReply(w, r) }))
	start := time.Unix(1800000000, 0)
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	state := notificationState{Schema: notificationSchema, Source: n.source, Generation: n.generation, ID: notice.ID, MessageID: notice.MessageID, MailboxID: notice.MailboxID, Binding: notificationBinding(n), Started: start.Unix(), NotificationProgress: NotificationProgress{State: "sending", Attempts: 1, NextAttempt: start.Unix() + 60}}
	if err := writeNotificationState(fd, state); err != nil {
		t.Fatal(err)
	}
	if progress, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(59*time.Second)); err != nil || progress.State != "sending" || calls.Load() != 0 {
		t.Fatal("interrupted claim retried early", progress, err)
	}
	if progress, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(60*time.Second)); err != nil || progress.State != "notified" || calls.Load() != 1 {
		t.Fatal("interrupted claim failed to retry", progress, err)
	}
	state.State, state.Attempts, state.NextAttempt = "sending", 3, 0
	if err := writeNotificationState(fd, state); err != nil {
		t.Fatal(err)
	}
	if progress, err := ProcessNotification(t.Context(), dir, n, notice, start.Add(10*time.Minute)); err != nil || progress.State != "unknown" || calls.Load() != 1 {
		t.Fatal("final unknown attempt was resent", progress, err)
	}
}

func TestNotificationTaskRejectsChangedIdentityAndUnsafeFiles(t *testing.T) {
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(validNoticeReply))
	start := time.Unix(1800000000, 0)
	if _, err := ProcessNotification(t.Context(), dir, n, notice, start); err != nil {
		t.Fatal(err)
	}
	changed := notice
	changed.MailboxID = "55555555-5555-4555-8555-555555555555"
	if _, err := ProcessNotification(t.Context(), dir, n, changed, start); !errors.Is(err, ErrNotificationState) {
		t.Fatal("changed mailbox accepted", err)
	}
	zero := new(Notifier)
	if _, err := ProcessNotification(t.Context(), dir, zero, notice, start); !errors.Is(err, ErrConfiguration) {
		t.Fatal("uninitialized notifier accepted", err)
	}
	if err := os.Remove(filepath.Join(dir, "notification.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".notification.lock", filepath.Join(dir, "notification.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessNotification(t.Context(), dir, n, notice, start); !errors.Is(err, ErrNotificationState) {
		t.Fatal("symlink accepted", err)
	}
}

func TestNotificationTaskSerializesConcurrentAttempts(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	n, notice, dir := notificationTaskFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; validNoticeReply(w, r) }))
	start := time.Unix(1800000000, 0)
	done := make(chan error, 1)
	go func() { _, err := ProcessNotification(context.Background(), dir, n, notice, start); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal("first attempt failed before send", err)
	case <-time.After(10 * time.Second):
		t.Fatal("first attempt did not start")
	}
	_, err := ProcessNotification(t.Context(), dir, n, notice, start)
	close(release)
	if !errors.Is(err, ErrNotificationBusy) {
		t.Fatal("parallel attempt was not blocked", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
