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
	if c.Source == nil || c.Interval <= 0 || c.OperationTimeout <= 0 || c.CleanupTimeout <= 0 {
		return fmt.Errorf("HTTP controller requires a desired source and positive poll/operation/cleanup durations")
	}
	e := c.Executor
	e.Authority = c.Source
	return e.withSession(ctx, func(s execution) error {
		// The process cannot know whether a prior external step succeeded just
		// before a crash. Retire all persisted targets before reading new work.
		// A fresh Source/Planner may only open a newly validated reservation.
		recovery := true
		ticker := time.NewTicker(c.Interval)
		defer ticker.Stop()
		events := c.Events
		cleanup := func() error {
			// Caller cancellation must not cancel revocation immediately. Keep
			// the leader lock held for the bounded cleanup; failure stays on disk.
			cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.CleanupTimeout)
			defer cancel()
			if err := s.recover(cleanCtx); err != nil {
				return err
			}
			c.Source.AfterRetirement()
			return nil
		}
		for {
			if err := ctx.Err(); err != nil {
				return errors.Join(err, cleanup())
			}
			if recovery {
				err := cleanup()
				if err != nil {
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
					// A failed complete observation cannot renew previous permits.
					// Cleanup is attempted for every lease, independent of Core.
					recovery = true
					c.report(fmt.Errorf("HTTP reconciliation failed: %w", err))
					if closeErr := cleanup(); closeErr != nil {
						c.report(fmt.Errorf("HTTP withdrawal incomplete: %w", closeErr))
					} else {
						recovery = false
					}
				}
			}
			select {
			case <-ctx.Done():
				return errors.Join(ctx.Err(), cleanup())
			case <-ticker.C:
			case _, open := <-events:
				if !open {
					events = nil
				}
			}
		}
	})
}

func (c Controller) report(err error) {
	if c.Report != nil {
		c.Report(err)
	}
}
