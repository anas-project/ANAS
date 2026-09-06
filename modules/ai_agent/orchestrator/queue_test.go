package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func entry(issue int, options ...func(*QueueEntry)) QueueEntry {
	item := QueueEntry{
		Repo: Repo{Owner: "anas-project", Name: "ANAS"}, Issue: issue,
		Agent: "codex", State: JobQueued, EnqueuedAt: scheduleNow,
	}
	for _, option := range options {
		option(&item)
	}
	return item
}

func due(at time.Time) func(*QueueEntry) { return func(e *QueueEntry) { e.Due = at } }
func after(blockers ...int) func(*QueueEntry) {
	return func(e *QueueEntry) { e.DependsOn = blockers }
}
func priority(value int) func(*QueueEntry)    { return func(e *QueueEntry) { e.Priority = value } }
func enqueued(at time.Time) func(*QueueEntry) { return func(e *QueueEntry) { e.EnqueuedAt = at } }
func state(s JobState) func(*QueueEntry)      { return func(e *QueueEntry) { e.State = s } }

func numbers(entries []QueueEntry) []int {
	out := make([]int, 0, len(entries))
	for _, item := range entries {
		out = append(out, item.Issue)
	}
	return out
}

func sameOrder(got []int, want ...int) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// AGENT-R-047: the order comes from three things and nothing else -- issue
// dependencies, then the earliest due date, then priority and enqueue time.
func TestQueueOrderFollowsDeadlineThenPriorityThenArrival(t *testing.T) {
	soon, later := scheduleNow.Add(time.Hour), scheduleNow.Add(48*time.Hour)
	ordered := Order([]QueueEntry{
		entry(3, due(later)),
		entry(1, due(soon)),
		entry(2, due(soon), priority(5)),
	})
	// Two jobs share a deadline, so priority separates them; the third is later.
	if got := numbers(ordered); !sameOrder(got, 2, 1, 3) {
		t.Fatalf("order = %v, want [2 1 3]", got)
	}

	// With the same deadline and priority, whoever was queued first goes first.
	ordered = Order([]QueueEntry{
		entry(2, due(soon), enqueued(scheduleNow.Add(time.Minute))),
		entry(1, due(soon), enqueued(scheduleNow)),
	})
	if got := numbers(ordered); !sameOrder(got, 1, 2) {
		t.Fatalf("order = %v, want the earlier arrival first", got)
	}
}

// A deadline is a promise; not having one is not. A job without a due date
// sorts after every job that has one.
func TestJobsWithoutADeadlineSortLast(t *testing.T) {
	ordered := Order([]QueueEntry{
		entry(1),
		entry(2, due(scheduleNow.Add(100*time.Hour))),
	})
	if got := numbers(ordered); !sameOrder(got, 2, 1) {
		t.Fatalf("order = %v, want the job with a deadline first", got)
	}
}

// A dependency is a hard order: it outranks the deadline, however urgent the
// dependent job is.
func TestDependenciesOutrankDeadlines(t *testing.T) {
	urgent, relaxed := scheduleNow.Add(time.Hour), scheduleNow.Add(72*time.Hour)
	ordered := Order([]QueueEntry{
		entry(2, due(urgent), after(1)),
		entry(1, due(relaxed)),
	})
	if got := numbers(ordered); !sameOrder(got, 1, 2) {
		t.Fatalf("order = %v; the blocker must come first even though #2 is more urgent", got)
	}
}

func TestTransitiveDependenciesAreOrdered(t *testing.T) {
	ordered := Order([]QueueEntry{
		entry(3, after(2)),
		entry(2, after(1)),
		entry(1),
	})
	if got := numbers(ordered); !sameOrder(got, 1, 2, 3) {
		t.Fatalf("order = %v, want the chain in order", got)
	}
}

// A dependency loop cannot be ordered, but the jobs must not vanish: the loop
// has to be visible rather than silently emptying the queue.
func TestDependencyCycleKeepsEveryJobVisible(t *testing.T) {
	ordered := Order([]QueueEntry{
		entry(1, after(2)),
		entry(2, after(1)),
		entry(3),
	})
	if len(ordered) != 3 {
		t.Fatalf("a dependency cycle dropped jobs: %v", numbers(ordered))
	}
}

// The order is total and stable: two runs over the same queue agree.
func TestQueueOrderIsDeterministic(t *testing.T) {
	entries := []QueueEntry{entry(3), entry(1), entry(2)}
	first := numbers(Order(entries))
	for attempt := 0; attempt < 5; attempt++ {
		if got := numbers(Order(entries)); !sameOrder(got, first...) {
			t.Fatalf("order changed between runs: %v then %v", first, got)
		}
	}
}

// AGENT-R-049: serial is the default, parallel is bounded, and the host ceiling
// wins over what a repository asks for.
func TestConcurrencyIsBoundedByTheHost(t *testing.T) {
	if got := (Concurrency{}).Limit(); got != 1 {
		t.Fatalf("the default limit is %d, want serial", got)
	}
	if got := (Concurrency{Parallel: 4}).Limit(); got != 4 {
		t.Fatalf("limit = %d, want 4", got)
	}
	if got := (Concurrency{Parallel: 8, HostLimit: 2}).Limit(); got != 2 {
		t.Fatalf("limit = %d; the host ceiling must win", got)
	}
}

func TestQueueModeParsing(t *testing.T) {
	if mode, err := ParseQueueMode("serial"); err != nil || mode.Limit() != 1 {
		t.Fatalf("serial = %+v, %v", mode, err)
	}
	if mode, err := ParseQueueMode("parallel 3"); err != nil || mode.Limit() != 3 {
		t.Fatalf("parallel 3 = %+v, %v", mode, err)
	}
	for _, argument := range []string{"", "parallel", "parallel 0", "parallel -1", "parallel two", "sideways"} {
		if _, err := ParseQueueMode(argument); err == nil {
			t.Errorf("ParseQueueMode(%q) was accepted", argument)
		}
	}
}

// Runnable respects dependencies, timings and the concurrency limit together.
func TestRunnableRespectsEverythingAtOnce(t *testing.T) {
	waiting, _ := ParseTiming("on merge #99", scheduleNow)
	future, _ := ParseTiming("at 2026-09-07T02:00:00Z", scheduleNow)

	entries := []QueueEntry{
		entry(1, state(JobDone)),
		entry(2, after(1)),
		entry(3, after(4)),
		entry(4),
		entry(5, func(e *QueueEntry) { e.Timing = waiting }),
		entry(6, func(e *QueueEntry) { e.Timing = future }),
	}
	runnable := numbers(Runnable(entries, scheduleNow, Concurrency{Parallel: 10}))
	// #2's blocker is done, #4 has none. #3 waits on #4, #5 on an event and #6
	// on a time.
	if !sameOrder(runnable, 2, 4) {
		t.Fatalf("runnable = %v, want [2 4]", runnable)
	}

	// The concurrency limit counts what is already running.
	entries = append(entries, entry(7, state(JobRunning)))
	if got := numbers(Runnable(entries, scheduleNow, Concurrency{Parallel: 1})); len(got) != 0 {
		t.Fatalf("runnable = %v; the only lease is taken", got)
	}
	if got := numbers(Runnable(entries, scheduleNow, Concurrency{Parallel: 2})); len(got) != 1 {
		t.Fatalf("runnable = %v, want exactly one more", got)
	}

	// Once the time arrives the scheduled job becomes runnable.
	if got := numbers(Runnable(entries, future.At, Concurrency{Parallel: 10})); !containsInt(got, 6) {
		t.Fatalf("runnable = %v, want #6 once its time has come", got)
	}
}

// A dependency on an issue outside this queue is not a permanent block: it is
// somebody else's issue, and treating it as unfinished would stop the work
// forever.
func TestDependencyOutsideTheQueueDoesNotBlock(t *testing.T) {
	runnable := numbers(Runnable([]QueueEntry{entry(2, after(999))}, scheduleNow, Concurrency{Parallel: 1}))
	if !sameOrder(runnable, 2) {
		t.Fatalf("runnable = %v; a dependency the queue does not know about blocked the job", runnable)
	}
}

// A cancelled blocker unblocks: waiting for work that will never happen is a
// deadlock, not an ordering.
func TestCancelledBlockerUnblocks(t *testing.T) {
	entries := []QueueEntry{entry(1, state(JobCanceled)), entry(2, after(1))}
	if got := numbers(Runnable(entries, scheduleNow, Concurrency{Parallel: 2})); !containsInt(got, 2) {
		t.Fatalf("runnable = %v; a cancelled blocker still blocks", got)
	}
}

func testBoard(t *testing.T) (*QueueBoard, *fakeIssues) {
	t.Helper()
	issues, store := newFakeIssues(), NewMemoryStore()
	return &QueueBoard{
		Repo: testRepo(t), Issues: issues, Outbox: &Outbox{Store: store},
		ForgejoURL: "https://git.example", Labels: LabelIndex{},
	}, issues
}

// AGENT-R-051: the order is visible to people as one pinned issue, updated in
// place.
func TestQueueBoardIsOneIssueUpdatedInPlace(t *testing.T) {
	board, issues := testBoard(t)
	ctx := context.Background()
	entries := []QueueEntry{entry(2, due(scheduleNow.Add(time.Hour))), entry(3)}

	number, pinned, err := board.Sync(ctx, entries, Concurrency{Parallel: 1}, 0)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if number == 0 || !pinned {
		t.Fatalf("Sync = %d, pinned %v", number, pinned)
	}
	created := issues.countCalls("CreateIssue")
	// Creating the board writes its body once, with the pin already decided.
	writes := issues.countCalls("EditIssueBody")
	if writes != 1 {
		t.Fatalf("creating the board wrote its body %d times, want 1", writes)
	}

	entries = append(entries, entry(4))
	if _, _, err := board.Sync(ctx, entries, Concurrency{Parallel: 1}, number); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if issues.countCalls("CreateIssue") != created {
		t.Fatal("the queue issue was recreated instead of edited")
	}
	if issues.countCalls("EditIssueBody") != writes+1 {
		t.Fatalf("EditIssueBody calls = %d, want one more", issues.countCalls("EditIssueBody"))
	}

	// An unchanged queue writes nothing.
	if _, _, err := board.Sync(ctx, entries, Concurrency{Parallel: 1}, number); err != nil {
		t.Fatalf("third Sync: %v", err)
	}
	if issues.countCalls("EditIssueBody") != writes+1 {
		t.Fatal("an unchanged queue was rewritten")
	}
}

// AGENT-R-051: pins are a limited resource the repository's own people also
// use. Losing one degrades to an unpinned issue that says so, rather than
// failing.
func TestQueueBoardDegradesWhenItCannotPin(t *testing.T) {
	board, issues := testBoard(t)
	ctx := context.Background()
	issues.pinLimit = 1
	issues.pinned = []int{99}

	number, pinned, err := board.Sync(ctx, []QueueEntry{entry(2)}, Concurrency{Parallel: 1}, 0)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if pinned {
		t.Fatal("the board reported itself pinned although no pin was available")
	}
	current, err := issues.Issue(ctx, testRepo(t), number)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !strings.Contains(current.Body, "could not be pinned") {
		t.Fatalf("the body does not explain the degradation: %s", current.Body)
	}
	if !strings.Contains(current.Body, "#2") {
		t.Fatal("the unpinned queue lost its content")
	}
}

// The rendered order matches the computed order, and an at-risk job says why.
func TestRenderedQueueShowsOrderAndRisk(t *testing.T) {
	entries := []QueueEntry{
		entry(3, due(scheduleNow.Add(72*time.Hour))),
		entry(2, due(scheduleNow.Add(time.Hour)), func(e *QueueEntry) {
			e.AtRisk, e.RiskReason = true, "expected to finish 3h after the due date"
		}),
	}
	body := RenderQueue(testRepo(t), entries, Concurrency{Parallel: 2}, true, "https://git.example")
	if strings.Index(body, "#2") > strings.Index(body, "#3") {
		t.Fatalf("the table is not in queue order:\n%s", body)
	}
	if !strings.Contains(body, "at risk") || !strings.Contains(body, "3h after the due date") {
		t.Fatalf("the at-risk job is not explained:\n%s", body)
	}
	if !strings.Contains(body, "at most 2 job") {
		t.Fatalf("the concurrency is not shown:\n%s", body)
	}
}

func TestEmptyQueueRendersSomethingUseful(t *testing.T) {
	body := RenderQueue(testRepo(t), nil, Concurrency{}, true, "https://git.example")
	if !strings.Contains(body, "Nothing is queued") {
		t.Fatalf("an empty queue rendered %q", body)
	}
}

// After a restart the queue issue is found again by its title, since its number
// is not held anywhere else.
func TestQueueIssueIsFoundAgainAfterARestart(t *testing.T) {
	board, issues := testBoard(t)
	ctx := context.Background()
	number, _, err := board.Sync(ctx, []QueueEntry{entry(2)}, Concurrency{}, 0)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	issues.mu.Lock()
	issues.issues[number].Title = queueIssueTitle
	issues.mu.Unlock()

	found, err := FindQueueIssue(ctx, issues, testRepo(t))
	if err != nil {
		t.Fatalf("FindQueueIssue: %v", err)
	}
	if found != number {
		t.Fatalf("FindQueueIssue = %d, want %d", found, number)
	}
}

// AGENT-R-050: the time a job actually took goes into Forgejo's own time
// tracking, so an agent's effort shows up in the same reports as everyone
// else's rather than only inside this module.
func TestActualDurationIsWrittenBackAsTrackedTime(t *testing.T) {
	issues := newFakeIssues()
	ctx := context.Background()
	issues.seedIssue(7, "alice", "")

	if err := issues.TrackTime(ctx, testRepo(t), 7, 42*time.Minute, "agent-codex"); err != nil {
		t.Fatalf("TrackTime: %v", err)
	}
	if issues.trackedTime[7] != 42*time.Minute {
		t.Fatalf("tracked = %s, want 42m", issues.trackedTime[7])
	}
	// It is attributed to the agent that did the work.
	if issues.countCalls("TrackTime:agent-codex") != 1 {
		t.Fatal("the time was not attributed to the agent")
	}
	// A second job on the same issue adds to the total rather than replacing it.
	if err := issues.TrackTime(ctx, testRepo(t), 7, 8*time.Minute, "agent-codex"); err != nil {
		t.Fatalf("TrackTime: %v", err)
	}
	if issues.trackedTime[7] != 50*time.Minute {
		t.Fatalf("tracked = %s, want the two jobs added up", issues.trackedTime[7])
	}
	// A job that took no measurable time writes nothing: upstream rejects a
	// non-positive duration, and there is nothing worth recording.
	before := issues.countCalls("TrackTime")
	if err := issues.TrackTime(ctx, testRepo(t), 7, 0, "agent-codex"); err != nil {
		t.Fatalf("TrackTime with no duration: %v", err)
	}
	if issues.countCalls("TrackTime") != before {
		t.Fatal("a zero duration produced a time entry")
	}
}

// The hard ordering is written as a Forgejo issue dependency as well as kept in
// the queue, so "waiting for #12" is visible on the issue itself.
func TestHardOrderIsAlsoAnIssueDependency(t *testing.T) {
	issues := newFakeIssues()
	ctx := context.Background()
	if err := issues.AddDependency(ctx, testRepo(t), 13, testRepo(t), 12); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if len(issues.dependencies[13]) != 1 || issues.dependencies[13][0] != 12 {
		t.Fatalf("dependencies = %v", issues.dependencies)
	}
}

// A dependency that is already recorded comes back from Forgejo as a 500, and
// every reconciliation sweep re-asserts the same ordering -- so that one
// message means success. Anything else that fails still fails.
func TestDuplicateDependencyIsNotAFailure(t *testing.T) {
	if !isAlreadyDependent(&statusError{status: 500,
		body: `{"message":"issue dependency does already exist [issue id: 2, dependency id: 1]"}`}) {
		t.Fatal("the duplicate-dependency refusal was not recognised")
	}
	for name, err := range map[string]error{
		"a real server fault": &statusError{status: 500, body: `{"message":"database is gone"}`},
		"a missing issue":     &statusError{status: 404, body: `{"message":"not found"}`},
		"not a status error":  context.Canceled,
	} {
		if isAlreadyDependent(err) {
			t.Errorf("%s was mistaken for an existing dependency", name)
		}
	}
}

// A board that could not be pinned is exactly the case the degradation exists
// for, and recovery must still find it -- otherwise the next sweep creates a
// second board beside the first and the repository ends up with two.
func TestUnpinnedBoardIsStillRecovered(t *testing.T) {
	board, issues := testBoard(t)
	ctx := context.Background()
	board.Labels = LabelIndex{LabelQueue: 42}
	issues.mu.Lock()
	issues.labels[LabelQueue] = RepoLabel{ID: 42, Name: LabelQueue}
	issues.pinLimit, issues.pinned = 1, []int{99}
	issues.mu.Unlock()

	number, pinned, err := board.Sync(ctx, []QueueEntry{entry(2)}, Concurrency{}, 0)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if pinned {
		t.Fatal("the board was pinned although no pin was available")
	}
	issues.mu.Lock()
	issues.issues[number].Title = queueIssueTitle
	issues.mu.Unlock()

	found, err := FindQueueIssue(ctx, issues, testRepo(t))
	if err != nil {
		t.Fatalf("FindQueueIssue: %v", err)
	}
	if found != number {
		t.Fatalf("FindQueueIssue = %d, want the unpinned board %d", found, number)
	}

	// A sweep that has lost the number must adopt it rather than make another.
	created := issues.countCalls("CreateIssue")
	again, _, err := board.Sync(ctx, []QueueEntry{entry(2), entry(3)}, Concurrency{}, 0)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if again != number {
		t.Fatalf("Sync created board %d beside the existing %d", again, number)
	}
	if issues.countCalls("CreateIssue") != created {
		t.Fatal("a second queue issue was created")
	}
}

// If a repository somehow ends up with two boards, recovery converges on the
// older one -- the one people have been reading -- instead of alternating.
func TestRecoveryPrefersTheOlderBoard(t *testing.T) {
	_, issues := testBoard(t)
	ctx := context.Background()
	for _, number := range []int{7, 3} {
		issue := issues.seedIssue(number, "agent-codex", "")
		issue.Title = queueIssueTitle
		issue.Labels = []RepoLabel{{ID: 42, Name: LabelQueue}}
		issues.pinned = append(issues.pinned, number)
	}
	found, err := FindQueueIssue(ctx, issues, testRepo(t))
	if err != nil {
		t.Fatalf("FindQueueIssue: %v", err)
	}
	if found != 3 {
		t.Fatalf("FindQueueIssue = %d, want the older board 3", found)
	}
}
