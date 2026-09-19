package hostaction

import (
	"context"
	"regexp"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

// These values are read from the authenticated system service manager, never
// from executor JSON. In particular an executor cannot claim its own exit code.
type systemdSnapshot struct {
	ID, Invocation, LoadState, Active, Sub, Fragment string
	User, Group, Type, Restart, KillMode, Result     string
	MainPID, ExecPID, ControlPID, Restarts           uint32
	Start, Stop                                      uint64
	Code, Status                                     int32
	Transient, DynamicUser, Delegate                 bool
	DropIns                                          []string
}

type systemdExitSource interface {
	snapshot(context.Context) (systemdSnapshot, error)
	empty(context.Context, string) (bool, error)
	close() error
}

var hostUnitName = regexp.MustCompile(`^anas-hostd@[A-Za-z0-9_.:\\-]{1,180}\.service$`)
var systemdInvocation = regexp.MustCompile(`^[0-9a-f]{32}$`)

type systemdExitWatch struct {
	source systemdExitSource
	first  systemdSnapshot
}

// An anasd restart is permitted: its new main process must be the actual peer
// of this connection and stay in one manager invocation throughout admission.
// The short-lived host executor has a stricter no-restart rule below.
func validInstalledServiceSnapshot(s systemdSnapshot, peer Peer, unit string) bool {
	return peer.pid > 1 && peer.uid == 0 && peer.gid == 0 && installedServiceUnit.MatchString(unit) &&
		s.ID == unit && s.LoadState == "loaded" && s.Fragment == "/etc/systemd/system/"+unit && len(s.DropIns) == 0 &&
		!s.Transient && !s.DynamicUser && !s.Delegate && s.User == "root" && s.Group == "root" &&
		(s.Active == "active" || s.Active == "activating") && s.MainPID == uint32(peer.pid) &&
		s.ExecPID == uint32(peer.pid) && s.Start != 0 && s.Stop == 0 &&
		systemdInvocation.MatchString(s.Invocation) && s.Invocation != "00000000000000000000000000000000"
}

func validHostUnit(s systemdSnapshot) bool {
	return hostUnitName.MatchString(s.ID) && systemdInvocation.MatchString(s.Invocation) &&
		s.Invocation != "00000000000000000000000000000000" && s.LoadState == "loaded" &&
		s.Fragment == "/etc/systemd/system/anas-hostd@.service" && len(s.DropIns) == 0 && !s.Transient &&
		s.User == "root" && s.Group == "root" && !s.DynamicUser && !s.Delegate &&
		s.Type == "exec" && s.Restart == "no" && s.Restarts == 0 && s.KillMode == "control-group"
}

func bindSystemdExit(ctx context.Context, peer Peer, process brokerProcess, source systemdExitSource) (*systemdExitWatch, error) {
	if ctx == nil || peer.pid <= 1 || peer.uid != 0 || peer.gid != 0 || !peer.verified || process == nil || source == nil {
		return nil, ErrUnavailable
	}
	if process.alive() != nil {
		return nil, ErrUnavailable
	}
	first, err := source.snapshot(ctx)
	if err != nil || !validHostUnit(first) || first.MainPID != uint32(peer.pid) || first.ExecPID != uint32(peer.pid) ||
		first.ControlPID != 0 || first.Start == 0 || first.Stop != 0 || first.Code != 0 ||
		(first.Active != "active" && first.Active != "activating") || process.alive() != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	return &systemdExitWatch{source: source, first: first}, nil
}

func (w *systemdExitWatch) matches(s systemdSnapshot) bool {
	return validHostUnit(s) && s.ID == w.first.ID && s.Invocation == w.first.Invocation &&
		s.ExecPID == w.first.ExecPID && s.Start == w.first.Start
}

// wait requires BOTH the socket-bound process to exit and the same systemd
// invocation to finish reaping/cleanup. A pidfd notification alone is not wait
// status. Ref/Unref keeps the manager's record alive without restarting a unit.
func (w *systemdExitWatch) wait(ctx context.Context, process brokerProcess) (actionabi.ExitState, error) {
	if w == nil || w.source == nil || ctx == nil || process == nil {
		return actionabi.ExitState{}, ErrUnavailable
	}
	if process.waitExited(ctx) != nil {
		return actionabi.ExitState{}, ErrBrokerExecutorRunning
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s, err := w.source.snapshot(ctx)
		if err != nil || !w.matches(s) {
			return actionabi.ExitState{}, ErrUnavailable
		}
		terminal := (s.Active == "inactive" && s.Sub == "dead") || (s.Active == "failed" && s.Sub == "failed")
		if terminal && s.MainPID == 0 && s.ControlPID == 0 && s.Stop >= s.Start && s.Stop != 0 && s.Code != 0 {
			empty, err := w.source.empty(ctx, s.ID)
			if err != nil {
				return actionabi.ExitState{}, ErrUnavailable
			}
			if empty {
				// Re-read after the process inventory to reject restart/reset races.
				again, err := w.source.snapshot(ctx)
				if err != nil || !w.matches(again) || again.MainPID != 0 || again.ControlPID != 0 ||
					again.Active != s.Active || again.Sub != s.Sub || again.Stop != s.Stop || again.Code != s.Code ||
					again.Status != s.Status || again.Result != s.Result || ctx.Err() != nil {
					return actionabi.ExitState{}, ErrUnavailable
				}
				exit := actionabi.ExitState{ProcessExited: true, ExitCode: -1, Forced: true}
				// CLD_EXITED is 1 on Linux. Signals, timeout, watchdog and OOM are
				// not cooperative cancellation and cannot validate success frames.
				if s.Code == 1 && s.Status >= 0 && s.Status <= 255 &&
					((s.Status == 0 && s.Result == "success") || (s.Status > 0 && s.Result == "exit-code")) {
					exit.Forced, exit.ExitCode = false, int(s.Status)
				}
				return exit, nil
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	return actionabi.ExitState{}, ErrBrokerExecutorRunning
}
