package computeingressruntime

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type DesiredSnapshot struct {
	Epoch   string
	Targets []PublicationTarget
}

// DesiredSource must enumerate the complete currently authorized request set,
// not a delta or a cached prior successful read. Any incomplete directory/Core
// read is an error. Event notifications only wake the loop; they carry no
// authority. The source owns its Planner and must not silently mint another
// reservation for a retired request on every poll.
type DesiredSource interface {
	AuthorizationSource
	ReadDesired(context.Context, Journal) (DesiredSnapshot, error)
	// Called only after all persisted publications were confirmed retired.
	// Discard token caches so a fresh complete authorization/observation read
	// can recover still-valid requests under new reservations.
	AfterRetirement()
}

// Controller is a serialized periodic reconciliation loop. Run holds the
// exclusive state lock through startup recovery, observations, external steps
// and shutdown cleanup. It does not install a daemon or supply host privileges.
type Controller struct {
	Executor         Executor
	Source           DesiredSource
	Interval         time.Duration
	OperationTimeout time.Duration
	CleanupTimeout   time.Duration
	Events           <-chan struct{}
	// Report is optional and synchronous. It must return promptly and must not
	// log consumer request bodies or credentials. No goroutine is spawned.
	Report func(error)
}

func (c Controller) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("HTTP controller requires an owner context")
	}
	e, err := c.configuredExecutor()
	if err != nil {
		return err
	}
	return e.withSession(ctx, func(s execution) error {
		cause, drainErr := c.runSession(ctx, s, nil, nil)
		if drainErr == nil {
			clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.CleanupTimeout)
			drainErr = s.completeWorkspaceDrain(clean)
			cancel()
		}
		return errors.Join(cause, drainErr)
	})
}

func (c Controller) configuredExecutor() (Executor, error) {
	if c.Source == nil || c.Interval <= 0 || c.OperationTimeout <= 0 || c.CleanupTimeout <= 0 {
		return Executor{}, fmt.Errorf("HTTP controller requires a desired source and positive poll/operation/cleanup durations")
	}
	e := c.Executor
	e.Authority = c.Source
	if e.Observer == nil || e.Host == nil || e.Probe == nil || e.Renderer == nil || e.Store == nil {
		return Executor{}, fmt.Errorf("HTTP controller requires all independent runtime adapters")
	}
	return e, nil
}

// Called inside one held state-store session. A managed owner can retain that
// exact session after failed shutdown and retry ONLY drain, never publishing.
// Cancellation and drain failure are distinct: errors.Is(Canceled) alone is
// not evidence that routes, permits and address holds were removed.
func (c Controller) runSession(ctx context.Context, s execution, ready, stopping func()) (error, error) {
	// A previous process may have crashed between an effect and its receipt.
	// Retire persisted targets before permitting a fresh authorization read.
	recovery := true
	ticker := time.NewTicker(c.Interval)
	defer ticker.Stop()
	events := c.Events
	shutdown := func() (error, error) {
		if stopping != nil {
			stopping()
		}
		return ctx.Err(), c.drain(ctx, s)
	}
	for {
		if ctx.Err() != nil {
			return shutdown()
		}
		if recovery {
			if err := c.drain(ctx, s); err != nil {
				c.report(fmt.Errorf("HTTP recovery incomplete: %w", err))
			} else {
				recovery = false
			}
		}
		if !recovery {
			opCtx, cancel := context.WithTimeout(ctx, c.OperationTimeout)
			desired, err := c.Source.ReadDesired(opCtx, s.journal)
			if err == nil {
				err = s.reconcile(opCtx, desired.Epoch, desired.Targets)
			}
			cancel()
			if err != nil {
				// An incomplete read must not renew previous permits. Withdrawal
				// uses independent cleanup authority and the old installed readers.
				recovery = true
				c.report(fmt.Errorf("HTTP reconciliation failed: %w", err))
				if closeErr := c.drain(ctx, s); closeErr != nil {
					c.report(fmt.Errorf("HTTP withdrawal incomplete: %w", closeErr))
				} else {
					recovery = false
				}
			} else if ready != nil && ctx.Err() == nil {
				ready()
			}
		}
		select {
		case <-ctx.Done():
			return shutdown()
		case <-ticker.C:
		case _, open := <-events:
			if !open {
				events = nil
			}
		}
	}
}

func (c Controller) drain(parent context.Context, s execution) error {
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), c.CleanupTimeout)
	defer cancel()
	if err := s.recover(cleanCtx); err != nil {
		return err
	}
	if err := cleanCtx.Err(); err != nil {
		return err
	}
	c.Source.AfterRetirement()
	return nil
}

func (c Controller) report(err error) {
	if c.Report != nil {
		c.Report(err)
	}
}
