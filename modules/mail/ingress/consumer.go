package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type Ledger interface {
	StageIngress(context.Context, store.IngressObservation) (store.IngressTask, error)
	ClaimIngress(context.Context, string) (store.IngressTask, error)
	AdvanceIngress(context.Context, store.IngressTask, string, string, string) (store.IngressTask, error)
	ReleaseIngress(context.Context, store.IngressTask, string) error
	ClaimIngressPoll(context.Context, string, string) (store.IngressPoll, error)
	AdvanceIngressPoll(context.Context, store.IngressPoll, string, int64) (store.IngressPoll, error)
	ReleaseIngressPoll(context.Context, store.IngressPoll, string) error
	IngressTask(context.Context, string) (store.IngressTask, error)
	ClaimIngressNotice(context.Context, string) (store.IngressNoticeTask, error)
	FinishIngressNotice(context.Context, store.IngressNoticeTask, string, string) error
}
type Source interface {
	List(context.Context, string) (SourcePage, error)
	Download(context.Context, SourceMessage, io.Writer) (string, error)
	Acknowledge(context.Context, SourceMessage, dms.IntakeReceipt) error
	Message(context.Context, string, string) (SourceMessage, error)
}

// Notices resolves only authenticated source metadata. A notification records
// a wake hint, never a recipient permission or final native delivery result.
func (c *Consumer) Notices(ctx context.Context) error {
	if !c.valid() {
		return ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var first error
	for count := 0; count < 20 && ctx.Err() == nil; count++ {
		t, err := c.Ledger.ClaimIngressNotice(ctx, c.SourceID)
		if errors.Is(err, store.ErrIngressIdle) {
			return first
		}
		if err != nil {
			return err
		}
		state, code := "pending", "source-unavailable"
		if t.SourceID != c.SourceID || t.Generation != c.Generation {
			err = ErrIdentity
			code = "source-identity-changed"
		} else {
			key := dms.IntakeMetadata{SourceID: t.SourceID, MessageID: t.MessageID, MailboxID: t.MailboxID}.Key()
			previous, readErr := c.Ledger.IngressTask(ctx, key)
			if readErr == nil && previous.Phase == "complete" && previous.Observation.Generation == c.Generation {
				state, code, err = "accepted", "", nil
			} else {
				var message SourceMessage
				message, err = c.Source.Message(ctx, t.MessageID, t.MailboxID)
				if err == nil {
					_, err = c.Ledger.StageIngress(ctx, observation(message, c.Generation))
				}
				if err == nil {
					state, code = "accepted", ""
				} else if errors.Is(err, ErrGone) {
					state, code = "expired", "source-copy-expired"
				} else {
					code = sourceErrorCode(err)
				}
			}
		}
		finish, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		finished := c.Ledger.FinishIngressNotice(finish, t, state, code)
		done()
		if finished != nil {
			return finished
		}
		if err != nil && first == nil {
			first = err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return first
}

type Native interface {
	Intake(context.Context, dms.IntakeMetadata, io.Reader) (dms.IntakeReceipt, error)
	IntakeStatus(context.Context, string) (dms.IntakeReceipt, error)
	ReleaseIntake(context.Context, dms.IntakeReceipt) (dms.IntakeReceipt, error)
}

// Consumer owns only its fixed private staging directory. The source must
// implement the verified protocol; confirmed receiving bindings live in PG.
// It never grants login, invokes Docker, infers header recipients or deletes
// undelivered source mail. One call is bounded below its database lease.
type Consumer struct {
	Ledger                          Ledger
	Source                          Source
	Native                          Native
	Directory, SourceID, Generation string
}

var ErrStaging = errors.New("mail ingress local staging unavailable")

func observation(m SourceMessage, generation string) store.IngressObservation {
	v := m.Metadata
	digest := v.SHA256
	if m.Delivery != nil {
		digest = m.Delivery.SHA256
	}
	return store.IngressObservation{SourceID: v.SourceID, Generation: generation, MessageID: v.MessageID, MailboxID: v.MailboxID, OwnerAnchor: v.OwnerAnchor, AcceptedAddress: v.AcceptedAddress, ClaimVersion: v.ClaimVersion, EnvelopeFrom: v.EnvelopeFrom, Sequence: m.Sequence, Bytes: m.Bytes, ETag: m.ETag, ReceivedAt: m.ReceivedAt.UTC(), ExpectedSHA256: digest}
}
func taskMessage(t store.IngressTask) SourceMessage {
	o := t.Observation
	digest := t.SHA256
	if digest == "" {
		digest = o.ExpectedSHA256
	}
	return SourceMessage{Metadata: dms.IntakeMetadata{SourceID: o.SourceID, MessageID: o.MessageID, MailboxID: o.MailboxID, OwnerAnchor: o.OwnerAnchor, AcceptedAddress: o.AcceptedAddress, ClaimVersion: o.ClaimVersion, EnvelopeFrom: o.EnvelopeFrom, SHA256: digest}, Sequence: o.Sequence, Bytes: o.Bytes, ETag: o.ETag, ReceivedAt: o.ReceivedAt}
}
func sourceErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAuthentication):
		return "source-authentication"
	case errors.Is(err, ErrGone):
		return "source-copy-expired"
	case errors.Is(err, ErrIntegrity):
		return "source-content-changed"
	case errors.Is(err, ErrStaging):
		return "local-staging-unavailable"
	case errors.Is(err, ErrIdentity):
		return "source-identity-changed"
	case errors.Is(err, store.ErrIngressChanged):
		return "source-binding-unconfirmed"
	default:
		return "source-unavailable"
	}
}
func (c *Consumer) valid() bool {
	return c.Ledger != nil && c.Source != nil && c.Native != nil && idPattern.MatchString(c.SourceID) && idPattern.MatchString(c.Generation) && filepath.IsAbs(c.Directory) && filepath.Clean(c.Directory) == c.Directory && c.Directory != "/"
}

// Poll persists each complete page before advancing its cursor. At the tail,
// retain that page's starting cursor so later polls see newly appended mail
// without repeatedly scanning all permanent tombstones. A consistent old NAS
// backup retains an earlier cursor and its staged tasks; delivered source
// flags never skip native reconciliation for items restored from that point.
func (c *Consumer) Poll(ctx context.Context) (result error) {
	if !c.valid() {
		return ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	p, err := c.Ledger.ClaimIngressPoll(ctx, c.SourceID, c.Generation)
	if err != nil {
		return err
	}
	code := "source-unavailable"
	defer func() {
		release, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer done()
		if e := c.Ledger.ReleaseIngressPoll(release, p, code); result == nil && e != nil {
			result = e
		}
	}()
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		page, e := c.Source.List(ctx, p.Cursor)
		if e != nil {
			code = sourceErrorCode(e)
			return e
		}
		if page.SourceID != c.SourceID || page.Generation != c.Generation || page.RouteRevision < p.RouteRevision {
			code = "source-identity-changed"
			return ErrIdentity
		}
		for _, message := range page.Messages {
			if _, e = c.Ledger.StageIngress(ctx, observation(message, c.Generation)); e != nil {
				code = sourceErrorCode(e)
				return e
			}
		}
		next := p.Cursor
		if page.Cursor != nil {
			next = *page.Cursor
		}
		updated, e := c.Ledger.AdvanceIngressPoll(ctx, p, next, page.RouteRevision)
		if e != nil {
			code = sourceErrorCode(e)
			return e
		}
		p = updated
		if page.Cursor == nil {
			code = ""
			return nil
		}
	}
	code = ""
	return nil
}
func privateDirectory(path string) error {
	if os.MkdirAll(path, 0700) != nil {
		return ErrStaging
	}
	i, e := os.Lstat(path)
	if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0077 != 0 {
		return ErrStaging
	}
	return nil
}
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func verifyFile(path string, size int64, digest string) (*os.File, error) {
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() != size {
		return nil, ErrIntegrity
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, ErrUnavailable
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, size+1))
	if e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		f.Close()
		return nil, ErrIntegrity
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

// stageRaw publishes with a hard link so an expired worker cannot overwrite a
// newer worker's immutable copy. Only a validated complete file is published.
func (c *Consumer) stageRaw(ctx context.Context, t store.IngressTask) (string, error) {
	if privateDirectory(c.Directory) != nil {
		return "", ErrStaging
	}
	path := filepath.Join(c.Directory, t.ID+".eml")
	if t.SHA256 != "" {
		if f, e := verifyFile(path, t.Observation.Bytes, t.SHA256); e == nil {
			f.Close()
			return t.SHA256, nil
		}
	}
	f, e := os.CreateTemp(c.Directory, ".download-")
	if e != nil {
		return "", ErrStaging
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	defer f.Close()
	digest, e := c.Source.Download(ctx, taskMessage(t), f)
	if e != nil {
		return "", e
	}
	if !secretPattern.MatchString(digest) || t.SHA256 != "" && digest != t.SHA256 {
		return "", ErrIntegrity
	}
	if e = f.Sync(); e != nil {
		return "", ErrStaging
	}
	if e = f.Close(); e != nil {
		return "", ErrStaging
	}
	if e = os.Link(temporary, path); e != nil {
		other, verify := verifyFile(path, t.Observation.Bytes, digest)
		if verify != nil {
			return "", ErrStaging
		}
		other.Close()
	}
	if syncDir(c.Directory) != nil {
		return "", ErrStaging
	}
	return digest, nil
}
func receiptMatches(t store.IngressTask, r dms.IntakeReceipt) bool {
	return r.ID == t.ID && r.MailboxID == t.Observation.MailboxID && r.SHA256 == t.SHA256
}
func (c *Consumer) nativeResult(ctx context.Context, t store.IngressTask) (dms.IntakeReceipt, error) {
	r, e := c.Native.IntakeStatus(ctx, t.ID)
	if r.ID != "" && !receiptMatches(t, r) {
		return dms.IntakeReceipt{}, ErrIdentity
	}
	// An ambiguous/queued submission is reconciled only. POST is reserved for
	// a missing, prepared or explicitly retryable operation; DMS also enforces
	// the persisted submission intent if an earlier status reply was lost.
	if r.ID == "" || r.State == "prepared" || r.State == "retryable" {
		if _, err := c.stageRaw(ctx, t); err != nil {
			return r, err
		}
		f, err := verifyFile(filepath.Join(c.Directory, t.ID+".eml"), t.Observation.Bytes, t.SHA256)
		if err != nil {
			return r, err
		}
		r, e = c.Native.Intake(ctx, taskMessage(t).Metadata, f)
		f.Close()
	}
	if !receiptMatches(t, r) {
		return dms.IntakeReceipt{}, ErrUnavailable
	}
	return r, e
}
func (c *Consumer) runTask(ctx context.Context, t store.IngressTask) (store.IngressTask, string, error) {
	var err error
	if t.Phase == "download" {
		digest, e := c.stageRaw(ctx, t)
		if e != nil {
			return t, sourceErrorCode(e), e
		}
		t, err = c.Ledger.AdvanceIngress(ctx, t, "native", digest, "")
		if err != nil {
			return t, "ledger-unavailable", err
		}
	}
	if t.Phase == "native" {
		r, e := c.nativeResult(ctx, t)
		if !receiptMatches(t, r) {
			return t, "native-unconfirmed", ErrUnavailable
		}
		if e != nil || r.State != "stored" && r.State != "processed" {
			if _, err = c.Ledger.AdvanceIngress(ctx, t, "native", t.SHA256, r.State); err != nil {
				return t, "ledger-unavailable", err
			}
			return t, "native-" + r.State, ErrUnavailable
		}
		t, err = c.Ledger.AdvanceIngress(ctx, t, "ack", t.SHA256, r.State)
		if err != nil {
			return t, "ledger-unavailable", err
		}
	}
	r := dms.IntakeReceipt{ID: t.ID, MailboxID: t.Observation.MailboxID, SHA256: t.SHA256, State: t.NativeState}
	if t.Phase == "ack" {
		// A restored NAS ledger must recheck the current native receipt before
		// confirming cloud delivery. If that native snapshot predates the mail,
		// the retained source copy is imported through the same deterministic key.
		confirmed, e := c.nativeResult(ctx, t)
		if e != nil || !receiptMatches(t, confirmed) || confirmed.State != "stored" && confirmed.State != "processed" {
			return t, "native-unconfirmed", ErrUnavailable
		}
		if confirmed.State != t.NativeState {
			t, err = c.Ledger.AdvanceIngress(ctx, t, "ack", t.SHA256, confirmed.State)
			if err != nil {
				return t, "ledger-unavailable", err
			}
			r.State = t.NativeState
		}
		if err = c.Source.Acknowledge(ctx, taskMessage(t), r); err != nil {
			return t, sourceErrorCode(err), err
		}
		t, err = c.Ledger.AdvanceIngress(ctx, t, "cleanup", t.SHA256, t.NativeState)
		if err != nil {
			return t, "ledger-unavailable", err
		}
	}
	if t.Phase == "cleanup" {
		out, e := c.Native.ReleaseIntake(ctx, r)
		if e != nil || !receiptMatches(t, out) || !out.RawReleased {
			return t, "native-cleanup-unconfirmed", ErrUnavailable
		}
		path := filepath.Join(c.Directory, t.ID+".eml")
		info, e := os.Lstat(path)
		if e == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return t, "local-cleanup-unconfirmed", ErrIntegrity
			}
			e = os.Remove(path)
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return t, "local-cleanup-unconfirmed", ErrUnavailable
		}
		if _, e = os.Lstat(c.Directory); e == nil && syncDir(c.Directory) != nil {
			return t, "local-cleanup-unconfirmed", ErrUnavailable
		}
		t, err = c.Ledger.AdvanceIngress(ctx, t, "complete", t.SHA256, t.NativeState)
		if err != nil {
			return t, "ledger-unavailable", err
		}
	}
	return t, "", nil
}

// Work resumes already accepted, immutable tasks without an AD read. A failed
// cloud list or directory outage cannot block native/acknowledgement recovery.
func (c *Consumer) Work(ctx context.Context) error {
	if !c.valid() {
		return ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var firstError error
	for n := 0; n < 20 && ctx.Err() == nil; n++ {
		t, err := c.Ledger.ClaimIngress(ctx, c.SourceID)
		if errors.Is(err, store.ErrIngressIdle) {
			return firstError
		}
		if err != nil {
			return err
		}
		if t.Observation.SourceID != c.SourceID || t.Observation.Generation != c.Generation {
			_ = c.Ledger.ReleaseIngress(ctx, t, "source-generation-changed")
			return ErrIdentity
		}
		taskCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		updated, code, workErr := c.runTask(taskCtx, t)
		cancel()
		// Keep the original lease when a failed DB update returned no task.
		if updated.ID != "" {
			t = updated
		}
		release, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		err = c.Ledger.ReleaseIngress(release, t, code)
		done()
		if err != nil {
			return err
		}
		if workErr != nil && firstError == nil {
			firstError = workErr
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return firstError
}
