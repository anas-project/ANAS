package cloudrelay

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
	"golang.org/x/sys/unix"
)

// RouteLedger is a candidate persistent route ledger, not a running source.
// Future SMTP acceptance must reserve its sequence and immutable recipient in
// the same locked transaction as this state. Until then it must not be used to
// accept mail or presented as a functional VPS relay.
type RouteLedger struct {
	directory, source, generation, forwardDomain string
	domains                                      []string
}

var (
	ErrRouteState    = errors.New("relay route state invalid or inaccessible")
	ErrRouteConflict = errors.New("relay route revision or binding conflict")
	ErrRouteBusy     = errors.New("relay route ledger busy")
	ErrRouteUnknown  = errors.New("relay route write outcome unknown")
)

const routeMax = 4 << 20
const routeMaxInteger int64 = 9007199254740991

type routeDisk struct {
	Domains       []string              `json:"domains"`
	State         ingress.RouteState    `json:"state"`
	Reservations  []EnvelopeReservation `json:"reservations"`
	Tombstones    []string              `json:"tombstones"`
	Pressure      *sourcePressure       `json:"pressure,omitempty"`
	ForwardDomain string                `json:"forward_domain,omitempty"`
}

func newRouteLedger(directory, source, generation string, domains []string, forwardDomain string) (*RouteLedger, error) {
	if !filepath.IsAbs(directory) || !identifier.MatchString(source) || !identifier.MatchString(generation) || len(domains) == 0 || len(domains) > 100 {
		return nil, ErrConfiguration
	}
	seen := map[string]bool{}
	for _, domain := range domains {
		canonical, err := directorymail.Domain(domain, "")
		if err != nil || domain != canonical || seen[domain] {
			return nil, ErrConfiguration
		}
		seen[domain] = true
	}
	if forwardDomain != "" {
		canonical, err := directorymail.Domain(forwardDomain, "")
		if err != nil || canonical != forwardDomain || seen[forwardDomain] || len("anas-"+strings.Repeat("0", 32)+"@"+forwardDomain) > 90 {
			return nil, ErrConfiguration
		}
	}
	return &RouteLedger{directory: directory, source: source, generation: generation, domains: append([]string(nil), domains...), forwardDomain: forwardDomain}, nil
}

// InitializeRouteLedger requires a fresh, dedicated private directory. The
// exclusive lock-file creation is deliberate: a lost state file cannot be
// interpreted as an empty source on a subsequent initialization.
func InitializeRouteLedger(directory, source, generation string, domains []string) (*RouteLedger, error) {
	return initializeRouteLedger(directory, source, generation, domains, "")
}

func initializeRouteLedger(directory, source, generation string, domains []string, forwardDomain string) (*RouteLedger, error) {
	l, err := newRouteLedger(directory, source, generation, domains, forwardDomain)
	if err != nil {
		return nil, err
	}
	dir, err := l.openDir()
	if err != nil {
		return nil, err
	}
	defer unix.Close(dir)
	dup, err := unix.Dup(dir)
	if err != nil {
		return nil, ErrRouteState
	}
	openedDir := os.NewFile(uintptr(dup), "route directory")
	entries, err := openedDir.Readdirnames(1)
	openedDir.Close()
	if err != io.EOF || len(entries) != 0 {
		return nil, ErrRouteState
	}
	lock, err := unix.Openat(dir, "routes.lock", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrRouteState
	}
	defer unix.Close(lock)
	if !secureNoticeFile(lock, false) || unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil || unix.Fsync(dir) != nil {
		return nil, ErrRouteState
	}
	fingerprint, err := ingress.RouteFingerprint([]ingress.Route{})
	if err != nil {
		return nil, ErrRouteState
	}
	state := ingress.RouteState{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, Fingerprint: fingerprint, Bindings: []ingress.RouteBinding{}}
	if err = l.write(dir, routeDisk{Domains: l.domains, State: state, Reservations: []EnvelopeReservation{}, Tombstones: []string{}, ForwardDomain: l.forwardDomain}); err != nil {
		return nil, err
	}
	return l, nil
}

// OpenRouteLedger refuses missing, corrupt, or differently configured state.
func OpenRouteLedger(directory, source, generation string, domains []string) (*RouteLedger, error) {
	return openRouteLedger(directory, source, generation, domains, "")
}

func openRouteLedger(directory, source, generation string, domains []string, forwardDomain string) (*RouteLedger, error) {
	l, err := newRouteLedger(directory, source, generation, domains, forwardDomain)
	if err != nil {
		return nil, err
	}
	if _, err = l.Read(); err != nil {
		return nil, err
	}
	return l, nil
}

// smtpAddress is a routing identifier, not an authentication credential.
// It binds the public claim and mailbox to this source generation, without
// relying on message headers or exposing a person's directory anchor.
func (l *RouteLedger) smtpAddress(route ingress.Route) string {
	if l.forwardDomain == "" {
		return route.Address
	}
	target, _ := ingress.ForwardTarget(l.source, l.generation, route, l.forwardDomain)
	return target
}

func (l *RouteLedger) openDir() (int, error) {
	fd, err := unix.Open(l.directory, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, ErrRouteState
	}
	if !secureNoticeFile(fd, true) {
		unix.Close(fd)
		return -1, ErrRouteState
	}
	return fd, nil
}
func (l *RouteLedger) locked(fn func(int, routeDisk) (routeDisk, error)) (routeDisk, error) {
	dir, err := l.openDir()
	if err != nil {
		return routeDisk{}, err
	}
	defer unix.Close(dir)
	lock, err := unix.Openat(dir, "routes.lock", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return routeDisk{}, ErrRouteState
	}
	defer unix.Close(lock)
	if !secureNoticeFile(lock, false) {
		return routeDisk{}, ErrRouteState
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return routeDisk{}, ErrRouteBusy
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	state, err := l.read(dir)
	if err != nil {
		return routeDisk{}, err
	}
	return fn(dir, state)
}
func (l *RouteLedger) allowed(r ingress.Route) bool {
	parts := strings.Split(r.Address, "@")
	if len(parts) != 2 || !identifier.MatchString(r.MailboxID) || !identifier.MatchString(r.OwnerAnchor) || !identifier.MatchString(r.ClaimVersion) {
		return false
	}
	canonical, err := directorymail.Address(parts[0], parts[1])
	if err != nil || canonical != r.Address {
		return false
	}
	for _, domain := range l.domains {
		if parts[1] == domain {
			return true
		}
	}
	return false
}
func (l *RouteLedger) valid(s ingress.RouteState) bool {
	if s.Schema != "anas.mail-source/v1" || s.SourceID != l.source || s.Generation != l.generation || s.Revision < 0 || s.Revision > routeMaxInteger || s.Sequence < 0 || s.Sequence >= routeMaxInteger || s.Bindings == nil || len(s.Bindings) > 5000 || s.Revision == 0 && (s.OperationID != "" || s.Sequence != 0 || len(s.Bindings) != 0) || s.Revision > 0 && !identifier.MatchString(s.OperationID) {
		return false
	}
	ids, addresses, targets := map[string]bool{}, map[string]bool{}, map[string]bool{}
	active := []ingress.Route{}
	for _, b := range s.Bindings {
		id := store.IngressBinding{SourceID: l.source, Generation: l.generation, Address: b.Address, ClaimVersion: b.ClaimVersion, MailboxID: b.MailboxID, OwnerAnchor: b.OwnerAnchor}.Key()
		target := l.smtpAddress(b.Route)
		if !l.allowed(b.Route) || b.ID != id || ids[id] || l.forwardDomain != "" && targets[target] || b.FirstSequence < 1 || b.FirstSequence > s.Sequence+1 || b.ClosedSequence != nil && (*b.ClosedSequence < b.FirstSequence-1 || *b.ClosedSequence > s.Sequence) {
			return false
		}
		ids[id] = true
		targets[target] = true
		if b.ClosedSequence == nil {
			if addresses[b.Address] {
				return false
			}
			addresses[b.Address] = true
			active = append(active, b.Route)
		}
	}
	fingerprint, err := ingress.RouteFingerprint(active)
	return err == nil && fingerprint == s.Fingerprint
}
func (l *RouteLedger) read(dir int) (routeDisk, error) {
	fd, err := unix.Openat(dir, "routes.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return routeDisk{}, ErrRouteState
	}
	f := os.NewFile(uintptr(fd), "routes.json")
	defer f.Close()
	if !secureNoticeFile(fd, false) {
		return routeDisk{}, ErrRouteState
	}
	raw, err := io.ReadAll(io.LimitReader(f, routeMax+1))
	if err != nil || len(raw) == 0 || len(raw) > routeMax {
		return routeDisk{}, ErrRouteState
	}
	var disk routeDisk
	if err = json.Unmarshal(raw, &disk); err != nil || len(disk.Domains) != len(l.domains) || disk.ForwardDomain != l.forwardDomain {
		return routeDisk{}, ErrRouteState
	}
	canonical, err := json.Marshal(disk)
	if err != nil || !bytes.Equal(raw, canonical) {
		return routeDisk{}, ErrRouteState
	}
	for i := range l.domains {
		if disk.Domains[i] != l.domains[i] {
			return routeDisk{}, ErrRouteState
		}
	}
	if !l.validDisk(disk) {
		return routeDisk{}, ErrRouteState
	}
	return disk, nil
}
func (l *RouteLedger) write(dir int, disk routeDisk) error {
	if !l.validDisk(disk) {
		return ErrRouteState
	}
	raw, err := json.Marshal(disk)
	if err != nil || len(raw) > routeMax {
		return ErrRouteState
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return ErrRouteState
	}
	name := ".routes-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrRouteState
	}
	defer unix.Unlinkat(dir, name, 0)
	f := os.NewFile(uintptr(fd), name)
	if n, e := f.Write(raw); e != nil || n != len(raw) || f.Sync() != nil {
		f.Close()
		return ErrRouteState
	}
	if f.Close() != nil {
		return ErrRouteState
	}
	if unix.Renameat(dir, name, dir, "routes.json") != nil || unix.Fsync(dir) != nil {
		return ErrRouteUnknown
	}
	return nil
}
func (l *RouteLedger) Read() (ingress.RouteState, error) {
	disk, err := l.locked(func(_ int, disk routeDisk) (routeDisk, error) { return disk, nil })
	return disk.State, err
}

// Replace uses the same revision, fingerprint and closed-binding semantics as
// the Worker source. A retry of the last committed operation is idempotent.
func (l *RouteLedger) Replace(expected int64, operation string, routes []ingress.Route) (ingress.RouteState, error) {
	if expected < 0 || expected >= routeMaxInteger || !identifier.MatchString(operation) || len(routes) > 5000 {
		return ingress.RouteState{}, ErrConfiguration
	}
	for _, route := range routes {
		if !l.allowed(route) {
			return ingress.RouteState{}, ErrConfiguration
		}
	}
	fingerprint, err := ingress.RouteFingerprint(routes)
	if err != nil {
		return ingress.RouteState{}, ErrConfiguration
	}
	disk, err := l.locked(func(dir int, disk routeDisk) (routeDisk, error) {
		state := disk.State
		if state.OperationID == operation && state.Revision == expected+1 && state.Fingerprint == fingerprint {
			// A previous rename may have succeeded even if its directory sync
			// failed. Confirm the directory before reporting an idempotent retry.
			if unix.Fsync(dir) != nil {
				return routeDisk{}, ErrRouteUnknown
			}
			return disk, nil
		}
		if state.Revision != expected || state.OperationID == operation {
			return routeDisk{}, ErrRouteConflict
		}
		desired := make(map[string]ingress.Route, len(routes))
		for _, route := range routes {
			id := store.IngressBinding{SourceID: l.source, Generation: l.generation, Address: route.Address, ClaimVersion: route.ClaimVersion, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor}.Key()
			desired[id] = route
		}
		for i := range state.Bindings {
			b := &state.Bindings[i]
			_, present := desired[b.ID]
			if b.ClosedSequence == nil && !present {
				sequence := state.Sequence
				b.ClosedSequence = &sequence
			}
			if b.ClosedSequence != nil && present {
				return routeDisk{}, ErrRouteConflict
			}
			delete(desired, b.ID)
		}
		for _, route := range routes {
			id := store.IngressBinding{SourceID: l.source, Generation: l.generation, Address: route.Address, ClaimVersion: route.ClaimVersion, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor}.Key()
			if _, present := desired[id]; present {
				state.Bindings = append(state.Bindings, ingress.RouteBinding{Route: route, ID: id, FirstSequence: state.Sequence + 1})
			}
		}
		if len(state.Bindings) > 5000 {
			return routeDisk{}, ErrRouteState
		}
		state.Revision++
		state.OperationID = operation
		state.Fingerprint = fingerprint
		disk.State = state
		if err := l.write(dir, disk); err != nil {
			return routeDisk{}, err
		}
		return disk, nil
	})
	return disk.State, err
}
