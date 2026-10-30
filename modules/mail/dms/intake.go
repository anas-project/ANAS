package dms

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
)

const IntakeMaxBytes = 25 << 20

var intakeID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sourceMessageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var postfixQueueID = regexp.MustCompile(`^[A-Za-z0-9]{5,64}$`)

// IntakeMetadata is supplied by the trusted Go source consumer, never extracted
// from To/Cc/Received or other message headers. AcceptedAddress is historical:
// accepted mail keeps its original immutable box after an address transfer.
type IntakeMetadata struct {
	SourceID        string `json:"source_id"`
	MessageID       string `json:"message_id"`
	MailboxID       string `json:"mailbox_id"`
	OwnerAnchor     string `json:"owner_anchor"`
	AcceptedAddress string `json:"accepted_address"`
	ClaimVersion    string `json:"claim_version"`
	EnvelopeFrom    string `json:"envelope_from"`
	SHA256          string `json:"sha256"`
}
type IntakeReceipt struct {
	ID          string `json:"id"`
	MailboxID   string `json:"mailbox_id"`
	SHA256      string `json:"sha256"`
	State       string `json:"state"`
	Check       string `json:"check,omitempty"`
	SMTPCode    int    `json:"smtp_code,omitempty"`
	RawReleased bool   `json:"raw_released,omitempty"`
}
type intakeSubmission struct {
	State, QueueID, Check string
	SMTPCode              int
}
type intakeRecord struct {
	Schema           string         `json:"schema"`
	Metadata         IntakeMetadata `json:"metadata"`
	State            string         `json:"state"`
	DeliveryToken    string         `json:"-"`
	QueueID          string         `json:"queue_id,omitempty"`
	Check            string         `json:"check,omitempty"`
	SMTPCode         int            `json:"smtp_code,omitempty"`
	ReleaseRequested bool           `json:"release_requested,omitempty"`
	RawReleased      bool           `json:"raw_released,omitempty"`
}

// Delivery tokens are private local intent material, never part of a receipt.
func (intakeRecord) String() string   { return "[private intake record]" }
func (intakeRecord) GoString() string { return "[private intake record]" }

func newIntakeDeliveryToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(token[:]), nil
}

type intakeDeliveryIntent struct {
	Schema   string         `json:"schema"`
	Metadata IntakeMetadata `json:"metadata"`
	Token    string         `json:"token"`
}

func (intakeDeliveryIntent) String() string   { return "[private delivery intent]" }
func (intakeDeliveryIntent) GoString() string { return "[private delivery intent]" }

func saveIntakeDeliveryIntent(dir string, metadata IntakeMetadata, token string) error {
	if !metadata.valid() || !intakeID.MatchString(token) {
		return ErrUnavailable
	}
	raw, err := json.Marshal(intakeDeliveryIntent{Schema: "anas.mail-delivery-intent/v1", Metadata: metadata, Token: token})
	if err != nil {
		return ErrUnavailable
	}
	defer clear(raw)
	return atomicFile(filepath.Join(dir, "delivery-intent.json"), raw, 0600)
}

func loadIntakeDeliveryIntent(dir string, metadata IntakeMetadata) (string, error) {
	path := filepath.Join(dir, "delivery-intent.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil // Legacy records have no per-attempt intent.
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return "", ErrUnavailable
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ErrUnavailable
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var intent intakeDeliveryIntent
	var extra any
	if decoder.Decode(&intent) != nil || decoder.Decode(&extra) != io.EOF || intent.Schema != "anas.mail-delivery-intent/v1" || intent.Metadata != metadata || !intakeID.MatchString(intent.Token) {
		return "", ErrUnavailable
	}
	canonical, err := json.Marshal(intent)
	if err != nil || !bytes.Equal(raw, canonical) {
		clear(canonical)
		return "", ErrUnavailable
	}
	clear(canonical)
	return intent.Token, nil
}

func (m IntakeMetadata) Key() string {
	h := sha256.Sum256([]byte(m.SourceID + "\x00" + m.MessageID + "\x00" + m.MailboxID))
	return hex.EncodeToString(h[:])
}
func (m IntakeMetadata) Validate() error {
	if !m.valid() {
		return ErrConflict
	}
	return nil
}
func (m IntakeMetadata) valid() bool {
	if !uuid.MatchString(m.SourceID) || !sourceMessageID.MatchString(m.MessageID) || !uuid.MatchString(m.MailboxID) || !uuid.MatchString(m.OwnerAnchor) || !uuid.MatchString(m.ClaimVersion) || !intakeID.MatchString(m.SHA256) {
		return false
	}
	parts := strings.Split(m.AcceptedAddress, "@")
	if len(parts) != 2 {
		return false
	}
	address, err := directorymail.Address(parts[0], parts[1])
	if err != nil || address != m.AcceptedAddress {
		return false
	}
	if m.EnvelopeFrom != "" {
		from, err := mail.ParseAddress(m.EnvelopeFrom)
		if err != nil || from.Name != "" || from.Address != m.EnvelopeFrom || len(m.EnvelopeFrom) > 254 || strings.ContainsAny(m.EnvelopeFrom, "\x00\r\n") {
			return false
		}
	}
	return true
}
func intakeState(value string) bool {
	switch value {
	case "prepared", "submitting", "queued", "unknown", "retryable", "rejected", "stored", "processed":
		return true
	}
	return false
}
func intakeCheck(value string) bool {
	switch value {
	case "", "smtp-submission", "message-headers", "smtp-connect", "smtp-greeting", "smtp-hello", "smtp-sender", "smtp-recipient", "smtp-data", "smtp-body", "smtp-ack", "native-delivery":
		return true
	}
	return false
}
func intakeCode(code int) bool { return code == 0 || code >= 200 && code <= 599 }
func intakeResult(r intakeRecord) IntakeReceipt {
	return IntakeReceipt{ID: r.Metadata.Key(), MailboxID: r.Metadata.MailboxID, SHA256: r.Metadata.SHA256, State: r.State, Check: r.Check, SMTPCode: r.SMTPCode, RawReleased: r.RawReleased}
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func loadIntake(dir string) (intakeRecord, error) {
	var result intakeRecord
	info, err := os.Lstat(dir)
	if err != nil {
		return result, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, ErrUnavailable
	}
	path := filepath.Join(dir, "state.json")
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return result, ErrUnavailable
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&result) != nil || d.Decode(&extra) != io.EOF || result.Schema != "anas.mail-intake/v1" || !result.Metadata.valid() || !intakeState(result.State) || !intakeCheck(result.Check) || !intakeCode(result.SMTPCode) || result.QueueID != "" && !postfixQueueID.MatchString(result.QueueID) {
		return intakeRecord{}, ErrUnavailable
	}
	if filepath.Base(dir) != result.Metadata.Key() {
		return intakeRecord{}, ErrUnavailable
	}
	result.DeliveryToken, err = loadIntakeDeliveryIntent(dir, result.Metadata)
	if err != nil {
		return intakeRecord{}, err
	}
	if (result.ReleaseRequested || result.RawReleased) && result.State != "stored" && result.State != "processed" || result.RawReleased && !result.ReleaseRequested {
		return intakeRecord{}, ErrUnavailable
	}
	return result, nil
}

// ReleaseIntake removes only the redundant input original after both native
// delivery and the source's durable acknowledgement. The native mailbox and
// permanent receipt are untouched. A persisted intent makes an interrupted
// unlink safe to repeat; callers cannot supply a path or release queued mail.
func (a *Adapter) ReleaseIntake(ctx context.Context, expected IntakeReceipt, acknowledged bool) (IntakeReceipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Older versions could infer processed from a reused Postfix queue ID.
	// Do not release an original based on that historical receipt.
	if expected.State == "processed" {
		return IntakeReceipt{}, ErrUnavailable
	}
	if !acknowledged || !intakeID.MatchString(expected.ID) || !uuid.MatchString(expected.MailboxID) || !intakeID.MatchString(expected.SHA256) || expected.State != "stored" {
		return IntakeReceipt{}, ErrConflict
	}
	if ctx.Err() != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	dir := filepath.Join(a.StateDir, "intake", expected.ID)
	r, err := loadIntake(dir)
	if err != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	if r.Metadata.MailboxID != expected.MailboxID || r.Metadata.SHA256 != expected.SHA256 || r.State != expected.State {
		return IntakeReceipt{}, ErrConflict
	}
	if r.RawReleased {
		return intakeResult(r), nil
	}
	r.ReleaseRequested = true
	if saveIntake(dir, r) != nil {
		return intakeResult(r), ErrUnavailable
	}
	path := filepath.Join(dir, "raw.eml")
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return intakeResult(r), ErrUnavailable
	}
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return intakeResult(r), ErrUnavailable
		}
		if os.Remove(path) != nil {
			return intakeResult(r), ErrUnavailable
		}
	}
	if syncDirectory(dir) != nil {
		return intakeResult(r), ErrUnavailable
	}
	r.RawReleased = true
	if saveIntake(dir, r) != nil {
		return intakeResult(r), ErrUnavailable
	}
	return intakeResult(r), nil
}
func saveIntake(dir string, record intakeRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return ErrUnavailable
	}
	return atomicFile(filepath.Join(dir, "state.json"), raw, 0600)
}
func verifyIntakeBody(body io.Reader, expected string, destination io.Writer) error {
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(h, destination), io.LimitReader(body, IntakeMaxBytes+1))
	if err != nil || size < 1 || size > IntakeMaxBytes || hex.EncodeToString(h.Sum(nil)) != expected {
		return ErrConflict
	}
	return nil
}
func publishIntake(root string, m IntakeMetadata, body io.Reader) (intakeRecord, error) {
	result := intakeRecord{Schema: "anas.mail-intake/v1", Metadata: m, State: "prepared"}
	if err := os.MkdirAll(root, 0700); err != nil {
		return result, ErrUnavailable
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, ErrUnavailable
	}
	temporary, err := os.MkdirTemp(root, ".prepared-")
	if err != nil {
		return result, ErrUnavailable
	}
	defer os.RemoveAll(temporary)
	f, err := os.OpenFile(filepath.Join(temporary, "raw.eml"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, ErrUnavailable
	}
	err = verifyIntakeBody(body, m.SHA256, f)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, ErrUnavailable
	}
	if err = saveIntake(temporary, result); err != nil {
		return result, ErrUnavailable
	}
	if err = os.Rename(temporary, filepath.Join(root, m.Key())); err != nil {
		return result, ErrUnavailable
	}
	if err = syncDirectory(root); err != nil {
		return result, ErrUnavailable
	}
	return result, nil
}

func (a *Adapter) intakeBox(ctx context.Context, m IntakeMetadata) (string, error) {
	state, err := a.load()
	if err != nil {
		return "", err
	}
	box, ok := state.Boxes[m.MailboxID]
	if !ok || !box.Applied || box.Projection.Mailbox.OwnerAnchor != m.OwnerAnchor {
		return "", ErrConflict
	}
	probe := a.probe
	if probe == nil {
		probe = a.liveProbe
	}
	if err = probe(ctx, map[string]entry{m.MailboxID: box}); err != nil {
		return "", ErrUnavailable
	}
	return box.Projection.Mailbox.ProtocolLogin + "@" + ProtocolRealm, nil
}

// Intake publishes original bytes and trusted metadata together before any
// native SMTP action. A submitting/unknown operation is only reconciled; it is
// never blindly submitted again. The existing Postfix queue remains the queue.
func (a *Adapter) Intake(ctx context.Context, id string, m IntakeMetadata, body io.Reader) (IntakeReceipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !m.valid() || id != m.Key() || body == nil {
		return IntakeReceipt{}, ErrConflict
	}
	user, err := a.intakeBox(ctx, m)
	if err != nil {
		return IntakeReceipt{}, err
	}
	root := filepath.Join(a.StateDir, "intake")
	dir := filepath.Join(root, id)
	record, err := loadIntake(dir)
	if errors.Is(err, os.ErrNotExist) {
		record, err = publishIntake(root, m, body)
	} else if err == nil {
		if record.Metadata != m {
			return IntakeReceipt{}, ErrConflict
		}
		err = verifyIntakeBody(body, m.SHA256, io.Discard)
	}
	if err != nil {
		return IntakeReceipt{}, err
	}
	if record.State == "prepared" || record.State == "retryable" {
		path := filepath.Join(dir, "raw.eml")
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > IntakeMaxBytes {
			return intakeResult(record), ErrUnavailable
		}
		f, err := os.Open(path)
		if err != nil {
			return intakeResult(record), ErrUnavailable
		}
		defer f.Close()
		if verifyIntakeBody(f, m.SHA256, io.Discard) != nil {
			return intakeResult(record), ErrUnavailable
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return intakeResult(record), ErrUnavailable
		}
		// Commit a fresh, unpredictable identity before this submission can
		// reach Postfix. Only an explicitly retryable attempt may get a new one;
		// queued or unknown work never does. The listener is not connected yet.
		record.DeliveryToken, err = newIntakeDeliveryToken()
		if err != nil {
			return intakeResult(record), err
		}
		if err = saveIntakeDeliveryIntent(dir, record.Metadata, record.DeliveryToken); err != nil {
			return intakeResult(record), err
		}
		record.State = "submitting"
		record.Check, record.SMTPCode = "smtp-submission", 0
		if saveIntake(dir, record) != nil {
			return intakeResult(record), ErrUnavailable
		}
		submit := a.intakeSubmit
		if submit == nil {
			submit = nativeIntakeSubmit
		}
		result := submit(ctx, record, user, f)
		record.State, record.QueueID, record.Check, record.SMTPCode = result.State, result.QueueID, result.Check, result.SMTPCode
		if !intakeState(record.State) || !intakeCheck(record.Check) || !intakeCode(record.SMTPCode) || record.QueueID != "" && !postfixQueueID.MatchString(record.QueueID) {
			return IntakeReceipt{}, ErrUnavailable
		}
		if saveIntake(dir, record) != nil {
			return intakeResult(record), ErrUnavailable
		}
	}
	return a.observeIntake(ctx, dir, record, user)
}
func (a *Adapter) IntakeStatus(ctx context.Context, id string) (IntakeReceipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !intakeID.MatchString(id) {
		return IntakeReceipt{}, ErrConflict
	}
	dir := filepath.Join(a.StateDir, "intake", id)
	record, err := loadIntake(dir)
	if err != nil {
		return IntakeReceipt{}, ErrUnavailable
	}
	user, err := a.intakeBox(ctx, record.Metadata)
	if err != nil {
		return intakeResult(record), err
	}
	return a.observeIntake(ctx, dir, record, user)
}
func (a *Adapter) observeIntake(ctx context.Context, dir string, record intakeRecord, user string) (IntakeReceipt, error) {
	if record.State == "processed" {
		// Preserve the old record and its release state for recovery. It is not
		// evidence that this particular message was delivered or discarded.
		result := intakeResult(record)
		result.State = "unknown"
		return result, ErrUnavailable
	}
	if record.State == "stored" {
		return intakeResult(record), nil
	}
	if record.State == "rejected" && record.Check == "native-delivery" {
		// An older observer could also infer rejection from an unrelated log.
		// Reconcile only; do not resubmit this uncertain message.
		record.State = "unknown"
	}
	if record.State == "submitting" || record.State == "unknown" || record.State == "queued" {
		observe := a.intakeObserve
		if observe == nil {
			observe = nativeIntakeObserve
		}
		record.State, record.QueueID = observe(ctx, record, user)
		if record.State == "processed" {
			record.State = "unknown"
		}
		if record.State == "stored" {
			record.Check = ""
			record.SMTPCode = 0
		}
		if !intakeState(record.State) || record.QueueID != "" && !postfixQueueID.MatchString(record.QueueID) {
			return IntakeReceipt{}, ErrUnavailable
		}
		if saveIntake(dir, record) != nil {
			return intakeResult(record), ErrUnavailable
		}
		if record.State == "stored" {
			return intakeResult(record), nil
		}
	}
	return intakeResult(record), ErrUnavailable
}
