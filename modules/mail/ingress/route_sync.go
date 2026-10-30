package ingress

import (
	"context"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type RouteLedger interface {
	StageSourceRoutes(context.Context, string, string, []store.IngressRoute, map[string]string) (store.IngressRouteOperation, error)
	IssueSourceRoutes(context.Context, store.IngressRouteOperation) error
	ObserveSourceRoutes(context.Context, store.IngressRouteResult) error
	SourceRoutesFailed(context.Context, store.IngressRouteOperation, string) error
}
type RouteSource interface {
	ReadRoutes(context.Context) (RouteState, error)
	ReplaceRoutes(context.Context, int64, string, []Route) (RouteState, error)
}

// RouteSync coordinates the existing global CAS endpoint. A lost response is
// recovered only through a matching durable operation. Cloud state unknown to
// an old backup stops reconciliation instead of authorizing an overwrite.
type RouteSync struct {
	Ledger         RouteLedger
	Source         RouteSource
	ID, Generation string
	mu             sync.Mutex
	confirmed      string
	checked        time.Time
}

func routeResult(s RouteState) store.IngressRouteResult {
	r := store.IngressRouteResult{SourceID: s.SourceID, Generation: s.Generation, OperationID: s.OperationID, Fingerprint: s.Fingerprint, Revision: s.Revision, Sequence: s.Sequence}
	for _, b := range s.Bindings {
		r.Bindings = append(r.Bindings, store.IngressRouteBinding{IngressBinding: store.IngressBinding{SourceID: s.SourceID, Generation: s.Generation, Address: b.Address, ClaimVersion: b.ClaimVersion, MailboxID: b.MailboxID, OwnerAnchor: b.OwnerAnchor, FirstSequence: b.FirstSequence}, ClosedSequence: b.ClosedSequence})
	}
	return r
}
func (s *RouteSync) Synchronize(ctx context.Context, routes []Route, ready map[string]string) error {
	return s.synchronize(ctx, routes, ready, false)
}

// Prepare requires a fresh source readback before another source can be
// withdrawn. The ordinary one-minute reconciliation cache is insufficient for
// that decision. Only internal receiving permissions are changed here.
func (s *RouteSync) Prepare(ctx context.Context, routes []Route, ready map[string]string) error {
	return s.synchronize(ctx, routes, ready, true)
}

func (s *RouteSync) synchronize(ctx context.Context, routes []Route, ready map[string]string, fresh bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fp, e := RouteFingerprint(routes)
	if e != nil {
		return e
	}
	if !fresh && s.confirmed == fp && time.Since(s.checked) < time.Minute {
		return nil
	}
	// Persist before even observing the network, so a fresh source is expected
	// to be empty. Never adopt somebody else's preconfigured state implicitly.
	op, e := s.Ledger.StageSourceRoutes(ctx, s.ID, s.Generation, routes, ready)
	if e != nil {
		return e
	}
	remote, e := s.Source.ReadRoutes(ctx)
	if e != nil {
		_ = s.Ledger.SourceRoutesFailed(ctx, op, "source-read-failed")
		return e
	}
	if e = s.Ledger.ObserveSourceRoutes(ctx, routeResult(remote)); e != nil {
		_ = s.Ledger.SourceRoutesFailed(ctx, op, "source-state-unrecognized")
		return e
	}
	// An earlier issued operation may have landed while its response was lost.
	// Its persisted history authorizes recovery, then the latest desired state
	// receives a fresh operation with that now-confirmed precondition.
	op, e = s.Ledger.StageSourceRoutes(ctx, s.ID, s.Generation, routes, ready)
	if e != nil {
		return e
	}
	if op.State != "confirmed" {
		if e = s.Ledger.IssueSourceRoutes(ctx, op); e != nil {
			return e
		}
		remote, e = s.Source.ReplaceRoutes(ctx, op.ExpectedRevision, op.ID, op.Routes)
		if e != nil {
			_ = s.Ledger.SourceRoutesFailed(ctx, op, "source-write-unconfirmed")
			return e
		}
		remote, e = s.Source.ReadRoutes(ctx)
		if e != nil {
			_ = s.Ledger.SourceRoutesFailed(ctx, op, "source-readback-failed")
			return e
		}
		if remote.Revision != op.ExpectedRevision+1 || remote.OperationID != op.ID || remote.Fingerprint != op.Fingerprint {
			_ = s.Ledger.SourceRoutesFailed(ctx, op, "source-readback-unconfirmed")
			return ErrIdentity
		}
		if e = s.Ledger.ObserveSourceRoutes(ctx, routeResult(remote)); e != nil {
			return e
		}
	}
	s.confirmed, s.checked = fp, time.Now()
	return nil
}
