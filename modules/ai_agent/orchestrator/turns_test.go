package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// AGENT-R-021: turns on one issue run one at a time. Two generations answering
// the same thread would interleave in the timeline and contradict each other.
func TestTurnsRunOneAtATime(t *testing.T) {
	queue := &TurnQueue{}
	ctx := context.Background()
	var mu sync.Mutex
	var concurrent, peak int
	var order []string

	for _, name := range []string{"first", "second", "third"} {
		name := name
		if err := queue.Submit(ctx, Turn{Trigger: name, Run: func(context.Context) error {
			mu.Lock()
			concurrent++
			if concurrent > peak {
				peak = concurrent
			}
			order = append(order, name)
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			concurrent--
			mu.Unlock()
			return nil
		}}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if err := queue.Drain(ctx); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1", peak)
	}
	if len(order) != 3 || order[0] != "first" || order[2] != "third" {
		t.Fatalf("order = %v, want submission order", order)
	}
}

// A comment arriving while a turn runs is queued and acknowledged with a
// reaction, so the person can see it landed without a placeholder comment.
func TestQueuedTurnsAreAcknowledged(t *testing.T) {
	var acknowledged []int64
	queue := &TurnQueue{Acknowledge: func(_ context.Context, id int64) error {
		acknowledged = append(acknowledged, id)
		return nil
	}}
	ctx := context.Background()
	if err := queue.Submit(ctx, Turn{Trigger: "first", CommentID: 1, Run: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := queue.Submit(ctx, Turn{Trigger: "second", CommentID: 2, Run: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(acknowledged) != 1 || acknowledged[0] != 2 {
		t.Fatalf("acknowledged = %v; only the queued turn needs a reaction", acknowledged)
	}
}

// AGENT-R-021: an interrupting command cuts the running turn short instead of
// waiting behind it, and clears what was queued.
func TestInterruptCancelsTheRunningTurnAndTheQueue(t *testing.T) {
	queue := &TurnQueue{}
	ctx := context.Background()
	started := make(chan struct{})
	var laterRan bool

	if err := queue.Submit(ctx, Turn{Trigger: "long", Run: func(runCtx context.Context) error {
		close(started)
		<-runCtx.Done()
		return runCtx.Err()
	}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := queue.Submit(ctx, Turn{Trigger: "later", Run: func(context.Context) error {
		laterRan = true
		return nil
	}}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- queue.Drain(ctx) }()
	<-started
	queue.Interrupt()

	if err := <-done; !errors.Is(err, ErrInterrupted) {
		t.Fatalf("Drain = %v, want ErrInterrupted", err)
	}
	if laterRan {
		t.Fatal("a queued turn ran after the interruption")
	}
	if queue.Pending() != 0 {
		t.Fatalf("pending = %d after an interruption", queue.Pending())
	}
}

// A failing turn does not stop the queue: the failure belongs to that turn, and
// the next instruction still deserves an answer.
func TestOneFailingTurnDoesNotStopTheQueue(t *testing.T) {
	queue := &TurnQueue{}
	ctx := context.Background()
	var ran []string
	for _, name := range []string{"bad", "good"} {
		name := name
		if err := queue.Submit(ctx, Turn{Trigger: name, Run: func(context.Context) error {
			ran = append(ran, name)
			if name == "bad" {
				return errors.New("generation failed")
			}
			return nil
		}}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	err := queue.Drain(ctx)
	if err == nil {
		t.Fatal("Drain hid a turn failure")
	}
	if len(ran) != 2 {
		t.Fatalf("ran = %v; the queue stopped at the failure", ran)
	}
}

// AGENT-R-022: the chair's stop condition is a prompt, so the ceilings are what
// actually terminate a round table.
func TestRoundTableCeilingsTerminate(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	base := func() *RoundTable {
		return &RoundTable{
			Limits: RoundTableLimits{MaxRounds: 3, MaxBudgetUSD: 1, MaxWallClock: time.Hour,
				SilenceWindow: 24 * time.Hour},
			Host: "codex", Started: now, LastHuman: now,
			Now: func() time.Time { return now },
		}
	}
	if limit := base().ExceededLimit(); limit != "" {
		t.Fatalf("a fresh round table reports %q", limit)
	}

	rounds := base()
	rounds.Rounds = 3
	if !contains([]string{"the round limit (3) was reached"}, rounds.ExceededLimit()) {
		t.Fatalf("round ceiling = %q", rounds.ExceededLimit())
	}

	budget := base()
	budget.SpentUSD = 1
	if budget.ExceededLimit() == "" {
		t.Fatal("the budget ceiling did not fire")
	}

	clock := base()
	clock.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if clock.ExceededLimit() == "" {
		t.Fatal("the wall-clock ceiling did not fire")
	}

	silent := base()
	silent.Now = func() time.Time { return now.Add(48 * time.Hour) }
	if silent.ExceededLimit() == "" {
		t.Fatal("the silence ceiling did not fire")
	}
}

// Only the chair starts a round; the others answer what they were asked.
// Without this every agent replies to every reply and the discussion grows
// instead of converging.
func TestOnlyTheChairMayStartARound(t *testing.T) {
	table := &RoundTable{Host: "codex"}
	if !table.MaySpeak("codex", nil) {
		t.Fatal("the chair may not speak")
	}
	if table.MaySpeak("claude_code", nil) {
		t.Fatal("a participant spoke without being addressed")
	}
	if !table.MaySpeak("claude_code", []string{"claude_code"}) {
		t.Fatal("an addressed participant may not answer")
	}
}
