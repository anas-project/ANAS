package hostaction

import (
	"context"
	"errors"
	"fmt"
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

// executeRecorded runs one compiled action under hostd's own invocation
// record. The record is created before any effect, so a replayed invocation id
// is refused, and it keeps the terminal event after the caller has gone. check
// pins the installed policy and socket throughout; it grants no handler or
// shell execution ability.
func executeRecorded(ctx context.Context, call *Invocation, journal AuditJournal, ledger InvocationLedger, release ReleaseIdentity, check func() error, progress func(actionabi.Event) error) (actionabi.Event, error) {
	if ctx == nil || call == nil || journal == nil || ledger == nil || check == nil || progress == nil || release.Validate() != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	if !call.peer.verified || check() != nil {
		return actionabi.Event{}, rejectAdmission(ctx, journal, call.peer, "installation_changed", ErrDenied)
	}
	entry, err := ledger.Begin(ctx, call.request, release, PeerIdentity{call.peer.pid, call.peer.uid, call.peer.gid})
	if errors.Is(err, ErrDenied) {
		return actionabi.Event{}, rejectAdmission(ctx, journal, call.peer, "invocation_replayed", ErrDenied)
	}
	if err != nil {
		return actionabi.Event{}, rejectAdmission(ctx, journal, call.peer, "invocation_record_unavailable", ErrUnavailable)
	}
	notStarted := actionabi.Event{ABI: actionabi.Version, JobID: call.request.JobID, InvocationID: call.request.InvocationID, Type: "error",
		Error: &actionabi.Failure{Outcome: actionabi.Failed, Code: "host_action_not_started", Message: "Host action was not started"}}
	if check() != nil {
		return actionabi.Event{}, errors.Join(ErrDenied, entry.Finish(notStarted))
	}
	if spec, ok := LookupAction(call.request.Action); ok && spec.Mutating {
		frame := actionabi.Event{ABI: actionabi.Version, JobID: call.request.JobID, InvocationID: call.request.InvocationID,
			Type: "progress", Progress: &actionabi.Progress{Phase: string(spec.Phase)}}
		if err := progress(frame); err != nil {
			return actionabi.Event{}, errors.Join(ErrUnavailable, entry.Finish(notStarted))
		}
	}
	var event actionabi.Event
	var executionErr error
	if call.request.Action == ActionStatus {
		event, executionErr = executePreflight(ctx, call, journal)
	} else {
		event, executionErr = executeIncusProvision(ctx, call, journal, release)
	}
	terminal := event
	if terminal.Type == "" {
		terminal = notStarted
	}
	if err := entry.Finish(terminal); err != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	if executionErr != nil {
		return actionabi.Event{}, executionErr
	}
	return event, nil
}
