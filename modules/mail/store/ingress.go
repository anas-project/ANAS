package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

var ErrIngressChanged = errors.New("mail source binding or delivery changed")
var ErrIngressIdle = errors.New("no due mail source delivery")
var ingressDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ingressMessageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var ingressETag = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// IngressBinding can be confirmed only after the caller has read back the
// matching cloud permission and revalidated the directory. It grants receiving
// only, never login/sending. Closed bindings cannot be reopened or repointed.
type IngressBinding struct {
	SourceID, Generation, Address, ClaimVersion, MailboxID, OwnerAnchor string
	FirstSequence                                                       int64
}

func ingressHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (b IngressBinding) Key() string {
	return ingressHash(strings.Join([]string{b.SourceID, b.Generation, b.Address, b.ClaimVersion, b.MailboxID, b.OwnerAnchor}, "\x00"))
}
func (b IngressBinding) valid() bool {
	address, err := primaryAddress(b.Address)
	return err == nil && address == b.Address && anchorPattern.MatchString(b.SourceID) && anchorPattern.MatchString(b.Generation) && anchorPattern.MatchString(b.ClaimVersion) && anchorPattern.MatchString(b.MailboxID) && anchorPattern.MatchString(b.OwnerAnchor) && b.FirstSequence >= 1 && b.FirstSequence <= 9007199254740991
}

func (s Store) ConfirmIngressBinding(ctx context.Context, b IngressBinding) error {
	if !b.valid() {
		return ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Use the projection lock shared by address reconfiguration. A disabled
	// person/box still receives; an incomplete or unapplied projection cannot
	// create a new cloud acceptance permission.
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM mail.mailbox WHERE id=$1 FOR SHARE`, b.MailboxID).Scan(&id); err != nil {
		return ingressError(err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT mailbox_id FROM mail.mailbox_projection WHERE mailbox_id=$1 FOR SHARE`, b.MailboxID).Scan(&id); err != nil {
		return ingressError(err)
	}
	p, err := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1`, b.MailboxID))
	if err != nil {
		return ingressError(err)
	}
	if !p.Applied || p.Mailbox.OwnerAnchor != b.OwnerAnchor || p.Resource.State == "pending" || p.Mailbox.State == "pending" {
		return ErrIngressChanged
	}
	found := false
	for _, a := range p.Resource.Addresses {
		if a.Address == b.Address && a.Version == b.ClaimVersion && a.State == "active" && a.OperationState == "complete" {
			found = true
		}
	}
	if !found {
		return ErrIngressChanged
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO mail.ingress_binding(id,source_id,generation,address,claim_version,mailbox_id,owner_anchor,first_sequence)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO UPDATE SET id=EXCLUDED.id
 WHERE mail.ingress_binding.closed_sequence IS NULL AND mail.ingress_binding.first_sequence=EXCLUDED.first_sequence`, b.Key(), b.SourceID, b.Generation, b.Address, b.ClaimVersion, b.MailboxID, b.OwnerAnchor, b.FirstSequence)
	if err != nil {
		return ingressError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return tx.Commit()
}

// CloseIngressBinding records the cloud's confirmed final accepted sequence.
// A late callback can repeat the exact boundary, never widen it. The caller
// must close/read back the cloud permission before opening a replacement.
func (s Store) CloseIngressBinding(ctx context.Context, b IngressBinding, through int64) error {
	if !b.valid() || through < b.FirstSequence-1 || through > 9007199254740991 {
		return ErrIngressChanged
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE mail.ingress_binding SET closed_sequence=$2 WHERE id=$1 AND first_sequence=$3 AND (closed_sequence IS NULL OR closed_sequence=$2)`, b.Key(), through, b.FirstSequence)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

// IngressObservation contains immutable facts from the source envelope. Body
// hashes and cloud delivery marks are deliberately excluded: the consumer
// computes its own hash and independently confirms the native DMS result.
type IngressObservation struct {
	SourceID        string    `json:"source_id"`
	Generation      string    `json:"generation"`
	MessageID       string    `json:"message_id"`
	MailboxID       string    `json:"mailbox_id"`
	OwnerAnchor     string    `json:"owner_anchor"`
	AcceptedAddress string    `json:"accepted_address"`
	ClaimVersion    string    `json:"claim_version"`
	EnvelopeFrom    string    `json:"envelope_from"`
	Sequence        int64     `json:"sequence"`
	Bytes           int64     `json:"bytes"`
	ETag            string    `json:"etag"`
	ReceivedAt      time.Time `json:"received_at"`
	ExpectedSHA256  string    `json:"-"`
}

func (o IngressObservation) Key() string {
	return ingressHash(o.SourceID + "\x00" + o.MessageID + "\x00" + o.MailboxID)
}
func (o IngressObservation) binding() IngressBinding {
	return IngressBinding{SourceID: o.SourceID, Generation: o.Generation, Address: o.AcceptedAddress, ClaimVersion: o.ClaimVersion, MailboxID: o.MailboxID, OwnerAnchor: o.OwnerAnchor, FirstSequence: 1}
}
func (o IngressObservation) valid() bool {
	if !o.binding().valid() || !ingressMessageID.MatchString(o.MessageID) || o.Sequence < 1 || o.Sequence > 9007199254740991 || o.Bytes < 1 || o.Bytes > 25<<20 || !ingressETag.MatchString(o.ETag) || o.ReceivedAt.IsZero() || o.ReceivedAt.After(time.Now().Add(5*time.Minute)) {
		return false
	}
	if o.ExpectedSHA256 != "" && !ingressDigest.MatchString(o.ExpectedSHA256) {
		return false
	}
	if o.EnvelopeFrom != "" {
		a, e := mail.ParseAddress(o.EnvelopeFrom)
		if e != nil || a.Name != "" || a.Address != o.EnvelopeFrom || len(o.EnvelopeFrom) > 254 || strings.ContainsAny(o.EnvelopeFrom, "\x00\r\n") {
			return false
		}
	}
	return true
}

type IngressTask struct {
	ID, Lease, SHA256, Phase, NativeState, Error string
	Observation                                  IngressObservation
	Attempts                                     int
}

const ingressSelect = `SELECT id,COALESCE(lease_token::text,''),COALESCE(sha256,''),phase,native_state,last_error,observation,attempts,COALESCE(expected_sha256,'') FROM mail.ingress_message`

func scanIngress(row projectionScanner) (IngressTask, error) {
	var t IngressTask
	var raw []byte
	var expected string
	err := row.Scan(&t.ID, &t.Lease, &t.SHA256, &t.Phase, &t.NativeState, &t.Error, &raw, &t.Attempts, &expected)
	if err != nil {
		return t, err
	}
	if json.Unmarshal(raw, &t.Observation) != nil || !t.Observation.valid() || t.Observation.Key() != t.ID {
		return IngressTask{}, ErrIngressChanged
	}
	t.Observation.ExpectedSHA256 = expected
	return t, nil
}
func ingressError(err error) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(projectionError(err), ErrProjectionConflict) || errors.Is(projectionError(err), ErrAddressUnavailable) {
		return ErrIngressChanged
	}
	return projectionError(err)
}
func (s Store) StageIngress(ctx context.Context, o IngressObservation) (IngressTask, error) {
	if !o.valid() {
		return IngressTask{}, ErrIngressChanged
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return IngressTask{}, ErrIngressChanged
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return IngressTask{}, err
	}
	defer tx.Rollback()
	// Binding closure and acceptance share this lock. Previously recorded work
	// remains resumable after a later closure, including while AD is offline.
	var first int64
	var last sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT first_sequence,closed_sequence FROM mail.ingress_binding WHERE id=$1 FOR SHARE`, o.binding().Key()).Scan(&first, &last)
	if err != nil {
		return IngressTask{}, ingressError(err)
	}
	if o.Sequence < first || last.Valid && o.Sequence > last.Int64 {
		return IngressTask{}, ErrIngressChanged
	}
	if !last.Valid {
		// A directory withdrawal must not wait for an unreachable cloud control
		// API before blocking new local work. Existing tasks remain resumable;
		// once cloud closure is confirmed, its sequence proves old acceptance.
		var recorded bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM mail.ingress_message WHERE id=$1)`, o.Key()).Scan(&recorded); err != nil {
			return IngressTask{}, err
		}
		if !recorded {
			p, e := scanProjection(tx.QueryRowContext(ctx, projectionSelect+` WHERE p.mailbox_id=$1 FOR SHARE OF p`, o.MailboxID))
			if e != nil {
				return IngressTask{}, ingressError(e)
			}
			valid := false
			for _, a := range p.Resource.Addresses {
				if a.Address == o.AcceptedAddress && a.Version == o.ClaimVersion && a.State == "active" && a.OperationState == "complete" {
					valid = true
				}
			}
			if !valid || p.Mailbox.OwnerAnchor != o.OwnerAnchor || p.Error == "directory-resource-missing" {
				return IngressTask{}, ErrIngressChanged
			}
		}
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO mail.ingress_message(id,binding_id,source_id,generation,message_id,mailbox_id,observation,expected_sha256)
 VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')) ON CONFLICT(id) DO UPDATE SET expected_sha256=COALESCE(mail.ingress_message.expected_sha256,EXCLUDED.expected_sha256)
 WHERE mail.ingress_message.observation=EXCLUDED.observation
 AND (EXCLUDED.expected_sha256 IS NULL OR mail.ingress_message.expected_sha256 IS NULL OR mail.ingress_message.expected_sha256=EXCLUDED.expected_sha256)
 AND (EXCLUDED.expected_sha256 IS NULL OR mail.ingress_message.sha256 IS NULL OR mail.ingress_message.sha256=EXCLUDED.expected_sha256)`, o.Key(), o.binding().Key(), o.SourceID, o.Generation, o.MessageID, o.MailboxID, raw, o.ExpectedSHA256)
	if err != nil {
		return IngressTask{}, ingressError(err)
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return IngressTask{}, ErrIngressChanged
	}
	t, err := scanIngress(tx.QueryRowContext(ctx, ingressSelect+` WHERE id=$1`, o.Key()))
	if err != nil {
		return IngressTask{}, err
	}
	return t, tx.Commit()
}
func (s Store) IngressTask(ctx context.Context, id string) (IngressTask, error) {
	if !ingressDigest.MatchString(id) {
		return IngressTask{}, ErrIngressChanged
	}
	t, e := scanIngress(s.DB.QueryRowContext(ctx, ingressSelect+` WHERE id=$1`, id))
	return t, ingressError(e)
}
func (s Store) ClaimIngress(ctx context.Context, source string) (IngressTask, error) {
	if !anchorPattern.MatchString(source) {
		return IngressTask{}, ErrIngressChanged
	}
	lease, err := mailboxID()
	if err != nil {
		return IngressTask{}, err
	}
	t, err := scanIngress(s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT id FROM mail.ingress_message WHERE source_id=$1 AND phase<>'complete' AND next_attempt<=clock_timestamp()
 AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY next_attempt,id FOR UPDATE SKIP LOCKED LIMIT 1),
 claimed AS (UPDATE mail.ingress_message t SET attempts=attempts+1,lease_until=clock_timestamp()+interval '2 minutes',lease_token=$2
 FROM candidate WHERE t.id=candidate.id RETURNING t.*)
 SELECT id,lease_token::text,COALESCE(sha256,''),phase,native_state,last_error,observation,attempts,COALESCE(expected_sha256,'') FROM claimed`, source, lease))
	if errors.Is(err, sql.ErrNoRows) {
		return IngressTask{}, ErrIngressIdle
	}
	return t, ingressError(err)
}

// AdvanceIngress changes one phase with an unexpired lease. The native result
// must precede cloud acknowledgement; queue acceptance cannot finish a task.
func (s Store) AdvanceIngress(ctx context.Context, t IngressTask, phase, digest, native string) (IngressTask, error) {
	valid := t.Phase == "download" && phase == "native" && ingressDigest.MatchString(digest) && native == "" ||
		t.Phase == "native" && phase == "native" && digest == t.SHA256 && ingressNativeState(native) ||
		t.Phase == "native" && phase == "ack" && digest == t.SHA256 && (native == "stored" || native == "processed") ||
		t.Phase == "ack" && phase == "ack" && digest == t.SHA256 && (native == "stored" || native == "processed") ||
		t.Phase == "ack" && phase == "cleanup" && digest == t.SHA256 && native == t.NativeState ||
		t.Phase == "cleanup" && phase == "complete" && digest == t.SHA256 && native == t.NativeState
	if !valid || !ingressDigest.MatchString(t.ID) || !anchorPattern.MatchString(t.Lease) {
		return IngressTask{}, ErrIngressChanged
	}
	row := s.DB.QueryRowContext(ctx, `UPDATE mail.ingress_message SET phase=$4,sha256=$5,native_state=$6,last_error='',updated_at=clock_timestamp(),
 acknowledged_at=CASE WHEN $4='cleanup' THEN clock_timestamp() ELSE acknowledged_at END,
 completed_at=CASE WHEN $4='complete' THEN clock_timestamp() ELSE completed_at END
 WHERE id=$1 AND lease_token=$2 AND phase=$3 AND lease_until>clock_timestamp() AND (expected_sha256 IS NULL OR expected_sha256=$5)
 RETURNING id,lease_token::text,sha256,phase,native_state,last_error,observation,attempts,COALESCE(expected_sha256,'')`, t.ID, t.Lease, t.Phase, phase, digest, native)
	out, err := scanIngress(row)
	return out, ingressError(err)
}
func ingressNativeState(s string) bool {
	switch s {
	case "prepared", "submitting", "queued", "unknown", "retryable", "rejected", "stored", "processed":
		return true
	}
	return false
}
func (s Store) ReleaseIngress(ctx context.Context, t IngressTask, code string) error {
	if !ingressDigest.MatchString(t.ID) || !anchorPattern.MatchString(t.Lease) || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrIngressChanged
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE mail.ingress_message SET lease_token=NULL,lease_until=NULL,last_error=$3,updated_at=clock_timestamp(),
 next_attempt=clock_timestamp()+make_interval(secs=>LEAST(300,5*attempts))
 WHERE id=$1 AND lease_token=$2 AND lease_until>clock_timestamp()`, t.ID, t.Lease, code)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}

type IngressPoll struct {
	SourceID, Generation, Cursor, Lease string
	RouteRevision                       int64
}

func (s Store) ClaimIngressPoll(ctx context.Context, source, generation string) (IngressPoll, error) {
	if !anchorPattern.MatchString(source) || !anchorPattern.MatchString(generation) {
		return IngressPoll{}, ErrIngressChanged
	}
	lease, err := mailboxID()
	if err != nil {
		return IngressPoll{}, err
	}
	var p IngressPoll
	err = s.DB.QueryRowContext(ctx, `INSERT INTO mail.ingress_poll(source_id,generation,lease_token,lease_until) VALUES($1,$2,$3,clock_timestamp()+interval '2 minutes')
 ON CONFLICT(source_id) DO UPDATE SET lease_token=EXCLUDED.lease_token,lease_until=EXCLUDED.lease_until
 WHERE mail.ingress_poll.generation=EXCLUDED.generation AND (mail.ingress_poll.lease_until IS NULL OR mail.ingress_poll.lease_until<=clock_timestamp())
 RETURNING source_id,generation,cursor,lease_token,route_revision`, source, generation, lease).Scan(&p.SourceID, &p.Generation, &p.Cursor, &p.Lease, &p.RouteRevision)
	return p, ingressError(err)
}
func (s Store) AdvanceIngressPoll(ctx context.Context, p IngressPoll, cursor string, revision int64) (IngressPoll, error) {
	if !anchorPattern.MatchString(p.SourceID) || !anchorPattern.MatchString(p.Generation) || !anchorPattern.MatchString(p.Lease) || len(cursor) > 2048 || strings.ContainsAny(cursor, "\x00\r\n") || revision < p.RouteRevision || revision > 9007199254740991 {
		return IngressPoll{}, ErrIngressChanged
	}
	var out IngressPoll
	err := s.DB.QueryRowContext(ctx, `UPDATE mail.ingress_poll SET cursor=$5,route_revision=$6,updated_at=clock_timestamp()
 WHERE source_id=$1 AND generation=$2 AND lease_token=$3 AND route_revision=$4 AND lease_until>clock_timestamp()
 RETURNING source_id,generation,cursor,lease_token,route_revision`, p.SourceID, p.Generation, p.Lease, p.RouteRevision, cursor, revision).Scan(&out.SourceID, &out.Generation, &out.Cursor, &out.Lease, &out.RouteRevision)
	return out, ingressError(err)
}
func (s Store) ReleaseIngressPoll(ctx context.Context, p IngressPoll, code string) error {
	if !anchorPattern.MatchString(p.SourceID) || !anchorPattern.MatchString(p.Generation) || !anchorPattern.MatchString(p.Lease) || len(code) > 64 || strings.Trim(code, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ErrIngressChanged
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE mail.ingress_poll SET lease_token=NULL,lease_until=NULL,last_error=$4,updated_at=clock_timestamp(),
 last_success=CASE WHEN $4='' THEN clock_timestamp() ELSE last_success END WHERE source_id=$1 AND generation=$2 AND lease_token=$3 AND lease_until>clock_timestamp()`, p.SourceID, p.Generation, p.Lease, code)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrIngressChanged
	}
	return nil
}
