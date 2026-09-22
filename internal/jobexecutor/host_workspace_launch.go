package jobexecutor

import (
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

// StartIngressWorkspace belongs to the installed daemon owner, not a request
// handler. Validation and controller lifetime use the running service's owner
// context. The observation client and coordinator cannot be supplied in the
// launch configuration: they are the same ones used by the actual host queue.
// Host/probe adapters and all filesystem installations remain explicit. There
// is no default network backend, UID/mount installer or production enablement.
func (s *HostActionService) StartIngressWorkspace(actor string, request computeingressruntime.HostWorkspaceStart) (*computeingressruntime.ControllerService, error) {
	if s == nil || request.Observation.Invoker != nil {
		return nil, hostaction.ErrDenied
	}
	s.mu.Lock()
	owner := s.owner
	available := s.available && !s.stopped && !s.closing
	s.mu.Unlock()
	if !available || owner == nil || owner.Err() != nil {
		return nil, hostaction.ErrUnavailable
	}
	if err := s.authorize(owner, actor, request.ScopeID); err != nil {
		return nil, err
	}
	request.Observation = incusingresshost.ProjectionClient{Invoker: HostObservationInvoker{Service: s, Actor: actor, WorkspaceID: request.ScopeID}}
	return s.options.IngressCoordinator.StartHostWorkspace(owner, request)
}
