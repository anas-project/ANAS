package cloudrelay

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

const cloudSourceSchema = "anas.mail-source/v1"

type sourceAck struct {
	SourceID   string `json:"source_id"`
	Generation string `json:"generation"`
	Receipt    struct {
		ID        string `json:"id"`
		MailboxID string `json:"mailbox_id"`
		SHA256    string `json:"sha256"`
		State     string `json:"state"`
	} `json:"receipt"`
}
type sourceEntry struct {
	message       ingress.SourceMessage
	reservationID string
}

// NewSourceHandler is a candidate protocol handler, not a listening server.
// Its caller must arrange HTTPS with certificate validation and a private
// credential lifecycle. SMTP admission and runtime scheduling remain unwired,
// so this handler cannot enable a production source.
func NewSourceHandler(store *OriginalStore, reader, control string, notifier *Notifier) (http.Handler, error) {
	if store == nil || store.ledger == nil || !secretValue.MatchString(reader) || !secretValue.MatchString(control) || reader == control {
		return nil, ErrConfiguration
	}
	if notifier != nil && (notifier.client == nil || len(notifier.secret) != 32 || notifier.source != store.ledger.source || notifier.generation != store.ledger.generation || hex.EncodeToString(notifier.secret) == reader || hex.EncodeToString(notifier.secret) == control) {
		return nil, ErrConfiguration
	}
	routes, err := NewRouteHandler(store.ledger, reader, control)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Cookie")) != 0 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/v1/routes" {
			routes.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/control-status" {
			auth := r.Header.Values("Authorization")
			if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+control)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			query, err := url.ParseQuery(r.URL.RawQuery)
			if r.Method != http.MethodGet || err != nil || len(query) != 1 || len(query["challenge"]) != 1 || !secretValue.MatchString(query.Get("challenge")) {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			status := ingress.RelayControlStatus{Schema: "anas.mail-relay-control/v1", SourceID: store.ledger.source, Generation: store.ledger.generation, Domains: append([]string(nil), store.ledger.domains...)}
			if notifier != nil {
				status.NotificationOrigin = notifier.origin
				status.NotificationProof = ingress.RelayNotificationProof(notifier.secret, status.SourceID, status.Generation, query.Get("challenge"), notifier.origin)
			}
			sourceJSONResponse(w, status)
			return
		}
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+reader)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		query, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil {
			http.Error(w, "invalid query", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/v1/status" && r.Method == http.MethodGet && len(query) == 0 && !r.URL.ForceQuery {
			status, err := store.sourceStatus(notifier)
			if err != nil {
				sourceError(w, err)
				return
			}
			sourceJSONResponse(w, status)
			return
		}
		if r.URL.Path == "/v1/messages" && r.Method == http.MethodGet {
			for k, v := range query {
				if k != "cursor" && k != "limit" || len(v) != 1 {
					http.Error(w, "invalid query", http.StatusBadRequest)
					return
				}
			}
			limit := 100
			if query.Has("limit") {
				limit, e = strconv.Atoi(query.Get("limit"))
				if e != nil || limit < 1 || limit > 100 {
					http.Error(w, "invalid query", http.StatusBadRequest)
					return
				}
			}
			var after int64
			if query.Has("cursor") {
				cursor := query.Get("cursor")
				after, e = strconv.ParseInt(cursor, 10, 64)
				if e != nil || after < 1 || after >= routeMaxInteger || len(cursor) != 16 || strconv.FormatInt(after, 10) != strings.TrimLeft(cursor, "0") {
					http.Error(w, "invalid cursor", http.StatusBadRequest)
					return
				}
			}
			page, e := store.sourceList(after, limit)
			if e != nil {
				sourceError(w, e)
				return
			}
			sourceJSONResponse(w, page)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) < 3 || len(parts) > 4 || parts[0] != "v1" || parts[1] != "messages" || len(query) != 1 || len(query["target"]) != 1 || !identifier.MatchString(query.Get("target")) || len(parts[2]) != 53 || !messageID.MatchString(parts[2]) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		id, target := parts[2], query.Get("target")
		if len(parts) == 3 && r.Method == http.MethodGet {
			message, revision, e := store.sourceMessage(id, target)
			if e != nil {
				sourceError(w, e)
				return
			}
			sourceJSONResponse(w, struct {
				Schema        string                `json:"schema"`
				SourceID      string                `json:"source_id"`
				Generation    string                `json:"generation"`
				RouteRevision int64                 `json:"route_revision"`
				Message       ingress.SourceMessage `json:"message"`
			}{cloudSourceSchema, store.ledger.source, store.ledger.generation, revision, message})
			return
		}
		if len(parts) == 4 && parts[3] == "raw" && r.Method == http.MethodGet {
			message, body, e := store.sourceRaw(id, target)
			if e != nil {
				sourceError(w, e)
				return
			}
			w.Header().Set("Content-Type", "message/rfc822")
			w.Header().Set("ETag", strconv.Quote(message.ETag))
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}
		if len(parts) == 4 && parts[3] == "ack" && r.Method == http.MethodPost {
			media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if e != nil || media != "application/json" {
				http.Error(w, "invalid receipt", http.StatusBadRequest)
				return
			}
			raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2048))
			var fields map[string]json.RawMessage
			var input sourceAck
			if e != nil || !exactObject(raw, "source_id", "generation", "receipt") || json.Unmarshal(raw, &fields) != nil || !exactObject(fields["receipt"], "id", "mailbox_id", "sha256", "state") || json.Unmarshal(raw, &input) != nil || input.SourceID != store.ledger.source || input.Generation != store.ledger.generation || input.Receipt.MailboxID != target {
				http.Error(w, "invalid receipt", http.StatusBadRequest)
				return
			}
			delivery, e := store.sourceAck(id, target, input)
			if e != nil {
				sourceError(w, e)
				return
			}
			sourceJSONResponse(w, struct {
				Schema     string                 `json:"schema"`
				SourceID   string                 `json:"source_id"`
				Generation string                 `json:"generation"`
				MessageID  string                 `json:"message_id"`
				MailboxID  string                 `json:"mailbox_id"`
				Delivery   ingress.SourceDelivery `json:"delivery"`
			}{cloudSourceSchema, store.ledger.source, store.ledger.generation, id, target, delivery})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}), nil
}
func sourceJSONResponse(w http.ResponseWriter, v any) {
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > 1<<20 {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}
func sourceError(w http.ResponseWriter, e error) {
	status := http.StatusServiceUnavailable
	if errors.Is(e, os.ErrNotExist) || errors.Is(e, ErrOriginalPending) {
		status = http.StatusNotFound
	} else if errors.Is(e, ErrEnvelopeExpired) {
		status = http.StatusGone
	} else if errors.Is(e, ErrEnvelopeConflict) {
		status = http.StatusConflict
	} else if errors.Is(e, ErrConfiguration) {
		status = http.StatusBadRequest
	}
	http.Error(w, http.StatusText(status), status)
}
func (s *OriginalStore) entry(reservation EnvelopeReservation, recipient EnvelopeRecipient) sourceEntry {
	return sourceEntry{message: ingress.SourceMessage{Metadata: s.ledger.envelopeMetadata(reservation, recipient), Sequence: recipient.Sequence, Bytes: reservation.Bytes, ETag: reservation.SHA256, ReceivedAt: reservation.ReceivedAt, Delivery: recipient.Delivery, Notification: publicSourceNotice(recipient.Notice)}, reservationID: reservation.ID}
}
func (s *OriginalStore) sourceLocked(fn func(int, int, routeDisk) (routeDisk, error)) error {
	_, err := s.ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		raw, e := s.openDirectory(dir)
		if e != nil {
			return routeDisk{}, e
		}
		defer unix.Close(raw)
		if e = s.checkConfig(raw); e != nil {
			return routeDisk{}, e
		}
		// A previously interrupted rename must be durable before being exposed.
		if unix.Fsync(dir) != nil || unix.Fsync(raw) != nil {
			return routeDisk{}, ErrOriginalUnknown
		}
		return fn(dir, raw, disk)
	})
	return err
}

// Source API requests can race briefly with retention or notification work.
// Retry only lock acquisition: once a callback runs, never replay its effects.
func (s *OriginalStore) sourceLockedBrief(fn func(int, int, routeDisk) (routeDisk, error)) error {
	for attempt := 0; ; attempt++ {
		ran := false
		err := s.sourceLocked(func(dir, raw int, disk routeDisk) (routeDisk, error) {
			ran = true
			return fn(dir, raw, disk)
		})
		if ran || !errors.Is(err, ErrRouteBusy) || attempt == 9 {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}
func (s *OriginalStore) sourceList(after int64, limit int) (ingress.SourcePage, error) {
	page := ingress.SourcePage{Schema: cloudSourceSchema, SourceID: s.ledger.source, Generation: s.ledger.generation, Messages: []ingress.SourceMessage{}}
	err := s.sourceLockedBrief(func(_ int, _ int, disk routeDisk) (routeDisk, error) {
		if after > disk.State.Sequence {
			return routeDisk{}, ErrConfiguration
		}
		page.RouteRevision = disk.State.Revision
		barrier := disk.State.Sequence + 1
		for _, r := range disk.Reservations {
			if r.State == "pending" {
				for _, recipient := range r.Recipients {
					if recipient.Sequence < barrier {
						barrier = recipient.Sequence
					}
				}
			}
		}
		if after >= barrier {
			return routeDisk{}, ErrConfiguration
		}
		var entries []sourceEntry
		for _, r := range disk.Reservations {
			if r.State == "ready" {
				for _, recipient := range r.Recipients {
					if recipient.Sequence > after && recipient.Sequence < barrier {
						entries = append(entries, s.entry(r, recipient))
					}
				}
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].message.Sequence < entries[j].message.Sequence })
		if len(entries) > limit {
			cursor := strconv.FormatInt(entries[limit-1].message.Sequence, 10)
			cursor = strings.Repeat("0", 16-len(cursor)) + cursor
			page.Cursor = &cursor
			entries = entries[:limit]
		}
		for _, entry := range entries {
			page.Messages = append(page.Messages, entry.message)
		}
		return disk, nil
	})
	return page, err
}
func (s *OriginalStore) findEntry(disk routeDisk, id, target string) (sourceEntry, int, int, error) {
	for i, reservation := range disk.Reservations {
		for j, recipient := range reservation.Recipients {
			entry := s.entry(reservation, recipient)
			if entry.message.Metadata.MessageID == id && entry.message.Metadata.MailboxID == target {
				if reservation.State != "ready" {
					return sourceEntry{}, 0, 0, ErrOriginalPending
				}
				return entry, i, j, nil
			}
		}
	}
	return sourceEntry{}, 0, 0, os.ErrNotExist
}
func (s *OriginalStore) sourceMessage(id, target string) (ingress.SourceMessage, int64, error) {
	var message ingress.SourceMessage
	var revision int64
	err := s.sourceLockedBrief(func(_ int, _ int, disk routeDisk) (routeDisk, error) {
		entry, _, _, e := s.findEntry(disk, id, target)
		if e != nil {
			return routeDisk{}, e
		}
		message = entry.message
		revision = disk.State.Revision
		return disk, nil
	})
	return message, revision, err
}
func (s *OriginalStore) sourceRaw(id, target string) (ingress.SourceMessage, []byte, error) {
	var message ingress.SourceMessage
	var body []byte
	err := s.sourceLockedBrief(func(_ int, raw int, disk routeDisk) (routeDisk, error) {
		entry, _, _, e := s.findEntry(disk, id, target)
		if e != nil {
			return routeDisk{}, e
		}
		body, e = readOriginal(raw, entry.reservationID, entry.message.Bytes, entry.message.Metadata.SHA256)
		if e != nil {
			return routeDisk{}, ErrOriginalState
		}
		message = entry.message
		return disk, nil
	})
	return message, body, err
}
func (s *OriginalStore) sourceAck(id, target string, input sourceAck) (ingress.SourceDelivery, error) {
	var delivery ingress.SourceDelivery
	err := s.sourceLockedBrief(func(dir int, raw int, disk routeDisk) (routeDisk, error) {
		entry, i, j, e := s.findEntry(disk, id, target)
		if e != nil {
			delivery, e = s.retiredAck(raw, disk, id, target, input)
			if e != nil {
				return routeDisk{}, e
			}
			return disk, nil
		}
		if input.Receipt.ID != entry.message.Metadata.Key() || input.Receipt.SHA256 != entry.message.Metadata.SHA256 || input.Receipt.State != "stored" && input.Receipt.State != "processed" {
			return routeDisk{}, ErrEnvelopeConflict
		}
		if _, e = readOriginal(raw, entry.reservationID, entry.message.Bytes, entry.message.Metadata.SHA256); e != nil {
			return routeDisk{}, ErrOriginalState
		}
		current := disk.Reservations[i].Recipients[j].Delivery
		if current != nil {
			delivery = *current
			return disk, nil
		}
		now := time.Now().UTC()
		if now.Before(entry.message.ReceivedAt) {
			return routeDisk{}, ErrOriginalState
		}
		delivery = ingress.SourceDelivery{ReceiptID: input.Receipt.ID, SHA256: input.Receipt.SHA256, State: input.Receipt.State, AcknowledgedAt: now}
		disk.Reservations[i].Recipients[j].Delivery = &delivery
		if e = s.ledger.write(dir, disk); e != nil {
			return routeDisk{}, e
		}
		return disk, nil
	})
	return delivery, err
}
