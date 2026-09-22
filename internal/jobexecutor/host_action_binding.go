package jobexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sync"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

// HostJobAuthorizer belongs to the trusted execution owner. Actor/workspace
// come from the persisted job, not a socket request. Peer comes from the
// separately authenticated host transport. It must re-resolve current rights.
type HostJobAuthorizer func(context.Context, consolejobs.Job, hostaction.PeerIdentity) error

// HostJobRequest freezes the installed release alongside public action input.
// This helper does not create/start/authorize a job or confer root privileges.
func HostJobRequest(release hostaction.ReleaseIdentity) (map[string]any, error) {
	return HostActionRequest(hostaction.ActionStatus, release, json.RawMessage(`{}`))
}

func HostActionRequest(action string, release hostaction.ReleaseIdentity, parameters json.RawMessage) (map[string]any, error) {
	request, _, err := hostaction.FrozenRequest(action, release, parameters)
	return request, err
}

// HostJobBinding lives on the job owner's side, NOT in the root executor.
// ServeBroker marshals this boundary without giving root console-store paths
// or allowing requests to register callbacks. Listener/service assembly and
// independent process-exit-status integration remain explicit prerequisites.
// The guard is single-use in this supervisor; it is not a second idempotency
// store. Recreating it after an unconfirmed attempt is forbidden by recovery.
type HostJobBinding struct {
	mu                    sync.Mutex
	store                 *consolejobs.Store
	lease                 *consolejobs.ExecutionLease
	job                   consolejobs.Job
	release               hostaction.ReleaseIdentity
	authorize             HostJobAuthorizer
	unretain              func()
	remote                hostBrokerSession
	used, closed          bool
	completionStarted     bool
	exchangeDone          chan struct{}
	exchangeErr           error
	exchangeFinished      bool
	acceptedPeer          hostaction.PeerIdentity
	completionFinished    bool
	completionQuarantined bool
}

var _ hostaction.JobBinding = (*HostJobBinding)(nil)

func NewHostJobBinding(ctx context.Context, store *consolejobs.Store, lease *consolejobs.ExecutionLease, jobID string, release hostaction.ReleaseIdentity, authorize HostJobAuthorizer) (*HostJobBinding, error) {
	if ctx == nil || store == nil || lease == nil || authorize == nil || release.Validate() != nil {
		return nil, hostaction.ErrUnavailable
	}
	job, err := store.Get(ctx, jobID)
	if err != nil || job.Action == nil {
		return nil, hostaction.ErrDenied
	}
	job, unretain, err := store.RetainActionExecution(ctx, lease, jobID, job.Action.InvocationID)
	if err != nil {
		return nil, hostaction.ErrDenied
	}
	b := &HostJobBinding{store: store, lease: lease, job: job, release: release, authorize: authorize, unretain: unretain, exchangeDone: make(chan struct{})}
	if !b.matches(job) {
		unretain()
		return nil, hostaction.ErrDenied
	}
	return b, nil
}

func (b *HostJobBinding) matches(job consolejobs.Job) bool {
	if job.Action == nil || job.Action.ABI != actionabi.Version ||
		job.Action.Outcome != "" || job.Action.Cancellation != nil || job.Status != consolejobs.StatusRunning ||
		job.CreatedBy == "" || job.WorkspaceID == "" || job.StartedAt == nil {
		return false
	}
	spec, ok := hostaction.LookupAction(job.Action.Name)
	if !ok || job.Mutating != spec.Mutating {
		return false
	}
	parameters, err := publicParametersFromStoredRequest(job.Action.Name, job.Request)
	if err != nil {
		return false
	}
	if !hostaction.ObservationScopeMatchesWorkspace(job.Action.Name, parameters, job.WorkspaceID) {
		return false
	}
	want, err := HostActionRequest(job.Action.Name, b.release, parameters)
	if err != nil {
		return false
	}
	body, err := json.Marshal(job.Request)
	if err != nil {
		return false
	}
	if spec.Mutating {
		body, err = json.Marshal(publicStoredRequest(job.Request))
		if err != nil {
			return false
		}
	}
	canonical, _ := json.Marshal(want)
	return bytes.Equal(body, canonical) && job.ID == b.job.ID && job.Kind == b.job.Kind && job.CreatedBy == b.job.CreatedBy &&
		job.WorkspaceID == b.job.WorkspaceID && job.Action.InvocationID == b.job.Action.InvocationID &&
		reflect.DeepEqual(job.StartedAt, b.job.StartedAt)
}

func (b *HostJobBinding) current(ctx context.Context) (consolejobs.Job, error) {
	job, release, err := b.store.RetainActionExecution(ctx, b.lease, b.job.ID, b.job.Action.InvocationID)
	if err != nil {
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	defer release()
	if !b.matches(job) {
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	return job, nil
}

func (b *HostJobBinding) WithHostInvocation(ctx context.Context, request actionabi.Request, release hostaction.ReleaseIdentity, peer hostaction.PeerIdentity, run func(context.Context) error) error {
	if b == nil || ctx == nil || run == nil || peer.PID <= 0 {
		return hostaction.ErrDenied
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used || b.closed || b.store == nil || b.lease == nil || b.job.Action == nil || b.unretain == nil || b.authorize == nil || release != b.release || ctx.Err() != nil {
		return hostaction.ErrDenied
	}
	parameters, err := parametersForExecution(b.job)
	if err != nil {
		return hostaction.ErrDenied
	}
	want := actionabi.Request{ABI: actionabi.Version, JobID: b.job.ID, InvocationID: b.job.Action.InvocationID, Action: b.job.Action.Name, Parameters: parameters}
	body, err := actionabi.EncodeRequest(request)
	canonical, _ := actionabi.EncodeRequest(want)
	if err != nil || !bytes.Equal(body, canonical) {
		return hostaction.ErrDenied
	}
	// Consume before authorization so failures cannot lead to implicit replay
	// through the same supervisor object. Store policy owns caller retries.
	b.used = true
	b.acceptedPeer = peer
	job, err := b.current(ctx)
	if err != nil {
		return err
	}
	if b.authorize(ctx, job, peer) != nil {
		return hostaction.ErrDenied
	}
	// Re-read after the callback; it may have waited while cancellation or
	// another trusted lifecycle writer changed the job.
	if _, err := b.current(ctx); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := run(ctx); err != nil {
		return hostaction.ErrUnavailable
	}
	job, err = b.current(ctx)
	if err != nil {
		return err
	}
	// A remote exchange can span permission revocation. Do not validate the
	// provisional result using only the roles checked before work started.
	if b.authorize(ctx, job, peer) != nil {
		return hostaction.ErrDenied
	}
	if _, err := b.current(ctx); err != nil {
		return err
	}
	return nil
}

// Close must be called by the supervisor only after confirmed process/stream
// cleanup, NOT when a subscriber disconnects. It waits for an active callback.
// On unknown containment the supervisor retains this object and its lease.
func (b *HostJobBinding) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.completionQuarantined || (b.completionStarted && !b.completionFinished) {
		b.mu.Unlock()
		return hostaction.ErrBrokerExecutorRunning
	}
	// Serve holds the session lock while it calls WithHostInvocation, which
	// takes b.mu. Never acquire those two locks in the reverse order here.
	remote := b.remote
	if remote != nil && !b.closed {
		b.mu.Unlock()
		if err := remote.Close(); err != nil {
			return err
		}
		b.mu.Lock()
	}
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		if b.unretain != nil {
			b.unretain()
		}
	}
	return nil
}
