package computeingressruntime

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

var ErrControllerChange = errors.New("HTTP controller scope is blocked by a configuration change")

const controllerChangeTimeout = 60 * time.Second

type coordinatedController struct {
	service *ControllerService
	done    chan struct{}
}

// ControllerCoordinator serializes trusted launches with configuration writes.
// It owns only IN-PROCESS controllers, not a second persistent registry. Empty
// entries never prove that the host has no artifacts: every launched controller
// must recover the original FileStateStore and independently inventory them.
// A production launcher must share this instance with the host-action service.
// This does not install/start a production launcher or bypass its publish gate.
type ControllerCoordinator struct {
	mu       sync.Mutex
	scopes   map[string]*coordinatedController
	blocks   map[string]*ControllerChange
	shutdown *ControllerChange
}

// MatchesScopes prevents the queue and launcher from silently using different
// workspace sets. This checks registration only, never external cleanliness.
func (c *ControllerCoordinator) MatchesScopes(scopes []string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(scopes) != len(c.scopes) || c.shutdown != nil {
		return false
	}
	seen := map[string]bool{}
	for _, id := range scopes {
		if _, exists := c.scopes[id]; !exists || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func NewControllerCoordinator(scopes []string) (*ControllerCoordinator, error) {
	if len(scopes) == 0 || len(scopes) > 1024 {
		return nil, ErrControllerChange
	}
	c := &ControllerCoordinator{scopes: map[string]*coordinatedController{}, blocks: map[string]*ControllerChange{}}
	for _, scope := range scopes {
		// Match registered workspace IDs without treating them as paths.
		if scope == "" || len(scope) > 64 {
			return nil, ErrControllerChange
		}
		if _, exists := c.scopes[scope]; exists {
			return nil, ErrControllerChange
		}
		c.scopes[scope] = nil
	}
	return c, nil
}

// Start atomically admits and claims a NEW owner before launching Run. It is
// trusted launcher input, never consumer/HTTP request input. Cancellation belongs
// to that launcher; it must keep dependencies alive until explicit drain succeeds.
func (c *ControllerCoordinator) Start(owner context.Context, scope string, service *ControllerService) error {
	if c == nil || owner == nil || owner.Err() != nil || service == nil {
		return ErrControllerChange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, exists := c.scopes[scope]
	if !exists || c.blocks[scope] != nil || c.shutdown != nil {
		return ErrControllerChange
	}
	if previous != nil {
		select {
		case <-previous.done:
			if previous.service.Phase() != ControllerStopped {
				return ErrControllerDrain
			}
		default:
			return ErrControllerChange
		}
	}
	ctx, cancel, err := service.claimRun(owner)
	if err != nil {
		return err
	}
	entry := &coordinatedController{service: service, done: make(chan struct{})}
	c.scopes[scope] = entry
	go func() {
		_ = service.runClaimed(ctx, cancel) // Phase and Stop retain failure evidence.
		close(entry.done)
	}()
	return nil
}

// ControllerChange fences replacement launches through BOTH drain and the
// caller's later configuration effect. A completed drain is not a reusable
// certificate: the caller holds this object until its exact job reaches a
// confirmed terminal state. Unknown execution must retain the fence.
type ControllerChange struct {
	coordinator *ControllerCoordinator
	scopes      []string
	done        chan struct{}
	err         error // Published once by closing done.
	released    bool  // Protected by coordinator.mu.
	shutdown    bool
}

// BeginChange closes launch admission synchronously and drains outside the
// queue goroutine. The queue must remain available for old cleanup dependencies.
// Each explicit change permits ONE drain attempt (or one retry of a retained
// failed owner); polling this handle never initiates another attempt.
func (c *ControllerCoordinator) BeginChange(ctx context.Context, scopes []string) (*ControllerChange, error) {
	if c == nil || ctx == nil || ctx.Err() != nil || len(scopes) == 0 || len(scopes) > 1024 {
		return nil, ErrControllerChange
	}
	ids := slices.Clone(scopes)
	slices.Sort(ids)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shutdown != nil {
		return nil, ErrControllerChange
	}
	for i, id := range ids {
		if _, exists := c.scopes[id]; !exists || c.blocks[id] != nil || i > 0 && id == ids[i-1] {
			return nil, ErrControllerChange
		}
	}
	change := &ControllerChange{coordinator: c, scopes: ids, done: make(chan struct{})}
	entries := make([]*coordinatedController, 0, len(ids))
	for _, id := range ids {
		c.blocks[id] = change
		if entry := c.scopes[id]; entry != nil {
			entries = append(entries, entry)
		}
	}
	startControllerDrain(ctx, change, entries, true)
	return change, nil
}

func startControllerDrain(ctx context.Context, change *ControllerChange, entries []*coordinatedController, retryFailed bool) {
	if len(entries) == 0 {
		close(change.done)
		return
	}
	go func() {
		// Subscriber cancellation abandons waiting, not the old cleanup. The
		// controller uses its original independent per-attempt cleanup budget.
		wait, cancel := context.WithTimeout(context.WithoutCancel(ctx), controllerChangeTimeout)
		defer cancel()
		attempts := make([]*controllerDrainAttempt, len(entries))
		for i, entry := range entries {
			attempt, err := entry.service.requestDrain(retryFailed && entry.service.Phase() == ControllerDrainFailed)
			if err != nil {
				change.err = ErrControllerDrain
			}
			attempts[i] = attempt
		}
		for i, entry := range entries {
			if attempts[i] == nil {
				continue
			}
			err := waitControllerDrain(wait, attempts[i])
			if err == nil {
				select {
				case <-entry.done:
				case <-wait.Done():
					err = wait.Err()
				}
			}
			if err != nil || entry.service.Phase() != ControllerStopped {
				change.err = ErrControllerDrain
				// Continue other scopes; one broken lease cannot keep all open.
			}
		}
		close(change.done)
	}()
}

// BeginShutdown permanently closes launch admission, including scopes already
// fenced by queued configuration jobs. Repeated calls return the same attempt;
// they never retry failed cleanup. Old host-action dependencies must stay alive.
func (c *ControllerCoordinator) BeginShutdown(ctx context.Context) (*ControllerChange, error) {
	if c == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrControllerChange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shutdown != nil {
		return c.shutdown, nil
	}
	c.shutdown = c.shutdownAttempt(ctx, false)
	return c.shutdown, nil
}

// Caller holds mu. Shutdown may overlap a configuration drain: Stop joins the
// current owner attempt; no replacement owner or second journal is created.
func (c *ControllerCoordinator) shutdownAttempt(ctx context.Context, retryFailed bool) *ControllerChange {
	change := &ControllerChange{coordinator: c, done: make(chan struct{}), shutdown: true}
	ids := make([]string, 0, len(c.scopes))
	for id := range c.scopes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	entries := make([]*coordinatedController, 0, len(ids))
	for _, id := range ids {
		if entry := c.scopes[id]; entry != nil {
			entries = append(entries, entry)
		}
	}
	startControllerDrain(ctx, change, entries, retryFailed)
	return change
}

// RetryShutdown is an explicit trusted-owner recovery operation. It never
// reopens admission, changes a scope or starts a replacement controller.
func (c *ControllerCoordinator) RetryShutdown(ctx context.Context) (*ControllerChange, error) {
	if c == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrControllerChange
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.shutdown == nil {
		return nil, ErrControllerChange
	}
	select {
	case <-c.shutdown.done:
		if c.shutdown.err != nil {
			c.shutdown = c.shutdownAttempt(ctx, true)
		}
	default:
	}
	return c.shutdown, nil
}

func (g *ControllerChange) Wait(ctx context.Context) error {
	if g == nil || ctx == nil {
		return ErrControllerChange
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.done:
		_, err := g.Poll()
		return err
	}
}

// Poll is nonblocking; it can run while the same queue executes cleanup jobs.
func (g *ControllerChange) Poll() (bool, error) {
	if g == nil || g.coordinator == nil {
		return true, ErrControllerChange
	}
	g.coordinator.mu.Lock()
	defer g.coordinator.mu.Unlock()
	if g.released {
		return true, ErrControllerChange
	}
	select {
	case <-g.done:
		return true, g.err
	default:
		return false, nil
	}
}

// Release is valid only after drain has completed and the associated job was
// rejected before starting or reached a confirmed terminal. Failed controllers
// remain registered and cannot be replaced; a later explicit change may retry.
func (g *ControllerChange) Release() error {
	if g == nil || g.coordinator == nil {
		return ErrControllerChange
	}
	c := g.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	if g.shutdown {
		return ErrControllerChange
	}
	if g.released {
		return nil
	}
	select {
	case <-g.done:
	default:
		return ErrControllerDrain
	}
	for _, id := range g.scopes {
		if c.blocks[id] != g {
			return ErrControllerChange
		}
	}
	for _, id := range g.scopes {
		delete(c.blocks, id)
	}
	g.released = true
	return nil
}
