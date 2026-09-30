//go:build linux

package hostaction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func testFileLedger(t *testing.T) (*fileLedger, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "invocations")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := &fileLedger{dir: handle, now: time.Now, root: uint32(os.Geteuid())}
	t.Cleanup(func() { _ = l.close() })
	return l, dir
}

func ledgerRequest(id string) actionabi.Request {
	return actionabi.Request{ABI: actionabi.Version, JobID: "job-" + id[:4], InvocationID: id, Action: ActionStatus, Parameters: []byte("{}")}
}

func ledgerState(t *testing.T, l *fileLedger, r actionabi.Request) InvocationStatus {
	t.Helper()
	status, err := l.Status(context.Background(), r.JobID, r.InvocationID)
	if err != nil || status.Validate(r.JobID, r.InvocationID) != nil {
		t.Fatal(status, err)
	}
	return status
}

func TestFileLedgerRecordsOneInvocationAndItsTerminal(t *testing.T) {
	l, dir := testFileLedger(t)
	r := ledgerRequest(strings.Repeat("a", 32))
	if s := ledgerState(t, l, r); s.State != InvocationAbsent {
		t.Fatal("unknown invocation reported", s.State)
	}
	entry, err := l.Begin(context.Background(), r, installedRelease(), PeerIdentity{PID: 99})
	if err != nil {
		t.Fatal(err)
	}
	if s := ledgerState(t, l, r); s.State != InvocationRunning {
		t.Fatal("held invocation not running", s.State)
	}
	if _, err := l.Begin(context.Background(), r, installedRelease(), PeerIdentity{PID: 99}); err != ErrDenied {
		t.Fatal("replayed invocation began twice", err)
	}
	changed := false
	terminal := actionabi.Event{ABI: actionabi.Version, JobID: r.JobID, InvocationID: r.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: []byte("{}")}}
	if err := entry.Finish(terminal); err != nil {
		t.Fatal(err)
	}
	s := ledgerState(t, l, r)
	if s.State != InvocationFinished || s.Terminal == nil || s.Terminal.Result == nil || s.Terminal.Result.Outcome != actionabi.Succeeded {
		t.Fatal("terminal not recorded", s)
	}
	if err := entry.Finish(terminal); err == nil {
		t.Fatal("second terminal accepted")
	}
	if _, err := l.Status(context.Background(), "other-job", r.InvocationID); err == nil {
		t.Fatal("record answered for another job")
	}
	if info, err := os.Stat(filepath.Join(dir, r.InvocationID+".json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("record is not private", err)
	}
}

func TestFileLedgerReportsAnExecutorThatDiedAsLost(t *testing.T) {
	l, _ := testFileLedger(t)
	r := ledgerRequest(strings.Repeat("b", 32))
	entry, err := l.Begin(context.Background(), r, installedRelease(), PeerIdentity{PID: 99})
	if err != nil {
		t.Fatal(err)
	}
	// The kernel releases the lock when the executor dies without Finish.
	if err := entry.(*fileLedgerEntry).lock.Close(); err != nil {
		t.Fatal(err)
	}
	if s := ledgerState(t, l, r); s.State != InvocationLost {
		t.Fatal("dead executor not reported lost", s.State)
	}
}

func TestFileLedgerPrunesOnlyExpiredFreeRecords(t *testing.T) {
	l, dir := testFileLedger(t)
	old := ledgerRequest(strings.Repeat("c", 32))
	live := ledgerRequest(strings.Repeat("d", 32))
	entry, err := l.Begin(context.Background(), old, installedRelease(), PeerIdentity{PID: 99})
	if err != nil {
		t.Fatal(err)
	}
	if err := entry.(*fileLedgerEntry).lock.Close(); err != nil {
		t.Fatal(err)
	}
	held, err := l.Begin(context.Background(), live, installedRelease(), PeerIdentity{PID: 99})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * invocationRetention)
	for _, id := range []string{old.InvocationID, live.InvocationID} {
		if err := os.Chtimes(filepath.Join(dir, id+".lock"), past, past); err != nil {
			t.Fatal(err)
		}
	}
	l.prune()
	if _, err := os.Stat(filepath.Join(dir, old.InvocationID+".lock")); !os.IsNotExist(err) {
		t.Fatal("expired free record kept", err)
	}
	if s := ledgerState(t, l, live); s.State != InvocationRunning {
		t.Fatal("pruning removed a running invocation", s.State)
	}
	_ = held.(*fileLedgerEntry).lock.Close()
}
