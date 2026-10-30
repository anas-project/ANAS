package ingress

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
	"strconv"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

const notificationSchema = "anas.mail-notification/v1"

type NotificationSource struct{ Generation, Secret string }
type notificationRecorder interface {
	RecordIngressNotice(context.Context, store.IngressNotice) error
}
type NotificationHandler struct {
	Store   notificationRecorder
	Sources map[string]NotificationSource
	Wake    func(string)
	Now     func() time.Time
}
type notificationInput struct {
	Schema     string `json:"schema"`
	SourceID   string `json:"source_id"`
	Generation string `json:"generation"`
	ID         string `json:"notification_id"`
	MessageID  string `json:"message_id"`
	MailboxID  string `json:"mailbox_id"`
}

func notificationReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// notificationFields requires one unambiguous, exact protocol object before
// decoding. encoding/json otherwise accepts duplicate and case-folded keys.
func notificationFields(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	open, err := d.Token()
	if err != nil || open != json.Delim('{') {
		return false
	}
	keys := map[string]bool{"schema": false, "source_id": false, "generation": false, "notification_id": false, "message_id": false, "mailbox_id": false}
	for d.More() {
		item, e := d.Token()
		name, ok := item.(string)
		seen, allowed := keys[name]
		if e != nil || !ok || !allowed || seen {
			return false
		}
		keys[name] = true
		var value string
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

func (h NotificationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/ingress/notify" || r.Method != "POST" {
		notificationReply(w, 404, map[string]string{"error": "not-found"})
		return
	}
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 || r.URL.RawQuery != "" || r.URL.ForceQuery || r.Header.Get("Origin") != "" || r.Header.Get("Cookie") != "" {
		notificationReply(w, 400, map[string]string{"error": "invalid-notification"})
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 4097))
	if e != nil || len(raw) > 4096 {
		notificationReply(w, 413, map[string]string{"error": "invalid-notification"})
		return
	}
	var input notificationInput
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if !notificationFields(raw) || d.Decode(&input) != nil || d.Decode(&extra) != io.EOF || input.Schema != notificationSchema || !idPattern.MatchString(input.SourceID) || !idPattern.MatchString(input.Generation) || !idPattern.MatchString(input.ID) || !idPattern.MatchString(input.MailboxID) || !messagePattern.MatchString(input.MessageID) {
		notificationReply(w, 400, map[string]string{"error": "invalid-notification"})
		return
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	ts, e := strconv.ParseInt(r.Header.Get("X-ANAS-Mail-Time"), 10, 64)
	source, known := h.Sources[input.SourceID]
	if e != nil || ts < now.Unix()-300 || ts > now.Unix()+300 || len(r.Header.Values("X-ANAS-Mail-Time")) != 1 || len(r.Header.Values("X-ANAS-Mail-Signature")) != 1 || !known || source.Generation != input.Generation || !secretPattern.MatchString(source.Secret) || !secretPattern.MatchString(r.Header.Get("X-ANAS-Mail-Signature")) {
		notificationReply(w, 401, map[string]string{"error": "notification-denied"})
		return
	}
	key, _ := hex.DecodeString(source.Secret)
	signature, _ := hex.DecodeString(r.Header.Get("X-ANAS-Mail-Signature"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(r.Header.Get("X-ANAS-Mail-Time") + "."))
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), signature) {
		notificationReply(w, 401, map[string]string{"error": "notification-denied"})
		return
	}
	sum := sha256.Sum256(raw)
	n := store.IngressNotice{SourceID: input.SourceID, Generation: input.Generation, ID: input.ID, MessageID: input.MessageID, MailboxID: input.MailboxID, BodySHA256: hex.EncodeToString(sum[:])}
	if h.Store == nil {
		notificationReply(w, 503, map[string]string{"error": "notification-not-recorded"})
		return
	}
	if e = h.Store.RecordIngressNotice(r.Context(), n); e != nil {
		status := 503
		if errors.Is(e, store.ErrIngressChanged) {
			status = 409
		}
		notificationReply(w, status, map[string]string{"error": "notification-not-recorded"})
		return
	}
	// Wake only after a durable receipt. A full wake channel is harmless:
	// pending notices and the independent 60-second scan survive process loss.
	if h.Wake != nil {
		h.Wake(input.SourceID)
	}
	notificationReply(w, 202, struct {
		Schema     string `json:"schema"`
		SourceID   string `json:"source_id"`
		Generation string `json:"generation"`
		ID         string `json:"notification_id"`
		Accepted   bool   `json:"accepted"`
	}{notificationSchema, input.SourceID, input.Generation, input.ID, true})
}
