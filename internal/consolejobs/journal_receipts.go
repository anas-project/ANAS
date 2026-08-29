package consolejobs

import (
	"os"
	"sync"
)

// journalAppendReceiptsLimit bounds the process-local optimization state. A
// Store that falls farther behind simply performs a full journal recovery; a
// missing receipt must never weaken integrity checks.
const journalAppendReceiptsLimit = 1024

type journalSnapshotKey struct {
	completeBytes int64
	modSeconds    int64
	modNanos      int32
	changeSeconds int64
	changeNanos   int64
	changeKnown   bool
}

func (snapshot journalSnapshot) key() journalSnapshotKey {
	return journalSnapshotKey{
		completeBytes: snapshot.completeBytes,
		modSeconds:    snapshot.modTime.Unix(),
		modNanos:      int32(snapshot.modTime.Nanosecond()),
		changeSeconds: snapshot.changeSeconds,
		changeNanos:   snapshot.changeNanos,
		changeKnown:   snapshot.changeKnown,
	}
}

func (snapshot journalSnapshot) equal(other journalSnapshot) bool {
	return snapshot.key() == other.key()
}

type journalAppendReceipt struct {
	after  journalSnapshot
	serial uint64
}

type journalAppendReceiptIndex struct {
	before journalSnapshotKey
	serial uint64
}

type journalReceiptLog struct {
	mu         sync.Mutex
	receipts   map[journalSnapshotKey]journalAppendReceipt
	order      [journalAppendReceiptsLimit]journalAppendReceiptIndex
	next       int
	count      int
	nextSerial uint64
}

func newJournalReceiptLog() *journalReceiptLog {
	return &journalReceiptLog{receipts: make(map[journalSnapshotKey]journalAppendReceipt)}
}

// record publishes a receipt only after the caller has durably appended and
// taken the final journal snapshot. Receipts are performance hints shared by
// cooperating Store instances in this process, not durable or cryptographic
// authority. An arbitrary same-privilege writer that races the advisory lock
// cannot be authenticated without rereading trusted bytes or external state.
func (log *journalReceiptLog) record(before, after journalSnapshot) {
	if log == nil || after.completeBytes <= before.completeBytes {
		return
	}
	log.mu.Lock()
	defer log.mu.Unlock()

	log.nextSerial++
	if log.nextSerial == 0 {
		// Serial wrap is practically unreachable. Resetting drops every hint and
		// therefore causes conservative full recovery rather than ambiguity.
		clear(log.receipts)
		log.order = [journalAppendReceiptsLimit]journalAppendReceiptIndex{}
		log.next = 0
		log.count = 0
		log.nextSerial = 1
	}
	if log.count == journalAppendReceiptsLimit {
		evicted := log.order[log.next]
		if current, exists := log.receipts[evicted.before]; exists && current.serial == evicted.serial {
			delete(log.receipts, evicted.before)
		}
	} else {
		log.count++
	}

	beforeKey := before.key()
	receipt := journalAppendReceipt{after: after, serial: log.nextSerial}
	log.receipts[beforeKey] = receipt
	log.order[log.next] = journalAppendReceiptIndex{before: beforeKey, serial: receipt.serial}
	log.next = (log.next + 1) % journalAppendReceiptsLimit
}

// confirmsAppendChain reports whether every byte-range transition from before
// to after was published by a cooperating Store in this process. If any link
// has expired or is absent, callers must fully recover the journal.
func (log *journalReceiptLog) confirmsAppendChain(before, after journalSnapshot) bool {
	if log == nil || after.completeBytes <= before.completeBytes {
		return false
	}
	log.mu.Lock()
	defer log.mu.Unlock()

	current := before
	for steps := 0; steps < journalAppendReceiptsLimit; steps++ {
		receipt, exists := log.receipts[current.key()]
		if !exists || receipt.after.completeBytes <= current.completeBytes || receipt.after.completeBytes > after.completeBytes {
			return false
		}
		current = receipt.after
		if current.equal(after) {
			return true
		}
	}
	return false
}

type journalReceiptCoordinator struct {
	storeID string
	file    os.FileInfo
	refs    int
	log     *journalReceiptLog
}

type journalReceiptHandle struct {
	path        string
	coordinator *journalReceiptCoordinator
	once        sync.Once
}

var journalReceiptRegistry = struct {
	sync.Mutex
	byPath map[string][]*journalReceiptCoordinator
}{byPath: make(map[string][]*journalReceiptCoordinator)}

// registerJournalReceiptHandle binds receipt sharing to both the durable store
// identity and the opened file identity (os.SameFile compares device/inode on
// supported platforms). Reusing a path for a different journal cannot inherit
// receipts from the old file generation.
func registerJournalReceiptHandle(path, storeID string, file os.FileInfo) *journalReceiptHandle {
	journalReceiptRegistry.Lock()
	defer journalReceiptRegistry.Unlock()

	coordinators := journalReceiptRegistry.byPath[path]
	for _, coordinator := range coordinators {
		if coordinator.storeID == storeID && os.SameFile(coordinator.file, file) {
			coordinator.refs++
			return &journalReceiptHandle{path: path, coordinator: coordinator}
		}
	}
	coordinator := &journalReceiptCoordinator{
		storeID: storeID,
		file:    file,
		refs:    1,
		log:     newJournalReceiptLog(),
	}
	journalReceiptRegistry.byPath[path] = append(coordinators, coordinator)
	return &journalReceiptHandle{path: path, coordinator: coordinator}
}

func (handle *journalReceiptHandle) record(before, after journalSnapshot) {
	if handle != nil && handle.coordinator != nil {
		handle.coordinator.log.record(before, after)
	}
}

func (handle *journalReceiptHandle) confirmsAppendChain(before, after journalSnapshot) bool {
	return handle != nil && handle.coordinator != nil && handle.coordinator.log.confirmsAppendChain(before, after)
}

func (handle *journalReceiptHandle) close() {
	if handle == nil {
		return
	}
	handle.once.Do(func() {
		journalReceiptRegistry.Lock()
		defer journalReceiptRegistry.Unlock()

		coordinator := handle.coordinator
		if coordinator == nil {
			return
		}
		coordinator.refs--
		if coordinator.refs > 0 {
			return
		}
		coordinators := journalReceiptRegistry.byPath[handle.path]
		for index, candidate := range coordinators {
			if candidate != coordinator {
				continue
			}
			coordinators = append(coordinators[:index], coordinators[index+1:]...)
			break
		}
		if len(coordinators) == 0 {
			delete(journalReceiptRegistry.byPath, handle.path)
		} else {
			journalReceiptRegistry.byPath[handle.path] = coordinators
		}
	})
}
