package cloudrelay

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

var (
	ErrEnvelopeRecipient = errors.New("envelope recipient has no active route")
	ErrEnvelopeConflict  = errors.New("envelope reservation conflicts with existing transaction")
	ErrEnvelopeCapacity  = errors.New("envelope reservation capacity exhausted")
	ErrEnvelopeExpired   = errors.New("envelope original expired after delivery")
)

// EnvelopeReservation binds each trusted SMTP envelope recipient to the route
// current at reservation time. It does not itself store an original message,
// confirm SMTP acceptance, grant delivery, or send a notification.
type EnvelopeReservation struct {
	ID           string              `json:"id"`
	State        string              `json:"state"`
	EnvelopeFrom string              `json:"envelope_from"`
	SHA256       string              `json:"sha256"`
	Bytes        int64               `json:"bytes"`
	ReceivedAt   time.Time           `json:"received_at"`
	Recipients   []EnvelopeRecipient `json:"recipients"`
}
type EnvelopeRecipient struct {
	Route     ingress.Route           `json:"route"`
	BindingID string                  `json:"binding_id"`
	Sequence  int64                   `json:"sequence"`
	Delivery  *ingress.SourceDelivery `json:"delivery,omitempty"`
	Notice    *sourceNotice           `json:"notice,omitempty"`
}

func normalizeEnvelope(id, from, digest string, size int64, recipients []string) ([]string, error) {
	if !identifier.MatchString(id) || !secretValue.MatchString(digest) || size < 1 || size > dms.IntakeMaxBytes || len(recipients) < 1 || len(recipients) > 100 {
		return nil, ErrConfiguration
	}
	addresses := append([]string(nil), recipients...)
	sort.Strings(addresses)
	for i, address := range addresses {
		if i > 0 && addresses[i-1] == address {
			return nil, ErrConfiguration
		}
	}
	return addresses, nil
}

// reserveLocked only modifies a private copy of the ledger. Its caller must
// durably write that state under the same lock before treating it as committed.
func (l *RouteLedger) reserveLocked(disk routeDisk, id, from, digest string, size int64, addresses []string) (routeDisk, EnvelopeReservation, bool, error) {
	for _, retired := range disk.Tombstones {
		if retired == id {
			return routeDisk{}, EnvelopeReservation{}, false, ErrEnvelopeExpired
		}
	}
	for _, existing := range disk.Reservations {
		if existing.ID != id {
			continue
		}
		if existing.State == "purging" {
			return routeDisk{}, EnvelopeReservation{}, false, ErrEnvelopeExpired
		}
		if existing.EnvelopeFrom != from || existing.SHA256 != digest || existing.Bytes != size || len(existing.Recipients) != len(addresses) {
			return routeDisk{}, EnvelopeReservation{}, false, ErrEnvelopeConflict
		}
		targets := make([]string, 0, len(existing.Recipients))
		for _, recipient := range existing.Recipients {
			targets = append(targets, l.smtpAddress(recipient.Route))
		}
		sort.Strings(targets)
		for i, target := range targets {
			if target != addresses[i] {
				return routeDisk{}, EnvelopeReservation{}, false, ErrEnvelopeConflict
			}
		}
		return disk, existing, true, nil
	}
	pending := 0
	for _, item := range disk.Reservations {
		if item.State == "pending" {
			pending++
		}
	}
	if len(disk.Reservations) >= 5000 || pending >= 64 || disk.State.Sequence > routeMaxInteger-int64(len(addresses))-1 {
		return routeDisk{}, EnvelopeReservation{}, false, ErrEnvelopeCapacity
	}
	active := map[string]ingress.RouteBinding{}
	for _, binding := range disk.State.Bindings {
		if binding.ClosedSequence == nil {
			active[l.smtpAddress(binding.Route)] = binding
		}
	}
	reservation := EnvelopeReservation{ID: id, State: "pending", EnvelopeFrom: from, SHA256: digest, Bytes: size, ReceivedAt: time.Now().UTC(), Recipients: make([]EnvelopeRecipient, 0, len(addresses))}
	for _, address := range addresses {
		binding, ok := active[address]
		if !ok {
			return routeDisk{}, EnvelopeReservation{}, false, ErrEnvelopeRecipient
		}
		disk.State.Sequence++
		recipient := EnvelopeRecipient{Route: binding.Route, BindingID: binding.ID, Sequence: disk.State.Sequence}
		if !l.validEnvelopeRecipient(reservation, recipient) {
			return routeDisk{}, EnvelopeReservation{}, false, ErrConfiguration
		}
		reservation.Recipients = append(reservation.Recipients, recipient)
	}
	sort.Slice(reservation.Recipients, func(i, j int) bool {
		return reservation.Recipients[i].Route.Address < reservation.Recipients[j].Route.Address
	})
	disk.Reservations = append(disk.Reservations, reservation)
	return disk, reservation, false, nil
}

// ReserveEnvelope persists only an internal pending envelope. It does not
// establish that an original exists or authorize an SMTP success response.
// IDs must be stable for retries and unique to each new SMTP transaction.
func (l *RouteLedger) ReserveEnvelope(id, from, digest string, size int64, recipients []string) (EnvelopeReservation, error) {
	addresses, err := normalizeEnvelope(id, from, digest, size, recipients)
	if err != nil {
		return EnvelopeReservation{}, err
	}
	var result EnvelopeReservation
	_, err = l.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		updated, reservation, existing, err := l.reserveLocked(disk, id, from, digest, size, addresses)
		if err != nil {
			return routeDisk{}, err
		}
		if existing {
			if unix.Fsync(dir) != nil {
				return routeDisk{}, ErrRouteUnknown
			}
		} else {
			if err = l.write(dir, updated); err != nil {
				return routeDisk{}, err
			}
		}
		result = reservation
		return updated, nil
	})
	return result, err
}

func (l *RouteLedger) envelopeMetadata(e EnvelopeReservation, r EnvelopeRecipient) dms.IntakeMetadata {
	return dms.IntakeMetadata{SourceID: l.source, MessageID: strings.Repeat("0", 16-len(strconv.FormatInt(r.Sequence, 10))) + strconv.FormatInt(r.Sequence, 10) + "-" + e.ID, MailboxID: r.Route.MailboxID, OwnerAnchor: r.Route.OwnerAnchor, AcceptedAddress: r.Route.Address, ClaimVersion: r.Route.ClaimVersion, EnvelopeFrom: e.EnvelopeFrom, SHA256: e.SHA256}
}
func (l *RouteLedger) validEnvelopeRecipient(e EnvelopeReservation, r EnvelopeRecipient) bool {
	if r.Sequence <= 0 || r.Sequence >= routeMaxInteger || !l.allowed(r.Route) {
		return false
	}
	// Match the eventual DMS intake format; the message ID is deterministic
	// for this reservation and distinct for each SMTP envelope recipient.
	m := l.envelopeMetadata(e, r)
	return m.Validate() == nil
}
func (l *RouteLedger) validDisk(disk routeDisk) bool {
	if !l.valid(disk.State) || disk.ForwardDomain != l.forwardDomain || disk.Reservations == nil || len(disk.Reservations) > 5000 || disk.Tombstones == nil || len(disk.Tombstones) > 50000 || len(disk.Domains) != len(l.domains) {
		return false
	}
	for i := range l.domains {
		if disk.Domains[i] != l.domains[i] {
			return false
		}
	}
	if disk.Pressure != nil && (disk.Pressure.At.IsZero() || disk.Pressure.NeededBytes < 1 || disk.Pressure.NeededBytes > dms.IntakeMaxBytes) {
		return false
	}
	bindings := make(map[string]ingress.RouteBinding, len(disk.State.Bindings))
	for _, binding := range disk.State.Bindings {
		bindings[binding.ID] = binding
	}
	ids, sequences := map[string]bool{}, map[int64]bool{}
	for _, id := range disk.Tombstones {
		if !identifier.MatchString(id) || ids[id] {
			return false
		}
		ids[id] = true
	}
	pending := 0
	for _, reservation := range disk.Reservations {
		if !identifier.MatchString(reservation.ID) || ids[reservation.ID] || reservation.State != "pending" && reservation.State != "ready" && reservation.State != "purging" || !secretValue.MatchString(reservation.SHA256) || reservation.Bytes < 1 || reservation.Bytes > dms.IntakeMaxBytes || reservation.ReceivedAt.IsZero() || len(reservation.Recipients) < 1 || len(reservation.Recipients) > 100 {
			return false
		}
		if reservation.State == "pending" {
			pending++
			if pending > 64 {
				return false
			}
		}
		ids[reservation.ID] = true
		previous := ""
		for _, recipient := range reservation.Recipients {
			binding, ok := bindings[recipient.BindingID]
			if !ok || binding.Route != recipient.Route || !l.validEnvelopeRecipient(reservation, recipient) || recipient.Route.Address <= previous || sequences[recipient.Sequence] || recipient.Sequence < binding.FirstSequence || recipient.Sequence > disk.State.Sequence || binding.ClosedSequence != nil && recipient.Sequence > *binding.ClosedSequence {
				return false
			}
			if d := recipient.Delivery; d != nil {
				meta := l.envelopeMetadata(reservation, recipient)
				if reservation.State == "pending" || d.ReceiptID != meta.Key() || d.SHA256 != reservation.SHA256 || d.State != "stored" && d.State != "processed" || d.AcknowledgedAt.IsZero() || d.AcknowledgedAt.Before(reservation.ReceivedAt) {
					return false
				}
			}
			if reservation.State == "purging" && recipient.Delivery == nil {
				return false
			}
			if recipient.Notice != nil && (reservation.State == "pending" || !l.validSourceNotice(reservation, recipient)) {
				return false
			}
			previous = recipient.Route.Address
			sequences[recipient.Sequence] = true
		}
	}
	return true
}
