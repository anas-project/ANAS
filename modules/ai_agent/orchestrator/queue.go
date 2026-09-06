package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// JobState is where a queued job has got to.
type JobState string

const (
	JobWaiting  JobState = "waiting"
	JobQueued   JobState = "queued"
	JobRunning  JobState = "running"
	JobBlocked  JobState = "blocked"
	JobDone     JobState = "done"
	JobFailed   JobState = "failed"
	JobCanceled JobState = "canceled"
)

// QueueEntry is one execution job waiting for or holding a lease.
type QueueEntry struct {
	Repo  Repo
	Issue int
	// Parent is the discussion issue this job was split from, so the queue can
	// be read back to the requirement it serves.
	Parent int
	Agent  string
	State  JobState
	// DependsOn are issue numbers that must finish first. They are written as
	// Forgejo issue dependencies as well, so the ordering is visible on each
	// issue rather than only in the queue overview.
	DependsOn  []int
	Due        time.Time
	Priority   int
	EnqueuedAt time.Time
	Estimate   Estimate
	Timing     Timing
	AtRisk     bool
	RiskReason string
}

// Order sorts the queue. The order is not invented here: it is exactly the
// three things the design names, applied in that sequence -- issue dependencies
// are a hard order, then earliest deadline first, then priority and the time
// something was enqueued. Nothing else may influence it (AGENT-R-047).
//
// The dependency pass is a topological sort, so a job never appears before one
// it waits on however urgent its own deadline is. Within what dependencies
// leave free, EDF decides; a job with no deadline sorts after every job that
// has one, because a deadline is a promise and no deadline is not.
func Order(entries []QueueEntry) []QueueEntry {
	ordered := append([]QueueEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool { return softLess(ordered[i], ordered[j]) })
	return topological(ordered)
}

func softLess(left, right QueueEntry) bool {
	// A deadline beats no deadline; two deadlines are compared directly.
	switch {
	case !left.Due.IsZero() && right.Due.IsZero():
		return true
	case left.Due.IsZero() && !right.Due.IsZero():
		return false
	case !left.Due.IsZero() && !right.Due.IsZero() && !left.Due.Equal(right.Due):
		return left.Due.Before(right.Due)
	}
	if left.Priority != right.Priority {
		// A larger number is more urgent, which reads the way people say it.
		return left.Priority > right.Priority
	}
	if !left.EnqueuedAt.Equal(right.EnqueuedAt) {
		return left.EnqueuedAt.Before(right.EnqueuedAt)
	}
	// The issue number is the last tiebreak, so the order is total and two runs
	// over the same queue produce the same answer.
	return left.Issue < right.Issue
}

// topological reorders so that every dependency precedes its dependent, while
// otherwise preserving the order it was given.
//
// A cycle cannot be ordered. It is not dropped: the entries are appended in the
// order they came, so a queue with a dependency loop is still shown and still
// runs, and the loop is visible instead of the jobs silently disappearing.
func topological(entries []QueueEntry) []QueueEntry {
	position := make(map[int]int, len(entries))
	for index, entry := range entries {
		position[entry.Issue] = index
	}
	emitted := make(map[int]bool, len(entries))
	visiting := make(map[int]bool, len(entries))
	out := make([]QueueEntry, 0, len(entries))

	var emit func(entry QueueEntry)
	emit = func(entry QueueEntry) {
		if emitted[entry.Issue] || visiting[entry.Issue] {
			return
		}
		visiting[entry.Issue] = true
		// Dependencies are visited in the order the soft sort already put them,
		// so two independent prerequisites keep their deadline order.
		blockers := append([]int(nil), entry.DependsOn...)
		sort.SliceStable(blockers, func(i, j int) bool {
			return position[blockers[i]] < position[blockers[j]]
		})
		for _, blocker := range blockers {
			if index, present := position[blocker]; present {
				emit(entries[index])
			}
		}
		visiting[entry.Issue] = false
		emitted[entry.Issue] = true
		out = append(out, entry)
	}
	for _, entry := range entries {
		emit(entry)
	}
	return out
}

// Concurrency is how many jobs a repository may run at once.
type Concurrency struct {
	// Parallel is the repository's own limit. One is the default: two agents
	// changing the same repository at once is rarely what someone meant, and
	// the cost of finding out is a conflicting pair of pull requests.
	Parallel int
	// HostLimit is the ceiling the deployment's resources impose. The
	// repository setting cannot exceed it: a repository asking for eight
	// parallel jobs on a host that can run two would just queue them inside the
	// compute provider, where nothing can explain the wait.
	HostLimit int
}

// Limit is the effective number of concurrent jobs.
func (c Concurrency) Limit() int {
	limit := c.Parallel
	if limit < 1 {
		limit = 1
	}
	if c.HostLimit > 0 && limit > c.HostLimit {
		return c.HostLimit
	}
	return limit
}

// ParseQueueMode reads the argument of `/queue`.
func ParseQueueMode(argument string) (Concurrency, error) {
	fields := strings.Fields(strings.TrimSpace(argument))
	if len(fields) == 0 {
		return Concurrency{}, fmt.Errorf("`/queue` needs serial or parallel <n>")
	}
	switch strings.ToLower(fields[0]) {
	case "serial":
		return Concurrency{Parallel: 1}, nil
	case "parallel":
		if len(fields) < 2 {
			return Concurrency{}, fmt.Errorf("`/queue parallel` needs a number")
		}
		limit, err := strconv.Atoi(fields[1])
		if err != nil || limit < 1 {
			return Concurrency{}, fmt.Errorf("%q is not a positive number of parallel jobs", fields[1])
		}
		return Concurrency{Parallel: limit}, nil
	}
	return Concurrency{}, fmt.Errorf("unknown queue mode %q; use serial or parallel <n>", fields[0])
}

// Runnable returns the jobs that may start now, in order, respecting
// dependencies, timings and the concurrency limit.
func Runnable(entries []QueueEntry, now time.Time, concurrency Concurrency) []QueueEntry {
	ordered := Order(entries)
	finished := map[int]bool{}
	running := 0
	for _, entry := range ordered {
		switch entry.State {
		case JobDone:
			finished[entry.Issue] = true
		case JobRunning:
			running++
		}
	}
	free := concurrency.Limit() - running
	var runnable []QueueEntry
	for _, entry := range ordered {
		if free <= 0 {
			break
		}
		if entry.State != JobQueued && entry.State != JobWaiting {
			continue
		}
		if !entry.Timing.Ready(now) {
			continue
		}
		if blockedBy(entry, finished, ordered) {
			continue
		}
		runnable = append(runnable, entry)
		free--
	}
	return runnable
}

// blockedBy reports whether a job still waits on something. A dependency on an
// issue the queue does not know about is treated as satisfied: it is an issue
// outside this queue, and refusing to run because of one would make an
// unrelated closed issue block work forever.
func blockedBy(entry QueueEntry, finished map[int]bool, all []QueueEntry) bool {
	known := map[int]JobState{}
	for _, candidate := range all {
		known[candidate.Issue] = candidate.State
	}
	for _, blocker := range entry.DependsOn {
		state, present := known[blocker]
		if !present {
			continue
		}
		if !finished[blocker] && state != JobCanceled {
			return true
		}
	}
	return false
}

// queueIssueTitle is how the pinned overview is recognised again after a
// restart, since its number is not remembered anywhere else.
const queueIssueTitle = "[agent] Queue"

// QueueBoard maintains the human-visible ordering: one pinned issue per
// repository whose body is the ordered table, updated in place.
//
// Forgejo has no writable board API, but it does have sortable pinned issues,
// so the queue is an issue. Pinning is limited and can be refused; when it is,
// the issue is still maintained and still correct, and the status says it is
// not pinned rather than pretending it is (AGENT-R-051).
type QueueBoard struct {
	Repo       Repo
	Issues     ForgejoIssues
	Outbox     *Outbox
	ForgejoURL string
	Labels     LabelIndex
	Now        func() time.Time
}

func (b *QueueBoard) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

// RenderQueue builds the overview body. It is deterministic, so an unchanged
// queue produces an identical body and no pointless edit.
func RenderQueue(repo Repo, entries []QueueEntry, concurrency Concurrency, pinned bool, forgejoURL string) string {
	ordered := Order(entries)
	var out strings.Builder
	out.WriteString(statusMarker + "\n")
	out.WriteString("**Agent queue for `" + repo.String() + "`**\n\n")
	out.WriteString(fmt.Sprintf("Running at most %d job(s) at a time.\n\n", concurrency.Limit()))
	if !pinned {
		out.WriteString("> This issue could not be pinned -- the repository has no pin left. " +
			"The order below is still authoritative; each execution issue also shows what blocks it.\n\n")
	}
	if len(ordered) == 0 {
		out.WriteString("Nothing is queued.\n")
		return out.String()
	}
	out.WriteString("| # | Job | State | Waits for | Due | Estimate | Agent |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for index, entry := range ordered {
		blockers := "—"
		if len(entry.DependsOn) > 0 {
			parts := make([]string, 0, len(entry.DependsOn))
			for _, blocker := range entry.DependsOn {
				parts = append(parts, "#"+strconv.Itoa(blocker))
			}
			blockers = strings.Join(parts, ", ")
		}
		due := "—"
		if !entry.Due.IsZero() {
			due = entry.Due.UTC().Format("2006-01-02")
			if entry.AtRisk {
				due += " ⚠"
			}
		}
		estimate := "—"
		if entry.Estimate.Valid() {
			estimate = entry.Estimate.WallClock.Round(time.Minute).String()
		}
		out.WriteString(fmt.Sprintf("| %d | #%d | %s | %s | %s | %s | %s |\n",
			index+1, entry.Issue, entry.State, blockers, due, estimate, entry.Agent))
	}
	out.WriteString("\nOrder follows issue dependencies first, then the earliest due date, " +
		"then priority and when the job was queued. Nothing else affects it.\n")
	for _, entry := range ordered {
		if entry.AtRisk && entry.RiskReason != "" {
			out.WriteString("\n- #" + strconv.Itoa(entry.Issue) + " is at risk: " + entry.RiskReason + "\n")
		}
	}
	return out.String()
}

// Sync creates or updates the queue issue and keeps it pinned when it can be.
func (b *QueueBoard) Sync(ctx context.Context, entries []QueueEntry, concurrency Concurrency, existing int) (int, bool, error) {
	if existing == 0 {
		// Look for a board this repository already has before making another.
		// Without this, a board that lost its pin -- the very case the
		// degradation exists for -- would be invisible to recovery and a second
		// one would appear beside it on the next sweep.
		found, err := FindQueueIssue(ctx, b.Issues, b.Repo)
		if err != nil {
			return 0, false, err
		}
		existing = found
	}
	if existing == 0 {
		// A new board is created and then pinned, and its body is written once
		// with the answer already known -- creating it and immediately editing
		// it to correct the pinned line would put a pointless revision in the
		// issue's history on every fresh repository.
		created, err := b.Issues.CreateIssue(ctx, b.Repo, queueIssueTitle, "", b.queueLabel())
		if err != nil {
			return 0, false, err
		}
		pinned, err := b.pin(ctx, created.Number)
		if err != nil {
			return created.Number, false, err
		}
		body := RenderQueue(b.Repo, entries, concurrency, pinned, b.ForgejoURL)
		if _, err := b.Issues.EditIssueBody(ctx, b.Repo, created.Number, body); err != nil {
			return created.Number, pinned, err
		}
		return created.Number, pinned, nil
	}

	number := existing
	pinned, err := b.pin(ctx, number)
	if err != nil {
		return number, false, err
	}
	body := RenderQueue(b.Repo, entries, concurrency, pinned, b.ForgejoURL)

	current, err := b.Issues.Issue(ctx, b.Repo, number)
	if err != nil {
		return number, pinned, err
	}
	if current.Body == body {
		return number, pinned, nil
	}
	if _, err := b.Issues.EditIssueBody(ctx, b.Repo, number, body); err != nil {
		return number, pinned, err
	}
	return number, pinned, nil
}

func (b *QueueBoard) queueLabel() []int64 {
	if id, ok := b.Labels[LabelQueue]; ok {
		return []int64{id}
	}
	return nil
}

// pin asks Forgejo to pin the queue issue and reports whether it is pinned. A
// refusal is not an error: pins are a limited resource the repository's own
// people also use, and losing the pin must not stop the queue from working.
func (b *QueueBoard) pin(ctx context.Context, number int) (bool, error) {
	allowed, err := b.Issues.NewPinAllowed(ctx, b.Repo)
	if err != nil {
		return false, err
	}
	if err := b.Issues.PinIssue(ctx, b.Repo, number); err != nil {
		if isStatus(err, 400) || isStatus(err, 403) || isStatus(err, 422) {
			return false, nil
		}
		return false, err
	}
	// An already-pinned issue reports allowed=false while still being pinned,
	// so the pin call's own success is what settles it.
	_ = allowed
	return true, nil
}

// FindQueueIssue recovers the queue issue, whose number is not held anywhere
// else. It looks at the pinned issues first because that is the cheap answer,
// and falls back to the `ai:queue` label -- a board that could not be pinned is
// still a board, and recovery that only looked at pins would miss exactly the
// case the degradation was built for.
//
// When several boards exist the oldest wins, so a repository that somehow ended
// up with two converges on the one people have been reading rather than
// alternating between them.
func FindQueueIssue(ctx context.Context, issues ForgejoIssues, repo Repo) (int, error) {
	pinned, err := issues.PinnedIssues(ctx, repo)
	if err != nil {
		return 0, err
	}
	found := 0
	for _, issue := range pinned {
		if issue.Title == queueIssueTitle && (found == 0 || issue.Number < found) {
			found = issue.Number
		}
	}
	if found != 0 {
		return found, nil
	}
	labelled, err := issues.IssuesByLabel(ctx, repo, LabelQueue)
	if err != nil {
		return 0, err
	}
	for _, issue := range labelled {
		if issue.Title == queueIssueTitle && (found == 0 || issue.Number < found) {
			found = issue.Number
		}
	}
	return found, nil
}
