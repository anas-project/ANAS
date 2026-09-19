package hostaction

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
)

// PeerIdentity is output from the kernel-authenticated invocation, not a DTO
// accepted from a client. Its PID is attribution, not a reusable process handle.
type PeerIdentity struct {
	PID      int32
	UID, GID uint32
}

// JobBinding is an execution-owner dependency. WithHostInvocation must match
// the exact running job and frozen release, retain its existing execution lease,
// and reauthorize its persisted actor before calling run ONCE synchronously.
// A root launcher must use an authenticated broker adapter, not open a job
// journal in a user-writable path. No such broker is implicitly installed here.
type JobBinding interface {
	WithHostInvocation(context.Context, actionabi.Request, ReleaseIdentity, PeerIdentity, func(context.Context) error) error
}

func rejectAdmission(ctx context.Context, journal AuditJournal, peer Peer, reason string, cause error) error {
	if journal == nil {
		return cause
	} // Uninstalled low-level Receive only.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	event := audit.Event{Type: "host_action_rejected", Outcome: "denied", Details: map[string]any{"reason": reason}}
	if peer.pid > 0 {
		event.Actor = fmt.Sprintf("uid:%d", peer.uid)
		event.Details["peer_uid"], event.Details["peer_gid"], event.Details["peer_pid"] = peer.uid, peer.gid, peer.pid
	}
	if _, err := journal.AppendContext(finish, event); err != nil {
		return ErrAudit
	}
	return cause
}

// executeBound is the mandatory job-bound variant for activation. A claimed
// job id alone never authorizes execution. check pins the installed policy and
// socket throughout admission; it grants no handler or shell execution ability.
func executeBound(ctx context.Context, call *Invocation, journal AuditJournal, binding JobBinding, release ReleaseIdentity, check func() error, progress ...func(actionabi.Event) error) (actionabi.Event, error) {
	if ctx == nil || call == nil || journal == nil || binding == nil || check == nil || release.Validate() != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	if !call.peer.verified || check() != nil {
		return actionabi.Event{}, rejectAdmission(ctx, journal, call.peer, "installation_changed", ErrDenied)
	}
	wire, err := actionabi.EncodeRequest(call.request)
	if err != nil {
		return actionabi.Event{}, ErrRequest
	}
	detached, err := actionabi.DecodeRequest(wire)
	if err != nil {
		return actionabi.Event{}, ErrRequest
	}
	var event actionabi.Event
	var executionErr error
	invoked := false
	duplicate := false
	finished := false
	var gate sync.Mutex
	err = binding.WithHostInvocation(ctx, detached, release, PeerIdentity{call.peer.pid, call.peer.uid, call.peer.gid}, func(owner context.Context) error {
		gate.Lock()
		defer gate.Unlock()
		if finished {
			return ErrDenied
		}
		if invoked {
			duplicate = true
			return ErrDenied
		}
		if owner == nil || check() != nil {
			return ErrDenied
		}
		invoked = true
		if spec, ok := LookupAction(call.request.Action); ok && spec.Mutating && len(progress) == 1 && progress[0] != nil {
			frame := actionabi.Event{ABI: actionabi.Version, JobID: call.request.JobID, InvocationID: call.request.InvocationID,
				Type: "progress", Progress: &actionabi.Progress{Phase: string(spec.Phase)}}
			if err := progress[0](frame); err != nil {
				executionErr = ErrUnavailable
				return executionErr
			}
		}
		if call.request.Action == ActionStatus {
			event, executionErr = executePreflight(owner, call, journal)
		} else {
			event, executionErr = executeIncusProvision(owner, call, journal, release)
		}
		return executionErr
	})
	gate.Lock()
	defer gate.Unlock()
	finished = true
	if !invoked {
		return actionabi.Event{}, rejectAdmission(ctx, journal, call.peer, "job_binding_denied", ErrDenied)
	}
	if err != nil || executionErr != nil || duplicate || check() != nil {
		// Never return a provisional success after the execution owner loses
		// its binding, even if the read-only handler itself completed.
		return actionabi.Event{}, ErrUnavailable
	}
	return event, nil
}
