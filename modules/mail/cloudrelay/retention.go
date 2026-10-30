package cloudrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

// A retained receipt proves each target was already confirmed. It does not
// contain the original body and must outlive retries of the same transaction.
type retiredEnvelope struct {
	Source     string              `json:"source"`
	Generation string              `json:"generation"`
	Envelope   EnvelopeReservation `json:"envelope"`
}

func (s *OriginalStore) readRetired(raw int, disk routeDisk, id string) (EnvelopeReservation, error) {
	fd, err := unix.Openat(raw, "retired-"+id, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return EnvelopeReservation{}, os.ErrNotExist
	}
	if err != nil {
		return EnvelopeReservation{}, ErrOriginalState
	}
	f := os.NewFile(uintptr(fd), "retired receipt")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return EnvelopeReservation{}, ErrOriginalState
	}
	body, err := io.ReadAll(io.LimitReader(f, routeMax+1))
	var record retiredEnvelope
	if err != nil || len(body) > routeMax || json.Unmarshal(body, &record) != nil {
		return EnvelopeReservation{}, ErrOriginalState
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(body, canonical) || record.Source != s.ledger.source || record.Generation != s.ledger.generation || record.Envelope.ID != id || record.Envelope.State != "purging" {
		return EnvelopeReservation{}, ErrOriginalState
	}
	probe := disk
	probe.Reservations = []EnvelopeReservation{record.Envelope}
	probe.Tombstones = []string{}
	if !s.ledger.validDisk(probe) {
		return EnvelopeReservation{}, ErrOriginalState
	}
	return record.Envelope, nil
}
func (s *OriginalStore) writeRetired(raw int, disk routeDisk, record EnvelopeReservation) error {
	body, err := json.Marshal(retiredEnvelope{Source: s.ledger.source, Generation: s.ledger.generation, Envelope: record})
	if err != nil || len(body) > routeMax {
		return ErrOriginalState
	}
	old, err := s.readRetired(raw, disk, record.ID)
	if err == nil {
		oldBody, _ := json.Marshal(retiredEnvelope{Source: s.ledger.source, Generation: s.ledger.generation, Envelope: old})
		if !bytes.Equal(oldBody, body) || unix.Fsync(raw) != nil {
			return ErrOriginalState
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	name := ".retired-" + record.ID
	fd, e := unix.Openat(raw, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e == nil {
		if !secureNoticeFile(fd, false) {
			unix.Close(fd)
			return ErrOriginalState
		}
		unix.Close(fd)
		if unix.Unlinkat(raw, name, 0) != nil || unix.Fsync(raw) != nil {
			return ErrOriginalUnknown
		}
	} else if !errors.Is(e, unix.ENOENT) {
		return ErrOriginalState
	}
	used, err := originalUsage(raw, disk)
	if err != nil {
		return err
	}
	root, _ := json.Marshal(disk)
	// A temporary overlap of raw and receipt must fit in the explicit capacity.
	if used+int64(len(body))+int64(len(root))+8192 > s.capacity {
		return ErrOriginalCapacity
	}
	fd, e = unix.Openat(raw, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return ErrOriginalState
	}
	f := os.NewFile(uintptr(fd), "staged receipt")
	if n, e := f.Write(body); e != nil || n != len(body) || f.Sync() != nil {
		f.Close()
		return ErrOriginalState
	}
	if f.Close() != nil {
		return ErrOriginalState
	}
	if unix.Renameat(raw, name, raw, "retired-"+record.ID) != nil || unix.Fsync(raw) != nil {
		return ErrOriginalUnknown
	}
	return nil
}
func allDelivered(e EnvelopeReservation) (time.Time, bool) {
	if e.State != "ready" && e.State != "purging" || len(e.Recipients) == 0 {
		return time.Time{}, false
	}
	var last time.Time
	for _, r := range e.Recipients {
		if r.Delivery == nil {
			return time.Time{}, false
		}
		if r.Delivery.AcknowledgedAt.After(last) {
			last = r.Delivery.AcknowledgedAt
		}
	}
	return last, !last.IsZero()
}

// purgeLocked persists intent and the final receipt before unlinking original
// bytes. Every intermediate state can be retried while holding the source lock.
func (s *OriginalStore) purgeLocked(dir, raw int, disk routeDisk, index int) (routeDisk, error) {
	if index < 0 || index >= len(disk.Reservations) || len(disk.Tombstones) >= 50000 {
		return routeDisk{}, ErrOriginalCapacity
	}
	e := disk.Reservations[index]
	if _, ok := allDelivered(e); !ok {
		return routeDisk{}, ErrOriginalState
	}
	_, rawErr := readOriginal(raw, e.ID, e.Bytes, e.SHA256)
	if e.State == "ready" {
		if rawErr != nil {
			return routeDisk{}, ErrOriginalState
		}
		e.State = "purging"
		disk.Reservations[index] = e
		if err := s.ledger.write(dir, disk); err != nil {
			return routeDisk{}, err
		}
	} else if rawErr != nil && !errors.Is(rawErr, os.ErrNotExist) {
		return routeDisk{}, ErrOriginalState
	}
	_, retiredErr := s.readRetired(raw, disk, e.ID)
	if errors.Is(rawErr, os.ErrNotExist) && retiredErr != nil {
		return routeDisk{}, ErrOriginalState
	}
	if err := s.writeRetired(raw, disk, e); err != nil {
		return routeDisk{}, err
	}
	if rawErr == nil {
		if unix.Unlinkat(raw, "raw-"+e.ID, 0) != nil || unix.Fsync(raw) != nil {
			return routeDisk{}, ErrOriginalUnknown
		}
	} else if unix.Fsync(raw) != nil {
		return routeDisk{}, ErrOriginalUnknown
	}
	disk.Reservations = append(disk.Reservations[:index], disk.Reservations[index+1:]...)
	disk.Tombstones = append(disk.Tombstones, e.ID)
	if err := s.ledger.write(dir, disk); err != nil {
		return routeDisk{}, err
	}
	return disk, nil
}

func (s *OriginalStore) recoverPurgesLocked(dir, raw int, disk routeDisk) (routeDisk, error) {
	for i := 0; i < len(disk.Reservations); {
		if disk.Reservations[i].State != "purging" {
			i++
			continue
		}
		updated, err := s.purgeLocked(dir, raw, disk, i)
		if err != nil {
			return routeDisk{}, err
		}
		disk = updated
	}
	return disk, nil
}

// maintainLocked removes one oldest fully delivered original, only after the
// configured retention period unless space is required for a new admission.
func (s *OriginalStore) maintainLocked(dir, raw int, disk routeDisk, now time.Time, pressure bool) (routeDisk, bool, error) {
	for i := range disk.Reservations {
		if disk.Reservations[i].State == "purging" {
			updated, err := s.purgeLocked(dir, raw, disk, i)
			return updated, err == nil, err
		}
	}
	index := -1
	var oldest time.Time
	for i, e := range disk.Reservations {
		last, ok := allDelivered(e)
		if !ok || last.After(now) {
			continue
		}
		if !pressure && now.Before(last.Add(time.Duration(s.deliveredDays)*24*time.Hour)) {
			continue
		}
		if index < 0 || last.Before(oldest) {
			index = i
			oldest = last
		}
	}
	if index < 0 {
		return disk, false, nil
	}
	updated, err := s.purgeLocked(dir, raw, disk, index)
	if err != nil {
		return routeDisk{}, false, err
	}
	return updated, true, nil
}

// Maintain removes at most one eligible delivered original per call and never
// removes undelivered mail. A supervisor must schedule it; this is a candidate.
func (s *OriginalStore) Maintain(now time.Time) (bool, error) {
	if s == nil || s.ledger == nil || now.IsZero() {
		return false, ErrConfiguration
	}
	var removed bool
	err := s.sourceLocked(func(dir, raw int, disk routeDisk) (routeDisk, error) {
		var e error
		disk, removed, e = s.maintainLocked(dir, raw, disk, now, false)
		if e != nil {
			return routeDisk{}, e
		}
		disk, e = s.clearPressureIfRecovered(dir, raw, disk)
		return disk, e
	})
	return removed, err
}

// RunRetention performs bounded cleanup passes. Errors are reported without
// dropping pending mail and retried later; a supervisor must start the loop.
func (s *OriginalStore) RunRetention(ctx context.Context, onError func(error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		removed, err := s.Maintain(time.Now().UTC())
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, ErrRouteBusy) && onError != nil {
			onError(err)
		}
		if removed && err == nil {
			continue
		}
		delay := time.Minute
		if errors.Is(err, ErrRouteBusy) {
			delay = 50 * time.Millisecond
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

// retiredAck repeats only a previously recorded final receipt. It never creates
// a receipt after the original has expired or infers delivery from deletion.
func (s *OriginalStore) retiredAck(raw int, disk routeDisk, id, target string, input sourceAck) (ingress.SourceDelivery, error) {
	if len(id) != 53 || id[16] != '-' || !identifier.MatchString(id[17:]) {
		return ingress.SourceDelivery{}, os.ErrNotExist
	}
	transaction := id[17:]
	var reservation EnvelopeReservation
	found := false
	for _, item := range disk.Reservations {
		if item.ID == transaction && item.State == "purging" {
			reservation = item
			found = true
			break
		}
	}
	if !found {
		indexed := false
		for _, key := range disk.Tombstones {
			if key == transaction {
				indexed = true
				break
			}
		}
		if !indexed {
			return ingress.SourceDelivery{}, os.ErrNotExist
		}
		var err error
		reservation, err = s.readRetired(raw, disk, transaction)
		if err != nil {
			return ingress.SourceDelivery{}, ErrOriginalState
		}
	}
	for _, recipient := range reservation.Recipients {
		meta := s.ledger.envelopeMetadata(reservation, recipient)
		if meta.MessageID != id || meta.MailboxID != target {
			continue
		}
		if recipient.Delivery == nil || input.Receipt.ID != meta.Key() || input.Receipt.SHA256 != reservation.SHA256 || input.Receipt.State != "stored" && input.Receipt.State != "processed" {
			return ingress.SourceDelivery{}, ErrEnvelopeConflict
		}
		return *recipient.Delivery, nil
	}
	return ingress.SourceDelivery{}, os.ErrNotExist
}
