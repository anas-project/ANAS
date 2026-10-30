package ingress

import (
	"context"
	"sync"
	"time"
)

// Run separates periodic source listing from existing delivery recovery. A
// failing AD/login bootstrap or slow cloud page cannot block a durable native
// receipt/ack task. Notifications wake the same paths; they never add another
// delivery mechanism. All errors remain in their PG task/poll records.
func (c *Consumer) Run(ctx context.Context, wake <-chan struct{}) error {
	if !c.valid() {
		return ErrConfiguration
	}
	pollKick, jobKick := make(chan struct{}, 1), make(chan struct{}, 1)
	kick := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case _, open := <-wake:
				if !open {
					wake = nil
					continue
				}
				kick(pollKick)
				kick(jobKick)
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			_ = c.Poll(ctx)
			kick(jobKick)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-pollKick:
			}
		}
	}()
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			_ = c.Notices(ctx)
			_ = c.Work(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-jobKick:
			}
		}
	}()
	workers.Wait()
	return ctx.Err()
}
