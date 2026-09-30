//go:build linux

package hostaction

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"golang.org/x/sys/unix"
)

var hostLedgerParts = [...]string{"/", "var", "lib", "anas-hostd", "invocations"}

// fileLedger keeps <invocation>.lock (created exclusively, flocked while the
// action runs) and <invocation>.json (replaced atomically) in one root-only
// directory. The descriptor chain is pinned like the audit directory.
type fileLedger struct {
	dir  *os.File
	now  func() time.Time
	root uint32
}

// openInstalledLedger opens /var/lib/anas-hostd/invocations, creating only the
// last component below the service's own 0700 state directory.
func openInstalledLedger() (*fileLedger, error) {
	parent := unix.AT_FDCWD
	var opened []int
	defer func() {
		for _, fd := range opened {
			_ = unix.Close(fd)
		}
	}()
	for i, name := range hostLedgerParts {
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ENOENT) && i == len(hostLedgerParts)-1 {
			if mkErr := unix.Mkdirat(parent, name, 0700); mkErr != nil && !errors.Is(mkErr, unix.EEXIST) {
				return nil, ErrUnavailable
			}
			fd, err = unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if err != nil {
			return nil, ErrUnavailable
		}
		opened = append(opened, fd)
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 || st.Mode&07000 != 0 ||
			(i >= 3 && st.Mode&0777 != 0700) {
			return nil, ErrUnavailable
		}
		parent = fd
	}
	last := opened[len(opened)-1]
	opened = opened[:len(opened)-1]
	return &fileLedger{dir: os.NewFile(uintptr(last), "host-invocation-ledger"), now: time.Now, root: 0}, nil
}

func (l *fileLedger) close() error {
	if l == nil || l.dir == nil {
		return nil
	}
	err := l.dir.Close()
	l.dir = nil
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

type fileLedgerEntry struct {
	ledger *fileLedger
	lock   *os.File
	record invocationRecord
	done   bool
}

func (l *fileLedger) Begin(ctx context.Context, request actionabi.Request, release ReleaseIdentity, peer PeerIdentity) (InvocationEntry, error) {
	if l == nil || l.dir == nil || ctx == nil || ctx.Err() != nil || !invocationName.MatchString(request.InvocationID) || request.JobID == "" {
		return nil, ErrUnavailable
	}
	l.prune()
	dir := int(l.dir.Fd())
	fd, err := unix.Openat(dir, request.InvocationID+".lock", unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if errors.Is(err, unix.EEXIST) {
		return nil, ErrDenied // A replayed invocation never runs twice.
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	lock := os.NewFile(uintptr(fd), "host-invocation-lock")
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		_ = lock.Close()
		return nil, ErrUnavailable
	}
	entry := &fileLedgerEntry{ledger: l, lock: lock, record: invocationRecord{
		Schema: invocationRecordSchema, InvocationID: request.InvocationID, JobID: request.JobID, Action: request.Action,
		ParametersDigest: consolejobs.DigestRequest(request.Parameters), Release: release, Peer: peer, StartedAt: l.now().UTC(),
	}}
	if err := l.write(entry.record); err != nil {
		_ = lock.Close()
		return nil, ErrUnavailable
	}
	return entry, nil
}

// Finish replaces the record with its terminal event before the lock is
// released, so a reader that sees a free lock always sees the terminal too.
func (e *fileLedgerEntry) Finish(terminal actionabi.Event) error {
	if e == nil || e.done || e.lock == nil {
		return ErrUnavailable
	}
	e.done = true
	defer e.lock.Close()
	if terminal.JobID != e.record.JobID || terminal.InvocationID != e.record.InvocationID || (terminal.Type != "result" && terminal.Type != "error") {
		return ErrUnavailable
	}
	if _, err := actionabi.EncodeExecutorEvent(terminal); err != nil {
		return ErrUnavailable
	}
	finished := e.ledger.now().UTC()
	e.record.FinishedAt, e.record.Terminal = &finished, &terminal
	return e.ledger.write(e.record)
}

func (l *fileLedger) write(record invocationRecord) error {
	body, err := json.Marshal(record)
	if err != nil || len(body) > maxInvocationRecord {
		return ErrUnavailable
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return ErrUnavailable
	}
	dir := int(l.dir.Fd())
	temporary := record.InvocationID + ".tmp-" + hex.EncodeToString(suffix[:])
	fd, err := unix.Openat(dir, temporary, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrUnavailable
	}
	file := os.NewFile(uintptr(fd), "host-invocation-record")
	_, writeErr := file.Write(body)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil ||
		unix.Renameat(dir, temporary, dir, record.InvocationID+".json") != nil || unix.Fsync(dir) != nil {
		_ = unix.Unlinkat(dir, temporary, 0)
		return ErrUnavailable
	}
	return nil
}

func (l *fileLedger) Status(ctx context.Context, jobID, invocationID string) (InvocationStatus, error) {
	status := InvocationStatus{Schema: InvocationStatusSchema}
	if l == nil || l.dir == nil || ctx == nil || ctx.Err() != nil || !invocationName.MatchString(invocationID) || jobID == "" {
		return status, ErrUnavailable
	}
	record, found, err := l.read(jobID, invocationID)
	if err != nil {
		return status, err
	}
	if found && record.Terminal != nil {
		status.State, status.Terminal = InvocationFinished, record.Terminal
		return status, nil
	}
	held, exists, err := l.locked(invocationID)
	if err != nil {
		return status, err
	}
	switch {
	case held:
		status.State = InvocationRunning
	case !exists && !found:
		status.State = InvocationAbsent
	default:
		// The lock is free: reread once, because Finish writes the terminal
		// just before it releases the lock.
		record, found, err = l.read(jobID, invocationID)
		if err != nil {
			return status, err
		}
		if found && record.Terminal != nil {
			status.State, status.Terminal = InvocationFinished, record.Terminal
		} else {
			status.State = InvocationLost
		}
	}
	return status, nil
}

func (l *fileLedger) read(jobID, invocationID string) (invocationRecord, bool, error) {
	fd, err := unix.Openat(int(l.dir.Fd()), invocationID+".json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return invocationRecord{}, false, nil
	}
	if err != nil {
		return invocationRecord{}, false, ErrUnavailable
	}
	file := os.NewFile(uintptr(fd), "host-invocation-record")
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != l.root || st.Mode&0777 != 0600 || st.Nlink != 1 ||
		st.Size <= 0 || st.Size > maxInvocationRecord {
		return invocationRecord{}, false, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(file, maxInvocationRecord+1))
	if err != nil {
		return invocationRecord{}, false, ErrUnavailable
	}
	record, err := decodeInvocationRecord(body, jobID, invocationID)
	if err != nil {
		return invocationRecord{}, false, err
	}
	return record, true, nil
}

// locked reports whether the invocation's lock file exists and is held.
func (l *fileLedger) locked(invocationID string) (held, exists bool, err error) {
	fd, openErr := unix.Openat(int(l.dir.Fd()), invocationID+".lock", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(openErr, unix.ENOENT) {
		return false, false, nil
	}
	if openErr != nil {
		return false, false, ErrUnavailable
	}
	defer unix.Close(fd)
	lockErr := unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB)
	if errors.Is(lockErr, unix.EWOULDBLOCK) {
		return true, true, nil
	}
	if lockErr != nil {
		return false, true, ErrUnavailable
	}
	_ = unix.Flock(fd, unix.LOCK_UN)
	return false, true, nil
}

// prune removes finished or lost records past retention, bounded per call.
// A record whose lock is still held is never removed.
func (l *fileLedger) prune() {
	dir := int(l.dir.Fd())
	names, err := readDirNames(dir, 4096)
	if err != nil {
		return
	}
	cutoff := l.now().Add(-invocationRetention)
	removed := 0
	for _, name := range names {
		if removed >= 64 {
			break
		}
		// A temporary record left by an executor that died mid-write.
		if prefix, _, ok := strings.Cut(name, ".tmp-"); ok && invocationName.MatchString(prefix) {
			var st unix.Stat_t
			if unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && time.Unix(st.Mtim.Unix()).Before(cutoff) {
				_ = unix.Unlinkat(dir, name, 0)
				removed++
			}
			continue
		}
		id, ok := strings.CutSuffix(name, ".lock")
		if !ok || !invocationName.MatchString(id) {
			continue
		}
		var st unix.Stat_t
		if unix.Fstatat(dir, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || time.Unix(st.Mtim.Unix()).After(cutoff) {
			continue
		}
		if held, _, err := l.locked(id); err != nil || held {
			continue
		}
		_ = unix.Unlinkat(dir, id+".json", 0)
		_ = unix.Unlinkat(dir, name, 0)
		removed++
	}
}

func readDirNames(dir int, limit int) ([]string, error) {
	fd, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "host-invocation-ledger")
	defer file.Close()
	return file.Readdirnames(limit)
}
