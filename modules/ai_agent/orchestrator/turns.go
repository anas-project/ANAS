package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrInterrupted is returned by a turn that a command cut short.
var ErrInterrupted = errors.New("interrupted")

// Turn is one unit of agent work on an issue: read the context, generate,
// write the result.
type Turn struct {
	// Trigger identifies what caused the turn, for the record.
	Trigger string
	// CommentID is the comment that triggered it, if any, so the queue can
	// acknowledge it with a reaction while it waits.
	CommentID int64
	Run       func(context.Context) error
}

// TurnQueue serialises the turns of one issue. Turns of the same issue must not
// overlap: two generations answering the same thread produce replies that
// contradict each other and interleave in the timeline. Comments that arrive
// while a turn is running are queued rather than dropped, and an interrupting
// command cuts the running turn short instead of waiting behind it
// (AGENT-R-021).
type TurnQueue struct {
	// Acknowledge reacts to a queued comment so the person can see the
	// instruction landed, without a placeholder comment.
	Acknowledge func(ctx context.Context, commentID int64) error

	mu        sync.Mutex
	queue     []Turn
	running   bool
	cancelRun context.CancelFunc
	idle      chan struct{}
}

// Submit adds a turn. It returns immediately: the caller is an event handler and
// must not block on a generation that can take minutes.
func (q *TurnQueue) Submit(ctx context.Context, turn Turn) error {
	q.mu.Lock()
	q.queue = append(q.queue, turn)
	queued := len(q.queue) > 1 || q.running
	q.mu.Unlock()
	if queued && turn.CommentID != 0 && q.Acknowledge != nil {
		// The turn is behind another one. A reaction says "received" without
		// occupying the timeline with a comment that would have to be cleaned
		// up once the real answer arrives.
		if err := q.Acknowledge(ctx, turn.CommentID); err != nil {
			return err
		}
	}
	return nil
}

// Interrupt cancels the running turn and clears anything queued behind it. It
// is what `/stop` and `ai:cancel` reach: an interruption that had to wait for
// the current generation would not be an interruption.
func (q *TurnQueue) Interrupt() {
	q.mu.Lock()
	cancel := q.cancelRun
	q.queue = nil
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Pending reports how many turns are waiting.
func (q *TurnQueue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

// Drain runs queued turns one at a time until the queue is empty or the context
// ends. A turn that fails does not stop the queue: the failure belongs to that
// turn, and the next instruction still deserves an answer.
func (q *TurnQueue) Drain(ctx context.Context) error {
	var failures []error
	for {
		q.mu.Lock()
		if len(q.queue) == 0 {
			q.mu.Unlock()
			return errors.Join(failures...)
		}
		turn := q.queue[0]
		q.queue = q.queue[1:]
		runCtx, cancel := context.WithCancel(ctx)
		q.cancelRun, q.running = cancel, true
		q.mu.Unlock()

		err := turn.Run(runCtx)
		// The cancellation state has to be read before this loop cancels the
		// context itself, or every completed turn looks interrupted.
		interrupted := errors.Is(runCtx.Err(), context.Canceled)
		cancel()

		q.mu.Lock()
		q.cancelRun, q.running = nil, false
		q.mu.Unlock()

		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case interrupted:
			// Cancelled by Interrupt, not by shutdown. That is a deliberate
			// outcome, not a failure to report.
			return ErrInterrupted
		case err == nil:
		default:
			failures = append(failures, fmt.Errorf("turn %q: %w", turn.Trigger, err))
		}
	}
}

// RoundTableLimits are the ceilings a multi-agent discussion runs under. They
// exist because the chair's stop condition is a prompt: it asks the model to
// judge when the discussion is finished, and a prompt cannot be relied on to
// terminate. These can (AGENT-R-022).
type RoundTableLimits struct {
	MaxRounds     int
	MaxBudgetUSD  float64
	MaxWallClock  time.Duration
	SilenceWindow time.Duration
}

// DefaultRoundTableLimits is what a deployment gets before it configures
// anything. The numbers are deliberately low: a round table that needs more
// than this is not converging, and the right answer is a person looking at it.
var DefaultRoundTableLimits = RoundTableLimits{
	MaxRounds: 8, MaxBudgetUSD: 5, MaxWallClock: 30 * time.Minute, SilenceWindow: 24 * time.Hour,
}

// RoundTable tracks one discussion against its ceilings.
type RoundTable struct {
	Limits    RoundTableLimits
	Host      string
	Started   time.Time
	Rounds    int
	SpentUSD  float64
	LastHuman time.Time
	Now       func() time.Time
}

func (r *RoundTable) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// ExceededLimit reports the ceiling that has been reached, or an empty string
// while the discussion may continue. The string is the explanation that goes
// into the document and the status comment, because a discussion cut short by a
// ceiling must not read as one that concluded (AGENT-R-022).
func (r *RoundTable) ExceededLimit() string {
	if r.Limits.MaxRounds > 0 && r.Rounds >= r.Limits.MaxRounds {
		return fmt.Sprintf("the round limit (%d) was reached", r.Limits.MaxRounds)
	}
	if r.Limits.MaxBudgetUSD > 0 && r.SpentUSD >= r.Limits.MaxBudgetUSD {
		return fmt.Sprintf("the discussion budget ($%.2f) was spent", r.Limits.MaxBudgetUSD)
	}
	if r.Limits.MaxWallClock > 0 && !r.Started.IsZero() &&
		r.now().Sub(r.Started) >= r.Limits.MaxWallClock {
		return fmt.Sprintf("the wall-clock limit (%s) was reached", r.Limits.MaxWallClock)
	}
	if r.Limits.SilenceWindow > 0 && !r.LastHuman.IsZero() &&
		r.now().Sub(r.LastHuman) >= r.Limits.SilenceWindow {
		return fmt.Sprintf("nobody responded for %s", r.Limits.SilenceWindow)
	}
	return ""
}

// MaySpeak reports whether an agent may take the floor. Only the chair starts a
// round; the others answer what they were asked. Without this every agent
// replies to every other agent's reply, and the discussion grows on its own
// rather than converging.
func (r *RoundTable) MaySpeak(agent string, addressed []string) bool {
	if agent == r.Host {
		return true
	}
	return contains(addressed, agent)
}
