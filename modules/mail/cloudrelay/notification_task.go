package cloudrelay

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrNotificationState   = errors.New("notification state invalid or inaccessible")
	ErrNotificationBusy    = errors.New("notification task is busy")
	ErrNotificationUnknown = errors.New("notification state outcome unknown")
)

// NotificationProgress reports a notification only, never DMS delivery.
type NotificationProgress struct {
	State       string `json:"state"`
	Attempts    int    `json:"attempts"`
	Check       string `json:"check,omitempty"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	NextAttempt int64  `json:"next_attempt,omitempty"`
}

type notificationState struct {
	Schema     string `json:"schema"`
	Source     string `json:"source"`
	Generation string `json:"generation"`
	ID         string `json:"id"`
	MessageID  string `json:"message_id"`
	MailboxID  string `json:"mailbox_id"`
	Binding    string `json:"binding"`
	Started    int64  `json:"started"`
	NotificationProgress
}

var notificationDelays = [...]int64{0, 60, 300}

func secureNoticeFile(fd int, directory bool) bool {
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0077 != 0 {
		return false
	}
	if directory {
		return stat.Mode&unix.S_IFMT == unix.S_IFDIR
	}
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Nlink == 1
}

func notificationBinding(n *Notifier) string {
	sum := sha256.Sum256([]byte(n.origin + "\x00" + hex.EncodeToString(n.secret)))
	return hex.EncodeToString(sum[:])
}

func validNotificationState(s notificationState, n *Notifier, notice Notification) bool {
	if s.Schema != notificationSchema || s.Source != n.source || s.Generation != n.generation || s.ID != notice.ID || s.MessageID != notice.MessageID || s.MailboxID != notice.MailboxID || s.Binding != notificationBinding(n) || s.Started <= 0 || s.Started > math.MaxInt64-notificationDelays[len(notificationDelays)-1] || s.Attempts < 0 || s.Attempts > len(notificationDelays) || s.HTTPStatus < 0 || s.HTTPStatus > 599 {
		return false
	}
	switch s.State {
	case "pending":
		return s.Attempts == 0 && s.NextAttempt == 0 && s.Check == "" && s.HTTPStatus == 0
	case "sending":
		return s.Attempts > 0 && s.Check == "" && s.HTTPStatus == 0 && (s.Attempts < len(notificationDelays) && s.NextAttempt == s.Started+notificationDelays[s.Attempts] || s.Attempts == len(notificationDelays) && s.NextAttempt == 0)
	case "failed":
		return s.Attempts > 0 && s.Attempts < len(notificationDelays) && s.NextAttempt == s.Started+notificationDelays[s.Attempts] && s.Check != ""
	case "notified":
		return s.Attempts > 0 && s.NextAttempt == 0 && s.Check == "" && (s.HTTPStatus == 200 || s.HTTPStatus == 202)
	case "exhausted":
		return s.Attempts == len(notificationDelays) && s.NextAttempt == 0 && s.Check != ""
	case "configuration-error":
		return s.Attempts > 0 && s.NextAttempt == 0 && s.Check != ""
	case "unknown":
		return s.Attempts == len(notificationDelays) && s.NextAttempt == 0 && s.Check == "" && s.HTTPStatus == 0
	default:
		return false
	}
}

func readNotificationState(dir int) (notificationState, bool, error) {
	fd, err := unix.Openat(dir, "notification.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return notificationState{}, false, nil
	}
	if err != nil {
		return notificationState{}, false, ErrNotificationState
	}
	f := os.NewFile(uintptr(fd), "notification.json")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return notificationState{}, false, ErrNotificationState
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(raw) == 0 || len(raw) > 4096 {
		return notificationState{}, false, ErrNotificationState
	}
	var value notificationState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&value) != nil || decoder.Decode(&extra) != io.EOF {
		return notificationState{}, false, ErrNotificationState
	}
	return value, true, nil
}

// writeNotificationState is called under the per-message lock. The rename and
// directory sync make a complete new version durable before network I/O.
func writeNotificationState(dir int, value notificationState) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 4096 {
		return ErrNotificationState
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return ErrNotificationState
	}
	name := ".notification-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrNotificationState
	}
	f := os.NewFile(uintptr(fd), name)
	defer unix.Unlinkat(dir, name, 0)
	if n, e := f.Write(raw); e != nil || n != len(raw) || f.Sync() != nil {
		f.Close()
		return ErrNotificationState
	}
	if f.Close() != nil {
		return ErrNotificationState
	}
	if unix.Renameat(dir, name, dir, "notification.json") != nil || unix.Fsync(dir) != nil {
		return ErrNotificationUnknown
	}
	return nil
}

// ProcessNotification performs at most one attempt for an already durably
// accepted message. The caller must use a dedicated private message directory
// and independently confirm the raw message and recipient before calling it.
// The notice never grants delivery authority and never deletes message data.
func ProcessNotification(ctx context.Context, directory string, n *Notifier, notice Notification, now time.Time) (NotificationProgress, error) {
	if !filepath.IsAbs(directory) || n == nil || n.client == nil || len(n.secret) != 32 || !identifier.MatchString(n.source) || !identifier.MatchString(n.generation) || !identifier.MatchString(notice.ID) || !identifier.MatchString(notice.MailboxID) || !messageID.MatchString(notice.MessageID) || now.Unix() <= 0 || now.Unix() > math.MaxInt64-notificationDelays[len(notificationDelays)-1] {
		return NotificationProgress{}, ErrConfiguration
	}
	dir, err := unix.Open(directory, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return NotificationProgress{}, ErrNotificationState
	}
	defer unix.Close(dir)
	if !secureNoticeFile(dir, true) {
		return NotificationProgress{}, ErrNotificationState
	}
	lock, err := unix.Openat(dir, ".notification.lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return NotificationProgress{}, ErrNotificationState
	}
	defer unix.Close(lock)
	if !secureNoticeFile(lock, false) {
		return NotificationProgress{}, ErrNotificationState
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return NotificationProgress{}, ErrNotificationBusy
		}
		return NotificationProgress{}, ErrNotificationState
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	state, exists, err := readNotificationState(dir)
	if err != nil {
		return NotificationProgress{}, err
	}
	if !exists {
		state = notificationState{Schema: notificationSchema, Source: n.source, Generation: n.generation, ID: notice.ID, MessageID: notice.MessageID, MailboxID: notice.MailboxID, Binding: notificationBinding(n), Started: now.Unix(), NotificationProgress: NotificationProgress{State: "pending"}}
		if err = writeNotificationState(dir, state); err != nil {
			return NotificationProgress{}, err
		}
	}
	if !validNotificationState(state, n, notice) {
		return NotificationProgress{}, ErrNotificationState
	}
	if state.State == "notified" || state.State == "exhausted" || state.State == "configuration-error" || state.State == "unknown" {
		return state.NotificationProgress, nil
	}
	if state.Attempts == len(notificationDelays) {
		state.State, state.NextAttempt = "unknown", 0
		if err = writeNotificationState(dir, state); err != nil {
			return NotificationProgress{}, err
		}
		return state.NotificationProgress, nil
	}
	due := state.Started + notificationDelays[state.Attempts]
	if now.Unix() < due {
		return state.NotificationProgress, nil
	}
	state.Attempts++
	state.State, state.Check, state.HTTPStatus = "sending", "", 0
	state.NextAttempt = 0
	if state.Attempts < len(notificationDelays) {
		state.NextAttempt = state.Started + notificationDelays[state.Attempts]
	}
	if err = writeNotificationState(dir, state); err != nil {
		return NotificationProgress{}, err
	}
	result := n.Send(ctx, notice, now)
	if result.State != "notified" && result.State != "failed" && result.State != "configuration-error" {
		return NotificationProgress{}, ErrNotificationUnknown
	}
	state.State, state.Check, state.HTTPStatus = result.State, result.Check, result.HTTPStatus
	if state.State == "failed" && state.Attempts == len(notificationDelays) {
		state.State = "exhausted"
	}
	if state.State != "failed" {
		state.NextAttempt = 0
	}
	if err = writeNotificationState(dir, state); err != nil {
		// The receiver may already have durably acknowledged this notice even
		// when the local result could not be recorded.
		return NotificationProgress{}, ErrNotificationUnknown
	}
	return state.NotificationProgress, nil
}
