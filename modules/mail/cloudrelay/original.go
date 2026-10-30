package cloudrelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"golang.org/x/sys/unix"
)

var (
	ErrOriginalState    = errors.New("relay original state invalid or inaccessible")
	ErrOriginalCapacity = errors.New("relay original capacity exhausted")
	ErrOriginalUnknown  = errors.New("relay original write outcome unknown")
	ErrOriginalPending  = errors.New("relay original is not yet committed")
)

// OriginalStore is an internal candidate; it is not an SMTP server or a
// complete mail-source implementation. capacity includes a fixed 4 MiB control
// reserve. Pending files and orphan originals count against this logical quota.
type OriginalStore struct {
	ledger        *RouteLedger
	capacity      int64
	deliveredDays int
}
type originalConfig struct {
	Source        string `json:"source"`
	Generation    string `json:"generation"`
	Capacity      int64  `json:"capacity"`
	DeliveredDays int    `json:"delivered_days"`
}

func validOriginalCapacity(n int64) bool { return n > routeMax && n <= routeMaxInteger }

// InitializeOriginalStore requires an existing unused route ledger. If setup is
// interrupted, the partial directory is not silently reinitialized.
func InitializeOriginalStore(ledger *RouteLedger, capacity int64, deliveredDays int) (*OriginalStore, error) {
	if ledger == nil || !validOriginalCapacity(capacity) || deliveredDays < 1 || deliveredDays > 3650 {
		return nil, ErrConfiguration
	}
	s := &OriginalStore{ledger: ledger, capacity: capacity, deliveredDays: deliveredDays}
	_, err := ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		if len(disk.Reservations) != 0 {
			return routeDisk{}, ErrOriginalState
		}
		if unix.Mkdirat(dir, "originals", 0700) != nil || unix.Fsync(dir) != nil {
			return routeDisk{}, ErrOriginalState
		}
		raw, err := s.openDirectory(dir)
		if err != nil {
			return routeDisk{}, err
		}
		defer unix.Close(raw)
		config, _ := json.Marshal(originalConfig{Source: ledger.source, Generation: ledger.generation, Capacity: capacity, DeliveredDays: deliveredDays})
		fd, err := unix.Openat(raw, "config.json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if err != nil {
			return routeDisk{}, ErrOriginalState
		}
		file := os.NewFile(uintptr(fd), "config.json")
		if n, e := file.Write(config); e != nil || n != len(config) || file.Sync() != nil {
			file.Close()
			return routeDisk{}, ErrOriginalState
		}
		if file.Close() != nil || unix.Fsync(raw) != nil {
			return routeDisk{}, ErrOriginalUnknown
		}
		return disk, nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func OpenOriginalStore(ledger *RouteLedger, capacity int64, deliveredDays int) (*OriginalStore, error) {
	if ledger == nil || !validOriginalCapacity(capacity) || deliveredDays < 1 || deliveredDays > 3650 {
		return nil, ErrConfiguration
	}
	s := &OriginalStore{ledger: ledger, capacity: capacity, deliveredDays: deliveredDays}
	_, err := ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		raw, e := s.openDirectory(dir)
		if e != nil {
			return routeDisk{}, e
		}
		defer unix.Close(raw)
		if e = s.checkConfig(raw); e != nil {
			return routeDisk{}, e
		}
		return disk, nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (s *OriginalStore) openDirectory(dir int) (int, error) {
	fd, err := unix.Openat(dir, "originals", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, ErrOriginalState
	}
	if !secureNoticeFile(fd, true) {
		unix.Close(fd)
		return -1, ErrOriginalState
	}
	return fd, nil
}
func (s *OriginalStore) checkConfig(dir int) error {
	fd, err := unix.Openat(dir, "config.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrOriginalState
	}
	f := os.NewFile(uintptr(fd), "config.json")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return ErrOriginalState
	}
	got, err := io.ReadAll(io.LimitReader(f, 1025))
	expected, _ := json.Marshal(originalConfig{Source: s.ledger.source, Generation: s.ledger.generation, Capacity: s.capacity, DeliveredDays: s.deliveredDays})
	if err != nil || !bytes.Equal(got, expected) {
		return ErrOriginalState
	}
	return nil
}

// originalUsage counts every recognized original and incomplete temporary
// file, including orphans. Unexpected objects fail closed. It is always called
// while the source lock is held.
func originalUsage(dir int, disk routeDisk) (int64, error) {
	// Dup would share the directory stream offset with prior scans, causing
	// subsequent quota checks to miss files. Open a fresh file description.
	dup, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return 0, ErrOriginalState
	}
	f := os.NewFile(uintptr(dup), "originals")
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil || len(names) > 60000 {
		return 0, ErrOriginalState
	}
	var used int64
	byID := map[string]int64{}
	rawSizes := map[string]int64{}
	for _, name := range names {
		if name == "config.json" {
			continue
		}
		id, kind := "", ""
		for _, prefix := range []string{"raw-", ".pending-", "retired-", ".retired-"} {
			if strings.HasPrefix(name, prefix) {
				id, kind = strings.TrimPrefix(name, prefix), prefix
				break
			}
		}
		if !identifier.MatchString(id) {
			return 0, ErrOriginalState
		}
		fd, e := unix.Openat(dir, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			return 0, ErrOriginalState
		}
		var stat unix.Stat_t
		ok := secureNoticeFile(fd, false) && unix.Fstat(fd, &stat) == nil && stat.Size >= 0 && stat.Size <= dms.IntakeMaxBytes
		if kind == "retired-" || kind == ".retired-" {
			ok = ok && stat.Size <= routeMax
		}
		unix.Close(fd)
		if !ok || used > routeMaxInteger-stat.Size {
			return 0, ErrOriginalState
		}
		used += stat.Size
		if kind == "raw-" || kind == ".pending-" {
			byID[id] += stat.Size
		}
		if kind == "raw-" {
			rawSizes[id] = stat.Size
		}
	}
	for _, reservation := range disk.Reservations {
		if reservation.State == "ready" {
			if size, ok := rawSizes[reservation.ID]; !ok || size != reservation.Bytes {
				return 0, ErrOriginalState
			}
		}
		if reservation.State != "purging" && byID[reservation.ID] < reservation.Bytes {
			missing := reservation.Bytes - byID[reservation.ID]
			if used > routeMaxInteger-missing {
				return 0, ErrOriginalState
			}
			used += missing
		}
	}
	return used, nil
}
func readOriginal(dir int, id string, size int64, digest string) ([]byte, error) {
	return readOriginalFile(dir, "raw-"+id, size, digest, false)
}

func readOriginalFile(dir int, name string, size int64, digest string, syncFile bool) ([]byte, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, ErrOriginalState
	}
	f := os.NewFile(uintptr(fd), "original")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return nil, ErrOriginalState
	}
	data, err := io.ReadAll(io.LimitReader(f, dms.IntakeMaxBytes+1))
	hash := sha256.Sum256(data)
	if err != nil || int64(len(data)) != size || hex.EncodeToString(hash[:]) != digest {
		return nil, ErrOriginalState
	}
	if syncFile && f.Sync() != nil {
		return nil, ErrOriginalUnknown
	}
	return data, nil
}

// recoverPending runs only after the exclusive source service lock is held,
// before SMTP or source workers start. It never invents an envelope for an
// unbound file or promotes an incomplete original. A sender that did not see
// SMTP's final response may independently retry, as with any SMTP server.
func (s *OriginalStore) recoverPending() (int, error) {
	if s == nil || s.ledger == nil {
		return 0, ErrConfiguration
	}
	recovered := 0
	_, err := s.ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		raw, e := s.openDirectory(dir)
		if e != nil {
			return routeDisk{}, e
		}
		defer unix.Close(raw)
		if e = s.checkConfig(raw); e != nil {
			return routeDisk{}, e
		}
		for i := range disk.Reservations {
			entry := &disk.Reservations[i]
			if entry.State != "pending" {
				continue
			}
			_, rawErr := readOriginalFile(raw, "raw-"+entry.ID, entry.Bytes, entry.SHA256, true)
			if rawErr != nil && !errors.Is(rawErr, os.ErrNotExist) {
				return routeDisk{}, rawErr
			}
			_, stageErr := readOriginalFile(raw, ".pending-"+entry.ID, entry.Bytes, entry.SHA256, true)
			if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
				return routeDisk{}, stageErr
			}
			if rawErr == nil && stageErr == nil {
				return routeDisk{}, ErrOriginalState
			}
			if rawErr != nil && stageErr != nil {
				// Old or damaged pending reservations remain quarantined.
				continue
			}
			if stageErr == nil && unix.Renameat(raw, ".pending-"+entry.ID, raw, "raw-"+entry.ID) != nil {
				return routeDisk{}, ErrOriginalUnknown
			}
			if unix.Fsync(raw) != nil {
				return routeDisk{}, ErrOriginalUnknown
			}
			entry.State = "ready"
			if e = s.ledger.write(dir, disk); e != nil {
				return routeDisk{}, e
			}
			recovered++
		}
		return disk, nil
	})
	return recovered, err
}

// Store stages and syncs the complete original, durably records the trusted
// envelope, publishes the original, then marks the reservation ready. An
// unbound staging file is quarantined, while a bound complete staging file can
// be recovered without choosing a new route. Pending work is never exposed as
// complete. This candidate is not yet a complete SMTP source implementation.
func (s *OriginalStore) Store(id, from string, recipients []string, original []byte) (EnvelopeReservation, error) {
	return s.storeWithCheckpoint(id, from, recipients, original, nil)
}

// checkpoint is only used by process-crash tests; production passes nil.
func (s *OriginalStore) storeWithCheckpoint(id, from string, recipients []string, original []byte, checkpoint func(string)) (EnvelopeReservation, error) {
	if s == nil || s.ledger == nil || len(original) < 1 || len(original) > dms.IntakeMaxBytes {
		return EnvelopeReservation{}, ErrConfiguration
	}
	original = bytes.Clone(original)
	digestBytes := sha256.Sum256(original)
	digest := hex.EncodeToString(digestBytes[:])
	size := int64(len(original))
	addresses, err := normalizeEnvelope(id, from, digest, size, recipients)
	if err != nil {
		return EnvelopeReservation{}, err
	}
	var result EnvelopeReservation
	_, err = s.ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		raw, e := s.openDirectory(dir)
		if e != nil {
			return routeDisk{}, e
		}
		defer unix.Close(raw)
		if e = s.checkConfig(raw); e != nil {
			return routeDisk{}, e
		}
		disk, e = s.recoverPurgesLocked(dir, raw, disk)
		if e != nil {
			return routeDisk{}, e
		}
		if _, retiredErr := s.readRetired(raw, disk, id); retiredErr == nil {
			return routeDisk{}, ErrEnvelopeExpired
		} else if !errors.Is(retiredErr, os.ErrNotExist) {
			return routeDisk{}, retiredErr
		}
		updated, reservation, existing, e := s.ledger.reserveLocked(disk, id, from, digest, size, addresses)
		if e != nil {
			if errors.Is(e, ErrEnvelopeCapacity) {
				if reportErr := s.recordPressure(dir, size); reportErr != nil {
					return routeDisk{}, reportErr
				}
			}
			return routeDisk{}, e
		}
		data, readErr := readOriginal(raw, id, size, digest)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return routeDisk{}, ErrOriginalState
		}
		// A raw file with no durable reservation cannot prove its former owner.
		// It remains quarantined even when its contents match the current request.
		if !existing && readErr == nil {
			return routeDisk{}, ErrOriginalState
		}
		if reservation.State == "ready" {
			if readErr != nil || !bytes.Equal(data, original) {
				return routeDisk{}, ErrOriginalState
			}
			if unix.Fsync(raw) != nil || unix.Fsync(dir) != nil {
				return routeDisk{}, ErrOriginalUnknown
			}
			result = reservation
			return updated, nil
		}
		for {
			used, usageErr := originalUsage(raw, updated)
			if usageErr != nil {
				return routeDisk{}, usageErr
			}
			if used <= s.capacity-routeMax {
				break
			}
			var removed bool
			disk, removed, e = s.maintainLocked(dir, raw, disk, time.Now().UTC(), true)
			if e != nil {
				if errors.Is(e, ErrOriginalCapacity) {
					if reportErr := s.recordPressure(dir, size); reportErr != nil {
						return routeDisk{}, reportErr
					}
				}
				return routeDisk{}, e
			}
			if !removed {
				if reportErr := s.recordPressure(dir, size); reportErr != nil {
					return routeDisk{}, reportErr
				}
				return routeDisk{}, ErrOriginalCapacity
			}
			updated, reservation, existing, e = s.ledger.reserveLocked(disk, id, from, digest, size, addresses)
			if e != nil {
				return routeDisk{}, e
			}
		}
		if readErr == nil {
			if !bytes.Equal(data, original) || unix.Fsync(raw) != nil {
				return routeDisk{}, ErrOriginalState
			}
		} else {
			pending := ".pending-" + id
			fd, e := unix.Openat(raw, pending, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if e == nil {
				if !secureNoticeFile(fd, false) {
					unix.Close(fd)
					return routeDisk{}, ErrOriginalState
				}
				unix.Close(fd)
				// A staged file without a reservation has no trustworthy owner.
				// Never silently claim or overwrite it under a new transaction.
				if !existing {
					return routeDisk{}, ErrOriginalState
				}
				if unix.Unlinkat(raw, pending, 0) != nil || unix.Fsync(raw) != nil {
					return routeDisk{}, ErrOriginalUnknown
				}
			} else if !errors.Is(e, unix.ENOENT) {
				return routeDisk{}, ErrOriginalState
			}
			fd, e = unix.Openat(raw, pending, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
			if e != nil {
				return routeDisk{}, ErrOriginalState
			}
			file := os.NewFile(uintptr(fd), "staged original")
			if n, e := file.Write(original); e != nil || n != len(original) || file.Sync() != nil {
				file.Close()
				return routeDisk{}, ErrOriginalState
			}
			if file.Close() != nil {
				return routeDisk{}, ErrOriginalState
			}
			if unix.Fsync(raw) != nil {
				return routeDisk{}, ErrOriginalUnknown
			}
			if checkpoint != nil {
				checkpoint("staged")
			}
		}
		if !existing {
			if e = s.ledger.write(dir, updated); e != nil {
				return routeDisk{}, e
			}
		} else if unix.Fsync(dir) != nil {
			return routeDisk{}, ErrRouteUnknown
		}
		if checkpoint != nil {
			checkpoint("reserved")
		}
		if readErr != nil {
			if unix.Renameat(raw, ".pending-"+id, raw, "raw-"+id) != nil || unix.Fsync(raw) != nil {
				return routeDisk{}, ErrOriginalUnknown
			}
		}
		if checkpoint != nil {
			checkpoint("published")
		}
		for i := range updated.Reservations {
			if updated.Reservations[i].ID == id {
				updated.Reservations[i].State = "ready"
			}
		}
		if e = s.ledger.write(dir, updated); e != nil {
			return routeDisk{}, e
		}
		if checkpoint != nil {
			checkpoint("ready")
		}
		updated, e = s.clearPressureIfRecovered(dir, raw, updated)
		if e != nil {
			return routeDisk{}, e
		}
		reservation.State = "ready"
		result = reservation
		return updated, nil
	})
	return result, err
}

func (s *OriginalStore) Read(id string) (EnvelopeReservation, []byte, error) {
	if s == nil || s.ledger == nil || !identifier.MatchString(id) {
		return EnvelopeReservation{}, nil, ErrConfiguration
	}
	var result EnvelopeReservation
	var data []byte
	_, err := s.ledger.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		raw, e := s.openDirectory(dir)
		if e != nil {
			return routeDisk{}, e
		}
		defer unix.Close(raw)
		if e = s.checkConfig(raw); e != nil {
			return routeDisk{}, e
		}
		for _, reservation := range disk.Reservations {
			if reservation.ID == id {
				if reservation.State != "ready" {
					return routeDisk{}, ErrOriginalPending
				}
				result = reservation
				data, e = readOriginal(raw, id, reservation.Bytes, reservation.SHA256)
				if e != nil {
					return routeDisk{}, ErrOriginalState
				}
				if unix.Fsync(raw) != nil || unix.Fsync(dir) != nil {
					return routeDisk{}, ErrOriginalUnknown
				}
				return disk, nil
			}
		}
		return routeDisk{}, os.ErrNotExist
	})
	return result, data, err
}
