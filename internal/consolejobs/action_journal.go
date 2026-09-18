package consolejobs

import (
	"errors"
	"reflect"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

// actionEventRecord bounds retained action history by discarding the entire
// previous prefix when capacity/age is exceeded. A cumulative marker replaces
// that prefix, followed by the new event. Both and the job update occupy one
// JSONL record, so a torn final line cannot expose half of a terminal commit.
func (store *Store) actionEventRecord(state *storeState, job Job, event actionabi.Event, now time.Time) (journalRecord, error) {
	if job.Action == nil || store.options.EventCapacity < 2 {
		return journalRecord{}, invalidError("action journal is unavailable")
	}
	events := state.events[job.ID]
	capacity := len(events) >= store.options.EventCapacity
	retention := len(events) > 0 && events[0].Timestamp.Before(now.Add(-store.options.EventRetention))
	count := uint64(1)
	if capacity || retention {
		count++
	}
	if state.lastEventID > ^uint64(0)-count || job.Action.LastSeq > ^uint64(0)-count {
		return journalRecord{}, invalidError("action event sequence exhausted")
	}
	record := journalRecord{Kind: recordActionEvent, RecordedAt: now}
	seq, id := job.Action.LastSeq, state.lastEventID
	appendFrame := func(frame actionabi.Event) {
		seq++
		id++
		frame.Seq = seq
		record.ActionEvents = append(record.ActionEvents, Event{
			ID: id, JobID: job.ID, Timestamp: now, Kind: "action." + frame.Type, Action: &frame,
		})
	}
	if capacity || retention {
		reason := "retention"
		if capacity && retention {
			reason = "capacity+retention"
		} else if capacity {
			reason = "capacity"
		}
		record.Prune = &eventPrune{JobID: job.ID, Through: state.latestEventByJob[job.ID], Reason: reason}
		appendFrame(actionabi.Event{
			ABI: actionabi.Version, JobID: job.ID, InvocationID: job.Action.InvocationID, Type: "truncated",
			Truncated: &actionabi.Truncated{FromSeq: 1, ThroughSeq: job.Action.LastSeq},
		})
	}
	appendFrame(event)
	next, err := actionJobAfterEvent(job, *record.ActionEvents[len(record.ActionEvents)-1].Action, now)
	if err != nil {
		return journalRecord{}, err
	}
	record.Job = &next
	return record, nil
}

func (state *storeState) applyActionEvent(record journalRecord) error {
	if record.Job == nil || record.Event != nil || record.Idempotency != nil || hasSnapshotFields(record) || len(record.ActionEvents) < 1 || len(record.ActionEvents) > 2 {
		return errors.New("action_event record has invalid fields")
	}
	previous, exists := state.jobs[record.Job.ID]
	if !exists || previous.Action == nil || previous.Status.terminal() {
		return errors.New("action event requires a nonterminal action job")
	}
	if err := validatePersistedJob(*record.Job); err != nil {
		return err
	}
	stream, err := actionabi.NewReplayStream(previous.ID, previous.Action.InvocationID, previous.Action.LastSeq)
	if err != nil {
		return err
	}
	id := state.lastEventID
	for _, event := range record.ActionEvents {
		if id == ^uint64(0) || event.ID != id+1 || event.JobID != previous.ID || !event.Timestamp.Equal(record.RecordedAt) || event.Action == nil {
			return errors.New("invalid action event identity or cursor")
		}
		if err := validatePersistedEvent(event); err != nil {
			return err
		}
		if err := stream.Accept(*event.Action); err != nil {
			return errors.New("invalid action journal sequence")
		}
		id = event.ID
	}
	if record.Prune == nil {
		if len(record.ActionEvents) != 1 || record.ActionEvents[0].Action.Type == "truncated" {
			return errors.New("action marker requires a prune record")
		}
	} else {
		prune := record.Prune
		marker := record.ActionEvents[0].Action
		if len(record.ActionEvents) != 2 || marker.Type != "truncated" || marker.Truncated.FromSeq != 1 || marker.Truncated.ThroughSeq != previous.Action.LastSeq ||
			prune.JobID != previous.ID || prune.Through != state.latestEventByJob[previous.ID] || prune.Through <= state.prunedThrough[previous.ID] ||
			(prune.Reason != "capacity" && prune.Reason != "retention" && prune.Reason != "capacity+retention") {
			return errors.New("invalid action truncation")
		}
	}
	last := *record.ActionEvents[len(record.ActionEvents)-1].Action
	expected, err := actionJobAfterEvent(previous, last, record.RecordedAt)
	if err != nil || !reflect.DeepEqual(expected, *record.Job) {
		return errors.New("action event and job update disagree")
	}
	// Validate everything before mutating recovered state.
	if record.Prune != nil {
		state.events[previous.ID] = nil
		state.prunedThrough[previous.ID] = record.Prune.Through
	}
	for _, event := range record.ActionEvents {
		state.events[previous.ID] = append(state.events[previous.ID], cloneEvent(event))
	}
	state.lastEventID = id
	state.latestEventByJob[previous.ID] = id
	state.jobs[previous.ID] = cloneJob(*record.Job)
	state.hasObsoleteHistory = true
	if state.obsoleteRevision != ^uint64(0) {
		state.obsoleteRevision++
	}
	return nil
}

// Check the action suffix in recovered snapshots as well as ordinary journals;
// a valid snapshot digest alone is not proof of valid action lifecycle history.
func (state *storeState) validateActionHistory() error {
	if err := state.validateActionRetryHistory(); err != nil {
		return err
	}
	for jobID, job := range state.jobs {
		events := state.events[jobID]
		if job.Action == nil {
			for _, event := range events {
				if event.Action != nil {
					return errors.New("legacy job contains an action event")
				}
			}
			continue
		}
		stream, err := actionabi.NewReplayStream(job.ID, job.Action.InvocationID, 0)
		if err != nil {
			return err
		}
		var lastSeq uint64
		var outcome actionabi.Outcome
		for index, event := range events {
			if event.Action == nil || (event.Action.Type == "truncated" && index != 0) {
				return errors.New("invalid retained action history")
			}
			if err := stream.Accept(*event.Action); err != nil {
				return errors.New("invalid retained action sequence")
			}
			lastSeq, outcome = event.Action.Seq, actionOutcome(*event.Action)
		}
		if lastSeq != job.Action.LastSeq || outcome != job.Action.Outcome {
			return errors.New("retained action history disagrees with job state")
		}
		if len(events) > 0 && events[len(events)-1].Action.Type == "truncated" {
			return errors.New("action truncation is missing its following event")
		}
		if len(events) == 0 {
			if state.latestEventByJob[jobID] != 0 || state.prunedThrough[jobID] != 0 {
				return errors.New("empty action history has event cursors")
			}
		} else if events[len(events)-1].ID != state.latestEventByJob[jobID] ||
			(events[0].Action.Type == "truncated") != (state.prunedThrough[jobID] > 0) {
			return errors.New("action history disagrees with global cursors")
		}
		if outcome != "" {
			last := events[len(events)-1]
			base := cloneJob(job)
			base.Status, base.Result, base.Error = StatusRunning, nil, nil
			if job.StartedAt == nil {
				base.Status = StatusQueued
			}
			base.Revision = 1
			expected, err := actionJobAfterEvent(base, *last.Action, last.Timestamp)
			if err != nil || !reflect.DeepEqual(expected.Result, job.Result) || !reflect.DeepEqual(expected.Error, job.Error) || job.FinishedAt == nil || !job.FinishedAt.Equal(last.Timestamp) {
				return errors.New("action terminal payload disagrees with job state")
			}
		}
	}
	return nil
}
