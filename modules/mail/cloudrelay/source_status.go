package cloudrelay

import (
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// sourcePressure records an admission failure, not a claim of free disk space.
// Its threshold is the largest rejected original until a safe recheck clears it.
type sourcePressure struct {
	At          time.Time `json:"at"`
	NeededBytes int64     `json:"needed_bytes"`
}

func (s *OriginalStore) recordPressure(dir int, needed int64) error {
	// A failed cleanup may already have changed the ledger. Always reread it
	// while the source lock is held rather than overwriting a stale snapshot.
	disk, err := s.ledger.read(dir)
	if err != nil {
		return err
	}
	if disk.Pressure == nil {
		disk.Pressure = &sourcePressure{At: time.Now().UTC(), NeededBytes: needed}
	} else if needed > disk.Pressure.NeededBytes {
		disk.Pressure.NeededBytes = needed
	}
	return s.ledger.write(dir, disk)
}
func (s *OriginalStore) clearPressureIfRecovered(dir, raw int, disk routeDisk) (routeDisk, error) {
	if disk.Pressure == nil {
		return disk, nil
	}
	if len(disk.Reservations) >= 5000 || len(disk.Tombstones) >= 50000 {
		return disk, nil
	}
	pending := 0
	for _, e := range disk.Reservations {
		if e.State == "purging" {
			return disk, nil
		}
		if e.State == "pending" {
			pending++
		}
	}
	if pending >= 64 {
		return disk, nil
	}
	used, err := originalUsage(raw, disk)
	if err != nil {
		return routeDisk{}, err
	}
	if used > s.capacity-routeMax-disk.Pressure.NeededBytes {
		return disk, nil
	}
	disk.Pressure = nil
	if err = s.ledger.write(dir, disk); err != nil {
		return routeDisk{}, err
	}
	return disk, nil
}
func (s *OriginalStore) sourceStatus(n *Notifier) (ingress.SourceStatus, error) {
	out := ingress.SourceStatus{Schema: cloudSourceSchema, SourceID: s.ledger.source, Generation: s.ledger.generation, CapacityBytes: s.capacity, DeliveredDays: s.deliveredDays, AccountQuota: "unknown", Notifications: "unconfigured"}
	if n != nil {
		out.Notifications = "configured"
	}
	err := s.sourceLockedBrief(func(_ int, raw int, disk routeDisk) (routeDisk, error) {
		out.ForwardDomain = disk.ForwardDomain
		used, e := originalUsage(raw, disk)
		if e != nil || used > s.capacity-routeMax {
			return routeDisk{}, ErrOriginalState
		}
		out.UsedBytes = routeMax + used
		for _, reservation := range disk.Reservations {
			if reservation.State == "pending" {
				out.PendingReservations++
			}
			if n != nil {
				for _, recipient := range reservation.Recipients {
					notice := recipient.Notice
					if notice == nil {
						continue
					}
					switch notice.State {
					case "notified", "exhausted", "configuration-error", "unknown":
						continue
					}
					if notice.Binding != notificationBinding(n) {
						out.Notifications = "configuration-error"
					}
				}
			}
		}
		if disk.Pressure != nil {
			out.Alert = &ingress.SourceAlert{Code: "capacity-or-admission-limit", At: disk.Pressure.At}
		}
		return disk, nil
	})
	return out, err
}
