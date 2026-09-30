package hostaction

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/audit"
)

// ActionInvocationStatus reads hostd's own record of an earlier invocation. It
// is the job owner's evidence for an invocation whose stream ended without a
// terminal frame; it replaces the former broker callback and systemd exit
// observation (simplification review item 4, 2026-09-30). The queried job and
// invocation are the request's own ids; parameters are always empty.
const ActionInvocationStatus = "host.invocation.status"

const (
	invocationRecordSchema = "anas.hostd-invocation/v1"
	InvocationStatusSchema = "anas.hostd-invocation-status/v1"
	invocationRetention    = 30 * 24 * time.Hour
	maxInvocationRecord    = 64 << 10
)

// InvocationState is what hostd's record says about one invocation.
type InvocationState string

const (
	InvocationAbsent   InvocationState = "absent"   // never began on this host
	InvocationRunning  InvocationState = "running"  // a live executor holds its lock
	InvocationFinished InvocationState = "finished" // a terminal event was recorded
	InvocationLost     InvocationState = "lost"     // began, no terminal, no live executor
)

type InvocationStatus struct {
	Schema   string           `json:"schema"`
	State    InvocationState  `json:"state"`
	Terminal *actionabi.Event `json:"terminal,omitempty"`
}

func (s InvocationStatus) Validate(jobID, invocationID string) error {
	if s.Schema != InvocationStatusSchema {
		return ErrRequest
	}
	switch s.State {
	case InvocationAbsent, InvocationRunning, InvocationLost:
		if s.Terminal != nil {
			return ErrRequest
		}
	case InvocationFinished:
		t := s.Terminal
		if t == nil || t.JobID != jobID || t.InvocationID != invocationID || (t.Type != "result" && t.Type != "error") {
			return ErrRequest
		}
		if _, err := actionabi.EncodeExecutorEvent(*t); err != nil {
			return ErrRequest
		}
	default:
		return ErrRequest
	}
	return nil
}

// invocationRecord is written only by hostd, below its root-private state
// directory. It holds digests and ids, never parameters or credentials.
type invocationRecord struct {
	Schema           string           `json:"schema"`
	InvocationID     string           `json:"invocation_id"`
	JobID            string           `json:"job_id"`
	Action           string           `json:"action"`
	ParametersDigest string           `json:"parameters_digest"`
	Release          ReleaseIdentity  `json:"release"`
	Peer             PeerIdentity     `json:"peer"`
	StartedAt        time.Time        `json:"started_at"`
	FinishedAt       *time.Time       `json:"finished_at,omitempty"`
	Terminal         *actionabi.Event `json:"terminal,omitempty"`
}

var invocationName = regexp.MustCompile(`^[0-9a-f]{32}$`)

func decodeInvocationRecord(body []byte, jobID, invocationID string) (invocationRecord, error) {
	var record invocationRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if len(body) == 0 || len(body) > maxInvocationRecord || decoder.Decode(&record) != nil || decoder.More() ||
		record.Schema != invocationRecordSchema || record.InvocationID != invocationID || record.JobID != jobID {
		return invocationRecord{}, ErrUnavailable
	}
	if record.Terminal != nil {
		if record.Terminal.JobID != jobID || record.Terminal.InvocationID != invocationID {
			return invocationRecord{}, ErrUnavailable
		}
		if _, err := actionabi.EncodeExecutorEvent(*record.Terminal); err != nil {
			return invocationRecord{}, ErrUnavailable
		}
	}
	return record, nil
}

// InvocationLedger is hostd's durable record of each invocation. Begin refuses
// an invocation id that already began here, so a replayed request never runs
// twice; the entry's lock is held until Finish records the terminal event.
type InvocationLedger interface {
	Begin(context.Context, actionabi.Request, ReleaseIdentity, PeerIdentity) (InvocationEntry, error)
	Status(ctx context.Context, jobID, invocationID string) (InvocationStatus, error)
}

// InvocationEntry records exactly one terminal and then releases its lock.
type InvocationEntry interface {
	Finish(actionabi.Event) error
}

// queryInvocation answers ActionInvocationStatus. It never starts, retries or
// finishes an action; it only reports the ledger.
func queryInvocation(ctx context.Context, call *Invocation, journal AuditJournal, ledger InvocationLedger, check func() error) (actionabi.Event, error) {
	if ctx == nil || call == nil || !call.peer.verified || journal == nil || ledger == nil || check == nil ||
		call.request.Action != ActionInvocationStatus || string(call.request.Parameters) != "{}" {
		return actionabi.Event{}, ErrUnavailable
	}
	if !call.used.CompareAndSwap(false, true) {
		return actionabi.Event{}, ErrRequest
	}
	if check() != nil {
		return actionabi.Event{}, rejectAdmission(ctx, journal, call.peer, "installation_changed", ErrDenied)
	}
	status, err := ledger.Status(ctx, call.request.JobID, call.request.InvocationID)
	if err != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	if _, err := journal.AppendContext(ctx, audit.Event{Type: "host_invocation_status", Actor: "uid:0", Outcome: string(status.State),
		Details: map[string]any{"job_id": call.request.JobID, "invocation_id": call.request.InvocationID, "peer_pid": call.peer.pid}}); err != nil {
		return actionabi.Event{}, ErrAudit
	}
	body, err := json.Marshal(status)
	if err != nil {
		return actionabi.Event{}, ErrUnavailable
	}
	changed := false
	return actionabi.Event{ABI: actionabi.Version, JobID: call.request.JobID, InvocationID: call.request.InvocationID, Type: "result",
		Result: &actionabi.Result{Outcome: actionabi.Succeeded, Changed: &changed, Value: body}}, nil
}

// readInvocationStatus reads the single result frame of a status query.
func readInvocationStatus(stream io.Reader, jobID, invocationID string) (InvocationStatus, error) {
	reader, err := actionabi.NewExecutionReader(stream, jobID, invocationID)
	if err != nil {
		return InvocationStatus{}, ErrUnavailable
	}
	frame, err := reader.Next()
	if err != nil || frame.Type != "result" || frame.Result == nil || frame.Result.Outcome != actionabi.Succeeded {
		return InvocationStatus{}, ErrUnavailable
	}
	if _, err := reader.Next(); err != io.EOF {
		return InvocationStatus{}, ErrUnavailable
	}
	var status InvocationStatus
	decoder := json.NewDecoder(bytes.NewReader(frame.Result.Value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&status) != nil || decoder.More() || status.Validate(jobID, invocationID) != nil {
		return InvocationStatus{}, ErrUnavailable
	}
	return status, nil
}
