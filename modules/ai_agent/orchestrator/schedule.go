package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Estimate is what an agent expects a job to cost, produced after it has read
// the frozen document and before the job is queued.
//
// The user is never asked for it. A person guessing how long an agent will take
// is guessing about something they cannot see, and the number would then be
// used to decide whether the work fits before a deadline -- so the estimate
// comes from the side that is about to do the work (AGENT-R-045).
type Estimate struct {
	// WallClock is how long the job is expected to take.
	WallClock time.Duration
	CostUSD   float64
	// Effort is the agent's own words about the size of the work, kept for the
	// status comment because a duration alone does not say what was assumed.
	Effort string
	// By is the runtime that produced it, and At when.
	By string
	At time.Time
}

// Valid reports whether an estimate can be scheduled on. A zero duration is not
// an optimistic estimate, it is a missing one.
func (e Estimate) Valid() bool { return e.WallClock > 0 && e.By != "" && !e.At.IsZero() }

// estimateSlack multiplies an estimate to get the wall-clock ceiling for a job.
// An estimate that is exactly right would be cut off at its own boundary, so
// the ceiling has to sit above it; twice is enough headroom that an ordinary
// overrun finishes, and low enough that a runaway is stopped while it still
// matters.
const estimateSlack = 2

// WallClockCeiling is the hard limit one job runs under: whichever of the
// estimate-derived allowance and the deployment ceiling is smaller. The
// deployment ceiling is not negotiable by an estimate -- an agent that predicts
// six hours does not thereby grant itself six hours (AGENT-R-058).
func WallClockCeiling(estimate Estimate, deploymentCeiling time.Duration) time.Duration {
	fromEstimate := estimate.WallClock * estimateSlack
	if !estimate.Valid() || fromEstimate > deploymentCeiling {
		return deploymentCeiling
	}
	return fromEstimate
}

// DueCheck is the answer to "does this fit before the deadline".
type DueCheck struct {
	// AtRisk is true when the work is not expected to finish in time.
	AtRisk bool
	// Shortfall is by how much it is expected to miss. It is reported rather
	// than rounded away, because "late" and "eight days late" call for
	// different decisions (AGENT-R-046).
	Shortfall   time.Duration
	ExpectedEnd time.Time
	Reason      string
}

// CheckDue decides whether a job is expected to finish before the issue's due
// date. A job that will not is marked and explained rather than quietly run
// into the deadline: the point of the check is that a person finds out while
// they can still act on it.
func CheckDue(estimate Estimate, due time.Time, start time.Time) DueCheck {
	if due.IsZero() {
		return DueCheck{Reason: "the issue has no due date"}
	}
	if !estimate.Valid() {
		// Without an estimate nothing can be promised, and silently assuming it
		// fits is the failure this check exists to prevent.
		return DueCheck{AtRisk: true,
			Reason: "there is no estimate yet, so it cannot be shown to fit before the due date"}
	}
	end := start.Add(estimate.WallClock)
	if !end.After(due) {
		return DueCheck{ExpectedEnd: end,
			Reason: fmt.Sprintf("expected to finish %s before the due date", due.Sub(end).Round(time.Minute))}
	}
	shortfall := end.Sub(due)
	return DueCheck{
		AtRisk: true, Shortfall: shortfall, ExpectedEnd: end,
		Reason: fmt.Sprintf("expected to finish %s after the due date (%s, estimate %s)",
			shortfall.Round(time.Minute), due.UTC().Format(time.RFC3339),
			estimate.WallClock.Round(time.Minute)),
	}
}

// TimingKind is when a job should start. Approval and timing are two separate
// decisions: approving work does not say it has to begin now.
type TimingKind string

const (
	// TimingScheduled is the default: queued and run when the schedule allows.
	TimingScheduled TimingKind = "scheduled"
	// TimingNow skips the overnight window and takes a lease as soon as one is
	// free. It still needs the approval and the authorization.
	TimingNow TimingKind = "now"
	// TimingAt queues at a wall-clock time.
	TimingAt TimingKind = "at"
	// TimingOn waits for an event the control plane already subscribes to.
	TimingOn TimingKind = "on"
	// TimingHold cancels a timing and returns to waiting for a person.
	TimingHold TimingKind = "hold"
)

// Timing is a parsed `/execute` argument.
type Timing struct {
	Kind  TimingKind
	At    time.Time
	Event Event
	// Deadline is when an event wait gives up. An open-ended wait is a job that
	// silently never runs, so every wait has one (AGENT-R-048).
	Deadline time.Time
}

// Label renders the timing as its `ai:exec-when/*` label, so what a command did
// stays visible on the issue.
func (t Timing) Label() string {
	switch t.Kind {
	case TimingNow:
		return LabelExecuteWhen + "now"
	case TimingAt:
		return LabelExecuteWhen + "at=" + t.At.UTC().Format(time.RFC3339)
	case TimingOn:
		return LabelExecuteWhen + "on=" + t.Event.String()
	case TimingHold:
		return LabelExecuteWhen + "hold"
	}
	return ""
}

// eventWaitDefault is how long an event wait lasts before it falls back to
// waiting for a person. A week is long enough for the pull request someone is
// waiting on to be merged, and short enough that a job nobody remembers does
// not sit in the queue indefinitely.
const eventWaitDefault = 7 * 24 * time.Hour

// Event is something the control plane already receives a webhook for. The set
// is closed on purpose: an event nobody subscribes to would be a wait that can
// never end (AGENT-R-048).
type Event struct {
	Kind   string
	Number int
	Branch string
}

func (e Event) String() string {
	switch {
	case e.Number != 0:
		return e.Kind + " #" + strconv.Itoa(e.Number)
	case e.Branch != "":
		return e.Kind + " " + e.Branch
	}
	return e.Kind
}

// eventKinds maps each supported event onto the webhook that delivers it and
// what identifies the thing being waited on.
var eventKinds = map[string]struct {
	webhook  string
	needsNum bool
	needsRef bool
}{
	"merge":    {webhook: "pull_request", needsNum: true},
	"closed":   {webhook: "issues", needsNum: true},
	"ci-green": {webhook: "action_run_success", needsRef: true},
	"ci-red":   {webhook: "action_run_failure", needsRef: true},
	"push":     {webhook: "push", needsRef: true},
}

// SupportedEvents lists what may be waited on, for the refusal message and the
// generated help.
func SupportedEvents() []string {
	kinds := make([]string, 0, len(eventKinds))
	for kind := range eventKinds {
		kinds = append(kinds, kind)
	}
	sortStrings(kinds)
	return kinds
}

// ParseTiming reads the argument of `/execute`. It is strict about what it
// accepts: a timing it half-understood would be a job that runs at a moment
// nobody asked for.
func ParseTiming(argument string, now time.Time) (Timing, error) {
	fields := strings.Fields(strings.TrimSpace(argument))
	if len(fields) == 0 {
		return Timing{Kind: TimingScheduled}, nil
	}
	switch strings.ToLower(fields[0]) {
	case "now":
		return Timing{Kind: TimingNow}, nil
	case "hold":
		return Timing{Kind: TimingHold}, nil
	case "at":
		if len(fields) < 2 {
			return Timing{}, fmt.Errorf("`/execute at` needs a time, for example 2026-09-07T02:00Z")
		}
		when, err := parseWhen(strings.Join(fields[1:], " "), now)
		if err != nil {
			return Timing{}, err
		}
		if !when.After(now) {
			return Timing{}, fmt.Errorf("%s is in the past", when.UTC().Format(time.RFC3339))
		}
		return Timing{Kind: TimingAt, At: when}, nil
	case "on":
		if len(fields) < 2 {
			return Timing{}, fmt.Errorf("`/execute on` needs an event; supported events are %s",
				strings.Join(SupportedEvents(), ", "))
		}
		event, err := parseEvent(fields[1:])
		if err != nil {
			return Timing{}, err
		}
		return Timing{Kind: TimingOn, Event: event, Deadline: now.Add(eventWaitDefault)}, nil
	}
	return Timing{}, fmt.Errorf("unknown execution timing %q; use now, at <time>, on <event> or hold", fields[0])
}

func parseWhen(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if when, err := time.Parse(layout, value); err == nil {
			if when.Location() == time.UTC && !strings.HasSuffix(value, "Z") &&
				!strings.Contains(value, "+") {
				// A time written without a zone is read in the deployment's
				// zone, which is the one the person was looking at.
				when = time.Date(when.Year(), when.Month(), when.Day(), when.Hour(),
					when.Minute(), when.Second(), 0, now.Location())
			}
			return when, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time; use 2026-09-07T02:00Z or 2026-09-07 02:00", value)
}

func parseEvent(fields []string) (Event, error) {
	kind := strings.ToLower(fields[0])
	spec, known := eventKinds[kind]
	if !known {
		return Event{}, fmt.Errorf("cannot wait for %q; supported events are %s",
			kind, strings.Join(SupportedEvents(), ", "))
	}
	event := Event{Kind: kind}
	argument := ""
	if len(fields) > 1 {
		argument = strings.TrimSpace(fields[1])
	}
	if spec.needsNum {
		number, err := strconv.Atoi(strings.TrimPrefix(argument, "#"))
		if err != nil || number <= 0 {
			return Event{}, fmt.Errorf("`on %s` needs an issue or pull request number, for example `on %s #12`", kind, kind)
		}
		event.Number = number
	}
	if spec.needsRef {
		if argument == "" {
			return Event{}, fmt.Errorf("`on %s` needs a branch, for example `on %s main`", kind, kind)
		}
		event.Branch = argument
	}
	return event, nil
}

// Webhook returns the delivery that can satisfy this event.
func (e Event) Webhook() string { return eventKinds[e.Kind].webhook }

// Satisfies reports whether a delivery is the event being waited for. The check
// is deliberately narrow: waiting for "#12 merged" must not be satisfied by
// some other pull request closing.
func (e Event) Satisfies(delivery EventDelivery) bool {
	if delivery.Webhook != e.Webhook() {
		return false
	}
	switch e.Kind {
	case "merge":
		return delivery.Number == e.Number && delivery.Merged
	case "closed":
		return delivery.Number == e.Number && delivery.State == "closed"
	case "ci-green", "ci-red", "push":
		return delivery.Branch == e.Branch
	}
	return false
}

// EventDelivery is the part of a webhook an event wait looks at.
type EventDelivery struct {
	Webhook string
	Number  int
	State   string
	Merged  bool
	Branch  string
}

// Expired reports whether an event wait has run out. A wait that ends this way
// does not fail the job: it returns to waiting for a person, and says so
// (AGENT-R-048).
func (t Timing) Expired(now time.Time) bool {
	return t.Kind == TimingOn && !t.Deadline.IsZero() && now.After(t.Deadline)
}

// Ready reports whether a timing permits queueing now. The zero value is the
// default timing, not an unknown one: a job nobody gave a timing to is an
// ordinary scheduled job, and reading the zero value as "never ready" would
// leave every such job in the queue forever.
func (t Timing) Ready(now time.Time) bool {
	switch t.Kind {
	case "", TimingScheduled, TimingNow:
		return true
	case TimingAt:
		return !now.Before(t.At)
	}
	return false
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
