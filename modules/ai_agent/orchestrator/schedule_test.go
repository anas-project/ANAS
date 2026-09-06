package main

import (
	"strings"
	"testing"
	"time"
)

var scheduleNow = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func goodEstimate(d time.Duration) Estimate {
	return Estimate{WallClock: d, CostUSD: 0.5, Effort: "a couple of files",
		By: "codex", At: scheduleNow}
}

// AGENT-R-045: an estimate comes from the agent. One without a producer or a
// time is not an estimate, and must not be scheduled on.
func TestEstimateNeedsAProducerAndADuration(t *testing.T) {
	if !goodEstimate(time.Hour).Valid() {
		t.Fatal("a complete estimate was rejected")
	}
	for name, estimate := range map[string]Estimate{
		"no duration": {By: "codex", At: scheduleNow},
		"no producer": {WallClock: time.Hour, At: scheduleNow},
		"no time":     {WallClock: time.Hour, By: "codex"},
		"empty":       {},
	} {
		if estimate.Valid() {
			t.Errorf("%s was accepted as an estimate", name)
		}
	}
}

// AGENT-R-046: a job that will not finish in time is marked and the shortfall
// is stated, rather than being run quietly into the deadline.
func TestDueCheckReportsTheShortfall(t *testing.T) {
	due := scheduleNow.Add(2 * time.Hour)

	fits := CheckDue(goodEstimate(time.Hour), due, scheduleNow)
	if fits.AtRisk || fits.Reason == "" {
		t.Fatalf("a job that fits was marked at risk: %+v", fits)
	}

	late := CheckDue(goodEstimate(5*time.Hour), due, scheduleNow)
	if !late.AtRisk {
		t.Fatal("a job that misses the deadline was not marked")
	}
	if late.Shortfall != 3*time.Hour {
		t.Fatalf("shortfall = %s, want 3h", late.Shortfall)
	}
	if !strings.Contains(late.Reason, "3h") {
		t.Fatalf("reason = %q, want it to say by how much", late.Reason)
	}
}

// No estimate means nothing can be promised, so the job is at risk rather than
// assumed to fit.
func TestMissingEstimateIsAtRisk(t *testing.T) {
	check := CheckDue(Estimate{}, scheduleNow.Add(time.Hour), scheduleNow)
	if !check.AtRisk || !strings.Contains(check.Reason, "no estimate") {
		t.Fatalf("check = %+v, want it at risk and explained", check)
	}
}

// An issue with no due date has no deadline to miss.
func TestNoDueDateIsNotARisk(t *testing.T) {
	if check := CheckDue(goodEstimate(100*time.Hour), time.Time{}, scheduleNow); check.AtRisk {
		t.Fatalf("check = %+v, want no risk without a due date", check)
	}
}

// AGENT-R-058: an estimate buys headroom but cannot raise the deployment's own
// ceiling.
func TestWallClockCeilingIsTheSmallerOfTheTwo(t *testing.T) {
	deployment := 4 * time.Hour
	if got := WallClockCeiling(goodEstimate(time.Hour), deployment); got != 2*time.Hour {
		t.Fatalf("ceiling = %s, want twice a one-hour estimate", got)
	}
	if got := WallClockCeiling(goodEstimate(6*time.Hour), deployment); got != deployment {
		t.Fatalf("ceiling = %s; an estimate must not raise the deployment ceiling", got)
	}
	if got := WallClockCeiling(Estimate{}, deployment); got != deployment {
		t.Fatalf("ceiling without an estimate = %s, want the deployment ceiling", got)
	}
}

// AGENT-R-048: the four timings parse, and each writes back a label so what a
// command did stays visible.
func TestTimingsParseAndWriteBackTheirLabel(t *testing.T) {
	for _, testCase := range []struct {
		argument string
		kind     TimingKind
		label    string
	}{
		{"now", TimingNow, LabelExecuteWhen + "now"},
		{"hold", TimingHold, LabelExecuteWhen + "hold"},
		{"", TimingScheduled, ""},
		{"at 2026-09-07T02:00:00Z", TimingAt, LabelExecuteWhen + "at=2026-09-07T02:00:00Z"},
		{"on merge #12", TimingOn, LabelExecuteWhen + "on=merge #12"},
		{"on ci-green main", TimingOn, LabelExecuteWhen + "on=ci-green main"},
	} {
		timing, err := ParseTiming(testCase.argument, scheduleNow)
		if err != nil {
			t.Fatalf("ParseTiming(%q): %v", testCase.argument, err)
		}
		if timing.Kind != testCase.kind {
			t.Errorf("ParseTiming(%q) kind = %q, want %q", testCase.argument, timing.Kind, testCase.kind)
		}
		if got := timing.Label(); got != testCase.label {
			t.Errorf("ParseTiming(%q) label = %q, want %q", testCase.argument, got, testCase.label)
		}
	}
}

// A timing that was only half understood would run a job at a moment nobody
// asked for, so anything ambiguous is refused with the alternatives named.
func TestAmbiguousTimingsAreRefused(t *testing.T) {
	for _, argument := range []string{
		"soon", "at", "at tomorrow", "at 2026-09-05T02:00:00Z",
		"on", "on something-nobody-subscribes-to", "on merge", "on merge notanumber", "on ci-green",
	} {
		if _, err := ParseTiming(argument, scheduleNow); err == nil {
			t.Errorf("ParseTiming(%q) was accepted", argument)
		}
	}
	// The refusal names what can be waited for, instead of only saying no.
	_, err := ParseTiming("on nonsense", scheduleNow)
	if err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("error = %v, want the supported events listed", err)
	}
}

// AGENT-R-048: an event wait always has a deadline, and reaching it returns the
// job to waiting for a person rather than failing it.
func TestEventWaitAlwaysExpires(t *testing.T) {
	timing, err := ParseTiming("on merge #12", scheduleNow)
	if err != nil {
		t.Fatalf("ParseTiming: %v", err)
	}
	if timing.Deadline.IsZero() {
		t.Fatal("an event wait was created with no deadline, so it could wait forever")
	}
	if timing.Expired(scheduleNow) {
		t.Fatal("a fresh wait is already expired")
	}
	if !timing.Expired(timing.Deadline.Add(time.Second)) {
		t.Fatal("the wait did not expire after its deadline")
	}
	// A timing that is not a wait never expires.
	immediate, _ := ParseTiming("now", scheduleNow)
	if immediate.Expired(scheduleNow.Add(10000 * time.Hour)) {
		t.Fatal("a non-waiting timing expired")
	}
}

// Only the events the control plane already receives can be waited for, and a
// wait is satisfied by the specific thing it named -- not by anything of the
// same kind.
func TestEventsAreSatisfiedOnlyByWhatTheyNamed(t *testing.T) {
	merge, err := ParseTiming("on merge #12", scheduleNow)
	if err != nil {
		t.Fatalf("ParseTiming: %v", err)
	}
	if !merge.Event.Satisfies(EventDelivery{Webhook: "pull_request", Number: 12, Merged: true}) {
		t.Fatal("the merge of the named pull request did not satisfy the wait")
	}
	for name, delivery := range map[string]EventDelivery{
		"another pull request": {Webhook: "pull_request", Number: 13, Merged: true},
		"closed without merge": {Webhook: "pull_request", Number: 12},
		"a different webhook":  {Webhook: "issues", Number: 12, Merged: true},
	} {
		if merge.Event.Satisfies(delivery) {
			t.Errorf("%s satisfied a wait for the merge of #12", name)
		}
	}

	green, _ := ParseTiming("on ci-green main", scheduleNow)
	if !green.Event.Satisfies(EventDelivery{Webhook: "action_run_success", Branch: "main"}) {
		t.Fatal("a green run on the named branch did not satisfy the wait")
	}
	if green.Event.Satisfies(EventDelivery{Webhook: "action_run_success", Branch: "other"}) {
		t.Fatal("a run on another branch satisfied the wait")
	}
	if green.Event.Satisfies(EventDelivery{Webhook: "action_run_failure", Branch: "main"}) {
		t.Fatal("a failed run satisfied a wait for a green one")
	}
}

// Every waitable event maps onto a webhook the ingress actually subscribes to;
// otherwise the wait could never end.
func TestEveryWaitableEventHasASubscribedWebhook(t *testing.T) {
	subscribed := map[string]bool{}
	for _, event := range subscribedEvents {
		subscribed[event] = true
	}
	// The run events are subscribed on the hook registration, which asks for a
	// wider set than the ingress dispatches on.
	for _, extra := range []string{"action_run_success", "action_run_failure", "push"} {
		subscribed[extra] = true
	}
	for _, kind := range SupportedEvents() {
		event := Event{Kind: kind}
		if event.Webhook() == "" {
			t.Errorf("%q maps to no webhook", kind)
			continue
		}
		if !subscribed[event.Webhook()] {
			t.Errorf("%q needs the %q webhook, which is not subscribed -- the wait could never end",
				kind, event.Webhook())
		}
	}
}

func TestReadyFollowsTheTiming(t *testing.T) {
	scheduled, _ := ParseTiming("", scheduleNow)
	if !scheduled.Ready(scheduleNow) {
		t.Fatal("the default timing is not ready")
	}
	future, _ := ParseTiming("at 2026-09-07T02:00:00Z", scheduleNow)
	if future.Ready(scheduleNow) {
		t.Fatal("a future timing is ready now")
	}
	if !future.Ready(future.At) {
		t.Fatal("a timing is not ready at its own time")
	}
	waiting, _ := ParseTiming("on merge #12", scheduleNow)
	if waiting.Ready(scheduleNow) {
		t.Fatal("a job waiting for an event is ready before it arrives")
	}
	held, _ := ParseTiming("hold", scheduleNow)
	if held.Ready(scheduleNow) {
		t.Fatal("a held job is ready")
	}
}
