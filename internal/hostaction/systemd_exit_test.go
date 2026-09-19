package hostaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

type exitSourceFixture struct {
	value                       systemdSnapshot
	readErr, emptyErr, closeErr error
	populated, closed           bool
	afterEmpty                  func()
}

func (f *exitSourceFixture) snapshot(context.Context) (systemdSnapshot, error) {
	return f.value, f.readErr
}
func (f *exitSourceFixture) empty(context.Context, string) (bool, error) {
	if f.afterEmpty != nil {
		f.afterEmpty()
	}
	return !f.populated, f.emptyErr
}
func (f *exitSourceFixture) close() error { f.closed = true; return f.closeErr }
func liveSystemdFixture() systemdSnapshot {
	return systemdSnapshot{ID: "anas-hostd@1-local.service", Invocation: strings.Repeat("a", 32), LoadState: "loaded", Active: "active", Sub: "running",
		Fragment: "/etc/systemd/system/anas-hostd@.service", User: "root", Group: "root", Type: "exec", Restart: "no", KillMode: "control-group",
		MainPID: 99, ExecPID: 99, Start: 100, Result: "success"}
}
func finishedSystemdFixture() systemdSnapshot {
	s := liveSystemdFixture()
	s.MainPID = 0
	s.Active = "inactive"
	s.Sub = "dead"
	s.Stop = 200
	s.Code = 1
	return s
}
func TestSystemdExitBindsLiveExactInvocation(t *testing.T) {
	for _, name := range []string{"valid", "PID", "exec PID", "zero start", "already stopped", "invocation", "unit", "fragment", "drop-in", "user", "delegate", "restart", "transient", "unloaded", "dead process", "read failure"} {
		t.Run(name, func(t *testing.T) {
			s := liveSystemdFixture()
			p := &brokerTestProcess{}
			f := &exitSourceFixture{}
			switch name {
			case "PID":
				s.MainPID++
			case "exec PID":
				s.ExecPID++
			case "zero start":
				s.Start = 0
			case "already stopped":
				s.Stop = 200
			case "invocation":
				s.Invocation = strings.Repeat("0", 32)
			case "unit":
				s.ID = "unrelated.service"
			case "fragment":
				s.Fragment = "/tmp/private-marker"
			case "drop-in":
				s.DropIns = []string{"/etc/override.conf"}
			case "user":
				s.User = "someone"
			case "delegate":
				s.Delegate = true
			case "restart":
				s.Restarts = 1
			case "transient":
				s.Transient = true
			case "unloaded":
				s.LoadState = "not-found"
			case "dead process":
				p.ended.Store(true)
			case "read failure":
				f.readErr = errors.New("private-marker")
			}
			f.value = s
			_, err := bindSystemdExit(context.Background(), Peer{pid: 99, verified: true}, p, f)
			if (err == nil) != (name == "valid") {
				t.Fatal(name, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-marker") {
				t.Fatal("raw systemd data leaked")
			}
		})
	}
}
func TestSystemdExitRequiresReapedStatusAndEmptySameUnit(t *testing.T) {
	for _, name := range []string{"success", "exit failure", "signal", "timeout", "default zeros", "remaining process", "restarted", "changed after inventory", "lost manager", "inventory failure"} {
		t.Run(name, func(t *testing.T) {
			f := &exitSourceFixture{value: liveSystemdFixture()}
			p := &brokerTestProcess{}
			w, err := bindSystemdExit(context.Background(), Peer{pid: 99, verified: true}, p, f)
			if err != nil {
				t.Fatal(err)
			}
			p.ended.Store(true)
			f.value = finishedSystemdFixture()
			switch name {
			case "exit failure":
				f.value.Status = 7
				f.value.Result = "exit-code"
				f.value.Active = "failed"
				f.value.Sub = "failed"
			case "signal":
				f.value.Code = 2
				f.value.Status = 9
				f.value.Result = "signal"
			case "timeout":
				f.value.Result = "timeout"
			case "default zeros":
				f.value.Code = 0
				f.value.Stop = 0
			case "remaining process":
				f.populated = true
			case "restarted":
				f.value.Invocation = strings.Repeat("b", 32)
			case "changed after inventory":
				f.afterEmpty = func() { f.value.Start++ }
			case "lost manager":
				f.readErr = errors.New("manager lost")
			case "inventory failure":
				f.emptyErr = errors.New("not an empty group")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
			defer cancel()
			exit, err := w.wait(ctx, p)
			switch name {
			case "success":
				if err != nil || !exit.ProcessExited || exit.Forced || exit.ExitCode != 0 {
					t.Fatal(exit, err)
				}
			case "exit failure":
				if err != nil || exit.Forced || exit.ExitCode != 7 {
					t.Fatal(exit, err)
				}
			case "signal", "timeout":
				if err != nil || !exit.Forced {
					t.Fatal(exit, err)
				}
			default:
				if err == nil || exit.ProcessExited {
					t.Fatal("missing evidence confirmed completion", exit, err)
				}
			}
		})
	}
}
func TestSystemdSessionCloseCannotDiscardMissingManagerEvidence(t *testing.T) {
	p := &brokerTestProcess{}
	p.ended.Store(true)
	f := &exitSourceFixture{value: finishedSystemdFixture()}
	s := &BrokerSession{process: p, granted: true, requireSystemd: true, exitWatch: &systemdExitWatch{source: f, first: liveSystemdFixture()}}
	if !errors.Is(s.Close(), ErrBrokerExecutorRunning) || f.closed {
		t.Fatal("pidfd alone discarded manager reference")
	}
	exit, err := s.ObserveSystemdExit(context.Background())
	if err != nil || exit != (actionabi.ExitState{ProcessExited: true}) {
		t.Fatal(exit, err)
	}
	f.closeErr = errors.New("private close error")
	if !errors.Is(s.Close(), ErrUnavailable) || !errors.Is(s.Close(), ErrUnavailable) {
		t.Fatal("cleanup failure was not sticky")
	}
}

func TestInstalledServiceAdmissionBindsCurrentProcessNotRestartCounter(t *testing.T) {
	for _, name := range []string{"fresh", "legitimately restarted", "foreign unit", "wrong main", "wrong exec", "zero invocation", "stopped", "non-root", "drop-in", "dynamic", "delegated", "foreign fragment"} {
		t.Run(name, func(t *testing.T) {
			s := liveSystemdFixture()
			s.ID = "anasd.service"
			s.Fragment = "/etc/systemd/system/anasd.service"
			s.Type, s.Restart = "simple", "on-failure"
			peer := Peer{pid: 99, uid: 0, gid: 0}
			switch name {
			case "legitimately restarted":
				s.Restarts = 3
			case "foreign unit":
				s.ID = "other.service"
			case "wrong main":
				s.MainPID++
			case "wrong exec":
				s.ExecPID++
			case "zero invocation":
				s.Invocation = strings.Repeat("0", 32)
			case "stopped":
				s.Stop = 200
			case "non-root":
				peer.uid = 1000
			case "drop-in":
				s.DropIns = []string{"override.conf"}
			case "dynamic":
				s.DynamicUser = true
			case "delegated":
				s.Delegate = true
			case "foreign fragment":
				s.Fragment = "/run/systemd/transient/anasd.service"
			}
			want := name == "fresh" || name == "legitimately restarted"
			if got := validInstalledServiceSnapshot(s, peer, "anasd.service"); got != want {
				t.Fatalf("installed service admission = %t, want %t", got, want)
			}
		})
	}
}
