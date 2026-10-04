package computeingress

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrRequestWriterUnavailable = errors.New("HTTP request directory is unavailable or unsupported")
	ErrRequestConflict          = errors.New("HTTP request slot is owned by a different request")
	ErrRequestReceiptStale      = errors.New("HTTP request receipt no longer identifies the submitted file")
	ErrRequestCommitUncertain   = errors.New("HTTP request change may be visible but its durable commit could not be confirmed")
)

const (
	requestWriteTimeout      = 5 * time.Second
	maxRequestWriterReceipts = 256
)

// RequestReceipt acknowledges a FILE submission, never a running backend or a
// loaded Traefik route. It is opaque, local to its writer, and deliberately not
// serializable as authority. A retained descriptor prevents inode reuse from
// letting an old receipt withdraw a new request using the same slot name.
type RequestReceipt struct {
	writer  *RequestWriter
	name    string
	request Request
	file    *os.File
	retired bool
}

// RequestWriter is a consumer convenience, not the publication security
// boundary. The independently privileged mediator still checks the registered
// mount, active deployment, frozen authorization and fresh instance facts.
// Close releases local handles; it does NOT retract durable requests.
type RequestWriter struct {
	mu        sync.Mutex
	directory requestDirectory
	active    map[string]*RequestReceipt
	closed    bool
}

type requestDirectory interface {
	list() ([]string, error)
	lock(context.Context) (func() error, error)
	read(string) (*os.File, Request, error)
	publish(context.Context, string, Request) (*os.File, error)
	remove(context.Context, string, *os.File, Request) error
	close() error
}

// OpenRequestWriter opens an EXISTING, private, installation-provided lease
// directory. It never creates a registry, chooses a lease, or mounts anything.
// Only supported local Linux filesystems are accepted by the implementation.
func OpenRequestWriter(path string) (*RequestWriter, error) {
	directory, err := openRequestDirectory(path)
	if err != nil {
		return nil, err
	}
	return &RequestWriter{directory: directory, active: make(map[string]*RequestReceipt)}, nil
}

// Submit atomically publishes one complete, bounded publish request. Retries of
// the same request are idempotent. A different address/label cannot overwrite
// a live slot: withdraw the old receipt first. No consumer-supplied filename,
// URL, auth, host port, middleware or entrypoint is accepted.
func (w *RequestWriter) Submit(ctx context.Context, request Request) (*RequestReceipt, error) {
	return w.receipt(ctx, request, true)
}

// Resume adopts an existing exact request after consumer restart without
// creating, changing or re-publishing a missing request. The caller supplies
// its persisted expected workload/instance/port, not data from another lease.
// The resulting fresh inode receipt may then be withdrawn even after the guest
// has disappeared. This is request-file recovery, not external host recovery.
func (w *RequestWriter) Resume(ctx context.Context, expected Request) (*RequestReceipt, error) {
	return w.receipt(ctx, expected, false)
}

func (w *RequestWriter) receipt(ctx context.Context, request Request, create bool) (*RequestReceipt, error) {
	if w == nil || ctx == nil {
		return nil, ErrRequestWriterUnavailable
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.directory == nil || w.active == nil {
		return nil, ErrRequestWriterUnavailable
	}
	name := requestSlotName(request)
	if w.active[name] == nil && len(w.active) >= maxRequestWriterReceipts {
		return nil, ErrRequestWriterUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, requestWriteTimeout)
	defer cancel()
	unlock, err := w.directory.lock(ctx)
	if err != nil {
		return nil, err
	}
	file, current, err := w.directory.read(name)
	if errors.Is(err, os.ErrNotExist) && create {
		file, err = w.directory.publish(ctx, name, request)
	} else if err == nil && current != request {
		_ = file.Close()
		file, err = nil, ErrRequestConflict
	}
	unlockErr := unlock()
	if err != nil || unlockErr != nil {
		if file != nil {
			_ = file.Close()
		}
		return nil, errors.Join(err, unlockErr)
	}
	if old := w.active[name]; old != nil {
		before, beforeErr := old.file.Stat()
		after, afterErr := file.Stat()
		if beforeErr == nil && afterErr == nil && os.SameFile(before, after) {
			_ = file.Close()
			return old, nil
		}
		// A cooperating writer may have withdrawn and recreated this slot.
		// Retire the local receipt rather than let it adopt the new inode.
		old.retired = true
		_ = old.file.Close()
	}
	receipt := &RequestReceipt{writer: w, name: name, request: request, file: file}
	w.active[name] = receipt
	return receipt, nil
}

// Withdraw removes only the exact request acknowledged by this receipt. The
// absence is a revoke intent consumed by periodic reconciliation; it does NOT
// mean that routes, permissions, connections or address reservations are gone.
// This avoids accumulating one revoke file for every completed ephemeral job.
func (w *RequestWriter) Withdraw(ctx context.Context, receipt *RequestReceipt) error {
	if w == nil || ctx == nil || receipt == nil || receipt.writer != w {
		return ErrRequestReceiptStale
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrRequestWriterUnavailable
	}
	if receipt.retired {
		return ErrRequestReceiptStale
	}
	if receipt.file == nil {
		return nil // A successfully withdrawn receipt is idempotent.
	}
	ctx, cancel := context.WithTimeout(ctx, requestWriteTimeout)
	defer cancel()
	unlock, err := w.directory.lock(ctx)
	if err != nil {
		return err
	}
	err = w.directory.remove(ctx, receipt.name, receipt.file, receipt.request)
	err = errors.Join(err, unlock())
	if err != nil {
		return err
	}
	_ = receipt.file.Close()
	receipt.file = nil
	if w.active[receipt.name] == receipt {
		delete(w.active, receipt.name)
	}
	return nil
}

// Sweep removes every request file for which keep reports false, including
// files another writer or an earlier process left. Each removal checks the
// file it read is still the one in place.
func (w *RequestWriter) Sweep(ctx context.Context, keep func(Request) bool) (int, error) {
	if w == nil || ctx == nil || keep == nil {
		return 0, ErrRequestWriterUnavailable
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.directory == nil {
		return 0, ErrRequestWriterUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, requestWriteTimeout)
	defer cancel()
	unlock, err := w.directory.lock(ctx)
	if err != nil {
		return 0, err
	}
	names, err := w.directory.list()
	removed := 0
	for _, name := range names {
		if err != nil {
			break
		}
		file, request, readErr := w.directory.read(name)
		if readErr != nil {
			continue
		}
		if keep(request) {
			_ = file.Close()
			continue
		}
		err = w.directory.remove(ctx, name, file, request)
		_ = file.Close()
		if err == nil {
			removed++
			if receipt := w.active[name]; receipt != nil {
				receipt.retired = true
				if receipt.file != nil {
					_ = receipt.file.Close()
					receipt.file = nil
				}
				delete(w.active, name)
			}
		}
	}
	return removed, errors.Join(err, unlock())
}

func (w *RequestWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var result error
	for _, receipt := range w.active {
		if receipt.file != nil {
			result = errors.Join(result, receipt.file.Close())
			receipt.file = nil
		}
	}
	if w.directory == nil {
		return result
	}
	return errors.Join(result, w.directory.close())
}

func requestSlotName(request Request) string {
	body, _ := json.Marshal(struct {
		Instance string `json:"instance"`
		Port     uint16 `json:"port"`
	}{request.Instance, request.Port})
	digest := sha256.Sum256(body)
	// Full 256-bit digest, 57-character basename: within ReadRequest's limit.
	return "http-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:])) + ".json"
}
