package jobexecutor

import (
	"context"
	"errors"
	"sort"

	"github.com/anas-project/ANAS/internal/actionabi"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

type hostIngressChange struct {
	jobID, invocation, action, workspace, requestDigest string
	gate                                                *computeingressruntime.ControllerChange
}

func (g *hostIngressChange) matches(job consolejobs.Job) bool {
	return g != nil && job.Action != nil && g.jobID == job.ID && g.invocation == job.Action.InvocationID &&
		g.action == job.Action.Name && g.workspace == job.WorkspaceID &&
		g.requestDigest == consolejobs.DigestRequest(mustMarshalJSON(job.Request))
}

func ingressConfigurationWrite(action string) bool {
	// Opening/closing per-publication actions MUST NOT be added here: cleanup
	// needs to execute while this barrier is pending. These existing actions
	// instead change scope, host credentials or daemon/storage dependencies.
	return hostaction.IsApplyAction(action)
}

func (s *HostActionService) actionAdmission(action string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	closing := s.closing || s.owner != nil && s.owner.Err() != nil
	return s.available && !s.stopped && (!closing || action == hostaction.ActionStatus || action == hostaction.ActionObserveHTTP)
}

func (s *HostActionService) shutdownRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing || s.owner != nil && s.owner.Err() != nil
}

// RetryIngressShutdown is a trusted daemon-owner operation, not an HTTP route
// or new privileged action. It retries only retained cleanup and never renews
// authority. Canceling this wait does not cancel the bounded drain attempt.
func (s *HostActionService) RetryIngressShutdown(ctx context.Context) error {
	if s == nil || ctx == nil {
		return hostaction.ErrUnavailable
	}
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if !closing {
		return hostaction.ErrUnavailable
	}
	change, err := s.options.IngressCoordinator.RetryShutdown(ctx)
	if err != nil {
		return err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	err = change.Wait(ctx)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return err
}

func (s *HostActionService) prepareIngressChange(ctx context.Context, job consolejobs.Job) (bool, error) {
	if job.Action == nil || !ingressConfigurationWrite(job.Action.Name) {
		return true, nil
	}
	s.mu.Lock()
	existing := s.ingressChanges[job.ID]
	s.mu.Unlock()
	if existing != nil {
		if !existing.matches(job) {
			return true, hostaction.ErrDenied
		}
		return existing.gate.Poll()
	}
	ids := []string{job.WorkspaceID}
	if job.Action.Name != hostaction.ActionObserverApply {
		// Host install/configure/enroll/uninstall and prune affect one shared
		// daemon, not just the workspace in which their job was authorized.
		ids = make([]string, 0, len(s.workspaces))
		for id := range s.workspaces {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	gate, err := s.options.IngressCoordinator.BeginChange(ctx, ids)
	if errors.Is(err, computeingressruntime.ErrControllerChange) {
		// A prior authorized configuration job still owns an overlapping
		// scope. Keep this job queued; never steal its fence or retry token.
		return false, nil
	}
	if err != nil {
		return true, err
	}
	change := &hostIngressChange{jobID: job.ID, invocation: job.Action.InvocationID, action: job.Action.Name,
		workspace: job.WorkspaceID, requestDigest: consolejobs.DigestRequest(mustMarshalJSON(job.Request)), gate: gate}
	s.mu.Lock()
	s.ingressChanges[job.ID] = change
	s.mu.Unlock()
	return gate.Poll()
}

// Independently checked at the broker's actual execution authorization,
// including its post-execution check. Enqueue-time permission is not enough.
func (s *HostActionService) checkIngressChange(job consolejobs.Job) error {
	if job.Action == nil || !ingressConfigurationWrite(job.Action.Name) {
		return nil
	}
	s.mu.Lock()
	change := s.ingressChanges[job.ID]
	s.mu.Unlock()
	if !change.matches(job) {
		return hostaction.ErrDenied
	}
	ready, err := change.gate.Poll()
	if !ready || err != nil {
		return hostaction.ErrDenied
	}
	return nil
}

func (s *HostActionService) retireIngressChanges(jobs []consolejobs.Job) error {
	for _, job := range jobs {
		s.mu.Lock()
		change := s.ingressChanges[job.ID]
		s.mu.Unlock()
		if change == nil {
			continue
		}
		if !change.matches(job) || consolejobs.ActionContainmentLost(job) ||
			(job.Action != nil && (job.Action.Outcome == actionabi.Unknown || job.Status == consolejobs.StatusInterrupted)) {
			return consolejobs.ErrActionContainment
		}
		if !moduleActionTerminal(job.Status) {
			continue
		}
		if ready, _ := change.gate.Poll(); !ready {
			continue // Rejected while a now-unauthorized job was still draining.
		}
		if err := change.gate.Release(); err != nil {
			return hostaction.ErrUnavailable
		}
		s.mu.Lock()
		delete(s.ingressChanges, job.ID)
		s.mu.Unlock()
	}
	return nil
}
