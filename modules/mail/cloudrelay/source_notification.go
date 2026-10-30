package cloudrelay

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// sourceNotice is part of the source ledger, separate from delivery. Only a
// digest of the endpoint/key binding is persisted, never the notification key.
type sourceNotice struct {
	ID      string `json:"id"`
	Binding string `json:"binding"`
	Started int64  `json:"started"`
	NotificationProgress
	NotifiedAt *time.Time `json:"notified_at,omitempty"`
}

func (l *RouteLedger) sourceNoticeID(e EnvelopeReservation, r EnvelopeRecipient) string {
	meta := l.envelopeMetadata(e, r)
	hash := sha256.Sum256([]byte("anas-mail-vps-notification-v1\x00" + l.source + "\x00" + l.generation + "\x00" + meta.MessageID + "\x00" + meta.MailboxID))
	b := hash[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func (l *RouteLedger) validSourceNotice(e EnvelopeReservation, r EnvelopeRecipient) bool {
	n := r.Notice
	if n == nil || n.ID != l.sourceNoticeID(e, r) || !secretValue.MatchString(n.Binding) || n.Started < e.ReceivedAt.Unix() || n.Started <= 0 || n.Started > math.MaxInt64-300 || n.Attempts < 1 || n.Attempts > 3 || n.HTTPStatus < 0 || n.HTTPStatus > 599 {
		return false
	}
	switch n.Check {
	case "", "timeout", "unreachable", "http-response", "invalid-receipt":
	default:
		return false
	}
	if n.State == "notified" {
		return n.NotifiedAt != nil && !n.NotifiedAt.IsZero() && !n.NotifiedAt.Before(e.ReceivedAt) && n.NextAttempt == 0 && n.Check == "" && (n.HTTPStatus == 200 || n.HTTPStatus == 202)
	}
	if n.NotifiedAt != nil {
		return false
	}
	switch n.State {
	case "sending":
		return n.Check == "" && n.HTTPStatus == 0 && (n.Attempts < 3 && n.NextAttempt == n.Started+notificationDelays[n.Attempts] || n.Attempts == 3 && n.NextAttempt == 0)
	case "failed":
		return n.Attempts < 3 && n.Check != "" && n.NextAttempt == n.Started+notificationDelays[n.Attempts]
	case "exhausted":
		return n.Attempts == 3 && n.Check != "" && n.NextAttempt == 0
	case "configuration-error":
		return n.Check != "" && n.NextAttempt == 0
	case "unknown":
		return n.Attempts == 3 && n.Check == "" && n.HTTPStatus == 0 && n.NextAttempt == 0
	default:
		return false
	}
}
func publicSourceNotice(n *sourceNotice) *ingress.SourceNotification {
	if n == nil {
		return nil
	}
	out := &ingress.SourceNotification{State: n.State, Attempts: n.Attempts, Check: n.Check, HTTPStatus: n.HTTPStatus, NotifiedAt: n.NotifiedAt}
	if n.NextAttempt > 0 {
		next := time.Unix(n.NextAttempt, 0).UTC()
		out.NextAttemptAt = &next
	}
	return out
}

type claimedNotice struct {
	ID, MessageID, MailboxID, Binding string
	Attempts                          int
}

// ProcessNextNotification performs at most one due notification attempt for a
// durably stored source target. The send claim is synced before I/O, and the
// result is merged into a fresh ledger afterward, never replacing delivery.
// A lost result retains its attempt and fixed ID for a later retry. It does not
// confirm DMS delivery or delete originals.
func (s *OriginalStore) ProcessNextNotification(ctx context.Context, n *Notifier, now time.Time) (bool, error) {
	if s == nil || s.ledger == nil || n == nil || n.client == nil || len(n.secret) != 32 || n.source != s.ledger.source || n.generation != s.ledger.generation || now.Unix() <= 0 || now.Unix() > math.MaxInt64-300 {
		return false, ErrConfiguration
	}
	binding := notificationBinding(n)
	var claim claimedNotice
	worked := false
	err := s.sourceLocked(func(dir, raw int, disk routeDisk) (routeDisk, error) {
		bestI, bestJ := -1, -1
		var bestSequence int64
		for i, e := range disk.Reservations {
			if e.State != "ready" {
				continue
			}
			for j, r := range e.Recipients {
				if r.Notice != nil {
					switch r.Notice.State {
					case "notified", "exhausted", "configuration-error", "unknown":
						continue
					}
					if r.Notice.Binding != binding {
						return routeDisk{}, ErrNotificationState
					}
					if r.Notice.Attempts < 3 && now.Unix() < r.Notice.Started+notificationDelays[r.Notice.Attempts] {
						continue
					}
				}
				if bestI < 0 || r.Sequence < bestSequence {
					bestI, bestJ, bestSequence = i, j, r.Sequence
				}
			}
		}
		if bestI < 0 {
			return disk, nil
		}
		e := disk.Reservations[bestI]
		r := e.Recipients[bestJ]
		if now.Before(e.ReceivedAt) {
			return routeDisk{}, ErrNotificationState
		}
		if _, e2 := readOriginal(raw, e.ID, e.Bytes, e.SHA256); e2 != nil {
			return routeDisk{}, ErrOriginalState
		}
		notice := r.Notice
		if notice == nil {
			notice = &sourceNotice{ID: s.ledger.sourceNoticeID(e, r), Binding: binding, Started: now.Unix()}
		}
		if notice.Attempts == 3 {
			notice.State = "unknown"
			notice.NextAttempt = 0
			disk.Reservations[bestI].Recipients[bestJ].Notice = notice
			if e2 := s.ledger.write(dir, disk); e2 != nil {
				return routeDisk{}, e2
			}
			worked = true
			return disk, nil
		}
		notice.Attempts++
		notice.State = "sending"
		notice.Check = ""
		notice.HTTPStatus = 0
		notice.NextAttempt = 0
		if notice.Attempts < 3 {
			notice.NextAttempt = notice.Started + notificationDelays[notice.Attempts]
		}
		disk.Reservations[bestI].Recipients[bestJ].Notice = notice
		if e2 := s.ledger.write(dir, disk); e2 != nil {
			return routeDisk{}, e2
		}
		meta := s.ledger.envelopeMetadata(e, r)
		claim = claimedNotice{ID: notice.ID, MessageID: meta.MessageID, MailboxID: meta.MailboxID, Binding: binding, Attempts: notice.Attempts}
		worked = true
		return disk, nil
	})
	if err != nil || claim.ID == "" {
		return worked, err
	}
	result := n.Send(ctx, Notification{ID: claim.ID, MessageID: claim.MessageID, MailboxID: claim.MailboxID}, now)
	if result.State != "notified" && result.State != "failed" && result.State != "configuration-error" {
		return true, ErrNotificationUnknown
	}
	commit := func(dir, _ int, disk routeDisk) (routeDisk, error) {
		_, i, j, e := s.findEntry(disk, claim.MessageID, claim.MailboxID)
		if e != nil {
			return routeDisk{}, ErrNotificationUnknown
		}
		notice := disk.Reservations[i].Recipients[j].Notice
		if notice == nil || notice.ID != claim.ID || notice.Binding != claim.Binding {
			return routeDisk{}, ErrNotificationUnknown
		}
		if notice.State != "sending" || notice.Attempts != claim.Attempts {
			return disk, nil
		}
		notice.State, notice.Check, notice.HTTPStatus = result.State, result.Check, result.HTTPStatus
		if notice.State == "failed" && notice.Attempts == 3 {
			notice.State = "exhausted"
		}
		if notice.State != "failed" {
			notice.NextAttempt = 0
		}
		if notice.State == "notified" {
			at := now.UTC()
			notice.NotifiedAt = &at
		}
		if e = s.ledger.write(dir, disk); e != nil {
			return routeDisk{}, ErrNotificationUnknown
		}
		return disk, nil
	}
	// A simultaneous status/read/ack request must not routinely lose an
	// otherwise confirmed result. Retry lock contention without resending;
	// persistent contention still leaves the recorded sending state unknown.
	for attempt := 0; attempt < 40; attempt++ {
		err = s.sourceLocked(commit)
		if !errors.Is(err, ErrRouteBusy) {
			break
		}
		if attempt == 39 {
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return true, ErrNotificationUnknown
		case <-timer.C:
		}
	}
	if err != nil {
		return true, ErrNotificationUnknown
	}
	return true, nil
}

// RunNotificationScheduler polls persisted due work independently of NAS user
// sessions. A supervisor still has to start it with private source credentials.
func (s *OriginalStore) RunNotificationScheduler(ctx context.Context, n *Notifier, onError func(error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		worked, err := s.ProcessNextNotification(ctx, n, time.Now())
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := time.Second
		if errors.Is(err, ErrRouteBusy) {
			// Another local source operation normally holds this lock briefly.
			delay = 50 * time.Millisecond
		} else if err != nil {
			if onError != nil {
				onError(err)
			}
			delay = 5 * time.Second
		} else if worked {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
